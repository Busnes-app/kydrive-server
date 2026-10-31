import React, { useEffect, useState } from 'react';
import { AppHeader } from './components/AppHeader';
import type { SuiteApp } from './components/AppHeader';
import { Drive } from './pages/Drive';
import type { Workspace } from './pages/Drive';
import { SharedView } from './pages/drive/SharedView';
import { SHARED_WITH_ME, change, editorURL, list, workspace } from './pages/drive/client';
import { openPendingLink, takeLinkFromURL } from './pages/drive/links';
import { Login } from './pages/Login';
import { ChangePassword } from './pages/ChangePassword';
import { Backup } from './pages/Backup';

import { Settings } from './pages/Settings';
import { AdminActivity, AdminLeavers, AdminWorkspaces } from './pages/Admin';
import { PersonalSettings, SETTINGS_SECTIONS } from './pages/PersonalSettings';
import { LinkDevice, hasLinkRequest, takeLinkRequestFromURL } from './pages/LinkDevice';
import './styles/theme.css';
import './ky-ui/tokens.css';
import './ky-ui/navigation.css';
import './ky-ui/appearance.css';
import './ky-ui/appearance-themes.css';
import './ky-ui/shell.css';
import { secureFetch } from './api';
import { Alert } from './components/Alert';

const ADMIN_TABS = ['workspaces', 'activity', 'leavers', 'backup', 'server-settings'];
const SETTINGS_TABS = SETTINGS_SECTIONS.map(s => `settings/${s.id}`);
// Pages that became Settings sections keep their old addresses.
const MOVED: Record<string, string> = { settings: 'settings/account', account: 'settings/account', appearance: 'settings/appearance' };

// Each page is a hash URL (#/drive, #/settings/devices), so links and Back work. Only on `/`:
// shared links (/l#…) and device linking (/link) own theirs.
function hashTab(): string | null {
  if (location.pathname !== '/') return null;
  const id = location.hash.replace(/^#\/?/, '');
  if (MOVED[id]) history.replaceState(null, '', `#/${MOVED[id]}`);
  return MOVED[id] ?? (id || null);
}
const go = (tab: string) => { location.hash = `/${tab}`; };

export const App: React.FC = () => {
  const [user, setUser] = useState<DriveUser | null>(null);
  const [notice, setNotice] = useState('');
  const [loading, setLoading] = useState<boolean>(true);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [selectedWorkspace, setSelectedWorkspace] = useState('');
  const [chosenTab, setChosenTab] = useState<string | null>(hashTab);
  const [settings, setSettings] = useState<Record<string, unknown> | null>(null);
  const [openInNewTab, setOpenInNewTab] = useState(true);
  const [version, setVersion] = useState('');
  const [apps, setApps] = useState<SuiteApp[]>([]);
  // Idempotent, so StrictMode's second call is harmless.
  const [linking, setLinking] = useState(() => { takeLinkRequestFromURL(); return hasLinkRequest(); });

  useEffect(() => {
    const sync = () => setChosenTab(hashTab());
    window.addEventListener('hashchange', sync);
    return () => window.removeEventListener('hashchange', sync);
  }, []);

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
      setSelectedWorkspace(SHARED_WITH_ME); go('drive');
    }, () => setNotice('This link is no longer valid. Ask whoever sent it for a new one.'));
  }, [user]);

  // The app rail: once per signed-in account. Any failure leaves it hidden.
  useEffect(() => {
    if (!user || user.must_change_password) return;
    let live = true;
    void fetch('/api/suite/apps').then(r => r.ok ? r.json() : null).then((body: unknown) => {
      if (live && isRecord(body) && Array.isArray(body.apps)) setApps(body.apps.filter(isSuiteApp));
    }).catch(() => {});
    return () => { live = false; setApps([]); };
  }, [user?.id, user?.must_change_password]);

  // The sidebar's workspaces come from the drive's own load. A page opened by its address
  // (Settings, an admin page) loads them here, so My files and the storage meter still show.
  const listFor = user && !user.must_change_password && !linking && (chosenTab ?? 'drive') !== 'drive' ? user.role : null;
  useEffect(() => {
    if (!listFor || workspaces.length) return;
    let live = true;
    void (async () => {
      await change('/api/drive/personal-workspace', 'POST', {});
      const listed = await list(`/api/drive/workspaces${listFor === 'admin' ? '?admin=true' : ''}`, workspace);
      if (live) setWorkspaces(listed);
    })().catch(() => {});
    return () => { live = false; };
  }, [listFor, workspaces.length]);

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
    history.replaceState(null, '', '/');
    setChosenTab(null);
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

  const admin = user.role === 'admin';
  const tabs = ['drive', ...(admin ? ADMIN_TABS : []), ...SETTINGS_TABS];
  // An unknown address lands on the drive, or Settings' first section.
  const fallback = chosenTab?.startsWith('settings/') ? SETTINGS_TABS[0] : 'drive';
  const activeTab = chosenTab && tabs.includes(chosenTab) ? chosenTab : fallback;
  const openWorkspace = (id: string) => { setSelectedWorkspace(id); go('drive'); };

  return (
    <AppHeader
      appName={typeof settings?.app_name === 'string' ? settings.app_name : 'KyDrive'}
      activeTab={activeTab}
      onTabChange={go}
      user={user}
      workspaces={workspaces}
      selectedWorkspace={selectedWorkspace}
      onWorkspaceSelect={openWorkspace}
      onLogout={handleLogout}
      version={version}
      apps={apps}
    >
      {notice && <div className="app-notice"><Alert>{notice}</Alert></div>}
      {activeTab === 'drive' && (selectedWorkspace === SHARED_WITH_ME ? <SharedView openInNewTab={openInNewTab} /> : <Drive admin={admin} workspaces={workspaces} onWorkspacesChange={setWorkspaces} selected={selectedWorkspace} onSelectWorkspace={setSelectedWorkspace} openInNewTab={openInNewTab} />)}

      {activeTab === 'workspaces' && <AdminWorkspaces workspaces={workspaces} onWorkspacesChange={setWorkspaces} onOpen={openWorkspace} />}
      {activeTab === 'activity' && <AdminActivity />}
      {activeTab === 'leavers' && <AdminLeavers workspaces={workspaces} />}
      {activeTab === 'backup' && <Backup />}
      {activeTab === 'server-settings' && <Settings settings={settings} onOpenAppearance={() => go('settings/appearance')} />}
      {activeTab.startsWith('settings/') && <PersonalSettings section={activeTab.slice('settings/'.length)} user={user}
        onTwoFactorChange={(enabled) => setUser({ ...user, totp_enabled: enabled })}
        openInNewTab={openInNewTab} onOpenInNewTabChange={value => void changeOpenInNewTab(value)} />}
    </AppHeader>
  );
};

interface DriveUser { id: string; username: string; display_name: string; role: string; must_change_password?: boolean; totp_enabled?: boolean; sso_provider?: string }
function isRecord(v: unknown): v is Record<string, unknown> { return typeof v === 'object' && v !== null && !Array.isArray(v); }
function isSuiteApp(v: unknown): v is SuiteApp { return isRecord(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.url === 'string' && v.url.startsWith('https://'); }
function isDriveUser(v: unknown): v is DriveUser { return isRecord(v) && typeof v.id === 'string' && typeof v.username === 'string' && typeof v.display_name === 'string' && typeof v.role === 'string' && (v.must_change_password === undefined || typeof v.must_change_password === 'boolean'); }
