import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { Backup } from './Backup';

function mockStatus(body: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (!url.endsWith('/api/backup/status')) throw new Error(`unexpected fetch ${url}`);
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }),
  );
}

// PAIRED is a healthy SQLite instance: the driver decides whether the screen warns that no
// capsule can carry the database.
const PAIRED = {
  key_pinned: true,
  paired: true,
  interval_sec: 86400,
  recovery_url: 'https://recovery.example',
  recovery_key_id: 'k1',
  threshold: 2,
  total_shares: 3,
  database_driver: 'sqlite',
  members: ['data/ky_server.db'],
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Backup', () => {
  const copy = (n: number) => ({ name: `c${n}.capsule`, size_bytes: 10, created_at: '2026-10-30T12:00:00Z' });
  const tone = (text: string) => screen.getByText(text, { selector: '.ky-status' }).getAttribute('data-ky-status');

  it('rates local copies against the number kept', async () => {
    for (const [copies, text, want] of [[0, '0 of 3', 'danger'], [2, '2 of 3', 'warning'], [3, '3 of 3', 'success']] as const) {
      mockStatus({ ...PAIRED, local_dir: '/backups', local_keep: 3, local_copies: Array.from({ length: copies }, (_, i) => copy(i)) });
      render(<Backup />);
      await screen.findByText(text, { selector: '.ky-status' });
      expect(tone(text)).toBe(want);
      cleanup();
    }
    mockStatus({ ...PAIRED, interval_sec: 86400 });
    render(<Backup />);
    await screen.findByText('KY_BACKUP_DIR not set');
    expect(tone('Off')).toBeNull();
  });

  it('shows a running schedule as success and waiting or off as neutral', async () => {
    for (const [body, text, want] of [
      [PAIRED, 'Daily', 'success'],
      [{ ...PAIRED, key_pinned: false, paired: false }, 'Daily', null],
      [{ ...PAIRED, interval_sec: 0, local_dir: '/backups', local_keep: 3, local_copies: [] }, 'Off', null],
    ] as const) {
      mockStatus(body);
      render(<Backup />);
      await screen.findByText(text, { selector: '.ky-status' });
      expect(tone(text)).toBe(want);
      cleanup();
    }
  });

  it('warns when a key is pinned but there is no destination', async () => {
    mockStatus({ key_pinned: true, paired: false, interval_sec: 0, recovery_key_id: 'k1', threshold: 2, total_shares: 3, database_driver: 'sqlite', members: [] });
    render(<Backup />);
    expect(await screen.findByText(/nowhere to go/i)).toBeTruthy();
    expect(screen.getByText(/automatic backups are off/i)).toBeTruthy();
    expect(screen.getByText(/KY_BACKUP_DIR not set/)).toBeTruthy();
  });

  it('says what a schedule waits for instead of a next run it cannot make', async () => {
    mockStatus({ key_pinned: false, paired: false, interval_sec: 86400, next_run_at: '2026-10-31T12:00:00Z', database_driver: 'sqlite', members: [] });
    render(<Backup />);
    expect(await screen.findByText('Waiting for recovery key')).toBeTruthy();
    expect(screen.queryByText(/^Next /)).toBeNull();
    cleanup();
    mockStatus({ ...PAIRED, next_run_at: '2026-10-31T12:00:00Z' });
    render(<Backup />);
    expect(await screen.findByText(/^Next /)).toBeTruthy();
  });

  it('never renders the token', async () => {
    mockStatus({
      key_pinned: true,
      paired: true,
      interval_sec: 86400,
      recovery_url: 'https://recovery.example',
      recovery_key_id: 'k1',
      threshold: 2,
      total_shares: 3,
      database_driver: 'sqlite',
      members: ['data/ky_server.db'],
    });
    const { container } = render(<Backup />);
    expect(await screen.findByText('https://recovery.example')).toBeTruthy();
    expect(container.textContent).not.toMatch(/token/i);
    expect(screen.getByText('data/ky_server.db')).toBeTruthy();
    expect(screen.getByRole('button', { name: /unpair/i })).toBeTruthy();
  });

  it('says so on Postgres, where no capsule carries the database', async () => {
    mockStatus({ ...PAIRED, database_driver: 'postgres' });
    render(<Backup />);
    expect(await screen.findByText(/Backups do not include the PostgreSQL database/)).toBeTruthy();
  });

  it('does not warn about the database on SQLite', async () => {
    mockStatus(PAIRED);
    render(<Backup />);
    await screen.findByText('https://recovery.example');
    expect(screen.queryByText(/Backups do not include the PostgreSQL database/)).toBeNull();
    expect(screen.queryByText(/encryption key is stored in the data directory/)).toBeNull();
  });

  it('warns when the encryption key lives in the data directory', async () => {
    mockStatus({ ...PAIRED, key_in_data_dir: true });
    render(<Backup />);
    expect(await screen.findByText(/encryption key is stored in the data directory/)).toBeTruthy();
  });

  it('shows the offsite repository and its integrity check', async () => {
    mockStatus({ ...PAIRED, bulk_repository: 'rest:https://backup.example/kydrive', bulk_last_check: { at: '2026-10-13T00:00:00Z' } });
    render(<Backup />);
    expect(await screen.findByText('rest:https://backup.example/kydrive')).toBeTruthy();
    expect(screen.getByText(/Integrity check passed/)).toBeTruthy();
    cleanup();
    mockStatus({ ...PAIRED, bulk_repository: '/app/bulk', bulk_last_check: { at: '2026-10-13T00:00:00Z', error: 'Fatal: repository contains errors' } });
    render(<Backup />);
    expect((await screen.findByText(/Integrity check .* failed: Fatal: repository contains errors/)).closest('[role="alert"]')).toBeTruthy();
    cleanup();
    mockStatus({ ...PAIRED, bulk_repository_error: 'bulk repository must use rest:https://' });
    render(<Backup />);
    expect(await screen.findByText(/must use rest:https/)).toBeTruthy();
  });

  it('saves the document retention and shows the last prune', async () => {
    const sent: unknown[] = [];
    let status: Record<string, unknown> = {
      ...PAIRED,
      bulk_retention: { keep_daily: 0, keep_weekly: 0, keep_monthly: 0 },
      bulk_keep_last: 7,
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/api/backup/bulk-retention') && init?.method === 'PUT') {
          const body = JSON.parse(String(init.body));
          sent.push(body);
          status = { ...status, bulk_retention: body, bulk_last_prune: { at: '2026-10-13T00:00:00Z', forgotten: 3 } };
          return new Response(JSON.stringify(body), { status: 200 });
        }
        if (url.endsWith('/api/backup/status')) return new Response(JSON.stringify(status), { status: 200 });
        throw new Error(`unexpected fetch ${url}`);
      }),
    );
    render(<Backup />);
    expect(await screen.findByText(/never removes the newest 7 deposit snapshots/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Daily snapshots'), { target: { value: '7' } });
    fireEvent.change(screen.getByLabelText('Monthly snapshots'), { target: { value: '12' } });
    fireEvent.click(within(screen.getByLabelText('Daily snapshots').closest('form')!).getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/pruned after the next scheduled backup/)).toBeTruthy();
    expect(sent).toEqual([{ keep_daily: 7, keep_weekly: 0, keep_monthly: 12 }]);
    expect(await screen.findByText(/3 snapshots removed/)).toBeTruthy();
  });
});
