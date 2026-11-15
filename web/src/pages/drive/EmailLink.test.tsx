import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { Drive } from '../Drive';
import type { Workspace } from './client';
import type { SuiteApp } from '../../components/AppHeader';
import { composeURL, kypostOrigin } from './EmailLink';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });
const home: Workspace = { id: 'p1', name: 'My files', role: 'manager', kind: 'personal', quota: 1 << 30, used: 0, trash_days: 0, keep_versions: 0, editors_share: false };
const kypost = { id: 'kypost', name: 'KyPost', url: 'https://post.example/login' };

function Harness({ apps }: { apps: SuiteApp[] }) {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  return <Drive apps={apps} admin={false} workspaces={workspaces} onWorkspacesChange={setWorkspaces} selected="p1" onSelectWorkspace={() => {}} openInNewTab={false} />;
}

function server() {
  const posts: string[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (init?.method === 'POST' && url.endsWith('/links')) { posts.push(`${url} ${String(init.body)}`); return json({ link: {}, url: 'https://drive.example/l/#tok' }, 201); }
    if (url.startsWith('/api/drive/workspaces') && !url.includes('/p1/')) return json([home]);
    if (url.includes('/files?')) return json([{ id: 'f1', name: 'Q3 report.pdf', parent: '', revision: 1, size: 10, trashed: false }]);
    if (url.includes('/folders?')) return json([]);
    return json({});
  }));
  return posts;
}

async function menu(): Promise<void> {
  fireEvent.click(await screen.findByRole('button', { name: 'Actions for Q3 report.pdf' }));
}

describe('Email a link', () => {
  it('is offered only when KyPost is in the suite app list over https', async () => {
    server();
    const { unmount } = render(<Harness apps={[]} />);
    await menu();
    expect(screen.queryByRole('menuitem', { name: 'Email a link' })).toBeNull();
    unmount();
    render(<Harness apps={[{ ...kypost, url: 'http://post.example/' }]} />);
    await menu();
    expect(screen.queryByRole('menuitem', { name: 'Email a link' })).toBeNull();
    expect(kypostOrigin([kypost])).toBe('https://post.example');
    expect(kypostOrigin([{ ...kypost, url: 'not a url' }])).toBe('');
  });

  it('makes an organisation reader link, then opens KyPost compose with the name and link', async () => {
    const posts = server();
    const tab = { opener: {} as unknown, location: { href: '' }, close: vi.fn() };
    vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window);
    render(<Harness apps={[kypost]} />);
    await menu();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Email a link' }));
    fireEvent.change(screen.getByLabelText('Link expires'), { target: { value: '7' } });
    expect(posts).toEqual([]);
    fireEvent.click(screen.getByRole('button', { name: 'Write email' }));
    await waitFor(() => expect(tab.location.href).not.toBe(''));
    expect(tab.opener).toBeNull();
    expect(posts).toHaveLength(1);
    expect(posts[0]).toMatch(/^\/api\/drive\/files\/f1\/links \{"role":"reader","expires":\d+\}$/);
    const u = new URL(tab.location.href);
    expect(u.origin + u.pathname).toBe('https://post.example/compose');
    expect(u.searchParams.get('subject')).toBe('Q3 report.pdf');
    expect(u.searchParams.get('body')).toContain('https://drive.example/l/#tok');
    expect(u.searchParams.get('to')).toBeNull();
  });

  it('keeps subject and body within the deep link limits', () => {
    const u = new URL(composeURL('https://post.example', 'é'.repeat(200), 'https://drive.example/l/#' + 'x'.repeat(3000), 0));
    expect(new TextEncoder().encode(u.searchParams.get('subject') ?? '').length).toBeLessThanOrEqual(255);
    expect(new TextEncoder().encode(u.searchParams.get('body') ?? '').length).toBeLessThanOrEqual(2000);
  });
});
