import { Dialog } from '../../components/Dialog';
import { When } from '../../components/When';
import { bytes } from './client';
import type { DriveFile, Version } from './client';

// Versions lists a file's versions, newest first, with when each was saved in local time. Restore
// makes an old version the newest one; earlier versions stay listed.
export function Versions({ file, versions, busy, canRestore, canDelete, onRestore, onDelete, onClose }: {
  file: DriveFile; versions: Version[]; busy: boolean; canRestore: boolean; canDelete: boolean;
  onRestore: (v: Version) => void; onDelete: (v: Version) => void; onClose: () => void;
}) {
  const sorted = [...versions].sort((a, b) => b.revision - a.revision);
  return <Dialog wide title={`Version history`} description={file.name} onClose={onClose}
    footer={<button type="button" className="btn-secondary" onClick={onClose}>Close</button>}>
    {canRestore && <p>Restoring a version saves it as a new version. Nothing is lost.</p>}
    <table className="drive-versions">
      <thead><tr><th>Version</th><th>Saved</th><th className="num">Size</th><th><span className="sr-only">Actions</span></th></tr></thead>
      <tbody>{sorted.map(v => {
        const current = v.revision === file.revision;
        return <tr key={v.revision}>
          <td>Version {v.revision}{current && <> <span className="badge badge-accent">Current</span></>}</td>
          <td><When value={v.created} /></td>
          <td className="num">{bytes(v.size)}</td>
          <td><div className="button-row end" style={{ marginTop: 0 }}>
            {canRestore && !current && <button className="btn-secondary btn-sm" disabled={busy} onClick={() => onRestore(v)}>Restore</button>}
            {canDelete && !current && <button className="btn-danger-quiet btn-sm" disabled={busy} onClick={() => onDelete(v)}>Delete</button>}
          </div></td>
        </tr>;
      })}</tbody>
    </table>
  </Dialog>;
}
