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
  // SSO is not configured here: no divider and no SSO button.
  await expect(page.getByText(/Single Sign[- ]?On/i)).toHaveCount(0);
  await expect(page.locator('.ky-auth-divider')).toHaveCount(0);
  await expect(page.locator('.ky-auth-check')).not.toHaveCSS('border-top-style', 'solid');
  await page.getByLabel('Username', { exact: true }).fill('admin');
  await page.locator('input[type=password]').fill('IncorrectPassword123!');
  await expect(page.getByRole('button', { name: 'Sign in', exact: true }).locator('svg')).toHaveCount(1);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('form').locator('..')).toContainText(/invalid|incorrect|failed/i);
  await fits(page);
  await page.locator('input[type=password]').fill('BrowserUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  const nav = page.getByRole('navigation', { name: 'Primary' });
  await expect(nav).toBeVisible();
  await expect(page.getByRole('heading', { name: 'My files', exact: true })).toBeVisible();
  await fits(page);
  // A local account without SSO gets an empty suite list and no app rail.
  expect(await (await page.request.get('/api/suite/apps')).json()).toEqual({ apps: [] });
  await expect(page.locator('.ky-app-rail')).toHaveCount(0);
  // Appearance is the only theme control in the shell: native radio cards, one saved choice.
  // It is a Settings section, behind the account footer's gear.
  const appearance = async (p) => {
    await p.getByRole('button', { name: 'Settings', exact: true }).click();
    await p.getByRole('menu', { name: 'Settings' }).getByRole('menuitem', { name: 'Appearance', exact: true }).click();
    const tab = p.getByRole('navigation', { name: 'Settings sections' }).getByRole('link', { name: 'Appearance', exact: true });
    await expect(tab).toHaveAttribute('aria-current', 'page');
    await expect(p.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible();
  };
  const pick = (p, pair, mode) => p.getByRole('group', { name: pair, exact: true }).getByRole('radio', { name: new RegExp(`^${mode}`) });
  await expect(page.getByLabel('Color theme')).toHaveCount(0);
  await appearance(page);
  await expect(page.getByRole('button', { name: 'Settings', exact: true })).toHaveAttribute('aria-current', 'page');
  await expect(nav.locator('[aria-current]')).toHaveCount(0);
  await fits(page);
  await pick(page, 'Nord', 'Light').check();
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(236, 239, 244)');
  // Only the selected segment carries the check mark.
  await expect(page.locator('.appearance-mode svg')).toHaveCount(1);
  await expect(page.locator('.appearance-mode').filter({ has: page.locator('input:checked') }).locator('svg')).toHaveCount(1);
  await pick(page, 'Busnes', 'Dark').check();
  await page.reload();
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(24, 35, 38)');
  const other = await context.newPage();
  await other.goto('/');
  await appearance(other);
  await pick(other, 'Busnes', 'Light').check();
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(248, 246, 240)');
  await other.close();
  await appearance(page);
  await pick(page, 'Busnes', 'Match system').check();
  await page.emulateMedia({ colorScheme: 'dark' });
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(24, 35, 38)');
  await page.emulateMedia({ colorScheme: 'light' });
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(248, 246, 240)');
  await page.emulateMedia({ colorScheme: testInfo.project.use.colorScheme });
  await pick(page, 'Ocean', 'Dark').check();
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(15, 27, 36)');
  // One radio group across the pairs: the arrow key moves on to the next pair.
  await page.keyboard.press('ArrowRight');
  await expect(pick(page, 'Forest', 'Match system')).toBeChecked();
  await pick(page, 'Busnes', 'Match system').check();
  await nav.getByRole('button').first().focus();
  await page.keyboard.press('Tab');
  await expect(nav.getByRole('button').nth(1)).toBeFocused();
  await expect(nav.getByRole('button').nth(1)).toHaveCSS('outline-style', 'solid');
  await nav.getByRole('button', { name: 'Server settings', exact: true }).click();
  await expect(nav.getByRole('button', { name: 'Server settings', exact: true })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('[aria-current="page"]')).toHaveCount(1);
  await expect(page.getByRole('heading', { name: 'Server settings', exact: true })).toBeVisible();
  await fits(page);
  await expect(page.getByRole('button', { name: 'Pair Device' })).toHaveCount(0);
  // New suite palettes must keep status text readable against both app surfaces.
  await appearance(page);
  const statusContrast = [];
  for (const id of ['light-matter', 'white-cliffs', 'forest-light', 'polished-ky', 'paper']) {
    await page.locator(`input[name="kydrive-theme"][value="${id}"]`).check();
    statusContrast.push(...await page.evaluate((theme) => {
      const root = document.documentElement;
      const probe = document.createElement('span');
      root.append(probe);
      const luminance = (value) => {
        probe.style.color = value;
        const color = getComputedStyle(probe).color;
        const scale = color.startsWith('color(') ? 1 : 255;
        return color.match(/[\d.]+/g).slice(0, 3).map(Number)
          .map(c => c / scale).map(c => c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)
          .reduce((sum, c, i) => sum + c * [0.2126, 0.7152, 0.0722][i], 0);
      };
      const rows = ['--danger', '--success'].flatMap(status => ['--bg', '--panel'].map(surface => {
        const [a, b] = [status, surface].map(v => luminance(`var(${v})`));
        return { theme, status, surface, ratio: (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05) };
      }));
      probe.remove();
      return rows;
    }, id));
  }
  await page.mouse.move(0, 0);
  await testInfo.attach('status-contrast', { body: JSON.stringify(statusContrast, null, 2), contentType: 'application/json' });
  await page.screenshot({ path: testInfo.outputPath('light-status-contrast.png'), fullPage: true });
  for (const row of statusContrast) expect(row.ratio, JSON.stringify(row)).toBeGreaterThanOrEqual(4.5);
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
