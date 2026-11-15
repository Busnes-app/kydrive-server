// Search type filter captures against a disposable server. Args: <server binary> <output dir>
// <before|after>. Light and dark, 1440 and 390 wide. Content search is off, so only names match.
import { createRequire } from 'node:module';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
const require = createRequire(new URL('../../web/package.json', import.meta.url));
const { chromium, request } = require('@playwright/test');
const [bin, out, stage] = process.argv.slice(2);
const PORT = 5488, base = `http://127.0.0.1:${PORT}`;

const dir = await mkdtemp(join(tmpdir(), 'ky-search-type-shot-'));
const server = spawn(bin, [], { cwd: dir, stdio: 'inherit', env: {
  PATH: process.env.PATH, KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: String(PORT), KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KY_BACKUP_DIR: join(dir, 'backups'), KY_ADMIN_PASSWORD: 'ShotInitial123!', KY_CAPTCHA_PROVIDER: 'none',
  KYDRIVE_CONTENT_SEARCH: 'off', PUSH_RELAY_URL: '', APNS_RELAY_URL: '',
} });
try {
  for (let i = 0; ; i++) { try { if ((await fetch(base + '/')).ok) break; } catch {} if (i > 100) throw new Error('server did not start'); await new Promise(r => setTimeout(r, 100)); }
  const api = await request.newContext({ baseURL: base });
  await api.get('/');
  await api.post('/api/auth/login', { data: { username: 'admin', password: 'ShotInitial123!' } });
  const csrf = (await api.storageState()).cookies.find(c => c.name === 'ky_csrf').value;
  const ch = await api.post('/api/auth/change-password', { headers: { 'X-CSRF-Token': csrf }, data: { current_password: 'ShotInitial123!', new_password: 'ShotUpdated456!' } });
  if (!ch.ok()) throw new Error('change password ' + ch.status());
  await api.post('/api/auth/login', { data: { username: 'admin', password: 'ShotUpdated456!' } });
  const h = { 'X-CSRF-Token': (await api.storageState()).cookies.find(c => c.name === 'ky_csrf').value };
  const ws = (await (await api.post('/api/drive/personal-workspace', { headers: h, data: {} })).json()).id;
  for (const name of ['Q3 report.pdf', 'Q3 report notes.docx', 'Report figures.xlsx', 'Annual report 2026.PDF', 'Report photo.jpg']) {
    const r = await api.post(`/api/drive/workspaces/${ws}/uploads?name=${encodeURIComponent(name)}&revision=0`, { headers: h, data: Buffer.from('shot ' + name) });
    if (!r.ok()) throw new Error('upload ' + r.status() + ' ' + await r.text());
  }
  await api.post(`/api/drive/workspaces/${ws}/folders`, { headers: h, data: { name: 'Reports', parent: '' } });

  const browser = await chromium.launch();
  for (const scheme of ['light', 'dark']) for (const [w, ht] of [[1440, 900], [390, 844]]) {
    const ctx = await browser.newContext({ viewport: { width: w, height: ht }, colorScheme: scheme });
    const r = await ctx.request.post(base + '/api/auth/login', { data: { username: 'admin', password: 'ShotUpdated456!' } });
    if (!r.ok()) throw new Error('login ' + r.status());
    const page = await ctx.newPage();
    const errors = [];
    page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
    const shot = (name) => page.screenshot({ path: `${out}/${stage}-${name}-${scheme}-${w}.png` });

    await page.goto(base + '/');
    const box = page.getByLabel('Search files');
    await box.fill('report');
    await page.locator('.drive-search-results li').first().waitFor();
    await page.waitForTimeout(300);
    await shot('results');
    if (stage === 'after') {
      await page.getByRole('button', { name: 'PDFs' }).click();
      await page.waitForTimeout(600);
      await shot('results-pdfs');
      await box.fill('');
      await page.waitForTimeout(600);
      await shot('all-pdfs');
    }
    console.log(`${stage} ${scheme}-${w}: console errors ${errors.length}`, errors.join(' | '));
    await ctx.close();
  }
  await browser.close();
} finally {
  server.kill('SIGTERM');
  await new Promise(r => server.on('exit', r));
  await rm(dir, { recursive: true, force: true });
}
