// The episode-8 launch bugs, checked against the real server: upload over an existing file,
// version dates, menus that close, document icons, the trash title and the Account layout; and
// from the re-shoot, name-match highlights and duplicate workspace names.
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
const fits = page => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth);

test('uploading an existing name offers Replace, Keep both and Cancel', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  await page.request.post('/api/drive/personal-workspace', { headers, data: {} });
  const mine = (await (await page.request.get('/api/drive/workspaces')).json()).find(w => w.kind === 'personal');
  const name = `budget-${testInfo.project.name.replace(/\W/g, '')}.csv`;
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/uploads?name=${name}&parent=&revision=0`, { headers, data: 'item,amount\n' })).status()).toBe(201);
  await page.reload();
  const choose = async (body, button) => {
    const chooser = page.waitForEvent('filechooser');
    await page.locator('.page-header').getByRole('button', { name: 'Upload files', exact: true }).click();
    await (await chooser).setFiles({ name, mimeType: 'text/csv', buffer: Buffer.from(body) });
    const ask = page.getByRole('dialog', { name: `${name} already exists` });
    await expect(ask).toBeVisible();
    await expect(page.getByText('revision conflict')).toHaveCount(0);
    if (testInfo.project.name === 'light-1280' && button === 'Replace (new version)') await page.screenshot({ path: testInfo.outputPath('conflict.png') });
    await ask.getByRole('button', { name: button, exact: true }).click();
    await expect(ask).toHaveCount(0);
  };
  const files = async () => (await (await page.request.get(`/api/drive/workspaces/${mine.id}/files?trash=false`)).json()).filter(f => f.parent === '');

  await choose('item,amount\nHosting,1300\n', 'Replace (new version)');
  await expect.poll(async () => (await files()).find(f => f.name === name)?.revision).toBe(2);
  await choose('item,amount\nPrint,900\n', 'Keep both');
  const kept = name.replace('.csv', ' (2).csv');
  await expect.poll(async () => (await files()).some(f => f.name === kept)).toBe(true);
  await choose('ignored', 'Cancel');
  expect((await files()).filter(f => f.name.startsWith(name.replace('.csv', ''))).length).toBe(2);

  // A .csv shows its type icon, never a blank thumbnail.
  const row = page.getByRole('row', { name: new RegExp(name.replace(/[.()]/g, '\\$&')) }).first();
  await expect(row.locator('svg.drive-file-icon')).toBeVisible();
  await expect(row.locator('img')).toHaveCount(0);

  // The menu closes once Version history is chosen; dates are local, not raw RFC 3339.
  await page.getByLabel(`Actions for ${name}`).click();
  await expect(page.getByRole('menu')).toBeVisible();
  await page.getByRole('menuitem', { name: 'Version history' }).click();
  await expect(page.getByRole('menu')).toHaveCount(0);
  const history = page.getByRole('dialog', { name: 'Version history' });
  await expect(history.getByText('Current')).toBeVisible();
  await expect(history.locator('tbody tr')).toHaveCount(2);
  expect(await history.textContent()).not.toMatch(/\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/);
  await expect(history.locator('time').first()).toHaveAttribute('datetime', /Z$/);
  await history.getByRole('button', { name: 'Restore' }).click();
  await expect.poll(async () => (await files()).find(f => f.name === name)?.revision).toBe(3);
  expect(await fits(page)).toBe(true);
});

test('the trash has a title and the Account page has the app layout', async ({ page }, testInfo) => {
  await signIn(page);
  await page.reload();
  const admin = page.locator('details.drive-admin');
  await expect(admin).not.toHaveAttribute('open');
  await expect(page.locator('.page-header').getByRole('button', { name: 'Upload files', exact: true })).toBeVisible();
  const task = await page.locator('.drive-files-panel').boundingBox();
  const management = await admin.boundingBox();
  expect(task.y + task.height).toBeLessThanOrEqual(management.y);
  await admin.locator(':scope > summary').focus();
  await page.keyboard.press('Enter');
  await expect(page.getByText('Create shared workspace', { exact: true })).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(admin).not.toHaveAttribute('open');
  await page.getByRole('button', { name: 'Trash', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Trash', level: 1 })).toBeVisible();
  await page.getByRole('button', { name: 'Back to files' }).click();

  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Account' }).click();
  await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
  const card = page.getByRole('region', { name: 'Two-factor authentication' });
  const main = await page.locator('.app-main').boundingBox();
  const box = await card.boundingBox();
  // Cards sit inside the page's padding, not flush against the sidebar.
  expect(box.x - main.x).toBeGreaterThanOrEqual(testInfo.project.use.viewport.width < 720 ? 12 : 32);
  // Each Kind choice is one card with the radio beside its label, not stacked above it.
  for (const label of [/^File manager or office app/, /^AI agent/]) {
    const choice = page.locator('label.choice').filter({ hasText: label });
    const radio = await choice.locator('input[type=radio]').boundingBox();
    const title = await choice.locator('b').boundingBox();
    expect(radio.x + radio.width).toBeLessThanOrEqual(title.x);
    expect(Math.abs((radio.y + radio.height / 2) - (title.y + title.height / 2))).toBeLessThan(14);
  }
  await expect(page.locator('fieldset.choice-group')).toHaveCSS('border-top-width', '0px');
  await page.screenshot({ path: testInfo.outputPath('account.png'), fullPage: true });
  expect(await fits(page)).toBe(true);
});

test('search marks the matched words in a file name', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  await page.request.post('/api/drive/personal-workspace', { headers, data: {} });
  const mine = (await (await page.request.get('/api/drive/workspaces')).json()).find(w => w.kind === 'personal');
  const word = `budget${testInfo.project.name.replace(/\W/g, '')}`;
  const name = `Q4 ${word}.csv`;
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/uploads?name=${encodeURIComponent(name)}&parent=&revision=0`, { headers, data: 'item,amount\n' })).status()).toBe(201);
  await page.reload();
  await page.getByLabel('Search files').fill(word.toUpperCase());
  const result = page.getByRole('search').getByRole('button', { name: new RegExp(name.replace('.', '\\.')) });
  await expect(result.locator('span > mark')).toHaveText(word);
});

test('a second shared workspace with the same name is refused', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  const name = `Studio ${testInfo.project.name.replace(/\W/g, '')}`;
  expect((await page.request.post('/api/drive/workspaces', { headers, data: { name, quota: 1048576 } })).status()).toBe(201);
  expect((await page.request.post('/api/drive/workspaces', { headers, data: { name: name.toLowerCase(), quota: 1048576 } })).status()).toBe(409);
  await page.reload();
  await page.getByText('Administration', { exact: true }).click();
  await page.getByText('Create shared workspace').click();
  await page.getByLabel('Name', { exact: true }).fill(name);
  await page.getByRole('button', { name: 'Create workspace' }).click();
  await expect(page.getByText(`A shared workspace named “${name}” already exists.`)).toBeVisible();
  const all = await (await page.request.get('/api/drive/workspaces?admin=true')).json();
  expect(all.filter(w => w.name.toLowerCase() === name.toLowerCase())).toHaveLength(1);
});
