import React, { useEffect, useRef, useState } from 'react';
import { Activity, Archive, Folder, FolderCog, LogOut, Palette, Settings as SettingsIcon, UserMinus, UserRound, Users } from 'lucide-react';
import { SHARED_WITH_ME, bytes } from '../pages/drive/client';
import type { Workspace } from '../pages/Drive';
import mitLicenseText from '../mit.txt?raw';

function revealNavItem(row: HTMLElement, item: Element) {
  if (row.scrollWidth <= row.clientWidth) return;
  const r = row.getBoundingClientRect(), i = item.getBoundingClientRect();
  // Include the shared 2px focus outline and its 2px offset without scrolling the page.
  if (i.left < r.left + 4) row.scrollLeft -= r.left + 4 - i.left;
  else if (i.right > r.right - 4) row.scrollLeft += i.right - r.right + 4;
}

interface AppHeaderProps {
  appName: string;
  activeTab: string;
  onTabChange: (tab: string) => void;
  user: { display_name: string; username: string; role: string };
  onLogout: () => void;
  workspaces: Workspace[];
  selectedWorkspace: string;
  onWorkspaceSelect: (id: string) => void;
  version?: string;
}

type NavItem = { id: string; label: string; icon: typeof Folder };
const accountItems: NavItem[] = [
  { id: 'account', label: 'Account', icon: UserRound },
  { id: 'appearance', label: 'Appearance', icon: Palette },
];
const adminItems: NavItem[] = [
  { id: 'workspaces', label: 'Workspaces', icon: FolderCog },
  { id: 'activity', label: 'Activity', icon: Activity },
  { id: 'leavers', label: 'Leavers', icon: UserMinus },
  { id: 'backup', label: 'Backup & recovery', icon: Archive },
  { id: 'settings', label: 'Settings', icon: SettingsIcon },
];

// A light track with an accent fill sized to the share used, so near-empty storage reads as empty.
export function StorageBar({ used, quota }: { used: number; quota: number }) {
  const percent = quota > 0 ? Math.min(100, Math.max(0, used / quota * 100)) : 0;
  return <div className="storage-bar" role="meter" aria-label="Storage used" aria-valuemin={0} aria-valuemax={quota} aria-valuenow={Math.min(used, quota)} aria-valuetext={`${bytes(used)} of ${bytes(quota)} used`}>
    <span className="storage-bar-fill" style={{ width: `${percent}%` }} />
  </div>;
}

export const AppHeader: React.FC<AppHeaderProps> = ({ appName, activeTab, onTabChange, user, onLogout, workspaces, selectedWorkspace, onWorkspaceSelect, version }) => {
  const navsRef = useRef<HTMLDivElement>(null);
  const aboutRef = useRef<HTMLDialogElement>(null);
  const [aboutOpen, setAboutOpen] = useState(false);
  const year = new Date().getFullYear();
  const name = appName || 'KyDrive';
  const role = user.role === 'admin' ? 'Administrator' : 'Everyday account';
  // Never the same word twice: a display name that repeats the role shows the username instead.
  const shownName = user.display_name && user.display_name.toLowerCase() !== role.toLowerCase() ? user.display_name : user.username;
  const personal = workspaces.find(w => w.kind === 'personal');
  // Phones scroll the navigation as one row: keep the current destination in view. Only the
  // row scrolls; scrollIntoView would also move the page.
  useEffect(() => {
    const row = navsRef.current, cur = row?.querySelector('[aria-current="page"]');
    if (row && cur) revealNavItem(row, cur);
  }, [activeTab, selectedWorkspace]);
  useEffect(() => {
    const d = aboutRef.current;
    // jsdom lacks showModal; opening by attribute keeps the dialog reachable in unit tests.
    if (aboutOpen && d && !d.open) { if (typeof d.showModal === 'function') d.showModal(); else d.setAttribute('open', ''); }
  }, [aboutOpen]);

  // Closing the native dialog first returns focus to the licence button.
  const closeAbout = () => { const d = aboutRef.current; if (d?.open && typeof d.close === 'function') d.close(); setAboutOpen(false); };
  const item = (id: string, label: string, Icon: typeof Folder, current: boolean, onClick: () => void) =>
    <button key={id} className={current ? 'ky-nav-item active' : 'ky-nav-item'} aria-current={current ? 'page' : undefined} onClick={onClick}>
      <Icon size={16} aria-hidden="true" /><span>{label}</span>
    </button>;
  const drive = (id: string) => activeTab === 'drive' && selectedWorkspace === id;
  const tabs = (items: NavItem[]) => items.map(({ id, label, icon }) => item(id, label, icon, activeTab === id, () => onTabChange(id)));

  return (
    <>
      <header className="app-header">
        <div className="app-brand">
          <img className="brand-mark" src="/app-icon.png" width={28} height={28} alt="" />
          <span>{name}</span>
        </div>

        <div ref={navsRef} className="app-navs" onFocusCapture={e => {
          if (e.target instanceof HTMLButtonElement) revealNavItem(e.currentTarget, e.target);
        }}>
          <nav className="app-nav" aria-label="Workspaces">
            <h2 className="app-nav-label">Personal</h2>
            {workspaces.filter(w => w.kind === 'personal').map(w => item(w.id, 'My files', Folder, drive(w.id), () => onWorkspaceSelect(w.id)))}
            {item(SHARED_WITH_ME, 'Shared with me', Users, drive(SHARED_WITH_ME), () => onWorkspaceSelect(SHARED_WITH_ME))}
            {workspaces.some(w => w.kind === 'shared') && <h2 className="app-nav-label">Shared workspaces</h2>}
            {workspaces.filter(w => w.kind === 'shared').map(w => item(w.id, w.name, Folder, drive(w.id), () => onWorkspaceSelect(w.id)))}
          </nav>
          <nav className="app-nav" aria-label="Primary"><h2 className="app-nav-label">Preferences</h2>{tabs(accountItems)}</nav>
          {user.role === 'admin' && <nav className="app-nav" aria-label="Administration">
            <h2 className="app-nav-label">Administration</h2>
            {tabs(adminItems)}
          </nav>}
        </div>
        <p className="app-nav-hint">Scroll for more destinations</p>
        <div className="app-header-actions">
          {personal && personal.quota > 0 && <div className="sidebar-storage">
            <StorageBar used={personal.used} quota={personal.quota} />
            <p>{bytes(personal.used)} of {bytes(personal.quota)} used</p>
          </div>}
          <div className="ky-user">
            <div className="ky-user-copy">
              <p className="ky-user-name">{shownName}</p>
              <p className="ky-user-role">{role}</p>
            </div>
            <button type="button" className="ky-user-signout" onClick={onLogout}>
              <LogOut aria-hidden="true" />
              <span>Sign out</span>
            </button>
          </div>
          <footer>
            <button type="button" className="ky-license-link" onClick={() => setAboutOpen(true)}>
              &copy; {year} &middot; MIT License
            </button>
          </footer>
        </div>
      </header>
      {aboutOpen && <dialog ref={aboutRef} className="ky-about" aria-label={`About ${name}`} {...{ closedby: 'any' }} onClose={() => setAboutOpen(false)}>
        <div className="ky-about-head">
          <div>
            <h2 className="ky-about-name">{name}{version && <span className="ky-about-version">v{version}</span>}</h2>
            <p className="ky-about-meta">Developed by Busnes.app</p>
            <p className="ky-about-meta">&copy; {year} &middot; Licensed under the MIT License</p>
          </div>
          <button type="button" className="ky-about-close" onClick={closeAbout}>Close</button>
        </div>
        <textarea className="ky-about-license" readOnly aria-label="MIT License" value={mitLicenseText} />
      </dialog>}
    </>
  );
};
