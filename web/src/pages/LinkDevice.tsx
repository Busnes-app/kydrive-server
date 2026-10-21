import { useEffect, useRef, useState } from 'react';
import { change, record } from './drive/client';
import { Alert } from '../components/Alert';

// The desktop app opens /link?redirect=&challenge=&state=&name=&platform=. The request moves to
// session storage at once, so it survives a KyIdentity sign-in, and waits for an explicit
// Approve; the server then answers with the loopback redirect carrying a one-time code.
const pending = 'ky-pending-device-link';
const platforms: Record<string, string> = { windows: 'Windows', macos: 'macOS', linux: 'Linux' };

type LinkRequest = { redirect: string; challenge: string; state: string; name: string; platform: string };

export function takeLinkRequestFromURL(): void {
  if (location.pathname.replace(/\/$/, '') !== '/link') return;
  sessionStorage.setItem(pending, location.search);
  history.replaceState(null, '', '/');
}

export function hasLinkRequest(): boolean { return sessionStorage.getItem(pending) !== null; }

// parse mirrors the server's checks, so a bad request shows an error instead of a button.
function parse(search: string): LinkRequest | null {
  const q = new URLSearchParams(search);
  const r = { redirect: q.get('redirect') ?? '', challenge: q.get('challenge') ?? '', state: q.get('state') ?? '', name: q.get('name') ?? '', platform: q.get('platform') ?? '' };
  const ok = /^http:\/\/(127\.0\.0\.1|\[::1\]):[1-9][0-9]{0,4}(\/[^?#]*)?$/.test(r.redirect) && /^[A-Za-z0-9_-]{43}$/.test(r.challenge)
    && r.state !== '' && r.state.length <= 256 && r.name.trim() === r.name && r.name !== '' && r.name.length <= 100 && Object.hasOwn(platforms, r.platform);
  return ok ? r : null;
}

export function LinkDevice({ onClose }: { onClose: () => void }) {
  const [request] = useState(() => parse(sessionStorage.getItem(pending) ?? ''));
  const [at] = useState(() => new Date());
  const [ip, setIp] = useState('');
  const [error, setError] = useState('');
  const [sent, setSent] = useState(false);
  const once = useRef(false);

  useEffect(() => {
    void fetch('/healthz').then(r => r.json()).then((v: unknown) => { if (record(v) && typeof v.clientIp === 'string') setIp(v.clientIp); }).catch(() => {});
  }, []);

  const cancel = () => { sessionStorage.removeItem(pending); onClose(); };
  const approve = async () => {
    if (!request || once.current) return;
    once.current = true; setSent(true);
    sessionStorage.removeItem(pending);
    try {
      const v = await change('/api/devices/approve', 'POST', request);
      if (!record(v) || typeof v.redirect !== 'string' || !v.redirect.startsWith(request.redirect + '?')) throw new Error('Unexpected server response.');
      location.assign(v.redirect);
    } catch (e) { setError(e instanceof Error ? e.message : 'Linking failed.'); }
  };

  return <main className="auth-center" style={{ minHeight: '100vh' }}>
    <section className="panel" style={{ width: '100%', maxWidth: '32rem' }} aria-labelledby="link-device-heading">
      <h2 id="link-device-heading">Link a computer</h2>
      {!request ? <>
        <Alert>This link request is not valid. Start linking again from the KyDrive app on your computer.</Alert>
        <div className="button-row"><button type="button" className="btn-secondary" onClick={cancel}>Close</button></div>
      </> : <>
        <p>Link <strong>{request.name}</strong> ({platforms[request.platform]}) to your KyDrive?</p>
        <dl className="settings-facts" style={{ margin: '1rem 0' }}>
          <dt>Request from</dt><dd className="font-mono">{ip || 'unknown'}</dd>
          <dt>Time</dt><dd>{at.toLocaleString()}</dd>
        </dl>
        <p className="alert alert-warn">The computer can open and change every file you can, until you revoke it on your Account page. Approve only if you just chose Link in the KyDrive app on this computer.</p>
        <div className="button-row">
          <button type="button" className="btn-secondary" onClick={cancel} disabled={sent && !error}>Cancel</button>
          <button type="button" onClick={() => void approve()} disabled={sent}>Approve</button>
        </div>
        {error && <div style={{ marginTop: '1rem' }}><Alert>{error} Start linking again from the KyDrive app.</Alert></div>}
      </>}
    </section>
  </main>;
}
