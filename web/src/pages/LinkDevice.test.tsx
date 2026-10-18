import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { LinkDevice, takeLinkRequestFromURL, hasLinkRequest } from './LinkDevice';

const challenge = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';
const request = (over: Record<string, string> = {}) => '?' + new URLSearchParams({ redirect: 'http://127.0.0.1:53682/cb', challenge, state: 'xyz', name: 'Work laptop', platform: 'windows', ...over }).toString();
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });

let assign: ReturnType<typeof vi.fn>;
beforeEach(() => {
  sessionStorage.clear();
  assign = vi.fn();
  vi.stubGlobal('location', { ...window.location, assign, pathname: '/', search: '' });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); sessionStorage.clear(); });

function open(search: string) {
  vi.stubGlobal('location', { ...window.location, assign, pathname: '/link', search });
  vi.spyOn(history, 'replaceState').mockImplementation(() => {});
  takeLinkRequestFromURL();
}

describe('LinkDevice', () => {
  it('moves the request out of the address bar and keeps it across sign-in', () => {
    open(request());
    expect(history.replaceState).toHaveBeenCalledWith(null, '', '/');
    expect(hasLinkRequest()).toBe(true);
  });

  it('approves only on a click, posts once and goes to the loopback redirect', async () => {
    open(request());
    const posts: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === '/healthz') return json({ clientIp: '203.0.113.7' });
      posts.push(JSON.parse(String(init?.body)));
      return json({ redirect: 'http://127.0.0.1:53682/cb?code=abc&state=xyz' });
    }));
    render(<LinkDevice onClose={() => {}} />);
    expect(await screen.findByText('203.0.113.7')).toBeTruthy();
    expect(screen.getByText('Work laptop')).toBeTruthy();
    expect(screen.getByText(/Windows/)).toBeTruthy();
    expect(posts).toEqual([]);
    const approve = screen.getByRole('button', { name: 'Approve' });
    fireEvent.click(approve);
    fireEvent.click(approve);
    await waitFor(() => expect(assign).toHaveBeenCalledWith('http://127.0.0.1:53682/cb?code=abc&state=xyz'));
    expect(posts).toEqual([{ redirect: 'http://127.0.0.1:53682/cb', challenge, state: 'xyz', name: 'Work laptop', platform: 'windows' }]);
    expect(hasLinkRequest()).toBe(false);
  });

  it.each([
    ['a localhost redirect', { redirect: 'http://localhost:53682/cb' }],
    ['a portless redirect', { redirect: 'http://127.0.0.1/cb' }],
    ['another host', { redirect: 'https://evil.example/cb' }],
    ['a fragment', { redirect: 'http://127.0.0.1:53682/cb#x' }],
    ['no state', { state: '' }],
    ['a bad challenge', { challenge: 'short' }],
    ['an unknown platform', { platform: 'toString' }],
  ])('shows an error, never a button or a redirect, for %s', async (_, over) => {
    open(request(over));
    vi.stubGlobal('fetch', vi.fn(async () => json({ clientIp: '203.0.113.7' })));
    render(<LinkDevice onClose={() => {}} />);
    expect(await screen.findByRole('alert')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull();
    expect(assign).not.toHaveBeenCalled();
  });

  it('shows a refused approval and does not redirect', async () => {
    open(request());
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => String(input) === '/healthz' ? json({ clientIp: '203.0.113.7' }) : json({ error: 'workspace quota exceeded: at most 20 computers' }, 413)));
    render(<LinkDevice onClose={() => {}} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Approve' }));
    expect(await screen.findByRole('alert')).toBeTruthy();
    expect(assign).not.toHaveBeenCalled();
  });

  it('cancels without posting', async () => {
    open(request());
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) => json({ clientIp: '203.0.113.7' }));
    vi.stubGlobal('fetch', fetchMock);
    const onClose = vi.fn();
    render(<LinkDevice onClose={onClose} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
    expect(onClose).toHaveBeenCalled();
    expect(hasLinkRequest()).toBe(false);
    expect(fetchMock.mock.calls.every(c => String(c[0]) === '/healthz')).toBe(true);
  });
});
