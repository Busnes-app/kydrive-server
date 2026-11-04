import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { SharedView } from './SharedView';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const json = (v: unknown) => new Response(JSON.stringify(v), { status: 200, headers: { 'Content-Type': 'application/json' } });

describe('SharedView', () => {
  it('lists shared items and browses into a shared folder', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/drive/shared-with-me') return json([
        { kind: 'folder', id: 'd1', workspace: 'w1', workspace_name: 'Finance', parent: '', name: 'Budgets', role: 'editor' },
        { kind: 'file', id: 'f9', workspace: 'w2', workspace_name: 'Ann Lee', parent: '', name: 'draft.docx', role: 'reader' },
      ]);
      if (url === '/api/drive/folders/d1/children') return json({ folder: { id: 'd1', name: 'Budgets', parent: '', trashed: false }, folders: [{ id: 'd2', name: '2026', parent: 'd1', trashed: false }], files: [{ id: 'f1', name: 'q1.xlsx', parent: 'd1', revision: 2, size: 2048, trashed: false }] });
      return json({ folder: {}, folders: [], files: [] });
    }));
    render(<SharedView openInNewTab={false} />);
    expect((await screen.findAllByText('Finance · Editor')).length).toBeGreaterThan(0);
    expect(screen.getAllByText('Ann Lee · Reader').length).toBeGreaterThan(0);
    expect(screen.getByRole('link', { name: 'draft.docx' }).getAttribute('href')).toBe('/editor.html?file=f9');
    fireEvent.click(screen.getByRole('button', { name: 'Budgets' }));
    expect(await screen.findByRole('link', { name: 'q1.xlsx' })).toBeTruthy();
    expect(screen.getAllByText('2.0 KiB').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: '2026' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Actions for q1.xlsx' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Shared with me' }));
    expect((await screen.findAllByText('Finance · Editor')).length).toBeGreaterThan(0);
  });

  it('says when nothing is shared', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json([])));
    render(<SharedView openInNewTab={false} />);
    expect(await screen.findByText('Nothing shared with you yet')).toBeTruthy();
  });

  it('lets an editor add and manage files inside a shared folder, never at the top', async () => {
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input), method = init?.method ?? 'GET';
      if (method !== 'GET') calls.push(`${method} ${url} ${String(init?.body ?? '')}`);
      if (url === '/api/drive/shared-with-me') return json([
        { kind: 'folder', id: 'd1', workspace: 'w1', workspace_name: 'Finance', parent: '', name: 'Budgets', role: 'editor' },
        { kind: 'folder', id: 'r1', workspace: 'w1', workspace_name: 'Finance', parent: '', name: 'Read only', role: 'reader' },
      ]);
      if (url === '/api/drive/folders/d1/children') return json({ folders: [{ id: 'd2', name: '2026', parent: 'd1', trashed: false }], files: [{ id: 'f1', name: 'q1.xlsx', parent: 'd1', revision: 2, size: 2048, trashed: false }] });
      if (url.endsWith('/folders') && method === 'POST') return json({ id: 'd3', name: 'Drafts', parent: 'd1', trashed: false });
      return json({ folders: [], files: [] });
    }));
    render(<SharedView openInNewTab={false} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Actions for folder Budgets' }));
    expect(screen.queryByRole('menuitem', { name: 'Rename' })).toBeNull();
    expect(screen.queryByRole('menuitem', { name: 'Move to trash' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Upload files' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Budgets' }));
    expect(await screen.findByRole('link', { name: 'q1.xlsx' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Upload files' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'New' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'New folder' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Folder name'), { target: { value: 'Drafts' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(calls).toContain('POST /api/drive/workspaces/w1/folders {"parent":"d1","name":"Drafts"}'));
    fireEvent.click(screen.getByRole('button', { name: 'Actions for q1.xlsx' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Move to trash' }));
    await waitFor(() => expect(calls).toContain('PUT /api/drive/files/f1/trash {"revision":2,"trashed":true}'));
    fireEvent.click(screen.getByRole('button', { name: 'Actions for folder 2026' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Rename' }));
    const rename = await screen.findByRole('dialog');
    fireEvent.change(within(rename).getByLabelText('Name'), { target: { value: '2027' } });
    fireEvent.click(within(rename).getByRole('button', { name: 'Rename' }));
    await waitFor(() => expect(calls).toContain('PUT /api/drive/folders/d2/location {"parent":"d1","name":"2027"}'));
  });

  it('offers a reader no changes inside a shared folder', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => String(input) === '/api/drive/shared-with-me'
      ? json([{ kind: 'folder', id: 'r1', workspace: 'w1', workspace_name: 'Finance', parent: '', name: 'Read only', role: 'reader' }])
      : json({ folders: [], files: [{ id: 'f1', name: 'q1.xlsx', parent: 'r1', revision: 2, size: 2048, trashed: false }] })));
    render(<SharedView openInNewTab={false} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Read only' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Actions for q1.xlsx' }));
    expect(screen.queryByRole('menuitem', { name: 'Move to trash' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Upload files' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'New' })).toBeNull();
  });
});
