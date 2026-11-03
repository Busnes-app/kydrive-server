import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { WorkspaceAdmin } from './WorkspaceAdmin';

const studio = { id: 'ws1', name: 'Studio', role: '', kind: 'shared' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0, editors_share: false, items: 0 };
const full = { ...studio, items: 12 };

function stub(status: number, error = '') {
  const calls: [string, RequestInit | undefined][] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push([String(input), init]);
    return new Response(JSON.stringify(status === 200 ? { applied: true } : { error }), { status });
  }));
  return calls;
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe('WorkspaceAdmin', () => {
  it('renames the workspace', async () => {
    const calls = stub(200);
    const renamed = vi.fn();
    render(<WorkspaceAdmin workspace={studio} onRenamed={renamed} onDeleted={() => {}} />);
    const rename = screen.getByRole('button', { name: 'Rename' }) as HTMLButtonElement;
    expect(rename.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: ' Design ' } });
    fireEvent.click(rename);
    await waitFor(() => expect(renamed).toHaveBeenCalled());
    expect(calls[0][0]).toBe('/api/drive/workspaces/ws1/name');
    expect(calls[0][1]?.method).toBe('PUT');
    expect(JSON.parse(String(calls[0][1]?.body))).toEqual({ name: 'Design' });
  });

  it('explains a taken name', async () => {
    stub(409, 'revision conflict: workspace name exists');
    const renamed = vi.fn();
    render(<WorkspaceAdmin workspace={studio} onRenamed={renamed} onDeleted={() => {}} />);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Other' } });
    fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
    expect((await screen.findByRole('alert')).textContent).toBe('A shared workspace named “Other” already exists.');
    expect(renamed).not.toHaveBeenCalled();
  });

  it('deletes only after the danger confirmation', async () => {
    const calls = stub(200);
    const deleted = vi.fn();
    render(<WorkspaceAdmin workspace={studio} onRenamed={() => {}} onDeleted={deleted} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete workspace' }));
    const ask = await screen.findByRole('dialog');
    expect(ask.textContent).toContain('Delete Studio?');
    const button = within(ask).getByRole('button', { name: 'Delete workspace' });
    expect(button.className).toBe('btn-danger');
    fireEvent.click(button);
    await waitFor(() => expect(deleted).toHaveBeenCalled());
    expect(calls).toHaveLength(1);
    expect(calls[0][0]).toBe('/api/drive/workspaces/ws1');
    expect(calls[0][1]?.method).toBe('DELETE');
  });

  it('sends nothing when the confirmation is declined', async () => {
    const calls = stub(200);
    render(<WorkspaceAdmin workspace={studio} onRenamed={() => {}} onDeleted={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete workspace' }));
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(calls).toHaveLength(0);
  });

  it('explains a name held by a deleted workspace', async () => {
    stub(409, 'revision conflict: workspace name exists in Deleted workspaces');
    render(<WorkspaceAdmin workspace={studio} onRenamed={() => {}} onDeleted={() => {}} />);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Old' } });
    fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
    expect((await screen.findByRole('alert')).textContent).toContain('A deleted workspace named “Old” is waiting in Deleted workspaces');
  });

  it('explains a workspace that changed before the delete', async () => {
    stub(409, 'revision conflict: workspace has uploads in progress');
    const deleted = vi.fn();
    render(<WorkspaceAdmin workspace={studio} onRenamed={() => {}} onDeleted={deleted} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete workspace' }));
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Delete workspace' }));
    expect((await screen.findByRole('alert')).textContent).toContain('Studio gained files or has an upload in progress');
    expect(deleted).not.toHaveBeenCalled();
  });

  it('asks for the name before deleting a workspace with files, and says what happens to them', async () => {
    const calls: [string, RequestInit | undefined][] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push([String(input), init]);
      return new Response(JSON.stringify(String(input).endsWith('trash-retention') ? { trash_days: 30 } : { deleted: true, recoverable: true }), { status: 200 });
    }));
    const deleted = vi.fn();
    render(<WorkspaceAdmin workspace={full} onRenamed={() => {}} onDeleted={deleted} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete workspace' }));
    const ask = await screen.findByRole('dialog');
    expect(ask.textContent).toContain('its 12 files and folders, their versions and its trash are kept for 30 days');
    expect(ask.textContent).toContain('restore it from Workspaces → Deleted workspaces');
    expect(ask.textContent).toContain('stop working for good, even after a restore');
    expect(ask.textContent).toContain('Backups taken earlier keep it');
    const button = within(ask).getByRole('button', { name: 'Delete workspace' }) as HTMLButtonElement;
    expect(button.className).toBe('btn-danger');
    const input = within(ask).getByLabelText(/Type Studio to confirm/);
    for (const wrong of ['studio', 'Studio ', 'Stud']) {
      fireEvent.change(input, { target: { value: wrong } });
      expect(button.disabled).toBe(true);
      fireEvent.submit(button.closest('form')!);
    }
    expect(calls.filter(c => c[1]?.method === 'DELETE')).toHaveLength(0);
    fireEvent.change(input, { target: { value: 'Studio' } });
    expect(button.disabled).toBe(false);
    fireEvent.click(button);
    await waitFor(() => expect(deleted).toHaveBeenCalled());
    const del = calls.find(c => c[1]?.method === 'DELETE');
    expect(del?.[0]).toBe('/api/drive/workspaces/ws1');
    expect(JSON.parse(String(del?.[1]?.body))).toEqual({ confirm: 'Studio' });
  });

  it('says when nothing purges a deleted workspace', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ trash_days: 0 }), { status: 200 })));
    render(<WorkspaceAdmin workspace={full} onRenamed={() => {}} onDeleted={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete workspace' }));
    expect((await screen.findByRole('dialog')).textContent).toContain('kept until an administrator restores it');
  });
});
