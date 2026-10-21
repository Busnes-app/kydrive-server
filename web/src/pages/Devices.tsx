import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Laptop } from 'lucide-react';
import { change, record, remove, response } from './drive/client';
import { Alert } from '../components/Alert';
import { Dialog } from '../components/Dialog';
import { When } from '../components/When';

type Device = { id: string; name: string; platform: string; created: string; last_used: number };
type Policy = { wipe_default: boolean; wipe_locked: boolean };
function device(v: unknown): v is Device { return record(v) && ['id', 'name', 'platform', 'created'].every(k => typeof v[k] === 'string') && typeof v.last_used === 'number'; }
function policy(v: unknown): v is Policy { return record(v) && typeof v.wipe_default === 'boolean' && typeof v.wipe_locked === 'boolean'; }
const policyURL = '/api/drive/device-policy';
const platforms: Record<string, string> = { windows: 'Windows', macos: 'macOS', linux: 'Linux' };

async function loadPolicy(): Promise<Policy> {
  const v = await response(await fetch(policyURL));
  if (!policy(v)) throw new Error('Unexpected server response.');
  return v;
}

// Devices lists the computers linked with the KyDrive desktop app and revokes them.
export function Devices() {
  const [list, setList] = useState<Device[]>([]);
  const [rules, setRules] = useState<Policy | null>(null);
  const [revoking, setRevoking] = useState<Device | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function run(action: () => Promise<void>) {
    setError(''); setBusy(true);
    try { await action(); } catch (e) { setError(e instanceof Error ? e.message : 'Request failed. Please try again.'); } finally { setBusy(false); }
  }
  const load = async () => {
    const v = await response(await fetch('/api/devices'));
    if (!Array.isArray(v) || !v.every(device)) throw new Error('Unexpected server response.');
    setList(v);
  };
  useEffect(() => { void run(async () => { setRules(await loadPolicy()); await load(); }); }, []);

  const revoke = (d: Device, wipe: boolean) => void run(async () => {
    await remove(`/api/devices/${encodeURIComponent(d.id)}?wipe=${wipe}`);
    setRevoking(null);
    await load();
  });

  return <section className="panel" aria-labelledby="devices-heading">
    <h2 id="devices-heading">Computers</h2>
    <p>Computers linked with the KyDrive desktop app keep your files in sync. The desktop app is in development; once you have it, link a computer from the app and approve it here in the browser.</p>
    {list.length > 0 ? <ul className="item-list" aria-label="Your computers">{list.map(d => <li key={d.id}>
      <div className="item-main"><span className="item-title"><Laptop size={16} aria-hidden="true" />{d.name}</span>
        <span className="item-meta">{platforms[d.platform] ?? d.platform} · linked <When value={d.created} dateOnly /> · {d.last_used ? <>last used <When value={d.last_used} relativeOnly /></> : 'never used'}</span></div>
      <div className="item-actions"><button type="button" className="btn-danger-quiet btn-sm" disabled={busy || !rules} onClick={() => setRevoking(d)}>Revoke</button></div>
    </li>)}</ul> : <p className="empty-note">No computers linked.</p>}
    {revoking && rules && <RevokeDialog device={revoking} rules={rules} busy={busy} onCancel={() => setRevoking(null)} onRevoke={wipe => revoke(revoking, wipe)} />}
    {error && <div style={{ marginTop: '1rem' }}><Alert>{error}</Alert></div>}
  </section>;
}

function RevokeDialog({ device, rules, busy, onCancel, onRevoke }: { device: Device; rules: Policy; busy: boolean; onCancel: () => void; onRevoke: (wipe: boolean) => void }) {
  const [wipe, setWipe] = useState(rules.wipe_default);
  return <Dialog title={`Revoke ${device.name}?`} description="It stops syncing at once." onClose={onCancel} onSubmit={() => onRevoke(rules.wipe_locked ? rules.wipe_default : wipe)}
    footer={<><button type="button" className="btn-secondary" onClick={onCancel}>Cancel</button><button type="submit" className="btn-danger" disabled={busy}>Revoke computer</button></>}>
    <label className="check">
      <input type="checkbox" checked={rules.wipe_locked ? rules.wipe_default : wipe} disabled={rules.wipe_locked} onChange={e => setWipe(e.target.checked)} /><span>Also remove KyDrive files from this computer{rules.wipe_locked && <small>Your administrator has set this.</small>}</span>
    </label>
    <p>Files are removed the next time the computer reaches KyDrive. One that stays offline for 30 days is never told, and removal is not guaranteed. Edits that never uploaded are kept in an Unsynced folder.</p>
  </Dialog>;
}

// DevicePolicySettings is the admin's drive-wide wipe default for revoked computers, and
// whether users may change it.
export function DevicePolicySettings() {
  const [rules, setRules] = useState<Policy | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { void loadPolicy().then(setRules, (e: unknown) => setError(e instanceof Error ? e.message : 'Could not load the computer settings')); }, []);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!rules) return;
    setBusy(true); setError('');
    try {
      const v = await change(policyURL, 'PUT', rules);
      if (!policy(v)) throw new Error('Unexpected server response');
      setRules(v);
    } catch (err) { setError(err instanceof Error ? err.message : 'Could not save the computer settings'); } finally { setBusy(false); }
  };

  return <section className="panel" aria-labelledby="computers-settings-heading">
    <div className="panel-header"><div><Laptop size={20} aria-hidden="true" /><h2 id="computers-settings-heading" className="panel-title" style={{ margin: 0 }}>Computers</h2></div></div>
    <p className="panel-desc">When someone revokes a computer linked with the desktop app, KyDrive can tell it to remove its synced files. A computer offline for 30 days is never told.</p>
    {!rules ? (error ? <Alert>{error}</Alert> : <p className="hint">Loading…</p>) : <form onSubmit={save} className="form">
      <label className="check"><input type="checkbox" checked={rules.wipe_default} onChange={e => setRules({ ...rules, wipe_default: e.target.checked })} /> Remove files when a computer is revoked (default)</label>
      <label className="check"><input type="checkbox" checked={rules.wipe_locked} onChange={e => setRules({ ...rules, wipe_locked: e.target.checked })} /> Users cannot change this</label>
      <div><button className="btn-secondary" disabled={busy}>Save computers</button></div>
      {error && <Alert>{error}</Alert>}
    </form>}
  </section>;
}
