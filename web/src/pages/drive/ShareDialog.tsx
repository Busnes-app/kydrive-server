import { useCallback, useEffect, useState } from 'react';
import { Alert } from '../../components/Alert';
import { Dialog } from '../../components/Dialog';
import { When } from '../../components/When';
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
  useEffect(() => { void load().catch(e => setError(e instanceof Error ? e.message : 'Shares unavailable')); }, [load]);
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

  const cap = (r: string) => r[0].toUpperCase() + r.slice(1);
  return <Dialog wide title={`Share ${name}`} description={`${kind === 'folder' ? 'People you add can open everything in this folder.' : 'People you add can open this file.'} They need a KyDrive account.`} onClose={onClose}
    footer={<button type="button" className="btn-secondary" onClick={onClose}>Done</button>}>
    <div className="form-row">
      <label className="field grow">Find a person<input type="search" value={q} placeholder="Name or username" onChange={e => setQ(e.target.value)} /></label>
      <label className="field">Access<select value={role} onChange={e => setRole(e.target.value)}>{roles.map(r => <option key={r} value={r}>{cap(r)}</option>)}</select></label>
    </div>
    {people.length > 0 && <ul aria-label="People" className="item-list">{people.map(p => <li key={p.id}><div className="item-main"><span className="item-title">{p.name}</span><span className="item-meta">{p.username}</span></div><div className="item-actions"><button type="button" className="btn-secondary btn-sm" disabled={busy} onClick={() => void add('user', p.id)}>Add as {role}</button></div></li>)}</ul>}
    {groups.length > 0 && <form className="form-row" onSubmit={e => { e.preventDefault(); if (group) void add('group', group); }}>
      <label className="field grow">Or a group<select value={group} onChange={e => setGroup(e.target.value)}><option value="">Choose group</option>{groups.map(g => <option key={g.id} value={g.id}>{g.display_name}</option>)}</select></label>
      <button className="btn-secondary" disabled={busy || !group}>Add group</button>
    </form>}
    <section className="dialog-section" aria-labelledby="shared-with-heading">
      <h3 id="shared-with-heading">Shared with</h3>
      {shares.length === 0 ? <p>Not shared with anyone yet.</p> : <ul aria-label="Shared with" className="item-list">{shares.map(s => <li key={s.id}><div className="item-main"><span className="item-title">{s.name} <span className="badge">{cap(s.role)}</span></span><span className="item-meta">{s.principal_kind === 'group' ? 'Group' : 'Person'}</span></div><div className="item-actions"><button type="button" className="btn-danger-quiet btn-sm" disabled={busy} onClick={() => void removeShare(s)}>Remove</button></div></li>)}</ul>}
    </section>
    <section className="dialog-section" aria-labelledby="org-link-heading">
      <h3 id="org-link-heading">Organisation link</h3>
      <p>Anyone signed in to KyDrive who opens the link gets this access until it expires or you revoke it.</p>
      <form className="form-row" onSubmit={e => { e.preventDefault(); void createLink(); }}>
        <label className="field">Link access<select value={linkRole} onChange={e => setLinkRole(e.target.value)}><option value="reader">Reader</option><option value="editor">Editor</option></select></label>
        <label className="field">Expires<select value={linkDays} onChange={e => setLinkDays(e.target.value)}><option value="1">In 1 day</option><option value="7">In 7 days</option><option value="30">In 30 days</option><option value="0">Never</option></select></label>
        <button className="btn-secondary" disabled={busy}>Create link</button>
      </form>
      {newLink && <div className="reveal" role="status"><strong>Copy this link now; it is not shown again.</strong><code className="secret font-mono">{newLink}</code></div>}
      {links.length > 0 && <ul aria-label="Links" className="item-list">{links.map(l => <li key={l.id}><div className="item-main"><span className="item-title">{cap(l.role)} link</span><span className="item-meta">{l.expires ? <>Expires <When value={l.expires} dateOnly /></> : 'Never expires'} · opened by {l.opened}</span></div><div className="item-actions"><button type="button" className="btn-danger-quiet btn-sm" disabled={busy} onClick={() => void revokeLink(l)}>Revoke</button></div></li>)}</ul>}
    </section>
    {policy?.enabled && <section className="dialog-section" aria-labelledby="guest-link-heading">
      <h3 id="guest-link-heading">Guest link</h3>
      <p>For people without an account: {kind === 'folder' ? 'they can view and download what is in this folder, or, upload-only, send files into it without seeing it.' : 'they can view and download this file.'} The link expires and can need a password.</p>
      <form className="form-row" onSubmit={e => { e.preventDefault(); void createGuest(); }}>
        <label className="field">Days<input type="number" min={1} max={policy.max_days} required value={guestDays} onChange={e => setGuestDays(e.target.valueAsNumber)} /></label>
        <label className="field">Password (optional)<input type="password" minLength={8} maxLength={1024} autoComplete="new-password" value={guestPassword} onChange={e => setGuestPassword(e.target.value)} /></label>
        {kind === 'folder' && <label className="check" style={{ alignSelf: 'center' }}><input type="checkbox" checked={uploadOnly} onChange={e => setUploadOnly(e.target.checked)} /> Upload only</label>}
        <button className="btn-secondary" disabled={busy}>Create guest link</button>
      </form>
      {newGuest && <div className="reveal" role="status"><strong>Copy this link now; it is not shown again. Send any password separately.</strong><code className="secret font-mono">{newGuest}</code></div>}
      {guests.length > 0 && <ul aria-label="Guest links" className="item-list">{guests.map(g => <li key={g.id}><div className="item-main"><span className="item-title">{g.upload_only ? 'Upload-only' : 'View'} guest link</span><span className="item-meta">Expires <When value={g.expires} dateOnly />{g.has_password ? ' · password' : ''}</span></div><div className="item-actions"><button type="button" className="btn-danger-quiet btn-sm" disabled={busy} onClick={() => void revokeLink(g)}>Revoke</button></div></li>)}</ul>}
    </section>}
    {error && <Alert>{error}</Alert>}
  </Dialog>;
}
