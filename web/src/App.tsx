import React, { useEffect, useState } from 'react';
import { AppHeader } from './components/AppHeader';
import { Drive } from './pages/Drive';
import type { Workspace } from './pages/Drive';
import { Login } from './pages/Login';
import { ChangePassword } from './pages/ChangePassword';
import { Backup } from './pages/Backup';

import { Settings } from './pages/Settings';
import './styles/theme.css';
import './ky-ui/tokens.css';
import './ky-ui/navigation.css';
import { secureFetch } from './api';

export const App: React.FC = () => {
  const [user, setUser] = useState<DriveUser | null>(null);
  const [notice, setNotice] = useState('');
  const [loading, setLoading] = useState<boolean>(true);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [selectedWorkspace, setSelectedWorkspace] = useState('');
  const [activeTab, setActiveTab] = useState<string>('drive');
  const [settings, setSettings] = useState<Record<string, unknown> | null>(null);

  useEffect(() => {
    const checkAuth = async () => {
      try {
        const [authResp, setResp] = useResponses(
          await fetch('/api/auth/me'),
          await fetch('/api/settings')
        );

        if (setResp.ok) {
          const s: unknown = await setResp.json();
          if (isRecord(s)) setSettings(s);
        }

        if (authResp.ok) {
          const a: unknown = await authResp.json();
          if (isRecord(a) && a.authenticated && isDriveUser(a.user)) {
            setUser(a.user);
          }
        }
      } catch (err) {
        console.error('Initialization error:', err);
      } finally {
        setLoading(false);
      }
    };

    checkAuth();
  }, []);

  // /api/settings returns more fields once authenticated, so re-read it after login.
  const loadSettings = async () => {
    const resp = await fetch('/api/settings');
    if (resp.ok) {
      const s: unknown = await resp.json();
      if (isRecord(s)) setSettings(s);
    }
  };

  const handleLogout = async () => {
    await secureFetch('/api/auth/logout', { method: 'POST' });
    setUser(null);
    setWorkspaces([]);
    setSelectedWorkspace('');
  };

  if (loading) {
    return (
      <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--bg)', color: 'var(--ink)' }}>
        Loading {typeof settings?.app_name === 'string' ? settings.app_name : 'KyDrive'}...
      </div>
    );
  }

  if (!user) {
    return (
      <>
      {notice && <p role="status" style={{ padding: 16 }}>{notice}</p>}
      <Login
        appName={typeof settings?.app_name === 'string' ? settings.app_name : 'KyDrive'}
        onSuccess={(u) => {
          setNotice('');
          if (isDriveUser(u)) setUser(u);
          void loadSettings();
        }}
      />
      </>
    );
  }

  if (user.must_change_password) {
    return <ChangePassword onLogout={handleLogout} onComplete={() => {
      setUser(null);
      setNotice('Password changed. Sign in with your new password.');
    }} />;
  }

  return (
    <div className="app-shell">
      <AppHeader
        appName={typeof settings?.app_name === 'string' ? settings.app_name : 'KyDrive'}
        activeTab={activeTab}
        onTabChange={(tab) => setActiveTab(tab)}
        user={user}
        workspaces={workspaces}
        selectedWorkspace={selectedWorkspace}
        onWorkspaceSelect={id => { setSelectedWorkspace(id); setActiveTab('drive'); }}
        onLogout={handleLogout}
      />

      <main className="app-main">
        {activeTab === 'drive' && <Drive admin={user.role === 'admin'} workspaces={workspaces} onWorkspacesChange={setWorkspaces} selected={selectedWorkspace} onSelectWorkspace={setSelectedWorkspace} />}
        
        {activeTab === 'backup' && <Backup />}
        {activeTab === 'settings' && <Settings settings={settings} />}
      </main>
    </div>
  );
};

function useResponses(r1: Response, r2: Response): [Response, Response] {
  return [r1, r2];
}

interface DriveUser { id: string; username: string; display_name: string; role: string; must_change_password?: boolean }
function isRecord(v: unknown): v is Record<string, unknown> { return typeof v === 'object' && v !== null && !Array.isArray(v); }
function isDriveUser(v: unknown): v is DriveUser { return isRecord(v) && typeof v.id === 'string' && typeof v.username === 'string' && typeof v.display_name === 'string' && typeof v.role === 'string' && (v.must_change_password === undefined || typeof v.must_change_password === 'boolean'); }
