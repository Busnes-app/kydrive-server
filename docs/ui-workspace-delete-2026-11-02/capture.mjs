// Workspace delete before/after captures (issue #62) against a disposable server.
// Args: <server binary> <output prefix> <before|after>.
// The browser server has no group API, so the driver inserts one group holding the admin into
// the disposable database (python3 sqlite3); the grant, folders and files go through the API.
import { createRequire } from 'node:module';
import { mkdtemp, rm, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn, execFileSync } from 'node:child_process';
const require = createRequire(new URL('../../web/package.json', import.meta.url));
const { chromium, request } = require('@playwright/test');
const [bin, out, mode] = process.argv.slice(2);
const PORT = 5484, base = `http://127.0.0.1:${PORT}`;

const dir = await mkdtemp(join(tmpdir(), 'ky-ws-delete-shot-'));
const server = spawn(bin, [], { cwd: dir, stdio: 'inherit', env: {
  PATH: process.env.PATH, KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: String(PORT), KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KY_BACKUP_DIR: join(dir, 'backups'), KY_ADMIN_PASSWORD: 'ShotInitial123!', KY_CAPTCHA_PROVIDER: 'none',
  PUSH_RELAY_URL: '', APNS_RELAY_URL: '',
} });
const seed = `import sqlite3,sys
db=sqlite3.connect(sys.argv[1],timeout=10)
(uid,)=db.execute("SELECT id FROM users WHERE username='admin'").fetchone()
db.execute("INSERT INTO groups(id,display_name,external_id,created_at,updated_at) VALUES('grp_studio','Studio crew','',datetime('now'),datetime('now'))")
db.execute("INSERT INTO group_members(group_id,user_id) VALUES('grp_studio',?)",(uid,))
db.commit()`;
try {
  for (let i = 0; ; i++) { try { if ((await fetch(base + '/')).ok) break; } catch {} if (i > 100) throw new Error('server did not start'); await new Promise(r => setTimeout(r, 100)); }
  const api = await request.newContext({ baseURL: base });
  await api.get('/');
  await api.post('/api/auth/login', { data: { username: 'admin', password: 'ShotInitial123!' } });
  let csrf = (await api.storageState()).cookies.find(c => c.name === 'ky_csrf').value;
  const ch = await api.post('/api/auth/change-password', { headers: { 'X-CSRF-Token': csrf }, data: { current_password: 'ShotInitial123!', new_password: 'ShotUpdated456!' } });
  if (!ch.ok()) throw new Error('change password ' + ch.status());
  await api.post('/api/auth/login', { data: { username: 'admin', password: 'ShotUpdated456!' } });
  csrf = (await api.storageState()).cookies.find(c => c.name === 'ky_csrf').value;
  const headers = { 'X-CSRF-Token': csrf };
  const db = (await readdir(join(dir, 'data'))).find(f => f.endsWith('.db'));
  execFileSync('python3', ['-I', '-c', seed, join(dir, 'data', db)]);
  const ws = async name => { const r = await api.post('/api/drive/workspaces', { headers, data: { name, quota: 1 << 30 } }); if (r.status() !== 201) throw new Error('create ' + r.status()); return (await r.json()).id; };
  const studio = await ws('Studio');
  await ws('Marketing');
  const g = await api.put(`/api/drive/workspaces/${studio}/grants/grp_studio`, { headers, data: { role: 'manager' } });
  if (!g.ok()) throw new Error('grant ' + g.status() + ' ' + await g.text());
  for (const name of ['Briefs', 'Footage']) await api.post(`/api/drive/workspaces/${studio}/folders`, { headers, data: { parent: '', name } });
  for (const [name, body] of [['Shot list.txt', 'Scene 1\nScene 2\n'], ['Budget.csv', 'item,cost\ncamera,1200\n'], ['Notes.txt', 'Call sheet at 8.\n']]) {
    const r = await api.post(`/api/drive/workspaces/${studio}/uploads?name=${encodeURIComponent(name)}&revision=0`, { headers: { ...headers, 'Content-Type': 'application/octet-stream' }, data: Buffer.from(body) });
    if (!r.ok()) throw new Error('upload ' + r.status() + ' ' + await r.text());
  }
  if (mode === 'after') {
    // A second workspace already deleted, so the Deleted workspaces list has something to show.
    const old = await ws('Archive 2025');
    await api.put(`/api/drive/workspaces/${old}/grants/grp_studio`, { headers, data: { role: 'manager' } });
    await api.post(`/api/drive/workspaces/${old}/folders`, { headers, data: { parent: '', name: 'Old' } });
    const d = await api.delete(`/api/drive/workspaces/${old}`, { headers, data: { confirm: 'Archive 2025' } });
    if (!d.ok()) throw new Error('delete ' + d.status() + ' ' + await d.text());
  }

  const browser = await chromium.launch();
  for (const scheme of ['light', 'dark']) for (const [w, h] of [[1440, 900], [390, 844]]) {
    const tag = `${scheme}-${w}`;
    const ctx = await browser.newContext({ viewport: { width: w, height: h }, colorScheme: scheme });
    const r = await ctx.request.post(base + '/api/auth/login', { data: { username: 'admin', password: 'ShotUpdated456!' } });
    if (!r.ok()) throw new Error('login ' + r.status());
    const page = await ctx.newPage();
    const errors = [];
    page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
    await page.goto(base + '/');
    const nav = page.getByRole('navigation', { name: 'Primary' });
    if (!await nav.getByRole('button', { name: 'Studio', exact: true }).isVisible()) await page.getByRole('button', { name: /menu/i }).first().click();
    await nav.getByRole('button', { name: 'Studio', exact: true }).click();
    await page.getByRole('tab', { name: 'Workspace settings' }).click();
    const panel = page.getByRole('region', { name: 'Workspace', exact: true });
    await panel.scrollIntoViewIfNeeded();
    await page.waitForTimeout(300);
    await panel.screenshot({ path: `${out}-panel-${tag}.png` });
    await panel.getByRole('button', { name: 'Delete workspace' }).click();
    const ask = page.getByRole('dialog', { name: 'Delete Studio?' });
    await ask.waitFor();
    if (mode === 'after') await ask.getByLabel(/Type Studio to confirm/).fill('Studio');
    await page.waitForTimeout(300);
    await page.screenshot({ path: `${out}-dialog-${tag}.png` });
    await ask.getByRole('button', { name: 'Delete workspace' }).click();
    await page.waitForTimeout(500);
    if (mode === 'before') {
      await panel.scrollIntoViewIfNeeded();
      await page.screenshot({ path: `${out}-result-${tag}.png`, fullPage: w < 720 });
    } else {
      if (!await nav.getByRole('button', { name: 'Workspaces', exact: true }).isVisible()) await page.getByRole('button', { name: /menu/i }).first().click();
      await nav.getByRole('button', { name: 'Workspaces', exact: true }).click();
      await page.getByRole('heading', { name: 'Deleted workspaces' }).waitFor();
      await page.waitForTimeout(300);
      await page.screenshot({ path: `${out}-result-${tag}.png`, fullPage: true });
      // Restore Studio so the next scheme starts from the same state.
      await page.getByRole('listitem').filter({ hasText: 'Studio' }).getByRole('button', { name: 'Restore' }).click();
      await page.getByRole('dialog', { name: 'Restore Studio?' }).getByRole('button', { name: 'Restore workspace' }).click();
      await page.getByRole('listitem').filter({ hasText: 'Studio' }).waitFor({ state: 'detached' });
    }
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
    console.log(`${tag}: overflow ${overflow}; console errors ${errors.length}`, errors.join(' | '));
    await ctx.close();
  }
  await browser.close();
} finally {
  server.kill('SIGTERM');
  await new Promise(r => server.on('exit', r));
  await rm(dir, { recursive: true, force: true });
}
