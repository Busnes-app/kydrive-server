import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { SettingsMenu } from './SettingsMenu';
import { AppHeader } from './AppHeader';

afterEach(cleanup);

const gear = () => screen.getByRole('button', { name: 'Settings' });
const menu = () => document.getElementById('m') as HTMLElement;
const items = () => within(menu()).getAllByRole('menuitem', { hidden: true });

describe('SettingsMenu', () => {
  it('lists every section, as links to its tab, for administrators and everyday users', () => {
    for (const role of ['admin', 'user']) {
      render(<AppHeader appName="KyDrive" activeTab="drive" onTabChange={() => {}} user={{ display_name: '', username: 'u', role }}
        onLogout={() => {}} workspaces={[]} selectedWorkspace="" onWorkspaceSelect={() => {}} />);
      for (const id of ['ky-settings-menu', 'ky-settings-menu-phone']) {
        const links = within(document.getElementById(id)!).getAllByRole('menuitem', { hidden: true });
        expect(links.map(a => [a.textContent, a.getAttribute('href')])).toEqual([
          ['Account', '#/settings/account'], ['Phones & apps', '#/settings/devices'], ['Appearance', '#/settings/appearance'], ['Files', '#/settings/files'],
        ]);
      }
      cleanup();
    }
  });

  it('marks the open section and the gear current only while Settings is open', () => {
    const { rerender } = render(<SettingsMenu id="m" />);
    expect(gear().getAttribute('aria-current')).toBeNull();
    expect(menu().querySelector('[aria-current]')).toBeNull();
    rerender(<SettingsMenu id="m" section="files" />);
    expect(gear().getAttribute('aria-current')).toBe('page');
    expect(Array.from(menu().querySelectorAll('[aria-current="page"]')).map(a => a.textContent)).toEqual(['Files']);
  });

  it('opens on a click with focus on the first item, and Escape returns focus to the gear', () => {
    render(<SettingsMenu id="m" />);
    expect(gear().getAttribute('aria-controls')).toBe('m');
    expect(gear().getAttribute('aria-expanded')).toBe('false');
    expect(menu().hidden).toBe(true);
    fireEvent.click(gear());
    expect(gear().getAttribute('aria-expanded')).toBe('true');
    expect(menu().hidden).toBe(false);
    expect(document.activeElement).toBe(items()[0]);
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(menu().hidden).toBe(true);
    expect(document.activeElement).toBe(gear());
  });

  it('moves with the arrows, Home and End, and wraps', () => {
    render(<SettingsMenu id="m" />);
    fireEvent.keyDown(gear(), { key: 'ArrowUp' });
    const list = items();
    expect(document.activeElement).toBe(list[3]);
    fireEvent.keyDown(list[3], { key: 'ArrowDown' });
    expect(document.activeElement).toBe(list[0]);
    fireEvent.keyDown(list[0], { key: 'ArrowUp' });
    expect(document.activeElement).toBe(list[3]);
    fireEvent.keyDown(list[3], { key: 'Home' });
    expect(document.activeElement).toBe(list[0]);
    fireEvent.keyDown(list[0], { key: 'ArrowDown' });
    expect(document.activeElement).toBe(list[1]);
    fireEvent.keyDown(list[1], { key: 'End' });
    expect(document.activeElement).toBe(list[3]);
  });

  it('closes on Tab, on a click outside and when an item is chosen', () => {
    render(<><SettingsMenu id="m" /><p>outside</p></>);
    fireEvent.click(gear());
    fireEvent.keyDown(document.activeElement!, { key: 'Tab' });
    expect(menu().hidden).toBe(true);
    fireEvent.click(gear());
    fireEvent.pointerDown(screen.getByText('outside'));
    expect(menu().hidden).toBe(true);
    fireEvent.click(gear());
    fireEvent.click(within(menu()).getByRole('menuitem', { name: 'Files' }));
    expect(menu().hidden).toBe(true);
    expect(document.activeElement).toBe(gear());
  });
});
