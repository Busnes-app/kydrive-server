import { secureFetch } from '../../api';

export type Workspace = { id: string; name: string; role: string; kind: 'personal' | 'shared'; quota: number; used: number; trash_days: number; keep_versions: number };
export type DriveFile = { id: string; name: string; parent: string; revision: number; size: number; trashed: boolean };
export type Folder = { id: string; name: string; parent: string; trashed: boolean };
export type Grant = { group_id: string; name: string; role: string };
export type Group = { id: string; display_name: string };
export type Version = { revision: number; size: number; created: string };
export function record(v: unknown): v is Record<string, unknown> { return typeof v === 'object' && v !== null && !Array.isArray(v); }
export function workspace(v: unknown): v is Workspace { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.role === 'string' && (v.kind === 'personal' || v.kind === 'shared') && typeof v.quota === 'number' && typeof v.used === 'number' && typeof v.trash_days === 'number' && typeof v.keep_versions === 'number'; }
export function file(v: unknown): v is DriveFile { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.parent === 'string' && typeof v.revision === 'number' && typeof v.size === 'number' && typeof v.trashed === 'boolean'; }
export function folder(v: unknown): v is Folder { return record(v) && typeof v.id === 'string' && typeof v.name === 'string' && typeof v.parent === 'string' && typeof v.trashed === 'boolean'; }
export function grant(v: unknown): v is Grant { return record(v) && typeof v.group_id === 'string' && typeof v.name === 'string' && typeof v.role === 'string'; }
export function group(v: unknown): v is Group { return record(v) && typeof v.id === 'string' && typeof v.display_name === 'string'; }
export function version(v: unknown): v is Version { return record(v) && typeof v.revision === 'number' && typeof v.size === 'number' && typeof v.created === 'string'; }
export async function response(r: Response): Promise<unknown> { const v: unknown = await r.json(); if (!r.ok) throw new Error(record(v) && typeof v.error === 'string' ? v.error : 'Drive request failed'); return v; }
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
