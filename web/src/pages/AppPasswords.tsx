import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { secureFetch } from '../api';
import { change, record, response } from './drive/client';

type Scope = 'dav' | 'mcp:read' | 'mcp:write';
type AppPassword = { id: string; name: string; created: string; last_used: number; scope: Scope; revoke_after?: number; root?: string; root_missing?: boolean };
type Created = { name: string; password: string; scope: Scope; username?: string; url: string; skill?: string; root?: string };
const scopes: Scope[] = ['dav', 'mcp:read', 'mcp:write'];
function appPassword(v: unknown): v is AppPassword { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.created === 'string' && typeof v.last_used === 'number' && scopes.includes(v.scope as Scope); }
function created(v: unknown): v is Created { return record(v) && ['name', 'password', 'url'].every(k => typeof v[k] === 'string') && scopes.includes(v.scope as Scope); }
const isAgent = (s: Scope) => s !== 'dav';

// grant says who gets a credential and what it can do. Every screen that issues or widens one
// shows it, so no prompt ever asks for "access" without naming the agent and its level.
export function grant(name: string, scope: Scope, root?: string): string {
  const where = root ? root : 'all your files';
  if (scope === 'dav') return `"${name}" gets read and write access to ${where} over WebDAV.`;
  return `AI agent "${name}" gets ${scope === 'mcp:write' ? 'read and write' : 'read-only'} access to ${where}.`;
}

const WRITE_WARNING = 'The agent can create, replace, rename, move and trash files you can edit. Anyone who can message the agent can try to make it do so. It can never delete anything for good.';

// AppPasswords lets a user connect WebDAV clients (file managers, LibreOffice) and AI agents with
// credentials that each work only for their kind and can be revoked one by one.
export function AppPasswords() {
  const [list, setList] = useState<AppPassword[]>([]);
  const [name, setName] = useState('');
  const [agent, setAgent] = useState(false);
  const [write, setWrite] = useState(false);
  const [folder, setFolder] = useState('');
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
    const body = agent ? { name: name.trim(), scope: write ? 'mcp:write' : 'mcp:read', folder: folder.trim() } : { name: name.trim() };
    const v = await change('/api/auth/app-passwords', 'POST', body);
    if (!created(v)) throw new Error('Unexpected server response.');
    setShown(v); setName(''); setFolder(''); await load();
  }); };
  const revoke = (p: AppPassword) => { if (window.confirm(isAgent(p.scope) ? `Revoke "${p.name}"? The agent loses access at once.` : `Revoke "${p.name}"? Devices using it stop syncing.`)) void run(async () => {
    const r = await secureFetch(`/api/auth/app-passwords/${p.id}`, { method: 'DELETE' });
    if (!r.ok) await response(r);
    await load();
  }); };
  const setAccess = (p: AppPassword, scope: Scope) => {
    if (scope === 'mcp:write' && !window.confirm(`${grant(p.name, scope, p.root)}\n\n${WRITE_WARNING}\n\nGive "${p.name}" write access?`)) return;
    void run(async () => { await change(`/api/auth/app-passwords/${p.id}`, 'PATCH', { scope }); await load(); });
  };

  return <section className="panel" style={{ maxWidth: 560 }} aria-labelledby="app-passwords-heading">
    <h2 id="app-passwords-heading">App passwords</h2>
    <p>Connect a file manager or office app over WebDAV, or an AI agent such as OpenClaw or Hermes Agent. Each password works only for its kind and can be revoked on its own.</p>
    {shown && (isAgent(shown.scope) ? <AgentSetup token={shown} onDone={() => setShown(null)} /> : <div role="status">
      <p><strong>Copy this password now. It will not be shown again.</strong></p>
      <p>{grant(shown.name, shown.scope)}</p>
      <dl>
        <dt>Server</dt><dd><code className="font-mono" style={{ userSelect: 'all' }}>{shown.url}</code></dd>
        <dt>Username</dt><dd><code className="font-mono" style={{ userSelect: 'all' }}>{shown.username}</code></dd>
        <dt>Password</dt><dd><code className="font-mono" style={{ userSelect: 'all', wordBreak: 'break-all' }}>{shown.password}</code></dd>
      </dl>
      <button type="button" onClick={() => setShown(null)}>I have copied it</button>
    </div>)}
    <form onSubmit={create}>
      <fieldset>
        <legend>Kind</legend>
        <label><input type="radio" name="app-password-kind" checked={!agent} onChange={() => setAgent(false)} /> File manager or office app (WebDAV)</label>
        <label><input type="radio" name="app-password-kind" checked={agent} onChange={() => setAgent(true)} /> AI agent</label>
      </fieldset>
      {agent && <fieldset>
        <legend>Agent access</legend>
        <label><input type="radio" name="app-password-access" checked={!write} onChange={() => setWrite(false)} /> Read only</label>
        <label><input type="radio" name="app-password-access" checked={write} onChange={() => setWrite(true)} /> Read and write</label>
        {write && <p role="note">{WRITE_WARNING}</p>}
        <label htmlFor="app-password-folder">Limit to folder (optional)</label>
        <input id="app-password-folder" placeholder="/My files/Projects" value={folder} onChange={e => setFolder(e.target.value)} />
      </fieldset>}
      <label htmlFor="app-password-name">{agent ? 'Agent name' : 'Device or app name'}</label>
      <input id="app-password-name" required maxLength={100} placeholder={agent ? 'Hermes' : 'Work laptop'} value={name} onChange={e => setName(e.target.value)} />
      {name.trim() && <p aria-live="polite">{grant(name.trim(), agent ? (write ? 'mcp:write' : 'mcp:read') : 'dav', agent ? folder.trim() || undefined : undefined)}</p>}
      <button type="submit" disabled={busy || !name.trim()}>{agent ? 'Create agent token' : 'Create app password'}</button>
    </form>
    {list.length > 0 && <ul aria-label="Your app passwords">{list.map(p => <li key={p.id}>
      <span>{p.name} · {isAgent(p.scope) ? 'AI agent · ' : ''}created {p.created.slice(0, 10)} · {p.last_used ? `last used ${new Date(p.last_used * 1000).toLocaleString()}` : 'never used'}</span>
      {isAgent(p.scope) && <>
        <select aria-label={`Access for ${p.name}`} value={p.scope} disabled={busy} onChange={e => setAccess(p, e.target.value as Scope)}>
          <option value="mcp:read">Read only</option>
          <option value="mcp:write">Read and write</option>
        </select>
        {p.root && <small> Limited to {p.root}</small>}
        {p.root_missing && <small> Its folder is in the trash or gone</small>}
        {p.revoke_after ? <small> Revoked if unused by {new Date(p.revoke_after * 1000).toLocaleDateString()}</small> : null}
      </>}
      <button type="button" className="btn-secondary" disabled={busy} onClick={() => revoke(p)}>Revoke</button>
    </li>)}</ul>}
    {error && <p role="alert">{error}</p>}
  </section>;
}

// AgentSetup shows a new agent token once, with copy-ready config that reads it from
// KYDRIVE_TOKEN, so the token itself never lands in a config file.
function AgentSetup({ token, onDone }: { token: Created; onDone: () => void }) {
  const openclaw = `"mcp": { "servers": { "kydrive": {
  "url": "${token.url}",
  "transport": "streamable-http",
  "headers": { "Authorization": "Bearer \${KYDRIVE_TOKEN}" }
}}}`;
  const hermes = `mcp_servers:
  kydrive:
    url: "${token.url}"
    headers:
      Authorization: "Bearer \${KYDRIVE_TOKEN}"`;
  return <div role="status">
    <p><strong>Set this as KYDRIVE_TOKEN in the agent's environment now. It will not be shown again.</strong></p>
    <p>{grant(token.name, token.scope, token.root || undefined)}{token.root && ' It sees nothing outside that folder.'}</p>
    <p><code className="font-mono" style={{ userSelect: 'all', wordBreak: 'break-all' }}>{token.password}</code></p>
    <p>The agent loses access if it does not use KyDrive for 90 days; create a new token then.</p>
    <h3>OpenClaw</h3>
    <p>Add to <code>openclaw.json</code>:</p>
    <pre><code>{openclaw}</code></pre>
    <p>Install the skill:</p>
    <pre><code>{`mkdir -p ~/.agents/skills/kydrive\ncurl -fsSo ~/.agents/skills/kydrive/SKILL.md ${token.skill ?? ''}`}</code></pre>
    <p>With a custom <code>OPENCLAW_STATE_DIR</code>, use <code>&lt;workspace&gt;/skills</code> instead.</p>
    <h3>Hermes Agent</h3>
    <p>Add to <code>~/.hermes/config.yaml</code>:</p>
    <pre><code>{hermes}</code></pre>
    <p>Install the skill:</p>
    <pre><code>{`hermes skills install ${token.skill ?? ''} --category productivity`}</code></pre>
    <button type="button" onClick={onDone}>I have copied it</button>
  </div>;
}
