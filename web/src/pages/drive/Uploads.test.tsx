import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { UploadList, useUploads } from './Uploads';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });

function Harness({ files, onIdle }: { files: File[]; onIdle: () => void }) {
  const uploads = useUploads(onIdle);
  return <><button onClick={() => void uploads.add('w1', '', { dirs: [], files: files.map(file => ({ dirs: [], file })) }, [])}>Go</button><UploadList {...uploads} /></>;
}

describe('upload queue', () => {
  it('uploads, reloads once when idle and frees a refused session', async () => {
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input), method = init?.method ?? 'GET';
      calls.push(`${method} ${url}`);
      if (url.endsWith('/upload-sessions')) { const b = JSON.parse(String(init?.body)); return json({ id: b.name, size: b.size, received: 0 }, 201); }
      if (method === 'PUT') return url.endsWith('/bad.txt') ? json({ error: 'Upload or workspace quota exceeded' }, 413) : new Response(null, { status: 204 });
      if (method === 'DELETE') return new Response(null, { status: 204 });
      return json({ id: 'f', name: 'ok.txt', parent: '', revision: 1, size: 2, trashed: false }, 201);
    }));
    const idle = vi.fn();
    render(<Harness files={[new File(['ok'], 'ok.txt'), new File(['no'], 'bad.txt')]} onIdle={idle} />);
    fireEvent.click(screen.getByRole('button', { name: 'Go' }));
    await screen.findByText('Upload or workspace quota exceeded');
    await waitFor(() => expect(idle).toHaveBeenCalledTimes(1));
    expect(screen.getByText('Done')).toBeTruthy();
    expect(screen.getByRole('status').textContent).toContain('1 file uploaded');
    expect(calls).toContain('DELETE /api/drive/upload-sessions/bad.txt');
    fireEvent.click(screen.getByRole('button', { name: 'Clear finished' }));
    expect(screen.queryByText('ok.txt')).toBeNull();
    expect(screen.getByText('bad.txt')).toBeTruthy();
  });
});
