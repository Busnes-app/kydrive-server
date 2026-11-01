import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';

const admin = { id: 'admin-test', username: 'admin', display_name: 'Administrator', role: 'admin' };

vi.mock('./pages/Login', () => ({
  Login: ({ onSuccess }: { onSuccess: (u: unknown) => void }) => <button onClick={() => onSuccess(admin)}>Fake sign in</button>,
}));

afterEach(() => { cleanup(); vi.unstubAllGlobals(); history.replaceState(null, '', '/'); });

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

  fireEvent.click(await screen.findByRole('button', { name: 'Server settings' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Server settings' }).getAttribute('aria-current')).toBe('page'));

  fireEvent.click(screen.getAllByRole('button', { name: 'Sign out' })[0]);
  expect((await screen.findByText(/Sign out failed/)).closest('[role="alert"]')).toBeTruthy();
  expect(screen.getAllByRole('button', { name: 'Sign out' })[0]).toBeTruthy();

  const settingsReads = fetcher.mock.calls.filter(([u]) => String(u) === '/api/settings').length;
  fireEvent.click(screen.getAllByRole('button', { name: 'Sign out' })[0]);
  fireEvent.click(await screen.findByRole('button', { name: 'Fake sign in' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Server settings' }).getAttribute('aria-current')).toBeNull());
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
  fireEvent.click(screen.getByRole('button', { name: 'Server settings' }));
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

it('keeps Settings behind the footer gear, with a tab and an address per section', async () => {
  const { App } = await import('./App');
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url === '/api/auth/me') return Response.json({ authenticated: true, user: { ...admin, sso_provider: 'local' } });
    return Response.json(url.startsWith('/api/drive/workspaces') || url.startsWith('/api/auth/app-passwords') || url.startsWith('/api/devices') ? [] : {});
  }));
  render(<App />);
  const nav = await screen.findByRole('navigation', { name: 'Primary' });
  for (const label of ['Account', 'Appearance', 'Settings']) expect(within(nav).queryByRole('button', { name: label })).toBeNull();
  expect(screen.queryByText('Preferences')).toBeNull();
  expect(screen.queryByText('Sign out')).toBeNull();
  const gear = screen.getAllByRole('button', { name: 'Settings' })[0];
  expect(gear.getAttribute('aria-haspopup')).toBe('menu');
  fireEvent.click(gear);
  const account = within(document.getElementById(gear.getAttribute('aria-controls')!)!).getByRole('menuitem', { name: 'Account' });
  expect(account.getAttribute('href')).toBe('#/settings/account');
  location.hash = '/settings/account';
  const tabs = await screen.findByRole('navigation', { name: 'Settings sections' });
  expect(within(tabs).getAllByRole('link').map(a => a.textContent)).toEqual(['Account', 'Phones & apps', 'Appearance', 'Files']);
  expect(gear.getAttribute('aria-current')).toBe('page');
  expect(nav.querySelector('[aria-current]')).toBeNull();
  location.hash = '/settings/files';
  expect(await screen.findByRole('checkbox', { name: 'Open files in a new tab' })).toBeTruthy();
});

it('sends the old Account and Appearance addresses to their Settings section', async () => {
  const { App } = await import('./App');
  for (const [old, section] of [['account', 'Account'], ['appearance', 'Appearance']]) {
    history.replaceState(null, '', `/#/${old}`);
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/auth/me') return Response.json({ authenticated: true, user: admin });
      return Response.json(url.startsWith('/api/drive/workspaces') || url.startsWith('/api/auth/app-passwords') || url.startsWith('/api/devices') ? [] : {});
    }));
    render(<App />);
    const tabs = await screen.findByRole('navigation', { name: 'Settings sections' });
    expect(within(tabs).getByRole('link', { name: section }).getAttribute('aria-current')).toBe('page');
    expect(location.hash).toBe(`#/settings/${old}`);
    cleanup();
  }
});

it('lists My files in the sidebar when the first page is Settings', async () => {
  const { App } = await import('./App');
  history.replaceState(null, '', '/#/settings/files');
  const mine = { id: 'p1', name: 'My files', role: 'manager', kind: 'personal', quota: 0, used: 0, trash_days: 0, keep_versions: 0, editors_share: false };
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url === '/api/auth/me') return Response.json({ authenticated: true, user: admin });
    if (url.startsWith('/api/drive/workspaces')) return Response.json([mine]);
    return Response.json({});
  }));
  render(<App />);
  const nav = await screen.findByRole('navigation', { name: 'Primary' });
  expect(await within(nav).findByRole('button', { name: 'My files' })).toBeTruthy();
  expect(screen.getByRole('checkbox', { name: 'Open files in a new tab' })).toBeTruthy();
});
