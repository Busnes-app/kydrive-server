import { secureFetch } from '../../api';

export type Workspace = { id: string; name: string; role: string; kind: 'personal' | 'shared'; quota: number; used: number; trash_days: number; keep_versions: number; editors_share: boolean };
export type DriveFile = { id: string; name: string; parent: string; revision: number; size: number; trashed: boolean; modified?: string };
export type Folder = { id: string; name: string; parent: string; trashed: boolean };
export type Grant = { group_id: string; name: string; role: string };
export type Group = { id: string; display_name: string };
export type Version = { revision: number; size: number; created: string };
export function record(v: unknown): v is Record<string, unknown> { return typeof v === 'object' && v !== null && !Array.isArray(v); }
export function workspace(v: unknown): v is Workspace { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.role === 'string' && (v.kind === 'personal' || v.kind === 'shared') && typeof v.quota === 'number' && typeof v.used === 'number' && typeof v.trash_days === 'number' && typeof v.keep_versions === 'number' && typeof v.editors_share === 'boolean'; }
export function file(v: unknown): v is DriveFile { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.parent === 'string' && typeof v.revision === 'number' && typeof v.size === 'number' && typeof v.trashed === 'boolean' && (v.modified === undefined || typeof v.modified === 'string'); }
export function folder(v: unknown): v is Folder { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.parent === 'string' && typeof v.trashed === 'boolean'; }
export function grant(v: unknown): v is Grant { return record(v) && typeof v.group_id === 'string' && typeof v.name === 'string' && typeof v.role === 'string'; }
export function group(v: unknown): v is Group { return record(v) && typeof v.id === 'string' && typeof v.display_name === 'string'; }
export function version(v: unknown): v is Version { return record(v) && typeof v.revision === 'number' && typeof v.size === 'number' && typeof v.created === 'string'; }
// HttpError keeps the status so callers can react to a conflict (409) without reading the message.
export class HttpError extends Error { constructor(message: string, readonly status: number) { super(message); } }
export async function response(r: Response): Promise<unknown> { const v: unknown = await r.json().catch(() => null); if (!r.ok) throw new HttpError(record(v) && typeof v.error === 'string' ? v.error : 'Drive request failed', r.status); return v; }
export async function list<T>(url: string, guard: (v: unknown) => v is T): Promise<T[]> { const v = await response(await fetch(url)); if (!Array.isArray(v) || !v.every(guard)) throw new Error('Invalid server response'); return v; }
export async function change(url: string, method: string, body: unknown): Promise<unknown> { return response(await secureFetch(url, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })); }
export const bytes = (n: number) => n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KiB` : n < 1073741824 ? `${(n / 1048576).toFixed(1)} MiB` : `${(n / 1073741824).toFixed(1)} GiB`;

export async function remove(url: string): Promise<unknown> { return response(await secureFetch(url, { method: 'DELETE' })); }

export function breadcrumb(folders: Folder[], id: string): Folder[] {
  const byID = new Map(folders.map(f => [f.id, f]));
  const out: Folder[] = [];
  const seen = new Set<string>();
  for (let at = byID.get(id); at && !seen.has(at.id); at = byID.get(at.parent)) { seen.add(at.id); out.unshift(at); }
  return out;
}

// SHARED_WITH_ME is the sidebar selection for items shared with the user; never a workspace ID.
export const SHARED_WITH_ME = 'shared-with-me';

const editorFormats = new Set(['docx', 'odt', 'txt', 'rtf', 'md', 'xlsx', 'ods', 'csv', 'pptx', 'odp', 'pdf', 'excalidraw']);
export const extension = (name: string) => { const dot = name.lastIndexOf('.'); return dot > 0 ? name.slice(dot + 1).toLowerCase() : ''; };

// hasEditor reports whether Euro-Office or the whiteboard can open a file.
export function hasEditor(name: string): boolean { return editorFormats.has(extension(name)); }

// opensInEditor reports whether clicking a file's name opens its editor; PDFs and every file
// without an editor open in quick look.
export function opensInEditor(name: string): boolean { return hasEditor(name) && extension(name) !== 'pdf'; }

export function editorURL(f: { id: string; name: string }): string {
  if (f.name.toLowerCase().endsWith('.excalidraw')) return `/whiteboard.html?file=${encodeURIComponent(f.id)}`;
  return `/editor.html?file=${encodeURIComponent(f.id)}`;
}

export type Share = { id: string; principal_kind: 'user' | 'group'; principal_id: string; name: string; role: string };
export type SharedItem = { kind: 'file' | 'folder'; id: string; workspace: string; workspace_name: string; parent: string; name: string; role: string };
export type Person = { id: string; name: string; username: string };
export function share(v: unknown): v is Share { return record(v) && typeof v.id === 'string' && (v.principal_kind === 'user' || v.principal_kind === 'group') && typeof v.principal_id === 'string' && typeof v.name === 'string' && typeof v.role === 'string'; }
export function sharedItem(v: unknown): v is SharedItem { return record(v) && (v.kind === 'file' || v.kind === 'folder') && ['id', 'workspace', 'workspace_name', 'parent', 'name', 'role'].every(k => typeof v[k] === 'string'); }
export function person(v: unknown): v is Person { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.username === 'string'; }
