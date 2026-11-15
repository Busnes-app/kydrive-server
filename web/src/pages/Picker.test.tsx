import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Picker, hasPickRequest, takePickRequestFromURL } from './Picker';

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });
const ws = { id: 'w1', name: 'My files', role: 'manager', kind: 'personal', quota: 1, used: 0, trash_days: 0, keep_versions: 0, editors_share: false };
const reader = { ...ws, id: 'w2', name: 'Read only', role: 'reader', kind: 'shared' };
const file = { id: 'f1', name: 'report.pdf', parent: '', revision: 1, size: 2048, trashed: false };

let opener: { postMessage: ReturnType<typeof vi.fn>; closed: boolean };
let calls: { url: string; method: string; body?: unknown }[];
beforeEach(() => {
  sessionStorage.clear();
  calls = [];
  opener = { postMessage: vi.fn(), closed: false };
  vi.stubGlobal('opener', opener);
  vi.spyOn(window, 'close').mockImplementation(() => {});
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); sessionStorage.clear(); });

function open(search: string, check: Response | (() => Response) = () => json({ app: 'KyPost', origin: 'https://post.example', mode: 'link' })) {
  vi.stubGlobal('location', { ...window.location, pathname: '/pick', search });
  vi.spyOn(history, 'replaceState').mockImplementation(() => {});
  takePickRequestFromURL();
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input), method = init?.method ?? 'GET';
    calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (url.startsWith('/api/drive/pick?')) return typeof check === 'function' ? check() : check;
    if (url === '/api/drive/personal-workspace') return json({});
    if (url === '/api/drive/workspaces') return json([ws, reader]);
    if (url.endsWith('/folders')) return json([]);
    if (url.endsWith('/files')) return json([file]);
    if (url === '/api/drive/pick') return json({ files: [{ name: 'report.pdf', size: 2048, url: 'https://drive.example/l/#tok', expires: null, mime: 'application/pdf' }] });
    return json({}, 404);
  }));
}

const posts = () => calls.filter(c => c.url === '/api/drive/pick');

describe('Picker', () => {
  it('moves the request out of the address bar so it survives sign-in', () => {
    open('?origin=https%3A%2F%2Fpost.example&mode=link');
    expect(history.replaceState).toHaveBeenCalledWith(null, '', '/');
    expect(hasPickRequest()).toBe(true);
  });

  it('sends nothing until the confirming click, then posts to exactly the origin once', async () => {
    open('?origin=https%3A%2F%2Fpost.example');
    render(<Picker onClose={() => {}} />);
    const box = await screen.findByRole('checkbox', { name: 'Choose report.pdf' });
    expect(calls[0].url).toBe('/api/drive/pick?origin=https%3A%2F%2Fpost.example&mode=link');
    // Only workspaces whose files the user may share are offered.
    expect(screen.queryByRole('option', { name: 'Read only' })).toBeNull();
    fireEvent.click(box);
    fireEvent.change(screen.getByLabelText('Link expires'), { target: { value: '7' } });
    expect(posts()).toEqual([]);
    expect(opener.postMessage).not.toHaveBeenCalled();
    const share = screen.getByRole('button', { name: 'Share 1 file' });
    fireEvent.click(share);
    fireEvent.click(share);
    await waitFor(() => expect(opener.postMessage).toHaveBeenCalledTimes(1));
    expect(opener.postMessage).toHaveBeenCalledWith({ type: 'kydrive.picked', files: [{ name: 'report.pdf', size: 2048, url: 'https://drive.example/l/#tok', expires: null, mime: 'application/pdf' }] }, 'https://post.example');
    expect(posts()).toEqual([{ url: '/api/drive/pick', method: 'POST', body: { origin: 'https://post.example', mode: 'link', files: ['f1'], days: 7 } }]);
    expect(window.close).toHaveBeenCalled();
    expect(hasPickRequest()).toBe(false);
  });

  it('copy mode sends no expiry', async () => {
    open('?origin=https%3A%2F%2Fpost.example&mode=copy');
    render(<Picker onClose={() => {}} />);
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Choose report.pdf' }));
    expect(screen.queryByLabelText('Link expires')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Attach 1 file' }));
    await waitFor(() => expect(opener.postMessage).toHaveBeenCalled());
    expect(posts()[0].body).toEqual({ origin: 'https://post.example', mode: 'copy', files: ['f1'] });
  });

  it('a refused origin shows a plain refusal and sends nothing', async () => {
    open('?origin=https%3A%2F%2Fevil.example', json({ error: 'Forbidden', code: 'bad_origin' }, 403));
    render(<Picker onClose={() => {}} />);
    expect(await screen.findByText(/nothing was shared/)).toBeTruthy();
    expect(screen.queryByRole('checkbox')).toBeNull();
    expect(calls.map(c => c.url)).toEqual(['/api/drive/pick?origin=https%3A%2F%2Fevil.example&mode=link']);
    expect(opener.postMessage).not.toHaveBeenCalled();
  });

  it('a closed opener gets nothing and no link is made', async () => {
    open('?origin=https%3A%2F%2Fpost.example');
    opener.closed = true;
    render(<Picker onClose={() => {}} />);
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Choose report.pdf' }));
    fireEvent.click(screen.getByRole('button', { name: 'Share 1 file' }));
    expect(await screen.findByText(/has closed/)).toBeTruthy();
    expect(posts()).toEqual([]);
  });
});
