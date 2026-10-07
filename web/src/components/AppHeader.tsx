import React from 'react';
import { LogOut, Settings as SettingsIcon, Folder, Archive } from 'lucide-react';
import type { Workspace } from '../pages/Drive';
import { ThemeSwitcher } from './ThemeSwitcher';

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

  const navItems = [
    { id: 'backup', label: 'Recovery', icon: Archive },
    { id: 'settings', label: 'Settings & DB', icon: SettingsIcon },
  ].filter(() => user.role === 'admin');

  return (
    <>
      <header className="app-header">
        <div className="app-brand">
            <img src="/app-icon.png" width={28} height={28} alt="" />
            <span>{appName || 'Busnes.app'}</span>
          </div>

          <nav className="app-nav" aria-label="Workspaces">
            <h2 className="app-nav-label">Personal</h2>
            {workspaces.filter(w => w.kind === 'personal').map(workspace => <button key={workspace.id} className={activeTab === 'drive' && selectedWorkspace === workspace.id ? 'ky-nav-item active' : 'ky-nav-item'} aria-current={activeTab === 'drive' && selectedWorkspace === workspace.id ? 'page' : undefined} onClick={() => onWorkspaceSelect(workspace.id)}><Folder size={16} /><span>My files</span></button>)}
            <h2 className="app-nav-label">Shared workspaces</h2>
            {workspaces.filter(w => w.kind === 'shared').map(workspace => <button key={workspace.id} className={activeTab === 'drive' && selectedWorkspace === workspace.id ? 'ky-nav-item active' : 'ky-nav-item'} aria-current={activeTab === 'drive' && selectedWorkspace === workspace.id ? 'page' : undefined} onClick={() => onWorkspaceSelect(workspace.id)}><Folder size={16} /><span>{workspace.name}</span></button>)}
            {!workspaces.some(w => w.kind === 'shared') && <p className="app-nav-empty">No shared workspaces assigned</p>}
          </nav>
          {navItems.length > 0 && <nav className="app-nav" aria-label="Primary">
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
          </nav>}
        <div className="app-header-actions">
          <ThemeSwitcher />
          <label className="app-preference"><input type="checkbox" checked={openInNewTab} onChange={e => onOpenInNewTabChange(e.target.checked)} />Open files in a new tab</label>

          {user && (
            <div className="app-user">
              <div className="app-user-copy">
                <div style={{ fontWeight: 600, color: 'var(--ink-strong)' }}>{user.display_name || user.username}</div>
                <div style={{ fontSize: '11px', color: 'var(--ink)' }}>{user.role}</div>
              </div>
              <button
                className="btn-secondary app-logout"
                onClick={onLogout}
                title="Sign out"
                aria-label="Sign out"
              >
                <LogOut size={16} />
              </button>
            </div>
          )}
        </div>
      </header>

    </>
  );
};
