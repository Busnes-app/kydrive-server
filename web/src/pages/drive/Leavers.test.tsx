import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { Leavers } from './Leavers';

const workspaces = [
  { id: 'mine', name: 'My files', role: 'manager', kind: 'personal' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0, editors_share: false },
  { id: 'team', name: 'Team', role: '', kind: 'shared' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0, editors_share: false },
];

function stub() {
  const calls: [string, RequestInit | undefined][] = [];
  let handedOver = false;
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push([url, init]);
    if (init?.method === 'POST') { handedOver = true; return new Response(JSON.stringify({ folder: 'f1' }), { status: 200 }); }
    const leavers = handedOver ? [] : [{ workspace: 'p1', name: 'Lee Ver', status: 'inactive', size: 2048, items: 3 }];
    return new Response(JSON.stringify({ leavers, colleagues: [{ id: 'u2', name: 'Heir' }] }), { status: 200 });
  }));
  return calls;
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe('Leavers', () => {
  it('hands a leaver over to a colleague after confirming the size', async () => {
    const calls = stub();
    const done = vi.fn();
    render(<Leavers workspaces={workspaces} onDone={done} />);
    await screen.findByText('2.0 KiB · 3 items');
    expect(screen.getByText('inactive')).toBeTruthy();
    expect(screen.queryByRole('option', { name: 'My files' })).toBeNull();
    fireEvent.change(screen.getByLabelText('Destination'), { target: { value: 'user:u2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Hand over' }));
    const ask = await screen.findByRole('dialog');
    expect(ask.textContent).toContain('2.0 KiB (3 items) from Lee Ver to Heir\'s My files');
    fireEvent.click(within(ask).getByRole('button', { name: 'Hand over' }));
    await waitFor(() => expect(done).toHaveBeenCalled());
    const post = calls.find(([, init]) => init?.method === 'POST');
    expect(post?.[0]).toBe('/api/drive/leavers/p1/handover');
    expect(JSON.parse(String(post?.[1]?.body))).toEqual({ user: 'u2' });
    expect(await screen.findByText('No removed accounts hold files.')).toBeTruthy();
  });

  it('sends nothing when the confirmation is declined', async () => {
    const calls = stub();
    render(<Leavers workspaces={workspaces} onDone={() => {}} />);
    await screen.findByText(/Lee Ver/);
    fireEvent.change(screen.getByLabelText('Destination'), { target: { value: 'workspace:team' } });
    fireEvent.click(screen.getByRole('button', { name: 'Hand over' }));
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(calls.some(([, init]) => init?.method === 'POST')).toBe(false);
  });
});
