import { useCallback, useRef, useState } from 'react';
import { secureFetch } from '../../api';
import { HttpError, bytes, file, list } from './client';
import type { DriveFile, Folder } from './client';
import { UploadInterrupted, freeName, makeFolders, sendUpload, startUpload } from './upload';
import type { Entry, Session, Target } from './upload';

type Upload = { key: number; file: File; workspace: string; parent: string; target: Target; done: number; finished: boolean; error: string; conflict: boolean; session?: Session };

// Choice is what to do with files whose name is already taken in their folder.
export type Choice = 'replace' | 'keep' | 'cancel';
// Decide asks the user once per batch, naming the files that clash.
export type Decide = (names: string[]) => Promise<Choice>;

// liveFiles lists the files currently in a workspace, so a choice uses fresh revisions.
async function liveFiles(workspace: string): Promise<DriveFile[]> {
  return (await list(`/api/drive/workspaces/${workspace}/files?trash=false`, file)).filter(f => !f.trashed);
}

// targetFor applies a choice to one clashing upload: a new version of the existing file, or a
// numbered name nothing in the folder uses yet (taken grows so a batch never picks one twice).
function targetFor(choice: 'replace' | 'keep', name: string, existing: DriveFile, taken: Set<string>): Target {
  if (choice === 'replace') return { name, replace: { id: existing.id, revision: existing.revision } };
  const free = freeName(name, taken);
  taken.add(free);
  return { name: free };
}

// useUploads runs uploads one at a time and calls onIdle when the queue drains.
export function useUploads(onIdle: () => void) {
  const [items, setItems] = useState<Upload[]>([]);
  const queue = useRef(Promise.resolve());
  const pending = useRef(0);
  const nextKey = useRef(0);
  const update = (key: number, patch: Partial<Upload>) => setItems(xs => xs.map(x => x.key === key ? { ...x, ...patch } : x));

  const enqueue = useCallback((u: Upload) => {
    pending.current++;
    queue.current = queue.current.then(async () => {
      let session = u.session;
      try {
        session ??= await startUpload(u.workspace, u.parent, u.file, u.target);
        update(u.key, { session, error: '', conflict: false });
        await sendUpload(session, u.file, done => update(u.key, { done }));
        update(u.key, { finished: true, done: u.file.size, session: undefined });
      } catch (e) {
        if (e instanceof UploadInterrupted) {
          update(u.key, { error: e.message, session: e.session });
        } else if (!session && e instanceof HttpError && e.status === 409) {
          // The name was taken (or the file changed) after the batch was checked: ask again.
          update(u.key, { conflict: true, error: '' });
        } else {
          // A refused upload cannot be resumed; free its session now rather than at expiry.
          if (session) void secureFetch(`/api/drive/upload-sessions/${session.id}`, { method: 'DELETE' }).catch(() => {});
          update(u.key, { error: e instanceof Error ? e.message : 'Upload failed', session: undefined });
        }
      } finally {
        if (--pending.current === 0) onIdle();
      }
    });
  }, [onIdle]);

  const add = useCallback(async (workspace: string, parent: string, tree: { dirs: string[][]; files: Entry[] }, folders: Folder[], decide: Decide) => {
    const ids = await makeFolders(workspace, parent, tree.dirs, folders);
    if (tree.files.length === 0) { onIdle(); return; }
    const existing = await liveFiles(workspace);
    const placed = tree.files.map(e => ({ file: e.file, parent: ids.get(e.dirs.join('/')) ?? parent }));
    const clash = (p: { file: File; parent: string }) => existing.find(f => f.parent === p.parent && f.name === p.file.name);
    const clashing = placed.filter(clash);
    const choice = clashing.length ? await decide(clashing.map(p => p.file.name)) : 'cancel';
    const taken = new Map<string, Set<string>>();
    const namesIn = (folder: string) => { let s = taken.get(folder); if (!s) taken.set(folder, s = new Set(existing.filter(f => f.parent === folder).map(f => f.name))); return s; };
    const fresh: Upload[] = [];
    for (const p of placed) {
      const found = clash(p);
      if (found && choice === 'cancel') continue;
      const target = found ? targetFor(choice as 'replace' | 'keep', p.file.name, found, namesIn(p.parent)) : { name: p.file.name };
      if (!found) namesIn(p.parent).add(p.file.name);
      fresh.push({ key: nextKey.current++, file: p.file, workspace, parent: p.parent, target, done: 0, finished: false, error: '', conflict: false });
    }
    if (fresh.length === 0) { onIdle(); return; }
    setItems(xs => [...xs, ...fresh]);
    fresh.forEach(enqueue);
  }, [enqueue, onIdle]);

  const retry = useCallback((u: Upload) => { update(u.key, { error: '' }); enqueue(u); }, [enqueue]);
  // resolve answers a clash found by the server for one upload.
  const resolve = useCallback(async (u: Upload, choice: Choice) => {
    if (choice === 'cancel') { setItems(xs => xs.filter(x => x.key !== u.key)); return; }
    try {
      const existing = await liveFiles(u.workspace);
      const found = existing.find(f => f.parent === u.parent && f.name === u.target.name);
      const taken = new Set([u.target.name, ...existing.filter(f => f.parent === u.parent).map(f => f.name)]);
      // Keep both always picks a new name; Replace with nothing left to replace uploads it as new.
      const target = choice === 'keep' ? { name: freeName(u.target.name, taken) } : found ? targetFor('replace', u.target.name, found, taken) : { name: u.target.name };
      const next = { ...u, target, conflict: false, error: '' };
      update(u.key, next);
      enqueue(next);
    } catch (e) { update(u.key, { error: e instanceof Error ? e.message : 'Upload failed' }); }
  }, [enqueue]);
  const clear = useCallback(() => setItems(xs => xs.filter(x => !x.finished)), []);
  return { items, add, retry, resolve, clear };
}

export function UploadList({ items, retry, resolve, clear }: Pick<ReturnType<typeof useUploads>, 'items' | 'retry' | 'resolve' | 'clear'>) {
  if (items.length === 0) return null;
  const finished = items.filter(item => item.finished).length;
  return <section className="drive-uploads" aria-label="Uploads">
    <div className="drive-uploads-head">
      <span role="status">{finished === items.length ? `${finished} ${finished === 1 ? 'file' : 'files'} uploaded` : `Uploading ${finished} of ${items.length}`}</span>
      {finished > 0 && <button className="btn-ghost btn-sm" onClick={clear}>Clear finished</button>}
    </div>
    <ul>{items.map(u => <li key={u.key}>
      <span title={u.target.name}>{u.target.name}{u.target.replace ? ' · new version' : ''}</span>
      <progress max={u.file.size || 1} value={u.finished ? u.file.size || 1 : u.done} aria-label={`Upload progress for ${u.target.name}`} />
      <span>{u.finished ? 'Done' : u.error || u.conflict ? '' : `${bytes(u.done)} of ${bytes(u.file.size)}`}</span>
      {u.conflict && <div className="drive-upload-problem"><span role="alert">A file named {u.target.name} already exists here.</span>
        <button className="btn-sm" onClick={() => void resolve(u, 'replace')}>Replace (new version)</button>
        <button className="btn-secondary btn-sm" onClick={() => void resolve(u, 'keep')}>Keep both</button>
        <button className="btn-ghost btn-sm" onClick={() => void resolve(u, 'cancel')}>Cancel</button></div>}
      {u.error && <div className="drive-upload-problem"><span role="alert">{u.error}</span><button className="btn-secondary btn-sm" onClick={() => retry(u)}>Retry</button></div>}
    </li>)}</ul>
  </section>;
}
