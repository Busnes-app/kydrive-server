// web/browser/row-menu.spec.mjs
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

test('row menu: keyboard, focus return through a dialog, stays in the window, adds no style element', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  expect((await page.request.post('/api/drive/personal-workspace', { headers, data: {} })).ok()).toBe(true);
  const mine = (await (await page.request.get('/api/drive/workspaces')).json()).find(w => w.kind === 'personal');
  const name = `menu ${testInfo.project.name.replace(/\W/g, '')}`;
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/documents`, { headers, data: { name, kind: 'markdown', parent: '' } })).ok()).toBe(true);
  await page.goto('/');

  const trigger = page.getByRole('button', { name: `Actions for ${name}.md` });
  const styles = await page.locator('style').count();
  await trigger.focus();
  await page.keyboard.press('Enter');
  const menu = page.getByRole('menu', { name: `Actions for ${name}.md` });
  await expect(menu).toBeVisible();
  const items = menu.getByRole('menuitem');
  await expect(items.first()).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(items.nth(1)).toBeFocused();
  await page.keyboard.press('ArrowUp');
  await page.keyboard.press('ArrowUp');
  await expect(items.last()).toBeFocused();
  expect(await page.locator('style').count()).toBe(styles);

  const box = await menu.boundingBox();
  const view = page.viewportSize();
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.y + box.height).toBeLessThanOrEqual(view.height);
  expect(box.x + box.width).toBeLessThanOrEqual(view.width);

  await page.keyboard.press('Escape');
  await expect(menu).toHaveCount(0);
  await expect(trigger).toBeFocused();

  await page.keyboard.press('Enter');
  await menu.getByRole('menuitem', { name: 'Version history' }).click();
  await expect(menu).toHaveCount(0);
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
});
