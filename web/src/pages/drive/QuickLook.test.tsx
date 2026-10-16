import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { QuickLook, previewKind, textLimit } from './QuickLook';
import { hasEditor, opensInEditor } from './client';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

function serve(type: string | null, status = 200, body = '', headers: Record<string, string> = {}) {
  const calls: [string, RequestInit | undefined][] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push([String(input), init]);
    const h = new Headers(headers);
    if (type) h.set('Content-Type', type);
    return new Response(init?.method === 'HEAD' ? null : body, { status, headers: h });
  }));
  return calls;
}

const file = { id: 'f/1', name: 'holiday.png' };
const url = '/api/drive/files/f%2F1/preview';

describe('QuickLook', () => {
  it('shows an image from the preview route', async () => {
    serve('image/png');
    render(<QuickLook file={file} onClose={() => {}} />);
    expect((await screen.findByRole('img', { name: 'holiday.png' })).getAttribute('src')).toBe(url);
    expect(screen.getByRole('link', { name: 'Open in new tab' }).getAttribute('href')).toBe(url);
  });

  it('frames a PDF and plays media', async () => {
    serve('application/pdf');
    render(<QuickLook file={{ id: 'p', name: 'a.pdf' }} onClose={() => {}} />);
    expect((await screen.findByTitle('a.pdf')).tagName).toBe('IFRAME');
    cleanup();
    serve('video/mp4');
    const { container } = render(<QuickLook file={{ id: 'v', name: 'a.mp4' }} onClose={() => {}} />);
    await vi.waitFor(() => expect(container.ownerDocument.querySelector('video')).not.toBeNull());
  });

  it('reads text as text, asking for the first part only', async () => {
    const calls = serve('text/plain; charset=utf-8', 206, '<script>alert(1)</script>', { 'Content-Range': `bytes 0-${textLimit - 1}/${textLimit + 10}` });
    render(<QuickLook file={{ id: 't', name: 'notes.txt' }} onClose={() => {}} />);
    expect(await screen.findByText('<script>alert(1)</script>')).toBeTruthy();
    expect(screen.getByText(/first 256 KiB/)).toBeTruthy();
    expect(new Headers(calls[1][1]?.headers).get('Range')).toBe(`bytes=0-${textLimit - 1}`);
    expect(document.querySelector('script')).toBeNull();
  });

  it('offers download when there is no preview', async () => {
    serve(null, 415);
    render(<QuickLook file={{ id: 'x', name: 'page.html' }} onClose={() => {}} />);
    expect(await screen.findByText(/No preview for this file/)).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Open in new tab' })).toBeNull();
    expect(screen.getByRole('link', { name: 'Download' }).getAttribute('href')).toBe('/api/drive/files/x/download');
  });

  it('says when access is refused', async () => {
    serve(null, 403);
    render(<QuickLook file={file} onClose={() => {}} />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/cannot open/);
  });
});

describe('opening files', () => {
  it('routes names to an editor or quick look', () => {
    expect(previewKind('application/ogg')).toBe('audio');
    expect(previewKind('text/html; charset=utf-8')).toBeNull();
    expect(['report.DOCX', 'board.excalidraw', 'notes.md'].every(opensInEditor)).toBe(true);
    expect(['paper.pdf', 'photo.jpg', 'README', '.docx'].some(opensInEditor)).toBe(false);
    expect(hasEditor('paper.pdf')).toBe(true);
  });
});
