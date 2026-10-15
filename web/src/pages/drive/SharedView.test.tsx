import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
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
    expect(await screen.findByText('Finance · editor')).toBeTruthy();
    expect(screen.getByText('Ann Lee · reader')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'draft.docx' }).getAttribute('href')).toBe('/editor.html?file=f9');
    fireEvent.click(screen.getByRole('button', { name: 'Budgets/' }));
    expect(await screen.findByRole('link', { name: 'q1.xlsx' })).toBeTruthy();
    expect(screen.getByText('2.0 KiB')).toBeTruthy();
    expect(screen.getByRole('button', { name: '2026/' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Shared with me' }));
    expect(await screen.findByText('Finance · editor')).toBeTruthy();
  });

  it('says when nothing is shared', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json([])));
    render(<SharedView openInNewTab={false} />);
    expect(await screen.findByText('Nothing shared with you yet')).toBeTruthy();
  });
});
