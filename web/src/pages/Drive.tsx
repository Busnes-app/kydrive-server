import { useCallback, useEffect, useRef, useState } from 'react';
import { secureFetch } from '../api';
import { bytes, breadcrumb, change, file, folder, grant, group, list, record, remove, response, version, workspace } from './drive/client';
import { MoveDialog } from './drive/MoveDialog';
import type { Item, Mode } from './drive/MoveDialog';
import type { DriveFile, Folder, Grant, Group, Version, Workspace } from './drive/client';

export type { Workspace } from './drive/client';


type DocumentKind = 'document' | 'spreadsheet' | 'presentation' | 'markdown' | 'rtf' | 'whiteboard';

function editorURL(f: DriveFile): string {
  if (f.name.toLowerCase().endsWith('.excalidraw')) {
    return `/whiteboard.html?file=${encodeURIComponent(f.id)}`;
  }
  return `/editor.html?file=${encodeURIComponent(f.id)}`;
}

export function Drive({ admin, workspaces, onWorkspacesChange, selected, onSelectWorkspace, openInNewTab }: { admin: boolean; workspaces: Workspace[]; onWorkspacesChange: (workspaces: Workspace[]) => void; selected: string; onSelectWorkspace: (id: string) => void; openInNewTab: boolean }) {
  const fileTarget = openInNewTab ? { target: '_blank', rel: 'noreferrer' } : {};
  const documentDialog = useRef<HTMLDialogElement>(null);
  const [newDocument, setNewDocument] = useState<{ kind: DocumentKind; name: string } | null>(null);
  const uploadInput = useRef<HTMLInputElement>(null);
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
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const [operations, setOperations] = useState('');
  const [audit, setAudit] = useState<string[]>([]);
  const current = workspaces.find(w => w.id === selected);
  const visibleFolders = trash ? folders : folders.filter(d => d.parent === parent);
  const canEdit = current?.role === 'editor' || current?.role === 'manager';
  const canPurge = current?.role === 'manager';
  const canManage = current?.kind === 'shared' && (admin || current.role === 'manager');
  const reload = useCallback(async () => {
    await change('/api/drive/personal-workspace', 'POST', {});
    const listWorkspaces = await list(`/api/drive/workspaces${admin ? '?admin=true' : ''}`, workspace);
    onWorkspacesChange(listWorkspaces);
    if (!selected) {
      const personal = listWorkspaces.find(w => w.kind === 'personal') || listWorkspaces[0];
      if (personal) onSelectWorkspace(personal.id);
      return;
    }
    const currentWS = listWorkspaces.find(w => w.id === selected);
    if (currentWS?.role) {
      const [fs, ds] = await Promise.all([list(`/api/drive/workspaces/${selected}/files?trash=${trash}`, file), list(`/api/drive/workspaces/${selected}/folders?trash=${trash}`, folder)]); setFiles(fs); setFolders(ds);
    } else { setFiles([]); setFolders([]); }
    if (currentWS?.kind === 'shared' && (admin || currentWS.role === 'manager')) setGrants(await list(`/api/drive/workspaces/${selected}/grants`, grant)); else setGrants([]);
  }, [admin, selected, trash, onWorkspacesChange, onSelectWorkspace]);
  useEffect(() => { setTrashDays(current?.trash_days ?? 0); setKeepVersions(current?.keep_versions ?? 0); }, [current?.id, current?.trash_days, current?.keep_versions]);
  useEffect(() => { setParent(''); setHistory(null); setView('files'); }, [selected]);
  useEffect(() => { void reload().catch(e => setMessage(e instanceof Error ? e.message : 'Drive unavailable')); }, [reload]);
  useEffect(() => { if (!canManage) return; void (async () => { const v = await response(await fetch(admin ? '/api/drive/directory' : `/api/drive/workspaces/${selected}/directory`)); if (!record(v) || !Array.isArray(v.groups) || !v.groups.every(group)) throw new Error('Invalid directory'); setGroups(v.groups); if (typeof v.identity_url === 'string' && v.identity_url.startsWith('https://')) setIdentityURL(v.identity_url); })().catch(e => setMessage(e instanceof Error ? e.message : 'Directory unavailable')); }, [admin, selected, canManage]);
  useEffect(() => { if (!admin) return; let active = true; const refresh = async () => { try { const v = await response(await fetch('/api/drive/status')); if (record(v) && record(v.editor_revocations) && typeof v.editor_revocations.pending === 'number' && active) setOperations(`Directory: ${v.scim_enabled ? 'SCIM enabled' : 'SCIM disabled'} · Editor: ${v.editor_configured ? 'configured' : 'not configured'} · Pending editor revocations: ${v.editor_revocations.pending} · Bulk backup: ${v.bulk_repository_configured ? 'configured' : 'not configured'}`); const events = await response(await fetch('/api/drive/audit')); if (Array.isArray(events) && active) setAudit(events.filter(record).filter(e => typeof e.action === 'string' && typeof e.created === 'string').slice(0,10).map(e => `${e.created} · ${e.action}`)); } catch { if (active) setOperations('Operational status unavailable; refresh before assuming access changes have reconciled.'); } }; void refresh(); const timer = setInterval(() => void refresh(),5000); return () => { active = false; clearInterval(timer); }; }, [admin]);
  useEffect(() => { const dialog = documentDialog.current; if (newDocument && dialog && !dialog.open) dialog.showModal(); return () => { if (dialog?.open) dialog.close(); }; }, [newDocument?.kind]);
  function startDocument(kind: DocumentKind) {
    const extension = {document: 'docx', spreadsheet: 'xlsx', presentation: 'pptx', markdown: 'md', rtf: 'rtf', whiteboard: 'excalidraw'}[kind];
    const base = `Untitled ${kind}`;
    let name = base;
    let suffix = 2;
    const names = new Set(files.filter(f => !f.trashed && f.parent === parent).map(f => f.name.toLowerCase()));
    while (names.has(`${name}.${extension}`.toLowerCase())) name = `${base} (${suffix++})`;
    setNewDocument({kind, name});
  }
  async function run(action: () => Promise<unknown>) { setBusy(true); setMessage(''); try { await action(); await reload(); } catch (e) { setMessage(e instanceof Error ? e.message : 'Operation failed'); } finally { setBusy(false); } }
  // Purges can fail partway; reload either way so the list shows what is really left.
  function runPurge(action: () => Promise<unknown>) { void run(async () => { try { await action(); } catch (e) { await reload().catch(() => {}); throw e; } }); }
  function purge(url: string, label: string) { if (window.confirm(`Delete ${label} permanently? This cannot be undone.`)) runPurge(() => remove(url)); }
  function emptyTrash() {
    if (!window.confirm('Empty the trash? Everything in it is deleted permanently.')) return;
    const trashedFolders = new Set(folders.map(d => d.id));
    runPurge(async () => {
      for (const d of folders.filter(d => !trashedFolders.has(d.parent))) await remove(`/api/drive/folders/${d.id}`);
      for (const f of files.filter(f => !trashedFolders.has(f.parent))) await remove(`/api/drive/files/${f.id}`);
    });
  }
  return <section className="drive-page">
    <div className="drive-heading"><div><h1>{current?.name || 'Workspaces'}</h1><p>{current?.kind === 'personal' ? 'Private files for your account' : 'Shared files for your team'}</p></div>{identityURL && <a className="btn-secondary" href={identityURL} target="_blank" rel="noreferrer">Manage people &amp; groups in KyIdentity</a>}</div>
    {newDocument && <dialog className="drive-document-dialog" ref={documentDialog} aria-labelledby="new-document-title" onCancel={() => setNewDocument(null)}><form onSubmit={e => { e.preventDefault();
      // Open the tab inside the click so pop-up blockers allow it; point it at the editor once the file exists.
      const tab = openInNewTab ? window.open('about:blank', '_blank') : null;
      if (tab) tab.opener = null;
      void run(async () => {
        try {
          const created = await change(`/api/drive/workspaces/${selected}/documents`, 'POST', {name: newDocument.name, kind: newDocument.kind, parent}); if (!file(created)) throw new Error('Invalid document response'); const target = editorURL(created); setNewDocument(null);
          if (tab) tab.location.href = target; else window.location.assign(target);
        } catch (err) { tab?.close(); throw err; }
      }); }}><h2 id="new-document-title">New {newDocument.kind}</h2><p>Create in {current?.name}</p><label>File name<input autoFocus required value={newDocument.name} onChange={e => setNewDocument({...newDocument,name:e.target.value})} /></label>{message && <p role="alert">{message}</p>}<div className="drive-dialog-actions"><button type="button" className="btn-secondary" onClick={() => setNewDocument(null)}>Cancel</button><button disabled={busy || !newDocument.name.trim()}>Create &amp; open</button></div></form></dialog>}
    {message && <p role="alert" className="panel">{message}</p>}
    {admin && <details className="panel"><summary>Operations &amp; audit</summary><p role="status">{operations}</p>{audit.map((e,i) => <p key={i}>{e}</p>)}</details>}
    {admin && <details className="panel drive-create-workspace"><summary>Create shared workspace</summary><form onSubmit={e => { e.preventDefault(); void run(async () => { await change('/api/drive/workspaces', 'POST', { name: newName, quota: quota * 1048576 }); setNewName(''); }); }}><h3>Create shared workspace</h3><label>Name<input required value={newName} onChange={e => setNewName(e.target.value)} /></label><label>Quota (MiB)<input type="number" min="1" required value={quota} onChange={e => setQuota(e.target.valueAsNumber)} /></label><button disabled={busy}>Create workspace</button></form></details>}
    <div className="drive-layout">
    <div className="drive-content">{current ? <><div className="panel drive-files-panel">
      <div className="drive-workspace-heading"><div><p className="drive-storage">{bytes(current.used)} of {bytes(current.quota)} used</p></div>{canManage && <div className="drive-view-switch" aria-label="Workspace views"><button className={view === 'files' ? 'btn-secondary active' : 'btn-secondary'} aria-pressed={view === 'files'} onClick={() => setView('files')}>Files</button><button className={view === 'settings' ? 'btn-secondary active' : 'btn-secondary'} aria-pressed={view === 'settings'} onClick={() => setView('settings')}>Workspace settings</button></div>}</div>
      {view === 'files' && (current.role ? <>
        <div className="drive-toolbar">
          <nav className="drive-breadcrumb" aria-label="Folder path">
            <button className="btn-link" onClick={() => setParent('')} aria-current={parent === '' ? 'page' : undefined}>{current.name}</button>
            {breadcrumb(folders, parent).map(f => <span key={f.id}> / <button className="btn-link" onClick={() => setParent(f.id)} aria-current={f.id === parent ? 'page' : undefined}>{f.name}</button></span>)}
          </nav>
          <div className="drive-toolbar-actions">
            {canEdit && !trash && <details className="drive-new-file"><summary className="btn">New file</summary><div className="drive-new-file-options">{((['document','spreadsheet','presentation','markdown','rtf','whiteboard']) satisfies DocumentKind[]).map(kind => <button className="btn-secondary" key={kind} onClick={e => { e.currentTarget.closest('details')?.removeAttribute('open'); startDocument(kind); }}>New {kind}</button>)}</div></details>}
            {trash && canPurge && <button className="btn-secondary" disabled={busy} onClick={emptyTrash}>Empty trash</button>}
            <button className="btn-secondary" aria-pressed={trash} onClick={() => { setFiles([]); setFolders([]); setTrash(!trash); }}>{trash ? 'Back to files' : 'Trash'}</button>
            <button className="btn-secondary" disabled={busy} onClick={() => void run(reload)}>Refresh</button>
            {canEdit && !trash && <><input ref={uploadInput} type="file" hidden aria-label="Choose file to upload" disabled={busy} onChange={e => { const f = e.target.files?.[0]; if (f) void run(() => responseAsyncUpload(selected, parent, f)); e.target.value = ''; }} /><button className="btn-secondary" disabled={busy} onClick={() => uploadInput.current?.click()}>Upload file</button>
              <details className="drive-new-folder"><summary className="btn-secondary">New folder</summary><form className="drive-folder-form" onSubmit={e => { e.preventDefault(); void run(async () => { await change(`/api/drive/workspaces/${selected}/folders`, 'POST', { parent, name: newFolder }); setNewFolder(''); }); }}><label>Folder name<input required value={newFolder} onChange={e => setNewFolder(e.target.value)} /></label><button disabled={busy}>Create</button></form></details></>}
          </div>
        </div>
        <div className="drive-table"><table><thead><tr><th>Name</th><th>Size</th><th>Version</th><th><span className="drive-actions-heading">Actions</span></th></tr></thead><tbody>{visibleFolders.map(d => <tr key={`folder-${d.id}`}><td>{trash ? d.name : <button className="drive-folder-name btn-link" onClick={() => setParent(d.id)}>{d.name}/</button>}</td><td>Folder</td><td>—</td><td>{trash ? <div className="drive-file-actions">{canEdit && <button className="btn-secondary" disabled={busy} onClick={() => void run(() => change(`/api/drive/folders/${d.id}/trash`, 'PUT', { trashed: false }))}>Restore</button>}{canPurge && <button className="btn-secondary" disabled={busy} onClick={() => purge(`/api/drive/folders/${d.id}`, d.name)}>Delete permanently</button>}</div> : canEdit && <details className="drive-file-menu"><summary className="btn-secondary" aria-label={`Actions for folder ${d.name}`}>Actions</summary><div className="drive-file-actions"><button className="btn-secondary" disabled={busy} onClick={() => setDialog({ mode: 'rename', item: { kind: 'folder', folder: d } })}>Rename</button><button className="btn-secondary" disabled={busy} onClick={() => setDialog({ mode: 'move', item: { kind: 'folder', folder: d } })}>Move</button><button className="btn-secondary" disabled={busy} onClick={() => void run(() => change(`/api/drive/folders/${d.id}/trash`, 'PUT', { trashed: true }))}>Move to trash</button></div></details>}</td></tr>)}{files.filter(f => trash || f.parent === parent).map(f => <tr key={f.id}><td>{!f.trashed ? <a className="drive-file-name" href={editorURL(f)} {...fileTarget}>{f.name}</a> : f.name}</td><td>{bytes(f.size)}</td><td>{f.revision}</td><td><details className="drive-file-menu"><summary className="btn-secondary" aria-label={`Actions for ${f.name}`}>Actions</summary><div className="drive-file-actions">{!f.trashed && <><a href={editorURL(f)} {...fileTarget}>Open editor</a><a href={`/api/drive/files/${f.id}/download`}>Download</a></>}{!f.trashed && <>{canEdit && <><button className="btn-secondary" disabled={busy} onClick={() => setDialog({ mode: 'rename', item: { kind: 'file', file: f } })}>Rename</button><button className="btn-secondary" disabled={busy} onClick={() => setDialog({ mode: 'move', item: { kind: 'file', file: f } })}>Move</button></>}<button className="btn-secondary" disabled={busy} onClick={() => setDialog({ mode: 'copy', item: { kind: 'file', file: f } })}>Copy</button></>}<button className="btn-secondary" disabled={busy} onClick={() => void run(async () => setHistory({ file: f, versions: await list(`/api/drive/files/${f.id}/versions`, version) }))}>Version history</button>{canEdit && <button className="btn-secondary" disabled={busy} onClick={() => void run(() => change(`/api/drive/files/${f.id}/trash`, 'PUT', { revision: f.revision, trashed: !f.trashed }))}>{f.trashed ? 'Restore' : 'Move to trash'}</button>}{canPurge && f.trashed && <button className="btn-secondary" disabled={busy} onClick={() => purge(`/api/drive/files/${f.id}`, f.name)}>Delete permanently</button>}</div></details></td></tr>)}</tbody></table></div>
        {files.filter(f => trash || f.parent === parent).length + visibleFolders.length === 0 && <div className="drive-empty"><h3>{trash ? 'Trash is empty' : 'No files here yet'}</h3><p>{trash ? 'Deleted items appear here until restored or deleted permanently.' : canEdit ? 'Create a document or upload a file to get started.' : 'Files shared with this workspace will appear here.'}</p></div>}
      </> : <p className="drive-empty">Assign a group permission in Workspace settings to access files.</p>)}
      </div>
    {dialog && current && <MoveDialog {...dialog} workspaceID={current.id} workspaces={workspaces} onClose={() => setDialog(null)} onDone={() => { setDialog(null); void run(reload); }} />}
    {history && <div className="panel"><h3>Versions of {history.file.name}</h3>{history.versions.map(v => <p key={v.revision}>Version {v.revision} · {v.created} · {bytes(v.size)} {canEdit && !history.file.trashed && <button disabled={busy} onClick={() => void run(async () => { await change(`/api/drive/files/${history.file.id}/restore`, 'POST', { revision: v.revision, expected: history.file.revision }); setHistory(null); })}>Restore as new version</button>}{canPurge && v.revision !== history.file.revision && <button disabled={busy} onClick={() => { if (window.confirm(`Delete version ${v.revision} permanently?`)) runPurge(async () => { await remove(`/api/drive/files/${history.file.id}/versions/${v.revision}`); setHistory({ ...history, versions: history.versions.filter(x => x.revision !== v.revision) }); }); }}>Delete version</button>}</p>)}<button onClick={() => setHistory(null)}>Close versions</button></div>}
    {canManage && view === 'settings' && <div className="panel drive-settings"><h3>Group permissions</h3><p>Choose who can access this workspace. Manage group members in KyIdentity.</p>{grants.map(g => <p className="drive-grant" key={g.group_id}><span>{g.name} · {g.role}</span><button className="btn-secondary" disabled={busy} onClick={() => void run(() => change(`/api/drive/workspaces/${selected}/grants/${g.group_id}`, 'PUT', { role: '' }))}>Remove access</button></p>)}{canManage && <form className="drive-toolbar" onSubmit={e => { e.preventDefault(); void run(() => change(`/api/drive/workspaces/${selected}/grants/${groupID}`, 'PUT', { role })); }}><label>Group<select required value={groupID} onChange={e => setGroupID(e.target.value)}><option value="">Choose group</option>{groups.map(g => <option key={g.id} value={g.id}>{g.display_name}</option>)}</select></label><label>Access<select value={role} onChange={e => setRole(e.target.value)}><option value="reader">Reader</option><option value="editor">Editor</option><option value="manager">Manager</option></select></label><button disabled={busy || !groupID}>Apply permission</button></form>}{admin && <form className="drive-toolbar" onSubmit={e => { e.preventDefault(); void run(() => change(`/api/drive/workspaces/${selected}/quota`, 'PUT', { quota: quota * 1048576 })); }}><label>Quota (MiB)<input type="number" min="1" required value={quota} onChange={e => setQuota(e.target.valueAsNumber)} /></label><button disabled={busy}>Set quota</button></form>}{admin && <form className="drive-toolbar" onSubmit={e => { e.preventDefault(); void run(() => change(`/api/drive/workspaces/${selected}/retention`, 'PUT', { trash_days: trashDays, keep_versions: keepVersions })); }}>
  <h3>Retention</h3><p>0 keeps everything. Deleted items cannot be recovered from the drive, only from backups.</p>
  <label>Empty trash after (days)<input type="number" min="0" max="3650" required value={trashDays} onChange={e => setTrashDays(e.target.valueAsNumber)} /></label>
  <label>Keep newest versions<input type="number" min="0" max="1000" required value={keepVersions} onChange={e => setKeepVersions(e.target.valueAsNumber)} /></label>
  <button disabled={busy}>Save retention</button>
</form>}</div>}</> : <div className="panel"><h2>Your shared drive</h2><p>Create a workspace and assign a KyIdentity group to start.</p></div>}</div></div>
  </section>;
}
async function responseAsyncUpload(workspace: string, parent: string, f: File) { const q = new URLSearchParams({ name: f.name, parent, revision: '0' }); return response(await secureFetch(`/api/drive/workspaces/${workspace}/uploads?${q}`, { method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: f })); }
