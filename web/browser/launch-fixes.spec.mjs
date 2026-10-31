// The episode-8 launch bugs, checked against the real server: upload over an existing file,
// version dates, menus that close, document icons, the trash title and the Account layout; and
// from the re-shoot, name-match highlights, duplicate workspace names and workspace rename/delete.
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
    await page.locator('.ky-page-head').getByRole('button', { name: 'Upload files', exact: true }).click();
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
  await page.getByLabel(`Actions for ${name}`).click();
  await page.getByRole('menuitem', { name: 'Share', exact: true }).click();
  const sharing = page.getByRole('dialog', { name: `Share ${name}`, exact: true });
  await expect(sharing.getByLabel('Find a person', { exact: true })).toHaveAccessibleDescription(/choose Add as reader beside a matching person/);
  const help = await sharing.locator('#share-person-help').boundingBox();
  const person = await sharing.getByLabel('Find a person', { exact: true }).boundingBox();
  expect(help.y).toBeGreaterThanOrEqual(person.y + person.height);
  if (testInfo.project.use.viewport.width < 720) {
    const access = await sharing.getByLabel('Access', { exact: true }).boundingBox();
    expect(help.y + help.height).toBeLessThan(access.y);
  }
  const close = sharing.getByRole('button', { name: 'Close', exact: true });
  const closeBox = await close.boundingBox();
  expect(closeBox.width).toBeGreaterThanOrEqual(40);
  expect(closeBox.height).toBeGreaterThanOrEqual(40);
  await close.click();
  if (testInfo.project.use.viewport.width < 720) {
    const root = await page.getByRole('navigation', { name: 'Folder path' }).boundingBox();
    const trashIcon = await page.getByRole('button', { name: 'Trash', exact: true }).locator('svg').boundingBox();
    expect(Math.abs(trashIcon.x - root.x)).toBeLessThanOrEqual(1);
  }
  expect(await fits(page)).toBe(true);
});

test('the trash has a title and the Account page has the app layout', async ({ page }, testInfo) => {
  await signIn(page);
  await page.reload();
  await expect(page.locator('.ky-user-name')).toBeVisible();
  await expect(page.locator('.ky-user-role')).toBeVisible();
  await expect(page.locator('.ky-user-name')).not.toHaveText(await page.locator('.ky-user-role').textContent());
  for (const control of await page.locator('.icon-btn').all()) {
    const box = await control.boundingBox();
    expect(box.width).toBeGreaterThanOrEqual(40);
    expect(box.height).toBeGreaterThanOrEqual(40);
  }
  const search = page.locator('.drive-search input');
  await expect(search).toBeVisible();
  const fieldContrast = await search.evaluate(input => {
    const canvas = document.createElement('canvas');
    canvas.width = canvas.height = 1;
    const ctx = canvas.getContext('2d');
    const luminance = color => {
      ctx.fillStyle = color;
      ctx.fillRect(0, 0, 1, 1);
      return [...ctx.getImageData(0, 0, 1, 1).data].slice(0, 3)
        .map(c => c / 255).map(c => c <= .04045 ? c / 12.92 : ((c + .055) / 1.055) ** 2.4)
        .reduce((sum, c, i) => sum + c * [.2126, .7152, .0722][i], 0);
    };
    const border = luminance(getComputedStyle(input).borderTopColor);
    return [getComputedStyle(input).backgroundColor, getComputedStyle(document.body).backgroundColor].map(color => {
      const surface = luminance(color);
      return (Math.max(border, surface) + .05) / (Math.min(border, surface) + .05);
    });
  });
  for (const ratio of fieldContrast) expect(ratio).toBeGreaterThanOrEqual(3);
  await expect(page.locator('.app-header').getByLabel('Open files in a new tab')).toHaveCount(0);
  if (testInfo.project.use.viewport.width < 720) await expect(page.getByText('Scroll for more destinations', { exact: true })).toBeVisible();
  if (testInfo.project.use.viewport.width < 720) {
    const destinations = page.locator('.app-nav button');
    await destinations.first().focus();
    for (let index = 0; index < await destinations.count(); index++) {
      await expect(destinations.nth(index)).toBeFocused();
      await expect.poll(() => destinations.nth(index).evaluate(button => {
        const row = button.closest('.app-navs').getBoundingClientRect();
        const box = button.getBoundingClientRect();
        const style = getComputedStyle(button);
        const ring = parseFloat(style.outlineWidth) + parseFloat(style.outlineOffset);
        return box.left - ring >= row.left - 1 && box.right + ring <= row.right + 1;
      })).toBe(true);
      if (index + 1 < await destinations.count()) await page.keyboard.press('Tab');
    }
  }
  // Administration lives in the sidebar, not under the file list.
  await expect(page.locator('.ky-page-head').getByRole('button', { name: 'Upload files', exact: true })).toBeVisible();
  await expect(page.locator('.ky-page-head').getByRole('button', { name: 'New', exact: true })).not.toHaveClass(/btn-secondary/);
  await expect(page.locator('details.drive-admin')).toHaveCount(0);
  const admin = page.getByRole('navigation', { name: 'Administration' });
  for (const name of ['Workspaces', 'Activity', 'Leavers', 'Backup & recovery', 'Settings']) await expect(admin.getByRole('button', { name, exact: true })).toHaveCount(1);
  // One accent-filled primary at a time: "+ New" in the page head.
  const accent = await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--accent').trim());
  expect(await page.locator('main button:visible').evaluateAll((buttons, a) => {
    const probe = document.body.appendChild(document.createElement('i'));
    probe.style.color = a;
    const fill = getComputedStyle(probe).color;
    probe.remove();
    return buttons.filter(b => getComputedStyle(b).backgroundColor === fill).length;
  }, accent)).toBe(1);
  await page.getByRole('button', { name: 'Trash', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Trash', level: 1 })).toBeVisible();
  await page.getByRole('button', { name: 'Back to files' }).click();

  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Account' }).click();
  await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
  await expect(page.getByLabel('Open files in a new tab')).toBeVisible();
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
  await page.getByRole('navigation', { name: 'Administration' }).getByRole('button', { name: 'Workspaces', exact: true }).click();
  await page.getByRole('button', { name: 'New workspace', exact: true }).click();
  const create = page.getByRole('dialog', { name: 'New shared workspace' });
  await create.getByLabel('Name', { exact: true }).fill(name);
  await create.getByRole('button', { name: 'Create workspace' }).click();
  await expect(create.getByText(`A shared workspace named “${name}” already exists.`)).toBeVisible();
  const all = await (await page.request.get('/api/drive/workspaces?admin=true')).json();
  expect(all.filter(w => w.name.toLowerCase() === name.toLowerCase())).toHaveLength(1);
});

test('an administrator renames a shared workspace and deletes it once empty', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  const tag = testInfo.project.name.replace(/\W/g, '');
  const [name, taken, fixed] = [`Mistake ${tag}`, `Taken ${tag}`, `Fixed ${tag}`];
  const created = await page.request.post('/api/drive/workspaces', { headers, data: { name, quota: 1048576 } });
  expect(created.status()).toBe(201);
  const { id } = await created.json();
  expect((await page.request.post('/api/drive/workspaces', { headers, data: { name: taken, quota: 1048576 } })).status()).toBe(201);
  await page.reload();
  await page.getByRole('button', { name, exact: true }).click();
  await page.getByRole('tab', { name: 'Workspace settings' }).click();
  const panel = page.getByRole('region', { name: 'Workspace', exact: true });
  await panel.getByLabel('Name').fill(taken.toLowerCase());
  await panel.getByRole('button', { name: 'Rename' }).click();
  await expect(panel.getByText(`A shared workspace named “${taken.toLowerCase()}” already exists.`)).toBeVisible();
  await panel.getByLabel('Name').fill(fixed);
  await panel.getByRole('button', { name: 'Rename' }).click();
  await expect(page.getByRole('navigation', { name: 'Workspaces' }).getByRole('button', { name: fixed, exact: true })).toBeVisible();

  await panel.getByRole('button', { name: 'Delete workspace' }).click();
  const ask = page.getByRole('dialog', { name: `Delete ${fixed}?` });
  await expect(ask).toBeVisible();
  if (testInfo.project.name === 'light-1280') await page.screenshot({ path: testInfo.outputPath('delete-workspace.png') });
  await ask.getByRole('button', { name: 'Delete workspace' }).click();
  await expect(ask).toHaveCount(0);
  await expect(page.getByRole('navigation', { name: 'Workspaces' }).getByRole('button', { name: fixed, exact: true })).toHaveCount(0);
  await expect(page.getByRole('heading', { level: 1, name: 'My files' })).toBeVisible();
  const all = await (await page.request.get('/api/drive/workspaces?admin=true')).json();
  expect(all.some(w => w.id === id)).toBe(false);
  expect(all.some(w => w.name === taken)).toBe(true);
});

test('the sidebar footer opens the MIT licence with the running version', async ({ page }, testInfo) => {
  await signIn(page);
  await page.reload();
  const link = page.getByRole('button', { name: /^© \d{4} · MIT License$/ });
  await expect(link).toBeVisible();
  expect(await fits(page)).toBe(true);
  await link.click();
  const dialog = page.getByRole('dialog');
  const { version } = await (await page.request.get('/api/auth/me')).json();
  await expect(dialog.locator('.ky-about-version')).toHaveText(`v${version}`);
  await expect(dialog.getByRole('textbox', { name: 'MIT License' })).toHaveValue(/^MIT License\n\nCopyright \(c\) \d{4} Busnes\.app/);
  await page.screenshot({ path: testInfo.outputPath('license.png') });
  await expect(dialog.getByRole('button', { name: 'Close', exact: true })).toHaveCSS('border-top-style', 'solid');
  await dialog.getByRole('button', { name: 'Close' }).click();
  await expect(dialog).toBeHidden();
  await expect(link).toBeFocused();
});

test('checked rows move to the trash together, and the list shows when files changed', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  await page.request.post('/api/drive/personal-workspace', { headers, data: {} });
  const mine = (await (await page.request.get('/api/drive/workspaces')).json()).find(w => w.kind === 'personal');
  const tag = testInfo.project.name.replace(/\W/g, '');
  const names = [`bulk-a-${tag}.txt`, `bulk-b-${tag}.txt`];
  for (const n of names) expect((await page.request.post(`/api/drive/workspaces/${mine.id}/uploads?name=${n}&parent=&revision=0`, { headers, data: 'x' })).status()).toBe(201);
  await page.reload();
  if (testInfo.project.use.viewport.width >= 720) {
    await expect(page.getByRole('columnheader', { name: 'Modified' })).toBeVisible();
    const size = page.getByRole('columnheader', { name: 'Size' });
    await expect(size).toHaveCSS('text-align', 'right');
    // The row menu has no border and appears on hover.
    const row = page.getByRole('row', { name: new RegExp(names[0]) });
    await expect(row.locator('.col-modified time')).toHaveAttribute('title', /\d/);
    const more = row.getByRole('button', { name: `Actions for ${names[0]}` });
    await page.mouse.move(0, 0);
    await expect(more).toHaveCSS('opacity', '0');
    await row.hover();
    await expect(more).toHaveCSS('opacity', '1');
    await expect(more).toHaveCSS('border-top-color', 'rgba(0, 0, 0, 0)');
  }
  else await expect(page.getByRole('button', { name: `Actions for ${names[0]}` })).toHaveCSS('opacity', '1'); // phones cannot hover
  for (const n of names) await page.getByLabel(`Select ${n}`, { exact: true }).check();
  const bar = page.getByRole('region', { name: 'Selected items' });
  await expect(bar).toContainText('2 selected');
  await bar.getByRole('button', { name: 'Move to trash' }).click();
  await expect(bar).toHaveCount(0);
  for (const n of names) await expect(page.getByRole('link', { name: n })).toHaveCount(0);
  const trashed = (await (await page.request.get(`/api/drive/workspaces/${mine.id}/files?trash=true`)).json()).map(f => f.name);
  for (const n of names) expect(trashed).toContain(n);
  expect(await fits(page)).toBe(true);
});
