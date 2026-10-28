import { useEffect, useState } from 'react';
import { Alert } from '../../components/Alert';
import { useConfirm } from '../../components/Dialog';
import { change, HttpError, remove, type Workspace } from './client';

// WorkspaceAdmin lets an administrator rename a shared workspace or delete an empty one.
export function WorkspaceAdmin({ workspace, onRenamed, onDeleted }: { workspace: Workspace; onRenamed: () => void; onDeleted: () => void }) {
  const [name, setName] = useState(workspace.name);
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const [confirm, confirmNode] = useConfirm();
  useEffect(() => { setName(workspace.name); setMessage(''); }, [workspace.id, workspace.name]);
  const wanted = name.trim();
  async function act(action: () => Promise<unknown>, conflict: string) {
    setBusy(true); setMessage('');
    try { await action(); return true; } catch (e) { setMessage(e instanceof HttpError && e.status === 409 ? conflict : e instanceof Error ? e.message : 'Operation failed'); return false; } finally { setBusy(false); }
  }
  async function rename() {
    if (await act(() => change(`/api/drive/workspaces/${workspace.id}/name`, 'PUT', { name: wanted }), `A shared workspace named “${wanted}” already exists.`)) onRenamed();
  }
  async function destroy() {
    if (!await confirm({ title: `Delete ${workspace.name}?`, body: 'The workspace is removed for everyone. Its group permissions, shares, links, service tokens and AI agent tokens limited to it stop working at once. Only a backup can bring it back.', confirm: 'Delete workspace', danger: true })) return;
    if (await act(() => remove(`/api/drive/workspaces/${workspace.id}`), `${workspace.name} is not empty. Only an empty workspace can be deleted: delete or move its files and folders, empty its trash and let uploads finish.`)) onDeleted();
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
      <p>Only an empty workspace can be deleted: no files or folders, nothing in its trash and no upload in progress.</p>
      <button className="btn-danger-quiet" disabled={busy} onClick={() => void destroy()}>Delete workspace</button>
    </div>
    {confirmNode}
  </section>;
}
