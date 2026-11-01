import { test, expect } from '@playwright/test';

async function signIn(page) {
  await page.goto('/');
  expect((await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'BrowserUpdated456!' } })).ok()).toBe(true);
  await page.reload();
}

// The gear opens a menu of the Settings sections (ky-ui 0.11.0), in the footer and the phone header.
test('settings menu: keyboard, current section and closing', async ({ page }) => {
  await signIn(page);
  const gear = page.getByRole('button', { name: 'Settings', exact: true });
  const menu = page.getByRole('menu', { name: 'Settings' });
  const items = menu.getByRole('menuitem');
  await expect(gear).toHaveCount(1); // the other placement is hidden at this width
  await expect(gear).toHaveAttribute('aria-expanded', 'false');
  await expect(menu).toBeHidden();
  // The gear and Sign out sit side by side, inside the sidebar.
  const [g, out, bar] = [await gear.boundingBox(), await page.getByRole('button', { name: 'Sign out' }).boundingBox(), await page.locator('.ky-sidebar').boundingBox()];
  expect(Math.abs(g.y - out.y)).toBeLessThan(1);
  expect(out.x + out.width).toBeLessThanOrEqual(bar.x + bar.width);

  await gear.focus();
  await page.keyboard.press('Enter');
  await expect(menu).toBeVisible();
  await expect(items).toHaveText(['Account', 'Phones & apps', 'Appearance', 'Files']);
  await expect(items.first()).toBeFocused();
  // The panel stays inside the window.
  const box = await menu.boundingBox();
  const { width, height } = page.viewportSize();
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(width);
  expect(box.y + box.height).toBeLessThanOrEqual(height);
  await page.keyboard.press('ArrowDown');
  await expect(items.nth(1)).toBeFocused();
  await page.keyboard.press('End');
  await expect(items.nth(3)).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(items.first()).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();
  await expect(gear).toBeFocused();

  // Space opens too; Enter on an item follows it and leaves focus on the gear.
  await page.keyboard.press('Space');
  await expect(items.first()).toBeFocused();
  await page.keyboard.press('End');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/#\/settings\/files$/);
  await expect(menu).toBeHidden();
  await expect(gear).toBeFocused();
  await expect(gear).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('navigation', { name: 'Settings sections' }).getByRole('link', { name: 'Files' })).toHaveAttribute('aria-current', 'page');

  await page.keyboard.press('ArrowDown');
  await expect(menu.getByRole('menuitem', { name: 'Files' })).toHaveAttribute('aria-current', 'page');
  await expect(menu.locator('[aria-current]')).toHaveCount(1);
  await page.keyboard.press('Tab');
  await expect(menu).toBeHidden();
  await gear.click();
  await expect(menu).toBeVisible();
  // A corner outside the menu (on phones it covers the page head).
  await page.mouse.click(4, height - 4);
  await expect(menu).toBeHidden();
});
