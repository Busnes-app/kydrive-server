import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Link2 } from 'lucide-react';
import { change, record, response } from './drive/client';

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

  return <div className="panel">
    <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '12px' }}>
      <Link2 size={20} style={{ color: 'var(--accent)' }} />
      <h3 style={{ fontSize: '16px' }}>Guest links</h3>
    </div>
    <p style={{ color: 'var(--ink)', fontSize: '13px', marginBottom: '16px' }}>
      Let workspace managers share a file or folder with people who have no account: view and download, or upload-only into a folder. Every guest link expires and can have a password. Turning this off stops every existing guest link at once.
    </p>
    {!policy ? <p>{error || 'Loading…'}</p> : <form onSubmit={save} style={{ display: 'grid', gap: '12px', maxWidth: 420 }}>
      <label className="drive-setting-toggle"><input type="checkbox" checked={policy.enabled} onChange={e => setPolicy({ ...policy, enabled: e.target.checked })} /> Allow guest links</label>
      <label style={{ display: 'grid', gap: '4px', fontSize: '13px' }}>Longest a guest link may last (days)<input type="number" min={1} max={365} required value={policy.max_days} onChange={e => setPolicy({ ...policy, max_days: e.target.valueAsNumber })} /></label>
      <button disabled={busy}>Save guest links</button>
      {error && <p role="alert">{error}</p>}
    </form>}
  </div>;
}
