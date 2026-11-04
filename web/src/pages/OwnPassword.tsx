import { useState } from 'react';
import { change } from './drive/client';
import { Alert } from '../components/Alert';

// OwnPassword lets a local account change its password after proving the current one. The
// server then ends every session and credential, so the page reloads to the sign-in screen.
export function OwnPassword({ local }: { local: boolean }) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  if (!local) return <section className="panel" aria-labelledby="own-password-heading"><h2 id="own-password-heading">Password</h2><p>Your password is managed in KyIdentity.</p></section>;
  async function submit() {
    setError('');
    if (next !== confirmation) { setError('The new passwords do not match.'); return; }
    if (current === next) { setError('Choose a different password.'); return; }
    setBusy(true);
    try { await change('/api/auth/change-password', 'POST', { current_password: current, new_password: next }); location.reload(); } catch (e) { setError(e instanceof Error ? e.message : 'Password change failed. Please try again.'); setBusy(false); }
  }
  return <section className="panel" aria-labelledby="own-password-heading">
    <h2 id="own-password-heading">Password</h2>
    <p>Changing your password signs you out everywhere and ends your app passwords, AI agent and service tokens and linked computers. Use at least 12 characters.</p>
    <form className="form" onSubmit={e => { e.preventDefault(); void submit(); }}>
      <div className="field"><label htmlFor="own-current-password">Current password</label>
        <input id="own-current-password" type="password" autoComplete="current-password" required maxLength={1024} value={current} onChange={e => setCurrent(e.target.value)} /></div>
      <div className="field"><label htmlFor="own-new-password">New password</label>
        <input id="own-new-password" type="password" autoComplete="new-password" required minLength={12} maxLength={1024} value={next} onChange={e => setNext(e.target.value)} /></div>
      <div className="field"><label htmlFor="own-confirm-password">Confirm new password</label>
        <input id="own-confirm-password" type="password" autoComplete="new-password" required minLength={12} maxLength={1024} value={confirmation} onChange={e => setConfirmation(e.target.value)} /></div>
      {error && <Alert>{error}</Alert>}
      <div><button type="submit" className="btn-secondary" disabled={busy}>{busy ? 'Changing password…' : 'Change password'}</button></div>
    </form>
  </section>;
}
