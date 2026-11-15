import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { KyPostStatus, KyPostSwitch } from './KyPost';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe('KyPostSwitch', () => {
  it('is hidden while KyPost is not paired', async () => {
    const fetcher = vi.fn(async () => Response.json({ paired: false, enabled: false }));
    vi.stubGlobal('fetch', fetcher);
    const { container } = render(<KyPostSwitch />);
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalled());
    expect(container.textContent).toBe('');
  });

  it('shows the stored choice and saves a change', async () => {
    let enabled = true;
    const calls: RequestInit[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') { calls.push(init); enabled = JSON.parse(String(init.body)).enabled; }
      return Response.json({ paired: true, enabled });
    }));
    render(<KyPostSwitch />);
    const box = await screen.findByLabelText<HTMLInputElement>('Let KyPost save and share my files');
    expect(box.checked).toBe(true);
    fireEvent.click(box);
    await vi.waitFor(() => expect(box.checked).toBe(false));
    expect(JSON.parse(String(calls[0].body))).toEqual({ enabled: false });
  });

  const guest = 'Let KyPost create guest links for people outside the organisation';
  it('hides the guest switch while the administrator policy is off, or on an older server', async () => {
    for (const body of [{ paired: true, enabled: true, guest_policy: false, guest_links: false }, { paired: true, enabled: true }]) {
      vi.stubGlobal('fetch', vi.fn(async () => Response.json(body)));
      render(<KyPostSwitch />);
      await screen.findByLabelText('Let KyPost save and share my files');
      expect(screen.queryByLabelText(guest)).toBeNull();
      cleanup();
    }
  });

  it('shows the guest switch off by default while the policy is on, and saves it alone', async () => {
    let state = { paired: true, enabled: true, guest_policy: true, guest_links: false };
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') { const b = JSON.parse(String(init.body)); bodies.push(b); state = { ...state, ...b }; }
      return Response.json(state);
    }));
    render(<KyPostSwitch />);
    const box = await screen.findByLabelText<HTMLInputElement>(guest);
    expect(box.checked).toBe(false);
    fireEvent.click(box);
    await vi.waitFor(() => expect(box.checked).toBe(true));
    expect(bodies).toEqual([{ guest_links: true }]);
    // With sharing off, KyPost can make no link at all, so the guest switch is disabled.
    fireEvent.click(screen.getByLabelText('Let KyPost save and share my files'));
    await vi.waitFor(() => expect(box.disabled).toBe(true));
  });
});

describe('KyPostStatus', () => {
  it('names the source and address, or why it is not connected', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ connected: true, source: 'kyidentity', url: 'https://post.example' })));
    render(<dl><KyPostStatus /></dl>);
    expect(await screen.findByText(/Connected through KyIdentity/)).toBeTruthy();
    cleanup();
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ connected: false, reason: 'no_peer' })));
    render(<dl><KyPostStatus /></dl>);
    expect(await screen.findByText('Not connected: KyIdentity has not paired KyPost with KyDrive')).toBeTruthy();
  });
});
