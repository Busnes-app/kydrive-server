import { test, expect } from '@playwright/test';

async function signIn(page) {
  await page.goto('/');
  expect((await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'BrowserUpdated456!' } })).ok()).toBe(true);
  const csrf = (await page.context().cookies()).find(c => c.name === 'ky_csrf').value;
  return { 'X-CSRF-Token': csrf };
}

test('files open in a new tab or in place, per saved user preference', async ({ page, browser }, testInfo) => {
  const headers = await signIn(page);
  // The server is shared by every project; start from the default.
  expect((await page.request.put('/api/auth/preferences', { headers, data: { open_in_new_tab: true } })).ok()).toBe(true);
  await page.goto('/');
  const preference = page.getByLabel('Open files in a new tab');
  await expect(preference).toBeChecked();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);

  // Creating a file opens the editor in a new tab and leaves the drive in place.
  const name = `tab-${testInfo.project.name}`;
  await page.locator('.drive-new-file summary').click();
  await page.getByRole('button', { name: 'New whiteboard' }).click();
  await page.getByLabel('File name').fill(name);
  const created = page.waitForEvent('popup');
  await page.getByRole('button', { name: 'Create & open' }).click();
  const editor = await created;
  await expect(editor).toHaveURL(/\/whiteboard\.html\?file=/);
  await expect(editor.locator('canvas.interactive')).toBeVisible();
  await editor.close();
  await expect(page).toHaveURL(/\/$/);
  const link = page.getByRole('link', { name: `${name}.excalidraw` });
  await expect(link).toBeVisible();

  // Opening an existing file also uses a new tab.
  const opened = page.waitForEvent('popup');
  await link.click();
  await (await opened).close();

  // Turning it off is saved for the account, not the browser: a context with the session
  // cookie but no local storage still sees it (a second sign-in would trip the login limit).
  await preference.uncheck();
  await expect.poll(async () => (await (await page.request.get('/api/auth/me')).json()).open_in_new_tab).toBe(false);
  const other = await browser.newContext({ storageState: { cookies: await page.context().cookies(), origins: [] } });
  const otherPage = await other.newPage();
  await otherPage.goto('/');
  await expect(otherPage.getByLabel('Open files in a new tab')).not.toBeChecked();
  await other.close();

  // With it off, files open in the same tab.
  await page.reload();
  await page.getByRole('link', { name: `${name}.excalidraw` }).click();
  await expect(page).toHaveURL(/\/whiteboard\.html\?file=/);
  expect(page.context().pages()).toHaveLength(1);
});
