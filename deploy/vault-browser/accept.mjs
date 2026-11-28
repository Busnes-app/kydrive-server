import assert from 'node:assert/strict';
import { stageCryptPad, assertWord, typeWord, checkExport } from './cryptpad-proof.mjs';
import { chromium } from '../../web/node_modules/playwright-core/index.mjs';

const [drive, editor, witness, gateway, pin, cryptpadFixture] = process.argv.slice(2);
const browser = await chromium.launch({ channel: 'chrome', args: pin ? ['--ignore-certificate-errors-spki-list=' + pin] : [] });
let stopOffice;

async function leaseAndJoin({ drive, gateway, file, invite, create }) {
  const call = (action, data) => {
    const result = JSON.parse(globalThis.kyDriveVault(action, JSON.stringify(data)));
    if (result.error) throw Error(result.error);
    return result.value;
  };
  const api = async (method, path, data) => {
    const body = data === undefined ? undefined : JSON.stringify(data);
    const headers = call('sign', { Origin: drive, Method: method, Path: path, Body: body ? new TextEncoder().encode(body).toBase64() : '' });
    if (body) headers['Content-Type'] = 'application/json';
    const result = await fetch(drive + path, { method, body, headers, credentials: 'omit', redirect: 'error' });
    if (!result.ok) throw Error('Vault request failed (' + result.status + ')');
    return result;
  };
  const path = '/api/vaults/' + invite.vault + '/files/' + file + '/editor';
  if (create) {
    const room = crypto.randomUUID().replaceAll('-', '');
    const binding = call('bindEditor', { File: file, Source: 1, Rooms: [room], EditCapability: crypto.randomUUID() });
    const verified = call('openEditor', { File: file, Binding: binding });
    await api('PUT', path, { binding, checkpoint: verified.checkpoint });
  }
  const binding = await (await api('GET', path)).json();
  const session = call('openEditor', { File: file, Binding: binding });
  const { token } = await (await api('POST', path + '/session', { checkpoint: session.checkpoint, edit: session.edit })).json();
  const socket = new WebSocket(gateway.replace(/^https/, 'wss') + '/cryptpad_websocket?room=' + session.rooms[0], ['kydrive-vault', 'grant.' + token]);
  const frames = [];
  socket.addEventListener('message', event => frames.push(JSON.parse(event.data)));
  globalThis.vaultGatewayProof = { socket, frames, room: session.rooms[0] };
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(Error('Gateway room join timed out')), 10000);
    socket.addEventListener('message', event => {
      const frame = JSON.parse(event.data);
      if (frame[2] === 'IDENT') socket.send(JSON.stringify([1, 'JOIN', session.rooms[0]]));
      if (frame[1] === 'JACK') { clearTimeout(timer); resolve(); }
    });
    socket.addEventListener('close', event => { clearTimeout(timer); reject(Error('Gateway closed (' + event.code + ')')); }, { once: true });
  });
}
try {
  if (cryptpadFixture) stopOffice = await stageCryptPad(cryptpadFixture, editor, gateway);
  const context = await browser.newContext();
  const page = await context.newPage();
  const passphrase = 'qa-test-vault-passphrase';
  const status = page.getByRole('status');
  const link = async (profile, tab, username) => {
    const login = await profile.request.post(drive + '/api/auth/login', {
      data: { username, password: 'SuperSecretPass123!' },
    });
    assert.equal(login.status(), 200);
    await tab.goto(editor);
    await tab.getByRole('button', { name: 'Link this browser' }).click();
    await tab.getByRole('heading', { name: 'Link a vault browser' }).waitFor();
    await tab.getByRole('button', { name: 'Approve', exact: true }).click();
    await tab.waitForURL(editor + '/');
    await tab.getByLabel('Vault passphrase').fill(passphrase);
    await tab.getByRole('button', { name: 'Create vault key' }).click();
    await tab.getByRole('status').filter({ hasText: 'Vault key ready' }).waitFor();
  };
  await link(context, page, 'vaultbrowser');
  const upload = page.waitForRequest(r => r.method() === 'POST' && r.url().startsWith(drive + '/api/vaults/'));
  await page.getByRole('button', { name: 'Create personal vault', exact: true }).click();
  const record = (await upload).postDataJSON();
  for (const ciphertext of [record.state.name_index, record.state.members[0].wrap]) {
    assert.match(Buffer.from(ciphertext, 'base64').toString('ascii', 0, 21), /^age-encryption.org\/v1/);
  }
  await status.filter({ hasText: 'Verified and opened personal vault' }).waitFor();

  await page.reload();
  await page.getByLabel('Vault passphrase').fill('wrong-test-passphrase');
  await page.getByRole('button', { name: 'Unlock vault key' }).click();
  await status.filter({ hasText: 'Could not unlock' }).waitFor();
  await page.getByLabel('Vault passphrase').fill(passphrase);
  await page.getByRole('button', { name: 'Unlock vault key' }).click();
  await status.filter({ hasText: 'Vault key ready' }).waitFor();
  await page.getByRole('button', { name: 'Open last vault' }).click();
  await status.filter({ hasText: 'Verified and opened personal vault' }).waitFor();

  await page.route(witness + '/v1/logs/**', async route => {
    const response = await route.fetch();
    const checkpoint = await response.json();
    checkpoint.head.hash = '0'.repeat(64);
    await route.fulfill({ response, json: checkpoint });
  });
  await page.getByRole('button', { name: 'Open last vault' }).click();
  await status.filter({ hasText: 'The vault witness checkpoint did not verify' }).waitFor();
  await page.unroute(witness + '/v1/logs/**');
  await page.getByRole('button', { name: 'Open last vault' }).click();
  await status.filter({ hasText: 'Verified and opened personal vault' }).waitFor();

  const guestContext = await browser.newContext();
  const guest = await guestContext.newPage();
  const guestStatus = guest.getByRole('status');
  await link(guestContext, guest, 'vaultguest');
  const [user, fingerprint] = (await guest.locator('#identity').textContent()).split(' · ');
  await page.getByLabel('Member account ID').fill(user);
  await page.getByLabel('Independently verified member fingerprint').fill('0'.repeat(64));
  await page.getByRole('button', { name: 'Add editor' }).click();
  await status.filter({ hasText: 'The recipient fingerprint does not match' }).waitFor();
  await page.getByLabel('Independently verified member fingerprint').fill(fingerprint);
  await page.getByRole('button', { name: 'Add editor' }).click();
  await status.filter({ hasText: 'Verified and opened shared vault (generation 1)' }).waitFor();

  const invite = await page.getByLabel('Invitation to share independently').inputValue();
  const wrongInvite = JSON.parse(invite);
  wrongInvite.owner.fingerprint = '0'.repeat(64);
  await guest.getByLabel('Verified vault invitation').fill(JSON.stringify(wrongInvite));
  await guest.getByRole('button', { name: 'Open shared vault' }).click();
  await guestStatus.filter({ hasText: 'The vault owner fingerprint does not match' }).waitFor();
  await guest.getByLabel('Verified vault invitation').fill(invite);
  await guest.getByRole('button', { name: 'Open shared vault' }).click();
  await guestStatus.filter({ hasText: 'Verified and opened shared vault (generation 1)' }).waitFor();
  const save = async (tab, source, revision, filename = 'test.txt') => {
    await tab.getByLabel('Revision', { exact: true }).fill(String(revision));
    await tab.getByLabel('File to encrypt').setInputFiles({ name: filename, mimeType: 'application/octet-stream', buffer: Buffer.from(source) });
    const upload = tab.waitForRequest(r => r.method() === 'PUT' && r.url().includes('/versions/' + revision));
    await tab.getByRole('button', { name: 'Save encrypted revision' }).click();
    const bytes = (await upload).postDataBuffer();
    assert.ok(!bytes.includes(Buffer.from(source)));
    assert.match(bytes.toString('ascii', 0, 21), /^age-encryption.org\/v1/);
    await tab.getByRole('status').filter({ hasText: 'Saved encrypted revision ' + revision }).waitFor();
  };
  const read = async (tab, revision) => {
    await tab.getByLabel('Revision', { exact: true }).fill(String(revision));
    await tab.getByRole('button', { name: 'Open encrypted revision' }).click();
    await tab.getByRole('status').filter({ hasText: 'Verified encrypted revision ' + revision }).waitFor();
    const blob = await tab.getByRole('link', { name: 'Download verified file' }).getAttribute('href');
    return tab.evaluate(async url => (await fetch(url)).text(), blob);
  };
  await save(page, 'synthetic shared vault revision', 1);
  const file = await page.getByLabel('File ID').inputValue();
  await guest.getByLabel('File ID').fill(file);
  assert.equal(await read(guest, 1), 'synthetic shared vault revision');
  if (cryptpadFixture) {
    const { readFile } = await import('node:fs/promises');
    await page.getByLabel('File ID').fill('');
    const source = await readFile(cryptpadFixture + '/fixtures/edit.docx');
    await save(page, source, 1, 'edit.docx');
    const docx = await page.getByLabel('File ID').inputValue();
    await read(page, 1);
    await guest.getByLabel('File ID').fill(docx);
    await read(guest, 1);
    const restored = await guest.getByRole('link', { name: 'Download verified file' }).getAttribute('href');
    const bytes = await guest.evaluate(async url => new Uint8Array(await (await fetch(url)).arrayBuffer()).toBase64(), restored);
    assert.deepEqual(Buffer.from(bytes, 'base64'), source);
    const relayEvents = new Map([[page, []], [guest, []]]);
    for (const tab of [page, guest]) {
      const events = relayEvents.get(tab);
      const log = message => {
        if (events.length >= 45) events.shift();
        events.push([Date.now(), message]);
      };
      tab.on('response', response => {
        if (response.url().endsWith('/editor/session')) log('grant ' + response.status());
      });
      tab.on('websocket', socket => {
        if (new URL(socket.url()).origin !== gateway.replace(/^https/, 'wss')) return;
        socket.on('close', () => log('socket closed'));
        socket.on('framesent', ({ payload }) => {
          if (typeof payload !== 'string') return;
          const frame = JSON.parse(payload);
          log(Array.isArray(frame) ? 'sent ' + frame[1] : 'sent renewal');
        });
        socket.on('framereceived', ({ payload }) => {
          if (typeof payload !== 'string') return;
          const [, reply, type] = JSON.parse(payload);
          log(['ACK', 'JACK', 'ERROR'].includes(reply) ? reply : 'received ' + type);
        });
      });
    }
    const openDOCX = async tab => {
      const problems = [];
      tab.on('pageerror', error => { if (problems.length < 10) problems.push(error.message.slice(0, 200)); });
      tab.on('websocket', socket => {
        if (new URL(socket.url()).origin !== gateway.replace(/^https/, 'wss')) return;
        const log = event => { if (problems.length < 20) problems.push(event); };
        log('gateway socket created');
        socket.on('socketerror', error => log(error.split('Unexpected response code: ').at(1) ?? 'gateway socket error'));
        socket.on('framereceived', frame => {
          if (typeof frame.payload !== 'string') return;
          const [, reply, type] = JSON.parse(frame.payload);
          log('gateway received ' + (['ACK', 'JACK', 'ERROR'].includes(reply) ? reply : type));
        });
      });
      tab.on('response', response => {
        const url = new URL(response.url());
        if (response.status() >= 400 && problems.length < 20) problems.push([url.origin, url.pathname.split('/').slice(0, 3).join('/'), response.status()]);
      });
      await tab.getByRole('button', { name: 'Edit DOCX together' }).click();
      try {
        await tab.getByRole('status').filter({ hasText: 'DOCX editor ready' }).waitFor({ timeout: 35000 });
      } catch (error) {
        throw Error('DOCX startup failed: ' + await tab.getByRole('status').textContent() + ' ' + JSON.stringify(problems), { cause: error });
      }
    };
    const firstLease = page.waitForResponse(response => response.request().method() === 'POST' &&
      response.url().endsWith('/editor/session') && response.ok());
    await openDOCX(page);
    const { expires_ms: firstExpiry } = await (await firstLease).json();
    await openDOCX(guest);
    await assertWord(guest, 'Original bake-off document');
    await typeWord(page, 'owner changes');
    await assertWord(guest, 'owner changes');
    await typeWord(guest, 'collaborator changes');
    await assertWord(page, 'collaborator changes');
    await guest.getByRole('button', { name: 'Close editor' }).click();
    await openDOCX(guest);
    await assertWord(guest, 'collaborator changes');
    await page.waitForFunction(expiry => Date.now() > expiry, firstExpiry, { timeout: 65000 });
    try {
      await typeWord(page, 'change after lease renewal');
      await assertWord(page, 'change after lease renewal');
      await assertWord(guest, 'change after lease renewal');
    } catch (error) {
      console.log('Relay activity after initial expiry', firstExpiry,
        await status.textContent(), await guestStatus.textContent(), JSON.stringify([...relayEvents.values()]));
      throw error;
    }
    await page.getByRole('button', { name: 'Save encrypted DOCX' }).click();
    await status.filter({ hasText: 'Saved encrypted DOCX revision 2' }).waitFor();
    await read(page, 2);
    const output = await page.getByRole('link', { name: 'Download verified file' }).getAttribute('href');
    const result = await page.evaluate(async url => new Uint8Array(await (await fetch(url)).arrayBuffer()).toBase64(), output);
    checkExport(Buffer.from(result, 'base64'), ['owner changes', 'collaborator changes', 'change after lease renewal']);
    console.log('Two linked browsers edited a DOCX, reopened history, edited beyond the first lease, and verified the encrypted export');
    await guest.getByLabel('File ID').fill(file);
    await guest.getByLabel('Revision', { exact: true }).fill('1');
    await page.getByLabel('File ID').fill(file);
  }
  if (gateway) {
    const roomRequest = { drive, gateway, file, invite: JSON.parse(invite) };
    await page.evaluate(leaseAndJoin, { ...roomRequest, create: true });
    await guest.evaluate(leaseAndJoin, roomRequest);
    const ciphertext = await page.evaluate(() => {
      const { socket, room } = globalThis.vaultGatewayProof;
      const encrypted = crypto.getRandomValues(new Uint8Array(96)).toBase64();
      socket.send(JSON.stringify([2, 'MSG', room, encrypted]));
      return encrypted;
    });
    await guest.waitForFunction(value => globalThis.vaultGatewayProof.frames.some(frame => frame[2] === 'MSG' && frame[4] === value), ciphertext);
  }
  const removed = page.waitForRequest(r => r.method() === 'POST' && r.url().endsWith('/records'));
  await page.getByRole('button', { name: 'Remove member' }).click();
  const rotated = (await removed).postDataJSON();
  assert.equal(rotated.state.generation, 2);
  assert.notEqual(rotated.state.recipient, record.state.recipient);
  assert.deepEqual(rotated.state.members.map(member => member.user), ['usr_vaultbrowser']);
  await status.filter({ hasText: 'Verified and opened personal vault (generation 2)' }).waitFor();
  if (gateway) {
    const closed = await guest.evaluate(() => new Promise((resolve, reject) => {
      const { socket, room } = globalThis.vaultGatewayProof;
      const timer = setTimeout(() => reject(Error('Revoked gateway socket stayed open')), 10000);
      socket.addEventListener('close', event => { clearTimeout(timer); resolve(event.code); }, { once: true });
      socket.send(JSON.stringify([3, 'MSG', room, crypto.getRandomValues(new Uint8Array(32)).toBase64()]));
    }));
    assert.equal(closed, 1008);
    console.log('Two browsers opened signed room sessions and the gateway closed the removed member’s socket');
  }
  if (cryptpadFixture) {
    await guestStatus.filter({ hasText: 'Vault editor access ended' }).waitFor({ timeout: 35000 });
    assert.equal(await guest.getByTitle('CryptPad DOCX editor').count(), 0);
    console.log('An open DOCX editor closed when its witnessed grant could no longer renew');
  }
  await save(page, 'synthetic revision after removal', 2);
  assert.equal(await read(page, 1), 'synthetic shared vault revision');
  assert.equal(await read(page, 2), 'synthetic revision after removal');
  await guest.getByLabel('Revision', { exact: true }).fill('2');
  await guest.getByRole('button', { name: 'Open encrypted revision' }).click();
  await guestStatus.filter({ hasText: 'Request refused (404)' }).waitFor();

  // Set up a history longer than one server page without routing or mocking any responses.
  const invitation = JSON.parse(invite);
  const path = '/api/vaults/' + invitation.vault + '/records';
  const generation = await page.evaluate(async ({ drive, witness, path, invitation, user, fingerprint }) => {
    const wasm = (action, data) => {
      const result = JSON.parse(globalThis.kyDriveVault(action, JSON.stringify(data)));
      if (result.error) throw Error(result.error);
      return result.value;
    };
    const json = async response => {
      if (!response.ok) throw Error('History fixture request failed (' + response.status + ')');
      return response.json();
    };
    const api = (method, uri, payload) => {
      const body = payload === undefined ? undefined : JSON.stringify(payload);
      const headers = wasm('sign', { Origin: drive, Method: method, Path: uri,
        Body: body ? new TextEncoder().encode(body).toBase64() : '' });
      if (body) headers['Content-Type'] = 'application/json';
      return fetch(drive + uri, { method, body, headers, credentials: 'omit', redirect: 'error' }).then(json);
    };
    const { records, head } = await api('GET', path);
    const { witnessKey } = await fetch('/config.json').then(json);
    const keys = Object.fromEntries(await Promise.all([invitation.owner.user, user].map(async id => [
      id, await api('GET', '/api/vault-keys/' + id),
    ])));
    let action = 'add';
    while (records.length < 33) {
      const next = wasm('changeMembership', { Action: action, Target: { user, fingerprint }, Key: keys[user] });
      const receipt = await api('POST', path, next);
      const checkpoint = await fetch(witness + '/v1/logs/' + head.log, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify([receipt]),
      }).then(json);
      records.push(next);
      wasm('openVault', { Invite: invitation, Records: records, Keys: keys, WitnessKey: witnessKey, Checkpoint: checkpoint });
      action = action === 'add' ? 'remove' : 'add';
    }
    return records.at(-1).state.generation;
  }, { drive, witness, path, invitation, user, fingerprint });
  const paginated = page.waitForResponse(response =>
    response.request().method() === 'GET' && response.url() === drive + path + '?after=32');
  await page.getByRole('button', { name: 'Open last vault' }).click();
  const secondPage = await paginated;
  assert.equal(secondPage.status(), 200);
  assert.deepEqual((await secondPage.json()).records.map(record => record.sequence), [33]);
  await status.filter({ hasText: 'Verified and opened personal vault (generation ' + generation + ')' }).waitFor();
  console.log('Linked browser verified and opened all 33 records across two signed history pages');
} finally {
  await browser.close();
  await stopOffice?.();
}
