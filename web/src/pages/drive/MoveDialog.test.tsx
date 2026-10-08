import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MoveDialog } from './MoveDialog';

const workspaces = [
  { id: 'w1', name: 'My files', role: 'manager', kind: 'personal' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0 },
  { id: 'w2', name: 'Team', role: 'editor', kind: 'shared' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0 },
  { id: 'w3', name: 'Readonly', role: 'reader', kind: 'shared' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0 },
];
const file = { id: 'f1', name: 'a.docx', parent: '', revision: 3, size: 1, trashed: false };

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe('MoveDialog', () => {
  it('moves a file into a folder of another workspace', async () => {
    const calls: [string, RequestInit | undefined][] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push([url, init]);
      const body = url.endsWith('/w2/folders') ? [{ id: 'd2', name: 'Reports', parent: '', trashed: false }] : url.endsWith('/w1/folders') ? [] : { ...file, workspace: 'w2', parent: 'd2' };
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }));
    const done = vi.fn();
    render(<MoveDialog mode="move" item={{ kind: 'file', file }} workspaceID="w1" workspaces={workspaces} onClose={() => {}} onDone={done} />);
    expect(screen.queryByRole('option', { name: 'Readonly' })).toBeNull();
    fireEvent.change(screen.getByLabelText('Workspace'), { target: { value: 'w2' } });
    await screen.findByRole('option', { name: 'Reports' });
    fireEvent.change(screen.getByLabelText('Folder'), { target: { value: 'd2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Move' }));
    await waitFor(() => expect(done).toHaveBeenCalled());
    const [url, init] = calls[calls.length - 1];
    expect(url).toBe('/api/drive/files/f1/location');
    expect(init?.method).toBe('PUT');
    expect(JSON.parse(String(init?.body))).toEqual({ revision: 3, workspace: 'w2', parent: 'd2', name: 'a.docx' });
  });

  it('hides a folder and its descendants as move targets', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify([
      { id: 'a', name: 'A', parent: '', trashed: false },
      { id: 'b', name: 'B', parent: 'a', trashed: false },
      { id: 'c', name: 'C', parent: '', trashed: false },
    ]), { status: 200 })));
    render(<MoveDialog mode="move" item={{ kind: 'folder', folder: { id: 'a', name: 'A', parent: '', trashed: false } }} workspaceID="w1" workspaces={workspaces} onClose={() => {}} onDone={() => {}} />);
    await screen.findByRole('option', { name: 'C' });
    expect(screen.queryByRole('option', { name: 'B' })).toBeNull();
    expect(screen.queryByLabelText('Workspace')).toBeNull();
  });

  it('does not offer other workspaces when moving from a non-manager workspace', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200 })));
    render(<MoveDialog mode="move" item={{ kind: 'file', file }} workspaceID="w2" workspaces={workspaces} onClose={() => {}} onDone={() => {}} />);
    await screen.findByRole('option', { name: 'Workspace root' });
    expect(screen.queryByLabelText('Workspace')).toBeNull();
    expect(screen.queryByRole('option', { name: 'My files' })).toBeNull();
  });
});
