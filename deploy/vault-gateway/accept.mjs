// Invoked by the signed KyDrive fixture in internal/api/vault_gateway_test.go.
import { chromium } from '../../web/node_modules/playwright-core/index.mjs';

const config = {
  gateway: process.env.KY_TEST_GATEWAY, control: process.env.KY_TEST_GATEWAY_CONTROL,
  room: process.env.KY_TEST_GATEWAY_ROOM, blob: process.env.KY_TEST_GATEWAY_BLOB,
  foreignBlob: process.env.KY_TEST_GATEWAY_FOREIGN_BLOB, digest: process.env.KY_TEST_GATEWAY_BLOB_DIGEST,
  edit: process.env.KY_TEST_GATEWAY_EDIT, view: process.env.KY_TEST_GATEWAY_VIEW,
};
const browser = await chromium.launch({
  channel: 'chrome',
  args: ['--ignore-certificate-errors-spki-list=' + process.env.KY_TEST_GATEWAY_PIN],
});
try {
  const page = await browser.newPage();
  await page.goto(config.control);
  const report = await page.evaluate(async ({ gateway, room, blob, foreignBlob, digest, edit, view }) => {
    const check = (value, label) => { if (!value) throw Error(label); };
    async function wait(condition, label) {
      const end = Date.now() + 10_000;
      while (Date.now() < end) {
        if (condition()) return;
        await new Promise(resolve => setTimeout(resolve, 20));
      }
      throw Error('timed out: ' + label);
    }
    const hk = '0123456789abcdef';
    const target = gateway.replace(/^https/, 'wss') + '/cryptpad_websocket?room=';
    const blobURL = id => '/blob/' + id.slice(0, 2) + '/' + id;
    const read = (path, token, options = {}) => {
      const headers = new Headers(options.headers);
      if (token) headers.set('Authorization', 'Bearer ' + token);
      return fetch(gateway + path, { ...options, headers, signal: AbortSignal.timeout(5000) });
    };
    async function open(lease, expect = true, anchor = room) {
      const socket = new WebSocket(target + anchor, ['kydrive-vault', 'grant.' + lease]);
      const frames = [];
      let status;
      socket.addEventListener('message', message => frames.push(JSON.parse(message.data)));
      socket.addEventListener('close', event => { status = event.code; });
      await wait(() => !expect ? status !== undefined : frames.some(frame => frame[2] === 'IDENT'), 'relay identification');
      if (!expect) {
        check(status === 1006 && frames.length === 0, 'denied upgrade reached the relay');
        return;
      }
      check(socket.protocol === 'kydrive-vault', 'gateway did not select its fixed subprotocol');
      return {
        socket, frames,
        send: frame => socket.send(JSON.stringify(frame)),
        refused: async () => {
          await wait(() => status !== undefined, 'connection refusal');
          check(status === 1008, 'expected gateway policy refusal');
        },
      };
    }
    const messages = x => x.frames.filter(frame => frame[1] === hk && frame[2] === 'MSG')
      .map(frame => JSON.parse(frame[4]));

    const editor = await open(edit);
    editor.send([1, 'JOIN', room]);
    await wait(() => editor.frames.some(frame => frame[1] === 'JACK' && frame[2] === room), 'join signed room');
    editor.send([2, 'MSG', hk, JSON.stringify(['GET_HISTORY', room, { txid: 'setup' }])]);
    await wait(() => messages(editor).some(msg => msg.state === 1 && msg.txid === 'setup'), 'initialize authorized history');
    const crypt = btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(96))));
    editor.send([3, 'MSG', room, crypt]);
    await wait(() => editor.frames.some(frame => frame[0] === 3 && frame[1] === 'ACK'), 'authorized room write');

    const reader = await open(view);
    reader.send([1, 'MSG', hk, JSON.stringify(['GET_FULL_HISTORY', room])]);
    await wait(() => messages(reader).some(msg => msg[0] === 'FULL_HISTORY_END') &&
      messages(reader).some(msg => msg[0] === 'FULL_HISTORY' && msg[1].includes(crypt)), 'authorized history read');

    const original = await read(blobURL(blob), view);
    check(original.status === 200 && original.headers.get('cache-control') === 'no-store', 'bound blob response failed');
    const bytes = new Uint8Array(await original.arrayBuffer());
    const sha = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), b => b.toString(16).padStart(2, '0')).join('');
    check(sha === digest, 'bound ciphertext bytes changed');
    const range = await read(blobURL(blob), view, { headers: { Range: 'bytes=0-7' } });
    check(range.status === 206 && (await range.arrayBuffer()).byteLength === 8, 'bound blob range failed');
    check((await read(blobURL(foreignBlob), view)).status === 403, 'foreign blob escaped signed binding');
    check((await read(blobURL(blob), '')).status === 403, 'anonymous file request passed');
    check((await read(blobURL(blob), edit, { method: 'POST' })).status === 405, 'unscoped blob upload passed');
    const cp = '/checkpoint/' + blob;
    const put = body => ({ method: 'PUT', headers: { 'Content-Type': 'application/octet-stream' }, body });
    check((await read(cp, view, put(bytes))).status === 403, 'reader wrote a checkpoint');
    check((await read('/checkpoint/' + foreignBlob, edit, put(bytes))).status === 403, 'foreign checkpoint write passed');
    check((await read(cp, edit, put(bytes))).status === 201, 'bound checkpoint write failed');
    check((await read(cp, edit, put(bytes.slice(1)))).status === 409, 'checkpoint was overwritten');
    const stored = await read(cp, view);
    check(stored.status === 200 && (await stored.arrayBuffer()).byteLength === bytes.length, 'checkpoint read failed');
    const readerWrite = await open(view);
    readerWrite.send([1, 'JOIN', room]);
    await wait(() => readerWrite.frames.some(frame => frame[1] === 'JACK'), 'reader join');
    readerWrite.send([2, 'MSG', room, crypt]);
    await readerWrite.refused();

    const foreign = 'ba000000000000000000000000000000';
    const historyProbe = await open(view);
    historyProbe.send([1, 'MSG', hk, JSON.stringify(['GET_FULL_HISTORY', foreign])]);
    await historyProbe.refused();

    const rpcProbe = await open(edit);
    rpcProbe.send([1, 'MSG', hk, JSON.stringify(['abc', ['IS_NEW_CHANNEL', foreign]])]);
    await rpcProbe.refused();
    const unknownRPC = await open(edit);
    unknownRPC.send([1, 'MSG', hk, JSON.stringify(['abc', ['GET_FILE_SIZE', room]])]);
    await unknownRPC.refused();
    const mismatchedRenewal = await open(view);
    mismatchedRenewal.send({ grant: edit });
    await mismatchedRenewal.refused();
    await open(view, false, foreign);
    check((await fetch('/authority-off', { method: 'POST' })).status === 204, 'authority did not stop');
    await open(edit, false);
    check((await read(blobURL(blob), view)).status === 403, 'blob served with unavailable authority');
    check((await fetch('/authority-on', { method: 'POST' })).status === 204, 'authority did not restart');

    check(editor.socket.readyState === WebSocket.OPEN && reader.socket.readyState === WebSocket.OPEN, 'revocation fixture sockets closed early');
    const revoked = await fetch('/revoke', { method: 'POST' });
    check(revoked.ok, 'revocation did not commit');
    const successor = await revoked.json();
    if (editor.socket.readyState === WebSocket.OPEN) editor.send([4, 'MSG', room, crypt]);
    if (reader.socket.readyState === WebSocket.OPEN) reader.send([2, 'MSG', hk, JSON.stringify(['GET_FULL_HISTORY', room])]);
    await Promise.all([editor.refused(), reader.refused()]);
    await open(edit, false);
    check((await read(blobURL(blob), edit)).status === 403, 'revoked blob request passed');
    check((await read(cp, edit)).status === 403, 'revoked checkpoint read passed');
    check((await read(cp, edit, put(bytes))).status === 403, 'revoked checkpoint write passed');
    check((await read(blobURL(blob), edit, { headers: { Range: 'bytes=0-7' } })).status === 403, 'revoked blob range passed');

    const remaining = await open(successor.token, true, successor.room);
    remaining.send([1, 'JOIN', successor.room]);
    await wait(() => remaining.frames.some(frame => frame[1] === 'JACK'), 'successor room join');
    remaining.socket.close();
    return {
      activeRoomWrite: true, boundHistory: true, boundBlobAndRange: true, immutableCheckpoint: true, scopedRenewal: true,
      crossBindingDenied: true, readerWriteDenied: true, unknownRPCDenied: true,
      openSocketsRevoked: true, reconnectDenied: true, directHTTPDenied: true,
      unavailableAuthorityDenied: true, successorRoom: true,
    };
  }, config);
  console.log(JSON.stringify(report));
} finally {
  await browser.close();
}
