import { afterEach, describe, expect, it, vi } from 'vitest';
import { openPendingLink, opensLinkInEditor, takeLinkFromURL } from './links';

afterEach(() => { vi.unstubAllGlobals(); sessionStorage.clear(); history.replaceState(null, '', '/'); });

describe('organisation links', () => {
  it('sends only editor files to the editor', () => {
    const item = (kind: 'file' | 'folder', name: string) => ({ kind, id: 'f1', workspace: 'w1', workspace_name: 'Ann', parent: '', name, role: 'reader' });
    expect(opensLinkInEditor(item('file', 'notes.docx'))).toBe(true);
    for (const name of ['report.pdf', 'site-survey.bin', 'photo.jpg']) expect(opensLinkInEditor(item('file', name))).toBe(false);
    expect(opensLinkInEditor(item('folder', 'Plans'))).toBe(false);
  });

  it('moves the token out of the address bar, then opens it once', async () => {
    history.replaceState(null, '', '/l/#secret-token');
    takeLinkFromURL();
    expect(location.pathname + location.hash).toBe('/');
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
      bodies.push(JSON.parse(String(init?.body)));
      return new Response(JSON.stringify({ kind: 'folder', id: 'd1', workspace: 'w1', workspace_name: 'Team', parent: '', name: 'Plans', role: 'reader' }), { status: 200 });
    }));
    expect((await openPendingLink())?.name).toBe('Plans');
    expect(bodies).toEqual([{ token: 'secret-token' }]);
    expect(await openPendingLink()).toBeNull(); // used once
  });

  it('ignores other paths', () => {
    history.replaceState(null, '', '/files#x');
    takeLinkFromURL();
    expect(location.hash).toBe('#x');
    expect(sessionStorage.length).toBe(0);
  });
});
