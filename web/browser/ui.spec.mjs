import { test, expect } from '@playwright/test';

async function fits(page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

test('production CSP, worker, themes, keyboard and responsive shell', async ({ page, context }, testInfo) => {
  const violations = [];
  page.on('console', message => {
    if (/Content Security Policy|violates.*directive/i.test(message.text())) violations.push(message.text());
  });
  const response = await page.goto('/');
  expect(response.headers()['content-security-policy']).toContain("script-src 'self'");
  expect(response.headers()['content-security-policy']).not.toContain("script-src 'self' 'unsafe-inline'");
  await expect.poll(() => page.evaluate(async () => (await navigator.serviceWorker.getRegistration())?.active?.state)).toBe('activated');
  await page.evaluate(async () => {
    const key = (await caches.keys())[0];
    await (await caches.open(key)).put('/', new Response('<h1>Stale shell</h1>', {
      headers: { 'Content-Type': 'text/html' },
    }));
  });
  // Online navigation must refresh the shell rather than pinning a previous deploy.
  await page.reload();
  await page.getByPlaceholder('admin', { exact: true }).fill('admin');
  await page.locator('input[type=password]').fill('IncorrectPassword123!');
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
  await expect(page.locator('form').locator('..')).toContainText(/invalid|incorrect|failed/i);
  await fits(page);
  await page.locator('input[type=password]').fill('BrowserUpdated456!');
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
  const nav = page.getByRole('navigation', { name: 'Primary' });
  await expect(nav).toBeVisible();
  await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
  await fits(page);
  // Appearance is the only theme control in the shell: native radio cards, one saved choice.
  const appearance = async (p) => {
    await p.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Appearance' }).click();
    await expect(p.getByRole('heading', { name: 'Appearance', level: 1 })).toBeVisible();
  };
  const pick = (p, name) => p.getByRole('radio', { name: new RegExp(`^${name}\\b`) });
  await expect(page.getByLabel('Color theme')).toHaveCount(0);
  await appearance(page);
  await fits(page);
  await pick(page, 'Paper Clean').check();
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(248, 250, 252)');
  await pick(page, 'Busnes Dark').check();
  await page.reload();
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(24, 35, 38)');
  const other = await context.newPage();
  await other.goto('/');
  await appearance(other);
  await pick(other, 'Busnes Light').check();
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(248, 246, 240)');
  await other.close();
  await appearance(page);
  await pick(page, 'Match system').check();
  await page.emulateMedia({ colorScheme: 'dark' });
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(24, 35, 38)');
  await page.emulateMedia({ colorScheme: 'light' });
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(248, 246, 240)');
  await page.emulateMedia({ colorScheme: testInfo.project.use.colorScheme });
  await pick(page, 'Ocean').check();
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(15, 27, 36)');
  await page.keyboard.press('ArrowRight');
  await expect(pick(page, 'Coffee')).toBeChecked();
  await pick(page, 'Match system').check();
  await nav.getByRole('button').first().focus();
  await page.keyboard.press('Tab');
  await expect(nav.getByRole('button').nth(1)).toBeFocused();
  await expect(nav.getByRole('button').nth(1)).toHaveCSS('outline-style', 'solid');
  await nav.getByRole('button', { name: 'Settings & DB' }).click();
  await expect(nav.getByRole('button', { name: 'Settings & DB' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('heading', { name: 'Settings', exact: true })).toBeVisible();
  await fits(page);
  await expect(page.getByRole('button', { name: 'Pair Device' })).toHaveCount(0);
  await page.evaluate(() => fetch('/browser-regression-uncached'));
  const cachedDynamic = await page.evaluate(async () => {
    const keys = await caches.keys();
    for (const key of keys) for (const req of await (await caches.open(key)).keys()) {
      if (/^\/(api|scim|browser-regression-uncached)(\/|$)/.test(new URL(req.url).pathname)) return true;
    }
    return false;
  });
  expect(cachedDynamic).toBe(false);
  expect(violations).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath('settings.png'), fullPage: true });
  await context.setOffline(true);
  const offline = await page.reload();
  expect(offline.ok()).toBe(true);
  expect(await offline.text()).toContain('id="root"');
  await context.setOffline(false);
});
