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
  const frame = page.frames().find(frame => /\/apps\/(document|spreadsheet|presentation)editor\/main\//.test(frame.url()));
  assert.ok(frame, 'Office iframe must be open');
  return frame;
}

async function assertWord(page, marker) {
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

async function typeWord(page, marker) {
  await page.bringToFront();
  await sdkFrame(page).locator('#id_viewer_overlay').click({ position: { x: 400, y: 135 }, timeout: 5000 });
  await page.keyboard.press('Control+End');
  await page.keyboard.insertText(' ' + marker);
  await sdkFrame(page).evaluate(() => window.Asc.editor.asc_Save());
}

export async function assertOffice(page, format, marker, editIndex) {
  if (format === 'docx') return assertWord(page, marker);
  await page.bringToFront();
  await sdkFrame(page).waitForFunction(({ format, marker, editIndex }) => {
    if (format === 'xlsx') {
      const sheet = window.AscBuilder?.Cell?.Api.GetActiveSheet();
      return sheet?.GetRange(editIndex === undefined ? 'A2' : 'G' + (10 + editIndex)).GetValue() === marker;
    }
    const slide = window.AscBuilder?.Slide?.Api.GetPresentation().GetSlideByIndex(0);
    return slide?.GetAllShapes().some(shape => {
      const content = shape.GetDocContent();
      return content && Array.from({ length: content.GetElementsCount() },
        (_, i) => content.GetElement(i).GetText()).join('\n').includes(marker);
    });
  }, { format, marker, editIndex }, { timeout: 15000 });
}

export async function editOffice(page, format, marker, editIndex) {
  if (format === 'docx') return typeWord(page, marker);
  await page.bringToFront();
  await sdkFrame(page).evaluate(({ format, marker, editIndex }) => {
    if (format === 'xlsx') {
      window.AscBuilder.Cell.Api.GetActiveSheet().GetRange('G' + (10 + editIndex)).SetValue(marker);
    } else {
      const api = window.AscBuilder.Slide.Api;
      const slide = api.GetPresentation().GetSlideByIndex(0);
      window.AscCommon.History.Create_NewPoint(0);
      const shape = api.CreateShape('rect', 4400000, 480000, api.CreateNoFill(), api.CreateStroke(0, api.CreateNoFill()));
      shape.SetPosition(1900000, 3300000 + editIndex * 650000);
      shape.GetDocContent().GetElement(0).AddText(marker);
      if (!slide.AddObject(shape)) throw Error('Could not add the test shape');
    }
    window.Asc.editor.asc_Save();
  }, { format, marker, editIndex });
}

export function checkExport(data, format, original, markers) {
  const verify = spawnSync('python3', ['-c', `
import io, sys, zipfile
from xml.etree import ElementTree as ET

kind, original, *markers = sys.argv[1:]
with zipfile.ZipFile(io.BytesIO(sys.stdin.buffer.read())) as doc:
    assert doc.testzip() is None
    if kind == 'xlsx':
        ns = {'s': 'http://schemas.openxmlformats.org/spreadsheetml/2006/main'}
        sheet = ET.fromstring(doc.read('xl/worksheets/sheet1.xml'))
        strings = [''.join(item.itertext()) for item in ET.fromstring(doc.read('xl/sharedStrings.xml'))]

        def value_at(ref, worksheet=sheet):
            cell = worksheet.find(f".//s:c[@r='{ref}']", ns)
            value = cell.findtext('s:v', namespaces=ns)
            return strings[int(value)] if cell.get('t') == 's' else value

        assert value_at('A2') == original
        assert value_at('B2') == '1200'
        second_sheet = ET.fromstring(doc.read('xl/worksheets/sheet2.xml'))
        assert value_at('A1', second_sheet) == 'Ünïcode ✓'
        assert [value_at(f'G{row}') for row in range(10, 10 + len(markers))] == markers
    else:
        part = {'docx': 'word/document.xml', 'pptx': 'ppt/slides/slide1.xml'}[kind]
        text = ''.join(ET.fromstring(doc.read(part)).itertext())
        if kind == 'pptx':
            assert 'Ship MCP' in text and 'Extract text' in text
        assert all(marker in text for marker in [original, *markers]), 'Missing expected content'
`, format, original, ...markers], { input: data });
  assert.equal(verify.status, 0, verify.stderr.toString());
}
