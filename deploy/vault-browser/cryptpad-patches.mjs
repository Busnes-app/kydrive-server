// Source adapters for the verified CryptPad 2026.10.1 staging bundle.
// Exact matches make a CryptPad upgrade fail closed until its flow is reviewed.
export function replaceOnce(source, from, to) {
  const first = source.indexOf(from);
  if (first < 0 || source.indexOf(from, first + from.length) >= 0) {
    throw Error('Pinned CryptPad asset changed near ' + from.slice(0, 70));
  }
  return source.slice(0, first) + to + source.slice(first + from.length);
}

export function patchAsset(path, bytes, editor) {
  const source = bytes.toString();
  const patch = (...changes) => Buffer.from(changes.reduce(
    (script, [from, to]) => replaceOnce(script, from, to), source));
  switch (path) {
    case '/api/config':
      return patch(['"enableEmbedding": false', '"enableEmbedding": true']);
    case '/integration/main.js':
      return patch(
        ["'/common/common-util.js',", "'/common/common-util.js',\n    '/ky-vault/cryptpad-adapter.js',"],
        ['function (Config, SCO, Hash, Util)', 'function (Config, SCO, Hash, Util, openVaultTransport)'],
        ['var p = window.parent;', 'var p = window.parent;\n        const vaultParent = ' + JSON.stringify(editor) + ';'],
        ["{ q: 'INTEGRATION_READY', txid: txid }, '*'", "{ q: 'INTEGRATION_READY', txid: txid }, vaultParent"],
        ["{ ack: txid, args: args}, '*'", "{ ack: txid, args: args}, vaultParent"],
        ["}, txid: txid}, '*'", "}, txid: txid}, vaultParent"],
        ['if (ev.source !== p)', 'if (ev.source !== p || ev.origin !== vaultParent)'],
        ['var chan = makeChan();', `var chan = makeChan();
        let transport;
        chan.on('PREPARE_VAULT', (_, cb) => {
            const hash = Hash.createRandomHash('integration');
            const room = Hash.getSecrets('integration', hash).channel;
            const rooms = Array.from({length: 3}, () => Hash.createChannelId());
            const blobs = rooms.map(() => crypto.getRandomValues(new Uint8Array(24)).toHex());
            cb({hash, room, rooms, blobs});
        });
        chan.on('RENEW_VAULT', (token, cb) => {
            if (!transport) return cb({error: 'Vault editor unavailable'});
            transport.renew(token).then(() => cb(), () => cb({error: 'Vault authorization refused'}));
        });`],
        ["chan.on('START', function (data, cb) {", "chan.on('START', function (data, cb) {\n            if (!data.vault || (data.document !== null && !(data.document instanceof Blob))) return cb({error: 'Vault editor source required'});\n            if (transport || Hash.getSecrets('integration', data.key).channel !== data.vault.room) return cb({error: 'Vault room mismatch'});\n            const vault = transport = openVaultTransport(data.vault);"],
        ['if (data.document) { return void startApp(data.document); }', 'return void startApp(data.document);'],
        ['initialState: blob,', 'initialState: blob,\n                    vault,'],
      );
    case '/common/onlyoffice/main.js':
      return patch(
        ['Cryptpad.otherPadAttrs = channels;', `Cryptpad.otherPadAttrs = channels;
            if (integration.vault) {
                sframeChan.on('Q_VAULT_CHECKPOINT_READ', (id, cb) => {
                    integration.vault.read(id).then(blob => cb({ blob }), () => cb({ error: 'ENETWORK' }));
                });
                sframeChan.on('Q_VAULT_CHECKPOINT_WRITE', ({number, blob}, cb) => {
                    integration.vault.write(number, blob).then(url => cb({ url }), error => cb({ error: error.message }));
                });
            }`],
        ['integrationConfig: integration.config || {},', 'integrationConfig: { ...integration.config, vaultRooms: integration.vault?.rooms },'],
      );
    case '/common/sframe-common-outer.js':
      return patch(
        ['if (window.history && window.history.replaceState && (currentPad.hash || window.location.hash))', 'if (!cfg.integration && window.history && window.history.replaceState && (currentPad.hash || window.location.hash))'],
      );
    case '/common/cryptpad-common.js':
      return patch(
        ['if (localStorage.CryptPad_noWorkers) {', 'if (rdyCfg.neverDrive) {\n                noWorker = true;\n            } else if (localStorage.CryptPad_noWorkers) {'],
      );
    case '/common/worker.bundle.min.js':
      return patch(
        ['n.join("0000000000000000000000000000000000").then(function(t){let r;t.members.forEach(e=>{16===e.length&&(r=e)}),n.historyKeeper=r,t.leave(),e()},function(n){console.error(n),e({error:"GET_HK"})})', 'n.historyKeeper="0123456789abcdef",e()'],
        ['if(!n)return t?((e,n)=>{if(I.rpc)n(I.rpc);else{var t=R.Nacl.sign.keyPair(),r=I.tempKeys={edPublic:o.encodeBase64(t.publicKey),edPrivate:o.encodeBase64(t.secretKey)};u.create(I.network,r,function(e,t){e?n({error:e}):(I.rpc=t,n(t))})}})(0,e):void e()', 'if(!n)e()'],
      );
    case '/common/onlyoffice/inner.js':
      return patch(
        ['const addLinkedCheckpoint = (cpData, cb) => {', 'const addLinkedCheckpoint = (cpData, cb) => {\n            if (privateData.integrationConfig?.vaultRooms) return cb();'],
        ['const checkLinkedDocs = () => {', 'const checkLinkedDocs = () => {\n            if (privateData.integrationConfig?.vaultRooms) return;'],
        ['file: data.url,\n                rtChannel: Hash.createChannelId(),', 'file: data.url,\n                rtChannel: privateData.integrationConfig?.vaultRooms?.[i - 1] || Hash.createChannelId(),'],
        ['            APP.FM.handleFile(blob, data);\n        };\n\n        var noLogin', `            if (privateData.integrationConfig?.vaultRooms) {
                const number = (sortCpIndex(content.hashes || {}).at(-1) || 0) + 1;
                return sframeChan.query('Q_VAULT_CHECKPOINT_WRITE', { number, blob }, (err, result) => {
                    onUploaded(data, {url: result?.url}, err || result?.error);
                }, {raw: true});
            }
            APP.FM.handleFile(blob, data);
        };

        var noLogin`],
        ['                var xhr = new XMLHttpRequest();\n                xhr.open(\'GET\', src, true);', `                if (privateData.integrationConfig?.vaultRooms) {
                    return sframeChan.query('Q_VAULT_CHECKPOINT_READ', hexFileName, async (err, result) => {
                        if (err || result?.error || !(result?.blob instanceof Blob)) return reject('ENETWORK');
                        const data = new Uint8Array(await result.blob.arrayBuffer());
                        FileCrypto.decrypt(data, key, (error, decrypted) => {
                            if (error) return reject(error);
                            resolve({blob: new Blob([decrypted.content], {type: 'plain/text'}), fileType: getFileType()});
                        });
                    }, {raw: true});
                }
                var xhr = new XMLHttpRequest();
                xhr.open('GET', src, true);`],
        ['                content.channel = Hash.createChannelId();\n                content.ctime', '                content.channel = privateData.integrationConfig?.vaultRooms?.[0] || Hash.createChannelId();\n                content.ctime'],
        ['                common.openPadChat(APP.onLocal);\n\n                if (!readOnly) {', '                if (!privateData.integrationConfig?.vaultRooms) common.openPadChat(APP.onLocal);\n\n                if (!readOnly && !privateData.integrationConfig?.vaultRooms) {'],
        ["                    if (privateData.initialState && (!content", `                    if (cfg.vaultRooms && newDoc && !privateData.initialState) {
                        integrationChannel.event('EV_INTEGRATION_ERROR', 'VAULT_CHECKPOINT_MISSING');
                        return void UI.errorLoadingScreen(Messages.error);
                    }
                    if (privateData.initialState && (!content`],
        ['                    common.openIntegrationChannel(APP.onLocal);', '                    if (!cfg.vaultRooms) common.openIntegrationChannel(APP.onLocal);'],
      );
    default:
      return bytes;
  }
}
