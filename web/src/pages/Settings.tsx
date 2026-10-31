import React, { useEffect, useState } from 'react';
import { Database, Trash2 } from 'lucide-react';
import { Alert } from '../components/Alert';
import { change, record, response } from './drive/client';
import { GuestLinkSettings } from './GuestLinkSettings';
import { DevicePolicySettings } from './Devices';

const trashURL = '/api/drive/trash-retention';

function trashDays(v: unknown): number {
  if (!record(v) || typeof v.trash_days !== 'number') throw new Error('Invalid server response');
  return v.trash_days;
}

// TrashRetention is the drive-wide trash purge age; a workspace's own trash setting overrides it.
export const TrashRetention: React.FC = () => {
  const [days, setDays] = useState<number | null>(null);
  const [saved, setSaved] = useState<number | null>(null);
  const [loadError, setLoadError] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    void (async () => {
      try {
        const d = trashDays(await response(await fetch(trashURL)));
        if (live) { setDays(d); setSaved(d); }
      } catch (e) {
        if (live) setLoadError(e instanceof Error ? e.message : 'Could not load the trash setting');
      }
    })();
    return () => { live = false; };
  }, []);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      const d = trashDays(await change(trashURL, 'PUT', { trash_days: days }));
      setDays(d);
      setSaved(d);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save the trash setting');
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="panel" aria-labelledby="trash-settings-heading">
      <div className="panel-header"><div><Trash2 size={20} aria-hidden="true" /><h2 id="trash-settings-heading" className="panel-title" style={{ margin: 0 }}>Trash</h2></div></div>
      <p className="panel-desc">
        Applies to every workspace, including each person&apos;s My files, unless the workspace sets its own trash retention. Items are permanently deleted from the drive; only backups can bring them back. 0 keeps trash until someone empties it.
      </p>
      {loadError ? <Alert>{loadError}</Alert> : days === null ? <p className="hint">Loading…</p> : (
        <form onSubmit={save} className="form-row">
          <label className="field">
            Delete trashed items after (days)
            <input type="number" min={0} max={3650} required value={Number.isNaN(days) ? '' : days} onChange={(e) => setDays(e.target.valueAsNumber)} />
          </label>
          <button type="submit" className="btn-secondary" disabled={busy}>Save</button>
        </form>
      )}
      {saved !== null && (
        <p role="status" className="hint" style={{ marginTop: '.75rem' }}>
          {saved === 0 ? 'Trash is kept until someone empties it.' : `Trashed items are permanently deleted after ${saved} days.`}
        </p>
      )}
      {error && <div style={{ marginTop: '.75rem' }}><Alert>{error}</Alert></div>}
    </section>
  );
};

interface SettingsProps {
  settings: Record<string, unknown> | null;
  onOpenAppearance: () => void;
}

const sections = [['trash', 'Trash'], ['guest', 'Guest links'], ['computers', 'Computers'], ['system', 'System']] as const;

export const Settings: React.FC<SettingsProps> = ({ settings, onOpenAppearance }) => {
  const driver = typeof settings?.db_driver === 'string' ? settings.db_driver : 'sqlite';
  const [tab, setTab] = useState<typeof sections[number][0]>('trash');
  return (
    <section className="page">
      <header className="ky-page-head">
        <div>
          <h1 className="ky-page-title">Settings</h1>
          <p className="ky-page-lede">Drive-wide rules for trash, guest links and linked computers.</p>
        </div>
      </header>
      <div className="tabs" role="tablist" aria-label="Settings sections">
        {sections.map(([id, label]) => <button key={id} id={`settings-tab-${id}`} role="tab" className="tab" aria-selected={tab === id} aria-controls="settings-panel" onClick={() => setTab(id)}>{label}</button>)}
      </div>
      <div id="settings-panel" role="tabpanel" aria-labelledby={`settings-tab-${tab}`} className="settings-panel">
        {tab === 'trash' && <TrashRetention />}
        {tab === 'guest' && <GuestLinkSettings />}
        {tab === 'computers' && <DevicePolicySettings />}
        {tab === 'system' && <section className="panel" aria-labelledby="system-heading">
          <div className="panel-header"><div><Database size={20} aria-hidden="true" /><h2 id="system-heading" className="panel-title" style={{ margin: 0 }}>System</h2></div></div>
          <dl className="settings-facts">
            <dt>Database</dt><dd><span className="badge font-mono">{driver}</span> One SQLite database, one writer</dd>
            <dt>Theme on this browser</dt><dd><button type="button" className="btn-secondary" onClick={onOpenAppearance}>Open Appearance</button></dd>
          </dl>
          <p className="hint" style={{ marginTop: '.75rem' }}>The theme follows this device until someone picks one; each browser keeps its own choice.</p>
        </section>}
      </div>
    </section>
  );
};
