import { useState } from 'react';
import { Dialog } from '../../components/Dialog';
import { Alert } from '../../components/Alert';
import { change, record } from './client';
import type { SuiteApp } from '../../components/AppHeader';

// "Email a link" (docs/SUITE.md, contract J): an organisation reader link, then KyPost's compose
// deep link with the file name as subject and the link in the body.

// kypostOrigin is the https origin of KyPost in the user's suite app list, or ''.
export function kypostOrigin(apps: SuiteApp[]): string {
  const app = apps.find(a => a.id === 'kypost');
  try { const u = new URL(app?.url ?? ''); return u.protocol === 'https:' ? u.origin : ''; } catch { return ''; }
}

// clip cuts s to at most n UTF-8 bytes without splitting a character.
function clip(s: string, n: number): string {
  const enc = new TextEncoder();
  let out = '';
  for (const ch of s) { if (enc.encode(out + ch).length > n) break; out += ch; }
  return out;
}

export function composeURL(origin: string, name: string, url: string, expires: number): string {
  const until = expires ? `It expires on ${new Date(expires * 1000).toLocaleDateString()}.` : 'It does not expire.';
  const body = clip(`${name}\n${url}\n\nOnly people signed in to our organisation can open this link. ${until}`, 2000);
  return `${origin}/compose?${new URLSearchParams({ subject: clip(name, 255), body })}`;
}

export function EmailLink({ file, origin, onClose }: { file: { id: string; name: string }; origin: string; onClose: () => void }) {
  const [days, setDays] = useState('30');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  async function submit() {
    // Opened inside the click so pop-up blockers allow it, then pointed at KyPost.
    const tab = window.open('about:blank', '_blank');
    if (tab) tab.opener = null;
    setBusy(true); setError('');
    try {
      const expires = days === '0' ? 0 : Math.floor(Date.now() / 1000) + Number(days) * 86400;
      const v = await change(`/api/drive/files/${file.id}/links`, 'POST', { role: 'reader', expires });
      if (!record(v) || typeof v.url !== 'string') throw new Error('Unexpected server response.');
      const target = composeURL(origin, file.name, v.url, expires);
      if (tab) tab.location.href = target; else location.assign(target);
      onClose();
    } catch (e) { tab?.close(); setError(e instanceof Error ? e.message : 'The link could not be made.'); } finally { setBusy(false); }
  }
  return <Dialog title={`Email a link to ${file.name}`} description="KyPost opens a new message with the link. Nothing is sent until you send it." onClose={onClose} onSubmit={() => void submit()}
    footer={<><button type="button" className="btn-secondary" onClick={onClose}>Cancel</button><button disabled={busy}>Write email</button></>}>
    <p>The link lets anyone signed in to KyDrive in your organisation view this file. People outside it cannot open it.</p>
    <label className="field">Link expires<select value={days} onChange={e => setDays(e.target.value)}><option value="1">In 1 day</option><option value="7">In 7 days</option><option value="30">In 30 days</option><option value="0">Never</option></select></label>
    {error && <Alert>{error}</Alert>}
  </Dialog>;
}
