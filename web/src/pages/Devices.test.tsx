import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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

describe('Link a phone', () => {
  it('shows a one-time QR code and names the phone that used it', async () => {
    const pixel = { id: 'p1', name: 'Pixel 9', platform: 'android', created: '2026-11-15T10:00:00Z', last_used: 0 };
    let devices: unknown[] = [laptop];
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      calls.push(`${method} ${String(input)}`);
      if (String(input) === '/api/drive/device-policy') return json({ wipe_default: true, wipe_locked: false });
      if (String(input) === '/api/devices/phone-code') { devices = [laptop, pixel]; return json({ link: 'kydrive://link?srv=https%3A%2F%2Fdrive.example&code=abc', expires_in: 120 }); }
      return json(devices);
    }));
    render(<Devices />);
    fireEvent.click(await screen.findByRole('button', { name: 'Link a phone' }));
    const qr = await screen.findByAltText('QR code to link a phone') as HTMLImageElement;
    expect(qr.src.startsWith('data:image/svg+xml')).toBe(true);
    expect(calls.filter(c => c === 'POST /api/devices/phone-code')).toHaveLength(1);
    expect(screen.getByRole('timer').textContent).toMatch(/Expires in 1[12][0-9] s/);
    await waitFor(() => expect(screen.getByRole('status').textContent).toMatch(/Pixel 9 \(Android\) is linked/), { timeout: 4000 });
    fireEvent.click(within(screen.getByText(/is linked/).closest('dialog') as HTMLElement).getByRole('button', { name: 'Revoke' }));
    expect(await screen.findByRole('button', { name: 'Revoke phone' })).toBeTruthy();
    expect(screen.getByLabelText(/^Also remove KyDrive files from this phone/)).toBeTruthy();
  });

  it('shows no code when the server refuses one', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === '/api/drive/device-policy') return json({ wipe_default: true, wipe_locked: false });
      if (String(input) === '/api/devices/phone-code') return json({ error: 'at most 20 linked devices' }, 429);
      return json([]);
    }));
    render(<Devices />);
    fireEvent.click(await screen.findByRole('button', { name: 'Link a phone' }));
    expect(await screen.findByText(/at most 20 linked devices/)).toBeTruthy();
    expect(screen.queryByAltText('QR code to link a phone')).toBeNull();
  });
});
