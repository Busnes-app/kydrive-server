// web/browser/file-management.spec.mjs
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';

// The server allows 20 sign-ins a minute and the suite already uses most of them. Workers restart
// per project, so keep the disposable session on disk and sign in again only when it is rejected.
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

test('rename, move, trash a folder, restore it and delete it permanently', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  await page.goto('/');
  expect((await page.request.post('/api/drive/personal-workspace', { headers, data: {} })).ok()).toBe(true);
  const spaces = await (await page.request.get('/api/drive/workspaces')).json();
  const mine = spaces.find(w => w.kind === 'personal');
  const tag = testInfo.project.name.replace(/\W/g, '');
  const folderName = `Box ${tag}`;
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/folders`, { headers, data: { parent: '', name: folderName } })).ok()).toBe(true);
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/documents`, { headers, data: { name: `note ${tag}`, kind: 'markdown', parent: '' } })).ok()).toBe(true);
  await page.reload();

  await page.getByLabel(`Actions for note ${tag}.md`).click();
  await page.getByRole('button', { name: 'Move', exact: true }).click();
  await page.getByRole('dialog').getByLabel('Name').fill(`moved ${tag}.md`);
  await page.getByRole('dialog').getByLabel('Folder').selectOption({ label: folderName });
  await page.getByRole('dialog').getByRole('button', { name: 'Move', exact: true }).click();
  await page.getByRole('button', { name: `${folderName}/` }).click();
  await expect(page.getByRole('link', { name: `moved ${tag}.md` })).toBeVisible();

  await page.getByRole('navigation', { name: 'Folder path' }).getByRole('button', { name: mine.name }).click();
  await page.getByLabel(`Actions for folder ${folderName}`).click();
  await page.getByRole('button', { name: 'Move to trash' }).click();
  await expect(page.getByRole('button', { name: `${folderName}/` })).toHaveCount(0);

  await page.getByRole('button', { name: 'Trash', exact: true }).click();
  await page.getByRole('row', { name: new RegExp(folderName) }).getByRole('button', { name: 'Restore', exact: true }).click();
  await page.getByRole('button', { name: 'Back to files' }).click();
  await expect(page.getByRole('button', { name: `${folderName}/` })).toBeVisible();

  await page.getByLabel(`Actions for folder ${folderName}`).click();
  await page.getByRole('button', { name: 'Move to trash' }).click();
  await page.getByRole('button', { name: 'Trash', exact: true }).click();
  page.once('dialog', d => d.accept());
  await page.getByRole('row', { name: new RegExp(folderName) }).getByRole('button', { name: 'Delete permanently' }).click();
  await expect(page.getByRole('row', { name: new RegExp(folderName) })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
