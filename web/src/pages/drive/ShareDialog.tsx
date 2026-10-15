import { useCallback, useEffect, useRef, useState } from 'react';
import { secureFetch } from '../../api';
import { change, list, person, record, response, share } from './client';
import { guestLink, link } from './links';
import type { GuestLink, Link } from './links';
import { guestPolicy } from '../GuestLinkSettings';
import type { GuestPolicy } from '../GuestLinkSettings';
import type { Group, Person, Share } from './client';

// ShareDialog shares one file or folder with people (found by name) or, where the user can see
// the workspace directory, groups. A folder share covers everything inside it.
export function ShareDialog({ kind, id, name, groups, maxRole, onClose }: { kind: 'file' | 'folder'; id: string; name: string; groups: Group[]; maxRole: 'editor' | 'manager'; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const item = `/api/drive/${kind === 'file' ? 'files' : 'folders'}/${id}`;
  const base = `${item}/shares`;
  const [shares, setShares] = useState<Share[]>([]);
  const [links, setLinks] = useState<Link[]>([]);
  const [linkRole, setLinkRole] = useState('reader');
  const [linkDays, setLinkDays] = useState('7');
  const [newLink, setNewLink] = useState('');
  const [policy, setPolicy] = useState<GuestPolicy | null>(null);
  const [guests, setGuests] = useState<GuestLink[]>([]);
  const [guestDays, setGuestDays] = useState(7);
  const [guestPassword, setGuestPassword] = useState('');
  const [uploadOnly, setUploadOnly] = useState(false);
  const [newGuest, setNewGuest] = useState('');
  const [q, setQ] = useState('');
  const [people, setPeople] = useState<Person[]>([]);
  const [group, setGroup] = useState('');
  const [role, setRole] = useState('reader');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setShares(await list(base, share));
    setLinks(await list(`${item}/links`, link));
    const p = await response(await fetch('/api/drive/guest-links-policy'));
    if (guestPolicy(p)) { setPolicy(p); setGuestDays(d => Math.min(d, p.max_days)); }
    if (guestPolicy(p) && p.enabled) setGuests(await list(`${item}/guest-links`, guestLink));
  }, [base, item]);
  useEffect(() => { ref.current?.showModal(); void load().catch(e => setError(e instanceof Error ? e.message : 'Shares unavailable')); }, [load]);
  useEffect(() => {
    if ([...q.trim()].length < 2) { setPeople([]); return; }
    const timer = setTimeout(() => void list(`/api/drive/people?q=${encodeURIComponent(q.trim())}`, person).then(setPeople, () => setPeople([])), 300);
    return () => clearTimeout(timer);
  }, [q]);

  async function run(action: () => Promise<unknown>) {
    setBusy(true); setError('');
    try { await action(); await load(); } catch (e) { setError(e instanceof Error ? e.message : 'Sharing failed'); } finally { setBusy(false); }
  }
  const add = (principal_kind: 'user' | 'group', principal_id: string) => run(async () => { await change(base, 'PUT', { principal_kind, principal_id, role }); setQ(''); setGroup(''); });
  const removeShare = (s: Share) => run(async () => { const r = await secureFetch(`/api/drive/shares/${s.id}`, { method: 'DELETE' }); if (!r.ok) await response(r); });
  const roles = maxRole === 'manager' ? ['reader', 'editor', 'manager'] : ['reader', 'editor'];
  const createLink = () => run(async () => {
    const expires = linkDays === '0' ? 0 : Math.floor(Date.now() / 1000) + Number(linkDays) * 86400;
    const v = await change(`${item}/links`, 'POST', { role: linkRole, expires });
    if (!record(v) || typeof v.url !== 'string') throw new Error('Invalid link response');
    setNewLink(v.url);
  });
  const revokeLink = (l: { id: string }) => run(async () => { const r = await secureFetch(`/api/drive/links/${l.id}`, { method: 'DELETE' }); if (!r.ok) await response(r); });
  const createGuest = () => run(async () => {
    const v = await change(`${item}/guest-links`, 'POST', { days: guestDays, password: guestPassword, upload_only: uploadOnly });
    if (!record(v) || typeof v.url !== 'string') throw new Error('Invalid link response');
    setNewGuest(v.url); setGuestPassword('');
  });

  return <dialog ref={ref} className="drive-document-dialog" aria-labelledby="share-title" onCancel={onClose}>
    <h2 id="share-title">Share {name}</h2>
    <p>{kind === 'folder' ? 'People you add can open everything in this folder.' : 'People you add can open this file.'} They need a KyDrive account.</p>
    <label>Access<select value={role} onChange={e => setRole(e.target.value)}>{roles.map(r => <option key={r} value={r}>{r[0].toUpperCase() + r.slice(1)}</option>)}</select></label>
    <label>Find a person<input type="search" value={q} placeholder="Name or username" onChange={e => setQ(e.target.value)} /></label>
    {people.length > 0 && <ul aria-label="People" className="drive-share-people">{people.map(p => <li key={p.id}><span>{p.name} <small>{p.username}</small></span><button type="button" className="btn-secondary" disabled={busy} onClick={() => void add('user', p.id)}>Add</button></li>)}</ul>}
    {groups.length > 0 && <form className="drive-toolbar" onSubmit={e => { e.preventDefault(); if (group) void add('group', group); }}>
      <label>Or a group<select value={group} onChange={e => setGroup(e.target.value)}><option value="">Choose group</option>{groups.map(g => <option key={g.id} value={g.id}>{g.display_name}</option>)}</select></label>
      <button disabled={busy || !group}>Add group</button>
    </form>}
    <h3>Shared with</h3>
    {shares.length === 0 ? <p>Not shared with anyone yet.</p> : <ul aria-label="Shared with" className="drive-share-people">{shares.map(s => <li key={s.id}><span>{s.name} · {s.principal_kind === 'group' ? 'group' : 'person'} · {s.role}</span><button type="button" className="btn-secondary" disabled={busy} onClick={() => void removeShare(s)}>Remove</button></li>)}</ul>}
    <h3>Organisation link</h3>
    <p>Anyone signed in to KyDrive who opens the link gets this access until it expires or you revoke it.</p>
    <form className="drive-toolbar" onSubmit={e => { e.preventDefault(); void createLink(); }}>
      <label>Link access<select value={linkRole} onChange={e => setLinkRole(e.target.value)}><option value="reader">Reader</option><option value="editor">Editor</option></select></label>
      <label>Expires<select value={linkDays} onChange={e => setLinkDays(e.target.value)}><option value="1">In 1 day</option><option value="7">In 7 days</option><option value="30">In 30 days</option><option value="0">Never</option></select></label>
      <button disabled={busy}>Create link</button>
    </form>
    {newLink && <p role="status"><strong>Copy this link now; it is not shown again.</strong> <code className="font-mono" style={{ userSelect: 'all', wordBreak: 'break-all' }}>{newLink}</code></p>}
    {links.length > 0 && <ul aria-label="Links" className="drive-share-people">{links.map(l => <li key={l.id}><span>{l.role} link · {l.expires ? `expires ${new Date(l.expires * 1000).toLocaleDateString()}` : 'never expires'} · opened by {l.opened}</span><button type="button" className="btn-secondary" disabled={busy} onClick={() => void revokeLink(l)}>Revoke</button></li>)}</ul>}
    {policy?.enabled && <>
      <h3>Guest link</h3>
      <p>For people without an account: {kind === 'folder' ? 'they can view and download what is in this folder, or, upload-only, send files into it without seeing it.' : 'they can view and download this file.'} The link expires and can need a password.</p>
      <form className="drive-toolbar" onSubmit={e => { e.preventDefault(); void createGuest(); }}>
        <label>Days<input type="number" min={1} max={policy.max_days} required value={guestDays} onChange={e => setGuestDays(e.target.valueAsNumber)} /></label>
        <label>Password (optional)<input type="password" minLength={8} maxLength={1024} autoComplete="new-password" value={guestPassword} onChange={e => setGuestPassword(e.target.value)} /></label>
        {kind === 'folder' && <label className="drive-setting-toggle"><input type="checkbox" checked={uploadOnly} onChange={e => setUploadOnly(e.target.checked)} /> Upload only</label>}
        <button disabled={busy}>Create guest link</button>
      </form>
      {newGuest && <p role="status"><strong>Copy this link now; it is not shown again.</strong> Send any password separately. <code className="font-mono" style={{ userSelect: 'all', wordBreak: 'break-all' }}>{newGuest}</code></p>}
      {guests.length > 0 && <ul aria-label="Guest links" className="drive-share-people">{guests.map(g => <li key={g.id}><span>{g.upload_only ? 'upload-only' : 'view'} guest link · expires {new Date(g.expires * 1000).toLocaleDateString()}{g.has_password ? ' · password' : ''}</span><button type="button" className="btn-secondary" disabled={busy} onClick={() => void revokeLink(g)}>Revoke</button></li>)}</ul>}
    </>}
    {error && <p role="alert">{error}</p>}
    <div className="drive-dialog-actions"><button type="button" onClick={onClose}>Done</button></div>
  </dialog>;
}
