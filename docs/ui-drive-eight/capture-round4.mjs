// Matched round4 after capture: real disposable server, real login, no source or storage overrides.
// node docs/ui-drive-eight/capture-round4.mjs /tmp/<owned-fixture>/owned-process.json
import { chromium } from '../../web/node_modules/@playwright/test/index.mjs';
import { readFile, writeFile, mkdir, rename } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { dirname, resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { platform, release } from 'node:os';

const out = dirname(fileURLToPath(import.meta.url));
const repo = resolve(out, '../..');
const owned = JSON.parse(await readFile(process.argv[2], 'utf8'));
const credentials = JSON.parse(await readFile(owned.credentials_file, 'utf8'));
const sha = b => createHash('sha256').update(b).digest('hex');
const git = (...args) => execFileSync('git', ['-C', repo, ...args]);
const head = git('rev-parse', 'HEAD').toString().trim();
if (head !== owned.source_commit || git('diff', 'HEAD').length) throw new Error('Source must match the frozen fixture commit');
const manifestPath = join(out, 'manifest-03-after.json');
try { await readFile(manifestPath); throw new Error('Frozen manifest exists; preserve it before any explicit confirmation'); }
catch (e) { if (e.code !== 'ENOENT') throw e; }
await mkdir(join(out, 'screenshots'), { recursive: true });
const browser = await chromium.launch();
const files = [];
const diagnostics = [];
const surfaces = ['login', 'files', 'folder', 'new-folder', 'preview', 'share', 'versions', 'trash', 'appearance', 'account'];
const manifest = {
  stage: '03-after', product: 'KyDrive', source_commit: head,
  branch: git('branch', '--show-current').toString().trim(),
  tracked_source_diff_bytes: 0,
  tracked_source_inventory_sha256: sha(git('ls-files', '-s')),
  server_binary_sha256: sha(await readFile(owned.binary)),
  embedded_index_sha256: sha(await readFile(join(repo, 'web/dist/index.html'))),
  capture_driver_sha256: sha(await readFile(fileURLToPath(import.meta.url))),
  package_lock_sha256: sha(await readFile(join(repo, 'web/package-lock.json'))),
  browser: { engine: 'Chromium', version: browser.version(), mode: 'headless', playwright: JSON.parse(await readFile(join(repo, 'web/node_modules/@playwright/test/package.json'))).version },
  host: `${platform()} ${release()}`, started_at: new Date().toISOString(),
  base_url: owned.base_url, owned_process_record: process.argv[2],
  theme_source: 'Fresh context per viewport/scheme; OS colorScheme emulation; ky_theme absent throughout',
  device_scale_factor: 1, locale: 'en-US', timezone: 'America/New_York',
  service_workers: 'allow (normal registration and lifecycle)', full_page: false,
  fixture: 'Disposable SQLite; actual local bootstrap administrator after mandatory password replacement. No ordinary account: staff requires external OIDC; SCIM supplies no passwords. Real UI created Project materials folder, uploaded Quarterly plan.md, Supplier list.csv, Meeting notes.txt, Archive note.txt, and trashed Archive note.txt. No editor, identity, email, recovery or external service configured. Upload receipts cleared by UI. No simulated roles, API mocks, storage auth injection or CSP/CSRF bypass.',
  missing_unscored: ['Ordinary account shell and task workflows (no real local staff provisioning boundary)', 'Group-granted shared workspace and populated Shared with me (no directory/groups/second account)', 'Euro-Office editing and save (not configured)', 'Live SSO, external guest collaboration, linked computers and recovery integrations (not configured)'],
  surfaces, files, diagnostics,
};
try {
  for (const theme of ['light', 'dark']) for (const [device, viewport] of [['desktop', {width:1280,height:900}], ['mobile', {width:390,height:844}]]) {
    const context = await browser.newContext({baseURL:owned.base_url, viewport, deviceScaleFactor:1, colorScheme:theme, locale:'en-US', timezoneId:'America/New_York', serviceWorkers:'allow'});
    const page = await context.newPage();
    page.on('pageerror', e => diagnostics.push({device, theme, kind:'pageerror', message:e.message}));
    page.on('response', r => { if (r.status() >= 400) diagnostics.push({device,theme,kind:'http',status:r.status(),url:r.url()}); });
    const settle = async () => { await page.evaluate(() => document.fonts.ready); await page.mouse.move(0,0); await page.waitForTimeout(500); };
    const capture = async surface => {
      await settle();
      const conditions = await page.evaluate(async () => ({
        url:location.href, title:document.title, user_agent:navigator.userAgent,
        dpr:devicePixelRatio, viewport:{width:innerWidth,height:innerHeight},
        saved_theme:localStorage.getItem('ky_theme'), data_theme:document.documentElement.getAttribute('data-theme'),
        computed_color_scheme:getComputedStyle(document.documentElement).colorScheme,
        service_worker_controller:navigator.serviceWorker.controller?.scriptURL ?? null,
        service_worker_registrations:(await navigator.serviceWorker.getRegistrations()).map(r=>r.scope),
        overflow_px:document.documentElement.scrollWidth-document.documentElement.clientWidth,
        hovered_buttons:[...document.querySelectorAll('button:hover,a.btn:hover')].map(x=>x.textContent),
        scroll:{x:scrollX,y:scrollY}, headings:[...document.querySelectorAll('h1,h2')].map(x=>x.textContent),
      }));
      if (conditions.saved_theme !== null || conditions.dpr !== 1) throw new Error('Capture condition mismatch');
      const file=`kydrive-03-after-${surface}-${device}-${theme}.png`;
      const png=await page.screenshot({path:join(out,'screenshots',file),fullPage:false});
      files.push({file:`screenshots/${file}`,surface,device,theme,account:surface==='login'?'signed-out':'local administrator',sha256:sha(png),bytes:png.length,width:png.readUInt32BE(16),height:png.readUInt32BE(20),captured_at:new Date().toISOString(),...conditions});
      console.log(file);
    };
    await page.goto('/');
    await page.getByText('Security check verified.',{exact:true}).waitFor();
    await page.evaluate(() => navigator.serviceWorker.ready);
    await capture('login');
    await page.getByLabel('Username',{exact:true}).fill(credentials.username);
    await page.getByLabel('Password',{exact:true}).fill(credentials.password);
    await page.locator('form button[type=submit]').click();
    await page.getByRole('navigation',{name:'Workspaces',exact:true}).waitFor();
    for (const surface of surfaces.slice(1)) {
      await page.goto('/');
      await page.getByRole('button',{name:'Actions for Meeting notes.txt',exact:true}).waitFor();
      if (surface==='folder') await page.getByRole('button',{name:'Project materials',exact:true}).click();
      if (surface==='new-folder') {
        await page.getByRole('button',{name:'New',exact:true}).click();
        await page.getByRole('menuitem',{name:'New folder',exact:true}).click();
        await page.getByLabel('Folder name',{exact:true}).fill('Working drafts');
      }
      if (['preview','share','versions'].includes(surface)) {
        await page.getByRole('button',{name:'Actions for Meeting notes.txt',exact:true}).click();
        await page.getByRole('menuitem',{name:{preview:'Preview',share:'Share',versions:'Version history'}[surface],exact:true}).click();
        await page.getByRole('dialog').waitFor();
        if(surface==='preview') await page.locator('dialog pre').waitFor();
      }
      if(surface==='trash') { await page.getByRole('button',{name:'Trash',exact:true}).click(); await page.getByText('Archive note.txt',{exact:true}).waitFor(); }
      if(['appearance','account'].includes(surface)) {
        const name=surface==='appearance'?'Appearance':'Account';
        await page.getByRole('navigation',{name:'Primary',exact:true}).getByRole('button',{name,exact:true}).click();
        await page.getByRole('heading',{name,level:1,exact:true}).waitFor();
      }
      await capture(surface);
    }
    await context.close();
  }
} finally {
  manifest.finished_at=new Date().toISOString();
  manifest.complete=files.length===40;
  const temporary=manifestPath+'.tmp';
  await writeFile(temporary,JSON.stringify(manifest,null,2)+'\n');
  await rename(temporary,manifestPath);
  await browser.close();
}
