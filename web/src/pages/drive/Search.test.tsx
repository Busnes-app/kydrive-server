import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { nameMatches, Search } from './Search';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.useRealTimers(); history.replaceState(null, '', '/'); });

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
    fireEvent.click(screen.getByRole('button', { name: /Q1 budget\.xlsx/ }));
    expect(open).toHaveBeenCalledWith(results[0]);
  });

  it('hides the results on Escape and shows them again on focus', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results, truncated: false }), { status: 200 })));
    render(<Search workspaces={workspaces} onOpen={() => {}} />);
    const box = screen.getByLabelText('Search files');
    fireEvent.change(box, { target: { value: 'budget' } });
    expect(await screen.findByText('Team / Reports')).toBeTruthy();
    fireEvent.keyDown(box, { key: 'Escape' });
    expect(screen.queryByText('Team / Reports')).toBeNull();
    fireEvent.focus(box);
    expect(screen.getByText('Team / Reports')).toBeTruthy();
  });

  it('names where a shared result comes from instead of a workspace', async () => {
    const shared = { kind: 'file', id: 'f2', workspace: 'w9', parent: 'd9', name: 'Plan.docx', path: 'Projects/Q1', shared: { from: 'Ann Lee', role: 'reader' } };
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results: [shared], truncated: false }), { status: 200 })));
    const open = vi.fn();
    render(<Search workspaces={workspaces} onOpen={open} />);
    fireEvent.change(screen.getByLabelText('Search files'), { target: { value: 'plan' } });
    expect(await screen.findByText('Ann Lee / Projects/Q1')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /Plan\.docx/ }));
    expect(open).toHaveBeenCalledWith(shared);
  });

  it('refuses a malformed shared field', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results: [{ ...results[0], shared: { from: 1 } }], truncated: false }), { status: 200 })));
    render(<Search workspaces={workspaces} onOpen={() => {}} />);
    fireEvent.change(screen.getByLabelText('Search files'), { target: { value: 'budget' } });
    expect(await screen.findByRole('alert')).toBeTruthy();
  });

  it('says when nothing matched', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results: [], truncated: false }), { status: 200 })));
    render(<Search workspaces={workspaces} onOpen={() => {}} />);
    fireEvent.change(screen.getByLabelText('Search files'), { target: { value: 'zz' } });
    expect(await screen.findByText('No matches')).toBeTruthy();
  });

  it('marks snippet highlights by UTF-16 offsets as text and says when indexing is behind', async () => {
    // "😀" is two UTF-16 code units, so "budget" starts at 9 in "Intro 😀 budget".
    const snip = { text: 'Intro 😀 budget <b>x</b> plan', highlights: [[9, 15], [25, 29], [40, 50], [2, 1]], cut_start: true, cut_end: false };
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results: [{ ...results[0], match: ['content'], snippet: snip }], truncated: false, contents: { searched: true, pending: 3 } }), { status: 200 })));
    const { container } = render(<Search workspaces={workspaces} onOpen={() => {}} />);
    fireEvent.change(screen.getByLabelText('Search files'), { target: { value: 'budget plan' } });
    expect(await screen.findByText('budget')).toBeTruthy();
    const marks = [...container.querySelectorAll('mark')].map(m => m.textContent);
    expect(marks).toEqual(['budget', 'plan']);
    const p = container.querySelector('.drive-search-snippet');
    expect(p?.textContent).toBe('…Intro 😀 budget <b>x</b> plan');
    expect(p?.querySelector('b')).toBeNull();
    expect(screen.getByText(/Still indexing 3 of your files/)).toBeTruthy();
  });

  it('marks the matched words in a name match, case-insensitively and as text', async () => {
    const hits = [{ ...results[0], name: 'Q4 Budget (budget).csv', match: ['name', 'content'] }, { ...results[0], id: 'f2', name: 'Budget notes.txt', match: ['content'] }];
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results: hits, truncated: false }), { status: 200 })));
    const { container } = render(<Search workspaces={workspaces} onOpen={() => {}} />);
    fireEvent.change(screen.getByLabelText('Search files'), { target: { value: ' budget ' } });
    expect(await screen.findByRole('button', { name: /Q4 Budget \(budget\)\.csv/ })).toBeTruthy();
    expect([...container.querySelectorAll('mark')].map(m => m.textContent)).toEqual(['Budget', 'budget']);
    expect(nameMatches('a.b (c)', '.b (')).toEqual([[1, 5]]);
    expect(nameMatches('Ärger', 'är')).toEqual([[0, 2]]);
    // The content syntax is not part of a name match.
    expect(nameMatches('Conference call.log', 'conf*')).toEqual([[0, 4]]);
    expect(nameMatches('Site report.docx', '"site report"')).toEqual([[0, 11]]);
    expect(nameMatches('a*b.txt', 'a*b')).toEqual([[0, 3]]);
    expect(nameMatches('x.txt', '**')).toEqual([]);
  });

  it('explains the syntax and says when a phrase or prefix search was cut short', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results: [], truncated: false, contents: { searched: true, pending: 0, unverified: 7, expanded_truncated: true } }), { status: 200 })));
    render(<Search workspaces={workspaces} onOpen={() => {}} />);
    fireEvent.change(screen.getByLabelText('Search files'), { target: { value: '"site report" co*' } });
    expect(await screen.findByText(/7 more files have these words but were not checked for the exact phrase/)).toBeTruthy();
    expect(screen.getByText(/only the most common were searched/)).toBeTruthy();
    expect(screen.getByText(/Add \* to 3 or more letters/)).toBeTruthy();
  });

  it('refuses a malformed snippet', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results: [{ ...results[0], snippet: { text: '<img src=x onerror=alert(1)>', highlights: 'all' } }], truncated: false }), { status: 200 })));
    const { container } = render(<Search workspaces={workspaces} onOpen={() => {}} />);
    fireEvent.change(screen.getByLabelText('Search files'), { target: { value: 'zz' } });
    expect(await screen.findByRole('alert')).toBeTruthy();
    expect(container.querySelector('img')).toBeNull();
  });

  it('filters by type from a row of toggle chips, with or without a query, and keeps the type in the address', async () => {
    const urls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => { urls.push(String(input)); return new Response(JSON.stringify({ results, truncated: false }), { status: 200 }); }));
    const open = vi.fn();
    render(<Search workspaces={workspaces} onOpen={open} />);
    const box = screen.getByLabelText('Search files');
    expect(screen.queryByRole('group', { name: 'File type' })).toBeNull();
    fireEvent.focus(box);
    const group = screen.getByRole('group', { name: 'File type' });
    expect([...group.querySelectorAll('button')].map(b => b.textContent)).toEqual(['Folders', 'Documents', 'Spreadsheets', 'Presentations', 'PDFs', 'Images', 'Videos', 'Audio', 'Archives']);
    expect(screen.getByText(/Enter at least 2 characters, or choose a type/)).toBeTruthy();
    const pdfs = screen.getByRole('button', { name: 'PDFs' });
    expect(pdfs.getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(pdfs);
    expect(pdfs.getAttribute('aria-pressed')).toBe('true');
    expect(location.search).toBe('?type=pdfs');
    expect(await screen.findByText('Team / Reports')).toBeTruthy();
    expect(urls).toEqual(['/api/drive/search?q=&type=pdfs']);
    expect(screen.queryByText(/match whole words/)).toBeNull();
    fireEvent.change(box, { target: { value: 'budget' } });
    await vi.waitFor(() => expect(urls.at(-1)).toBe('/api/drive/search?q=budget&type=pdfs'));
    // One type at a time; pressing the chosen one again clears it.
    fireEvent.click(screen.getByRole('button', { name: 'Images' }));
    expect(location.search).toBe('?type=images');
    expect(screen.getAllByRole('button', { pressed: true }).map(b => b.textContent)).toEqual(['Images']);
    fireEvent.click(screen.getByRole('button', { name: 'Images' }));
    expect(location.search).toBe('');
    await vi.waitFor(() => expect(urls.at(-1)).toBe('/api/drive/search?q=budget'));
    // Back restores the previous type.
    history.back();
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Images' }).getAttribute('aria-pressed')).toBe('true'));
    await vi.waitFor(() => expect(urls.at(-1)).toBe('/api/drive/search?q=budget&type=images'));
    // Opening a result clears the type, so Back comes back to this list.
    fireEvent.click(await screen.findByRole('button', { name: /Q1 budget\.xlsx/ }));
    expect(open).toHaveBeenCalled();
    expect(location.search).toBe('');
    expect(screen.queryByRole('group', { name: 'File type' })).toBeNull();
  });

  it('opens with the type in the address and ignores an unknown one', async () => {
    const urls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => { urls.push(String(input)); return new Response(JSON.stringify({ results, truncated: false }), { status: 200 }); }));
    history.replaceState(null, '', '/?type=folders#/drive');
    const { unmount } = render(<Search workspaces={workspaces} onOpen={() => {}} />);
    expect(screen.getByRole('button', { name: 'Folders' }).getAttribute('aria-pressed')).toBe('true');
    await vi.waitFor(() => expect(urls).toEqual(['/api/drive/search?q=&type=folders']));
    unmount();
    history.replaceState(null, '', '/?type=executables');
    render(<Search workspaces={workspaces} onOpen={() => {}} />);
    expect(screen.queryByRole('group', { name: 'File type' })).toBeNull();
    fireEvent.focus(screen.getByLabelText('Search files'));
    expect(screen.queryAllByRole('button', { pressed: true })).toEqual([]);
    expect(urls).toHaveLength(1);
  });

  it('moves through the chips by keyboard and closes on Escape', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results, truncated: false }), { status: 200 })));
    render(<Search workspaces={workspaces} onOpen={() => {}} />);
    fireEvent.focus(screen.getByLabelText('Search files'));
    const docs = screen.getByRole('button', { name: 'Documents' });
    expect(docs.tagName).toBe('BUTTON');
    expect(docs.getAttribute('type')).toBe('button');
    docs.focus();
    fireEvent.keyDown(docs, { key: 'Escape' });
    expect(screen.queryByRole('group', { name: 'File type' })).toBeNull();
  });
});
