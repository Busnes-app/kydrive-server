import React, { useEffect, useState } from 'react';
import { Alert } from '../components/Alert';
import { change, record, response } from './drive/client';

// guest_policy and guest_links are absent from servers older than contract K.
type Switch = { paired: boolean; enabled: boolean; guest_policy?: boolean; guest_links?: boolean };
const isSwitch = (v: unknown): v is Switch => record(v) && typeof v.paired === 'boolean' && typeof v.enabled === 'boolean';

/** KyPostSwitch is the user's "Let KyPost save and share my files", shown only while paired, and
 * "Let KyPost create guest links", shown only while the administrator's guest-link policy is on. */
export const KyPostSwitch: React.FC = () => {
  const [state, setState] = useState<Switch | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    let live = true;
    void fetch('/api/drive/kypost').then(response).then(v => { if (live && isSwitch(v)) setState(v); }).catch(() => undefined);
    return () => { live = false; };
  }, []);
  if (!state?.paired) return null;
  const set = async (body: { enabled: boolean } | { guest_links: boolean }) => {
    setError('');
    try {
      const v = await change('/api/drive/kypost', 'PUT', body);
      if (isSwitch(v)) setState(v);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not save the setting');
    }
  };
  return <>
    <label className="app-preference"><input type="checkbox" checked={state.enabled} onChange={e => void set({ enabled: e.target.checked })} />Let KyPost save and share my files</label>
    <p className="hint">KyPost can save mail attachments to a Mail attachments folder in My files, and share a file there with people in your organisation by link. It can also attach a copy of a file you choose for it in KyDrive’s picker. It cannot open, list or delete anything else.</p>
    {state.guest_policy && <>
      <label className="app-preference"><input type="checkbox" checked={state.guest_links === true} disabled={!state.enabled} onChange={e => void set({ guest_links: e.target.checked })} />Let KyPost create guest links for people outside the organisation</label>
      <p className="hint">When you send a file from Mail attachments to someone outside the organisation, KyPost can give them a guest link instead: they can view and download that file, without an account, until the link expires. You can revoke it from the file’s Share dialog.</p>
    </>}
    {error && <Alert>{error}</Alert>}
  </>;
};

type Link = { connected: boolean; source?: string; url?: string; reason?: string };
const reasons: Record<string, string> = {
  pending: 'Not checked yet',
  no_kyidentity: 'KyIdentity sign-in is not set up',
  no_peer: 'KyIdentity has not paired KyPost with KyDrive',
  unauthorized: 'KyIdentity refused KyDrive’s sign-in credentials',
  unreachable: 'KyIdentity did not answer',
  invalid_peer: 'KyIdentity gave a KyPost address that is not https://host',
};

/** KyPostStatus is the administrator's view of the KyPost pairing. It never shows the secret. */
export const KyPostStatus: React.FC = () => {
  const [link, setLink] = useState<Link | null>(null);
  useEffect(() => {
    let live = true;
    void fetch('/api/settings/kypost').then(response).then(v => { if (live && record(v) && typeof v.connected === 'boolean') setLink(v as Link); }).catch(() => undefined);
    return () => { live = false; };
  }, []);
  if (!link) return <dd>Unknown</dd>;
  if (!link.connected) return <dd>Not connected: {reasons[link.reason ?? ''] ?? link.reason}</dd>;
  return <dd>Connected {link.source === 'environment' ? 'by the environment' : 'through KyIdentity'} <span className="font-mono">{link.url}</span></dd>;
};
