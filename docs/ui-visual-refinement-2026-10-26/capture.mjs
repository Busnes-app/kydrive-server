// Visual refinement captures against a disposable KyDrive with synthetic data only.
// From the repository root, after `cd web && npm ci`:
//   node docs/ui-visual-refinement-2026-10-26/capture.mjs <stage nn> <before|after>
// It builds web/dist and the server from this tree, serves fresh SQLite data on loopback
// (captcha none, no editor, directory or recovery), replaces the bootstrap password through
// the real API and uploads three synthetic text files to My files through the Upload input.
import { createHash, randomBytes } from 'node:crypto';
import { createRequire } from 'node:module';
import { connect } from 'node:net';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { release, tmpdir, type } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync, spawn } from 'node:child_process';

const [stage, phase] = process.argv.slice(2);
if (!/^\d\d$/.test(stage ?? '') || !['before', 'after'].includes(phase)) throw new Error('usage: capture.mjs <nn> <before|after>');
const evidenceDir = 'docs/ui-visual-refinement-2026-10-26';
const root = fileURLToPath(new URL('../../', import.meta.url));
const here = fileURLToPath(new URL('.', import.meta.url));
const pw = createRequire(join(root, 'web/package.json'));
const { chromium } = pw('@playwright/test');
const port = process.env.CAPTURE_PORT ?? '5391';
const base = `http://127.0.0.1:${port}`;
const secret = () => `Refine-${randomBytes(18).toString('base64url')}9!`;
const initial = secret();
const password = secret();
const FILES = [
  { name: 'Quarterly plan.md', mimeType: 'text/markdown', buffer: Buffer.from('# Quarterly plan\n\n- Hire one support engineer\n- Ship the new intake form\n') },
  { name: 'Supplier list.csv', mimeType: 'text/csv', buffer: Buffer.from('supplier,category\nNorthwind Paper,office\nContoso Tools,workshop\n') },
  { name: 'Meeting notes.txt', mimeType: 'text/plain', buffer: Buffer.from('Weekly sync: agree the launch checklist and owners.\n') },
];

function sha(bytes) { return createHash('sha256').update(bytes).digest('hex'); }
function git(...args) { return execFileSync('git', ['-C', root, ...args], { maxBuffer: 64 << 20 }); }
// Hash of uncommitted source, excluding this evidence directory.
function worktreeHash() {
  const evidence = `:(exclude)${evidenceDir}`;
  const tracked = git('diff', 'HEAD', '--binary', '--', '.', evidence);
  const untracked = git('ls-files', '--others', '--exclude-standard', '-z', '--', '.', evidence).toString().split('\0').filter(Boolean);
  if (!tracked.length && !untracked.length) return null;
  const h = createHash('sha256').update(tracked);
  for (const file of untracked.sort()) h.update(file).update(execFileSync('cat', [join(root, file)]));
  return h.digest('hex');
}

// Refuse to start when another process holds a port: the run would capture someone else's server.
for (const p of [port]) {
  const busy = await new Promise(r => { const s = connect(+p, '127.0.0.1', () => { s.destroy(); r(true); }); s.on('error', () => r(false)); });
  if (busy) throw new Error(`127.0.0.1:${p} is in use by another process; set CAPTURE_PORT`);
}
const dir = await mkdtemp(join(tmpdir(), 'kydrive-capture-'));
execFileSync('npm', ['run', 'build'], { cwd: join(root, 'web'), stdio: ['ignore', 'ignore', 'inherit'] });
const binary = join(dir, 'kydrive-server');
execFileSync('go', ['build', '-o', binary, './cmd/server'], { cwd: root, stdio: 'inherit' });
const server = spawn(binary, [], {
  cwd: dir, stdio: ['ignore', 'ignore', 'inherit'],
  env: {
    PATH: process.env.PATH, HOME: dir, KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: port,
    KY_DB_DRIVER: 'sqlite', KY_DATA_DIR: join(dir, 'data'), KY_BACKUP_DIR: join(dir, 'backups'),
    KY_ADMIN_PASSWORD: initial, KY_CAPTCHA_PROVIDER: 'none', KY_EXCALIDRAW_FONTS_DIR: join(root, 'web/dist-fonts'),
  },
});
const exited = new Promise(r => server.once('exit', r));
const cleanup = async () => { server.kill('SIGTERM'); await exited; await rm(dir, { recursive: true, force: true }); };

const signIn = async page => {
  await page.goto('/');
  await page.getByLabel('Username').fill('admin');
  await page.getByLabel('Password').fill(password);
  await page.locator('form button[type=submit]').first().click();
  await page.getByRole('navigation', { name: 'Workspaces' }).waitFor();
};
const surfaces = {
  async files(page) {
    await page.goto('/');
    await page.getByRole('navigation', { name: 'Workspaces' }).waitFor();
    await page.getByText(FILES[0].name).first().waitFor();
  },
  async settings(page) {
    await surfaces.files(page);
    await page.getByRole('button', { name: 'Settings & DB' }).click();
    await page.getByRole('heading', { name: 'Settings', level: 1 }).waitFor();
    await page.getByText('Delete trashed items after (days)').waitFor();
  },
};

try {
  for (let i = 0; ; i++) {
    try { if ((await fetch(`${base}/healthz`)).status < 500) break; } catch (e) { if (i > 240) throw e; }
    await new Promise(r => setTimeout(r, 250));
  }
  const login = await fetch(`${base}/api/auth/login`, { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: base }, body: JSON.stringify({ username: 'admin', password: initial }) });
  if (!login.ok) throw new Error(`login ${login.status}`);
  const cookies = login.headers.getSetCookie().map(c => c.split(';')[0]);
  const csrf = cookies.find(c => c.startsWith('ky_csrf='))?.split('=')[1];
  const changed = await fetch(`${base}/api/auth/change-password`, { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: base, Cookie: cookies.join('; '), 'X-CSRF-Token': csrf }, body: JSON.stringify({ current_password: initial, new_password: password }) });
  if (!changed.ok) throw new Error(`change-password ${changed.status}`);

  const browser = await chromium.launch();
  const setup = await browser.newContext({ baseURL: base });
  const seed = await setup.newPage();
  await signIn(seed);
  await seed.getByLabel('Choose files to upload').setInputFiles(FILES);
  for (const f of FILES) await seed.getByRole('link', { name: f.name }).or(seed.getByRole('button', { name: f.name })).first().waitFor();
  const storage = await setup.storageState();
  await setup.close();

  const out = join(here, 'screenshots');
  await mkdir(out, { recursive: true });
  const shots = [];
  let fonts = '';
  for (const theme of ['light', 'dark']) {
    for (const [device, viewport] of [['desktop', { width: 1280, height: 900 }], ['mobile', { width: 390, height: 844 }]]) {
      const ctx = await browser.newContext({ baseURL: base, colorScheme: theme, viewport, deviceScaleFactor: 1, storageState: storage, serviceWorkers: 'block', timezoneId: 'America/Chicago', locale: 'en-US' });
      const page = await ctx.newPage();
      for (const [surface, open] of Object.entries(surfaces)) {
        await open(page);
        await page.evaluate(() => document.fonts.ready);
        fonts ||= await page.evaluate(() => `Space Grotesk loaded: ${document.fonts.check('15px "Space Grotesk"')}`);
        await page.mouse.move(0, 0);
        await page.waitForTimeout(400);
        const file = `kydrive-${stage}-${phase}-${surface}-${device}-${theme}.png`;
        const bytes = await page.screenshot({ path: join(out, file), fullPage: true });
        shots.push({ file, sha256: sha(bytes), surface, device, viewport, theme_source: `OS ${theme} (prefers-color-scheme), no saved choice` });
      }
      await ctx.close();
    }
  }

  // Behaviour checks in the real app, both phases: each records pass/fail, none aborts.
  const checks = [];
  const check = async (name, fn) => {
    try { checks.push({ name, pass: true, detail: (await fn()) ?? '' }); } catch (e) { checks.push({ name, pass: false, detail: String(e.message ?? e).split('\n')[0] }); }
  };
  const errors = [];
  const contextFor = async viewport => {
    const ctx = await browser.newContext({ baseURL: base, colorScheme: 'light', viewport, storageState: storage, timezoneId: 'America/Chicago', locale: 'en-US' });
    ctx.on('page', p => { p.on('console', m => { if (m.type() === 'error') errors.push(m.text()); }); p.on('pageerror', e => errors.push(e.message)); });
    return ctx;
  };
  const phone = await contextFor({ width: 390, height: 844 });
  const pp = await phone.newPage();
  await surfaces.files(pp);
  await check('phone: header height', async () => `${await pp.evaluate(() => Math.round(document.querySelector('.app-header').getBoundingClientRect().height))}px`);
  await check('phone: workspaces, destinations, preference and sign-out stay reachable', async () => {
    const names = ['My files', 'Shared with me', 'Account', 'Appearance', 'Recovery', 'Settings & DB'];
    // Scoped to the header: the file list's breadcrumb also has a My files button.
    for (const name of names) if (!(await pp.locator('.app-header').getByRole('button', { name, exact: true }).isVisible())) throw new Error(`${name} missing`);
    for (const l of [pp.getByLabel('Open files in a new tab'), pp.getByRole('button', { name: 'Sign out' })]) {
      const b = await l.boundingBox();
      if (!b || b.x < 0 || b.x + b.width > 390 || b.y < 0) throw new Error(`${await l.evaluate(el => el.outerHTML.slice(0, 60))} outside the viewport`);
    }
    return `${names.join(', ')}; preference and Sign out in view`;
  });
  await check('phone: choosing the last destination keeps it in view and the page still', async () => {
    await pp.getByRole('button', { name: 'Settings & DB' }).click();
    await pp.getByRole('heading', { name: 'Settings', level: 1 }).waitFor();
    await pp.waitForTimeout(300);
    const r = await pp.evaluate(() => {
      const cur = document.querySelector('.app-header [aria-current="page"]'), i = cur.getBoundingClientRect();
      return { label: cur.textContent.trim(), inView: i.left >= 0 && i.right <= innerWidth, y: scrollY };
    });
    if (!r.inView || r.y !== 0) throw new Error(JSON.stringify(r));
    return JSON.stringify(r);
  });
  const desktop = await contextFor({ width: 1280, height: 900 });
  const dp = await desktop.newPage();
  await surfaces.settings(dp);
  await check('settings: no second theme selector', async () => {
    const n = await dp.getByRole('combobox', { name: 'Color theme' }).count();
    if (n) throw new Error(`${n} theme select on Settings`);
    return 'none';
  });
  await check('settings: the Appearance action opens the one theme picker', async () => {
    await dp.locator('main').getByRole('button', { name: 'Open Appearance' }).click();
    await dp.getByRole('heading', { name: 'Appearance', level: 1 }).waitFor();
    return `radios ${await dp.locator('.ky-appearance-radio').count()}`;
  });
  await check('no console errors or CSP violations', async () => { if (errors.length) throw new Error(errors.join(' | ')); return 'none'; });
  await phone.close();
  await desktop.close();

  const manifest = {
    product: 'KyDrive-server', stage, phase, captured_at: new Date().toISOString(),
    source_commit: git('rev-parse', 'HEAD').toString().trim(), uncommitted_source_sha256: worktreeHash(),
    os: `${type()} ${release()}`, driver: `Playwright ${pw('@playwright/test/package.json').version} Chromium ${browser.version()}, device scale 1, service workers blocked, America/Chicago en-US`,
    fonts: `self-hosted Space Grotesk bundled in web/dist; ${fonts}`,
    server: `web/dist and kydrive-server built from this tree into a temporary directory, on 127.0.0.1:${port}; fresh SQLite data removed after capture`,
    server_binary_sha256: sha(await readFile(binary)),
    fixture: 'synthetic: admin account (bootstrap password replaced through /api/auth/change-password); three text uploads (Quarterly plan.md, Supplier list.csv, Meeting notes.txt) in My files; no shared workspaces, editor, directory or recovery configured',
    shots,
    checks,
  };
  await writeFile(join(here, `manifest-${stage}-${phase}.json`), JSON.stringify(manifest, null, 2) + '\n');
  await browser.close();
  console.log(`${shots.length} captures, ${checks.filter(c => c.pass).length}/${checks.length} checks passed, manifest-${stage}-${phase}.json`);
  for (const c of checks.filter(c => !c.pass)) console.log(`FAIL ${c.name}: ${c.detail}`);
} finally {
  await cleanup();
}
