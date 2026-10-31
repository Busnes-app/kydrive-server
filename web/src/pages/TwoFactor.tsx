import { useState } from 'react';
import type { FormEvent } from 'react';
import { secureFetch } from '../api';
import { Alert } from '../components/Alert';

interface TwoFactorProps {
  enabled: boolean;
  // Directory accounts sign in through their identity provider, which owns their MFA.
  local: boolean;
  onChange: (enabled: boolean) => void;
}

async function post(path: string, body?: object): Promise<Record<string, unknown>> {
  const response = await secureFetch(path, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body ?? {}),
  });
  const data: unknown = await response.json().catch(() => ({}));
  const record = typeof data === 'object' && data !== null ? data as Record<string, unknown> : {};
  if (!response.ok) throw new Error(typeof record.error === 'string' ? record.error : 'Request failed. Please try again.');
  return record;
}

export function TwoFactor({ enabled, local, onChange }: TwoFactorProps) {
  const [enrolment, setEnrolment] = useState<{ secret: string; uri: string } | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function run(action: () => Promise<void>) {
    setError(''); setBusy(true);
    try { await action(); } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Request failed. Please try again.');
    } finally { setBusy(false); }
  }

  const start = () => run(async () => {
    const data = await post('/api/auth/mfa/enroll');
    if (typeof data.secret !== 'string' || typeof data.otpauth_uri !== 'string') throw new Error('Unexpected server response.');
    setEnrolment({ secret: data.secret, uri: data.otpauth_uri });
  });

  const confirm = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); void run(async () => {
    const data = await post('/api/auth/mfa/enroll/confirm', { code: code.trim() });
    const codes = Array.isArray(data.recovery_codes) ? data.recovery_codes.filter((c): c is string => typeof c === 'string') : [];
    setRecoveryCodes(codes); setEnrolment(null); setCode('');
    onChange(true);
  }); };

  const disable = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); void run(async () => {
    await post('/api/auth/mfa/disable', { password, code: code.trim() });
    setPassword(''); setCode('');
    onChange(false);
  }); };

  let body;
  if (!local) {
    body = <p>Your identity provider manages two-factor authentication for this account.</p>;
  } else if (recoveryCodes.length > 0) {
    body = <>
      <div className="reveal" role="status">
        <p><strong>Save these recovery codes now.</strong> Each one signs you in once if you lose your authenticator. They will not be shown again.</p>
        <ol className="font-mono secret" style={{ listStylePosition: 'inside', columns: 2 }}>{recoveryCodes.map((c) => <li key={c}>{c}</li>)}</ol>
        <div><button type="button" onClick={() => setRecoveryCodes([])}>I have saved them</button></div>
      </div>
    </>;
  } else if (enabled) {
    body = <form className="form" onSubmit={disable}>
      <p><span className="ky-status" data-ky-status="success">On</span> Signing in needs your password and a code from your authenticator app.</p>
      <div className="field"><label htmlFor="mfa-password">Password</label>
      <input id="mfa-password" type="password" autoComplete="current-password" required maxLength={1024} value={password} onChange={(e) => setPassword(e.target.value)} /></div>
      <div className="field"><label htmlFor="mfa-code">Authenticator or recovery code</label>
      <input id="mfa-code" autoComplete="one-time-code" required value={code} onChange={(e) => setCode(e.target.value)} /></div>
      <div><button type="submit" className="btn-danger-quiet" disabled={busy}>Turn off two-factor authentication</button></div>
    </form>;
  } else if (enrolment) {
    body = <form className="form" onSubmit={confirm}>
      <p>Add this account to your authenticator app, then enter the 6-digit code it shows.</p>
      <div className="field"><span className="field-label">Setup key</span><code className="secret font-mono">{enrolment.secret}</code></div>
      <p><a href={enrolment.uri}>Open in authenticator app</a></p>
      <div className="field"><label htmlFor="mfa-code">Code</label>
      <input id="mfa-code" className="otp-input" inputMode="numeric" autoComplete="one-time-code" required value={code} onChange={(e) => setCode(e.target.value)} /></div>
      <div className="button-row" style={{ marginTop: 0 }}>
        <button type="submit" disabled={busy}>Confirm</button>
        <button type="button" className="btn-secondary" disabled={busy} onClick={() => { setEnrolment(null); setCode(''); }}>Cancel</button>
      </div>
    </form>;
  } else {
    body = <>
      <p className="panel-desc"><span className="ky-status">Off</span> Turn it on to require a code from an authenticator app when you sign in.</p>
      <button type="button" disabled={busy} onClick={() => void start()}>Turn on two-factor authentication</button>
    </>;
  }

  return (
    <section className="panel" aria-labelledby="mfa-heading">
      <h2 id="mfa-heading">Two-factor authentication</h2>
      {!local && <p className="panel-desc" style={{ marginBottom: 0 }}>Your identity provider manages two-factor authentication for this account.</p>}
      {local && body}
      {error && <div style={{ marginTop: '1rem' }}><Alert>{error}</Alert></div>}
    </section>
  );
}
