import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { OfficeNetworks } from './OfficeNetworks';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const ROUTE = '/api/settings/rate-limit-networks';

it('shows the stored networks one per line and saves an edited list', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    expect(String(input)).toBe(ROUTE);
    if (init?.method === 'PUT') {
      expect(JSON.parse(String(init.body))).toEqual({ networks: '203.0.113.0/24,198.51.100.7' });
      return Response.json({ networks: '203.0.113.0/24,198.51.100.7/32', environment: '192.0.2.0/24', exempted: 0 });
    }
    return Response.json({ networks: '203.0.113.0/24', environment: '192.0.2.0/24', exempted: 41 });
  }));
  const { container } = render(<OfficeNetworks />);
  const box = (await screen.findByDisplayValue('203.0.113.0/24')) as HTMLTextAreaElement;
  expect(container.textContent).toMatch(/41 since/);
  expect(container.textContent).toMatch(/192\.0\.2\.0\/24/);
  expect(container.textContent).toMatch(/never add guest Wi-Fi, a VPN provider or\s+your reverse proxy/);

  fireEvent.change(box, { target: { value: '203.0.113.0/24\n 198.51.100.7 \n' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save networks' }));
  expect((await screen.findByRole('status')).textContent).toMatch(/Saved/);
  expect(box.value).toBe('203.0.113.0/24\n198.51.100.7/32');
});

it('shows why the server refused a list and keeps what was typed', async () => {
  vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) =>
    init?.method === 'PUT'
      ? Response.json({ error: '"10.0.0.0/8" is too broad; the widest IPv4 range allowed is /16' }, { status: 400 })
      : Response.json({ networks: '', environment: '', exempted: 0 })));
  const { container } = render(<OfficeNetworks />);
  const box = (await screen.findByPlaceholderText('203.0.113.0/24')) as HTMLTextAreaElement;
  fireEvent.change(box, { target: { value: '10.0.0.0/8' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save networks' }));
  expect((await screen.findByRole('alert')).textContent).toBe('"10.0.0.0/8" is too broad; the widest IPv4 range allowed is /16');
  expect(box.value).toBe('10.0.0.0/8');
  expect(container.textContent).not.toMatch(/Also set by the server environment/);
});
