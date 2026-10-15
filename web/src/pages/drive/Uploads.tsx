import { useCallback, useRef, useState } from 'react';
import { secureFetch } from '../../api';
import { bytes } from './client';
import type { Folder } from './client';
import { UploadInterrupted, makeFolders, sendUpload, startUpload } from './upload';
import type { Entry, Session } from './upload';

type Upload = { key: number; file: File; workspace: string; parent: string; done: number; finished: boolean; error: string; session?: Session };

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
        session ??= await startUpload(u.workspace, u.parent, u.file);
        update(u.key, { session, error: '' });
        await sendUpload(session, u.file, done => update(u.key, { done }));
        update(u.key, { finished: true, done: u.file.size, session: undefined });
      } catch (e) {
        if (e instanceof UploadInterrupted) {
          update(u.key, { error: e.message, session: e.session });
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

  const add = useCallback(async (workspace: string, parent: string, tree: { dirs: string[][]; files: Entry[] }, folders: Folder[]) => {
    const ids = await makeFolders(workspace, parent, tree.dirs, folders);
    if (tree.files.length === 0) { onIdle(); return; }
    const fresh = tree.files.map(e => ({ key: nextKey.current++, file: e.file, workspace, parent: ids.get(e.dirs.join('/')) ?? parent, done: 0, finished: false, error: '' }));
    setItems(xs => [...xs, ...fresh]);
    fresh.forEach(enqueue);
  }, [enqueue, onIdle]);

  const retry = useCallback((u: Upload) => { update(u.key, { error: '' }); enqueue(u); }, [enqueue]);
  const clear = useCallback(() => setItems(xs => xs.filter(x => !x.finished)), []);
  return { items, add, retry, clear };
}

export function UploadList({ items, retry, clear }: Pick<ReturnType<typeof useUploads>, 'items' | 'retry' | 'clear'>) {
  if (items.length === 0) return null;
  return <section className="drive-uploads" aria-label="Uploads">
    <ul>{items.map(u => <li key={u.key}>
      <span>{u.file.name}</span>
      <progress max={u.file.size || 1} value={u.finished ? u.file.size || 1 : u.done} aria-label={`Upload progress for ${u.file.name}`} />
      <span>{u.finished ? 'Done' : u.error ? '' : `${bytes(u.done)} of ${bytes(u.file.size)}`}</span>
      {u.error && <><span role="alert">{u.error}</span><button className="btn-secondary" onClick={() => retry(u)}>Retry</button></>}
    </li>)}</ul>
    {items.some(u => u.finished) && <button className="btn-secondary" onClick={clear}>Clear finished</button>}
  </section>;
}
