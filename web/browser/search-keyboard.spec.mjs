// web/browser/search-keyboard.spec.mjs
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';

// Shares file-management's saved session to stay under the server's 20 sign-ins a minute.
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


test('search: arrows move from the box through the results and Enter opens one', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  expect((await page.request.post('/api/drive/personal-workspace', { headers, data: {} })).ok()).toBe(true);
  const mine = (await (await page.request.get('/api/drive/workspaces')).json()).find(w => w.kind === 'personal');
  const name = `kbfind ${testInfo.project.name.replace(/\W/g, '')}`;
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/folders`, { headers, data: { parent: '', name } })).ok()).toBe(true);
  await page.goto('/');

  const box = page.getByRole('combobox', { name: 'Search files' });
  await box.fill(name);
  const hit = page.getByRole('search').getByRole('option', { name: new RegExp(name) });
  await expect(hit).toBeVisible();
  await page.keyboard.press('ArrowDown');
  await expect(box).toBeFocused();
  await expect(box).toHaveAttribute('aria-activedescendant', await hit.getAttribute('id'));
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { name, level: 1, exact: true })).toBeVisible();
  await expect(box).toHaveValue('');
});
