import { useCallback, useEffect, useRef, useState } from 'react';
import { ArrowLeft, ChevronRight, Copy, Download, ExternalLink, Eye, FolderInput, FolderOpen, FolderPlus, FolderUp, History, PenLine, RefreshCw, Share2, Trash2, Upload } from 'lucide-react';
import { bytes, breadcrumb, change, editorURL, file, folder, hasEditor, HttpError, opensInEditor, grant, group, list, record, remove, response, version, workspace } from './drive/client';
import { Leavers } from './drive/Leavers';
import { MoveDialog } from './drive/MoveDialog';
import { ShareDialog } from './drive/ShareDialog';
import { QuickLook } from './drive/QuickLook';
import { FileIcon, FolderIcon, iconFor } from './drive/FileIcon';
import { Search } from './drive/Search';
import { Versions } from './drive/Versions';
import type { Hit } from './drive/Search';
import { UploadList, useUploads } from './drive/Uploads';
import type { Choice } from './drive/Uploads';
import { fromDataTransfer, fromFolderInput } from './drive/upload';
import type { Entry } from './drive/upload';
import type { Item, Mode } from './drive/MoveDialog';
import type { DriveFile, Folder, Grant, Group, Version, Workspace } from './drive/client';
import { Alert } from '../components/Alert';
import { Dialog, useConfirm } from '../components/Dialog';
import { Menu } from '../components/Menu';
import type { MenuEntry } from '../components/Menu';
import { When } from '../components/When';

export type { Workspace } from './drive/client';

type DocumentKind = 'document' | 'spreadsheet' | 'presentation' | 'markdown' | 'rtf' | 'whiteboard';
const extensions: Record<DocumentKind, string> = { document: 'docx', spreadsheet: 'xlsx', presentation: 'pptx', markdown: 'md', rtf: 'rtf', whiteboard: 'excalidraw' };
const documentKinds: [DocumentKind, string][] = [['document', 'New document'], ['spreadsheet', 'New spreadsheet'], ['presentation', 'New presentation'], ['markdown', 'New markdown'], ['rtf', 'New rich text'], ['whiteboard', 'New whiteboard']];
type AuditEvent = { action: string; created: string };

export function Drive({ admin, workspaces, onWorkspacesChange, selected, onSelectWorkspace, openInNewTab }: { admin: boolean; workspaces: Workspace[]; onWorkspacesChange: (workspaces: Workspace[]) => void; selected: string; onSelectWorkspace: (id: string) => void; openInNewTab: boolean }) {
  const fileTarget = openInNewTab ? { target: '_blank', rel: 'noreferrer' } : {};
  const [newDocument, setNewDocument] = useState<{ kind: DocumentKind; name: string } | null>(null);
  const [newFolderOpen, setNewFolderOpen] = useState(false);
  const uploadInput = useRef<HTMLInputElement>(null);
  const folderInput = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const [view, setView] = useState<'files' | 'settings'>('files');
  const [files, setFiles] = useState<DriveFile[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [parent, setParent] = useState('');
  const [trash, setTrash] = useState(false);
  const [grants, setGrants] = useState<Grant[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  const [identityURL, setIdentityURL] = useState('');
  const [newName, setNewName] = useState('');
  const [quota, setQuota] = useState(1024);
  const [trashDays, setTrashDays] = useState(0);
  const [keepVersions, setKeepVersions] = useState(0);
  const [newFolder, setNewFolder] = useState('');
  const [groupID, setGroupID] = useState('');
  const [role, setRole] = useState('reader');
  const [history, setHistory] = useState<{ file: DriveFile; versions: Version[] } | null>(null);
  const [dialog, setDialog] = useState<{ mode: Mode; item: Item } | null>(null);
  const [sharing, setSharing] = useState<{ kind: 'file' | 'folder'; id: string; name: string } | null>(null);
  const [preview, setPreview] = useState<DriveFile | null>(null);
  const [clash, setClash] = useState<{ names: string[]; answer: (c: Choice) => void } | null>(null);
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const [operations, setOperations] = useState('');
  const [audit, setAudit] = useState<AuditEvent[]>([]);
  const [confirm, confirmNode] = useConfirm();
  const current = workspaces.find(w => w.id === selected);
  const path = breadcrumb(folders, parent);
  const pageTitle = view === 'settings' ? 'Workspace settings' : trash ? 'Trash' : path.at(-1)?.name ?? current?.name ?? 'Workspaces';
  const visibleFolders = trash ? folders : folders.filter(d => d.parent === parent);
  const visibleFiles = files.filter(f => trash || f.parent === parent);
  const canEdit = current?.role === 'editor' || current?.role === 'manager';
  const canPurge = current?.role === 'manager';
  const canManage = current?.kind === 'shared' && (admin || current.role === 'manager');
  const canShare = canPurge || (canEdit && current?.editors_share === true);
  const generation = useRef(0);
  const shown = useRef({ selected, trash });
  shown.current = { selected, trash };
  const reload = useCallback(async () => {
    // Load the view on screen now, not the one an earlier action started from,
    // and let a newer reload win over an older one still in flight.
    const { selected, trash } = shown.current;
    const mine = ++generation.current;
    const stale = () => mine !== generation.current;
    await change('/api/drive/personal-workspace', 'POST', {});
    const listWorkspaces = await list(`/api/drive/workspaces${admin ? '?admin=true' : ''}`, workspace);
    if (stale()) return;
    onWorkspacesChange(listWorkspaces);
    if (!selected) {
      const personal = listWorkspaces.find(w => w.kind === 'personal') || listWorkspaces[0];
      if (personal) onSelectWorkspace(personal.id);
      return;
    }
    const currentWS = listWorkspaces.find(w => w.id === selected);
    const [fs, ds] = currentWS?.role ? await Promise.all([list(`/api/drive/workspaces/${selected}/files?trash=${trash}`, file), list(`/api/drive/workspaces/${selected}/folders?trash=${trash}`, folder)]) : [[], []];
    const gs = currentWS?.kind === 'shared' && (admin || currentWS.role === 'manager') ? await list(`/api/drive/workspaces/${selected}/grants`, grant) : [];
    if (stale()) return;
    setFiles(fs); setFolders(ds); setGrants(gs);
  }, [admin, selected, trash, onWorkspacesChange, onSelectWorkspace]);
  const uploads = useUploads(useCallback(() => { void reload().catch(e => setMessage(e instanceof Error ? e.message : 'Drive unavailable')); }, [reload]));
  useEffect(() => { setTrashDays(current?.trash_days ?? 0); setKeepVersions(current?.keep_versions ?? 0); }, [current?.id, current?.trash_days, current?.keep_versions]);
  // A search result in another workspace opens its folder once that workspace is selected.
  const openAfterSwitch = useRef('');
  useEffect(() => { setParent(openAfterSwitch.current); openAfterSwitch.current = ''; setHistory(null); setView('files'); }, [selected]);
  const canDrop = (dt: DataTransfer) => canEdit && !trash && view === 'files' && dt.types.includes('Files');
  function openHit(h: Hit) {
    const target = h.kind === 'folder' ? h.id : h.parent;
    setTrash(false);
    if (h.workspace === selected) { setParent(target); return; }
    openAfterSwitch.current = target;
    onSelectWorkspace(h.workspace);
  }
  useEffect(() => { void reload().catch(e => setMessage(e instanceof Error ? e.message : 'Drive unavailable')); }, [reload]);
  useEffect(() => { if (!canManage) return; let active = true; void (async () => { const v = await response(await fetch(admin ? '/api/drive/directory' : `/api/drive/workspaces/${selected}/directory`)); if (!active) return; if (!record(v) || !Array.isArray(v.groups) || !v.groups.every(group)) throw new Error('Invalid directory'); setGroups(v.groups); if (typeof v.identity_url === 'string' && v.identity_url.startsWith('https://')) setIdentityURL(v.identity_url); })().catch(e => { if (active) setMessage(e instanceof Error ? e.message : 'Directory unavailable'); }); return () => { active = false; }; }, [admin, selected, canManage]);
  useEffect(() => { if (!admin) return; let active = true; const refresh = async () => { try { const v = await response(await fetch('/api/drive/status')); if (record(v) && record(v.editor_revocations) && typeof v.editor_revocations.pending === 'number' && active) setOperations(`Directory: ${v.scim_enabled ? 'SCIM enabled' : 'SCIM disabled'} · Editor: ${v.editor_configured ? 'configured' : 'not configured'} · Pending editor revocations: ${v.editor_revocations.pending} · Bulk backup: ${v.bulk_repository_configured ? 'configured' : 'not configured'}`); const events = await response(await fetch('/api/drive/audit')); if (Array.isArray(events) && active) setAudit(events.filter(record).filter((e): e is AuditEvent => typeof e.action === 'string' && typeof e.created === 'string').slice(0, 10)); } catch { if (active) setOperations('Operational status unavailable; refresh before assuming access changes have reconciled.'); } }; void refresh(); const timer = setInterval(() => void refresh(), 5000); return () => { active = false; clearInterval(timer); }; }, [admin]);
  function startDocument(kind: DocumentKind) {
    const extension = extensions[kind];
    const base = `Untitled ${kind}`;
    let name = base;
    let suffix = 2;
    const names = new Set(files.filter(f => !f.trashed && f.parent === parent).map(f => f.name.toLowerCase()));
    while (names.has(`${name}.${extension}`.toLowerCase())) name = `${base} (${suffix++})`;
    setMessage('');
    setNewDocument({ kind, name });
  }
  async function run(action: () => Promise<unknown>) { setBusy(true); setMessage(''); try { await action(); await reload(); } catch (e) { setMessage(e instanceof Error ? e.message : 'Operation failed'); } finally { setBusy(false); } }
  // Purges can fail partway; reload either way so the list shows what is really left.
  function runPurge(action: () => Promise<unknown>) { void run(async () => { try { await action(); } catch (e) { await reload().catch(() => {}); throw e; } }); }
  async function purge(url: string, label: string) { if (await confirm({ title: `Delete ${label} permanently?`, body: 'It is removed from the drive at once and cannot be restored here; only a backup can bring it back.', confirm: 'Delete permanently', danger: true })) runPurge(() => remove(url)); }
  async function emptyTrash() {
    if (!await confirm({ title: 'Empty the trash?', body: `Everything in the trash of ${current?.name ?? 'this workspace'} is deleted permanently. Only a backup can bring it back.`, confirm: 'Empty trash', danger: true })) return;
    const trashedFolders = new Set(folders.map(d => d.id));
    runPurge(async () => {
      for (const d of folders.filter(d => !trashedFolders.has(d.parent))) await remove(`/api/drive/folders/${d.id}`);
      for (const f of files.filter(f => !trashedFolders.has(f.parent))) await remove(`/api/drive/files/${f.id}`);
    });
  }
  const decide = (names: string[]) => new Promise<Choice>(answer => setClash({ names, answer }));
  const upload = (tree: Promise<{ dirs: string[][]; files: Entry[] }> | { dirs: string[][]; files: Entry[] }) => void run(async () => uploads.add(selected, parent, await tree, folders, decide));
  const showHistory = (f: DriveFile) => void run(async () => setHistory({ file: f, versions: await list(`/api/drive/files/${f.id}/versions`, version) }));

  const folderActions = (d: Folder): MenuEntry[] => [
    { label: 'Open', icon: <FolderOpen size={16} />, onSelect: () => setParent(d.id) },
    { label: 'Download zip', icon: <Download size={16} />, href: `/api/drive/folders/${d.id}/zip` },
    ...(canShare ? [{ label: 'Share', icon: <Share2 size={16} />, onSelect: () => setSharing({ kind: 'folder', id: d.id, name: d.name }) }] : []),
    ...(canEdit ? ['separator' as const,
      { label: 'Rename', icon: <PenLine size={16} />, onSelect: () => setDialog({ mode: 'rename', item: { kind: 'folder', folder: d } }) },
      { label: 'Move', icon: <FolderInput size={16} />, onSelect: () => setDialog({ mode: 'move', item: { kind: 'folder', folder: d } }) },
      'separator' as const,
      { label: 'Move to trash', icon: <Trash2 size={16} />, danger: true, onSelect: () => void run(() => change(`/api/drive/folders/${d.id}/trash`, 'PUT', { trashed: true })) }] : []),
  ];
  const fileActions = (f: DriveFile): MenuEntry[] => [
    { label: 'Preview', icon: <Eye size={16} />, onSelect: () => setPreview(f) },
    ...(hasEditor(f.name) ? [{ label: 'Open editor', icon: <ExternalLink size={16} />, href: editorURL(f), newTab: openInNewTab }] : []),
    { label: 'Download', icon: <Download size={16} />, href: `/api/drive/files/${f.id}/download` },
    ...(canShare ? [{ label: 'Share', icon: <Share2 size={16} />, onSelect: () => setSharing({ kind: 'file', id: f.id, name: f.name }) }] : []),
    'separator',
    ...(canEdit ? [{ label: 'Rename', icon: <PenLine size={16} />, onSelect: () => setDialog({ mode: 'rename', item: { kind: 'file', file: f } }) },
      { label: 'Move', icon: <FolderInput size={16} />, onSelect: () => setDialog({ mode: 'move', item: { kind: 'file', file: f } }) }] : []),
    { label: 'Copy', icon: <Copy size={16} />, onSelect: () => setDialog({ mode: 'copy', item: { kind: 'file', file: f } }) },
    { label: 'Version history', icon: <History size={16} />, onSelect: () => showHistory(f) },
    ...(canEdit ? ['separator' as const, { label: 'Move to trash', icon: <Trash2 size={16} />, danger: true, onSelect: () => void run(() => change(`/api/drive/files/${f.id}/trash`, 'PUT', { revision: f.revision, trashed: true })) }] : []),
  ];
  const restoreButton = (onClick: () => void) => canEdit && <button className="btn-secondary btn-sm" disabled={busy} onClick={onClick}>Restore</button>;
  const purgeButton = (onClick: () => void) => canPurge && <button className="btn-danger-quiet btn-sm" disabled={busy} onClick={onClick}>Delete permanently</button>;

  const newMenu: MenuEntry[] = [
    { label: 'New folder', icon: <FolderPlus size={16} />, onSelect: () => { setNewFolder(''); setNewFolderOpen(true); } },
    'separator',
    ...documentKinds.map(([kind, label]) => { const Icon = iconFor(`x.${extensions[kind]}`); return { label, icon: <Icon size={16} />, onSelect: () => startDocument(kind) }; }),
    'separator',
    { label: 'Upload folder', icon: <FolderUp size={16} />, onSelect: () => folderInput.current?.click() },
  ];

  const filesPanel = current && current.role && <div className={dragging ? 'panel drive-files-panel drive-drop-active' : 'panel drive-files-panel'}
    onDragOver={e => { if (canDrop(e.dataTransfer)) { e.preventDefault(); setDragging(true); } }}
    onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false); }}
    onDrop={e => { setDragging(false); if (!canDrop(e.dataTransfer)) return; e.preventDefault(); upload(fromDataTransfer(e.dataTransfer)); }}>
    {trash ? <div className="drive-trash-banner">
      <p>{current.trash_days > 0 ? `Items here are deleted permanently after ${current.trash_days} days.` : 'Deleted items stay here until they are restored or deleted permanently.'}</p>
      <div className="drive-toolbar-actions">
        {canPurge && <button className="btn-danger-quiet" disabled={busy || visibleFiles.length + visibleFolders.length === 0} onClick={() => void emptyTrash()}>Empty trash</button>}
        <button className="btn-secondary" aria-pressed={trash} onClick={() => { setFiles([]); setFolders([]); setTrash(false); }}><ArrowLeft size={16} aria-hidden="true" />Back to files</button>
      </div>
    </div> : <div className="drive-toolbar">
      <nav className="drive-breadcrumb" aria-label="Folder path">
        <button className="btn-link" onClick={() => setParent('')} aria-current={parent === '' ? 'page' : undefined}>{current.name}</button>
        {path.map(f => <span key={f.id} style={{ display: 'contents' }}><ChevronRight className="drive-breadcrumb-sep" size={16} aria-hidden="true" /><button className="btn-link" onClick={() => setParent(f.id)} aria-current={f.id === parent ? 'page' : undefined}>{f.name}</button></span>)}
      </nav>
      <div className="drive-toolbar-actions">
        <button className="btn-ghost" aria-pressed={trash} onClick={() => { setFiles([]); setFolders([]); setTrash(true); }}><Trash2 size={16} aria-hidden="true" />Trash</button>
        <button className="icon-btn" aria-label="Refresh" title="Refresh" disabled={busy} onClick={() => void run(reload)}><RefreshCw size={16} aria-hidden="true" /></button>
        {canEdit && <>
          <Menu label="New" trigger="text" items={newMenu} />
          <input ref={uploadInput} type="file" multiple hidden aria-label="Choose files to upload" onChange={e => { const chosen = Array.from(e.target.files ?? []); e.target.value = ''; upload({ dirs: [], files: chosen.map(file => ({ dirs: [], file })) }); }} />
          <input ref={folderInput} type="file" hidden aria-label="Choose folder to upload" {...{ webkitdirectory: '' }} onChange={e => { const chosen = e.target.files; if (chosen) { const tree = fromFolderInput(chosen); e.target.value = ''; upload(tree); } }} />
        </>}
      </div>
    </div>}
    <UploadList {...uploads} />
    {visibleFiles.length + visibleFolders.length > 0 && <div className="drive-table"><table>
      <thead><tr><th>Name</th><th className="num">Size</th><th className="num">Version</th><th className="col-actions"><span className="sr-only">Actions</span></th></tr></thead>
      <tbody>
        {visibleFolders.map(d => <tr key={`folder-${d.id}`}>
          <td><span className="drive-name-cell"><FolderIcon /><span className="drive-name-text">{trash ? <span className="drive-file-name">{d.name}</span> : <button className="drive-file-name btn-link" onClick={() => setParent(d.id)}>{d.name}</button>}<span className="drive-row-meta">Folder</span></span></span></td>
          <td className="num">Folder</td><td className="num">—</td>
          <td className="col-actions">{trash ? <div className="button-row">{restoreButton(() => void run(() => change(`/api/drive/folders/${d.id}/trash`, 'PUT', { trashed: false })))}{purgeButton(() => void purge(`/api/drive/folders/${d.id}`, d.name))}</div>
            : <Menu label={`Actions for folder ${d.name}`} items={folderActions(d)} />}</td>
        </tr>)}
        {visibleFiles.map(f => <tr key={f.id}>
          <td><span className="drive-name-cell"><FileIcon file={f} /><span className="drive-name-text">{f.trashed ? <span className="drive-file-name">{f.name}</span> : opensInEditor(f.name) ? <a className="drive-file-name" href={editorURL(f)} {...fileTarget}>{f.name}</a> : <button className="drive-file-name btn-link" onClick={() => setPreview(f)}>{f.name}</button>}<span className="drive-row-meta">{bytes(f.size)} · version {f.revision}</span></span></span></td>
          <td className="num">{bytes(f.size)}</td><td className="num">{f.revision}</td>
          <td className="col-actions">{f.trashed ? <div className="button-row">{restoreButton(() => void run(() => change(`/api/drive/files/${f.id}/trash`, 'PUT', { revision: f.revision, trashed: false })))}{purgeButton(() => void purge(`/api/drive/files/${f.id}`, f.name))}</div>
            : <Menu label={`Actions for ${f.name}`} items={fileActions(f)} />}</td>
        </tr>)}
      </tbody>
    </table></div>}
    {visibleFiles.length + visibleFolders.length === 0 && <div className="drive-empty">
      {trash ? <Trash2 size={32} aria-hidden="true" /> : <FolderOpen size={32} aria-hidden="true" />}
      <h3>{trash ? 'Trash is empty' : parent ? 'This folder is empty' : 'No files here yet'}</h3>
      <p>{trash ? 'Deleted items appear here until restored or deleted permanently.' : canEdit ? 'Drop files here, upload them, or create a document from New.' : 'Files shared with this workspace will appear here.'}</p>
      {!trash && canEdit && <div className="button-row"><button className="btn-secondary" disabled={busy} onClick={() => uploadInput.current?.click()}><Upload size={16} aria-hidden="true" />Upload files</button></div>}
    </div>}
  </div>;

  const settingsPanel = canManage && current && <div className="drive-settings-grid">
    <section className="panel" aria-labelledby="grants-heading">
      <h2 id="grants-heading">Group permissions</h2>
      <p>Choose which groups can open this workspace and what they can do. Manage group members in KyIdentity.</p>
      {grants.length === 0 ? <p className="empty-note">No group has access yet.</p> : <ul className="item-list" aria-label="Group permissions">{grants.map(g => <li key={g.group_id}>
        <div className="item-main"><span className="item-title">{g.name} <span className="badge">{g.role[0].toUpperCase() + g.role.slice(1)}</span></span></div>
        <div className="item-actions"><button className="btn-danger-quiet btn-sm" disabled={busy} onClick={() => void run(() => change(`/api/drive/workspaces/${selected}/grants/${g.group_id}`, 'PUT', { role: '' }))}>Remove access</button></div>
      </li>)}</ul>}
      <form className="form-row" style={{ marginTop: '1rem' }} onSubmit={e => { e.preventDefault(); void run(() => change(`/api/drive/workspaces/${selected}/grants/${groupID}`, 'PUT', { role })); }}>
        <label className="field">Group<select required value={groupID} onChange={e => setGroupID(e.target.value)}><option value="">Choose group</option>{groups.map(g => <option key={g.id} value={g.id}>{g.display_name}</option>)}</select></label>
        <label className="field">Access<select value={role} onChange={e => setRole(e.target.value)}><option value="reader">Reader</option><option value="editor">Editor</option><option value="manager">Manager</option></select></label>
        <button disabled={busy || !groupID}>Apply permission</button>
      </form>
      {current.role === 'manager' && <div className="panel-section"><label className="check"><input type="checkbox" checked={current.editors_share} disabled={busy} onChange={e => { const on = e.target.checked; void run(() => change(`/api/drive/workspaces/${selected}/sharing`, 'PUT', { editors_share: on })); }} /><span>Editors may share files and folders, up to editor access</span></label></div>}
    </section>
    {admin && <section className="panel" aria-labelledby="quota-heading">
      <h2 id="quota-heading">Storage</h2>
      <p>{bytes(current.used)} of {bytes(current.quota)} used.</p>
      <form className="form-row" onSubmit={e => { e.preventDefault(); void run(() => change(`/api/drive/workspaces/${selected}/quota`, 'PUT', { quota: quota * 1048576 })); }}>
        <label className="field">Quota (MiB)<input type="number" min="1" required value={quota} onChange={e => setQuota(e.target.valueAsNumber)} /></label>
        <button className="btn-secondary" disabled={busy}>Set quota</button>
      </form>
    </section>}
    {admin && <section className="panel" aria-labelledby="retention-heading">
      <h2 id="retention-heading">Retention</h2>
      <p>Empty trash at 0 follows the drive-wide trash setting; keep versions at 0 keeps every version. Deleted items cannot be recovered from the drive, only from backups.</p>
      <form className="form-row" onSubmit={e => { e.preventDefault(); void run(() => change(`/api/drive/workspaces/${selected}/retention`, 'PUT', { trash_days: trashDays, keep_versions: keepVersions })); }}>
        <label className="field">Empty trash after (days)<input type="number" min="0" max="3650" required value={trashDays} onChange={e => setTrashDays(e.target.valueAsNumber)} /></label>
        <label className="field">Keep newest versions<input type="number" min="0" max="1000" required value={keepVersions} onChange={e => setKeepVersions(e.target.valueAsNumber)} /></label>
        <button className="btn-secondary" disabled={busy}>Save retention</button>
      </form>
    </section>}
  </div>;

  return <section className="page drive-page">
    <header className="page-header">
      <div><h1>{pageTitle}</h1><p className="page-desc">{trash || view === 'settings' ? current?.name : current?.kind === 'personal' ? 'Private files for your account' : current ? 'Shared files for your team' : 'Your files and shared workspaces'}</p>
        {current && <span className="drive-storage"><meter min={0} max={current.quota || 1} value={current.used} aria-label="Storage used" />{bytes(current.used)} of {bytes(current.quota)} used</span>}
      </div>
      <div className="page-actions">
        {canEdit && view === 'files' && !trash && <button className="drive-upload-button" disabled={busy} onClick={() => uploadInput.current?.click()}><Upload size={16} aria-hidden="true" />Upload files</button>}
        {identityURL && <a className="btn btn-secondary btn-sm" href={identityURL} target="_blank" rel="noreferrer">Manage people &amp; groups in KyIdentity<ExternalLink size={14} aria-hidden="true" /></a>}
      </div>
    </header>
    {message && !newDocument && !newFolderOpen && <div style={{ marginBottom: '1rem' }}><Alert>{message}</Alert></div>}
    {current && <Search workspaces={workspaces} onOpen={openHit} />}
    {current ? <>
      {canManage && <div className="tabs" role="tablist" aria-label="Workspace views">
        <button role="tab" className="tab" aria-selected={view === 'files'} onClick={() => setView('files')}>Files</button>
        <button role="tab" className="tab" aria-selected={view === 'settings'} onClick={() => setView('settings')}>Workspace settings</button>
      </div>}
      {view === 'files' && (current.role ? filesPanel : <div className="panel drive-empty"><FolderOpen size={32} aria-hidden="true" /><h3>No access to files yet</h3><p>Assign a group permission in Workspace settings to open this workspace's files.</p></div>)}
      {view === 'settings' && settingsPanel}
    </> : <div className="panel drive-empty"><FolderOpen size={32} aria-hidden="true" /><h3>Your shared drive</h3><p>Create a workspace and assign a KyIdentity group to start.</p></div>}

    {admin && <details className="drive-admin">
      <summary>Administration</summary>
      <div className="drive-admin-panels">
      <details className="panel"><summary>Operations &amp; audit <small>Directory, editor and backup status</small></summary><div className="details-body">
        <p role="status" className="hint">{operations || 'Loading…'}</p>
        {audit.length > 0 && <ul className="drive-audit" aria-label="Recent audit events">{audit.map((e, i) => <li key={i}><When value={e.created} /><code>{e.action}</code></li>)}</ul>}
      </div></details>
      <details className="panel drive-create-workspace"><summary>Create shared workspace</summary><div className="details-body">
        <form className="form-row" onSubmit={e => { e.preventDefault(); void run(async () => { try { await change('/api/drive/workspaces', 'POST', { name: newName, quota: quota * 1048576 }); } catch (err) { throw err instanceof HttpError && err.status === 409 ? new Error(`A shared workspace named “${newName.trim()}” already exists.`) : err; } setNewName(''); }); }}>
          <label className="field grow">Name<input required value={newName} onChange={e => setNewName(e.target.value)} /></label>
          <label className="field">Quota (MiB)<input type="number" min="1" required value={quota} onChange={e => setQuota(e.target.valueAsNumber)} /></label>
          <button disabled={busy}>Create workspace</button>
        </form>
      </div></details>
      <Leavers workspaces={workspaces} onDone={() => void reload().catch(() => {})} />
      </div>
    </details>}

    {newDocument && <Dialog title={`New ${documentKinds.find(([k]) => k === newDocument.kind)?.[1].slice(4) ?? newDocument.kind}`} description={`Create in ${current?.name ?? 'this workspace'}`} onClose={() => setNewDocument(null)}
      onSubmit={() => {
        // Open the tab inside the submit so pop-up blockers allow it; point it at the editor once the file exists.
        const tab = openInNewTab ? window.open('about:blank', '_blank') : null;
        if (tab) tab.opener = null;
        void run(async () => {
          try {
            const created = await change(`/api/drive/workspaces/${selected}/documents`, 'POST', { name: newDocument.name, kind: newDocument.kind, parent }); if (!file(created)) throw new Error('Invalid document response'); const target = editorURL(created); setNewDocument(null);
            if (tab) tab.location.href = target; else window.location.assign(target);
          } catch (err) { tab?.close(); throw err; }
        });
      }}
      footer={<><button type="button" className="btn-secondary" onClick={() => setNewDocument(null)}>Cancel</button><button disabled={busy || !newDocument.name.trim()}>Create &amp; open</button></>}>
      <label className="field">File name<input autoFocus required value={newDocument.name} onChange={e => setNewDocument({ ...newDocument, name: e.target.value })} /></label>
      {message && <Alert>{message}</Alert>}
    </Dialog>}
    {newFolderOpen && <Dialog title="New folder" description={`Create in ${[current?.name, ...breadcrumb(folders, parent).map(f => f.name)].filter(Boolean).join(' / ')}`} onClose={() => setNewFolderOpen(false)}
      onSubmit={() => void run(async () => { await change(`/api/drive/workspaces/${selected}/folders`, 'POST', { parent, name: newFolder }); setNewFolder(''); setNewFolderOpen(false); })}
      footer={<><button type="button" className="btn-secondary" onClick={() => setNewFolderOpen(false)}>Cancel</button><button disabled={busy || !newFolder.trim()}>Create</button></>}>
      <label className="field">Folder name<input autoFocus required value={newFolder} onChange={e => setNewFolder(e.target.value)} /></label>
      {message && <Alert>{message}</Alert>}
    </Dialog>}
    {clash && <Dialog title={clash.names.length === 1 ? `${clash.names[0]} already exists` : `${clash.names.length} files already exist`}
      onClose={() => { clash.answer('cancel'); setClash(null); }}
      footer={<>
        <button type="button" className="btn-ghost" onClick={() => { clash.answer('cancel'); setClash(null); }}>Cancel</button>
        <button type="button" className="btn-secondary" onClick={() => { clash.answer('keep'); setClash(null); }}>Keep both</button>
        <button type="button" autoFocus onClick={() => { clash.answer('replace'); setClash(null); }}>Replace (new version)</button>
      </>}>
      <p>{clash.names.length === 1 ? 'A file with this name is already in the folder.' : 'Files with these names are already in their folders.'} Replace uploads {clash.names.length === 1 ? 'it' : 'each'} as a new version; earlier versions stay in Version history. Keep both uploads {clash.names.length === 1 ? 'it' : 'them'} under a numbered name. Cancel skips {clash.names.length === 1 ? 'it' : 'these files'}; other files still upload.</p>
      {clash.names.length > 1 && <ul className="drive-conflict-list" aria-label="Files that already exist">{clash.names.map((n, i) => <li key={i}>{n}</li>)}</ul>}
    </Dialog>}
    {dialog && current && <MoveDialog {...dialog} workspaceID={current.id} workspaces={workspaces} onClose={() => setDialog(null)} onDone={() => { setDialog(null); void run(reload); }} />}
    {sharing && <ShareDialog {...sharing} groups={canManage ? groups : []} maxRole={canPurge ? 'manager' : 'editor'} onClose={() => setSharing(null)} />}
    {preview && <QuickLook key={preview.id} file={preview} onClose={() => setPreview(null)} />}
    {history && <Versions file={history.file} versions={history.versions} busy={busy} canRestore={canEdit && !history.file.trashed} canDelete={canPurge}
      onClose={() => setHistory(null)}
      onRestore={v => void run(async () => { await change(`/api/drive/files/${history.file.id}/restore`, 'POST', { revision: v.revision, expected: history.file.revision }); setHistory(null); })}
      onDelete={async v => { if (await confirm({ title: `Delete version ${v.revision} permanently?`, body: `${history.file.name} keeps its other versions. Only a backup can bring this one back.`, confirm: 'Delete version', danger: true })) runPurge(async () => { await remove(`/api/drive/files/${history.file.id}/versions/${v.revision}`); setHistory({ ...history, versions: history.versions.filter(x => x.revision !== v.revision) }); }); }} />}
    {confirmNode}
  </section>;
}
