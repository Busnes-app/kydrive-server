// Search finds what is shared with the user: a shared folder's file shows where it is shared
// from, with a path starting at the shared folder, and opens that folder in Shared with me.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';

// Shares file-management's saved session to stay under 20 sign-ins a minute.
const sessionFile = 'test-results/file-management-session.json';
// SCIM goes through a cookieless context, as a directory client would: a session cookie
// would make the CSRF check refuse it.
const scim = { Authorization: 'Bearer browser-test-scim-token-0123456789abcdef0123456789', 'Content-Type': 'application/scim+json' };

async function signIn(page) {
  await page.goto('/');
  const saved = await readFile(sessionFile, 'utf8').then(JSON.parse, () => null);
  if (saved) {
    await page.context().addCookies(saved);
    if ((await page.request.get('/api/auth/me')).ok()) return { 'X-CSRF-Token': saved.find(c => c.name === 'ky_csrf').value };
  }
  expect((await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'BrowserUpdated456!' } })).ok()).toBe(true);
  const cookies = await page.context().cookies();
  await mkdir('test-results', { recursive: true });
  await writeFile(sessionFile, JSON.stringify(cookies));
  return { 'X-CSRF-Token': cookies.find(c => c.name === 'ky_csrf').value };
}

test('finds a file in a folder shared with the user', async ({ page, request }, testInfo) => {
  const headers = await signIn(page);
  const me = await (await page.request.get('/api/auth/me')).json();
  const tag = `shared${testInfo.project.name.replace(/\W/g, '')}`;
  // A workspace the admin fills through a temporary group, shares one folder from, then leaves.
  const group = await request.post('/scim/v2/Groups', { headers: scim, data: { schemas: ['urn:ietf:params:scim:schemas:core:2.0:Group'], displayName: `${tag} crew`, members: [{ value: me.user.id }] } });
  expect(group.status()).toBe(201);
  const groupID = (await group.json()).id;
  const ws = await page.request.post('/api/drive/workspaces', { headers, data: { name: `Field team ${tag}`, quota: 1 << 20 } });
  expect(ws.status()).toBe(201);
  const space = await ws.json();
  expect((await page.request.put(`/api/drive/workspaces/${space.id}/grants/${groupID}`, { headers, data: { role: 'manager' } })).ok()).toBe(true);
  const folder = await (await page.request.post(`/api/drive/workspaces/${space.id}/folders`, { headers, data: { parent: '', name: 'Site reports' } })).json();
  const name = `${tag} survey.txt`;
  expect((await page.request.post(`/api/drive/workspaces/${space.id}/uploads?name=${encodeURIComponent(name)}&parent=${folder.id}&revision=0`, { headers, data: 'x' })).status()).toBe(201);
  expect((await page.request.put(`/api/drive/folders/${folder.id}/shares`, { headers, data: { principal_kind: 'user', principal_id: me.user.id, role: 'reader' } })).ok()).toBe(true);
  expect((await request.delete(`/scim/v2/Groups/${groupID}`, { headers: scim })).status()).toBe(204);

  await page.goto('/#/drive');
  await page.reload(); // the hash change alone keeps the signed-out page
  const search = page.getByRole('search');
  await page.getByLabel('Search files').fill(tag);
  await expect(search.getByText(/No matches|survey/).first()).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('search-shared.png') });
  const hit = search.getByRole('button', { name: new RegExp(name) });
  await expect(hit).toContainText(`Field team ${tag} / Site reports`);
  await hit.click();
  await expect(page.getByRole('heading', { name: 'Shared with me' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Folder path' }).getByRole('button', { name: 'Site reports' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('button', { name })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('search-shared-opened.png') });
});
