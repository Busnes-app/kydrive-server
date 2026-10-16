import { useEffect, useState } from 'react';
import { bytes, editorURL, file, folder, hasEditor, list, opensInEditor, record, response, sharedItem } from './client';
import { QuickLook } from './QuickLook';
import { FileIcon } from './FileIcon';
import type { DriveFile, Folder, SharedItem } from './client';

// SharedView lists what others shared with the user outside their own workspaces and browses
// into shared folders. The path starts at the shared item: nothing above it is visible.
export function SharedView({ openInNewTab }: { openInNewTab: boolean }) {
  const target = openInNewTab ? { target: '_blank', rel: 'noreferrer' } : {};
  const [items, setItems] = useState<SharedItem[]>([]);
  const [path, setPath] = useState<{ id: string; name: string }[]>([]);
  const [contents, setContents] = useState<{ folders: Folder[]; files: DriveFile[] }>({ folders: [], files: [] });
  const [error, setError] = useState('');
  const [preview, setPreview] = useState<{ id: string; name: string } | null>(null);
  const at = path[path.length - 1];

  useEffect(() => { void list('/api/drive/shared-with-me', sharedItem).then(setItems, e => setError(e instanceof Error ? e.message : 'Shared items unavailable')); }, []);
  useEffect(() => {
    if (!at) return;
    let active = true;
    void (async () => {
      const v = await response(await fetch(`/api/drive/folders/${at.id}/children`));
      if (!record(v) || !Array.isArray(v.folders) || !v.folders.every(folder) || !Array.isArray(v.files) || !v.files.every(file)) throw new Error('Invalid folder response');
      if (active) setContents({ folders: v.folders, files: v.files });
    })().catch(e => { if (active) setError(e instanceof Error ? e.message : 'Folder unavailable'); });
    return () => { active = false; };
  }, [at]);

  const fileRow = (f: { id: string; name: string; revision?: number }, detail: string) => <tr key={f.id}>
    <td><span className="drive-name-cell"><FileIcon file={f} />{opensInEditor(f.name) ? <a className="drive-file-name" href={editorURL(f)} {...target}>{f.name}</a> : <button className="drive-file-name btn-link" onClick={() => setPreview(f)}>{f.name}</button>}</span></td><td>{detail}</td>
    <td><div className="drive-file-actions"><button className="btn-secondary" onClick={() => setPreview(f)}>Preview</button>{hasEditor(f.name) && <a href={editorURL(f)} {...target}>Open editor</a>}<a href={`/api/drive/files/${f.id}/download`}>Download</a></div></td>
  </tr>;
  const open = (id: string, name: string) => { setError(''); setPath([...path, { id, name }]); };

  return <section className="drive-page">
    <div className="drive-heading"><div><h1>Shared with me</h1><p>Files and folders people shared with you outside your own workspaces</p></div></div>
    {error && <p role="alert" className="panel">{error}</p>}
    <div className="panel drive-files-panel">
      <nav className="drive-breadcrumb" aria-label="Folder path">
        <button className="btn-link" onClick={() => setPath([])} aria-current={path.length === 0 ? 'page' : undefined}>Shared with me</button>
        {path.map((p, i) => <span key={p.id}> / <button className="btn-link" onClick={() => setPath(path.slice(0, i + 1))} aria-current={i === path.length - 1 ? 'page' : undefined}>{p.name}</button></span>)}
      </nav>
      <div className="drive-table"><table><thead><tr><th>Name</th><th>Details</th><th><span className="drive-actions-heading">Actions</span></th></tr></thead><tbody>
        {at ? <>
          {contents.folders.map(d => <tr key={d.id}><td><button className="drive-folder-name btn-link" onClick={() => open(d.id, d.name)}>{d.name}/</button></td><td>Folder</td><td /></tr>)}
          {contents.files.map(f => fileRow(f, bytes(f.size)))}
        </> : items.map(i => i.kind === 'folder'
          ? <tr key={i.id}><td><button className="drive-folder-name btn-link" onClick={() => open(i.id, i.name)}>{i.name}/</button></td><td>{i.workspace_name} · {i.role}</td><td /></tr>
          : fileRow(i, `${i.workspace_name} · ${i.role}`))}
      </tbody></table></div>
      {!at && items.length === 0 && !error && <div className="drive-empty"><h3>Nothing shared with you yet</h3><p>When someone shares a file or folder with you, it appears here.</p></div>}
    </div>
    {preview && <QuickLook key={preview.id} file={preview} onClose={() => setPreview(null)} />}
  </section>;
}
