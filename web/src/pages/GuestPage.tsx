import { useCallback, useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { bytes, record } from './drive/client';

type Item = { kind: 'file' | 'folder'; id: string; name: string; size: number };
type Listing = { name: string; kind: string; upload_only: boolean; path: Item[]; items: Item[] };
function item(v: unknown): v is Item { return record(v) && (v.kind === 'file' || v.kind === 'folder') && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.size === 'number'; }
function listing(v: unknown): v is Listing {
  return record(v) && typeof v.name === 'string' && typeof v.kind === 'string' && typeof v.upload_only === 'boolean' && Array.isArray(v.path) && v.path.every(item) && Array.isArray(v.items) && v.items.every(item);
}

// GuestPage is what someone without an account sees at /g/#<token>. The token stays in the
// fragment, which is never sent to the server, and goes out only in request bodies and headers.
export function GuestPage() {
  const token = location.hash.slice(1);
  const [password, setPassword] = useState('');
  const [needPassword, setNeedPassword] = useState(false);
  const [view, setView] = useState<Listing | null>(null);
  const [folder, setFolder] = useState('');
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const download = useRef<HTMLFormElement>(null);
  const fileField = useRef<HTMLInputElement>(null);

  const load = useCallback(async (at: string, secret: string) => {
    setBusy(true); setMessage('');
    try {
      const r = await fetch('/api/public/links/view', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token, password: secret, folder: at }) });
      if (r.status === 401) { setNeedPassword(true); if (secret) setMessage('That password is not right.'); return; }
      if (r.status === 429) { setMessage('Too many attempts. Wait a minute and try again.'); return; }
      if (!r.ok) { setView(null); setMessage('This link does not work. It may have expired or been turned off.'); return; }
      const v: unknown = await r.json();
      if (!listing(v)) throw new Error();
      setNeedPassword(false); setView(v); setFolder(at);
    } catch { setMessage('Something went wrong. Try again.'); } finally { setBusy(false); }
  }, [token]);

  useEffect(() => { if (token) void load('', ''); else setMessage('This link is incomplete.'); }, [token, load]);

  const get = (id: string) => { if (fileField.current && download.current) { fileField.current.value = id; download.current.submit(); } };
  const send = async (files: FileList | null) => {
    if (!files?.length) return;
    setBusy(true); setMessage('');
    const sent: string[] = [];
    for (const f of Array.from(files)) {
      const r = await fetch(`/api/public/links/upload?name=${encodeURIComponent(f.name)}`, { method: 'POST', headers: { 'X-Link-Token': token, 'X-Link-Password': password, 'Content-Type': 'application/octet-stream' }, body: f }).catch(() => null);
      if (!r?.ok) { setMessage(r?.status === 409 ? `${f.name}: a file with this name was already sent.` : `${f.name} was not sent.`); break; }
      sent.push(f.name);
    }
    if (sent.length) setMessage(`Sent: ${sent.join(', ')}`);
    setBusy(false);
  };
  const unlock = (e: FormEvent) => { e.preventDefault(); void load(folder, password); };

  return <main className="guest-page">
    <h1>{view?.name ?? 'Shared with you'}</h1>
    {needPassword && <form onSubmit={unlock} className="drive-toolbar">
      <label>Password<input type="password" autoFocus required value={password} onChange={e => setPassword(e.target.value)} /></label>
      <button disabled={busy}>Open</button>
    </form>}
    {view?.upload_only && <section>
      <p>Choose files to send. You will not see what is already in this folder.</p>
      <label className="btn">Choose files<input type="file" multiple hidden disabled={busy} onChange={e => { void send(e.target.files); e.target.value = ''; }} /></label>
    </section>}
    {view && !view.upload_only && <>
      {view.path.length > 1 && <nav className="drive-breadcrumb" aria-label="Folder path">{view.path.map((p, i) => <span key={p.id}>{i > 0 && ' / '}<button className="btn-link" onClick={() => void load(p.id, password)}>{p.name}</button></span>)}</nav>}
      <ul className="drive-share-people" aria-label="Files">{view.items.map(i => <li key={i.id}>{i.kind === 'folder'
        ? <button className="btn-link" onClick={() => void load(i.id, password)}>{i.name}/</button>
        : <><span>{i.name} <small>{bytes(i.size)}</small></span><button className="btn-secondary" onClick={() => get(i.id)}>Download</button></>}</li>)}</ul>
      {view.items.length === 0 && <p>This folder is empty.</p>}
    </>}
    {message && <p role="status">{message}</p>}
    <form ref={download} method="post" action="/api/public/links/download" hidden>
      <input type="hidden" name="token" value={token} readOnly />
      <input type="hidden" name="password" value={password} readOnly />
      <input type="hidden" name="file" ref={fileField} />
    </form>
    <p className="guest-note">Shared through KyDrive. Only open files you expect.</p>
  </main>;
}
