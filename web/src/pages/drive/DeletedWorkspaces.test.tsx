import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { DeletedWorkspaces, purgeDate } from './DeletedWorkspaces';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const studio = { id: 'ws1', name: 'Studio', deleted_at: '2026-10-01T10:00:00Z', deleted_by: 'Boss', used: 2048, items: 3 };

function stub(list: unknown[]) {
  const calls: [string, RequestInit | undefined][] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push([String(input), init]);
    if (init?.method === 'POST') { list.length = 0; return new Response(JSON.stringify({ restored: true }), { status: 200 }); }
    return new Response(JSON.stringify({ purge_days: 90, workspaces: list }), { status: 200 });
  }));
  return calls;
}

describe('DeletedWorkspaces', () => {
  it('renders nothing when none are deleted', async () => {
    const calls = stub([]);
    const { container } = render(<DeletedWorkspaces onRestored={() => {}} />);
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(container.textContent).toBe('');
  });

  it('lists size, count and purge date, and restores after confirmation', async () => {
    const calls = stub([studio]);
    const restored = vi.fn();
    render(<DeletedWorkspaces onRestored={restored} />);
    const row = (await screen.findByText('Studio')).closest('li')!;
    expect(row.textContent).toContain('2.0 KiB · 3 items');
    expect(row.textContent).toContain('by Boss');
    expect(row.querySelector('time[datetime="2026-12-30T10:00:00.000Z"]')).not.toBeNull();
    fireEvent.click(within(row).getByRole('button', { name: 'Restore' }));
    const ask = await screen.findByRole('dialog');
    expect(ask.textContent).toContain('stay revoked');
    fireEvent.click(within(ask).getByRole('button', { name: 'Restore workspace' }));
    await waitFor(() => expect(restored).toHaveBeenCalled());
    expect(calls.find(c => c[1]?.method === 'POST')?.[0]).toBe('/api/drive/workspaces/ws1/restore');
    await waitFor(() => expect(screen.queryByText('Studio')).toBeNull());
  });

  it('computes the purge date from the trash age', () => {
    expect(purgeDate('2026-10-01T10:00:00Z', 0)).toBeNull();
    expect(purgeDate('2026-10-01T10:00:00Z', 1)).toBe('2026-10-02T10:00:00.000Z');
  });
});
