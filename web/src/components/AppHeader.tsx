import React, { useEffect, useRef } from 'react';
import { LogOut, Settings as SettingsIcon, Folder, Archive, UserRound, Users, Palette } from 'lucide-react';
import { SHARED_WITH_ME } from '../pages/drive/client';
import type { Workspace } from '../pages/Drive';

interface AppHeaderProps {
  appName: string;
  activeTab: string;
  onTabChange: (tab: string) => void;
  user: { display_name: string; username: string; role: string };
  onLogout: () => void;
  workspaces: Workspace[];
  selectedWorkspace: string;
  onWorkspaceSelect: (id: string) => void;
  openInNewTab: boolean;
  onOpenInNewTabChange: (value: boolean) => void;
}

export const AppHeader: React.FC<AppHeaderProps> = ({ appName, activeTab, onTabChange, user, onLogout, workspaces, selectedWorkspace, onWorkspaceSelect, openInNewTab, onOpenInNewTabChange }) => {
  const navsRef = useRef<HTMLDivElement>(null);
  // Phones scroll both navigations as one row: keep the current destination in view. Only the
  // row scrolls; scrollIntoView would also move the page.
  useEffect(() => {
    const row = navsRef.current, cur = row?.querySelector('[aria-current="page"]');
    if (!row || !cur || row.scrollWidth <= row.clientWidth) return;
    const r = row.getBoundingClientRect(), i = cur.getBoundingClientRect();
    if (i.left < r.left) row.scrollLeft -= r.left - i.left;
    else if (i.right > r.right) row.scrollLeft += i.right - r.right;
  }, [activeTab, selectedWorkspace]);

  const navItems = [
    { id: 'account', label: 'Account', icon: UserRound },
    { id: 'appearance', label: 'Appearance', icon: Palette },
    ...(user.role === 'admin' ? [
      { id: 'backup', label: 'Recovery', icon: Archive },
      { id: 'settings', label: 'Settings & DB', icon: SettingsIcon },
    ] : []),
  ];

  return (
    <>
      <header className="app-header">
        <div className="app-brand">
            <img src="/app-icon.png" width={28} height={28} alt="" />
            <span>{appName || 'Busnes.app'}</span>
          </div>

          <div ref={navsRef} className="app-navs">
          <nav className="app-nav" aria-label="Workspaces">
            <h2 className="app-nav-label">Personal</h2>
            {workspaces.filter(w => w.kind === 'personal').map(workspace => <button key={workspace.id} className={activeTab === 'drive' && selectedWorkspace === workspace.id ? 'ky-nav-item active' : 'ky-nav-item'} aria-current={activeTab === 'drive' && selectedWorkspace === workspace.id ? 'page' : undefined} onClick={() => onWorkspaceSelect(workspace.id)}><Folder size={16} /><span>My files</span></button>)}
            <button className={activeTab === 'drive' && selectedWorkspace === SHARED_WITH_ME ? 'ky-nav-item active' : 'ky-nav-item'} aria-current={activeTab === 'drive' && selectedWorkspace === SHARED_WITH_ME ? 'page' : undefined} onClick={() => onWorkspaceSelect(SHARED_WITH_ME)}><Users size={16} /><span>Shared with me</span></button>
            <h2 className="app-nav-label">Shared workspaces</h2>
            {workspaces.filter(w => w.kind === 'shared').map(workspace => <button key={workspace.id} className={activeTab === 'drive' && selectedWorkspace === workspace.id ? 'ky-nav-item active' : 'ky-nav-item'} aria-current={activeTab === 'drive' && selectedWorkspace === workspace.id ? 'page' : undefined} onClick={() => onWorkspaceSelect(workspace.id)}><Folder size={16} /><span>{workspace.name}</span></button>)}
            {!workspaces.some(w => w.kind === 'shared') && <p className="app-nav-empty">No shared workspaces assigned</p>}
          </nav>
          <nav className="app-nav" aria-label="Primary">
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = activeTab === item.id;
              return (
                <button
                  key={item.id}
                  onClick={() => onTabChange(item.id)}
                  className={active ? 'ky-nav-item active' : 'ky-nav-item'}
                  aria-current={active ? 'page' : undefined}
                >
                  <Icon size={16} />
                  <span>{item.label}</span>
                </button>
              );
            })}
          </nav>
          </div>
        <div className="app-header-actions">
          <label className="app-preference"><input type="checkbox" checked={openInNewTab} onChange={e => onOpenInNewTabChange(e.target.checked)} />Open files in a new tab</label>

          {user && (
            <div className="app-user">
              <div className="app-user-copy">
                <div className="app-user-name">{user.display_name || user.username}</div>
                <div className="app-user-role">{user.role === 'admin' ? 'Administrator' : user.username}</div>
              </div>
              <button
                className="icon-btn app-logout"
                onClick={onLogout}
                title="Sign out"
                aria-label="Sign out"
              >
                <LogOut size={16} aria-hidden="true" />
              </button>
            </div>
          )}
        </div>
      </header>

    </>
  );
};
