import { useState } from 'react';
import type { FormEvent } from 'react';
import { secureFetch } from '../api';

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
      <p role="status"><strong>Save these recovery codes now.</strong> Each one signs you in once if you lose your authenticator. They will not be shown again.</p>
      <ol className="font-mono">{recoveryCodes.map((c) => <li key={c}>{c}</li>)}</ol>
      <button type="button" onClick={() => setRecoveryCodes([])}>I have saved them</button>
    </>;
  } else if (enabled) {
    body = <form onSubmit={disable}>
      <p>Two-factor authentication is on. Signing in needs your password and a code from your authenticator app.</p>
      <label htmlFor="mfa-password">Password</label>
      <input id="mfa-password" type="password" autoComplete="current-password" required maxLength={1024} value={password} onChange={(e) => setPassword(e.target.value)} />
      <label htmlFor="mfa-code">Authenticator or recovery code</label>
      <input id="mfa-code" autoComplete="one-time-code" required value={code} onChange={(e) => setCode(e.target.value)} />
      <button type="submit" className="btn-secondary" disabled={busy}>Turn off two-factor authentication</button>
    </form>;
  } else if (enrolment) {
    body = <form onSubmit={confirm}>
      <p>Add this account to your authenticator app, then enter the 6-digit code it shows.</p>
      <p>Setup key: <code className="font-mono" style={{ userSelect: 'all', wordBreak: 'break-all' }}>{enrolment.secret}</code></p>
      <p><a href={enrolment.uri}>Open in authenticator app</a></p>
      <label htmlFor="mfa-code">Code</label>
      <input id="mfa-code" inputMode="numeric" autoComplete="one-time-code" required value={code} onChange={(e) => setCode(e.target.value)} />
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, marginTop: 12 }}>
        <button type="submit" disabled={busy}>Confirm</button>
        <button type="button" className="btn-secondary" disabled={busy} onClick={() => { setEnrolment(null); setCode(''); }}>Cancel</button>
      </div>
    </form>;
  } else {
    body = <>
      <p>Two-factor authentication is off. Turn it on to require a code from an authenticator app when you sign in.</p>
      <button type="button" disabled={busy} onClick={() => void start()}>Turn on two-factor authentication</button>
    </>;
  }

  return (
    <section className="panel" style={{ maxWidth: 560 }} aria-labelledby="mfa-heading">
      <h2 id="mfa-heading">Two-factor authentication</h2>
      {body}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
