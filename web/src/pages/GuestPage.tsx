import { useCallback, useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { bytes, record } from './drive/client';
import { ChevronRight, Download, FolderOpen, Upload } from 'lucide-react';
import { FolderIcon, iconFor } from './drive/FileIcon';

type Item = { kind: 'file' | 'folder'; id: string; name: string; size: number };
type Listing = { name: string; kind: string; upload_only: boolean; path: Item[]; items: Item[] };
function GuestIcon({ name }: { name: string }) { const Icon = iconFor(name); return <Icon className="drive-file-icon" aria-hidden="true" />; }
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
      if (!r?.ok) { setMessage(`${f.name} was not sent.`); break; }
      sent.push(f.name);
    }
    if (sent.length) setMessage(`Sent: ${sent.join(', ')}`);
    setBusy(false);
  };
  const unlock = (e: FormEvent) => { e.preventDefault(); void load(folder, password); };

  return <main className="guest-page">
    <div className="guest-brand"><img src="/app-icon.png" width={28} height={28} alt="" style={{ borderRadius: 8 }} />KyDrive</div>
    <header><h1 className="page-title">{view?.name ?? 'Shared with you'}</h1>{view && !view.upload_only && <p className="page-desc">Shared with you through a guest link.</p>}</header>
    {needPassword && <form onSubmit={unlock} className="panel form">
      <p>This link needs a password. Ask whoever sent it.</p>
      <label className="field">Password<input type="password" autoFocus required value={password} onChange={e => setPassword(e.target.value)} /></label>
      <div><button disabled={busy}>Open</button></div>
    </form>}
    {view?.upload_only && <section className="panel drive-empty">
      <Upload size={32} aria-hidden="true" />
      <h3>Send files</h3>
      <p>Choose files to send. You will not see what is already in this folder.</p>
      <label className="btn">Choose files<input type="file" multiple hidden disabled={busy} onChange={e => { void send(e.target.files); e.target.value = ''; }} /></label>
    </section>}
    {view && !view.upload_only && <div className="panel drive-files-panel">
      {view.path.length > 1 && <div className="drive-toolbar"><nav className="drive-breadcrumb" aria-label="Folder path">{view.path.map((p, i) => <span key={p.id} style={{ display: 'contents' }}>{i > 0 && <ChevronRight className="drive-breadcrumb-sep" size={16} aria-hidden="true" />}<button className="btn-link" aria-current={i === view.path.length - 1 ? 'page' : undefined} onClick={() => void load(p.id, password)}>{p.name}</button></span>)}</nav></div>}
      {view.items.length > 0 && <div className="drive-table"><table aria-label="Files"><tbody>{view.items.map(i => <tr key={i.id}>{i.kind === 'folder'
        ? <><td><span className="drive-name-cell"><FolderIcon /><button className="drive-file-name btn-link" onClick={() => void load(i.id, password)}>{i.name}</button></span></td><td className="num">Folder</td><td className="col-actions" /></>
        : <><td><span className="drive-name-cell"><GuestIcon name={i.name} /><span className="drive-name-text"><span className="drive-file-name">{i.name}</span><span className="drive-row-meta">{bytes(i.size)}</span></span></span></td><td className="num">{bytes(i.size)}</td><td className="col-actions"><button className="btn-secondary btn-sm" onClick={() => get(i.id)}><Download size={14} aria-hidden="true" />Download</button></td></>}</tr>)}</tbody></table></div>}
      {view.items.length === 0 && <div className="drive-empty"><FolderOpen size={32} aria-hidden="true" /><h3>This folder is empty.</h3></div>}
    </div>}
    {message && <p role="status" className="alert alert-info">{message}</p>}
    <form ref={download} method="post" action="/api/public/links/download" hidden>
      <input type="hidden" name="token" value={token} readOnly />
      <input type="hidden" name="password" value={password} readOnly />
      <input type="hidden" name="file" ref={fileField} />
    </form>
    <p className="guest-note">Shared through KyDrive. Only open files you expect.</p>
  </main>;
}
