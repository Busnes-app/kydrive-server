import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { secureFetch } from '../api';
import { change, record, response } from './drive/client';

type AppPassword = { id: string; name: string; created: string; last_used: number };
type Created = { name: string; password: string; username: string; url: string };
function appPassword(v: unknown): v is AppPassword { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.created === 'string' && typeof v.last_used === 'number'; }
function created(v: unknown): v is Created { return record(v) && ['name', 'password', 'username', 'url'].every(k => typeof v[k] === 'string'); }

// AppPasswords lets a user connect WebDAV clients (file managers, LibreOffice) with passwords
// that work only for WebDAV and can be revoked one by one.
export function AppPasswords() {
  const [list, setList] = useState<AppPassword[]>([]);
  const [name, setName] = useState('');
  const [shown, setShown] = useState<Created | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function run(action: () => Promise<void>) {
    setError(''); setBusy(true);
    try { await action(); } catch (e) { setError(e instanceof Error ? e.message : 'Request failed. Please try again.'); } finally { setBusy(false); }
  }
  const load = async () => {
    const v = await response(await fetch('/api/auth/app-passwords'));
    if (!Array.isArray(v) || !v.every(appPassword)) throw new Error('Unexpected server response.');
    setList(v);
  };
  useEffect(() => { void run(load); }, []);

  const create = (e: FormEvent) => { e.preventDefault(); void run(async () => {
    const v = await change('/api/auth/app-passwords', 'POST', { name: name.trim() });
    if (!created(v)) throw new Error('Unexpected server response.');
    setShown(v); setName(''); await load();
  }); };
  const revoke = (p: AppPassword) => { if (window.confirm(`Revoke "${p.name}"? Devices using it stop syncing.`)) void run(async () => {
    const r = await secureFetch(`/api/auth/app-passwords/${p.id}`, { method: 'DELETE' });
    if (!r.ok) await response(r);
    await load();
  }); };

  return <section className="panel" style={{ maxWidth: 560 }} aria-labelledby="app-passwords-heading">
    <h2 id="app-passwords-heading">App passwords</h2>
    <p>Connect a file manager or office app over WebDAV. Each app password works only for WebDAV and can be revoked on its own.</p>
    {shown && <div role="status">
      <p><strong>Copy this password now. It will not be shown again.</strong></p>
      <dl>
        <dt>Server</dt><dd><code className="font-mono" style={{ userSelect: 'all' }}>{shown.url}</code></dd>
        <dt>Username</dt><dd><code className="font-mono" style={{ userSelect: 'all' }}>{shown.username}</code></dd>
        <dt>Password</dt><dd><code className="font-mono" style={{ userSelect: 'all', wordBreak: 'break-all' }}>{shown.password}</code></dd>
      </dl>
      <button type="button" onClick={() => setShown(null)}>I have copied it</button>
    </div>}
    <form onSubmit={create}>
      <label htmlFor="app-password-name">Device or app name</label>
      <input id="app-password-name" required maxLength={100} placeholder="Work laptop" value={name} onChange={e => setName(e.target.value)} />
      <button type="submit" disabled={busy || !name.trim()}>Create app password</button>
    </form>
    {list.length > 0 && <ul aria-label="Your app passwords">{list.map(p => <li key={p.id}>
      <span>{p.name} · created {p.created.slice(0, 10)} · {p.last_used ? `last used ${new Date(p.last_used * 1000).toLocaleString()}` : 'never used'}</span>
      <button type="button" className="btn-secondary" disabled={busy} onClick={() => revoke(p)}>Revoke</button>
    </li>)}</ul>}
    {error && <p role="alert">{error}</p>}
  </section>;
}
