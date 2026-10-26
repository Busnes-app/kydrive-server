// KyDrive adapter for capture-core.mjs: the web/browser harness environment on its own
// port (fresh SQLite, captcha none, no editor or directory), an administrator whose initial
// password is replaced through the real API, and three synthetic uploads in My files.
import { randomBytes } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { here, main, waitReady } from './capture-core.mjs';

const repo = join(here(import.meta.url), '../..');
const PORT = '5873';
const baseURL = `http://127.0.0.1:${PORT}`;
const secret = () => `Fleet-${randomBytes(18).toString('base64url')}9!`;
const initial = secret();
const password = secret();
let seeded = false;
const sh = (cmd, args, opts) => { const r = spawnSync(cmd, args, { stdio: ['ignore', 'ignore', 'inherit'], ...opts }); if (r.status !== 0) throw new Error(`${cmd} ${args[0]} exited ${r.status}`); };
const FILES = [
  { name: 'Quarterly plan.md', mimeType: 'text/markdown', buffer: Buffer.from('# Quarterly plan\n\n- Hire one support engineer\n- Ship the new intake form\n') },
  { name: 'Supplier list.csv', mimeType: 'text/csv', buffer: Buffer.from('supplier,category\nNorthwind Paper,office\nContoso Tools,workshop\n') },
  { name: 'Meeting notes.txt', mimeType: 'text/plain', buffer: Buffer.from('Weekly sync: agree the launch checklist and owners.\n') },
];

// Fleet review F7: the real filled "Delete permanently" confirmation for a synthetic file in the
// trash, under a saved light (Sky) or dark (Forest) suite palette. The dialog is never confirmed.
let trashed = false;
async function deleteDialog(page, palette) {
  await page.goto('/');
  await page.evaluate((id) => localStorage.setItem('ky_theme', id), palette);
  await page.reload();
  await page.getByRole('navigation', { name: 'Workspaces' }).waitFor();
  const victim = FILES[2].name;
  if (!trashed) {
    await page.getByLabel(`Actions for ${victim}`).click();
    await page.getByRole('menuitem', { name: 'Move to trash' }).click();
    trashed = true;
  }
  await page.getByRole('button', { name: 'Trash', exact: true }).click();
  await page.getByRole('row', { name: new RegExp(victim) }).getByRole('button', { name: 'Delete permanently' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete permanently' }).waitFor();
  await page.waitForTimeout(300);
}

await main({
  product: 'kydrive',
  repo,
  dist: join(repo, 'web/dist/index.html'),
  fixture: 'web built from this tree; fresh SQLite data dir (web/browser/server.mjs environment on port 5873); captcha none; no editor, directory or recovery configured; KY_ADMIN_PASSWORD bootstrap "admin" replaced through /api/auth/change-password; three synthetic text uploads (Quarterly plan.md, Supplier list.csv, Meeting notes.txt) in My files through the Upload files input',
  async start(dir) {
    sh('npm', ['run', 'build'], { cwd: join(repo, 'web') });
    const binary = join(dir, 'kydrive-server');
    sh('go', ['build', '-o', binary, './cmd/server'], { cwd: repo });
    const env = {
      PATH: process.env.PATH, HOME: dir, KY_APP_URL: baseURL, KY_HOST: '127.0.0.1', KY_PORT: PORT,
      KY_DB_DRIVER: 'sqlite', KY_DATA_DIR: join(dir, 'data'), KY_BACKUP_DIR: join(dir, 'backups'),
      KY_ADMIN_PASSWORD: initial, KY_CAPTCHA_PROVIDER: 'none',
      KY_EXCALIDRAW_FONTS_DIR: join(repo, 'web/dist-fonts'),
    };
    const proc = spawn(binary, [], { cwd: dir, env, stdio: ['ignore', 'ignore', 'inherit'] });
    await waitReady(`${baseURL}/healthz`);
    const login = await fetch(`${baseURL}/api/auth/login`, { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: baseURL }, body: JSON.stringify({ username: 'admin', password: initial }) });
    if (!login.ok) throw new Error(`login ${login.status}`);
    const cookies = login.headers.getSetCookie().map((c) => c.split(';')[0]);
    const csrf = cookies.find((c) => c.startsWith('ky_csrf='))?.split('=')[1];
    const changed = await fetch(`${baseURL}/api/auth/change-password`, { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: baseURL, Cookie: cookies.join('; '), 'X-CSRF-Token': csrf }, body: JSON.stringify({ current_password: initial, new_password: password }) });
    if (!changed.ok) throw new Error(`change-password ${changed.status}`);
    return { baseURL, binary, pid: proc.pid, stop: async () => { proc.kill('SIGTERM'); await new Promise((r) => proc.once('exit', r)); } };
  },
  async signIn(page) {
    await page.goto('/');
    await page.getByLabel('Username').fill('admin');
    await page.getByLabel('Password').fill(password);
    await page.locator('form button[type=submit]').first().click();
    await page.getByRole('navigation', { name: 'Workspaces' }).waitFor();
    if (!seeded) {
      await page.getByLabel('Choose files to upload').setInputFiles(FILES);
      for (const f of FILES) await page.getByRole('link', { name: f.name }).or(page.getByRole('button', { name: f.name })).first().waitFor();
      seeded = true;
    }
  },
  hoverTarget: (page) => page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Account' }),
  surfaces: {
    async files(page, { device }) {
      await page.goto('/');
      await page.getByRole('navigation', { name: 'Workspaces' }).waitFor();
      await page.getByText(FILES[0].name).first().waitFor();
      if (device === 'mobile') await page.waitForTimeout(300);
    },
    async appearance(page) {
      if (!(await page.getByRole('button', { name: 'Appearance' }).count())) throw new Error('no Appearance destination at this revision');
      await page.getByRole('button', { name: 'Appearance' }).click();
      await page.getByRole('heading', { name: 'Appearance', level: 1 }).waitFor();
    },
    'delete-dialog-sky': (page) => deleteDialog(page, 'sky'),
    'delete-dialog-forest': (page) => deleteDialog(page, 'forest'),
    async settings(page) {
      await page.getByRole('button', { name: 'Settings & DB' }).click();
      await page.getByRole('heading', { level: 1 }).first().waitFor();
      await page.waitForTimeout(500);
    },
  },
});
