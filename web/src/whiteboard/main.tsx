import '../theme';
import '@excalidraw/excalidraw/index.css';
import React, { useEffect, useRef, useState } from 'react';
import ReactDOM from 'react-dom/client';
import { Excalidraw, MainMenu, loadFromBlob, serializeAsJSON } from '@excalidraw/excalidraw';
import type { ExcalidrawInitialDataState } from '@excalidraw/excalidraw/types';
import { secureFetch } from '../api';
import './whiteboard.css';

type Meta = { id: string; workspace: string; parent: string; name: string; revision: number; editable: boolean };
type Scene = Parameters<NonNullable<React.ComponentProps<typeof Excalidraw>['onChange']>>;

function meta(v: unknown): v is Meta {
  if (typeof v !== 'object' || v === null) return false;
  const m = v as Record<string, unknown>;
  return typeof m.id === 'string' && typeof m.workspace === 'string' && typeof m.parent === 'string' && typeof m.name === 'string' && typeof m.revision === 'number' && typeof m.editable === 'boolean';
}

async function errorText(r: Response): Promise<string> {
  try { const v: unknown = await r.json(); if (typeof v === 'object' && v !== null && typeof (v as { error?: unknown }).error === 'string') return (v as { error: string }).error; } catch { /* Non-JSON error body. */ }
  return `HTTP ${r.status}`;
}

async function open(id: string): Promise<{ file: Meta; scene: ExcalidrawInitialDataState }> {
  const info = await fetch(`/api/drive/files/${encodeURIComponent(id)}`);
  if (!info.ok) throw new Error(await errorText(info));
  const file: unknown = await info.json();
  if (!meta(file)) throw new Error('Invalid server response');
  const download = await fetch(`/api/drive/files/${encodeURIComponent(id)}/download`);
  if (!download.ok) throw new Error(await errorText(download));
  // Excalidraw's own loader validates and migrates the scene, so old or foreign files open as upstream would.
  const { elements, appState, files } = await loadFromBlob(await download.blob(), null, null);
  const theme = document.documentElement.style.colorScheme === 'dark' ? 'dark' : 'light';
  return { file, scene: { elements, appState: { ...appState, theme }, files } };
}

function Board({ file, scene }: { file: Meta; scene: ExcalidrawInitialDataState }) {
  const [status, setStatus] = useState(file.editable ? 'Saved' : 'Read only');
  const revision = useRef(file.revision);
  const latest = useRef<Scene | null>(null);
  const saved = useRef<string | null>(null);
  const timer = useRef(0);
  const saving = useRef(false);
  const stopped = useRef(!file.editable);

  const save = async () => {
    timer.current = 0;
    if (saving.current || stopped.current || !latest.current) return;
    const [elements, appState, files] = latest.current;
    const body = serializeAsJSON(elements, appState, files, 'local');
    if (body === saved.current) { setStatus('Saved'); return; }
    saving.current = true;
    setStatus('Saving…');
    try {
      const q = new URLSearchParams({ name: file.name, parent: file.parent, revision: String(revision.current), file: file.id });
      const r = await secureFetch(`/api/drive/workspaces/${encodeURIComponent(file.workspace)}/uploads?${q}`, { method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body });
      if (!r.ok) {
        // A rejected write (conflict, quota, lost access) must not be retried over someone else's revision.
        stopped.current = true;
        setStatus(`Not saved: ${await errorText(r)}`);
        return;
      }
      const out: unknown = await r.json();
      revision.current = (out as { revision: number }).revision;
      saved.current = body;
      setStatus('Saved');
    } catch {
      setStatus('Offline: not saved');
    } finally {
      saving.current = false;
    }
    if (!stopped.current) schedule();
  };

  const schedule = () => {
    clearTimeout(timer.current);
    timer.current = window.setTimeout(() => void save(), 1000);
  };

  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      if (!latest.current || !file.editable) return;
      const [elements, appState, files] = latest.current;
      if (serializeAsJSON(elements, appState, files, 'local') !== saved.current) e.preventDefault();
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, []);

  return (<>
    <Excalidraw
      initialData={scene}
      name={file.name.replace(/\.excalidraw$/i, '')}
      viewModeEnabled={!file.editable}
      onChange={(...next) => {
        latest.current = next;
        // The first change is Excalidraw's restored scene; opening a file never writes a revision.
        if (saved.current === null) { saved.current = serializeAsJSON(next[0], next[1], next[2], 'local'); return; }
        if (!stopped.current) schedule();
      }}
      renderTopRightUI={isMobile => isMobile ? null : <span className="wb-status" role="status">{status}</span>}
    >
      <MainMenu>
        <MainMenu.Item onSelect={() => window.location.assign('/')}>Back to KyDrive</MainMenu.Item>
        <MainMenu.DefaultItems.LoadScene />
        <MainMenu.DefaultItems.Export />
        <MainMenu.DefaultItems.SaveAsImage />
        <MainMenu.DefaultItems.ClearCanvas />
        <MainMenu.Separator />
        <MainMenu.DefaultItems.ToggleTheme />
        <MainMenu.DefaultItems.ChangeCanvasBackground />
        <MainMenu.DefaultItems.Help />
      </MainMenu>
    </Excalidraw>
    {status !== 'Saved' && <span className="wb-status wb-status-mobile" role="status">{status}</span>}
  </>);
}

function App() {
  const [state, setState] = useState<{ file: Meta; scene: ExcalidrawInitialDataState } | string | null>(null);
  useEffect(() => {
    const id = new URLSearchParams(window.location.search).get('file');
    if (!id) { setState('No whiteboard specified'); return; }
    open(id).then(setState, (e: unknown) => setState(e instanceof Error ? e.message : 'Could not open whiteboard'));
  }, []);
  if (state === null) return <p className="wb-message">Opening…</p>;
  if (typeof state === 'string') return <p className="wb-message" role="alert">{state}</p>;
  document.title = `${state.file.name} · KyDrive`;
  return <Board file={state.file} scene={state.scene} />;
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
