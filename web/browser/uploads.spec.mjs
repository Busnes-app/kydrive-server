// web/browser/uploads.spec.mjs
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
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

test('upload a folder tree, resume a dropped chunk, search and download the folder as a zip', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  expect((await page.request.post('/api/drive/personal-workspace', { headers, data: {} })).ok()).toBe(true);
  const tag = testInfo.project.name.replace(/\W/g, '');
  const top = `Tree${tag}`;
  const dir = await mkdtemp(join(tmpdir(), 'kydrive-upload-'));
  try {
    await mkdir(join(dir, top, 'sub'), { recursive: true });
    await writeFile(join(dir, top, 'a.txt'), 'alpha');
    await writeFile(join(dir, top, 'sub', `findme-${tag}.txt`), 'bravo');
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'My files' })).toBeVisible();

    await page.getByLabel('Choose folder to upload').setInputFiles(join(dir, top));
    await page.getByRole('button', { name: top, exact: true }).click();
    await expect(page.getByRole('link', { name: 'a.txt' })).toBeVisible();
    await page.getByRole('button', { name: 'sub', exact: true }).click();
    await expect(page.getByRole('link', { name: `findme-${tag}.txt` })).toBeVisible();

    // The first chunk's connection drops; the upload asks the server where it got to and finishes.
    let dropped = false;
    await page.route('**/api/drive/upload-sessions/*', route => {
      if (route.request().method() === 'PUT' && !dropped) { dropped = true; return route.abort('connectionreset'); }
      return route.continue();
    });
    const body = 'x'.repeat(200000);
    await page.locator('.drive-files-panel').evaluate((panel, { name, body }) => {
      const dt = new DataTransfer();
      dt.items.add(new File([body], name));
      for (const type of ['dragover', 'drop']) panel.dispatchEvent(new DragEvent(type, { dataTransfer: dt, bubbles: true, cancelable: true }));
    }, { name: `dropped-${tag}.bin`, body });
    await expect(page.getByRole('button', { name: `dropped-${tag}.bin`, exact: true })).toBeVisible({ timeout: 15000 });
    expect(dropped).toBe(true);
    await page.unroute('**/api/drive/upload-sessions/*');
    const spaces = await (await page.request.get('/api/drive/workspaces')).json();
    const mine = spaces.find(w => w.kind === 'personal');
    const files = await (await page.request.get(`/api/drive/workspaces/${mine.id}/files`)).json();
    const droppedFile = files.find(f => f.name === `dropped-${tag}.bin`);
    expect(droppedFile.size).toBe(body.length);
    expect(await (await page.request.get(`/api/drive/files/${droppedFile.id}/download`)).text()).toBe(body);

    await page.getByRole('navigation', { name: 'Folder path' }).getByRole('button', { name: 'My files' }).click();
    await page.getByLabel('Search files').fill(`dme-${tag}`);
    await page.getByRole('search').getByRole('button', { name: new RegExp(`findme-${tag}\\.txt`) }).click();
    // The current folder is the title, plain text at the end of the path.
    await expect(page.getByRole('navigation', { name: 'Folder path' }).getByRole('heading', { name: 'sub', level: 1 })).toBeVisible();

    await page.getByRole('navigation', { name: 'Folder path' }).getByRole('button', { name: 'My files' }).click();
    await page.getByLabel(`Actions for folder ${top}`).click();
    const download = page.waitForEvent('download');
    await page.getByRole('menuitem', { name: 'Download zip' }).click();
    const zip = await readFile(await (await download).path());
    for (const entry of [`${top}/a.txt`, `${top}/sub/findme-${tag}.txt`, 'alpha', 'bravo']) expect(zip.includes(entry)).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
