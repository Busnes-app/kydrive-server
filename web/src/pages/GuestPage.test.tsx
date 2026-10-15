import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { GuestPage } from './GuestPage';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); history.replaceState(null, '', '/'); });

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });

describe('GuestPage', () => {
  it('asks for the password, then lists the folder and sends the token only in bodies', async () => {
    history.replaceState(null, '', '/g/#tok123');
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).not.toContain('tok123');
      const body = JSON.parse(String(init?.body));
      bodies.push(body);
      if (body.password !== 'correct horse') return json({ error: 'Password required' }, 401);
      return json({ name: 'Deliverables', kind: 'folder', upload_only: false, path: [{ kind: 'folder', id: 'd1', name: 'Deliverables', size: 0 }], items: [{ kind: 'file', id: 'f1', name: 'report.pdf', size: 2048 }] });
    }));
    render(<GuestPage />);
    fireEvent.change(await screen.findByLabelText('Password'), { target: { value: 'wrong one' } });
    fireEvent.click(screen.getByRole('button', { name: 'Open' }));
    expect(await screen.findByText('That password is not right.')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'correct horse' } });
    fireEvent.click(screen.getByRole('button', { name: 'Open' }));
    expect(await screen.findByText(/report\.pdf/)).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Deliverables' })).toBeTruthy();
    expect(bodies[0]).toEqual({ token: 'tok123', password: '', folder: '' });
    const form = document.querySelector('form[action="/api/public/links/download"]') as HTMLFormElement;
    const submit = vi.fn();
    form.submit = submit;
    fireEvent.click(screen.getByRole('button', { name: 'Download' }));
    expect(submit).toHaveBeenCalled();
    expect((form.elements.namedItem('file') as HTMLInputElement).value).toBe('f1');
    expect((form.elements.namedItem('token') as HTMLInputElement).value).toBe('tok123');
  });

  it('sends files through an upload-only link without listing anything', async () => {
    history.replaceState(null, '', '/g/#drop');
    const uploads: { url: string; token: string | null }[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.startsWith('/api/public/links/upload')) { uploads.push({ url, token: new Headers(init?.headers).get('X-Link-Token') }); return json({ name: 'invoice.pdf' }, 201); }
      return json({ name: 'Inbox', kind: 'folder', upload_only: true, path: [{ kind: 'folder', id: 'd1', name: 'Inbox', size: 0 }], items: [] });
    }));
    const { container } = render(<GuestPage />);
    expect(await screen.findByText(/You will not see what is already in this folder/)).toBeTruthy();
    const input = container.querySelector('input[type=file]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [new File(['pdf'], 'invoice.pdf')] } });
    await waitFor(() => expect(screen.getByText('Sent: invoice.pdf')).toBeTruthy());
    expect(uploads).toEqual([{ url: '/api/public/links/upload?name=invoice.pdf', token: 'drop' }]);
  });

  it('explains a dead link', async () => {
    history.replaceState(null, '', '/g/#gone');
    vi.stubGlobal('fetch', vi.fn(async () => json({ error: 'Not found' }, 404)));
    render(<GuestPage />);
    expect(await screen.findByText(/This link does not work/)).toBeTruthy();
  });
});
