import React, { useEffect, useState } from 'react';
import { AppHeader } from './components/AppHeader';
import { Drive } from './pages/Drive';
import type { Workspace } from './pages/Drive';
import { SharedView } from './pages/drive/SharedView';
import { SHARED_WITH_ME, editorURL } from './pages/drive/client';
import { openPendingLink, takeLinkFromURL } from './pages/drive/links';
import { Login } from './pages/Login';
import { ChangePassword } from './pages/ChangePassword';
import { Backup } from './pages/Backup';

import { Settings } from './pages/Settings';
import { AdminActivity, AdminLeavers, AdminWorkspaces } from './pages/Admin';
import { Appearance } from './pages/Appearance';
import { TwoFactor } from './pages/TwoFactor';
import { AppPasswords } from './pages/AppPasswords';
import { Devices } from './pages/Devices';
import { LinkDevice, hasLinkRequest, takeLinkRequestFromURL } from './pages/LinkDevice';
import './styles/theme.css';
import './ky-ui/tokens.css';
import './ky-ui/navigation.css';
import './ky-ui/appearance.css';
import './ky-ui/appearance-themes.css';
import './ky-ui/shell.css';
import { secureFetch } from './api';
import { Alert } from './components/Alert';

export const App: React.FC = () => {
  const [user, setUser] = useState<DriveUser | null>(null);
  const [notice, setNotice] = useState('');
  const [loading, setLoading] = useState<boolean>(true);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [selectedWorkspace, setSelectedWorkspace] = useState('');
  const [activeTab, setActiveTab] = useState<string>('drive');
  const [settings, setSettings] = useState<Record<string, unknown> | null>(null);
  const [openInNewTab, setOpenInNewTab] = useState(true);
  const [version, setVersion] = useState('');
  // Idempotent, so StrictMode's second call is harmless.
  const [linking, setLinking] = useState(() => { takeLinkRequestFromURL(); return hasLinkRequest(); });

  useEffect(() => {
    takeLinkFromURL();
    const checkAuth = async () => {
      try {
        const authResp = await fetch('/api/auth/me');
        const setResp = await fetch('/api/settings');

        if (setResp.ok) {
          const s: unknown = await setResp.json();
          if (isRecord(s)) setSettings(s);
        }

        if (authResp.ok) {
          const a: unknown = await authResp.json();
          if (isRecord(a) && a.authenticated && isDriveUser(a.user)) {
            setUser(a.user);
            if (typeof a.open_in_new_tab === 'boolean') setOpenInNewTab(a.open_in_new_tab);
            if (typeof a.version === 'string') setVersion(a.version);
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

  // A link opened before signing in waits until there is a user.
  useEffect(() => {
    if (!user || user.must_change_password) return;
    void openPendingLink().then(item => {
      if (!item) return;
      if (item.kind === 'file') { location.assign(editorURL(item)); return; }
      setSelectedWorkspace(SHARED_WITH_ME); setActiveTab('drive');
    }, () => setNotice('This link is no longer valid. Ask whoever sent it for a new one.'));
  }, [user]);

  // /api/settings returns more fields once authenticated, so re-read it after login.
  const loadSettings = async () => {
    const resp = await fetch('/api/settings');
    if (resp.ok) {
      const s: unknown = await resp.json();
      if (isRecord(s)) setSettings(s);
    }
  };

  const loadPreferences = async () => {
    const resp = await fetch('/api/auth/me');
    if (!resp.ok) return;
    const a: unknown = await resp.json();
    if (!isRecord(a)) return;
    if (typeof a.open_in_new_tab === 'boolean') setOpenInNewTab(a.open_in_new_tab);
    if (typeof a.version === 'string') setVersion(a.version);
  };

  const changeOpenInNewTab = async (value: boolean) => {
    setOpenInNewTab(value);
    const resp = await secureFetch('/api/auth/preferences', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ open_in_new_tab: value }) }).catch(() => null);
    if (!resp?.ok) setOpenInNewTab(!value);
  };

  const handleLogout = async () => {
    const resp = await secureFetch('/api/auth/logout', { method: 'POST' }).catch(() => null);
    if (!resp?.ok) {
      setNotice('Sign out failed. Try again.');
      return;
    }
    // Drop everything the previous account loaded; settings may hold admin-only fields.
    setNotice('');
    setUser(null);
    setWorkspaces([]);
    setSelectedWorkspace('');
    setActiveTab('drive');
    setSettings(null);
    setOpenInNewTab(true);
    setVersion('');
    void loadSettings().catch(() => {});
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
      {notice && <div className="app-notice" style={{ maxWidth: '24rem', margin: '1.5rem auto 0' }}><Alert tone="info">{notice}</Alert></div>}
      <Login
        appName={typeof settings?.app_name === 'string' ? settings.app_name : 'KyDrive'}
        sso={settings?.sso_enabled === true}
        onSuccess={(u) => {
          setNotice('');
          if (isDriveUser(u)) setUser(u);
          void loadSettings();
          void loadPreferences();
        }}
      />
      </>
    );
  }

  if (user.must_change_password) {
    return <>{notice && <div className="app-notice"><Alert>{notice}</Alert></div>}<ChangePassword onLogout={handleLogout} onComplete={() => {
      setUser(null);
      setNotice('Password changed. Sign in with your new password.');
    }} /></>;
  }

  if (linking) return <LinkDevice onClose={() => setLinking(false)} />;

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
        version={version}
      />

      <main className="app-main">
        {notice && <div className="app-notice"><Alert>{notice}</Alert></div>}
        {activeTab === 'drive' && (selectedWorkspace === SHARED_WITH_ME ? <SharedView openInNewTab={openInNewTab} /> : <Drive admin={user.role === 'admin'} workspaces={workspaces} onWorkspacesChange={setWorkspaces} selected={selectedWorkspace} onSelectWorkspace={setSelectedWorkspace} openInNewTab={openInNewTab} />)}
        
        {activeTab === 'workspaces' && user.role === 'admin' && <AdminWorkspaces workspaces={workspaces} onWorkspacesChange={setWorkspaces} onOpen={id => { setSelectedWorkspace(id); setActiveTab('drive'); }} />}
        {activeTab === 'activity' && user.role === 'admin' && <AdminActivity />}
        {activeTab === 'leavers' && user.role === 'admin' && <AdminLeavers workspaces={workspaces} />}
        {activeTab === 'backup' && <Backup />}
        {activeTab === 'settings' && <Settings settings={settings} onOpenAppearance={() => setActiveTab('appearance')} />}
        {activeTab === 'appearance' && <Appearance />}
        {activeTab === 'account' && <section className="page page-narrow account-page">
          <header className="ky-page-head"><div><h1 className="ky-page-title">Account</h1><p className="ky-page-lede">Signed in as {user.display_name || user.username} ({user.username}). Sign-in security, app passwords, AI agents and linked computers.</p></div></header>
          <div className="stack">
            <section className="account-preferences" aria-labelledby="file-preferences-title">
              <h2 id="file-preferences-title">Files</h2>
              <label className="app-preference"><input type="checkbox" checked={openInNewTab} onChange={e => void changeOpenInNewTab(e.target.checked)} />Open files in a new tab</label>
            </section>
            <TwoFactor enabled={user.totp_enabled === true} local={user.sso_provider === 'local'} onChange={(enabled) => setUser({ ...user, totp_enabled: enabled })} />
            <AppPasswords />
            <Devices />
          </div>
        </section>}
      </main>
    </div>
  );
};

interface DriveUser { id: string; username: string; display_name: string; role: string; must_change_password?: boolean; totp_enabled?: boolean; sso_provider?: string }
function isRecord(v: unknown): v is Record<string, unknown> { return typeof v === 'object' && v !== null && !Array.isArray(v); }
function isDriveUser(v: unknown): v is DriveUser { return isRecord(v) && typeof v.id === 'string' && typeof v.username === 'string' && typeof v.display_name === 'string' && typeof v.role === 'string' && (v.must_change_password === undefined || typeof v.must_change_password === 'boolean'); }
