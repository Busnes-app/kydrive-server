import React, { useEffect, useState } from 'react';
import { Settings as SettingsIcon, Database, Palette, Trash2 } from 'lucide-react';
import { ThemeSwitcher } from '../components/ThemeSwitcher';
import { change, record, response } from './drive/client';
import { GuestLinkSettings } from './GuestLinkSettings';

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
    <div className="panel">
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '12px' }}>
        <Trash2 size={20} style={{ color: 'var(--accent)' }} />
        <h3 style={{ fontSize: '16px' }}>Trash</h3>
      </div>
      <p style={{ color: 'var(--ink)', fontSize: '13px', marginBottom: '16px' }}>
        Applies to every workspace, including each person&apos;s My files, unless the workspace sets its own trash retention. Items are permanently deleted from the drive; only backups can bring them back. 0 keeps trash until someone empties it.
      </p>
      {loadError ? <p>{loadError}</p> : days === null ? <p>Loading…</p> : (
        <form onSubmit={save} style={{ display: 'flex', alignItems: 'flex-end', gap: '12px', flexWrap: 'wrap' }}>
          <label style={{ display: 'grid', gap: '4px', fontSize: '13px' }}>
            Delete trashed items after (days)
            <input type="number" min={0} max={3650} required value={Number.isNaN(days) ? '' : days} onChange={(e) => setDays(e.target.valueAsNumber)} />
          </label>
          <button type="submit" disabled={busy}>Save</button>
        </form>
      )}
      {saved !== null && (
        <p role="status" style={{ fontSize: '13px', marginTop: '12px' }}>
          {saved === 0 ? 'Trash is kept until someone empties it.' : `Trashed items are permanently deleted after ${saved} days.`}
        </p>
      )}
      {error && <p role="alert" style={{ fontSize: '13px', marginTop: '12px', color: 'var(--danger)' }}>{error}</p>}
    </div>
  );
};

interface SettingsProps {
  settings: any;
}

export const Settings: React.FC<SettingsProps> = ({ settings }) => {
  return (
    <div style={{ maxWidth: '1080px', margin: '0 auto', padding: '32px 20px' }}>
      <div style={{ marginBottom: '24px' }}>
        <h1 style={{ fontSize: '24px', fontWeight: 'bold', display: 'flex', alignItems: 'center', gap: '10px' }}>
          <SettingsIcon size={24} style={{ color: 'var(--accent)' }} />
          <span>System Settings & Architecture</span>
        </h1>
        <p style={{ color: 'var(--ink)', fontSize: '14px', marginTop: '4px' }}>
          Pluggable database drivers, color theme defaults, and instance configuration.
        </p>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 480px), 1fr))', gap: '20px' }}>
        {/* Pluggable Database Card */}
        <div className="panel">
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '12px' }}>
            <Database size={20} style={{ color: 'var(--accent)' }} />
            <h3 style={{ fontSize: '16px' }}>Pluggable Database Layer</h3>
          </div>
          <p style={{ color: 'var(--ink)', fontSize: '13px', marginBottom: '16px' }}>
            Supports zero-CGO SQLite (default) and PostgreSQL clustering on demand.
          </p>

          <div style={{ background: 'var(--bg)', border: '1px solid var(--line)', borderRadius: '6px', padding: '16px' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '8px', fontSize: '13px' }}>
              <span style={{ color: 'var(--ink)' }}>Active Storage Driver:</span>
              <span className="badge badge-accent font-mono">{settings?.db_driver || 'sqlite'}</span>
            </div>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '13px' }}>
              <span style={{ color: 'var(--ink)' }}>Swapping Capability:</span>
              <span style={{ color: 'var(--success)' }}>Supported (PostgreSQL / SQLite)</span>
            </div>
          </div>
        </div>

        {/* KySecurity Theme Card */}
        <div className="panel">
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '12px' }}>
            <Palette size={20} style={{ color: 'var(--accent)' }} />
            <h3 style={{ fontSize: '16px' }}>KySecurity Design System</h3>
          </div>
          <p style={{ color: 'var(--ink)', fontSize: '13px', marginBottom: '16px' }}>
            Color tokens: Patina Ky (`#0d0f14`/`#4deeea`), Cyber, Nord, Paper, OLED with Space Grotesk and IBM Plex Mono fonts.
          </p>

          <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
            <span style={{ fontSize: '13px', color: 'var(--ink)' }}>Active Color Palette:</span>
            <ThemeSwitcher />
          </div>
        </div>

        <TrashRetention />
        <GuestLinkSettings />
      </div>
    </div>
  );
};
