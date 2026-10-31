import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { Menu } from './Menu';
import { AppHeader } from './AppHeader';
import { useConfirm } from './Dialog';
import { relative, toDate } from './time';
import { When } from './When';
import { Versions } from '../pages/drive/Versions';
import { FileIcon } from '../pages/drive/FileIcon';
import { Login } from '../pages/Login';

afterEach(() => { cleanup(); vi.useRealTimers(); });

describe('Menu', () => {
  it('closes after an action and runs it', () => {
    const rename = vi.fn();
    render(<Menu label="Actions for a.docx" items={[{ label: 'Rename', onSelect: rename }, { label: 'Version history', onSelect: () => {} }]} />);
    const button = screen.getByRole('button', { name: 'Actions for a.docx' });
    fireEvent.click(button);
    expect(button.getAttribute('aria-expanded')).toBe('true');
    fireEvent.click(screen.getByRole('menuitem', { name: 'Rename' }));
    expect(rename).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('menu')).toBeNull();
    expect(button.getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(button);
  });

  it('closes on Escape and on a click outside, and moves with the arrow keys', () => {
    render(<><Menu label="More" items={[{ label: 'One', onSelect: () => {} }, 'separator', { label: 'Two', href: '/two' }]} /><p>Outside</p></>);
    const button = screen.getByRole('button', { name: 'More' });
    fireEvent.click(button);
    expect(document.activeElement?.textContent).toBe('One');
    fireEvent.keyDown(document, { key: 'ArrowDown' });
    expect(document.activeElement?.textContent).toBe('Two');
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('menu')).toBeNull();
    expect(document.activeElement).toBe(button);
    fireEvent.click(button);
    fireEvent.pointerDown(screen.getByText('Outside'));
    expect(screen.queryByRole('menu')).toBeNull();
  });
});

describe('useConfirm', () => {
  function Ask({ onAnswer }: { onAnswer: (ok: boolean) => void }) {
    const [confirm, node] = useConfirm();
    return <><button onClick={() => void confirm({ title: 'Delete it?', body: 'Gone for good.', confirm: 'Delete', danger: true }).then(onAnswer)}>Start</button>{node}</>;
  }
  it('resolves true only on the confirm button and styles it as danger', async () => {
    const answers: boolean[] = [];
    render(<Ask onAnswer={ok => answers.push(ok)} />);
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    const dialog = screen.getByRole('dialog', { name: 'Delete it?' });
    expect(within(dialog).getByRole('button', { name: 'Delete' }).className).toBe('btn-danger');
    await act(async () => { fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' })); });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    await act(async () => { fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Close' })); });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    await act(async () => { fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' })); });
    expect(answers).toEqual([false, false, true]);
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

describe('dates', () => {
  it('reads RFC 3339 with nanoseconds and Unix seconds', () => {
    expect(toDate('2026-10-21T02:19:28.441150584Z')?.toISOString()).toBe('2026-10-21T02:19:28.441Z');
    expect(toDate(1760000000)?.getTime()).toBe(1760000000000);
    expect(toDate('not a date')).toBeNull();
  });
  it('says how long ago in words', () => {
    const now = new Date('2026-10-21T12:00:00Z');
    expect(relative(new Date('2026-10-21T11:59:30Z'), now)).toBe('just now');
    expect(relative(new Date('2026-10-21T11:55:00Z'), now)).toBe('5 minutes ago');
    expect(relative(new Date('2026-10-20T12:00:00Z'), now)).toBe('yesterday');
    expect(relative(new Date('2026-10-24T12:00:00Z'), now)).toBe('in 3 days');
  });
  it('shows local time and keeps the exact instant in dateTime', () => {
    const { container } = render(<When value="2026-10-21T02:19:28.441150584Z" />);
    const time = container.querySelector('time')!;
    expect(time.getAttribute('dateTime')).toBe('2026-10-21T02:19:28.441Z');
    expect(time.textContent).not.toContain('T02:19');
    expect(time.textContent).toMatch(/2026.*·/);
  });
});

describe('Versions', () => {
  const file = { id: 'f1', name: 'Launch plan.docx', parent: '', revision: 2, size: 2, trashed: false };
  const versions = [{ revision: 1, size: 1200, created: '2026-10-21T02:57:45.140930121Z' }, { revision: 2, size: 25395, created: '2026-10-21T02:58:24.244592230Z' }];
  it('lists newest first in local time, marks the current one and restores only older ones', () => {
    const restore = vi.fn(), remove = vi.fn();
    render(<Versions file={file} versions={versions} busy={false} canRestore canDelete onRestore={restore} onDelete={remove} onClose={() => {}} />);
    const dialog = screen.getByRole('dialog', { name: 'Version history' });
    expect(dialog.textContent).not.toMatch(/\d{2}:\d{2}:\d{2}\.\d+Z/);
    const rows = within(dialog).getAllByRole('row').slice(1);
    expect(rows[0].textContent).toContain('Version 2');
    expect(rows[0].textContent).toContain('Current');
    expect(within(rows[0]).queryByRole('button')).toBeNull();
    fireEvent.click(within(rows[1]).getByRole('button', { name: 'Restore' }));
    expect(restore).toHaveBeenCalledWith(versions[0]);
    expect(within(rows[1]).getByRole('button', { name: 'Delete' }).className).toContain('btn-danger-quiet');
  });
});

describe('FileIcon for documents', () => {
  it('shows a type icon for .docx and .csv instead of a blank thumbnail', () => {
    for (const name of ['Launch plan.docx', 'Q4 budget.csv', 'Deck.pptx', 'Brand guide.pdf']) {
      const { container } = render(<FileIcon file={{ id: 'x', name }} />);
      expect(container.querySelector('img')).toBeNull();
      expect(container.querySelector('svg.drive-file-icon')).not.toBeNull();
      cleanup();
    }
  });
});

describe('AppHeader licence footer', () => {
  const header = (version?: string) => render(<AppHeader appName="KyDrive" activeTab="drive" onTabChange={() => {}}
    user={{ display_name: 'Ada', username: 'ada', role: 'user' }} onLogout={() => {}} workspaces={[]}
    selectedWorkspace="" onWorkspaceSelect={() => {}} version={version} />);

  it('opens the MIT licence with the server version', () => {
    header('1.2.3');
    fireEvent.click(screen.getByRole('button', { name: `© ${new Date().getFullYear()} · MIT License` }));
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByText('KyDrive')).toBeTruthy();
    expect(within(dialog).getByText('v1.2.3')).toBeTruthy();
    expect(within(dialog).getByText('Developed by Busnes.app')).toBeTruthy();
    const text = (within(dialog).getByRole('textbox', { name: 'MIT License' }) as HTMLTextAreaElement).value;
    expect(text).toMatch(/^MIT License\n\nCopyright \(c\) \d{4} Busnes\.app/);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('shows no badge without a version', () => {
    header();
    fireEvent.click(screen.getByRole('button', { name: /MIT License/ }));
    expect(within(screen.getByRole('dialog')).queryByText(/^v\d/)).toBeNull();
  });

  it('closes with the outlined Close button, not an icon', () => {
    header('1.2.3');
    fireEvent.click(screen.getByRole('button', { name: /MIT License/ }));
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Close' }).className).toBe('ky-about-close');
  });

  it('shows the name over the role, never the same word twice, with a labelled Sign out', () => {
    render(<AppHeader appName="KyDrive" activeTab="drive" onTabChange={() => {}}
      user={{ display_name: 'Administrator', username: 'admin', role: 'admin' }} onLogout={() => {}}
      workspaces={[{ id: 'p', name: 'My files', role: 'manager', kind: 'personal', quota: 1024, used: 512, trash_days: 0, keep_versions: 0, editors_share: false }]}
      selectedWorkspace="p" onWorkspaceSelect={() => {}} />);
    expect(document.querySelector('.ky-user-name')?.textContent).toBe('admin');
    expect(document.querySelector('.ky-user-role')?.textContent).toBe('Administrator');
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeTruthy();
    expect(screen.getByRole('navigation', { name: 'Administration' })).toBeTruthy();
    expect(screen.getByRole('meter', { name: 'Storage used' })).toBeTruthy();
    expect(screen.getByText('512 B of 1.0 KiB used')).toBeTruthy();
  });
});

describe('Login', () => {
  it('shows Single Sign On under a divider only when it is configured', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));
    const { container, rerender } = render(<Login appName="KyDrive" onSuccess={() => {}} />);
    expect(screen.getByRole('button', { name: 'Sign in' }).querySelector('svg')).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Continue with Single Sign On' })).toBeNull();
    expect(container.querySelector('.ky-auth-divider')).toBeNull();
    rerender(<Login appName="KyDrive" sso onSuccess={() => {}} />);
    expect(screen.getByRole('link', { name: 'Continue with Single Sign On' }).getAttribute('href')).toBe('/api/sso/kysignon/login');
    expect(container.querySelector('.ky-auth-divider')).toBeTruthy();
    vi.unstubAllGlobals();
  });
});

