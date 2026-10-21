import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Devices, DevicePolicySettings } from './Devices';


// li finds a list row by its whole text, which now spans a title, a badge and a meta line.
const li = (re: RegExp) => (_: string, el: Element | null) => el?.tagName === 'LI' && re.test(el.textContent ?? '');

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });
const laptop = { id: 'd1', name: 'Work laptop', platform: 'macos', created: '2026-10-15T10:00:00Z', last_used: 0 };

function server(policy: { wipe_default: boolean; wipe_locked: boolean }) {
  let devices = [laptop];
  const calls: string[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    calls.push(`${method} ${String(input)}`);
    if (String(input) === '/api/drive/device-policy') return json(policy);
    if (method === 'DELETE') { devices = []; return json({ wipe: true }); }
    return json(devices);
  }));
  return calls;
}

describe('Devices', () => {
  it('lists computers and revokes with the wipe flag the user chose', async () => {
    const calls = server({ wipe_default: true, wipe_locked: false });
    render(<Devices />);
    expect(await screen.findByText(/Work laptop/)).toBeTruthy();
    expect(screen.getByText(li(/Work laptop.*macOS · linked .*2026 · never used/))).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
    const wipe = await screen.findByLabelText(/^Also remove KyDrive files from this computer/) as HTMLInputElement;
    expect(wipe.checked).toBe(true);
    expect(screen.getByText(/offline for 30 days/)).toBeTruthy();
    fireEvent.click(wipe);
    expect(wipe.checked).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Revoke computer' }));
    await waitFor(() => expect(calls).toContain('DELETE /api/devices/d1?wipe=false'));
    await waitFor(() => expect(screen.queryByText(/Work laptop/)).toBeNull());
  });

  it('fixes the checkbox at the default when the policy is locked', async () => {
    const calls = server({ wipe_default: true, wipe_locked: true });
    render(<Devices />);
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    const wipe = await screen.findByLabelText(/^Also remove KyDrive files from this computer/) as HTMLInputElement;
    expect(wipe.disabled).toBe(true);
    expect(wipe.checked).toBe(true);
    fireEvent.click(wipe);
    expect(wipe.checked).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Revoke computer' }));
    await waitFor(() => expect(calls).toContain('DELETE /api/devices/d1?wipe=true'));
  });

  it('cancels a revoke without a request', async () => {
    const calls = server({ wipe_default: false, wipe_locked: false });
    render(<Devices />);
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    expect(((await screen.findByLabelText(/^Also remove KyDrive files from this computer/)) as HTMLInputElement).checked).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(calls.some(c => c.startsWith('DELETE'))).toBe(false);
  });
});

describe('DevicePolicySettings', () => {
  it('loads the defaults and saves the wipe default and lock', async () => {
    const puts: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') { const b = JSON.parse(String(init.body)); puts.push(b); return json(b); }
      return json({ wipe_default: true, wipe_locked: false });
    }));
    render(<DevicePolicySettings />);
    const wipe = await screen.findByLabelText(/Remove files when a computer is revoked/) as HTMLInputElement;
    expect(wipe.checked).toBe(true);
    fireEvent.click(wipe);
    fireEvent.click(screen.getByLabelText(/Users cannot change this/));
    fireEvent.click(screen.getByRole('button', { name: 'Save computers' }));
    await waitFor(() => expect(puts).toEqual([{ wipe_default: false, wipe_locked: true }]));
  });
});
