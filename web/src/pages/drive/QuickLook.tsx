import { useEffect, useRef, useState } from 'react';

type Kind = 'image' | 'pdf' | 'audio' | 'video' | 'text';
type Shown = { kind: 'loading' | 'none' } | { kind: 'error'; message: string } | { kind: Exclude<Kind, 'text'> } | { kind: 'text'; text: string; cut: boolean };

export const textLimit = 256 * 1024;

// previewKind maps the type the server sniffed to the element that shows it.
export function previewKind(type: string): Kind | null {
  if (type.startsWith('image/')) return 'image';
  if (type === 'application/pdf') return 'pdf';
  if (type.startsWith('audio/') || type === 'application/ogg') return 'audio';
  if (type.startsWith('video/')) return 'video';
  if (type.startsWith('text/plain')) return 'text';
  return null;
}

async function load(url: string): Promise<Shown> {
  const head = await fetch(url, { method: 'HEAD' });
  if (head.status === 415) return { kind: 'none' };
  if (!head.ok) throw new Error(head.status === 403 ? 'You cannot open this file.' : 'Preview unavailable.');
  const kind = previewKind(head.headers.get('Content-Type') ?? '');
  if (!kind) return { kind: 'none' };
  if (kind !== 'text') return { kind };
  const r = await fetch(url, { headers: { Range: `bytes=0-${textLimit - 1}` } });
  if (r.status === 416) return { kind: 'text', text: '', cut: false }; // empty file
  if (!r.ok) throw new Error('Preview unavailable.');
  const total = Number(r.headers.get('Content-Range')?.split('/')[1] ?? 0);
  return { kind: 'text', text: await r.text(), cut: r.status === 206 && total > textLimit };
}

// QuickLook shows a file in a dialog when the server will preview it, and offers Download otherwise.
export function QuickLook({ file, onClose }: { file: { id: string; name: string }; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [shown, setShown] = useState<Shown>({ kind: 'loading' });
  const url = `/api/drive/files/${encodeURIComponent(file.id)}/preview`;
  // jsdom lacks showModal; opening by attribute keeps the dialog reachable in unit tests.
  useEffect(() => { const d = ref.current; if (d && !d.open) { if (typeof d.showModal === 'function') d.showModal(); else d.setAttribute('open', ''); } }, []);
  useEffect(() => {
    let live = true;
    setShown({ kind: 'loading' });
    load(url).then(s => { if (live) setShown(s); }).catch(e => { if (live) setShown({ kind: 'error', message: e instanceof Error ? e.message : 'Preview unavailable.' }); });
    return () => { live = false; };
  }, [url]);
  const rendered = shown.kind !== 'loading' && shown.kind !== 'none' && shown.kind !== 'error';
  return <dialog ref={ref} className="drive-preview-dialog" aria-labelledby="preview-title" onCancel={onClose}>
    <header>
      <h2 id="preview-title">{file.name}</h2>
      <div className="drive-dialog-actions">
        {rendered && <a href={url} target="_blank" rel="noreferrer">Open in new tab</a>}
        <a href={`/api/drive/files/${encodeURIComponent(file.id)}/download`}>Download</a>
        <button type="button" className="btn-secondary" autoFocus onClick={onClose}>Close</button>
      </div>
    </header>
    <div className="drive-preview-body">
      {shown.kind === 'loading' && <p role="status">Loading preview…</p>}
      {shown.kind === 'none' && <p>No preview for this file. Download it to open it.</p>}
      {shown.kind === 'error' && <p role="alert">{shown.message}</p>}
      {shown.kind === 'image' && <img src={url} alt={file.name} />}
      {shown.kind === 'pdf' && <iframe src={url} title={file.name} />}
      {shown.kind === 'audio' && <audio src={url} controls />}
      {shown.kind === 'video' && <video src={url} controls />}
      {shown.kind === 'text' && <><pre>{shown.text}</pre>{shown.cut && <p>Showing the first 256 KiB. Download the file for the rest.</p>}</>}
    </div>
  </dialog>;
}
