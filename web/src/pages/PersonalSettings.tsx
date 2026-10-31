import React from 'react';
import { Appearance } from './Appearance';
import { AppPasswords } from './AppPasswords';
import { Devices } from './Devices';
import { TwoFactor } from './TwoFactor';

export const SETTINGS_SECTIONS = [
  { id: 'account', label: 'Account' },
  { id: 'devices', label: 'Phones & apps' },
  { id: 'appearance', label: 'Appearance' },
  { id: 'files', label: 'Files' },
] as const;

interface PersonalSettingsProps {
  section: string;
  user: { display_name: string; username: string; role: string; totp_enabled?: boolean; sso_provider?: string };
  onTwoFactorChange: (enabled: boolean) => void;
  openInNewTab: boolean;
  onOpenInNewTabChange: (value: boolean) => void;
}

/** Settings: the page head, one tab per section, then the open section. */
export const PersonalSettings: React.FC<PersonalSettingsProps> = ({ section, user, onTwoFactorChange, openInNewTab, onOpenInNewTabChange }) => {
  const open = SETTINGS_SECTIONS.find(s => s.id === section) ?? SETTINGS_SECTIONS[0];
  // Each section opens with its own description, so the tabs stay put when switching.
  // Never the same word twice: a display name that repeats the username or role is left out.
  const role = user.role === 'admin' ? 'Administrator' : 'Everyday account';
  const named = user.display_name && ![user.username.toLowerCase(), role.toLowerCase()].includes(user.display_name.toLowerCase());
  const intro = {
    account: `Signed in as ${named ? `${user.display_name} (${user.username})` : user.username}, ${role === 'Administrator' ? 'an administrator' : 'an everyday account'}. Two-factor authentication guards your sign-in.`,
    devices: 'App passwords for phones, apps and AI agents, and the computers linked to your account.',
    appearance: 'Pick a palette and how it handles light and dark. Match system switches with your device’s setting; Light or Dark keeps one. Your choice applies straight away and is saved in this browser only.',
    files: 'How KyDrive opens your files.',
  }[open.id];
  return (
    <section className="page account-page">
      <header className="ky-page-head"><div><h1 className="ky-page-title">Settings</h1><p className="ky-page-lede">Your account, the phones, apps and computers that use it, and how KyDrive looks and opens files.</p></div></header>
      <nav className="ky-settings-tabs" aria-label="Settings sections">
        {SETTINGS_SECTIONS.map(s => <a key={s.id} className="ky-nav-item" href={`#/settings/${s.id}`} aria-current={s === open ? 'page' : undefined}>{s.label}</a>)}
      </nav>
      {/* Appearance's pair cards take the full width, as before; the other sections keep the settings width. */}
      <section className={open.id === 'appearance' ? undefined : 'stack settings-panel'} aria-label={open.label}>
        <p className="settings-intro">{intro}</p>
        {open.id === 'account' && <TwoFactor enabled={user.totp_enabled === true} local={user.sso_provider === 'local'} onChange={onTwoFactorChange} />}
        {open.id === 'devices' && <><AppPasswords /><Devices /></>}
        {open.id === 'appearance' && <Appearance />}
        {open.id === 'files' && <div className="account-preferences">
          <label className="app-preference"><input type="checkbox" checked={openInNewTab} onChange={e => onOpenInNewTabChange(e.target.checked)} />Open files in a new tab</label>
        </div>}
      </section>
    </section>
  );
};
