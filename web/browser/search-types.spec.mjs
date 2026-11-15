// Search type chips end to end: a type alone lists that type, narrows a query, is in the
// address so Back restores it, and works from the keyboard.
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

test('filters search results by type from the chips', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  await page.request.post('/api/drive/personal-workspace', { headers, data: {} });
  const mine = (await (await page.request.get('/api/drive/workspaces')).json()).find(w => w.kind === 'personal');
  const tag = `kind${testInfo.project.name.replace(/\W/g, '')}`;
  const pdf = `${tag} plan.PDF`, doc = `${tag} plan.docx`;
  for (const name of [pdf, doc]) {
    expect((await page.request.post(`/api/drive/workspaces/${mine.id}/uploads?name=${encodeURIComponent(name)}&parent=&revision=0`, { headers, data: 'x' })).status()).toBe(201);
  }
  expect((await page.request.get('/api/drive/search?type=pdf')).status()).toBe(400);

  await page.goto('/#/drive');
  await page.reload(); // the hash change alone keeps the signed-out page
  const search = page.getByRole('search');
  const box = page.getByLabel('Search files');
  await box.fill(tag);
  await expect(search.getByRole('button', { name: pdf })).toBeVisible();
  await expect(search.getByRole('button', { name: doc })).toBeVisible();

  // Keyboard: Tab from the box reaches the chips; Space presses one.
  await box.focus();
  await page.keyboard.press('Tab');
  await expect(search.getByRole('button', { name: 'Folders' })).toBeFocused();
  for (let i = 0; i < 4; i++) await page.keyboard.press('Tab');
  const pdfs = search.getByRole('group', { name: 'File type' }).getByRole('button', { name: 'PDFs' });
  await expect(pdfs).toBeFocused();
  await page.keyboard.press('Space');
  await expect(pdfs).toHaveAttribute('aria-pressed', 'true');
  await expect(page).toHaveURL(/\?type=pdfs#\/drive$/);
  await expect(search.getByRole('button', { name: doc })).toHaveCount(0);
  await expect(search.getByRole('button', { name: pdf })).toBeVisible();

  // A type alone lists every PDF the user can read.
  await box.fill('');
  await expect(search.getByRole('button', { name: pdf })).toBeVisible();
  await expect(search.getByRole('button', { name: doc })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('type-pdfs.png') });

  // Another chip replaces it; Back restores PDFs, Forward Documents.
  await search.getByRole('button', { name: 'Documents' }).click();
  await expect(page).toHaveURL(/\?type=documents#\/drive$/);
  await expect(search.getByRole('button', { name: doc })).toBeVisible();
  await page.goBack();
  await expect(pdfs).toHaveAttribute('aria-pressed', 'true');
  await expect(search.getByRole('button', { name: pdf })).toBeVisible();
  await page.goForward();
  await expect(search.getByRole('button', { name: 'Documents' })).toHaveAttribute('aria-pressed', 'true');

  // A reload keeps the type; opening a result clears it, and Back returns to the list.
  await page.reload();
  await expect(search.getByRole('button', { name: doc })).toBeVisible();
  await search.getByRole('button', { name: doc }).click();
  await expect(page).toHaveURL(/\/#\/drive$/);
  await expect(page.getByRole('group', { name: 'File type' })).toHaveCount(0);
  await page.goBack();
  await expect(search.getByRole('button', { name: doc })).toBeVisible();
  await box.press('Escape');
  await expect(page.getByRole('group', { name: 'File type' })).toHaveCount(0);
});
