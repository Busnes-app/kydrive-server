import { useCallback, useEffect, useState } from 'react';
import { Plus } from 'lucide-react';
import { Alert } from '../components/Alert';
import { Dialog } from '../components/Dialog';
import { When } from '../components/When';
import { HttpError, bytes, change, list, record, response, workspace } from './drive/client';
import type { Workspace } from './drive/client';
import { Leavers } from './drive/Leavers';
import { auditLabel } from './auditLabels';

// Administration destinations in the sidebar: shared workspaces, operational status and leavers.

export function AdminWorkspaces({ workspaces, onWorkspacesChange, onOpen }: { workspaces: Workspace[]; onWorkspacesChange: (w: Workspace[]) => void; onOpen: (id: string) => void }) {
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [quota, setQuota] = useState(1024);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const reload = useCallback(async () => onWorkspacesChange(await list('/api/drive/workspaces?admin=true', workspace)), [onWorkspacesChange]);
  useEffect(() => { reload().catch(e => setMessage(e instanceof Error ? e.message : 'Workspaces unavailable')); }, [reload]);
  const shared = workspaces.filter(w => w.kind === 'shared');
  async function create() {
    setBusy(true); setMessage('');
    try {
      await change('/api/drive/workspaces', 'POST', { name, quota: quota * 1048576 });
      setCreating(false); setName('');
      await reload();
    } catch (err) {
      setMessage(err instanceof HttpError && err.status === 409 ? `A shared workspace named “${name.trim()}” already exists.` : err instanceof Error ? err.message : 'Could not create the workspace');
    } finally { setBusy(false); }
  }
  return <section className="page">
    <header className="ky-page-head">
      <div>
        <h1 className="ky-page-title">Workspaces</h1>
        <p className="ky-page-lede">Shared workspaces on this drive. Open one to set its group permissions, quota and retention.</p>
      </div>
      <button className="ky-page-action" onClick={() => { setMessage(''); setCreating(true); }}><Plus aria-hidden="true" />New workspace</button>
    </header>
    {message && !creating && <Alert>{message}</Alert>}
    {shared.length === 0 ? <p className="empty-note">No shared workspaces yet.</p> : <div className="panel drive-files-panel"><div className="drive-table"><table>
      <thead><tr><th>Name</th><th className="num">Used</th><th className="num">Quota</th></tr></thead>
      <tbody>{shared.map(w => <tr key={w.id}>
        <td><button className="drive-file-name btn-link" onClick={() => onOpen(w.id)}>{w.name}</button></td>
        <td className="num">{bytes(w.used)}</td><td className="num">{bytes(w.quota)}</td>
      </tr>)}</tbody>
    </table></div></div>}
    {creating && <Dialog closeButton={false} title="New shared workspace" description="Give a KyIdentity group access from its Workspace settings once it exists." onClose={() => setCreating(false)} onSubmit={() => void create()}
      footer={<><button type="button" className="btn-secondary" onClick={() => setCreating(false)}>Cancel</button><button disabled={busy || !name.trim()}>Create workspace</button></>}>
      <label className="field">Name<input autoFocus required value={name} onChange={e => setName(e.target.value)} /></label>
      <label className="field">Quota (MiB)<input type="number" min="1" required value={quota} onChange={e => setQuota(e.target.valueAsNumber)} /></label>
      {message && <Alert>{message}</Alert>}
    </Dialog>}
  </section>;
}

type AuditEvent = { action: string; created: string };
type Status = { scim: boolean; editor: boolean; pending: number; bulk: boolean };

export function AdminActivity() {
  const [status, setStatus] = useState<Status | null>(null);
  const [failed, setFailed] = useState(false);
  const [audit, setAudit] = useState<AuditEvent[]>([]);
  useEffect(() => {
    let active = true;
    const refresh = async () => {
      try {
        const v = await response(await fetch('/api/drive/status'));
        if (record(v) && record(v.editor_revocations) && typeof v.editor_revocations.pending === 'number' && active) {
          setStatus({ scim: v.scim_enabled === true, editor: v.editor_configured === true, pending: v.editor_revocations.pending, bulk: v.bulk_repository_configured === true });
          setFailed(false);
        }
        const events = await response(await fetch('/api/drive/audit'));
        if (Array.isArray(events) && active) setAudit(events.filter(record).filter((e): e is AuditEvent => typeof e.action === 'string' && typeof e.created === 'string').slice(0, 10));
      } catch { if (active) setFailed(true); }
    };
    void refresh();
    const timer = setInterval(() => void refresh(), 5000);
    return () => { active = false; clearInterval(timer); };
  }, []);
  return <section className="page">
    <header className="ky-page-head">
      <div>
        <h1 className="ky-page-title">Activity</h1>
        <p className="ky-page-lede">Directory, editor and backup status, and the latest audit events. Refreshes every 5 seconds.</p>
      </div>
    </header>
    <div className="stack">
      <section className="panel" aria-labelledby="status-heading">
        <h2 id="status-heading">Status</h2>
        {failed && <Alert>Operational status unavailable; refresh before assuming access changes have reconciled.</Alert>}
        {status ? <dl className="settings-facts" role="status">
          <dt>Directory</dt><dd>{status.scim ? 'SCIM enabled' : 'SCIM disabled'}</dd>
          <dt>Editor</dt><dd>{status.editor ? 'Configured' : 'Not configured'}</dd>
          <dt>Pending editor revocations</dt><dd>{status.pending}</dd>
          <dt>Bulk backup</dt><dd>{status.bulk ? 'Configured' : 'Not configured'}</dd>
        </dl> : !failed && <p className="hint">Loading…</p>}
      </section>
      <section className="panel" aria-labelledby="audit-heading">
        <h2 id="audit-heading">Recent audit events</h2>
        {audit.length === 0 ? <p className="empty-note">No audit events yet.</p> : <ul className="drive-audit" aria-label="Recent audit events">{audit.map((e, i) => <li key={i}><When value={e.created} /><span title={e.action}>{auditLabel(e.action)}</span></li>)}</ul>}
      </section>
    </div>
  </section>;
}

export function AdminLeavers({ workspaces }: { workspaces: Workspace[] }) {
  return <section className="page">
    <header className="ky-page-head">
      <div>
        <h1 className="ky-page-title">Leavers</h1>
        <p className="ky-page-lede">Move a removed account's My files to a shared workspace or a colleague. File names stay hidden here.</p>
      </div>
    </header>
    <Leavers workspaces={workspaces} onDone={() => {}} />
  </section>;
}
