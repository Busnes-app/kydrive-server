import { test, expect } from '@playwright/test';

async function signIn(page) {
  await page.goto('/');
  expect((await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'BrowserUpdated456!' } })).ok()).toBe(true);
  const csrf = (await page.context().cookies()).find(c => c.name === 'ky_csrf').value;
  return { 'X-CSRF-Token': csrf };
}

// The test server pairs KyPost from the environment, so the switch starts off.
test('KyPost switch in Settings > Files and the pairing in Server settings', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  expect((await page.request.put('/api/drive/kypost', { headers, data: { enabled: false } })).ok()).toBe(true);
  await page.goto('/#/settings/files');
  await page.reload();
  const box = page.getByLabel('Let KyPost save and share my files');
  await expect(box).not.toBeChecked();
  await box.click();
  await expect(box).toBeChecked();
  await expect.poll(async () => (await (await page.request.get('/api/drive/kypost')).json()).enabled).toBe(true);
  await page.reload();
  await expect(box).toBeChecked();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('kypost-switch.png'), fullPage: true });

  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Server settings', exact: true }).click();
  await page.getByRole('tab', { name: 'System' }).click();
  await expect(page.getByText('Connected by the environment')).toBeVisible();
  await expect(page.getByText('https://post.example')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('kypost-status.png'), fullPage: true });
});
