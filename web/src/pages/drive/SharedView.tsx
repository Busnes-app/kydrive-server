import { useEffect, useState } from 'react';
import { bytes, editorURL, file, folder, hasEditor, list, opensInEditor, record, response, sharedItem } from './client';
import { QuickLook } from './QuickLook';
import { FileIcon, FolderIcon } from './FileIcon';
import { ChevronRight, Download, ExternalLink, Eye, FolderOpen, Users } from 'lucide-react';
import { Alert } from '../../components/Alert';
import { Menu } from '../../components/Menu';
import type { MenuEntry } from '../../components/Menu';
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

  const cap = (r: string) => r[0].toUpperCase() + r.slice(1);
  const actions = (f: { id: string; name: string }): MenuEntry[] => [
    { label: 'Preview', icon: <Eye size={16} />, onSelect: () => setPreview(f) },
    ...(hasEditor(f.name) ? [{ label: 'Open editor', icon: <ExternalLink size={16} />, href: editorURL(f), newTab: openInNewTab }] : []),
    { label: 'Download', icon: <Download size={16} />, href: `/api/drive/files/${f.id}/download` },
  ];
  const fileRow = (f: { id: string; name: string; revision?: number }, detail: string) => <tr key={f.id}>
    <td><span className="drive-name-cell"><FileIcon file={f} /><span className="drive-name-text">{opensInEditor(f.name) ? <a className="drive-file-name" href={editorURL(f)} {...target}>{f.name}</a> : <button className="drive-file-name btn-link" onClick={() => setPreview(f)}>{f.name}</button>}<span className="drive-row-meta">{detail}</span></span></span></td><td className="num">{detail}</td>
    <td className="col-actions"><Menu label={`Actions for ${f.name}`} items={actions(f)} /></td>
  </tr>;
  const folderRow = (id: string, name: string, detail: string) => <tr key={id}>
    <td><span className="drive-name-cell"><FolderIcon /><span className="drive-name-text"><button className="drive-file-name btn-link" onClick={() => open(id, name)}>{name}</button><span className="drive-row-meta">{detail}</span></span></span></td><td className="num">{detail}</td>
    <td className="col-actions"><Menu label={`Actions for folder ${name}`} items={[{ label: 'Open', icon: <FolderOpen size={16} />, onSelect: () => open(id, name) }, { label: 'Download zip', icon: <Download size={16} />, href: `/api/drive/folders/${id}/zip` }]} /></td>
  </tr>;
  const open = (id: string, name: string) => { setError(''); setPath([...path, { id, name }]); };
  const rows = at ? contents.folders.length + contents.files.length : items.length;

  return <section className="page drive-page">
    <header className="page-header"><div><h1>Shared with me</h1><p className="page-desc">Files and folders people shared with you outside your own workspaces</p></div></header>
    {error && <div style={{ marginBottom: '1rem' }}><Alert>{error}</Alert></div>}
    <div className="panel drive-files-panel">
      <div className="drive-toolbar"><nav className="drive-breadcrumb" aria-label="Folder path">
        <button className="btn-link" onClick={() => setPath([])} aria-current={path.length === 0 ? 'page' : undefined}>Shared with me</button>
        {path.map((p, i) => <span key={p.id} style={{ display: 'contents' }}><ChevronRight className="drive-breadcrumb-sep" size={16} aria-hidden="true" /><button className="btn-link" onClick={() => setPath(path.slice(0, i + 1))} aria-current={i === path.length - 1 ? 'page' : undefined}>{p.name}</button></span>)}
      </nav></div>
      {rows > 0 && <div className="drive-table"><table><thead><tr><th>Name</th><th className="num">{at ? 'Size' : 'Shared from'}</th><th className="col-actions"><span className="sr-only">Actions</span></th></tr></thead><tbody>
        {at ? <>
          {contents.folders.map(d => folderRow(d.id, d.name, 'Folder'))}
          {contents.files.map(f => fileRow(f, bytes(f.size)))}
        </> : items.map(i => i.kind === 'folder' ? folderRow(i.id, i.name, `${i.workspace_name} · ${cap(i.role)}`) : fileRow(i, `${i.workspace_name} · ${cap(i.role)}`))}
      </tbody></table></div>}
      {rows === 0 && !error && <div className="drive-empty"><Users size={32} aria-hidden="true" />{at ? <><h3>This folder is empty</h3><p>Nothing has been added to it yet.</p></> : <><h3>Nothing shared with you yet</h3><p>When someone shares a file or folder with you, it appears here.</p></>}</div>}
    </div>
    {preview && <QuickLook key={preview.id} file={preview} onClose={() => setPreview(null)} />}
  </section>;
}
