import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AppPasswords } from './AppPasswords';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });

describe('AppPasswords', () => {
  it('shows a new password once, lists it without the secret and revokes after confirming', async () => {
    let stored: unknown[] = [];
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      calls.push(`${method} ${String(input)}`);
      if (method === 'POST') { stored = [{ id: 'p1', name: 'Laptop', created: '2026-10-15T10:00:00Z', last_used: 0 }]; return json({ id: 'p1', name: 'Laptop', created: '2026-10-15T10:00:00Z', password: 'abcd-efgh-ijkl-mnop-qrst-uvwx-yz23-4567', username: 'alice', url: 'https://drive.example/dav/' }, 201); }
      if (method === 'DELETE') { stored = []; return new Response(null, { status: 204 }); }
      return json(stored);
    }));
    vi.stubGlobal('confirm', vi.fn(() => true));
    render(<AppPasswords />);
    fireEvent.change(await screen.findByLabelText('Device or app name'), { target: { value: 'Laptop' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create app password' }));
    expect(await screen.findByText('abcd-efgh-ijkl-mnop-qrst-uvwx-yz23-4567')).toBeTruthy();
    expect(screen.getByText('https://drive.example/dav/')).toBeTruthy();
    expect(screen.getByText(/Laptop · created 2026-10-15 · never used/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'I have copied it' }));
    expect(screen.queryByText('abcd-efgh-ijkl-mnop-qrst-uvwx-yz23-4567')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(screen.queryByText(/Laptop · created/)).toBeNull());
    expect(calls).toContain('DELETE /api/auth/app-passwords/p1');
  });

  it('does nothing when the revoke is not confirmed', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json([{ id: 'p1', name: 'Phone', created: '2026-10-15T10:00:00Z', last_used: 1760000000 }])));
    vi.stubGlobal('confirm', vi.fn(() => false));
    render(<AppPasswords />);
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
