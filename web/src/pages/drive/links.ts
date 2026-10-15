import { change, record, sharedItem } from './client';
import type { SharedItem } from './client';

// An organisation link is /l/#<token>. The token stays in the fragment, which browsers never
// send to the server; it is moved to session storage and out of the address bar at once, then
// opened once the user is signed in.
const pending = 'ky-pending-link';

export function takeLinkFromURL(): void {
  if (location.pathname.replace(/\/$/, '') === '/l' && location.hash.length > 1) {
    sessionStorage.setItem(pending, location.hash.slice(1));
    history.replaceState(null, '', '/');
  }
}

export async function openPendingLink(): Promise<SharedItem | null> {
  const token = sessionStorage.getItem(pending);
  if (!token) return null;
  sessionStorage.removeItem(pending);
  const v = await change('/api/drive/links/open', 'POST', { token });
  if (!sharedItem(v)) throw new Error('Invalid link response');
  return v;
}

export type Link = { id: string; role: string; expires: number; created: string; created_by: string; opened: number };
export function link(v: unknown): v is Link {
  return record(v) && ['id', 'role', 'created', 'created_by'].every(k => typeof v[k] === 'string') && typeof v.expires === 'number' && typeof v.opened === 'number';
}

export type GuestLink = { id: string; expires: number; created: string; created_by: string; upload_only: boolean; has_password: boolean };
export function guestLink(v: unknown): v is GuestLink {
  return record(v) && ['id', 'created', 'created_by'].every(k => typeof v[k] === 'string') && typeof v.expires === 'number' && typeof v.upload_only === 'boolean' && typeof v.has_password === 'boolean';
}
