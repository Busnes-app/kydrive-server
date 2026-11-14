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
