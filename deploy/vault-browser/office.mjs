export function vaultOffice({ origin, mount, onSave, onReady, onError }) {
  const frame = document.createElement('iframe');
  frame.src = origin + '/integration/';
  frame.title = 'CryptPad DOCX editor';
  frame.referrerPolicy = 'no-referrer';
  frame.width = '100%';
  frame.height = '650';
  const pending = new Map();
  const events = new AbortController();
  let ready, fail;
  const loaded = new Promise((resolve, reject) => { ready = resolve; fail = reject; });
  const timeout = setTimeout(() => fail(Error('CryptPad did not initialize')), 15000);

  const send = data => frame.contentWindow?.postMessage(data, origin);
  function settle(txid, value) {
    const request = pending.get(txid);
    if (!request) return;
    clearTimeout(request.timer);
    pending.delete(txid);
    if (value?.error || !frame.isConnected) request.reject(Error('CryptPad request closed or refused'));
    else request.resolve(value);
  }

  async function receive({ source, origin: sender, data }) {
    if (sender !== origin || source !== frame.contentWindow || !data || typeof data !== 'object') return;
    if (data.q === 'INTEGRATION_READY') {
      clearTimeout(timeout);
      ready();
    }
    if (typeof data.ack === 'string') settle(data.ack, data.args);
    if (data.msg?.q === 'DOCUMENT_READY') onReady();
    if (data.msg?.q === 'DOCUMENT_ERROR') onError();
    if (data.msg?.q === 'SAVE' && typeof data.txid === 'string') {
      try {
        const blob = data.msg.data;
        if (!(blob instanceof Blob) || blob.size > (32 << 20) - 8192) throw Error('Invalid DOCX export');
        await onSave(blob);
        send({ ack: data.txid });
      } catch (error) {
        onError(error);
        if (frame.isConnected) send({ ack: data.txid, args: 'Encrypted export failed' });
      }
    }
  }
  window.addEventListener('message', receive, { signal: events.signal });
  mount.replaceChildren(frame);

  async function query(q, data) {
    await loaded;
    const txid = crypto.randomUUID();
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(txid);
        reject(Error('CryptPad did not answer'));
      }, 30000);
      pending.set(txid, { timer, resolve, reject });
      send({ msg: { q, data }, txid });
    });
  }

  return {
    prepare: () => query('PREPARE_VAULT'),
    start: (vault, hash, blob, name) => query('START', {
      vault, key: hash, document: blob, application: 'doc', ext: 'docx',
      name: 'vault-document.docx', autosave: 0, readOnly: false, editorConfig: { user: { name } },
    }),
    save: () => send({ msg: { q: 'MANUAL_SAVE' } }),
    renew: token => query('RENEW_VAULT', token),
    close() {
      clearTimeout(timeout);
      events.abort();
      frame.remove();
      for (const txid of pending.keys()) settle(txid, { error: true });
    },
  };
}
