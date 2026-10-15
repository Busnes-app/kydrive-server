import { secureFetch } from '../../api';
import { change, file, folder, record, response } from './client';
import type { DriveFile, Folder } from './client';

// Resumable uploads: the server counts what it has stored, so every resume starts from its count.
export const CHUNK = 32 << 20;

export type Session = { id: string; size: number; received: number };
export function session(v: unknown): v is Session { return record(v) && typeof v.id === 'string' && typeof v.size === 'number' && typeof v.received === 'number'; }

// UploadInterrupted keeps the session so Retry resumes instead of starting over.
export class UploadInterrupted extends Error { constructor(readonly session: Session) { super('Upload interrupted; retry to resume'); } }

type Options = { chunk?: number; retries?: number; wait?: (ms: number) => Promise<void> };
const pause = (ms: number) => new Promise<void>(r => setTimeout(r, ms));

export async function startUpload(workspace: string, parent: string, f: File): Promise<Session> {
  const v = await change(`/api/drive/workspaces/${workspace}/upload-sessions`, 'POST', { parent, name: f.name, size: f.size });
  if (!session(v)) throw new Error('Invalid upload response');
  return v;
}

export async function sendUpload(s: Session, f: File, onProgress: (done: number) => void, { chunk = CHUNK, retries = 3, wait = pause }: Options = {}): Promise<DriveFile> {
  const url = `/api/drive/upload-sessions/${s.id}`;
  let offset = s.received, failures = 0;
  onProgress(offset);
  while (offset < s.size) {
    const end = Math.min(offset + chunk, s.size);
    let r: Response;
    try {
      r = await secureFetch(url, { method: 'PUT', headers: { 'Content-Range': `bytes ${offset}-${end - 1}/${s.size}`, 'Content-Type': 'application/octet-stream' }, body: f.slice(offset, end) });
    } catch {
      if (++failures > retries) throw new UploadInterrupted({ ...s, received: offset });
      await wait(1000 * 2 ** failures);
      // The chunk may have landed before the connection dropped; ask.
      try { const v = await response(await fetch(url)); if (session(v)) offset = v.received; } catch { /* retry the same chunk */ }
      continue;
    }
    const resume = Number(r.headers.get('Upload-Offset'));
    if (r.status === 409 && Number.isSafeInteger(resume) && resume !== offset) { offset = resume; onProgress(offset); continue; }
    if (!r.ok) await response(r); // throws the server's message
    offset = end; failures = 0;
    onProgress(offset);
  }
  const done = await response(await secureFetch(`${url}/complete`, { method: 'POST' }));
  if (!file(done)) throw new Error('Invalid upload response');
  return done;
}

// A file to upload and the folder names leading to it from the upload target.
export type Entry = { dirs: string[]; file: File };

// fromDataTransfer must run inside the drop handler: the items are gone once it returns.
export function fromDataTransfer(dt: DataTransfer): Promise<{ dirs: string[][]; files: Entry[] }> {
  const roots = Array.from(dt.items).map(i => i.webkitGetAsEntry()).filter((e): e is FileSystemEntry => e !== null);
  return walk(roots);
}

export async function walk(roots: FileSystemEntry[]): Promise<{ dirs: string[][]; files: Entry[] }> {
  const dirs: string[][] = [], files: Entry[] = [];
  const visit = async (entry: FileSystemEntry, path: string[]): Promise<void> => {
    if (entry.isFile) {
      files.push({ dirs: path, file: await new Promise<File>((ok, fail) => (entry as FileSystemFileEntry).file(ok, fail)) });
      return;
    }
    const here = [...path, entry.name];
    dirs.push(here);
    const reader = (entry as FileSystemDirectoryEntry).createReader();
    // readEntries returns at most ~100 entries per call; read until it returns none.
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((ok, fail) => reader.readEntries(ok, fail));
      if (batch.length === 0) break;
      for (const child of batch) await visit(child, here);
    }
  };
  for (const root of roots) await visit(root, []);
  return { dirs, files };
}

// fromFolderInput reads <input webkitdirectory> files, whose relative paths name their folders.
export function fromFolderInput(list: FileList): { dirs: string[][]; files: Entry[] } {
  const files = Array.from(list).map(f => ({ dirs: f.webkitRelativePath.split('/').slice(0, -1), file: f }));
  const seen = new Set<string>(), dirs: string[][] = [];
  for (const { dirs: d } of files) for (let i = 1; i <= d.length; i++) { const key = d.slice(0, i).join('/'); if (!seen.has(key)) { seen.add(key); dirs.push(d.slice(0, i)); } }
  return { dirs, files };
}

// makeFolders creates the dropped folder tree under parent, parents first, reusing folders that
// already exist. It returns the folder id for each path ("" for the target itself).
export async function makeFolders(workspace: string, parent: string, dirs: string[][], existing: Folder[]): Promise<Map<string, string>> {
  const ids = new Map<string, string>([['', parent]]);
  const known = [...existing];
  for (const path of [...dirs].sort((a, b) => a.length - b.length)) {
    const key = path.join('/'), under = ids.get(path.slice(0, -1).join('/')) ?? parent, name = path[path.length - 1];
    if (ids.has(key)) continue;
    let found = known.find(d => !d.trashed && d.parent === under && d.name === name);
    if (!found) {
      const created = await change(`/api/drive/workspaces/${workspace}/folders`, 'POST', { parent: under, name });
      if (!folder(created)) throw new Error('Invalid folder response');
      known.push(created);
      found = created;
    }
    ids.set(key, found.id);
  }
  return ids;
}
