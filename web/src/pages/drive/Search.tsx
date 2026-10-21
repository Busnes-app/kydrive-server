import { useEffect, useRef, useState } from 'react';
import { Folder, Search as SearchIcon } from 'lucide-react';
import { record, response } from './client';
import type { Workspace } from './client';
import { iconFor } from './FileIcon';

export type Hit = { kind: 'file' | 'folder'; id: string; workspace: string; parent: string; name: string; path: string };
function hit(v: unknown): v is Hit { return record(v) && (v.kind === 'file' || v.kind === 'folder') && ['id', 'workspace', 'parent', 'name', 'path'].every(k => typeof v[k] === 'string'); }

// Search finds files and folders by name across every workspace the user can read. Results open
// below the box and close on Escape or a click elsewhere.
export function Search({ workspaces, onOpen }: { workspaces: Workspace[]; onOpen: (h: Hit) => void }) {
  const [q, setQ] = useState('');
  const [found, setFound] = useState<{ results: Hit[]; truncated: boolean } | null>(null);
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
        setFound({ results: v.results, truncated: v.truncated }); setError(''); setHidden(false);
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
    <input type="search" aria-label="Search files" value={q} maxLength={100} placeholder="Search file and folder names" onFocus={() => setHidden(false)} onChange={e => setQ(e.target.value)} />
    {shown && <div className="drive-search-results" aria-live="polite">
      {error ? <p role="alert">{error}</p> : found && (found.results.length === 0 ? <p>No matches</p> : <ul>{found.results.map(h => {
        const Icon = h.kind === 'folder' ? Folder : iconFor(h.name);
        return <li key={`${h.kind}-${h.id}`}><button type="button" onClick={() => { setQ(''); onOpen(h); }}>
          <Icon size={18} aria-hidden="true" /><span>{h.name}</span>
          <small>{[names.get(h.workspace) ?? 'Workspace', h.path].filter(Boolean).join(' / ')}</small>
        </button></li>;
      })}</ul>)}
      {found?.truncated && <p>Showing the first {found.results.length} matches; refine the search to see more.</p>}
    </div>}
  </div>;
}
