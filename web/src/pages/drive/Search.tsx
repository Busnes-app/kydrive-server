import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Folder, Search as SearchIcon } from 'lucide-react';
import { record, response } from './client';
import type { Workspace } from './client';
import { iconFor } from './FileIcon';

// Snippet highlights are [start, end) offsets in UTF-16 code units, which is what String.slice uses.
export type Snippet = { text: string; highlights: [number, number][]; cut_start: boolean; cut_end: boolean };
export type Hit = { kind: 'file' | 'folder'; id: string; workspace: string; parent: string; name: string; path: string; match?: string[]; snippet?: Snippet };
type Found = { query: string; results: Hit[]; truncated: boolean; pending: number };

function snippet(v: unknown): v is Snippet {
  return record(v) && typeof v.text === 'string' && typeof v.cut_start === 'boolean' && typeof v.cut_end === 'boolean' && Array.isArray(v.highlights)
    && v.highlights.every(h => Array.isArray(h) && h.length === 2 && h.every(n => Number.isInteger(n)));
}
function hit(v: unknown): v is Hit {
  return record(v) && (v.kind === 'file' || v.kind === 'folder') && ['id', 'workspace', 'parent', 'name', 'path'].every(k => typeof v[k] === 'string')
    && (v.match === undefined || (Array.isArray(v.match) && v.match.every(m => typeof m === 'string')))
    && (v.snippet === undefined || snippet(v.snippet));
}

// marks renders text with <mark> around its highlights; out-of-order or out-of-range
// highlights are skipped. Never markup from the server.
function marks(text: string, highlights: [number, number][]): ReactNode[] {
  const parts: ReactNode[] = [];
  let at = 0;
  for (const [start, end] of highlights) {
    if (start < at || end <= start || end > text.length) continue;
    if (start > at) parts.push(text.slice(at, start));
    parts.push(<mark key={start}>{text.slice(start, end)}</mark>);
    at = end;
  }
  parts.push(text.slice(at));
  return parts;
}

export function Marked({ s }: { s: Snippet }) {
  return <span className="drive-search-snippet">{s.cut_start && '…'}{marks(s.text, s.highlights)}{s.cut_end && '…'}</span>;
}

// nameMatches finds query in name case-insensitively, as the server's name match does. The
// "iu" regular expression keeps offsets in name itself, which lower-casing first would not.
export function nameMatches(name: string, query: string): [number, number][] {
  if (!query) return [];
  return [...name.matchAll(new RegExp(query.replace(/[\\^$.*+?()[\]{}|]/g, '\\$&'), 'giu'))].map(m => [m.index, m.index + m[0].length]);
}

// Search finds files and folders by name, and files by the words in them, across every workspace
// the user can read. Results open below the box and close on Escape or a click elsewhere.
export function Search({ workspaces, onOpen }: { workspaces: Workspace[]; onOpen: (h: Hit) => void }) {
  const [q, setQ] = useState('');
  const [found, setFound] = useState<Found | null>(null);
  const [error, setError] = useState('');
  const [hidden, setHidden] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const query = q.trim();
    if ([...query].length < 2) { setFound(null); setError(''); return; }
    const abort = new AbortController();
    const timer = setTimeout(() => void (async () => {
      try {
        const v = await response(await fetch(`/api/drive/search?q=${encodeURIComponent(query)}`, { signal: abort.signal }));
        if (!record(v) || !Array.isArray(v.results) || !v.results.every(hit) || typeof v.truncated !== 'boolean') throw new Error('Invalid search response');
        const pending = record(v.contents) && typeof v.contents.pending === 'number' ? v.contents.pending : 0;
        setFound({ query, results: v.results, truncated: v.truncated, pending }); setError(''); setHidden(false);
      } catch (e) { if (!abort.signal.aborted) setError(e instanceof Error ? e.message : 'Search failed'); }
    })(), 300);
    return () => { clearTimeout(timer); abort.abort(); };
  }, [q]);
  useEffect(() => {
    if (!found && !error) return;
    const away = (e: Event) => { if (!box.current?.contains(e.target as Node)) setHidden(true); };
    document.addEventListener('pointerdown', away);
    return () => document.removeEventListener('pointerdown', away);
  }, [found, error]);
  const names = new Map(workspaces.map(w => [w.id, w.name]));
  const shown = !hidden && (found || error);
  return <div className="drive-search" role="search" ref={box} onKeyDown={e => { if (e.key === 'Escape') setHidden(true); }}>
    <SearchIcon className="drive-search-icon" size={18} aria-hidden="true" />
    <input type="search" aria-label="Search files" value={q} maxLength={100} placeholder="Search names and words in files" onFocus={() => setHidden(false)} onChange={e => setQ(e.target.value)} />
    {shown && <div className="drive-search-results" aria-live="polite">
      {error ? <p role="alert">{error}</p> : found && (found.results.length === 0 ? <p>No matches</p> : <ul>{found.results.map(h => {
        const Icon = h.kind === 'folder' ? Folder : iconFor(h.name);
        return <li key={`${h.kind}-${h.id}`}><button type="button" onClick={() => { setQ(''); onOpen(h); }}>
          <Icon size={18} aria-hidden="true" /><span>{h.match?.includes('name') ? marks(h.name, nameMatches(h.name, found.query)) : h.name}</span>
          <small>{[names.get(h.workspace) ?? 'Workspace', h.path].filter(Boolean).join(' / ')}</small>
          {h.snippet && <Marked s={h.snippet} />}
        </button></li>;
      })}</ul>)}
      {found?.truncated && <p>Showing the first {found.results.length} matches; refine the search to see more.</p>}
      {found && found.pending > 0 && <p className="drive-search-note">Still indexing {found.pending} of your files, so their contents may not be found yet.</p>}
      {found && <p className="drive-search-note">File contents match whole words.</p>}
    </div>}
  </div>;
}
