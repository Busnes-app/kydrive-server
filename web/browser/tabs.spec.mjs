// web/browser/tabs.spec.mjs
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


test('settings tabs: arrows move between tabs, Enter or Space opens one', async ({ page }) => {
  await signIn(page);
  await page.goto('/#/server-settings');
  await page.reload();
  const list = page.getByRole('tablist', { name: 'Settings sections' });
  const trash = list.getByRole('tab', { name: 'Trash' });
  await expect(trash).toHaveAttribute('aria-selected', 'true');
  await trash.focus();
  await page.keyboard.press('ArrowRight');
  const guest = list.getByRole('tab', { name: 'Guest links' });
  await expect(guest).toBeFocused();
  await expect(trash).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Enter');
  await expect(guest).toHaveAttribute('aria-selected', 'true');
  // Base UI keeps the outgoing panel mounted, inert, while its exit transition runs.
  await expect(page.locator('[role=tabpanel]:not([inert])')).toHaveAttribute('aria-labelledby', await guest.getAttribute('id'));
  await page.keyboard.press('ArrowLeft');
  await page.keyboard.press('ArrowLeft');
  await expect(list.getByRole('tab', { name: 'System' })).toBeFocused();
  await page.keyboard.press('Space');
  await expect(page.getByRole('heading', { name: 'System' })).toBeVisible();
});
