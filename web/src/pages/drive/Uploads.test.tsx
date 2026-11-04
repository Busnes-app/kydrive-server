import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { UploadList, useUploads } from './Uploads';
import type { Choice } from './Uploads';
import { freeName, numbered } from './upload';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });
const existing = (name: string, id = 'f1', revision = 3) => ({ id, name, parent: '', revision, size: 1, trashed: false });

function Harness({ files, onIdle, decide = async () => 'cancel' }: { files: File[]; onIdle: () => void; decide?: (names: string[]) => Promise<Choice> }) {
  const uploads = useUploads(onIdle);
  return <><button onClick={() => void uploads.add('w1', '', { dirs: [], files: files.map(file => ({ dirs: [], file })) }, [], decide)}>Go</button><UploadList {...uploads} /></>;
}

// server answers like the drive API: the folder listing, session starts (recording their
// bodies), chunk PUTs and completion.
function server({ listing = [] as unknown[], folders = [] as unknown[], refuse = (() => false) as (b: Record<string, unknown>) => boolean, completeFails = [] as (number | 'network' | 'stale')[] } = {}) {
  const starts: Record<string, unknown>[] = [], calls: string[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input), method = init?.method ?? 'GET';
    calls.push(`${method} ${url}`);
    if (url.includes('/files?trash=false')) return json(listing);
    if (url.includes('/folders?trash=false')) return json(folders);
    if (url.endsWith('/upload-sessions')) {
      const b = JSON.parse(String(init?.body));
      starts.push(b);
      if (refuse(b)) return json({ error: 'revision conflict: name or file exists' }, 409);
      return json({ id: String(b.name ?? b.file), size: b.size, received: 0 }, 201);
    }
    if (method === 'PUT') return url.endsWith('/bad.txt') ? json({ error: 'Upload or workspace quota exceeded' }, 413) : new Response(null, { status: 204 });
    if (method === 'DELETE') return new Response(null, { status: 204 });
    if (url.endsWith('/complete') && completeFails.length) {
      const f = completeFails.shift();
      if (f === 'network') throw new TypeError('Failed to fetch');
      if (f === 'stale') return json({ error: 'revision conflict: file changed' }, 409);
      return json({ error: 'revision conflict: upload incomplete or already completing' }, f);
    }
    return json({ id: 'f', name: 'ok.txt', parent: '', revision: 1, size: 2, trashed: false }, 201);
  }));
  return { starts, calls };
}

describe('upload queue', () => {
  it('uploads, reloads once when idle and frees a refused session', async () => {
    const { calls } = server();
    const idle = vi.fn();
    render(<Harness files={[new File(['ok'], 'ok.txt'), new File(['no'], 'bad.txt')]} onIdle={idle} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    await screen.findByText('Upload or workspace quota exceeded');
    await waitFor(() => expect(idle).toHaveBeenCalledTimes(1));
    expect(screen.getByText('Done')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy();
    expect(calls).toContain('DELETE /api/drive/upload-sessions/bad.txt');
    fireEvent.click(screen.getByRole('button', { name: 'Clear finished' }));
    expect(screen.queryByText('ok.txt')).toBeNull();
    expect(screen.getByText('bad.txt')).toBeTruthy();
  });

  it('asks once about taken names and uploads a new version on Replace', async () => {
    const { starts } = server({ listing: [existing('report.txt')] });
    const decide = vi.fn(async () => 'replace' as const);
    render(<Harness files={[new File(['v2'], 'report.txt'), new File(['x'], 'new.txt')]} onIdle={() => {}} decide={decide} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    await waitFor(() => expect(starts).toHaveLength(2));
    expect(decide).toHaveBeenCalledWith(['report.txt']);
    expect(starts).toEqual([{ file: 'f1', revision: 3, size: 2 }, { parent: '', name: 'new.txt', size: 1 }]);
    expect(await screen.findByText('report.txt · new version')).toBeTruthy();
  });

  it('keeps both under the first free numbered name', async () => {
    const { starts } = server({ listing: [existing('report.txt'), existing('report (2).txt', 'f2')] });
    render(<Harness files={[new File(['v2'], 'report.txt')]} onIdle={() => {}} decide={async () => 'keep'} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    await waitFor(() => expect(starts).toEqual([{ parent: '', name: 'report (3).txt', size: 2 }]));
  });

  it('skips only the clashing files on Cancel', async () => {
    const { starts } = server({ listing: [existing('report.txt')] });
    const idle = vi.fn();
    render(<Harness files={[new File(['v2'], 'report.txt'), new File(['x'], 'new.txt')]} onIdle={idle} decide={async () => 'cancel'} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    await waitFor(() => expect(idle).toHaveBeenCalled());
    expect(starts).toEqual([{ parent: '', name: 'new.txt', size: 1 }]);
    expect(screen.queryByText('report.txt')).toBeNull();
  });

  it('offers the same choice when the server finds the name taken', async () => {
    let taken = false;
    const { starts } = server({ refuse: b => { if (b.name === 'late.txt' && !taken) { taken = true; return true; } return false; } });
    render(<Harness files={[new File(['a'], 'late.txt')]} onIdle={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    expect((await screen.findByRole('alert')).textContent).toBe('A file named late.txt already exists here.');
    expect(screen.queryByText(/revision conflict/)).toBeNull();
    expect(screen.getByRole('button', { name: 'Replace (new version)' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Keep both' }));
    await waitFor(() => expect(starts.at(-1)).toEqual({ parent: '', name: 'late (2).txt', size: 1 }));
    expect(await screen.findByText('Done')).toBeTruthy();
  });

  it('takes a new name on Replace when a folder holds the name', async () => {
    let refused = false;
    const { starts } = server({ folders: [{ id: 'd1', name: 'Backup', parent: '', trashed: false }], refuse: b => { if (b.name === 'Backup' && !refused) { refused = true; return true; } return false; } });
    render(<Harness files={[new File(['a'], 'Backup')]} onIdle={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Replace (new version)' }));
    await waitFor(() => expect(starts.at(-1)).toEqual({ parent: '', name: 'Backup (2)', size: 1 }));
    expect(await screen.findByText('Done')).toBeTruthy();
  });

  it.each([['in transit', 'network'], ['behind a proxy timeout', 504], ['while the server still completes it', 409]] as const)('keeps the staged bytes when completion fails %s, so Retry completes them', async (_, failure) => {
    const { starts, calls } = server({ completeFails: [failure] });
    render(<Harness files={[new File(['ok'], 'ok.txt')]} onIdle={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Done')).toBeTruthy();
    expect(starts).toHaveLength(1);
    expect(calls.filter(c => c.startsWith('DELETE'))).toEqual([]);
    // Retry goes straight to completion: every byte was acknowledged, none is sent again.
    expect(calls.filter(c => c.startsWith('PUT'))).toHaveLength(1);
  });

  it('says a refused retry of a lost completion may already be saved, never Done', async () => {
    server({ completeFails: [504, 403] });
    render(<Harness files={[new File(['ok'], 'ok.txt')]} onIdle={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));
    expect(await screen.findByText(/may already be in the folder/)).toBeTruthy();
    expect(screen.queryByText('Done')).toBeNull();
  });

  it('reports a first completion refused with 403 as the server said', async () => {
    server({ completeFails: [403] });
    render(<Harness files={[new File(['ok'], 'ok.txt')]} onIdle={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    expect(await screen.findByText(/revision conflict/)).toBeTruthy();
    expect(screen.queryByText(/may already be in the folder/)).toBeNull();
  });

  it.each([['with a server error', 500], ['because the file changed since the start', 'stale']] as const)('frees the session when completion fails for good %s', async (_, failure) => {
    const { calls } = server({ completeFails: [failure] });
    render(<Harness files={[new File(['ok'], 'ok.txt')]} onIdle={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    await screen.findByRole('button', { name: 'Retry' });
    await waitFor(() => expect(calls).toContain('DELETE /api/drive/upload-sessions/ok.txt'));
  });
});

describe('numbered names', () => {
  it('matches the server: before the extension, dotfiles whole', () => {
    expect(numbered('Q4 budget.csv', 2)).toBe('Q4 budget (2).csv');
    expect(numbered('archive.tar.gz', 3)).toBe('archive.tar (3).gz');
    expect(numbered('.bashrc', 2)).toBe('.bashrc (2)');
    expect(numbered('README', 2)).toBe('README (2)');
    expect(freeName('a.txt', new Set(['a.txt', 'a (2).txt']))).toBe('a (3).txt');
  });
});
