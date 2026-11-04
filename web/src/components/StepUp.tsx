import { useCallback, useState } from 'react';
import type { ReactNode } from 'react';
import { change, HttpError } from '../pages/drive/client';
import { Alert } from './Alert';
import { Dialog } from './Dialog';

type Ask = { method: 'password' | 'sso'; resolve: (ok: boolean) => void; beforeLeave?: () => void };

// useStepUp runs a request that mints a credential outliving the session (WebDAV password, agent
// token, linked computer). When the server answers step_up_required it asks the user to confirm
// it's them and retries once: by password in place, or through KyIdentity, which leaves the page
// and returns to it. beforeLeave saves what the return needs to finish the action.
export function useStepUp(): [<T>(action: () => Promise<T>, beforeLeave?: () => void) => Promise<T>, ReactNode] {
  const [ask, setAsk] = useState<Ask | null>(null);
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const guarded = useCallback(async <T,>(action: () => Promise<T>, beforeLeave?: () => void): Promise<T> => {
    try { return await action(); } catch (e) {
      if (!(e instanceof HttpError) || e.code !== 'step_up_required') throw e;
      const method = e.method === 'sso' ? 'sso' : 'password';
      if (!await new Promise<boolean>(resolve => setAsk({ method, resolve, beforeLeave }))) throw new Error('Nothing was created because you did not confirm it was you.');
      return action();
    }
  }, []);
  const done = (ok: boolean) => { ask?.resolve(ok); setAsk(null); setPassword(''); setError(''); };
  const confirm = async () => {
    setBusy(true); setError('');
    try { await change('/api/auth/step-up', 'POST', { password }); done(true); } catch (e) { setError(e instanceof Error ? e.message : 'Could not confirm. Please try again.'); } finally { setBusy(false); }
  };
  const toKyIdentity = () => {
    ask?.beforeLeave?.();
    location.assign(`/api/sso/kysignon/login?step_up=1&return=${encodeURIComponent(location.pathname + location.search + location.hash)}`);
  };
  const node = ask && (ask.method === 'password'
    ? <Dialog title="Confirm it's you" description="Enter your password to continue. You will not be asked again for five minutes." onClose={() => done(false)} onSubmit={() => void confirm()}
      footer={<><button type="button" className="btn-secondary" onClick={() => done(false)}>Cancel</button><button type="submit" disabled={busy || !password}>Confirm</button></>}>
      <div className="field"><label htmlFor="step-up-password">Password</label>
        <input id="step-up-password" type="password" autoComplete="current-password" autoFocus value={password} onChange={e => setPassword(e.target.value)} /></div>
      {error && <Alert>{error}</Alert>}
    </Dialog>
    : <Dialog title="Confirm it's you" onClose={() => done(false)}
      footer={<><button type="button" className="btn-secondary" onClick={() => done(false)}>Cancel</button><button type="button" autoFocus onClick={toKyIdentity}>Continue to KyIdentity</button></>}>
      <p>KyIdentity will ask you to sign in again, then bring you back here to finish.</p>
    </Dialog>);
  return [guarded, node];
}
