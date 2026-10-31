import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';

const admin = { id: 'admin-test', username: 'admin', display_name: 'Administrator', role: 'admin' };

vi.mock('./pages/Login', () => ({
  Login: ({ onSuccess }: { onSuccess: (u: unknown) => void }) => <button onClick={() => onSuccess(admin)}>Fake sign in</button>,
}));

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('keeps the session on a failed sign-out and resets the shell on success', async () => {
  const { App } = await import('./App');
  const logouts = [new Response('{}', { status: 500 }), new Response('{}', { status: 200 })];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url === '/api/auth/logout') return logouts.shift()!;
    if (url === '/api/auth/me') return Response.json({ authenticated: true, user: admin, open_in_new_tab: true });
    if (url === '/api/settings') return Response.json({ app_name: 'Test Server' });
    if (url.startsWith('/api/drive/workspaces') && init?.method === undefined) return Response.json([]);
    return Response.json({});
  });
  vi.stubGlobal('fetch', fetcher);
  render(<App />);

  fireEvent.click(await screen.findByRole('button', { name: 'Settings' }));
  expect(screen.getByRole('button', { name: 'Settings' }).getAttribute('aria-current')).toBe('page');

  fireEvent.click(screen.getAllByRole('button', { name: 'Sign out' })[0]);
  expect((await screen.findByText(/Sign out failed/)).closest('[role="alert"]')).toBeTruthy();
  expect(screen.getAllByRole('button', { name: 'Sign out' })[0]).toBeTruthy();

  const settingsReads = fetcher.mock.calls.filter(([u]) => String(u) === '/api/settings').length;
  fireEvent.click(screen.getAllByRole('button', { name: 'Sign out' })[0]);
  fireEvent.click(await screen.findByRole('button', { name: 'Fake sign in' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Settings' }).getAttribute('aria-current')).toBeNull());
  expect(screen.queryByText(/Sign out failed/)).toBeNull();
  expect(fetcher.mock.calls.filter(([u]) => String(u) === '/api/settings').length).toBeGreaterThan(settingsReads);
});

it('keeps the session when sign-out cannot reach the server', async () => {
  const { App } = await import('./App');
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url === '/api/auth/logout') throw new TypeError('offline');
    if (url === '/api/auth/me') return Response.json({ authenticated: true, user: admin });
    return Response.json(url.startsWith('/api/drive/workspaces') ? [] : {});
  }));
  render(<App />);
  fireEvent.click((await screen.findAllByRole('button', { name: 'Sign out' }))[0]);
  expect((await screen.findByRole('alert')).textContent).toContain('Sign out failed');
  expect(screen.queryByRole('button', { name: 'Fake sign in' })).toBeNull();
});

it('loads the app rail once after sign-in, drops entries that are not https and clears it on sign-out', async () => {
  const { App } = await import('./App');
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url === '/api/auth/me') return Response.json({ authenticated: true, user: admin });
    if (url === '/api/suite/apps') return Response.json({ apps: [
      { id: 'kypost', name: 'KyPost', url: 'https://mail.example.com/api/auth/oidc/login' },
      { id: 'evil', name: 'Evil', url: 'javascript:alert(1)' },
    ] });
    return Response.json(url.startsWith('/api/drive/workspaces') ? [] : {});
  });
  vi.stubGlobal('fetch', fetcher);
  render(<App />);
  const rails = await screen.findAllByRole('navigation', { name: 'Apps' });
  expect(rails).toHaveLength(2);
  expect(screen.getAllByRole('link', { name: 'KyPost' })).toHaveLength(2);
  expect(screen.queryByRole('link', { name: 'Evil' })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  expect(fetcher.mock.calls.filter(([u]) => String(u) === '/api/suite/apps').length).toBe(1);
  fireEvent.click(screen.getAllByRole('button', { name: 'Sign out' })[0]);
  await screen.findByRole('button', { name: 'Fake sign in' });
  expect(screen.queryByRole('navigation', { name: 'Apps' })).toBeNull();
});

it('shows no app rail when the server has no list', async () => {
  const { App } = await import('./App');
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url === '/api/auth/me') return Response.json({ authenticated: true, user: admin });
    if (url === '/api/suite/apps') return new Response('{}', { status: 500 });
    return Response.json(url.startsWith('/api/drive/workspaces') ? [] : {});
  }));
  render(<App />);
  await screen.findAllByRole('button', { name: 'Sign out' });
  await new Promise(r => setTimeout(r, 0));
  expect(screen.queryByRole('navigation', { name: 'Apps' })).toBeNull();
});
