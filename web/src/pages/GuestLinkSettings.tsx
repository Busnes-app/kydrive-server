import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Link2 } from 'lucide-react';
import { change, record, response } from './drive/client';
import { Alert } from '../components/Alert';

const policyURL = '/api/drive/guest-links-policy';

export type GuestPolicy = { enabled: boolean; max_days: number };
export function guestPolicy(v: unknown): v is GuestPolicy { return record(v) && typeof v.enabled === 'boolean' && typeof v.max_days === 'number'; }

// GuestLinkSettings turns guest links (for people without an account) on or off for the whole
// drive and sets how long one may last. Off by default.
export function GuestLinkSettings() {
  const [policy, setPolicy] = useState<GuestPolicy | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void (async () => {
      try {
        const v = await response(await fetch(policyURL));
        if (!guestPolicy(v)) throw new Error('Unexpected server response');
        setPolicy(v);
      } catch (e) { setError(e instanceof Error ? e.message : 'Could not load the guest link setting'); }
    })();
  }, []);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!policy) return;
    setBusy(true); setError('');
    try {
      const v = await change(policyURL, 'PUT', policy);
      if (!guestPolicy(v)) throw new Error('Unexpected server response');
      setPolicy(v);
    } catch (err) { setError(err instanceof Error ? err.message : 'Could not save the guest link setting'); } finally { setBusy(false); }
  };

  return <section className="panel" aria-labelledby="guest-settings-heading">
    <div className="panel-header"><div><Link2 size={20} aria-hidden="true" /><h2 id="guest-settings-heading" className="panel-title" style={{ margin: 0 }}>Guest links</h2></div><span className="ky-status" data-ky-status={policy?.enabled ? 'success' : undefined}>{policy?.enabled ? 'On' : 'Off'}</span></div>
    <p className="panel-desc">
      Let workspace managers share a file or folder with people who have no account: view and download, or upload-only into a folder. Every guest link expires and can have a password. Turning this off stops every existing guest link at once.
    </p>
    {!policy ? (error ? <Alert>{error}</Alert> : <p className="hint">Loading…</p>) : <form onSubmit={save} className="form">
      <label className="check"><input type="checkbox" checked={policy.enabled} onChange={e => setPolicy({ ...policy, enabled: e.target.checked })} /> Allow guest links</label>
      <label className="field" style={{ maxWidth: '18rem' }}>Longest a guest link may last (days)<input type="number" min={1} max={365} required value={policy.max_days} onChange={e => setPolicy({ ...policy, max_days: e.target.valueAsNumber })} /></label>
      <div><button className="btn-secondary" disabled={busy}>Save guest links</button></div>
      {error && <Alert>{error}</Alert>}
    </form>}
  </section>;
}
