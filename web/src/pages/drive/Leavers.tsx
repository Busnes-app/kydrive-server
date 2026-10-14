import { useCallback, useEffect, useState } from 'react';
import { bytes, change, record, response, type Workspace } from './client';

type Leaver = { workspace: string; name: string; status: string; size: number; items: number };
type Colleague = { id: string; name: string };
function leaver(v: unknown): v is Leaver { return record(v) && typeof v.workspace === 'string' && typeof v.name === 'string' && typeof v.status === 'string' && typeof v.size === 'number' && typeof v.items === 'number'; }
function colleague(v: unknown): v is Colleague { return record(v) && typeof v.id === 'string' && typeof v.name === 'string'; }

// Leavers lets an administrator move a removed account's My files without seeing their names.
export function Leavers({ workspaces, onDone }: { workspaces: Workspace[]; onDone: () => void }) {
  const [leavers, setLeavers] = useState<Leaver[]>([]);
  const [colleagues, setColleagues] = useState<Colleague[]>([]);
  const [targets, setTargets] = useState<Record<string, string>>({});
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const shared = workspaces.filter(w => w.kind === 'shared');
  const load = useCallback(async () => {
    const v = await response(await fetch('/api/drive/leavers'));
    if (!record(v) || !Array.isArray(v.leavers) || !v.leavers.every(leaver) || !Array.isArray(v.colleagues) || !v.colleagues.every(colleague)) throw new Error('Invalid server response');
    setLeavers(v.leavers); setColleagues(v.colleagues);
  }, []);
  useEffect(() => { load().catch(e => setMessage(e instanceof Error ? e.message : 'Leavers unavailable')); }, [load]);
  // A target is "workspace:<id>" or "user:<id>".
  function label(target: string) {
    const id = target.slice(target.indexOf(':') + 1);
    return target.startsWith('user:') ? `${colleagues.find(c => c.id === id)?.name}'s My files` : shared.find(w => w.id === id)?.name ?? id;
  }
  async function handOver(l: Leaver) {
    const target = targets[l.workspace] ?? '';
    if (!window.confirm(`Move ${bytes(l.size)} (${l.items} items) from ${l.name} to ${label(target)}? Everyone with access there will see the files.`)) return;
    const id = target.slice(target.indexOf(':') + 1);
    setBusy(true); setMessage('');
    try {
      await change(`/api/drive/leavers/${encodeURIComponent(l.workspace)}/handover`, 'POST', target.startsWith('user:') ? { user: id } : { workspace: id });
      setMessage(`Moved ${l.name}'s files to ${label(target)}.`);
      await load();
      onDone();
    } catch (e) { setMessage(e instanceof Error ? e.message : 'Handover failed'); } finally { setBusy(false); }
  }
  return <details className="panel"><summary>Leavers</summary>
    <p>Move a removed account's My files to a shared workspace or a colleague. File names stay hidden here.</p>
    {message && <p role="status">{message}</p>}
    {leavers.length === 0 && <p>No removed accounts hold files.</p>}
    {leavers.map(l => <form key={l.workspace} className="drive-toolbar" onSubmit={e => { e.preventDefault(); void handOver(l); }}>
      <span>{l.name} · {l.status} · {bytes(l.size)} · {l.items} items</span>
      <label>Destination<select required value={targets[l.workspace] ?? ''} onChange={e => setTargets({ ...targets, [l.workspace]: e.target.value })}>
        <option value="">Choose destination</option>
        <optgroup label="Shared workspaces">{shared.map(w => <option key={w.id} value={`workspace:${w.id}`}>{w.name}</option>)}</optgroup>
        <optgroup label="Colleagues">{colleagues.map(c => <option key={c.id} value={`user:${c.id}`}>{c.name}</option>)}</optgroup>
      </select></label>
      <button disabled={busy || !targets[l.workspace]}>Hand over</button>
    </form>)}
  </details>;
}
