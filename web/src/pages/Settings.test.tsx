import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { TrashRetention } from './Settings';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe('TrashRetention', () => {
  it('shows the drive-wide default and saves a new age', async () => {
    const calls: [string, RequestInit | undefined][] = [];
    let days = 90;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push([String(input), init]);
      if (init?.method === 'PUT') days = JSON.parse(String(init.body)).trash_days;
      return Response.json({ trash_days: days });
    }));
    render(<TrashRetention />);
    expect((await screen.findByRole('status')).textContent).toBe('Trashed items are permanently deleted after 90 days.');
    fireEvent.change(screen.getByLabelText('Delete trashed items after (days)'), { target: { value: '0' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Trash is kept until someone empties it.')).toBeTruthy();
    const put = calls.find(([, init]) => init?.method === 'PUT');
    expect(put?.[0]).toBe('/api/drive/trash-retention');
    expect(JSON.parse(String(put?.[1]?.body))).toEqual({ trash_days: 0 });
  });

  it('reports a refused save', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_: RequestInfo | URL, init?: RequestInit) =>
      init?.method === 'PUT' ? Response.json({ error: 'trash_days must be 0 to 3650' }, { status: 400 }) : Response.json({ trash_days: 90 })));
    render(<TrashRetention />);
    await screen.findByRole('status');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toBe('trash_days must be 0 to 3650');
  });
});
