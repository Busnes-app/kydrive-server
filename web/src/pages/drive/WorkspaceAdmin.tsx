import { useEffect, useState } from 'react';
import { Alert } from '../../components/Alert';
import { Dialog, useConfirm } from '../../components/Dialog';
import { change, HttpError, record, remove, response, type Workspace } from './client';

// keptFor says how long a deleted workspace waits before it is purged (drive-wide trash age).
function keptFor(days: number | null): string {
  if (days === null) return 'for the drive-wide trash period (Server settings → Trash)';
  if (days === 0) return 'until an administrator restores it: trash retention is off, so nothing purges it';
  return `for ${days} ${days === 1 ? 'day' : 'days'}`;
}

// WorkspaceAdmin lets an administrator rename or delete a shared workspace. An empty one is
// removed at once; one holding items goes to Deleted workspaces, after typing its name.
export function WorkspaceAdmin({ workspace, onRenamed, onDeleted }: { workspace: Workspace; onRenamed: () => void; onDeleted: () => void }) {
  const [name, setName] = useState(workspace.name);
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const [confirm, confirmNode] = useConfirm();
  const [asking, setAsking] = useState<{ days: number | null } | null>(null);
  const [typed, setTyped] = useState('');
  useEffect(() => { setName(workspace.name); setMessage(''); }, [workspace.id, workspace.name]);
  const wanted = name.trim();
  const empty = workspace.items === 0;
  async function act(action: () => Promise<unknown>, conflict: (server: string) => string) {
    setBusy(true); setMessage('');
    try { await action(); return true; } catch (e) { setMessage(e instanceof HttpError && e.status === 409 ? conflict(e.message) : e instanceof Error ? e.message : 'Operation failed'); return false; } finally { setBusy(false); }
  }
  async function rename() {
    if (await act(() => change(`/api/drive/workspaces/${workspace.id}/name`, 'PUT', { name: wanted }), server => server.includes('Deleted workspaces') ? `A deleted workspace named “${wanted}” is waiting in Deleted workspaces. Restore and rename it, or pick another name.` : `A shared workspace named “${wanted}” already exists.`)) onRenamed();
  }
  async function destroy() {
    if (!empty) {
      let days: number | null = null;
      try { const v = await response(await fetch('/api/drive/trash-retention')); if (record(v) && typeof v.trash_days === 'number') days = v.trash_days; } catch { /* the copy falls back to naming the setting */ }
      setTyped(''); setAsking({ days });
      return;
    }
    if (!await confirm({ title: `Delete ${workspace.name}?`, body: `${workspace.name} is empty, so it is removed for good at once. Its group permissions, shares, links, service tokens and AI agent tokens limited to it stop working. Only a backup can bring it back.`, confirm: 'Delete workspace', danger: true })) return;
    if (await act(() => remove(`/api/drive/workspaces/${workspace.id}`), () => `${workspace.name} gained files or has an upload in progress. Reload and try again.`)) onDeleted();
  }
  async function deleteRecoverably() {
    if (typed !== workspace.name) return;
    setAsking(null);
    if (await act(() => remove(`/api/drive/workspaces/${workspace.id}`, { confirm: typed }), () => 'The name did not match. Nothing was deleted.')) onDeleted();
  }
  return <section className="panel" aria-labelledby="workspace-admin-heading">
    <h2 id="workspace-admin-heading">Workspace</h2>
    {message && <Alert>{message}</Alert>}
    <form className="form-row" onSubmit={e => { e.preventDefault(); void rename(); }}>
      <label className="field grow">Name<input required value={name} onChange={e => setName(e.target.value)} /></label>
      <button className="btn-secondary" disabled={busy || !wanted || wanted === workspace.name}>Rename</button>
    </form>
    <div className="panel-section">
      <h3>Delete workspace</h3>
      <p>Everyone loses access at once. An empty workspace is removed for good; one with files or folders moves to Deleted workspaces, where an administrator can restore it until the trash period ends.</p>
      <button className="btn-danger-quiet" disabled={busy} onClick={() => void destroy()}>Delete workspace</button>
    </div>
    {confirmNode}
    {asking && <Dialog title={`Delete ${workspace.name}?`} closeButton={false} onClose={() => setAsking(null)} onSubmit={() => void deleteRecoverably()}
      footer={<><button type="button" className="btn-secondary" onClick={() => setAsking(null)}>Cancel</button><button type="submit" className="btn-danger" disabled={typed !== workspace.name}>Delete workspace</button></>}>
      <p>{workspace.name} leaves everyone’s sidebar now. Its members, people it is shared with and linked computers lose access at once.</p>
      <p><strong>What happens to the files:</strong> its {workspace.items === undefined ? '' : `${workspace.items} `}files and folders, their versions and its trash are kept {keptFor(asking.days)}. Until then an administrator can restore it from Workspaces → Deleted workspaces; after that they are permanently deleted.</p>
      <p>Its links, service tokens and AI agent tokens limited to it stop working for good, even after a restore. Backups taken earlier keep it until their own retention ends.</p>
      <label className="field">Type <strong>{workspace.name}</strong> to confirm<input autoFocus autoComplete="off" spellCheck={false} value={typed} onChange={e => setTyped(e.target.value)} /></label>
    </Dialog>}
  </section>;
}
