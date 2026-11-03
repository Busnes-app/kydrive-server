import { useCallback, useEffect, useState } from 'react';
import { Alert } from '../../components/Alert';
import { useConfirm } from '../../components/Dialog';
import { When } from '../../components/When';
import { bytes, change, record, response } from './client';

type Deleted = { id: string; name: string; deleted_at: string; deleted_by: string; used: number; items: number };
function deleted(v: unknown): v is Deleted {
  return record(v) && ['id', 'name', 'deleted_at', 'deleted_by'].every(k => typeof v[k] === 'string') && typeof v.used === 'number' && typeof v.items === 'number';
}

// purgeDate is when the hourly worker removes a workspace deleted at `at`; null when never.
export function purgeDate(at: string, days: number): string | null {
  if (days <= 0) return null;
  const t = Date.parse(at);
  return Number.isNaN(t) ? null : new Date(t + days * 86400000).toISOString();
}

// DeletedWorkspaces lists deleted shared workspaces for administrators (size and item count,
// never file names) and restores one. It renders nothing while there are none.
export function DeletedWorkspaces({ onRestored }: { onRestored: () => void }) {
  const [items, setItems] = useState<Deleted[]>([]);
  const [days, setDays] = useState(0);
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const [confirm, confirmNode] = useConfirm();
  const load = useCallback(async () => {
    const v = await response(await fetch('/api/drive/deleted-workspaces'));
    if (!record(v) || typeof v.purge_days !== 'number' || !Array.isArray(v.workspaces) || !v.workspaces.every(deleted)) throw new Error('Invalid server response');
    setItems(v.workspaces); setDays(v.purge_days);
  }, []);
  useEffect(() => { load().catch(e => setMessage(e instanceof Error ? e.message : 'Deleted workspaces unavailable')); }, [load]);
  async function restore(d: Deleted) {
    if (!await confirm({ title: `Restore ${d.name}?`, body: `${d.name} returns with its files, folders, versions and trash. Its group permissions and shares apply again; links, service tokens and AI agent tokens revoked by the delete stay revoked.`, confirm: 'Restore workspace' })) return;
    setBusy(true); setMessage('');
    try {
      await change(`/api/drive/workspaces/${encodeURIComponent(d.id)}/restore`, 'POST', {});
      await load();
      onRestored();
    } catch (e) { setMessage(e instanceof Error ? e.message : 'Restore failed'); } finally { setBusy(false); }
  }
  if (items.length === 0 && !message) return null;
  return <section className="panel" aria-labelledby="deleted-workspaces-heading">
    <h2 id="deleted-workspaces-heading">Deleted workspaces</h2>
    <p className="panel-desc">{days > 0 ? `Each is permanently deleted ${days} ${days === 1 ? 'day' : 'days'} after it was deleted (Server settings → Trash). Until then nobody can open it.` : 'Trash retention is off, so deleted workspaces are kept until restored. Nobody can open them meanwhile.'}</p>
    {message && <Alert>{message}</Alert>}
    {items.length > 0 && <ul className="item-list" aria-label="Deleted workspaces">{items.map(d => {
      const purge = purgeDate(d.deleted_at, days);
      return <li key={d.id}>
        <div className="item-main">
          <span className="item-title">{d.name}</span>
          <span className="item-meta">{bytes(d.used)} · {d.items} items · deleted <When value={d.deleted_at} relativeOnly /> by {d.deleted_by}{purge && <> · purged on <When value={purge} dateOnly /></>}</span>
        </div>
        <button className="btn-secondary" disabled={busy} onClick={() => void restore(d)}>Restore</button>
      </li>;
    })}</ul>}
    {confirmNode}
  </section>;
}
