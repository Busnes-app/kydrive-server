import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Network } from 'lucide-react';
import { change, record, response } from './drive/client';
import { Alert } from '../components/Alert';

const networksURL = '/api/settings/rate-limit-networks';

export type ExemptStatus = { networks: string; environment: string; exempted: number };
function exemptStatus(v: unknown): v is ExemptStatus {
  return record(v) && typeof v.networks === 'string' && typeof v.environment === 'string' && typeof v.exempted === 'number';
}
const lines = (list: string) => list.split(',').filter(Boolean).join('\n');

// OfficeNetworks lists the networks whose sign-ins skip the per-address rate limit. One per line.
export function OfficeNetworks() {
  const [status, setStatus] = useState<ExemptStatus | null>(null);
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  const show = (s: ExemptStatus) => { setStatus(s); setText(lines(s.networks)); };

  useEffect(() => {
    let live = true;
    void (async () => {
      try {
        const v = await response(await fetch(networksURL));
        if (!exemptStatus(v)) throw new Error('Unexpected server response');
        if (live) show(v);
      } catch (e) { if (live) setError(e instanceof Error ? e.message : 'Could not load the networks'); }
    })();
    return () => { live = false; };
  }, []);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMessage(''); setError('');
    try {
      const v = await change(networksURL, 'PUT', { networks: text.split(/[\s,]+/).filter(Boolean).join(',') });
      if (!exemptStatus(v)) throw new Error('Unexpected server response');
      show(v);
      setMessage(v.networks ? 'Saved. These networks now skip the per-address limit.' : 'Saved. No network skips the limit.');
    } catch (err) { setError(err instanceof Error ? err.message : 'Could not save the networks'); } finally { setBusy(false); }
  };

  return <section className="panel" aria-labelledby="exempt-heading">
    <div className="panel-header"><div><Network size={20} aria-hidden="true" /><h2 id="exempt-heading" className="panel-title" style={{ margin: 0 }}>Office networks</h2></div></div>
    <p className="panel-desc">
      Sign-in is limited per network address. Staff in one office usually share one address and can trip that limit together. Networks
      listed here skip it; per-account limits and the security check still apply.
    </p>
    {!status ? (error ? <Alert>{error}</Alert> : <p className="hint">Loading…</p>) : <form onSubmit={save} className="form">
      <label className="field">
        Networks that skip the per-address limit, one per line
        <textarea className="font-mono" rows={4} value={text} onChange={e => setText(e.target.value)} placeholder="203.0.113.0/24" aria-describedby="exempt-hint" />
      </label>
      <p id="exempt-hint" className="hint">
        List only networks you control: everyone behind a listed address loses the limit, so never add guest Wi-Fi, a VPN provider or
        your reverse proxy. The widest range accepted is /16 (IPv4) or /48 (IPv6).
      </p>
      <div><button className="btn-secondary" disabled={busy}>Save networks</button></div>
      {message && <Alert tone="success">{message}</Alert>}
      {error && <Alert>{error}</Alert>}
    </form>}
    {status && <dl className="settings-facts" style={{ marginTop: '.75rem' }}>
      {status.environment && <><dt>Also set by the server environment</dt><dd className="font-mono">{status.environment.split(',').join(', ')}</dd></>}
      <dt>Requests that skipped the limit</dt><dd>{status.exempted} since the last change or restart</dd>
    </dl>}
  </section>;
}
