import { useCallback, useEffect, useState } from 'react';
import { secureFetch } from '../api';

type Workspace = { id: string; name: string; role: string; quota: number; used: number };
type DriveFile = { id: string; name: string; parent: string; revision: number; size: number; trashed: boolean };
type Folder = { id: string; name: string; parent: string };
type Grant = { group_id: string; name: string; role: string };
type Group = { id: string; display_name: string };
type Version = { revision: number; size: number; created: string };
function record(v: unknown): v is Record<string, unknown> { return typeof v === 'object' && v !== null && !Array.isArray(v); }
function workspace(v: unknown): v is Workspace { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.role === 'string' && typeof v.quota === 'number' && typeof v.used === 'number'; }
function file(v: unknown): v is DriveFile { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.parent === 'string' && typeof v.revision === 'number' && typeof v.size === 'number' && typeof v.trashed === 'boolean'; }
function folder(v: unknown): v is Folder { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.parent === 'string'; }
function grant(v: unknown): v is Grant { return record(v) && typeof v.group_id === 'string' && typeof v.name === 'string' && typeof v.role === 'string'; }
function group(v: unknown): v is Group { return record(v) && typeof v.id === 'string' && typeof v.display_name === 'string'; }
function version(v: unknown): v is Version { return record(v) && typeof v.revision === 'number' && typeof v.size === 'number' && typeof v.created === 'string'; }
async function response(r: Response): Promise<unknown> { const v: unknown = await r.json(); if (!r.ok) throw new Error(record(v) && typeof v.error === 'string' ? v.error : 'Drive request failed'); return v; }
async function list<T>(url: string, guard: (v: unknown) => v is T): Promise<T[]> { const v = await response(await fetch(url)); if (!Array.isArray(v) || !v.every(guard)) throw new Error('Invalid server response'); return v; }
async function change(url: string, method: string, body: unknown): Promise<unknown> { return response(await secureFetch(url, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })); }
const bytes = (n: number) => `${(n / 1048576).toFixed(1)} MiB`;

export function Drive({ admin }: { admin: boolean }) {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [selected, setSelected] = useState('');
  const [files, setFiles] = useState<DriveFile[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [parent, setParent] = useState('');
  const [trash, setTrash] = useState(false);
  const [grants, setGrants] = useState<Grant[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  const [identityURL, setIdentityURL] = useState('');
  const [newName, setNewName] = useState('');
  const [quota, setQuota] = useState(1024);
  const [newFolder, setNewFolder] = useState('');
  const [groupID, setGroupID] = useState('');
  const [role, setRole] = useState('reader');
  const [history, setHistory] = useState<{ file: DriveFile; versions: Version[] } | null>(null);
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const [operations, setOperations] = useState('');
  const [audit, setAudit] = useState<string[]>([]);
  const current = workspaces.find(w => w.id === selected);
  const canEdit = current?.role === 'editor' || current?.role === 'manager';
  const canManage = admin || current?.role === 'manager';
  const reload = useCallback(async () => {
    const ws = await list(`/api/drive/workspaces${admin ? '?admin=true' : ''}`, workspace); setWorkspaces(ws);
    if (!selected) { if (ws[0]) setSelected(ws[0].id); return; }
    const currentWS = ws.find(w => w.id === selected);
    if (currentWS?.role) {
      const [fs, ds] = await Promise.all([list(`/api/drive/workspaces/${selected}/files?trash=${trash}`, file), list(`/api/drive/workspaces/${selected}/folders`, folder)]); setFiles(fs); setFolders(ds);
    } else { setFiles([]); setFolders([]); }
    if (admin || currentWS?.role === 'manager') setGrants(await list(`/api/drive/workspaces/${selected}/grants`, grant)); else setGrants([]);
  }, [admin, selected, trash]);
  useEffect(() => { void reload().catch(e => setMessage(e instanceof Error ? e.message : 'Drive unavailable')); }, [reload]);
  useEffect(() => { if (!admin && current?.role !== 'manager') return; void (async () => { const v = await response(await fetch(admin ? '/api/drive/directory' : `/api/drive/workspaces/${selected}/directory`)); if (!record(v) || !Array.isArray(v.groups) || !v.groups.every(group)) throw new Error('Invalid directory'); setGroups(v.groups); if (typeof v.identity_url === 'string' && v.identity_url.startsWith('https://')) setIdentityURL(v.identity_url); })().catch(e => setMessage(e instanceof Error ? e.message : 'Directory unavailable')); }, [admin, selected, current?.role]);
  useEffect(() => { if (!admin) return; let active = true; const refresh = async () => { try { const v = await response(await fetch('/api/drive/status')); if (record(v) && record(v.editor_revocations) && typeof v.editor_revocations.pending === 'number' && active) setOperations(`Directory: ${v.scim_enabled ? 'SCIM enabled' : 'SCIM disabled'} · Editor: ${v.editor_configured ? 'configured' : 'not configured'} · Pending editor revocations: ${v.editor_revocations.pending} · Bulk backup: ${v.bulk_repository_configured ? 'configured' : 'not configured'}`); const events = await response(await fetch('/api/drive/audit')); if (Array.isArray(events) && active) setAudit(events.filter(record).filter(e => typeof e.action === 'string' && typeof e.created === 'string').slice(0,10).map(e => `${e.created} · ${e.action}`)); } catch { if (active) setOperations('Operational status unavailable; refresh before assuming access changes have reconciled.'); } }; void refresh(); const timer = setInterval(() => void refresh(),5000); return () => { active = false; clearInterval(timer); }; }, [admin]);
  async function run(action: () => Promise<unknown>) { setBusy(true); setMessage(''); try { await action(); await reload(); } catch (e) { setMessage(e instanceof Error ? e.message : 'Operation failed'); } finally { setBusy(false); } }
  return <section className="drive-page">
    <div className="drive-heading"><div><h1>KyDrive</h1><p>Organization files and shared workspaces</p></div>{identityURL && <a className="btn-secondary" href={identityURL} target="_blank" rel="noreferrer">Manage people &amp; groups in KyIdentity</a>}</div>
    {message && <p role="alert" className="panel">{message}</p>}
    {admin && <details className="panel"><summary>Operations &amp; audit</summary><p role="status">{operations}</p>{audit.map((e,i) => <p key={i}>{e}</p>)}</details>}
    <div className="drive-layout"><aside className="panel"><h2>Workspaces</h2>{workspaces.map(w => <button key={w.id} className={`ky-nav-item ${selected === w.id ? 'active' : ''}`} aria-current={selected === w.id ? 'page' : undefined} onClick={() => { setSelected(w.id); setParent(''); setHistory(null); }}>{w.name}<small>{w.role || 'Administration'}</small></button>)}{workspaces.length === 0 && <p>No workspaces assigned.</p>}
    {admin && <form onSubmit={e => { e.preventDefault(); void run(async () => { await change('/api/drive/workspaces', 'POST', { name: newName, quota: quota * 1048576 }); setNewName(''); }); }}><h3>Create workspace</h3><label>Name<input required value={newName} onChange={e => setNewName(e.target.value)} /></label><label>Quota (MiB)<input type="number" min="1" required value={quota} onChange={e => setQuota(e.target.valueAsNumber)} /></label><button disabled={busy}>Create workspace</button></form>}</aside>
    <div>{current ? <><div className="panel"><h2>{current.name}</h2><p>{bytes(current.used)} used of {bytes(current.quota)} · Versions and trash count toward quota.</p><progress max={current.quota} value={current.used} aria-label="Workspace storage" />
    {current.role ? <><div className="drive-toolbar"><label>Folder<select value={parent} onChange={e => setParent(e.target.value)}><option value="">Workspace root</option>{folders.map(f => <option key={f.id} value={f.id}>{f.name}</option>)}</select></label><label><input type="checkbox" checked={trash} onChange={e => setTrash(e.target.checked)} /> Show trash</label><button disabled={busy} onClick={() => void run(reload)}>Refresh</button></div>
    {canEdit && !trash && <div className="drive-toolbar"><label>Upload file<input type="file" disabled={busy} onChange={e => { const f = e.target.files?.[0]; if (f) void run(() => responseAsyncUpload(selected, parent, f)); e.target.value = ''; }} /></label><form onSubmit={e => { e.preventDefault(); void run(async () => { await change(`/api/drive/workspaces/${selected}/folders`, 'POST', { parent, name: newFolder }); setNewFolder(''); }); }}><label>New folder<input required value={newFolder} onChange={e => setNewFolder(e.target.value)} /></label><button disabled={busy}>Create folder</button></form></div>}
    <div className="drive-table"><table><thead><tr><th>Name</th><th>Size</th><th>Version</th><th>Actions</th></tr></thead><tbody>{files.filter(f => trash || f.parent === parent).map(f => <tr key={f.id}><td>{f.name}</td><td>{bytes(f.size)}</td><td>{f.revision}</td><td><div className="drive-actions">{!f.trashed && <><a href={`/api/drive/files/${f.id}/download`}>Download</a><a href={`/editor.html?file=${f.id}`} target="_blank" rel="noreferrer">Open editor</a></>}<button disabled={busy} onClick={() => void run(async () => setHistory({ file: f, versions: await list(`/api/drive/files/${f.id}/versions`, version) }))}>Versions</button>{canEdit && <button disabled={busy} onClick={() => void run(() => change(`/api/drive/files/${f.id}/trash`, 'PUT', { revision: f.revision, trashed: !f.trashed }))}>{f.trashed ? 'Restore from trash' : 'Move to trash'}</button>}</div></td></tr>)}</tbody></table></div>{files.length === 0 && <p>{trash ? 'Trash is empty.' : 'No files yet.'}</p>}</> : <p>Assign a group permission to make this workspace available. Administrators need a group grant to access its files.</p>}</div>
    {history && <div className="panel"><h3>Versions of {history.file.name}</h3>{history.versions.map(v => <p key={v.revision}>Version {v.revision} · {v.created} · {bytes(v.size)} {canEdit && !history.file.trashed && <button disabled={busy} onClick={() => void run(async () => { await change(`/api/drive/files/${history.file.id}/restore`, 'POST', { revision: v.revision, expected: history.file.revision }); setHistory(null); })}>Restore as new version</button>}</p>)}<button onClick={() => setHistory(null)}>Close versions</button></div>}
    {canManage && <div className="panel"><h3>Group permissions</h3><p>Membership comes from KyIdentity. KyDrive owns workspace access.</p>{grants.map(g => <p key={g.group_id}>{g.name} · {g.role} <button disabled={busy} onClick={() => void run(() => change(`/api/drive/workspaces/${selected}/grants/${g.group_id}`, 'PUT', { role: '' }))}>Remove access</button></p>)}{canManage && <form className="drive-toolbar" onSubmit={e => { e.preventDefault(); void run(() => change(`/api/drive/workspaces/${selected}/grants/${groupID}`, 'PUT', { role })); }}><label>Group<select required value={groupID} onChange={e => setGroupID(e.target.value)}><option value="">Choose group</option>{groups.map(g => <option key={g.id} value={g.id}>{g.display_name}</option>)}</select></label><label>Access<select value={role} onChange={e => setRole(e.target.value)}><option value="reader">Reader</option><option value="editor">Editor</option><option value="manager">Manager</option></select></label><button disabled={busy || !groupID}>Apply permission</button></form>}{admin && <form className="drive-toolbar" onSubmit={e => { e.preventDefault(); void run(() => change(`/api/drive/workspaces/${selected}/quota`, 'PUT', { quota: quota * 1048576 })); }}><label>Quota (MiB)<input type="number" min="1" required value={quota} onChange={e => setQuota(e.target.valueAsNumber)} /></label><button disabled={busy}>Set quota</button></form>}</div>}</> : <div className="panel"><h2>Your shared drive</h2><p>Create a workspace and assign a KyIdentity group to start.</p></div>}</div></div>
  </section>;
}
async function responseAsyncUpload(workspace: string, parent: string, f: File) { const q = new URLSearchParams({ name: f.name, parent, revision: '0' }); return response(await secureFetch(`/api/drive/workspaces/${workspace}/uploads?${q}`, { method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: f })); }
