import { afterEach, describe, expect, it, vi } from 'vitest';
import { parentsFirst, setTrashBatch, trashNotice } from './Drive';

describe('trashNotice', () => {
  it('names a workspace\'s own trash period', () => {
    expect(trashNotice(30)).toBe('Items here are deleted permanently after 30 days.');
  });
  it('never promises to keep trash when the workspace follows the drive-wide period', () => {
    expect(trashNotice(0)).toBe('Items here may be deleted permanently after the trash period your administrator sets.');
  });
});

describe('parentsFirst', () => {
  it('restores a trashed folder before the trashed folders inside it, whatever the listing order', () => {
    const f = (id: string, parent: string) => ({ id, name: id, parent, trashed: true });
    const trashed = [f('a-docs', 'z-project'), f('deep', 'a-docs'), f('z-project', ''), f('other', 'live')];
    expect(parentsFirst(trashed, trashed).map(d => d.id)).toEqual(['z-project', 'other', 'a-docs', 'deep']);
  });
});

describe('setTrashBatch', () => {
  afterEach(() => vi.unstubAllGlobals());
  const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });
  const dir = (id: string, parent: string) => ({ id, name: id, parent, trashed: true });
  const doc = (id: string, parent: string) => ({ id, name: id, parent, revision: 1, size: 1, trashed: true });

  // Z-Project/A-Docs/file.txt trashed together, plus Z-Project/loose.txt trashed on its own first.
  function server() {
    const trash = new Set(['z', 'a', 'f', 'loose']), batch = new Set(['z', 'a', 'f']), calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === 'PUT') {
        const id = url.split('/')[4];
        calls.push(id);
        if (!trash.has(id)) return json({ error: 'conflict' }, 409);
        if (id === 'a' && trash.has('z')) throw new Error('child restored before its parent');
        for (const x of id === 'z' ? batch : [id]) trash.delete(x);
        return json({});
      }
      calls.push('list');
      return json(url.includes('/files') ? [doc('f', 'a'), doc('loose', 'z')].filter(x => trash.has(x.id)) : [dir('z', ''), dir('a', 'z')].filter(x => trash.has(x.id)));
    }));
    return { trash, calls };
  }

  it('restores a selected tree parents first, counts items its folder brought back as done, and lists the trash once', async () => {
    const { trash, calls } = server();
    await setTrashBatch('w1', [dir('a', 'z'), dir('z', '')], [doc('f', 'a'), doc('loose', 'z')], [dir('a', 'z'), dir('z', '')], false);
    expect(trash.size).toBe(0);
    expect(calls).toEqual(['z', 'a', 'list', 'list', 'f', 'loose']);
  });

  it('still fails an item that stays in the trash', async () => {
    server();
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => init?.method === 'PUT' ? json({ error: 'name exists' }, 409) : json(String(input).includes('/files') ? [doc('f', '')] : [])));
    await expect(setTrashBatch('w1', [], [doc('f', '')], [], false)).rejects.toThrow('name exists');
  });
});
