import { afterEach, describe, expect, it, vi } from 'vitest';
import { UploadInterrupted, fromFolderInput, makeFolders, sendUpload, startUpload, walk } from './upload';

afterEach(() => { vi.unstubAllGlobals(); });

const json = (v: unknown, status = 200, headers: Record<string, string> = {}) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json', ...headers } });
const done = { id: 'f1', name: 'a.bin', parent: '', revision: 1, size: 10, trashed: false };
const now = () => Promise.resolve();

type Call = { url: string; method: string; range: string | null; body: unknown };
function server(handle: (c: Call, n: number) => Response | Promise<Response>) {
  const calls: Call[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const c = { url: String(input), method: init?.method ?? 'GET', range: new Headers(init?.headers).get('Content-Range'), body: init?.body };
    calls.push(c);
    return handle(c, calls.length);
  }));
  return calls;
}

describe('resumable upload', () => {
  it('starts a session with the file name, size and target folder', async () => {
    const calls = server(() => json({ id: 's1', size: 10, received: 0 }, 201));
    expect(await startUpload('w1', 'd1', new File(['0123456789'], 'a.bin'))).toEqual({ id: 's1', size: 10, received: 0 });
    expect(calls[0].url).toBe('/api/drive/workspaces/w1/upload-sessions');
    expect(JSON.parse(String(calls[0].body))).toEqual({ parent: 'd1', name: 'a.bin', size: 10 });
  });

  it('sends Content-Range chunks then completes, reporting acknowledged bytes', async () => {
    const calls = server(c => c.method === 'PUT' ? new Response(null, { status: 204 }) : json(done, 201));
    const progress: number[] = [];
    const f = await sendUpload({ id: 's1', size: 10, received: 0 }, new File(['0123456789'], 'a.bin'), n => progress.push(n), { chunk: 4 });
    expect(f).toEqual(done);
    expect(calls.map(c => c.range ?? `${c.method} ${c.url}`)).toEqual(['bytes 0-3/10', 'bytes 4-7/10', 'bytes 8-9/10', 'POST /api/drive/upload-sessions/s1/complete']);
    expect(progress).toEqual([0, 4, 8, 10]);
  });

  it('resumes from the server count after a dropped connection', async () => {
    const calls = server((c, n) => {
      if (n === 2) throw new TypeError('network down');
      if (c.method === 'GET') return json({ id: 's1', size: 10, received: 8 }); // the dropped chunk landed
      return c.method === 'PUT' ? new Response(null, { status: 204 }) : json(done, 201);
    });
    await sendUpload({ id: 's1', size: 10, received: 0 }, new File(['0123456789'], 'a.bin'), () => {}, { chunk: 4, wait: now });
    expect(calls.map(c => c.range ?? c.method)).toEqual(['bytes 0-3/10', 'bytes 4-7/10', 'GET', 'bytes 8-9/10', 'POST']);
  });

  it('jumps to Upload-Offset on a conflict', async () => {
    const calls = server((c, n) => n === 1 ? json({ error: 'conflict' }, 409, { 'Upload-Offset': '4' }) : c.method === 'PUT' ? new Response(null, { status: 204 }) : json(done, 201));
    await sendUpload({ id: 's1', size: 10, received: 0 }, new File(['0123456789'], 'a.bin'), () => {}, { chunk: 4 });
    expect(calls.map(c => c.range ?? c.method)).toEqual(['bytes 0-3/10', 'bytes 4-7/10', 'bytes 8-9/10', 'POST']);
  });

  it('keeps the session for Retry once retries run out', async () => {
    server((c, n) => { if (n === 1) return new Response(null, { status: 204 }); if (c.method === 'GET') return json({ id: 's1', size: 10, received: 4 }); throw new TypeError('offline'); });
    const err = await sendUpload({ id: 's1', size: 10, received: 0 }, new File(['0123456789'], 'a.bin'), () => {}, { chunk: 4, retries: 2, wait: now }).catch(e => e);
    expect(err).toBeInstanceOf(UploadInterrupted);
    expect((err as UploadInterrupted).session).toEqual({ id: 's1', size: 10, received: 4 });
  });

  it('surfaces a refusal without retrying', async () => {
    const calls = server(() => json({ error: 'Upload or workspace quota exceeded' }, 413));
    await expect(sendUpload({ id: 's1', size: 10, received: 0 }, new File(['0123456789'], 'a.bin'), () => {})).rejects.toThrow('quota exceeded');
    expect(calls).toHaveLength(1);
  });
});

describe('folder uploads', () => {
  it('creates folders parents first and reuses existing ones', async () => {
    let next = 0;
    const calls = server(c => { const b = JSON.parse(String(c.body)); return json({ id: `new${++next}`, name: b.name, parent: b.parent, trashed: false }, 201); });
    const ids = await makeFolders('w1', 'top', [['A', 'B'], ['C'], ['A']], [{ id: 'a', name: 'A', parent: 'top', trashed: false }, { id: 'x', name: 'C', parent: 'top', trashed: true }]);
    expect(calls.map(c => JSON.parse(String(c.body)))).toEqual([{ parent: 'top', name: 'C' }, { parent: 'a', name: 'B' }]);
    expect(Object.fromEntries(ids)).toEqual({ '': 'top', A: 'a', C: 'new1', 'A/B': 'new2' });
  });

  it('reads folder paths from a directory picker', () => {
    const f = (path: string) => { const x = new File(['x'], path.split('/').pop()!); Object.defineProperty(x, 'webkitRelativePath', { value: path }); return x; };
    const list = [f('Top/a.txt'), f('Top/Sub/b.txt')] as unknown as FileList;
    const out = fromFolderInput(list);
    expect(out.dirs).toEqual([['Top'], ['Top', 'Sub']]);
    expect(out.files.map(e => [e.dirs, e.file.name])).toEqual([[['Top'], 'a.txt'], [['Top', 'Sub'], 'b.txt']]);
  });

  it('walks dropped directories until readEntries is empty', async () => {
    const fileEntry = (name: string) => ({ isFile: true, name, file: (ok: (f: File) => void) => ok(new File(['x'], name)) });
    const dirEntry = (name: string, batches: unknown[][]) => ({ isFile: false, name, createReader: () => ({ readEntries: (ok: (e: unknown[]) => void) => ok(batches.shift() ?? []) }) });
    const root = dirEntry('Drop', [[fileEntry('1.txt')], [dirEntry('Inner', [[fileEntry('2.txt')]])]]);
    const out = await walk([root as unknown as FileSystemEntry, fileEntry('loose.txt') as unknown as FileSystemEntry]);
    expect(out.dirs).toEqual([['Drop'], ['Drop', 'Inner']]);
    expect(out.files.map(e => `${e.dirs.join('/')}:${e.file.name}`)).toEqual(['Drop:1.txt', 'Drop/Inner:2.txt', ':loose.txt']);
  });
});
