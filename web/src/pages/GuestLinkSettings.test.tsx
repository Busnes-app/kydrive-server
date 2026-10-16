import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { GuestLinkSettings } from './GuestLinkSettings';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe('GuestLinkSettings', () => {
  it('loads the policy off by default and saves it', async () => {
    const puts: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') { const b = JSON.parse(String(init.body)); puts.push(b); return new Response(JSON.stringify(b), { status: 200 }); }
      return new Response(JSON.stringify({ enabled: false, max_days: 30 }), { status: 200 });
    }));
    render(<GuestLinkSettings />);
    const box = await screen.findByLabelText('Allow guest links') as HTMLInputElement;
    expect(box.checked).toBe(false);
    fireEvent.click(box);
    fireEvent.change(screen.getByLabelText(/Longest a guest link may last/), { target: { value: '14' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save guest links' }));
    await waitFor(() => expect(puts).toEqual([{ enabled: true, max_days: 14 }]));
  });
});
