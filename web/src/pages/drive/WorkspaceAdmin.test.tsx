import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { WorkspaceAdmin } from './WorkspaceAdmin';

const studio = { id: 'ws1', name: 'Studio', role: '', kind: 'shared' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0, editors_share: false };

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

  it('explains why a workspace with content stays', async () => {
    stub(409, 'revision conflict: workspace is not empty');
    const deleted = vi.fn();
    render(<WorkspaceAdmin workspace={studio} onRenamed={() => {}} onDeleted={deleted} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete workspace' }));
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Delete workspace' }));
    expect((await screen.findByRole('alert')).textContent).toContain('Studio is not empty. Only an empty workspace can be deleted');
    expect(deleted).not.toHaveBeenCalled();
  });
});
