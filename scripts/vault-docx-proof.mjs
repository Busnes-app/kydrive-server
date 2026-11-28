// Run through TestVaultWitnessAndRevocation with KYDRIVE_DOCX_FIXTURE prepared.
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { readFileSync, renameSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const fixture = await import(pathToFileURL(resolve('browser-fixture.mjs')));
const { chromium } = await import(pathToFileURL(resolve(process.env.OFFICE_PLAYWRIGHT)));
const control = new URL(process.env.KY_VAULT_FIXTURE_CONTROLLER);
assert.ok(control.protocol === 'http:' && ['127.0.0.1', '[::1]'].includes(control.hostname) &&
  control.href === control.origin + '/', 'Expected a loopback fixture controller origin');
const exportsDir = process.env.KY_VAULT_FIXTURE_EXPORTS;
const nonce = randomBytes(5).toString('hex');
const file = 'edit.docx';
const markers = ['KVAULTALICE', 'KVAULTBOB', 'KVAULTSAVED', 'KVAULTFRESH', 'KVAULTDENIED', 'KVAULTLIVE']
  .map(marker => marker + nonce);
const report = event => console.log(JSON.stringify(event));

async function call(method, path, body) {
  const response = await fetch(control.origin + path, { method, body, redirect: 'error', signal: AbortSignal.timeout(10000) });
  if (!response.ok) throw Error(path + ' returned ' + response.status + ' ' + await response.text());
  return response;
}

function captured(start = 0) {
  const rows = readFileSync('relay-boundary.ndjson', 'utf8').slice(start).split('\n').filter(Boolean);
  return rows.flatMap(line => { try { return [JSON.parse(line)]; } catch { return []; } });
}

async function signedRooms(page, link, other) {
  const frames = captured().filter(row => row.kind === 'websocket-in').map(row => JSON.parse(row.text))
    .filter(frame => frame[1] === 'MSG' && /^[a-f0-9]{32,34}$/.test(frame[2]) && typeof frame[3] === 'string');
  return page.mainFrame().evaluate(({ frames, link, other }) => new Promise(resolve => {
    require(['/common/common-hash.js','/components/chainpad-crypto/crypto.js'], (hash, crypto) => {
      const keys = href => {
        const u = new URL(href);
        return hash.getSecrets('doc', u.hash.slice(1)).keys;
      };
      const current = keys(link);
      const read = crypto.createEncryptor(current);
      const old = other && crypto.createEncryptor(keys(other));
      const rooms = new Set();
      let signed = 0, readableWithOldKey = 0;
      for (const frame of frames) {
        const msg = frame[3].replace(/^cp\|[^|]+\|/, '');
        try {
          if (!JSON.parse(read.decrypt(msg, current.validateKey, false))) continue;
        } catch { continue; }
        signed++;
        rooms.add(frame[2]);
        if (old) {
          try { if (JSON.parse(old.decrypt(msg, current.validateKey, false))) readableWithOldKey++; } catch {}
        }
      }
      resolve({ rooms: [...rooms].sort(), signed, readableWithOldKey });
    });
  }), { frames, link, other });
}

function policy(tokens) {
  writeFileSync('vault-room-tokens.next', JSON.stringify(tokens), { mode: 0o600 });
  renameSync('vault-room-tokens.next', 'vault-room-tokens.json');
}

const authority = new URL('./vault-relay-authorize.mjs', import.meta.url).href;
let front = readFileSync('verified-front.mjs', 'utf8');
const hook = 'try { refusal = deniedChannel(packet); }';
assert.ok(front.includes(hook), 'Expected the verified-origin fixture from the editor proof');
front = 'import { authorizeVaultRoom } from ' + JSON.stringify(authority) + ';\n' + front;
front = front.replace("socket.on('message',(packet,binary)=>{", "socket.on('message',async (packet,binary)=>{");
front = front.replace(hook, `try {
      const [,action,channel] = JSON.parse(packet.toString());
      if (existsSync('vault-room-tokens.json') && ['JOIN','MSG'].includes(action)) {
        const token = JSON.parse(readFileSync('vault-room-tokens.json','utf8'))[channel];
        if (token && !await authorizeVaultRoom(process.env.KY_VAULT_RELAY_AUTHORITY, token, channel, action === 'MSG')) {
          refusal = {action, channel};
        }
      }
    }`);
writeFileSync('vault-fixture-front.mjs', front);
policy({});
const origins = spawn(process.execPath, ['vault-fixture-front.mjs', 'fixture-release-public.pem'], { stdio: 'inherit' });
let aliceBrowser, bobBrowser;
const relayName = 'cryptpad-vault-proof-relay';
function workingMiB() {
  const pid = execFileSync('docker', ['inspect', '--format', '{{.State.Pid}}', relayName], { encoding: 'utf8' }).trim();
  const cgroup = readFileSync('/proc/'+pid+'/cgroup', 'utf8').split(':')[2].trim();
  const dir = '/sys/fs/cgroup'+cgroup;
  const inactive = readFileSync(dir+'/memory.stat', 'utf8').match(/^inactive_file (\d+)/m)[1];
  return (Number(readFileSync(dir+'/memory.current'))-Number(inactive))/1048576;
}
let peakRelayMiB = 0;
const sample = setInterval(() => {
  try { peakRelayMiB = Math.max(peakRelayMiB, workingMiB()); } catch {}
}, 500);
try {
  await fixture.until(async () => (await fetch(fixture.cp + '/__verified_release')).ok, 15000);
  [aliceBrowser, bobBrowser] = await Promise.all([chromium.launch(fixture.options), chromium.launch(fixture.options)]);
  const alice = await fixture.editorContext(aliceBrowser);
  const bob = await fixture.editorContext(bobBrowser);
  await Promise.all([fixture.register(alice, 'alice'+nonce), fixture.register(bob, 'bob'+nonce)]);
  const a = (await fixture.cpImport(alice, file, resolve(exportsDir, 'source.docx'))).page;
  const link = await fixture.shareLink(a);
  const b = (await fixture.openOffice(bob, link)).page;
  assert.match(await fixture.readOffice(a, file), /Original bake-off document/);
  await fixture.editOffice(a, file, markers[0], 0);
  await fixture.until(async () => (await fixture.readOffice(b, file)).includes(markers[0]), 25000);
  await fixture.editOffice(b, file, markers[1], 1);
  await fixture.until(async () => (await fixture.readOffice(a, file)).includes(markers[1]), 25000);

  const first = await signedRooms(a, link);
  assert.ok(first.rooms.length >= 2, 'Expected a pad and an Office edit room');
  const oldLease = (await (await call('POST', '/bind', JSON.stringify({ url: link, rooms: first.rooms }))).json()).token;
  const oldTokens = Object.fromEntries(first.rooms.map(room => [room, oldLease]));
  policy(oldTokens);
  await fixture.editOffice(b, file, markers[2], 1);
  await fixture.until(async () => (await fixture.readOffice(a, file)).includes(markers[2]), 25000);

  async function checkpoint(page, stage, expected) {
    await fixture.exportOffice(page, resolve(exportsDir, stage + '.docx'));
    await call('POST', '/save/' + stage);
    const checked = resolve(exportsDir, stage + '-restored.docx');
    execFileSync('python3', ['check-export.py', checked, ...expected]);
    return checked;
  }

  const restored = await checkpoint(a, 'coedited', markers.slice(0,3));
  await a.close();
  await call('POST', '/rotate');
  const offset = readFileSync('relay-boundary.ndjson', 'utf8').length;
  const staleEdit = fixture.editOffice(b, file, markers[4], 1).catch(() => {});
  await fixture.until(() => captured(offset).some(row => row.kind === 'websocket-denied' &&
    row.action === 'MSG' && first.rooms.includes(row.channel)), 20000);
  report({ event: 'connected-collaborator-write-refused', guardedRooms: first.rooms.length });
  await staleEdit;
  await b.close();

  const successor = await fixture.editorContext(aliceBrowser);
  await fixture.register(successor, 'successor'+nonce);
  const fresh = (await fixture.cpImport(successor, file, resolve(restored))).page;
  const freshLink = await fixture.shareLink(fresh);
  assert.notEqual(freshLink, link);
  await fixture.editOffice(fresh, file, markers[3], 0);
  const second = await signedRooms(fresh, freshLink, link);
  assert.ok(second.signed > 0 && second.rooms.length >= 2);
  assert.equal(second.readableWithOldKey, 0);
  assert.ok(second.rooms.every(room => !first.rooms.includes(room)));
  const newLease = (await (await call('POST', '/bind', JSON.stringify({ url: freshLink, rooms: second.rooms }))).json()).token;
  policy({ ...oldTokens, ...Object.fromEntries(second.rooms.map(room => [room, newLease])) });
  const expected = markers.slice(0,4);
  const last = await checkpoint(fresh, 'rotated', expected);
  const output = execFileSync('python3', ['check-export.py', last, ...expected], { encoding: 'utf8' });
  assert.ok(!(await fixture.readOffice(fresh, file)).includes(markers[4]));
  report({ event: 'encrypted-docx-round-trip', revisions: 3, rooms: second.rooms.length,
    newSignedFrames: second.signed, oldKeyDecrypts: second.readableWithOldKey, verified: output.trim() });

  const observerCtx = await fixture.editorContext(bobBrowser);
  await fixture.register(observerCtx, 'observer'+nonce);
  const observer = (await fixture.openOffice(observerCtx, freshLink)).page;
  assert.ok((await fixture.readOffice(observer, file)).includes(markers[3]));
  const beforeLiveEdit = readFileSync('relay-boundary.ndjson', 'utf8').length;
  await fixture.editOffice(fresh, file, markers[5], 0);
  await fixture.until(async () => (await fixture.readOffice(observer, file)).includes(markers[5]), 25000);
  await fixture.until(() => {
    const rows = captured(beforeLiveEdit);
    const writes = rows.filter(row => row.kind === 'websocket-in').flatMap(row => {
      const [seq, type, room] = JSON.parse(row.text);
      return type === 'MSG' && second.rooms.includes(room) ? [[row.connection, seq]] : [];
    });
    return rows.some(row => {
      if (row.kind !== 'websocket-out') return false;
      const [seq, reply] = JSON.parse(row.text);
      return reply === 'ACK' && writes.some(([conn, sent]) => conn === row.connection && seq === sent);
    });
  }, 20000);
  await observerCtx.close();
  await successor.close();
  execFileSync('docker', ['restart', '-t', '10', relayName], { stdio: 'ignore' });
  await fixture.until(async () => (await fetch('http://localhost:18950/api/config')).ok, 45000);
  const renewed = (await (await call('POST', '/renew')).json()).token;
  policy({ ...oldTokens, ...Object.fromEntries(second.rooms.map(room => [room, renewed])) });
  const recovered = await fixture.editorContext(aliceBrowser);
  await fixture.register(recovered, 'recovered'+nonce);
  const reloaded = (await fixture.openOffice(recovered, freshLink)).page;
  await fixture.until(async () => (await fixture.readOffice(reloaded, file)).includes(markers[5]), 25000);
  assert.ok(markers.every(marker => !readFileSync('relay-boundary.ndjson', 'utf8').includes(marker)));
  report({ event: 'live-edit-survived-relay-restart', peakRelayMiB: Math.round(peakRelayMiB * 10) / 10 });
} finally {
  clearInterval(sample);
  await Promise.allSettled([aliceBrowser?.close(), bobBrowser?.close()]);
  origins.kill();
}
