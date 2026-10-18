import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Laptop } from 'lucide-react';
import { change, record, remove, response } from './drive/client';

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

  return <section className="panel" style={{ maxWidth: 560 }} aria-labelledby="devices-heading">
    <h2 id="devices-heading">Computers</h2>
    <p>Computers linked with the KyDrive desktop app keep your files in sync. Link one from the app; you approve it here in the browser.</p>
    {list.length > 0 ? <ul aria-label="Your computers">{list.map(d => <li key={d.id}>
      <span><strong>{d.name}</strong> · {platforms[d.platform] ?? d.platform} · linked {d.created.slice(0, 10)} · {d.last_used ? `last used ${new Date(d.last_used * 1000).toLocaleString()}` : 'never used'}</span>
      <button type="button" className="btn-secondary" disabled={busy || !rules} onClick={() => setRevoking(d)}>Revoke</button>
    </li>)}</ul> : <p>No computers linked.</p>}
    {revoking && rules && <RevokeDialog device={revoking} rules={rules} busy={busy} onCancel={() => setRevoking(null)} onRevoke={wipe => revoke(revoking, wipe)} />}
    {error && <p role="alert">{error}</p>}
  </section>;
}

function RevokeDialog({ device, rules, busy, onCancel, onRevoke }: { device: Device; rules: Policy; busy: boolean; onCancel: () => void; onRevoke: (wipe: boolean) => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [wipe, setWipe] = useState(rules.wipe_default);
  // jsdom lacks showModal; opening by attribute keeps the form reachable in unit tests.
  useEffect(() => { const d = ref.current; if (d && !d.open) { if (typeof d.showModal === 'function') d.showModal(); else d.setAttribute('open', ''); } }, []);
  const submit = (e: FormEvent) => { e.preventDefault(); onRevoke(rules.wipe_locked ? rules.wipe_default : wipe); };
  return <dialog ref={ref} className="drive-document-dialog" onCancel={onCancel} aria-labelledby="revoke-device-heading">
    <form onSubmit={submit}>
      <h2 id="revoke-device-heading">Revoke {device.name}?</h2>
      <p>It stops syncing at once.</p>
      <label className="drive-setting-toggle">
        <input type="checkbox" checked={rules.wipe_locked ? rules.wipe_default : wipe} disabled={rules.wipe_locked} onChange={e => setWipe(e.target.checked)} /> Also remove KyDrive files from this computer
      </label>
      {rules.wipe_locked && <p>Your administrator has set this.</p>}
      <p>Files are removed the next time the computer reaches KyDrive. One that stays offline for 30 days is never told, and removal is not guaranteed. Edits that never uploaded are kept in an Unsynced folder.</p>
      <div className="drive-dialog-actions">
        <button type="button" className="btn-secondary" onClick={onCancel}>Cancel</button>
        <button type="submit" disabled={busy}>Revoke computer</button>
      </div>
    </form>
  </dialog>;
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

  return <div className="panel">
    <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '12px' }}>
      <Laptop size={20} style={{ color: 'var(--accent)' }} />
      <h3 style={{ fontSize: '16px' }}>Computers</h3>
    </div>
    <p style={{ color: 'var(--ink)', fontSize: '13px', marginBottom: '16px' }}>
      When someone revokes a computer linked with the desktop app, KyDrive can tell it to remove its synced files. A computer offline for 30 days is never told.
    </p>
    {!rules ? <p>{error || 'Loading…'}</p> : <form onSubmit={save} style={{ display: 'grid', gap: '12px', maxWidth: 420 }}>
      <label className="drive-setting-toggle"><input type="checkbox" checked={rules.wipe_default} onChange={e => setRules({ ...rules, wipe_default: e.target.checked })} /> Remove files when a computer is revoked (default)</label>
      <label className="drive-setting-toggle"><input type="checkbox" checked={rules.wipe_locked} onChange={e => setRules({ ...rules, wipe_locked: e.target.checked })} /> Users cannot change this</label>
      <button disabled={busy}>Save computers</button>
      {error && <p role="alert">{error}</p>}
    </form>}
  </div>;
}
