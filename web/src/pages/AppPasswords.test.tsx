import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AppPasswords, grant } from './AppPasswords';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });

describe('AppPasswords', () => {
  it('shows a new password once, lists it without the secret and revokes after confirming', async () => {
    let stored: unknown[] = [];
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      calls.push(`${method} ${String(input)}`);
      if (method === 'POST') { stored = [{ id: 'p1', name: 'Laptop', created: '2026-10-15T10:00:00Z', last_used: 0, scope: 'dav' }]; return json({ id: 'p1', name: 'Laptop', created: '2026-10-15T10:00:00Z', scope: 'dav', password: 'abcd-efgh-ijkl-mnop-qrst-uvwx-yz23-4567', username: 'alice', url: 'https://drive.example/dav/' }, 201); }
      if (method === 'DELETE') { stored = []; return new Response(null, { status: 204 }); }
      return json(stored);
    }));
    vi.stubGlobal('confirm', vi.fn(() => true));
    render(<AppPasswords />);
    fireEvent.change(await screen.findByLabelText('Device or app name'), { target: { value: 'Laptop' } });
    expect(screen.getByText('"Laptop" gets read and write access to all your files over WebDAV.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Create app password' }));
    expect(await screen.findByText('abcd-efgh-ijkl-mnop-qrst-uvwx-yz23-4567')).toBeTruthy();
    expect(screen.getAllByText('"Laptop" gets read and write access to all your files over WebDAV.').length).toBeGreaterThan(0);
    expect(screen.getByText('https://drive.example/dav/')).toBeTruthy();
    expect(screen.getByText(/Laptop · created 2026-10-15 · never used/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'I have copied it' }));
    expect(screen.queryByText('abcd-efgh-ijkl-mnop-qrst-uvwx-yz23-4567')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(screen.queryByText(/Laptop · created/)).toBeNull());
    expect(calls).toContain('DELETE /api/auth/app-passwords/p1');
  });

  it('does nothing when the revoke is not confirmed', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json([{ id: 'p1', name: 'Phone', created: '2026-10-15T10:00:00Z', last_used: 1760000000, scope: 'dav' }])));
    vi.stubGlobal('confirm', vi.fn(() => false));
    render(<AppPasswords />);
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('creates an AI agent token, sends its access and folder, and shows the setup once', async () => {
    let stored: unknown[] = [];
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST') {
        bodies.push(JSON.parse(String(init.body)));
        stored = [{ id: 'a1', name: 'Hermes', created: '2026-10-20T10:00:00Z', last_used: 0, scope: 'mcp:write', revoke_after: 1768800000, root: '/Team/Projects' }];
        return json({ id: 'a1', name: 'Hermes', created: '2026-10-20T10:00:00Z', scope: 'mcp:write', password: 'kya_abcdefghijklmnopqrstuvwxyz234567', root: '/Team/Projects', url: 'https://drive.example/mcp', skill: 'https://drive.example/agent/skills/kydrive/SKILL.md' }, 201);
      }
      return json(stored);
    }));
    render(<AppPasswords />);
    fireEvent.click(await screen.findByLabelText('AI agent'));
    expect(screen.queryByRole('note')).toBeNull();
    fireEvent.click(screen.getByLabelText('Read and write'));
    expect(screen.getByRole('note').textContent).toContain('never delete anything for good');
    fireEvent.change(screen.getByLabelText('Limit to folder (optional)'), { target: { value: '/Team/Projects' } });
    fireEvent.change(screen.getByLabelText('Agent name'), { target: { value: 'Hermes' } });
    // Before anything is issued, the form says who gets what.
    expect(screen.getByText('AI agent "Hermes" gets read and write access to /Team/Projects.')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('Read only'));
    expect(screen.getByText('AI agent "Hermes" gets read-only access to /Team/Projects.')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('Read and write'));
    fireEvent.click(screen.getByRole('button', { name: 'Create agent token' }));
    expect(await screen.findByText('kya_abcdefghijklmnopqrstuvwxyz234567')).toBeTruthy();
    expect(bodies).toEqual([{ name: 'Hermes', scope: 'mcp:write', folder: '/Team/Projects' }]);
    expect(screen.getByText(/AI agent "Hermes" gets read and write access to \/Team\/Projects\. It sees nothing outside that folder\./)).toBeTruthy();
    expect(screen.getAllByText(/Bearer \$\{KYDRIVE_TOKEN\}/)).toHaveLength(2);
    expect(screen.getByText(/hermes skills install https:\/\/drive.example\/agent\/skills\/kydrive\/SKILL.md/)).toBeTruthy();
    expect(screen.getByText(/Hermes · AI agent · created 2026-10-20/)).toBeTruthy();
    expect(screen.getByText(/Limited to \/Team\/Projects/)).toBeTruthy();
    expect(screen.getByText(/Revoked if unused by/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'I have copied it' }));
    expect(screen.queryByText('kya_abcdefghijklmnopqrstuvwxyz234567')).toBeNull();
  });

  it('asks before widening an agent to write and changes nothing when declined', async () => {
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${String(input)} ${init?.body ?? ''}`);
      return json(init?.method === 'PATCH' ? {} : [{ id: 'a1', name: 'Hermes', created: '2026-10-20T10:00:00Z', last_used: 0, scope: 'mcp:read', revoke_after: 1768800000, root_missing: true }]);
    }));
    const confirm = vi.fn(() => false);
    vi.stubGlobal('confirm', confirm);
    render(<AppPasswords />);
    const access = await screen.findByLabelText('Access for Hermes');
    expect(screen.getByText(/Its folder is in the trash or gone/)).toBeTruthy();
    fireEvent.change(access, { target: { value: 'mcp:write' } });
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(confirm.mock.calls[0]).toEqual([expect.stringContaining('AI agent "Hermes" gets read and write access to all your files.')]);
    expect(calls.some(c => c.startsWith('PATCH'))).toBe(false);
    confirm.mockReturnValue(true);
    fireEvent.change(access, { target: { value: 'mcp:write' } });
    await waitFor(() => expect(calls).toContain('PATCH /api/auth/app-passwords/a1 {"scope":"mcp:write"}'));
  });

  it('never issues a token without naming who gets it and at what access', () => {
    for (const [name, scope, root] of [['Hermes', 'mcp:read', undefined], ['OpenClaw', 'mcp:write', '/Team'], ['Phone', 'dav', undefined]] as const) {
      const text = grant(name, scope, root);
      expect(text).toContain(`"${name}"`);
      expect(text).toMatch(scope === 'mcp:read' ? /read-only/ : /read and write/);
      expect(text).not.toMatch(/an app|wants access/i);
    }
  });
});
