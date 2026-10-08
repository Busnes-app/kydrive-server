import { useEffect, useRef, useState } from 'react';
import { change, folder, list, type DriveFile, type Folder, type Workspace } from './client';

export type Item = { kind: 'file'; file: DriveFile } | { kind: 'folder'; folder: Folder };
export type Mode = 'rename' | 'move' | 'copy';

function descendants(folders: Folder[], root: string): Set<string> {
  const out = new Set([root]);
  for (let grew = true; grew;) { grew = false; for (const f of folders) if (!out.has(f.id) && out.has(f.parent)) { out.add(f.id); grew = true; } }
  return out;
}

export function MoveDialog({ mode, item, workspaceID, workspaces, onClose, onDone }: { mode: Mode; item: Item; workspaceID: string; workspaces: Workspace[]; onClose: () => void; onDone: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const original = item.kind === 'file' ? item.file : item.folder;
  const writable = workspaces.filter(w => w.role === 'editor' || w.role === 'manager');
  // Moving a file across workspaces needs manager rights on the source; copying does not.
  const crossWorkspace = item.kind === 'file' && (mode === 'copy' || (mode === 'move' && workspaces.find(w => w.id === workspaceID)?.role === 'manager'));
  const [name, setName] = useState(original.name);
  const [target, setTarget] = useState(mode === 'copy' && !writable.some(w => w.id === workspaceID) ? writable[0]?.id ?? workspaceID : workspaceID);
  const [parent, setParent] = useState(original.parent);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  // jsdom lacks showModal; opening by attribute keeps the form reachable in unit tests.
  useEffect(() => { const d = ref.current; if (d && !d.open) { if (typeof d.showModal === 'function') d.showModal(); else d.setAttribute('open', ''); } }, []);
  useEffect(() => { if (mode === 'rename') return; void list(`/api/drive/workspaces/${target}/folders`, folder).then(setFolders).catch(e => setError(e instanceof Error ? e.message : 'Folders unavailable')); }, [mode, target]);
  const hidden = item.kind === 'folder' ? descendants(folders, item.folder.id) : new Set<string>();
  const verb = { rename: 'Rename', move: 'Move', copy: 'Copy' }[mode];
  async function submit() {
    setBusy(true); setError('');
    try {
      if (item.kind === 'folder') await change(`/api/drive/folders/${item.folder.id}/location`, 'PUT', { parent, name });
      else if (mode === 'copy') await change(`/api/drive/files/${item.file.id}/copy`, 'POST', { workspace: target, parent, name });
      else await change(`/api/drive/files/${item.file.id}/location`, 'PUT', { revision: item.file.revision, workspace: target, parent, name });
      onDone();
    } catch (e) { setError(e instanceof Error ? e.message : `${verb} failed`); } finally { setBusy(false); }
  }
  return <dialog ref={ref} className="drive-document-dialog" aria-labelledby="move-title" onCancel={onClose}>
    <form onSubmit={e => { e.preventDefault(); void submit(); }}>
      <h2 id="move-title">{verb} {original.name}</h2>
      <label>Name<input autoFocus required value={name} onChange={e => setName(e.target.value)} /></label>
      {mode !== 'rename' && crossWorkspace && <label>Workspace<select value={target} onChange={e => { setTarget(e.target.value); setParent(''); }}>{writable.map(w => <option key={w.id} value={w.id}>{w.name}</option>)}</select></label>}
      {mode !== 'rename' && <label>Folder<select value={parent} onChange={e => setParent(e.target.value)}><option value="">Workspace root</option>{folders.filter(f => !hidden.has(f.id)).map(f => <option key={f.id} value={f.id}>{f.name}</option>)}</select></label>}
      {error && <p role="alert">{error}</p>}
      <div className="drive-dialog-actions"><button type="button" className="btn-secondary" onClick={onClose}>Cancel</button><button disabled={busy || !name.trim()}>{verb}</button></div>
    </form>
  </dialog>;
}
