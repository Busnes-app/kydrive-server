// Wave 2 suite integration captures (picker, Email a link, KyPost switches) against a disposable
// server, a fake KyIdentity suite directory and an environment KyPost pairing (no KyPost runs).
// Args: <server binary> <output dir> <before|after>. Light and dark, 1440 and 390 wide.
// Edits the disposable database so password sign-in stands in for a directory account.
import { createRequire } from 'node:module';
import { mkdtemp, rm, readdir, writeFile, chmod } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn, execFileSync } from 'node:child_process';
import http from 'node:http';
const require = createRequire(new URL('../../web/package.json', import.meta.url));
const { chromium, request } = require('@playwright/test');
const [bin, out, stage] = process.argv.slice(2);
const PORT = 5486, IDP = 5487, base = `http://127.0.0.1:${PORT}`;
const MAIL = 'https://mail.example.com';

const idp = http.createServer((req, res) => {
  const ok = req.headers.authorization === 'Basic ' + Buffer.from('kydrive:shot-secret').toString('base64');
  if (!req.url.startsWith('/api/suite/apps') || !ok) { res.writeHead(401); res.end('{"error":"invalid_client"}'); return; }
  res.setHeader('content-type', 'application/json');
  res.end(JSON.stringify({ apps: [
    { id: 'kypost', name: 'KyPost', url: MAIL + '/api/auth/oidc/login' },
    { id: 'kycalendar', name: 'KyCalendar', url: 'https://calendar.example.com/sso/login' },
    { id: 'kydrive', name: 'KyDrive', url: 'https://drive.example.com/api/sso/kysignon/login' },
  ] }));
}).listen(IDP, '127.0.0.1');

const dir = await mkdtemp(join(tmpdir(), 'ky-wave2-shot-'));
await writeFile(join(dir, 'pair.secret'), 'p'.repeat(48));
await chmod(join(dir, 'pair.secret'), 0o600);
const server = spawn(bin, [], { cwd: dir, stdio: 'inherit', env: {
  PATH: process.env.PATH, KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: String(PORT), KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KY_BACKUP_DIR: join(dir, 'backups'), KY_ADMIN_PASSWORD: 'ShotInitial123!', KY_CAPTCHA_PROVIDER: 'none',
  KY_KYSIGNON_ISSUER: `http://127.0.0.1:${IDP}`, KY_KYSIGNON_CLIENT_ID: 'kydrive', KY_KYSIGNON_SECRET: 'shot-secret',
  KYDRIVE_KYPOST_URL: MAIL, KYDRIVE_KYPOST_SECRET_FILE: join(dir, 'pair.secret'),
  PUSH_RELAY_URL: '', APNS_RELAY_URL: '',
} });
try {
  for (let i = 0; ; i++) { try { if ((await fetch(base + '/')).ok) break; } catch {} if (i > 100) throw new Error('server did not start'); await new Promise(r => setTimeout(r, 100)); }
  const api = await request.newContext({ baseURL: base });
  await api.get('/');
  await api.post('/api/auth/login', { data: { username: 'admin', password: 'ShotInitial123!' } });
  let csrf = (await api.storageState()).cookies.find(c => c.name === 'ky_csrf').value;
  const ch = await api.post('/api/auth/change-password', { headers: { 'X-CSRF-Token': csrf }, data: { current_password: 'ShotInitial123!', new_password: 'ShotUpdated456!' } });
  if (!ch.ok()) throw new Error('change password ' + ch.status());
  const db = (await readdir(join(dir, 'data'))).find(f => f.endsWith('.db'));
  execFileSync('python3', ['-I', '-c', "import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute(\"UPDATE users SET sso_provider='scim', sso_subject='sub-shot' WHERE username='admin'\"); c.commit()", join(dir, 'data', db)]);
  await api.post('/api/auth/login', { data: { username: 'admin', password: 'ShotUpdated456!' } });
  csrf = (await api.storageState()).cookies.find(c => c.name === 'ky_csrf').value;
  const h = { 'X-CSRF-Token': csrf };
  const home = await (await api.post('/api/drive/personal-workspace', { headers: h, data: {} })).json();
  const ws = home.id ?? home.workspace ?? (await (await api.get('/api/drive/workspaces')).json())[0].id;
  for (const name of ['Q3 report.pdf', 'Budget 2027.xlsx', 'Site photos.zip']) {
    const r = await api.post(`/api/drive/workspaces/${ws}/uploads?name=${encodeURIComponent(name)}&revision=0`, { headers: h, data: Buffer.from('shot ' + name) });
    if (!r.ok()) throw new Error('upload ' + r.status() + ' ' + await r.text());
  }
  await api.put('/api/drive/guest-links-policy', { headers: h, data: { enabled: true, max_days: 14 } });
  await api.put('/api/drive/kypost', { headers: h, data: { enabled: true } });

  const browser = await chromium.launch();
  for (const scheme of ['light', 'dark']) for (const [w, ht] of [[1440, 900], [390, 844]]) {
    const ctx = await browser.newContext({ viewport: { width: w, height: ht }, colorScheme: scheme });
    const r = await ctx.request.post(base + '/api/auth/login', { data: { username: 'admin', password: 'ShotUpdated456!' } });
    if (!r.ok()) throw new Error('login ' + r.status());
    const page = await ctx.newPage();
    const errors = [];
    page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
    const shot = (name) => page.screenshot({ path: `${out}/${stage}-${name}-${scheme}-${w}.png`, fullPage: w < 720 });

    // J: a file's menu, and (after) the Email a link dialog.
    await page.goto(base + '/');
    await page.getByRole('navigation', { name: 'Apps' }).first().waitFor({ state: 'attached' });
    await page.getByRole('button', { name: 'Actions for Q3 report.pdf' }).click();
    await page.waitForTimeout(300);
    await shot('file-menu');
    const email = page.getByRole('menuitem', { name: 'Email a link' });
    if (await email.count()) { await email.click(); await page.getByRole('dialog').waitFor(); await page.waitForTimeout(200); await shot('email-dialog'); }

    // C/I2/K: Settings → Files with KyPost paired and the guest-link policy on.
    await page.goto(base + '/#/settings/files');
    await page.getByLabel('Let KyPost save and share my files').waitFor();
    await page.waitForTimeout(300);
    await shot('settings-files');

    // I: the picker, opened by KyPost, and a refused origin.
    await page.goto(base + '/pick?origin=' + encodeURIComponent(MAIL) + '&mode=link');
    await page.waitForTimeout(800);
    const box = page.getByRole('checkbox', { name: 'Choose Q3 report.pdf' });
    if (await box.count()) await box.check();
    await shot('picker');
    if (stage === 'after') {
      await page.goto(base + '/pick?origin=' + encodeURIComponent('https://evil.example') + '&mode=link');
      await page.getByText(/nothing was shared/).waitFor();
      await shot('picker-refused');
    }
    console.log(`${stage} ${scheme}-${w}: console errors ${errors.length}`, errors.join(' | '));
    await ctx.close();
  }
  await browser.close();
} finally {
  server.kill('SIGTERM'); idp.close();
  await new Promise(r => server.on('exit', r));
  await rm(dir, { recursive: true, force: true });
}
