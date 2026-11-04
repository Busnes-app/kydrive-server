import { useCallback, useEffect, useRef, useState } from 'react';
import { bytes, change, editorURL, file, folder, hasEditor, list, opensInEditor, record, response, sharedItem } from './client';
import { QuickLook } from './QuickLook';
import { FileIcon, FolderIcon, iconFor } from './FileIcon';
import { MoveDialog } from './MoveDialog';
import type { Item } from './MoveDialog';
import { UploadList, useUploads } from './Uploads';
import type { Choice } from './Uploads';
import { documentKinds, extensions } from '../Drive';
import type { DocumentKind } from '../Drive';
import { ChevronRight, Download, ExternalLink, Eye, FolderOpen, FolderPlus, PenLine, Trash2, Upload, Users } from 'lucide-react';
import { Alert } from '../../components/Alert';
import { Dialog, useConfirm } from '../../components/Dialog';
import { Menu } from '../../components/Menu';
import type { MenuEntry } from '../../components/Menu';
import type { DriveFile, Folder, SharedItem } from './client';

type Place = { id: string; name: string; workspace: string; role: string };

async function children(id: string): Promise<{ folders: Folder[]; files: DriveFile[] }> {
  const v = await response(await fetch(`/api/drive/folders/${id}/children`));
  if (!record(v) || !Array.isArray(v.folders) || !v.folders.every(folder) || !Array.isArray(v.files) || !v.files.every(file)) throw new Error('Invalid folder response');
  return { folders: v.folders, files: v.files };
}

// SharedView lists what others shared with the user outside their own workspaces and browses
// into shared folders. The path starts at the shared item: nothing above it is visible. Inside a
// folder shared as editor or manager it offers what that role allows there: upload, new folder,
// new document, rename and move to trash. Resharing stays off for share holders.
export function SharedView({ openInNewTab }: { openInNewTab: boolean }) {
  const target = openInNewTab ? { target: '_blank', rel: 'noreferrer' } : {};
  const [items, setItems] = useState<SharedItem[]>([]);
  const [path, setPath] = useState<Place[]>([]);
  const [contents, setContents] = useState<{ folders: Folder[]; files: DriveFile[] }>({ folders: [], files: [] });
  const [error, setError] = useState('');
  const [preview, setPreview] = useState<{ id: string; name: string } | null>(null);
  const [renaming, setRenaming] = useState<Item | null>(null);
  const [creating, setCreating] = useState<{ kind: DocumentKind | 'folder'; name: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(0);
  const [confirm, confirmNode] = useConfirm();
  const uploadInput = useRef<HTMLInputElement>(null);
  const at = path[path.length - 1];
  const canEdit = at?.role === 'editor' || at?.role === 'manager';
  const refresh = useCallback(() => setLoaded(n => n + 1), []);
  const uploads = useUploads(refresh, useCallback(async (_: string, parent: string) => children(parent), []));

  useEffect(() => { void list('/api/drive/shared-with-me', sharedItem).then(setItems, e => setError(e instanceof Error ? e.message : 'Shared items unavailable')); }, []);
  useEffect(() => {
    if (!at) return;
    let active = true;
    void children(at.id).then(c => { if (active) setContents(c); }, e => { if (active) setError(e instanceof Error ? e.message : 'Folder unavailable'); });
    return () => { active = false; };
  }, [at, loaded]);

  async function run(action: () => Promise<unknown>) { setBusy(true); setError(''); try { await action(); refresh(); return true; } catch (e) { setError(e instanceof Error ? e.message : 'Operation failed'); return false; } finally { setBusy(false); } }
  const decide = async (names: string[]): Promise<Choice> => await confirm({ title: names.length === 1 ? `${names[0]} already exists` : `${names.length} files already exist`, body: 'Replace uploads each as a new version; earlier versions stay in Version history. Cancel skips them; other files still upload.', confirm: 'Replace (new version)' }) ? 'replace' : 'cancel';
  const upload = (chosen: File[]) => { if (at) void run(() => uploads.add(at.workspace, at.id, { dirs: [], files: chosen.map(f => ({ dirs: [], file: f })) }, contents.folders, decide)); };
  function create() {
    if (!at || !creating) return;
    const kind = creating.kind;
    // Open the tab inside the submit so pop-up blockers allow it; point it at the editor once the file exists.
    const tab = kind !== 'folder' && openInNewTab ? window.open('about:blank', '_blank') : null;
    if (tab) tab.opener = null;
    void run(async () => {
      try {
        if (kind === 'folder') { await change(`/api/drive/workspaces/${at.workspace}/folders`, 'POST', { parent: at.id, name: creating.name }); setCreating(null); return; }
        const created = await change(`/api/drive/workspaces/${at.workspace}/documents`, 'POST', { name: creating.name, kind, parent: at.id });
        if (!file(created)) throw new Error('Invalid document response');
        setCreating(null);
        if (tab) tab.location.href = editorURL(created); else window.location.assign(editorURL(created));
      } catch (e) { tab?.close(); throw e; }
    });
  }

  const cap = (r: string) => r[0].toUpperCase() + r.slice(1);
  const editing = (rename: () => void, trash: () => Promise<unknown>): MenuEntry[] => canEdit ? ['separator',
    { label: 'Rename', icon: <PenLine size={16} />, onSelect: rename },
    'separator',
    { label: 'Move to trash', icon: <Trash2 size={16} />, danger: true, onSelect: () => void run(trash) }] : [];
  const actions = (f: { id: string; name: string }, inside?: DriveFile): MenuEntry[] => [
    { label: 'Preview', icon: <Eye size={16} />, onSelect: () => setPreview(f) },
    ...(hasEditor(f.name) ? [{ label: 'Open editor', icon: <ExternalLink size={16} />, href: editorURL(f), newTab: openInNewTab }] : []),
    { label: 'Download', icon: <Download size={16} />, href: `/api/drive/files/${f.id}/download` },
    ...(inside ? editing(() => setRenaming({ kind: 'file', file: inside }), () => change(`/api/drive/files/${inside.id}/trash`, 'PUT', { revision: inside.revision, trashed: true })) : []),
  ];
  const fileRow = (f: { id: string; name: string; revision?: number }, detail: string, inside?: DriveFile) => <tr key={f.id}>
    <td><span className="drive-name-cell"><FileIcon file={f} /><span className="drive-name-text">{opensInEditor(f.name) ? <a className="drive-file-name" href={editorURL(f)} {...target}>{f.name}</a> : <button className="drive-file-name btn-link" onClick={() => setPreview(f)}>{f.name}</button>}<span className="drive-row-meta">{detail}</span></span></span></td><td className="num">{detail}</td>
    <td className="col-actions"><Menu label={`Actions for ${f.name}`} items={actions(f, inside)} /></td>
  </tr>;
  const folderRow = (id: string, name: string, detail: string, place: Omit<Place, 'id' | 'name'>, inside?: Folder) => <tr key={id}>
    <td><span className="drive-name-cell"><FolderIcon /><span className="drive-name-text"><button className="drive-file-name btn-link" onClick={() => open({ id, name, ...place })}>{name}</button><span className="drive-row-meta">{detail}</span></span></span></td><td className="num">{detail}</td>
    <td className="col-actions"><Menu label={`Actions for folder ${name}`} items={[{ label: 'Open', icon: <FolderOpen size={16} />, onSelect: () => open({ id, name, ...place }) }, { label: 'Download zip', icon: <Download size={16} />, href: `/api/drive/folders/${id}/zip` },
      ...(inside ? editing(() => setRenaming({ kind: 'folder', folder: inside }), () => change(`/api/drive/folders/${inside.id}/trash`, 'PUT', { trashed: true })) : [])]} /></td>
  </tr>;
  const open = (p: Place) => { setError(''); setPath([...path, p]); };
  const rows = at ? contents.folders.length + contents.files.length : items.length;
  const newMenu: MenuEntry[] = [
    { label: 'New folder', icon: <FolderPlus size={16} />, onSelect: () => setCreating({ kind: 'folder', name: '' }) },
    'separator',
    ...documentKinds.map(([kind, label]) => { const Icon = iconFor(`x.${extensions[kind]}`); return { label, icon: <Icon size={16} />, onSelect: () => setCreating({ kind, name: `Untitled ${kind}` }) }; }),
  ];

  return <section className="page drive-page">
    <header className="page-header"><div><h1>Shared with me</h1><p className="page-desc">Files and folders people shared with you outside your own workspaces</p></div>
      {at && canEdit && <div className="page-actions">
        <button className="btn-secondary" disabled={busy} onClick={() => uploadInput.current?.click()}><Upload size={16} aria-hidden="true" />Upload files</button>
        <Menu label="New" trigger="primary" items={newMenu} />
        <input ref={uploadInput} type="file" multiple hidden aria-label="Choose files to upload" onChange={e => { const chosen = Array.from(e.target.files ?? []); e.target.value = ''; upload(chosen); }} />
      </div>}
    </header>
    {error && !creating && <div style={{ marginBottom: '1rem' }}><Alert>{error}</Alert></div>}
    <div className="panel drive-files-panel">
      <div className="drive-toolbar"><nav className="drive-breadcrumb" aria-label="Folder path">
        <button className="btn-link" onClick={() => setPath([])} aria-current={path.length === 0 ? 'page' : undefined}>Shared with me</button>
        {path.map((p, i) => <span key={p.id} style={{ display: 'contents' }}><ChevronRight className="drive-breadcrumb-sep" size={16} aria-hidden="true" /><button className="btn-link" onClick={() => setPath(path.slice(0, i + 1))} aria-current={i === path.length - 1 ? 'page' : undefined}>{p.name}</button></span>)}
      </nav></div>
      <UploadList {...uploads} />
      {rows > 0 && <div className="drive-table"><table><thead><tr><th>Name</th><th className="num">{at ? 'Size' : 'Shared from'}</th><th className="col-actions"><span className="sr-only">Actions</span></th></tr></thead><tbody>
        {at ? <>
          {contents.folders.map(d => folderRow(d.id, d.name, 'Folder', { workspace: at.workspace, role: at.role }, d))}
          {contents.files.map(f => fileRow(f, bytes(f.size), f))}
        </> : items.map(i => i.kind === 'folder' ? folderRow(i.id, i.name, `${i.workspace_name} · ${cap(i.role)}`, { workspace: i.workspace, role: i.role }) : fileRow(i, `${i.workspace_name} · ${cap(i.role)}`))}
      </tbody></table></div>}
      {rows === 0 && !error && <div className="drive-empty"><Users size={32} aria-hidden="true" />{at ? <><h3>This folder is empty</h3><p>{canEdit ? 'Upload files or create a document from New.' : 'Nothing has been added to it yet.'}</p></> : <><h3>Nothing shared with you yet</h3><p>When someone shares a file or folder with you, it appears here.</p></>}</div>}
    </div>
    {creating && at && <Dialog title={creating.kind === 'folder' ? 'New folder' : documentKinds.find(([k]) => k === creating.kind)?.[1] ?? 'New document'} description={`Create in ${at.name}`} onClose={() => setCreating(null)} onSubmit={create}
      footer={<><button type="button" className="btn-secondary" onClick={() => setCreating(null)}>Cancel</button><button disabled={busy || !creating.name.trim()}>{creating.kind === 'folder' ? 'Create' : 'Create & open'}</button></>}>
      <label className="field">{creating.kind === 'folder' ? 'Folder name' : 'File name'}<input autoFocus required value={creating.name} onChange={e => setCreating({ ...creating, name: e.target.value })} /></label>
      {error && <Alert>{error}</Alert>}
    </Dialog>}
    {renaming && at && <MoveDialog mode="rename" item={renaming} workspaceID={at.workspace} workspaces={[]} onClose={() => setRenaming(null)} onDone={() => { setRenaming(null); refresh(); }} />}
    {preview && <QuickLook key={preview.id} file={preview} onClose={() => setPreview(null)} />}
    {confirmNode}
  </section>;
}
