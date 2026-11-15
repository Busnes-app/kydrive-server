import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Laptop, Smartphone } from 'lucide-react';
import QRCode from 'qrcode';
import { change, record, remove, response } from './drive/client';
import { Alert } from '../components/Alert';
import { Dialog } from '../components/Dialog';
import { When } from '../components/When';
import { useStepUp } from '../components/StepUp';

type Device = { id: string; name: string; platform: string; created: string; last_used: number };
type Policy = { wipe_default: boolean; wipe_locked: boolean };
function device(v: unknown): v is Device { return record(v) && ['id', 'name', 'platform', 'created'].every(k => typeof v[k] === 'string') && typeof v.last_used === 'number'; }
function policy(v: unknown): v is Policy { return record(v) && typeof v.wipe_default === 'boolean' && typeof v.wipe_locked === 'boolean'; }
const policyURL = '/api/drive/device-policy';
const platforms: Record<string, string> = { windows: 'Windows', macos: 'macOS', linux: 'Linux', android: 'Android', ios: 'iOS' };
const isPhone = (d: Device) => d.platform === 'android' || d.platform === 'ios';
const kind = (d: Device) => isPhone(d) ? 'phone' : 'computer';

async function loadPolicy(): Promise<Policy> {
  const v = await response(await fetch(policyURL));
  if (!policy(v)) throw new Error('Unexpected server response.');
  return v;
}

// Devices lists the computers and phones linked with KyDrive apps, links a phone and revokes them.
export function Devices() {
  const [list, setList] = useState<Device[]>([]);
  const [rules, setRules] = useState<Policy | null>(null);
  const [revoking, setRevoking] = useState<Device | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [linking, setLinking] = useState(false);

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
    <h2 id="devices-heading">Devices</h2>
    <p>Computers linked with the KyDrive desktop app keep your files in sync; link one from the app and approve it here. Phones with KyDrive for Android link by scanning a code shown here.</p>
    <div className="button-row"><button type="button" className="btn-secondary" onClick={() => setLinking(true)}><Smartphone size={16} aria-hidden="true" />Link a phone</button></div>
    {list.length > 0 ? <ul className="item-list" aria-label="Your devices">{list.map(d => <li key={d.id}>
      <div className="item-main"><span className="item-title">{isPhone(d) ? <Smartphone size={16} aria-hidden="true" /> : <Laptop size={16} aria-hidden="true" />}{d.name}</span>
        <span className="item-meta">{platforms[d.platform] ?? d.platform} · linked <When value={d.created} dateOnly /> · {d.last_used ? <>last used <When value={d.last_used} relativeOnly /></> : 'never used'}</span></div>
      <div className="item-actions"><button type="button" className="btn-danger-quiet btn-sm" disabled={busy || !rules} onClick={() => setRevoking(d)}>Revoke</button></div>
    </li>)}</ul> : <p className="empty-note">No devices linked.</p>}
    {linking && <LinkPhoneDialog known={list.map(d => d.id)} onClose={() => { setLinking(false); void run(load); }} onRevoke={d => { setLinking(false); setRevoking(d); }} />}
    {revoking && rules && <RevokeDialog device={revoking} rules={rules} busy={busy} onCancel={() => setRevoking(null)} onRevoke={wipe => revoke(revoking, wipe)} />}
    {error && <div style={{ marginTop: '1rem' }}><Alert>{error}</Alert></div>}
  </section>;
}

function RevokeDialog({ device, rules, busy, onCancel, onRevoke }: { device: Device; rules: Policy; busy: boolean; onCancel: () => void; onRevoke: (wipe: boolean) => void }) {
  const [wipe, setWipe] = useState(rules.wipe_default);
  return <Dialog title={`Revoke ${device.name}?`} description="It loses access at once." onClose={onCancel} onSubmit={() => onRevoke(rules.wipe_locked ? rules.wipe_default : wipe)}
    footer={<><button type="button" className="btn-secondary" onClick={onCancel}>Cancel</button><button type="submit" className="btn-danger" disabled={busy}>Revoke {kind(device)}</button></>}>
    <label className="check">
      <input type="checkbox" checked={rules.wipe_locked ? rules.wipe_default : wipe} disabled={rules.wipe_locked} onChange={e => setWipe(e.target.checked)} /><span>Also remove KyDrive files from this {kind(device)}{rules.wipe_locked && <small>Your administrator has set this.</small>}</span>
    </label>
    <p>Files are removed the next time the {kind(device)} reaches KyDrive. One that stays offline for 30 days is never told, and removal is not guaranteed.{isPhone(device) ? '' : ' Edits that never uploaded are kept in an Unsynced folder.'}</p>
  </Dialog>;
}

// LinkPhoneDialog shows a one-time QR code for KyDrive for Android to scan. The code works once,
// for two minutes; the dialog watches the device list and names the phone that used it, so a
// phone the user does not recognise can be revoked at once.
function LinkPhoneDialog({ known, onClose, onRevoke }: { known: string[]; onClose: () => void; onRevoke: (d: Device) => void }) {
  const [stepUp, stepUpNode] = useStepUp();
  const [qr, setQr] = useState('');
  const [left, setLeft] = useState(0);
  const [linked, setLinked] = useState<Device | null>(null);
  const [error, setError] = useState('');
  const asked = useRef(false);
  const [before] = useState(known);

  const fresh = async () => {
    setError(''); setQr(''); setLinked(null);
    try {
      const v = await stepUp(() => change('/api/devices/phone-code', 'POST', {}));
      if (!record(v) || typeof v.link !== 'string' || !v.link.startsWith('kydrive://link?') || typeof v.expires_in !== 'number') throw new Error('Unexpected server response.');
      setQr('data:image/svg+xml;charset=utf-8,' + encodeURIComponent(await QRCode.toString(v.link, { type: 'svg', margin: 2, errorCorrectionLevel: 'M' })));
      setLeft(v.expires_in);
    } catch (e) { setError(e instanceof Error ? e.message : 'Could not make a code.'); }
  };
  useEffect(() => { if (!asked.current) { asked.current = true; void fresh(); } }, []);

  // While the code is live, count down and look for the phone that used it.
  const waiting = qr !== '' && left > 0 && !linked;
  useEffect(() => {
    if (!waiting) return;
    const tick = setInterval(() => setLeft(n => n - 1), 1000);
    const poll = setInterval(() => void fetch('/api/devices').then(response).then(v => {
      const d = Array.isArray(v) ? v.find(x => device(x) && isPhone(x) && !before.includes(x.id)) : undefined;
      if (d) setLinked(d);
    }).catch(() => {}), 2000);
    return () => { clearInterval(tick); clearInterval(poll); };
  }, [waiting, before]);

  return <Dialog title="Link a phone" description="Open KyDrive for Android and scan this code. It works once, for two minutes." onClose={onClose}
    footer={linked ? <><button type="button" className="btn-danger-quiet" onClick={() => onRevoke(linked)}>Revoke</button><button type="button" onClick={onClose}>Done</button></>
      : <><button type="button" className="btn-secondary" onClick={onClose}>Cancel</button>{qr && left <= 0 && <button type="button" onClick={() => void fresh()}>New code</button>}</>}>
    {linked ? <p role="status"><strong>{linked.name}</strong> ({platforms[linked.platform]}) is linked. If you don't recognise it, revoke it now.</p>
      : qr && left > 0 ? <>
        <img src={qr} alt="QR code to link a phone" width={240} height={240} style={{ display: 'block', margin: '0 auto', background: '#fff' }} />
        <p className="hint" role="timer" aria-live="off">Expires in {left} s. Anyone who scans it can open your files, so show it only to your own phone.</p>
      </> : qr ? <p role="status">This code has expired.</p> : !error && <p className="hint">Making a code…</p>}
    {error && <Alert>{error}</Alert>}
    {stepUpNode}
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
