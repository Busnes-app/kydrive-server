// Loaded only by the pinned CryptPad staging integration iframe.
define(['/common/common-hash.js', '/file/file-crypto.js'], function (Hash, FileCrypto) {
  return function openVaultTransport({ gateway, room, token, rooms, blobs }) {
    const service = new URL(gateway);
    if (service.protocol !== 'https:' || service.href !== service.origin + '/' ||
        !/^[a-f0-9]{32}$/.test(room) || !/^[\w-]+\.[\w-]+$/.test(token) ||
        !Array.isArray(rooms) || !Array.isArray(blobs) || rooms.length !== blobs.length ||
        !rooms.length || rooms.length > 6 || !rooms.every(id => /^[a-f0-9]{32}$/.test(id)) ||
        !blobs.every(id => /^[a-f0-9]{48}$/.test(id))) throw Error('Invalid vault editor session');
    const WebSocket = globalThis.WebSocket;
    const sockets = new Set();
    globalThis.WebSocket = class extends WebSocket {
      constructor(url) {
        if (url !== location.origin.replace(/^http/, 'ws') + '/cryptpad_websocket') {
          throw Error('Unexpected CryptPad socket destination');
        }
        super(service.origin.replace(/^https/, 'wss') + '/cryptpad_websocket?room=' + room,
          ['kydrive-vault', 'grant.' + token]);
        sockets.add(this);
        this.addEventListener('close', () => sockets.delete(this), { once: true });
      }
    };
    const checkpoint = (id, options = {}) => fetch(service.origin + '/checkpoint/' + id, {
      ...options, headers: { Authorization: 'Bearer ' + token, ...options.headers },
      credentials: 'omit', cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(15000),
    });
    return {
      rooms,
      async renew(fresh) {
        if (typeof fresh !== 'string' || !/^[\w-]+\.[\w-]+$/.test(fresh)) throw Error('Invalid relay grant');
        token = fresh;
        for (const socket of sockets) {
          if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ grant: fresh }));
          if (socket.readyState === WebSocket.CONNECTING) socket.close();
        }
      },
      async read(id) {
        if (!blobs.includes(id)) throw Error('Checkpoint outside this editor binding');
        const response = await checkpoint(id);
        if (!response.ok) throw Error('Checkpoint unavailable');
        return response.blob();
      },
      async write(number, plaintext) {
        const id = blobs[number - 1];
        if (!id || !(plaintext instanceof Blob) || plaintext.size > (32 << 20) - 8192) {
          throw Error('Checkpoint exceeds the staging limit');
        }
        // CryptPad file encryption starts at nonce zero. Use a fresh key even if
        // two editors race for the same reserved slot or an upload is retried.
        const key = crypto.getRandomValues(new Uint8Array(32));
        const next = FileCrypto.encrypt(new Uint8Array(await plaintext.arrayBuffer()), {}, key);
        const chunks = [];
        await new Promise((resolve, reject) => {
          function collect(error, chunk) {
            if (error) return reject(error);
            if (!chunk) return resolve();
            chunks.push(chunk);
            next(collect);
          }
          next(collect);
        });
        const response = await checkpoint(id, {
          method: 'PUT', headers: { 'Content-Type': 'application/octet-stream' }, body: new Blob(chunks),
        });
        if (response.status !== 201) throw Error('Checkpoint publication refused (' + response.status + ')');
        const hash = Hash.getFileHashFromKeys({
          version: 1, channel: id, keys: { fileKeyStr: key.toBase64() },
        });
        return '/file/#' + hash;
      },
    };
  };
});
