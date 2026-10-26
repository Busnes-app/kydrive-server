import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';
import { THEME_CATALOG } from '../src/ky-ui/themes.js';

// The filled danger button's label must read on its fill for every suite palette, as the
// screen shows it: pixels from a screenshot, so hover brightness and focus are included. The
// control is the real "Delete permanently" confirmation for a synthetic trashed folder; the
// dialog is always cancelled.
const LEGACY = ['patina', 'cyber', 'nord', 'paper', 'oled'];
const SUITE = THEME_CATALOG.filter(t => !t.id.startsWith('busnes-') && !LEGACY.includes(t.id)).map(t => t.id);
const sessionFile = 'test-results/file-management-session.json'; // shared: sign-ins are rate limited

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

// The clip stays inside the rounded corners. Fill = the most common pixel; label = the pixel
// that contrasts most with it.
const shot = async (page, button) => {
  const box = await button.boundingBox();
  return page.screenshot({ animations: 'disabled', clip: { x: box.x + 8, y: box.y + 4, width: box.width - 16, height: box.height - 8 } });
};
const contrast = (decoder, png) => decoder.evaluate(async b64 => {
  const img = new Image();
  img.src = `data:image/png;base64,${b64}`;
  await img.decode();
  const canvas = Object.assign(document.createElement('canvas'), { width: img.width, height: img.height });
  const ctx = canvas.getContext('2d');
  ctx.drawImage(img, 0, 0);
  const data = ctx.getImageData(0, 0, img.width, img.height).data;
  const lum = (r, g, b) => [r, g, b].map(v => v / 255).map(v => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4).reduce((s, v, i) => s + v * [0.2126, 0.7152, 0.0722][i], 0);
  const counts = new Map();
  for (let i = 0; i < data.length; i += 4) { const key = data[i] << 16 | data[i + 1] << 8 | data[i + 2]; counts.set(key, (counts.get(key) ?? 0) + 1); }
  const fill = [...counts].sort((a, b) => b[1] - a[1])[0][0];
  const fl = lum(fill >> 16, fill >> 8 & 255, fill & 255);
  let best = 1;
  for (const key of counts.keys()) {
    const l = lum(key >> 16, key >> 8 & 255, key & 255);
    best = Math.max(best, (Math.max(l, fl) + 0.05) / (Math.min(l, fl) + 0.05));
  }
  return +best.toFixed(2);
}, png.toString('base64'));

test('the filled danger button label reads on its fill in every suite palette', async ({ page, context }, testInfo) => {
  test.skip(testInfo.project.use.viewport.width < 1280, 'one width per OS scheme is enough for colour');
  test.setTimeout(120000);
  const headers = await signIn(page);
  expect((await page.request.post('/api/drive/personal-workspace', { headers, data: {} })).ok()).toBe(true);
  const mine = (await (await page.request.get('/api/drive/workspaces')).json()).find(w => w.kind === 'personal');
  const folderName = `Danger ${testInfo.project.name.replace(/\W/g, '')}`;
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/folders`, { headers, data: { parent: '', name: folderName } })).ok()).toBe(true);
  await page.reload();
  await page.getByLabel(`Actions for folder ${folderName}`).click();
  await page.getByRole('menuitem', { name: 'Move to trash' }).click();
  await page.getByRole('button', { name: 'Trash', exact: true }).click();
  await page.getByRole('row', { name: new RegExp(folderName) }).getByRole('button', { name: 'Delete permanently' }).click();
  const dialog = page.getByRole('dialog');
  const button = dialog.getByRole('button', { name: 'Delete permanently' });
  await expect(button).toHaveClass(/btn-danger/);
  const helper = await context.newPage(); // another tab saves the choice; the app applies it live
  const decoder = await context.newPage();
  try {
    await helper.goto('/app-icon.png');
    const choose = async id => {
      await helper.evaluate(id => localStorage.setItem('ky_theme', id), id);
      await expect.poll(() => page.evaluate(() => document.documentElement.dataset.theme)).toBe(id);
    };
    const results = [];
    for (const id of SUITE) {
      await choose(id);
      await button.focus();
      const focus = await contrast(decoder, await shot(page, button));
      await button.hover();
      const hover = await contrast(decoder, await shot(page, button));
      await dialog.getByRole('heading').first().hover();
      await button.blur();
      const rest = await contrast(decoder, await shot(page, button));
      results.push({ id, rest, hover, focus });
    }
    console.log(testInfo.project.name, JSON.stringify(results));
    expect(results.filter(r => Math.min(r.rest, r.hover, r.focus) < 4.5)).toEqual([]);
    // Busnes and the five original base palettes keep their own label colour (polled: the
    // button's colour transition settles after the switch).
    for (const id of ['busnes-light', 'busnes-dark', ...LEGACY]) {
      await choose(id);
      await expect.poll(() => button.evaluate(el => {
        const probe = document.body.appendChild(document.createElement('i'));
        probe.style.color = getComputedStyle(document.documentElement).getPropertyValue('--button-text').trim() || '#ffffff';
        const want = getComputedStyle(probe).color;
        probe.remove();
        return getComputedStyle(el).color === want;
      }), { message: id }).toBe(true);
    }
  } finally {
    await dialog.getByRole('button', { name: 'Cancel' }).click(); // never confirm
    await expect(dialog).toHaveCount(0);
    await helper.close();
    await decoder.close();
  }
});
