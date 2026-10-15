import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { Search } from './Search';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.useRealTimers(); });

const workspaces = [{ id: 'w1', name: 'Team', role: 'editor', kind: 'shared' as const, quota: 1, used: 0, trash_days: 0, keep_versions: 0, editors_share: false }];
const results = [{ kind: 'file', id: 'f1', workspace: 'w1', parent: 'd1', name: 'Q1 budget.xlsx', path: 'Reports' }];

describe('Search', () => {
  it('waits for two characters and a pause, then opens a result', async () => {
    vi.useFakeTimers();
    const urls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => { urls.push(String(input)); return new Response(JSON.stringify({ results, truncated: true }), { status: 200 }); }));
    const open = vi.fn();
    render(<Search workspaces={workspaces} onOpen={open} />);
    const box = screen.getByLabelText('Search files');
    fireEvent.change(box, { target: { value: 'b' } });
    await act(() => vi.advanceTimersByTimeAsync(500));
    fireEvent.change(box, { target: { value: 'bu' } });
    fireEvent.change(box, { target: { value: 'bud get' } });
    await act(() => vi.advanceTimersByTimeAsync(299));
    expect(urls).toEqual([]);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(urls).toEqual(['/api/drive/search?q=bud%20get']);
    expect(screen.getByText('Team / Reports')).toBeTruthy();
    expect(screen.getByText(/Showing the first 1 matches/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Q1 budget.xlsx' }));
    expect(open).toHaveBeenCalledWith(results[0]);
  });

  it('says when nothing matched', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results: [], truncated: false }), { status: 200 })));
    render(<Search workspaces={workspaces} onOpen={() => {}} />);
    fireEvent.change(screen.getByLabelText('Search files'), { target: { value: 'zz' } });
    expect(await screen.findByText('No matches')).toBeTruthy();
  });
});
