import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { secureFetch } from '../api';
import { change, record, response } from './drive/client';
import { Alert } from '../components/Alert';
import { useConfirm } from '../components/Dialog';
import { When } from '../components/When';

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
  const [confirm, confirmNode] = useConfirm();

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
  const revoke = async (p: AppPassword) => { if (await confirm({ title: `Revoke "${p.name}"?`, body: isAgent(p.scope) ? 'The agent loses access at once.' : 'Devices using it stop syncing.', confirm: 'Revoke', danger: true })) void run(async () => {
    const r = await secureFetch(`/api/auth/app-passwords/${p.id}`, { method: 'DELETE' });
    if (!r.ok) await response(r);
    await load();
  }); };
  const setAccess = async (p: AppPassword, scope: Scope) => {
    if (scope === 'mcp:write' && !await confirm({ title: `Give "${p.name}" write access?`, body: <><p>{grant(p.name, scope, p.root)}</p><p>{WRITE_WARNING}</p></>, confirm: 'Give write access', danger: true })) return;
    void run(async () => { await change(`/api/auth/app-passwords/${p.id}`, 'PATCH', { scope }); await load(); });
  };

  const pendingScope: Scope = agent ? (write ? 'mcp:write' : 'mcp:read') : 'dav';
  return <section className="panel" aria-labelledby="app-passwords-heading">
    <h2 id="app-passwords-heading">App passwords and AI agents</h2>
    <p>Connect a file manager or office app over WebDAV, or an AI agent such as OpenClaw or Hermes Agent. Each password works only for its kind and can be revoked on its own.</p>
    {shown && (isAgent(shown.scope) ? <AgentSetup token={shown} onDone={() => setShown(null)} /> : <div className="reveal" role="status">
      <strong>Copy this password now. It will not be shown again.</strong>
      <p>{grant(shown.name, shown.scope)}</p>
      <dl>
        <dt>Server</dt><dd><code className="secret font-mono">{shown.url}</code></dd>
        <dt>Username</dt><dd><code className="secret font-mono">{shown.username}</code></dd>
        <dt>Password</dt><dd><code className="secret font-mono">{shown.password}</code></dd>
      </dl>
      <div><button type="button" onClick={() => setShown(null)}>I have copied it</button></div>
    </div>)}
    <form className="form" onSubmit={create}>
      <fieldset className="choice-group">
        <legend>Kind</legend>
        <label className="choice"><input type="radio" name="app-password-kind" checked={!agent} onChange={() => setAgent(false)} /><span><b>File manager or office app (WebDAV)</b><small>Finder, Windows Explorer, rclone, Cyberduck, LibreOffice</small></span></label>
        <label className="choice"><input type="radio" name="app-password-kind" checked={agent} onChange={() => setAgent(true)} /><span><b>AI agent</b><small>OpenClaw, Hermes Agent or another MCP client</small></span></label>
      </fieldset>
      {agent && <fieldset className="choice-group">
        <legend>Agent access</legend>
        <label className="choice"><input type="radio" name="app-password-access" checked={!write} onChange={() => setWrite(false)} /><span><b>Read only</b><small>Lists, searches and reads files</small></span></label>
        <label className="choice"><input type="radio" name="app-password-access" checked={write} onChange={() => setWrite(true)} /><span><b>Read and write</b><small>Can also create, change and trash files</small></span></label>
        {write && <p role="note" className="alert alert-warn" style={{ gridColumn: '1 / -1' }}>{WRITE_WARNING}</p>}
      </fieldset>}
      {agent && <div className="field"><label htmlFor="app-password-folder">Limit to folder (optional)</label>
        <input id="app-password-folder" placeholder="/My files/Projects" value={folder} onChange={e => setFolder(e.target.value)} />
        <span className="hint">The agent sees nothing outside this folder. Leave empty for all your files.</span></div>}
      <div className="field"><label htmlFor="app-password-name">{agent ? 'Agent name' : 'Device or app name'}</label>
        <input id="app-password-name" required maxLength={100} placeholder={agent ? 'Hermes' : 'Work laptop'} value={name} onChange={e => setName(e.target.value)} /></div>
      {name.trim() && <p aria-live="polite" className="hint">{grant(name.trim(), pendingScope, agent ? folder.trim() || undefined : undefined)}</p>}
      <div><button type="submit" disabled={busy || !name.trim()}>{agent ? 'Create agent token' : 'Create app password'}</button></div>
    </form>
    {error && <div style={{ marginTop: '1rem' }}><Alert>{error}</Alert></div>}
    <div className="panel-section">
      <h3>In use</h3>
      {list.length === 0 ? <p className="empty-note">No app passwords or agent tokens yet.</p> : <ul className="item-list" aria-label="Your app passwords">{list.map(p => <li key={p.id}>
        <div className="item-main">
          <span className="item-title">{p.name} <span className="badge">{isAgent(p.scope) ? 'AI agent' : 'WebDAV'}</span></span>
          <span className="item-meta">Created <When value={p.created} dateOnly /> · {p.last_used ? <>last used <When value={p.last_used} relativeOnly /></> : 'never used'}</span>
          {isAgent(p.scope) && <span className="item-meta">
            {p.root && <>Limited to {p.root}. </>}
            {p.root_missing && <>Its folder is in the trash or gone. </>}
            {p.revoke_after ? <>Revoked if unused by <When value={p.revoke_after} dateOnly />.</> : null}
          </span>}
        </div>
        <div className="item-actions">
          {isAgent(p.scope) && <select aria-label={`Access for ${p.name}`} value={p.scope} disabled={busy} onChange={e => void setAccess(p, e.target.value as Scope)}>
            <option value="mcp:read">Read only</option>
            <option value="mcp:write">Read and write</option>
          </select>}
          <button type="button" className="btn-danger-quiet btn-sm" disabled={busy} onClick={() => void revoke(p)}>Revoke</button>
        </div>
      </li>)}</ul>}
    </div>
    {confirmNode}
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
  return <div className="reveal" role="status">
    <strong>Set this as KYDRIVE_TOKEN in the agent's environment now. It will not be shown again.</strong>
    <p>{grant(token.name, token.scope, token.root || undefined)}{token.root && ' It sees nothing outside that folder.'}</p>
    <code className="secret font-mono">{token.password}</code>
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
    <div><button type="button" onClick={onDone}>I have copied it</button></div>
  </div>;
}
