// web/browser/previews.spec.mjs
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';

// Shares the saved session with file-management.spec.mjs to stay under 20 sign-ins a minute.
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

const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64');

test('quick look previews images, text and PDFs, and refuses HTML', async ({ page }, testInfo) => {
  const headers = await signIn(page);
  expect((await page.request.post('/api/drive/personal-workspace', { headers, data: {} })).ok()).toBe(true);
  const mine = (await (await page.request.get('/api/drive/workspaces')).json()).find(w => w.kind === 'personal');
  const tag = testInfo.project.name.replace(/\W/g, '');
  const put = async (name, body) => {
    const r = await page.request.post(`/api/drive/workspaces/${mine.id}/uploads?name=${encodeURIComponent(name)}&parent=&revision=0`, { headers, data: body });
    expect(r.status(), name).toBe(201);
    return (await r.json()).id;
  };
  await put(`dot ${tag}.png`, png);
  await put(`log ${tag}.log`, Buffer.from('first line\n<b>not bold</b>\n'));
  const pdf = await put(`paper ${tag}.pdf`, Buffer.from('%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n'));
  await put(`page ${tag}.html`, Buffer.from('<!DOCTYPE html><script>document.title="ran"</script>'));
  await page.goto('/');

  await page.getByRole('button', { name: `dot ${tag}.png` }).click();
  const dialog = page.getByRole('dialog', { name: `dot ${tag}.png` });
  const img = dialog.getByRole('img', { name: `dot ${tag}.png` });
  await expect(img).toBeVisible();
  expect(await img.evaluate(i => i.complete && i.naturalWidth)).toBe(1);
  await page.screenshot({ path: testInfo.outputPath('image-preview.png') });
  await dialog.getByRole('button', { name: 'Close' }).click();
  await expect(dialog).toHaveCount(0);

  await page.getByRole('button', { name: `log ${tag}.log` }).click();
  await expect(page.getByRole('dialog').locator('pre')).toHaveText('first line\n<b>not bold</b>\n');
  await page.screenshot({ path: testInfo.outputPath('text-preview.png') });
  await page.keyboard.press('Escape');

  await page.getByRole('button', { name: `paper ${tag}.pdf` }).click();
  await expect(page.getByRole('dialog').locator('iframe')).toHaveAttribute('src', `/api/drive/files/${pdf}/preview`);
  await page.keyboard.press('Escape');

  await page.getByRole('button', { name: `page ${tag}.html` }).click();
  await expect(page.getByRole('dialog').getByText('No preview for this file')).toBeVisible();
  await expect(page).not.toHaveTitle('ran');
  await page.keyboard.press('Escape');

  const served = await page.request.get(`/api/drive/files/${pdf}/preview`);
  expect(served.headers()['content-type']).toBe('application/pdf');
  expect(served.headers()['content-security-policy']).toMatch(/^sandbox;/);
  expect(served.headers()['x-content-type-options']).toBe('nosniff');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
