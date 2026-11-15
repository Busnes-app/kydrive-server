import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Check, Folder, Search as SearchIcon } from 'lucide-react';
import { record, response } from './client';
import type { Workspace } from './client';
import { iconFor } from './FileIcon';

// Snippet highlights are [start, end) offsets in UTF-16 code units, which is what String.slice uses.
export type Snippet = { text: string; highlights: [number, number][]; cut_start: boolean; cut_end: boolean };
// shared is set on an item reached only through Shared with me; path then starts at the shared item.
export type Hit = { kind: 'file' | 'folder'; id: string; workspace: string; parent: string; name: string; path: string; match?: string[]; snippet?: Snippet; shared?: { from: string; role: string } };
type Found = { query: string; results: Hit[]; truncated: boolean; pending: number; unverified: number; capped: boolean };

function snippet(v: unknown): v is Snippet {
  return record(v) && typeof v.text === 'string' && typeof v.cut_start === 'boolean' && typeof v.cut_end === 'boolean' && Array.isArray(v.highlights)
    && v.highlights.every(h => Array.isArray(h) && h.length === 2 && h.every(n => Number.isInteger(n)));
}
function hit(v: unknown): v is Hit {
  return record(v) && (v.kind === 'file' || v.kind === 'folder') && ['id', 'workspace', 'parent', 'name', 'path'].every(k => typeof v[k] === 'string')
    && (v.match === undefined || (Array.isArray(v.match) && v.match.every(m => typeof m === 'string')))
    && (v.snippet === undefined || snippet(v.snippet))
    && (v.shared === undefined || (record(v.shared) && typeof v.shared.from === 'string' && typeof v.shared.role === 'string'));
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

// nameMatches finds query in name case-insensitively, as the server's name match does, without
// the content syntax (quotes, a * ending a word). The "iu" regular expression keeps offsets in
// name itself, which lower-casing first would not.
export function nameMatches(name: string, raw: string): [number, number][] {
  const query = raw.replace(/\*+(?=\s|$)/g, '').replace(/"/g, '').trim();
  if (!query) return [];
  return [...name.matchAll(new RegExp(query.replace(/[\\^$.*+?()[\]{}|]/g, '\\$&'), 'giu'))].map(m => [m.index, m.index + m[0].length]);
}

// The server's search types (drive.typeExtensions), in the order the chips show them.
export const TYPES = [['folders', 'Folders'], ['documents', 'Documents'], ['spreadsheets', 'Spreadsheets'], ['presentations', 'Presentations'],
  ['pdfs', 'PDFs'], ['images', 'Images'], ['videos', 'Videos'], ['audio', 'Audio'], ['archives', 'Archives']] as const;

// The chosen type lives in the address (?type=pdfs), so Back and Forward restore it. The query
// never does: it would reach browser history and server logs.
function urlType(): string {
  const t = new URLSearchParams(location.search).get('type') ?? '';
  return TYPES.some(([v]) => v === t) ? t : '';
}
function pushType(t: string) {
  const u = new URL(location.href);
  if (t) u.searchParams.set('type', t); else u.searchParams.delete('type');
  if (u.href !== location.href) history.pushState(null, '', u);
}

// idle: nothing to search yet. A query needs 2 characters, except none at all with a type.
function idle(q: string, type: string): boolean {
  const n = [...q.trim()].length;
  return n < 2 && !(n === 0 && type);
}

// Search finds files and folders by name, and files by the words in them, across every workspace
// the user can read and what is shared with them, optionally of one type (a type alone lists all of it). Results open below
// the box and close on Escape or a click elsewhere.
export function Search({ workspaces, onOpen }: { workspaces: Workspace[]; onOpen: (h: Hit) => void }) {
  const [q, setQ] = useState('');
  const [type, setType] = useState(urlType);
  const [found, setFound] = useState<Found | null>(null);
  const [error, setError] = useState('');
  const [open, setOpen] = useState(() => urlType() !== '');
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const back = () => { const t = urlType(); setType(t); if (t) setOpen(true); };
    window.addEventListener('popstate', back);
    return () => window.removeEventListener('popstate', back);
  }, []);
  useEffect(() => {
    const query = q.trim();
    if (idle(query, type)) { setFound(null); setError(''); return; }
    const abort = new AbortController();
    const timer = setTimeout(() => void (async () => {
      try {
        const v = await response(await fetch(`/api/drive/search?q=${encodeURIComponent(query)}${type ? `&type=${type}` : ''}`, { signal: abort.signal }));
        if (!record(v) || !Array.isArray(v.results) || !v.results.every(hit) || typeof v.truncated !== 'boolean') throw new Error('Invalid search response');
        const c = record(v.contents) ? v.contents : {};
        const count = (n: unknown) => typeof n === 'number' ? n : 0;
        setFound({ query, results: v.results, truncated: v.truncated, pending: count(c.pending), unverified: count(c.unverified), capped: c.expanded_truncated === true }); setError(''); setOpen(true);
      } catch (e) { if (!abort.signal.aborted) { setError(e instanceof Error ? e.message : 'Search failed'); setOpen(true); } }
    })(), 300);
    return () => { clearTimeout(timer); abort.abort(); };
  }, [q, type]);
  useEffect(() => {
    if (!open) return;
    const away = (e: Event) => { if (!box.current?.contains(e.target as Node)) setOpen(false); };
    document.addEventListener('pointerdown', away);
    return () => document.removeEventListener('pointerdown', away);
  }, [open]);
  const choose = (t: string) => { pushType(t); setType(t); };
  const names = new Map(workspaces.map(w => [w.id, w.name]));
  return <div className="drive-search" role="search" ref={box} onKeyDown={e => { if (e.key === 'Escape') setOpen(false); }}>
    <SearchIcon className="drive-search-icon" size={18} aria-hidden="true" />
    <input type="search" aria-label="Search files" value={q} maxLength={100} placeholder="Search names and words in files" onFocus={() => setOpen(true)} onChange={e => setQ(e.target.value)} />
    {open && <div className="drive-search-results">
      <div className="drive-search-types" role="group" aria-label="File type">
        {TYPES.map(([v, label]) => <button key={v} type="button" aria-pressed={type === v} onClick={() => choose(type === v ? '' : v)}>
          {type === v && <Check size={14} aria-hidden="true" />}{label}
        </button>)}
      </div>
      <div aria-live="polite">
      {idle(q, type) && <p className="drive-search-note">Enter at least 2 characters, or choose a type.</p>}
      {error ? <p role="alert">{error}</p> : found && (found.results.length === 0 ? <p>No matches</p> : <ul>{found.results.map(h => {
        const Icon = h.kind === 'folder' ? Folder : iconFor(h.name);
        return <li key={`${h.kind}-${h.id}`}><button type="button" onClick={() => { setQ(''); setOpen(false); choose(''); onOpen(h); }}>
          <Icon size={18} aria-hidden="true" /><span>{h.match?.includes('name') ? marks(h.name, nameMatches(h.name, found.query)) : h.name}</span>
          <small>{[h.shared?.from ?? names.get(h.workspace) ?? 'Workspace', h.path].filter(Boolean).join(' / ')}</small>
          {h.snippet && <Marked s={h.snippet} />}
        </button></li>;
      })}</ul>)}
      {found?.truncated && <p>Showing the first {found.results.length} matches; refine the search to see more.</p>}
      {found && found.pending > 0 && <p className="drive-search-note">Still indexing {found.pending} of your files, so their contents may not be found yet.</p>}
      {found && found.unverified > 0 && <p className="drive-search-note">{found.unverified} more files have these words but were not checked for the exact phrase.</p>}
      {found?.capped && <p className="drive-search-note">A word start matched many words; only the most common were searched.</p>}
      {found?.query && <p className="drive-search-note">File contents match whole words. Add * to 3 or more letters to match the start of a word (conf*), or put words in quotes for an exact phrase ("site report").</p>}
      </div>
    </div>}
  </div>;
}
