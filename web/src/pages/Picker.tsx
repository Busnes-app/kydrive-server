import { useEffect, useRef, useState } from 'react';
import { ChevronRight } from 'lucide-react';
import { Alert } from '../components/Alert';
import { FileIcon, FolderIcon } from './drive/FileIcon';
import { bytes, change, file, folder, list, record, response, workspace, type DriveFile, type Folder, type Workspace } from './drive/client';

// Another suite app opens /pick?origin=&mode=link|copy in a popup (docs/SUITE.md, contract I).
// The query moves to session storage at once, so it survives sign-in. The server decides
// whether the origin may be served; only the confirming click makes links (or records the
// copy) and posts {type:'kydrive.picked', files} to the opener at exactly that origin.
const pending = 'ky-pending-pick';

export function takePickRequestFromURL(): void {
  if (location.pathname.replace(/\/$/, '') !== '/pick') return;
  sessionStorage.setItem(pending, location.search);
  history.replaceState(null, '', '/');
}

export function hasPickRequest(): boolean { return sessionStorage.getItem(pending) !== null; }

// Files the user may share: a manager's workspace, or an editor's where editors may share.
const shareable = (w: Workspace) => w.role === 'manager' || (w.role === 'editor' && w.editors_share);

export function Picker({ onClose }: { onClose: () => void }) {
  const [query] = useState(() => new URLSearchParams(sessionStorage.getItem(pending) ?? ''));
  const origin = query.get('origin') ?? '';
  const mode = query.get('mode') || 'link';
  const [app, setApp] = useState<string | null>(null);
  const [refused, setRefused] = useState('');
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [ws, setWs] = useState('');
  const [path, setPath] = useState<Folder[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [files, setFiles] = useState<DriveFile[]>([]);
  const [chosen, setChosen] = useState<Map<string, DriveFile>>(new Map());
  const [days, setDays] = useState(30);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const once = useRef(false);

  useEffect(() => {
    void fetch(`/api/drive/pick?${new URLSearchParams({ origin, mode })}`).then(response).then(v => {
      if (!record(v) || typeof v.app !== 'string') throw new Error('Unexpected server response.');
      setApp(v.app);
      return change('/api/drive/personal-workspace', 'POST', {}).catch(() => {}).then(() => list('/api/drive/workspaces', workspace)).then(all => { const ok = all.filter(shareable); setWorkspaces(ok); setWs(ok[0]?.id ?? ''); });
    }).catch(() => setRefused('This page can only be opened by a Ky Suite app you use, and nothing was shared.'));
  }, [origin, mode]);

  const parent = path.at(-1)?.id ?? '';
  useEffect(() => {
    if (!ws) return;
    let live = true;
    Promise.all([list(`/api/drive/workspaces/${ws}/folders`, folder), list(`/api/drive/workspaces/${ws}/files`, file)]).then(([d, f]) => {
      if (!live) return;
      setFolders(d.filter(x => !x.trashed && x.parent === parent));
      setFiles(f.filter(x => !x.trashed && x.parent === parent));
    }).catch(e => { if (live) setError(e instanceof Error ? e.message : 'Files unavailable'); });
    return () => { live = false; };
  }, [ws, parent]);

  const close = () => { sessionStorage.removeItem(pending); onClose(); window.close(); };
  const toggle = (f: DriveFile, on: boolean) => setChosen(prev => { const next = new Map(prev); if (on) next.set(f.id, f); else next.delete(f.id); return next; });

  const confirm = async () => {
    const opener = window.opener as Window | null;
    if (!opener || opener.closed) { setError('The app that opened this window has closed. Nothing was shared.'); return; }
    if (once.current) return;
    once.current = true; setBusy(true); setError('');
    try {
      const v = await change('/api/drive/pick', 'POST', { origin, mode, files: [...chosen.keys()], ...(mode === 'link' ? { days } : {}) });
      if (!record(v) || !Array.isArray(v.files)) throw new Error('Unexpected server response.');
      opener.postMessage({ type: 'kydrive.picked', files: v.files }, origin);
      close();
    } catch (e) { once.current = false; setBusy(false); setError(e instanceof Error ? e.message : 'Sharing failed.'); }
  };

  const verb = mode === 'copy' ? 'Attach' : 'Share';
  const n = chosen.size;
  return <main className="page" style={{ maxWidth: '48rem', margin: '0 auto' }}>
    <header className="page-header"><div><h1>Choose files from KyDrive</h1>
      {app && <p className="page-desc">{mode === 'copy' ? `${app} will receive a copy of each file you choose.` : `${app} will receive an organisation link to each file you choose. Only signed-in people in your organisation can open it.`}</p>}
    </div></header>
    {refused ? <>
      <Alert>{refused}</Alert>
      <div className="button-row" style={{ marginTop: '1rem' }}><button type="button" className="btn-secondary" onClick={close}>Close</button></div>
    </> : app && <div className="panel drive-files-panel">
      <div className="drive-toolbar">
        <label className="field">Workspace<select value={ws} onChange={e => { setWs(e.target.value); setPath([]); }}>{workspaces.map(w => <option key={w.id} value={w.id}>{w.name}</option>)}</select></label>
        <nav className="drive-breadcrumb" aria-label="Folder path">
          <button className="btn-link" onClick={() => setPath([])} aria-current={path.length === 0 ? 'page' : undefined}>{workspaces.find(w => w.id === ws)?.name ?? 'Files'}</button>
          {path.map((p, i) => <span key={p.id} style={{ display: 'contents' }}><ChevronRight className="drive-breadcrumb-sep" size={16} aria-hidden="true" /><button className="btn-link" onClick={() => setPath(path.slice(0, i + 1))} aria-current={i === path.length - 1 ? 'page' : undefined}>{p.name}</button></span>)}
        </nav>
      </div>
      {folders.length + files.length > 0 ? <div className="drive-table"><table><thead><tr><th className="col-select"><span className="sr-only">Choose</span></th><th>Name</th><th className="num">Size</th></tr></thead><tbody>
        {folders.map(d => <tr key={d.id}><td className="col-select" /><td><span className="drive-name-cell"><FolderIcon /><span className="drive-name-text"><button className="drive-file-name btn-link" onClick={() => setPath([...path, d])}>{d.name}</button><span className="drive-row-meta">Folder</span></span></span></td><td className="num">—</td></tr>)}
        {files.map(f => <tr key={f.id} className={chosen.has(f.id) ? 'is-selected' : undefined}>
          <td className="col-select"><input type="checkbox" aria-label={`Choose ${f.name}`} checked={chosen.has(f.id)} disabled={!chosen.has(f.id) && n >= 20} onChange={e => toggle(f, e.target.checked)} /></td>
          <td><span className="drive-name-cell"><FileIcon file={f} /><span className="drive-name-text"><span className="drive-file-name">{f.name}</span><span className="drive-row-meta">{bytes(f.size)}</span></span></span></td>
          <td className="num">{bytes(f.size)}</td>
        </tr>)}
      </tbody></table></div> : <div className="drive-empty"><h3>No files here</h3><p>{workspaces.length ? 'Open another folder or workspace.' : 'You have no workspace whose files you may share.'}</p></div>}
      <div className="form-row" style={{ padding: '1rem' }}>
        {mode === 'link' && <label className="field">Link expires<select value={days} onChange={e => setDays(Number(e.target.value))}>
          <option value={1}>In 1 day</option><option value={7}>In 7 days</option><option value={30}>In 30 days</option><option value={0}>Never</option>
        </select></label>}
        <div className="button-row">
          <button type="button" className="btn-secondary" onClick={close}>Cancel</button>
          <button type="button" disabled={busy || n === 0} onClick={() => void confirm()}>{n === 0 ? verb : `${verb} ${n} ${n === 1 ? 'file' : 'files'}`}</button>
        </div>
      </div>
      {error && <div style={{ padding: '0 1rem 1rem' }}><Alert>{error}</Alert></div>}
    </div>}
  </main>;
}
