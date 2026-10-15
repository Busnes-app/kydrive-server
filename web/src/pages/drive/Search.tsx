import { useEffect, useState } from 'react';
import { record, response } from './client';
import type { Workspace } from './client';

export type Hit = { kind: 'file' | 'folder'; id: string; workspace: string; parent: string; name: string; path: string };
function hit(v: unknown): v is Hit { return record(v) && (v.kind === 'file' || v.kind === 'folder') && ['id', 'workspace', 'parent', 'name', 'path'].every(k => typeof v[k] === 'string'); }

// Search finds files and folders by name across every workspace the user can read.
export function Search({ workspaces, onOpen }: { workspaces: Workspace[]; onOpen: (h: Hit) => void }) {
  const [q, setQ] = useState('');
  const [found, setFound] = useState<{ results: Hit[]; truncated: boolean } | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    const query = q.trim();
    if ([...query].length < 2) { setFound(null); setError(''); return; }
    const abort = new AbortController();
    const timer = setTimeout(() => void (async () => {
      try {
        const v = await response(await fetch(`/api/drive/search?q=${encodeURIComponent(query)}`, { signal: abort.signal }));
        if (!record(v) || !Array.isArray(v.results) || !v.results.every(hit) || typeof v.truncated !== 'boolean') throw new Error('Invalid search response');
        setFound({ results: v.results, truncated: v.truncated }); setError('');
      } catch (e) { if (!abort.signal.aborted) setError(e instanceof Error ? e.message : 'Search failed'); }
    })(), 300);
    return () => { clearTimeout(timer); abort.abort(); };
  }, [q]);
  const names = new Map(workspaces.map(w => [w.id, w.name]));
  return <div className="drive-search">
    <label>Search files<input type="search" value={q} maxLength={100} placeholder="Name contains…" onChange={e => setQ(e.target.value)} /></label>
    {error && <p role="alert">{error}</p>}
    {found && <div className="drive-search-results" aria-live="polite">
      {found.results.length === 0 ? <p>No matches</p> : <ul>{found.results.map(h => <li key={`${h.kind}-${h.id}`}>
        <button className="btn-link" onClick={() => { setQ(''); onOpen(h); }}>{h.name}{h.kind === 'folder' ? '/' : ''}</button>
        <span>{[names.get(h.workspace) ?? 'Workspace', h.path].filter(Boolean).join(' / ')}</span>
      </li>)}</ul>}
      {found.truncated && <p>Showing the first {found.results.length} matches; refine the search to see more.</p>}
    </div>}
  </div>;
}
