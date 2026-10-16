// web/browser/guest.spec.mjs
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';

// Shares file-management's saved session to stay under 20 sign-ins a minute.
const sessionFile = 'test-results/file-management-session.json';

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

test('a guest without an account opens a password link and downloads a file', async ({ page, browser }, testInfo) => {
  const headers = await signIn(page);
  expect((await page.request.put('/api/drive/guest-links-policy', { headers, data: { enabled: true, max_days: 7 } })).ok()).toBe(true);
  expect((await page.request.post('/api/drive/personal-workspace', { headers, data: {} })).ok()).toBe(true);
  const mine = (await (await page.request.get('/api/drive/workspaces')).json()).find(w => w.kind === 'personal');
  const tag = testInfo.project.name.replace(/\W/g, '');
  const folder = await (await page.request.post(`/api/drive/workspaces/${mine.id}/folders`, { headers, data: { parent: '', name: `For client ${tag}` } })).json();
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/uploads?name=brief-${tag}.txt&parent=${folder.id}&revision=0`, { headers: { ...headers, 'Content-Type': 'application/octet-stream' }, data: 'the brief' })).ok()).toBe(true);
  const created = await page.request.post(`/api/drive/folders/${folder.id}/guest-links`, { headers, data: { days: 1, password: 'correct horse' } });
  expect(created.ok()).toBe(true);
  const { url } = await created.json();
  const token = url.split('#')[1];

  // A fresh browser: no cookies, no session.
  const guest = await browser.newContext();
  const g = await guest.newPage();
  const requests = [];
  g.on('request', r => requests.push(r.url()));
  await g.goto(new URL(url).pathname + '#' + token);
  await g.getByLabel('Password').fill('correct horse');
  const viewed = g.waitForResponse(r => r.url().endsWith('/api/public/links/view') && r.status() === 200);
  await g.getByRole('button', { name: 'Open' }).click();
  const response = await viewed;
  expect(response.headers()['referrer-policy']).toBe('no-referrer');
  expect(response.headers()['x-robots-tag']).toContain('noindex');
  await expect(g.getByText(`brief-${tag}.txt`)).toBeVisible();
  const download = g.waitForEvent('download');
  await g.getByRole('button', { name: 'Download' }).click();
  expect(await readFile(await (await download).path(), 'utf8')).toBe('the brief');
  expect(requests.some(u => u.includes(token))).toBe(false); // the token never leaves the fragment
  expect(await guest.cookies()).toEqual([]);
  expect(await g.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await guest.close();
});
