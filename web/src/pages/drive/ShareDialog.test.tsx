import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ShareDialog } from './ShareDialog';

beforeAll(() => { HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); }; });

// li finds a list row by its whole text, which now spans a title, a badge and a meta line.
const li = (re: RegExp) => (_: string, el: Element | null) => el?.tagName === 'LI' && re.test(el.textContent ?? '');

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });

describe('ShareDialog', () => {
  it('finds a person, shares at the chosen role and removes a share', async () => {
    let shares: unknown[] = [{ id: 's1', principal_kind: 'group', principal_id: 'g1', name: 'Finance', role: 'reader' }];
    const calls: { method: string; url: string; body?: unknown }[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input), method = init?.method ?? 'GET';
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (url.startsWith('/api/drive/people')) return json([{ id: 'u2', name: 'Ann Lee', username: 'ann' }]);
      if (method === 'PUT') { shares = [...shares, { id: 's2', principal_kind: 'user', principal_id: 'u2', name: 'Ann Lee', role: 'editor' }]; return json(shares[1]); }
      if (method === 'DELETE') { shares = shares.filter(s => (s as { id: string }).id !== 's1'); return new Response(null, { status: 204 }); }
      return json(url.endsWith('/links') ? [] : shares);
    }));
    render(<ShareDialog kind="folder" id="d1" name="Reports" groups={[]} maxRole="editor" onClose={() => {}} />);
    expect(await screen.findByText(li(/Finance.*Reader.*Group/))).toBeTruthy();
    expect(screen.queryByRole('option', { name: 'Manager' })).toBeNull(); // editors cannot share above their role
    fireEvent.change(screen.getByLabelText('Access'), { target: { value: 'editor' } });
    fireEvent.change(screen.getByLabelText('Find a person'), { target: { value: 'an' } });
    fireEvent.click(await screen.findByRole('button', { name: 'Add as editor' }, { timeout: 2000 }));
    await screen.findByText(li(/Ann Lee.*Editor.*Person/));
    expect(calls.find(c => c.method === 'PUT')).toEqual({ method: 'PUT', url: '/api/drive/folders/d1/shares', body: { principal_kind: 'user', principal_id: 'u2', role: 'editor' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0]);
    await waitFor(() => expect(screen.queryByText(/Finance/)).toBeNull());
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/drive/shares/s1')).toBe(true);
  });

  it('offers groups when the directory is available', async () => {
    const puts: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => { if (init?.method === 'PUT') { puts.push(JSON.parse(String(init.body))); return json({}); } return json([]); }));
    render(<ShareDialog kind="file" id="f1" name="a.docx" groups={[{ id: 'g1', display_name: 'Finance' }]} maxRole="manager" onClose={() => {}} />);
    expect(await screen.findByText('Not shared with anyone yet.')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Or a group'), { target: { value: 'g1' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add group' }));
    await waitFor(() => expect(puts).toEqual([{ principal_kind: 'group', principal_id: 'g1', role: 'reader' }]));
  });

  it('creates an organisation link, shows it once and revokes it', async () => {
    let links: unknown[] = [];
    const calls: { method: string; url: string; body?: unknown }[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input), method = init?.method ?? 'GET';
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (method === 'POST') { links = [{ id: 'l1', role: 'editor', expires: 0, created: '2026-10-15T10:00:00Z', created_by: 'Owner', opened: 0 }]; return json({ link: links[0], url: 'https://drive.example/l/#tok' }, 201); }
      if (method === 'DELETE') { links = []; return new Response(null, { status: 204 }); }
      return json(url.endsWith('/links') ? links : []);
    }));
    render(<ShareDialog kind="file" id="f1" name="a.docx" groups={[]} maxRole="manager" onClose={() => {}} />);
    fireEvent.change(await screen.findByLabelText('Link access'), { target: { value: 'editor' } });
    fireEvent.change(screen.getByLabelText('Expires'), { target: { value: '0' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create link' }));
    expect(await screen.findByText('https://drive.example/l/#tok')).toBeTruthy();
    expect(calls.find(c => c.method === 'POST')).toEqual({ method: 'POST', url: '/api/drive/files/f1/links', body: { role: 'editor', expires: 0 } });
    expect(await screen.findByText(li(/Editor link.*Never expires · opened by 0/))).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(screen.queryByText(/Editor link/)).toBeNull());
    expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/drive/links/l1')).toBe(true);
  });
});
