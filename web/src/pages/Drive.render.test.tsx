import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { Drive } from './Drive';
import type { Workspace } from './drive/client';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });
const ws = (id: string, kind: 'personal' | 'shared', role: string): Workspace => ({ id, name: id === 'p1' ? 'My files' : 'Team', role, kind, quota: 1 << 30, used: 0, trash_days: 0, keep_versions: 0, editors_share: false });

function Harness({ admin, selected }: { admin: boolean; selected: string }) {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  return <Drive admin={admin} workspaces={workspaces} onWorkspacesChange={setWorkspaces} selected={selected} onSelectWorkspace={() => {}} openInNewTab={false} />;
}

describe('Drive', () => {
  // Pass 4 R2: a dedicated installer administrator has no My files (403) but manages shared access.
  it('loads a shared workspace\'s grants for an administrator the server refuses My files', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/drive/personal-workspace') return json({ error: 'Use your everyday identity for personal files and editing' }, 403);
      if (url.startsWith('/api/drive/workspaces?')) return json([ws('w2', 'shared', '')]);
      if (url.endsWith('/grants')) return json([{ group_id: 'g1', name: 'Finance', role: 'reader' }]);
      if (url === '/api/drive/directory') return json({ groups: [{ id: 'g1', display_name: 'Finance' }] });
      return json([]);
    }));
    render(<Harness admin selected="w2" />);
    fireEvent.click(await screen.findByRole('tab', { name: 'Workspace settings' }));
    expect((await screen.findAllByText('Finance')).length).toBeGreaterThan(0);
    expect(screen.queryByText(/everyday identity/)).toBeNull();
  });

  // Pass 4 R1: rows of the previous workspace never sit under the next one, where Rename would move them.
  it('clears the previous workspace\'s rows while the next one loads', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/drive/personal-workspace') return json({});
      if (url.startsWith('/api/drive/workspaces?') || url === '/api/drive/workspaces') return json([ws('p1', 'personal', 'manager'), ws('w2', 'shared', 'editor')]);
      if (url.startsWith('/api/drive/workspaces/w2/')) return new Promise<Response>(() => {});
      if (url.startsWith('/api/drive/workspaces/p1/files')) return json([{ id: 'f1', name: 'Private.txt', parent: '', revision: 1, size: 1, trashed: false }]);
      return json([]);
    }));
    const { rerender } = render(<Harness admin={false} selected="p1" />);
    await screen.findByText('Private.txt');
    rerender(<Harness admin={false} selected="w2" />);
    await screen.findByRole('heading', { name: 'Team' });
    expect(screen.queryByText('Private.txt')).toBeNull();
  });
});
