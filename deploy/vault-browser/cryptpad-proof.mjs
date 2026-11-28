import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { replaceOnce } from './cryptpad-patches.mjs';

// Use only the signed static assets from the prepared fixture. There is no raw relay fallback.
const cryptpad = 'http://localhost:18951';

export async function stageCryptPad(fixture, editor, gateway) {
  const source = await readFile(join(fixture, 'verified-front.mjs'), 'utf8');
  const changes = [
    ["'frame-ancestors '+source", "'frame-ancestors '+source+" + JSON.stringify(' ' + editor)],
    ["main.replace('http:', 'ws:')", JSON.stringify(gateway + ' ' + gateway.replace('https:', 'wss:'))],
    ['return await data(req,res,url);', 'return error(res,404);'],
    ["appendFileSync('asset-misses.log',path+'\\n');", ''],
    ["wss.handleUpgrade(req,socket,head,ws=>wss.emit('connection',ws,req));", 'socket.destroy();'],
    ['origin === sandbox;', 'origin === sandbox || origin === ' + JSON.stringify(editor) + ';'],
    ["const path = decodeURIComponent(url.pathname);", `const path = decodeURIComponent(url.pathname);
      if (path === '/ky-vault/cryptpad-adapter.js' && req.method === 'GET') {
        res.writeHead(200, {...headers(req,path), 'Content-Type':'text/javascript'});
        return res.end(adapter);
      }`],
    ['res.writeHead(200,{...headers(req,path)', 'const output = patchAsset(path, bytes, ' + JSON.stringify(editor) + ');\n      res.writeHead(200,{...headers(req,path)'],
    ["'Content-Length':bytes.length,'X-Verified-Release'", "'Content-Length':output.length,'X-Verified-Release'"],
    ["res.end(req.method === 'HEAD' ? undefined : bytes);", "res.end(req.method === 'HEAD' ? undefined : output);"],
  ];
  const patch = new URL('./cryptpad-patches.mjs', import.meta.url);
  const adapter = new URL('./cryptpad-adapter.js', import.meta.url);
  const fixed = 'import {patchAsset} from ' + JSON.stringify(patch.href) + ';\n' +
    'const adapter = await (await import("node:fs/promises")).readFile(new URL(' + JSON.stringify(adapter.href) + '));\n' +
    changes.reduce((script, change) => replaceOnce(script, ...change), source);
  const front = spawn(process.execPath, ['--input-type=module', '-', 'fixture-release-public.pem'], {
    cwd: fixture, stdio: ['pipe', 'pipe', 'inherit'],
  });
  front.stdin.end(fixed);
  try {
    await new Promise((resolve, reject) => {
      front.once('error', reject);
      front.once('exit', code => reject(Error('CryptPad asset fixture exited (' + code + ')')));
      front.stdout.on('data', bytes => { if (bytes.toString().includes('Verified client release')) resolve(); });
    });
  } catch (error) {
    front.kill();
    throw error;
  }
  return async () => {
    if (front.exitCode !== null || front.signalCode !== null) return;
    front.kill();
    await once(front, 'exit');
  };
}

function sdkFrame(page) {
  const frame = page.frames().find(frame => /\/apps\/documenteditor\/main\//.test(frame.url()));
  assert.ok(frame, 'Office iframe must be open');
  return frame;
}

export async function assertWord(page, marker) {
  await page.bringToFront();
  const sdk = sdkFrame(page);
  await sdk.getByText('Got it', { exact: true }).click({ timeout: 600 }).catch(() => {});
  await sdk.locator('#id_viewer_overlay').click({ position: { x: 400, y: 135 }, timeout: 5000 });
  await assert.doesNotReject(async () => {
    for (let attempt = 0; attempt < 20; attempt++) {
      await page.keyboard.press('Control+a');
      const text = await sdk.evaluate(() => window.Asc.editor.asc_GetSelectedText());
      await page.keyboard.press('ArrowRight');
      if (text.includes(marker)) return;
      await new Promise(resolve => setTimeout(resolve, 200));
    }
    throw Error('Office editor is missing ' + marker);
  });
}

export async function typeWord(page, marker) {
  await page.bringToFront();
  await sdkFrame(page).locator('#id_viewer_overlay').click({ position: { x: 400, y: 135 }, timeout: 5000 });
  await page.keyboard.press('Control+End');
  await page.keyboard.insertText(' ' + marker);
  await sdkFrame(page).evaluate(() => window.Asc.editor.asc_Save());
}

export function checkExport(data, markers) {
  const verify = spawnSync('python3', ['-c', `
import io, sys, zipfile
from xml.etree import ElementTree
with zipfile.ZipFile(io.BytesIO(sys.stdin.buffer.read())) as doc:
    assert doc.testzip() is None
    text = ''.join(ElementTree.fromstring(doc.read('word/document.xml')).itertext())
    assert all(marker in text for marker in sys.argv[1:]), 'Missing expected content'
`, 'Original bake-off document', ...markers], { input: data });
  assert.equal(verify.status, 0, verify.stderr.toString());
}
