// App rail before/after captures against a disposable server and a fake KyIdentity suite
// directory. Args: <server binary> <output prefix> <1 to mark admin as a directory account>.
// Marking edits the disposable database: password sign-in stands in for SSO sign-in.
import { createRequire } from 'node:module';
import { mkdtemp, rm, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn, execFileSync } from 'node:child_process';
import http from 'node:http';
const require = createRequire(new URL('../../web/package.json', import.meta.url));
const { chromium, request } = require('@playwright/test');
const [bin, out, mark] = process.argv.slice(2);
const PORT = 5480, IDP = 5482, base = `http://127.0.0.1:${PORT}`;

const idp = http.createServer((req, res) => {
  const ok = req.headers.authorization === 'Basic ' + Buffer.from('kydrive:shot-secret').toString('base64');
  if (!req.url.startsWith('/api/suite/apps') || !ok) { res.writeHead(401); res.end('{"error":"invalid_client"}'); return; }
  res.setHeader('content-type', 'application/json');
  res.end(JSON.stringify({ apps: [
    { id: 'kyidentity', name: 'KyIdentity', url: 'https://id.example.com/' },
    { id: 'kypost', name: 'KyPost', url: 'https://mail.example.com/api/auth/oidc/login' },
    { id: 'kycalendar', name: 'KyCalendar', url: 'https://calendar.example.com/sso/login' },
    { id: 'kydrive', name: 'KyDrive', url: 'https://drive.example.com/api/sso/kysignon/login' },
    { id: 'kynotes', name: 'KyNotes', url: 'https://notes.example.com/sso/login' },
  ] }));
}).listen(IDP, '127.0.0.1');

const dir = await mkdtemp(join(tmpdir(), 'ky-rail-shot-'));
const server = spawn(bin, [], { cwd: dir, stdio: 'inherit', env: {
  PATH: process.env.PATH, KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: String(PORT), KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KY_BACKUP_DIR: join(dir, 'backups'), KY_ADMIN_PASSWORD: 'ShotInitial123!', KY_CAPTCHA_PROVIDER: 'none',
  KY_KYSIGNON_ISSUER: `http://127.0.0.1:${IDP}`, KY_KYSIGNON_CLIENT_ID: 'kydrive', KY_KYSIGNON_SECRET: 'shot-secret',
  PUSH_RELAY_URL: '', APNS_RELAY_URL: '',
} });
try {
  for (let i = 0; ; i++) { try { if ((await fetch(base + '/')).ok) break; } catch {} if (i > 100) throw new Error('server did not start'); await new Promise(r => setTimeout(r, 100)); }
  const browser = await chromium.launch();
  const api = await request.newContext({ baseURL: base });
  await api.get('/');
  await api.post('/api/auth/login', { data: { username: 'admin', password: 'ShotInitial123!' } });
  const csrf = (await api.storageState()).cookies.find(c => c.name === 'ky_csrf').value;
  const ch = await api.post('/api/auth/change-password', { headers: { 'X-CSRF-Token': csrf }, data: { current_password: 'ShotInitial123!', new_password: 'ShotUpdated456!' } });
  if (!ch.ok()) throw new Error('change password ' + ch.status());
  if (mark === '1') {
    const db = (await readdir(join(dir, 'data'))).find(f => f.endsWith('.db'));
    execFileSync('sqlite3', [join(dir, 'data', db), "UPDATE users SET sso_provider='scim', sso_subject='sub-shot' WHERE username='admin'"]);
  }
  for (const scheme of ['light', 'dark']) for (const [w, h] of [[1440, 900], [390, 844]]) {
    const ctx = await browser.newContext({ viewport: { width: w, height: h }, colorScheme: scheme });
    const r = await ctx.request.post(base + '/api/auth/login', { data: { username: 'admin', password: 'ShotUpdated456!' } });
    if (!r.ok()) throw new Error('login ' + r.status());
    const page = await ctx.newPage();
    const errors = [];
    page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
    await page.goto(base + '/');
    await page.getByRole('button', { name: 'Shared with me' }).first().waitFor();
    if (mark === '1') await page.getByRole('navigation', { name: 'Apps' }).first().waitFor({ state: 'attached' });
    await page.waitForTimeout(500);
    const visible = await page.locator('nav.ky-app-rail').evaluateAll(ns => ns.map(n => n.checkVisibility()));
    console.log(`${scheme}-${w}: rails visible ${JSON.stringify(visible)}; console errors ${errors.length}`, errors.join(' | '));
    await page.screenshot({ path: `${out}-${scheme}-${w}.png`, fullPage: w < 720 });
    if (w >= 720) await page.locator('.ky-sidebar-foot').screenshot({ path: `${out}-${scheme}-${w}-foot.png` });
    await ctx.close();
  }
  await browser.close();
} finally {
  server.kill('SIGTERM'); idp.close();
  await new Promise(r => server.on('exit', r));
  await rm(dir, { recursive: true, force: true });
}
