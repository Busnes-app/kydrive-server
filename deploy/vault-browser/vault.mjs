import { officeApplications, vaultOffice } from './office.mjs';

const status = document.querySelector('#status');
const link = document.querySelector('#link');
const keyForm = document.querySelector('#key');
const passphrase = document.querySelector('#passphrase');
const keyAction = document.querySelector('#key-action');
const create = document.querySelector('#vault');
const open = document.querySelector('#open');
const requestKey = 'kydrive-vault-link';
const join = document.querySelector('#join');
const membership = document.querySelector('#membership');
const change = document.querySelector('#change-membership');
const files = document.querySelector('#files');
const fileForm = document.querySelector('#file-form');
const download = document.querySelector('#download');
const encoder = new TextEncoder();
const version = document.querySelector('#revision');
const fileID = document.querySelector('#file-id');
const officeControls = document.querySelector('#office-controls');
const officeFormat = document.querySelector('#office-format');
const officeMount = document.querySelector('#office');
const saveOffice = document.querySelector('#save-office');
const closeOffice = document.querySelector('#close-office');
const uuidPattern = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
const userPattern = /^[A-Za-z0-9_-]{1,128}$/;
const fingerprintPattern = /^[0-9a-f]{64}$/;
const jsonHeader = value => encoder.encode(JSON.stringify(value)).toBase64({ alphabet: 'base64url', omitPadding: true });

// Remove the one-time code before loading any configuration or running a request.
const redirect = new URL(location.href);
const code = redirect.searchParams.get('code');
const returnedState = redirect.searchParams.get('state');
if (redirect.search) history.replaceState(null, '', location.pathname);

function origin(value) {
  const url = new URL(value);
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);
  if (url.href !== url.origin + '/' || (url.protocol !== 'https:' && !(loopback && url.protocol === 'http:'))) {
    throw new Error('The staging site needs configured HTTPS service origins');
  }
  return url.origin;
}

function wasm(action, value = null) {
  const result = JSON.parse(globalThis.kyDriveVault(action, JSON.stringify(value)));
  if (result.error) throw new Error(result.error);
  return result.value;
}

async function request(url, options = {}) {
  return fetch(url, { credentials: 'omit', cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(15000), ...options });
}

async function checked(response) {
  if (!response.ok) throw new Error('Request refused (' + response.status + ')');
  return response;
}

let working = false;
function action(button, work) {
  return async event => {
    event.preventDefault();
    if (working) return;
    working = button.disabled = true;
    try { await work(event); }
    catch (error) { status.textContent = error.message; }
    finally { working = button.disabled = false; }
  };
}

async function start() {
  const config = await (await checked(await request('/config.json'))).json();
  const drive = origin(config.drive);
  const witness = origin(config.witness);
  const cryptpad = config.cryptpad ? origin(config.cryptpad) : '';
  const gateway = config.gateway ? origin(config.gateway) : '';
  if (cryptpad && (!gateway.startsWith('https://') || [drive, witness, gateway, location.origin].includes(cryptpad))) {
    throw Error('Configure a separate trusted CryptPad asset origin and HTTPS gateway');
  }
  officeControls.hidden = !cryptpad;
  if (drive === location.origin || witness === drive) throw new Error('The editor and witness must be separate from KyDrive');
  if (typeof config.witnessKey !== 'string' || !config.witnessKey) throw new Error('Configure the independent witness public key');

  const go = new globalThis.Go();
  const { instance } = await WebAssembly.instantiateStreaming(await checked(await request('/vault.wasm')), go.importObject);
  void go.run(instance);
  const revisionPlaintextLimit = wasm('revisionPlaintextLimit');

  const storage = 'kydrive-vault-browser:' + drive;
  let sealed = localStorage.getItem(storage);
  let user;
  let active;
  let office;
  let renewal;
  const closeEditor = () => {
    clearTimeout(renewal);
    office?.close();
    office = undefined;
    saveOffice.hidden = closeOffice.hidden = true;
    saveOffice.disabled = false;
  };
  closeOffice.addEventListener('click', () => {
    closeEditor();
    status.textContent = 'Office editor closed';
  });
  const api = async (method, path, value, extra = {}) => {
    const binary = value instanceof Uint8Array;
    const body = value === undefined ? undefined : binary ? value : JSON.stringify(value);
    const encoded = body === undefined ? '' : (binary ? body : encoder.encode(body)).toBase64();
    const headers = wasm('sign', { origin: drive, method, path, body: encoded });
    if (body !== undefined) headers['Content-Type'] = binary ? 'application/octet-stream' : 'application/json';
    return request(drive + path, { method, headers: { ...headers, ...extra }, body });
  };
  const keyFor = async id => {
    if (!userPattern.test(id ?? '')) throw new Error('Invalid member account ID');
    return (await checked(await api('GET', '/api/vault-keys/' + id))).json();
  };
  const publishKey = async () => {
    const key = wasm('publicKey');
    await checked(await api('PUT', '/api/vault-keys', key));
    const registered = await keyFor(user);
    if (registered.recipient !== key.recipient || registered.signing !== key.signing) throw new Error('The registered vault key does not match this browser');
  };
  const vaultStorage = () => storage + ':vault:' + user;
  const lastVault = () => {
    const stored = localStorage.getItem(vaultStorage());
    return uuidPattern.test(stored ?? '') ? { vault: stored, owner: wasm('identity') } : JSON.parse(stored);
  };
  const recordsPath = id => '/api/vaults/' + id + '/records';
  async function openVault(invite) {
    closeEditor();
    active = undefined;
    wasm('closeVault');
    membership.hidden = files.hidden = true;
    if (!download.hidden) {
      URL.revokeObjectURL(download.href);
      download.hidden = true;
    }
    if (!uuidPattern.test(invite?.vault ?? '') || !userPattern.test(invite.owner?.user ?? '') ||
        !fingerprintPattern.test(invite.owner?.fingerprint ?? '')) throw new Error('Invalid verified vault invitation');
    const records = [], receipts = [];
    let after = 0;
    let page;
    do {
      const path = recordsPath(invite.vault) + (after ? '?after=' + after : '');
      page = await (await checked(await api('GET', path))).json();
      if (!Array.isArray(page.records) || !page.records.length || page.records.length > 32 ||
          !Array.isArray(page.receipts) || page.receipts.length !== page.records.length || records.length >= 1024) {
        throw new Error('Invalid vault history page');
      }
      records.push(...page.records);
      receipts.push(page.receipts);
      after += page.records.length;
    } while (after < page.head.sequence);
    const members = new Set(records.flatMap(record => record.state.members.map(member => member.user)));
    const keys = Object.fromEntries(await Promise.all([...members].map(async id => [id, await keyFor(id)])));
    const input = { Invite: invite, Records: records, Keys: keys, WitnessKey: config.witnessKey };
    const head = wasm('verifyHistory', input);
    if (head.log !== page.head.log || head.hash !== page.head.hash || head.sequence !== page.head.sequence) {
      throw new Error('The returned vault head does not match the verified records');
    }
    const log = witness + '/v1/logs/' + head.log;
    const current = await request(log);
    const seen = current.status === 404 ? 0 : (await (await checked(current)).json()).head.sequence;
    for (const batch of receipts) {
      const needed = batch.filter(receipt => receipt.link.sequence > seen);
      if (needed.length) {
        const advanced = await request(log, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(needed) });
        if (advanced.status === 409) break; // Another member may have advanced it first. Verify the final head.
        await checked(advanced);
      }
    }
    const checkpoint = await (await checked(await request(log))).json();
    const state = wasm('openVault', { ...input, Checkpoint: checkpoint });
    try {
      await checked(await api('POST', '/api/vaults/' + invite.vault + '/check', { checkpoint, edit: false }));
    } catch (error) {
      wasm('closeVault');
      throw error;
    }
    active = { invite, checkpoint };
    localStorage.setItem(vaultStorage(), JSON.stringify(invite));
    open.hidden = false;
    membership.hidden = files.hidden = false;
    document.querySelector('#share').value = JSON.stringify(invite);
    document.querySelector('#members').textContent = state.members.map(member => member.user + ' (' + member.role + ')').join(', ');
    change.hidden = state.members.find(member => member.user === user)?.role !== 'manager';
    status.textContent = (state.members.length === 1 ? 'Verified and opened personal vault' : 'Verified and opened shared vault') + ' (generation ' + state.generation + ')';
  }
  const ready = () => {
    keyForm.hidden = true;
    create.hidden = join.hidden = false;
    open.hidden = !lastVault();
    const member = wasm('identity');
    const identity = document.querySelector('#identity');
    identity.textContent = member.user + ' · ' + member.fingerprint;
    identity.hidden = false;
    status.textContent = 'Vault key ready';
  };

  link.addEventListener('click', action(link, async () => {
    const verifier = crypto.getRandomValues(new Uint8Array(32)).toBase64({ alphabet: 'base64url', omitPadding: true });
    const challenge = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))).toBase64({ alphabet: 'base64url', omitPadding: true });
    const state = crypto.randomUUID();
    sessionStorage.setItem(requestKey, JSON.stringify({ verifier, state, drive, started: Date.now() }));
    const params = new URLSearchParams({ redirect: location.origin + '/', challenge, state, name: 'Vault browser', platform: 'vault-browser' });
    location.assign(drive + '/link?' + params);
  }));

  keyForm.addEventListener('submit', action(keyAction, async () => {
    const secret = passphrase.value;
    passphrase.value = '';
    if (sealed) {
      user = wasm('unlock', { Passphrase: secret, Sealed: sealed });
    } else {
      // Enrolment is immutable. Check first, and keep the encrypted key before publishing it.
      const existing = await api('GET', '/api/vault-keys/' + encodeURIComponent(user));
      if (existing.ok) throw new Error('This account already has a vault key. Staging needs a fresh account.');
      if (existing.status !== 404) await checked(existing);
      sealed = wasm('createKey', secret);
      localStorage.setItem(storage, sealed);
    }
    await publishKey();
    ready();
  }));
  create.addEventListener('click', action(create, async () => {
    const record = wasm('createVault');
    const invite = { vault: record.vault, owner: wasm('identity') };
    await checked(await api('POST', recordsPath(invite.vault), record));
    localStorage.setItem(vaultStorage(), JSON.stringify(invite));
    open.hidden = false;
    await openVault(invite);
  }));
  open.addEventListener('click', action(open, () => openVault(lastVault())));
  join.addEventListener('submit', action(join.querySelector('button'), () => openVault(JSON.parse(document.querySelector('#invitation').value))));
  change.addEventListener('submit', action(change.querySelector('button'), async event => {
    const actionName = event.submitter.value;
    const target = { user: document.querySelector('#member').value.trim(), fingerprint: document.querySelector('#fingerprint').value.trim() };
    const { invite } = active;
    await openVault(invite);
    const key = actionName === 'add' ? await keyFor(target.user) : undefined;
    const record = wasm('changeMembership', { Action: actionName, Target: target, Key: key });
    await checked(await api('POST', recordsPath(invite.vault), record));
    await openVault(invite);
  }));
  const filePath = file => '/api/vaults/' + active.invite.vault + '/files/' + file;
  async function writeRevision(file, number, source, binding = '') {
    if (source.size > revisionPlaintextLimit) throw new Error('File is too large for an encrypted 32 MiB revision');
    const vault = active;
    const content = new Uint8Array(await source.arrayBuffer()).toBase64();
    if (active !== vault) throw Error('The active vault changed before the save');
    const sealed = wasm('sealRevision', { File: file, Number: number, Binding: binding, Body: content });
    await checked(await api('PUT', filePath(file) + '/versions/' + number, Uint8Array.fromBase64(sealed.body), {
      'KyDrive-Vault-Revision': jsonHeader(sealed.manifest),
      'KyDrive-Vault-Checkpoint': jsonHeader(active.checkpoint),
    }));
  }

  const revisionManifest = response => JSON.parse(new TextDecoder().decode(Uint8Array.fromBase64(
    response.headers.get('KyDrive-Vault-Revision'), { alphabet: 'base64url' })));
  async function readRevision(file, number) {
    const response = await checked(await api('GET', filePath(file) + '/versions/' + number));
    const manifest = revisionManifest(response);
    const body = new Uint8Array(await response.arrayBuffer());
    const plaintext = wasm('readRevision', { File: file, Number: number, Manifest: manifest, Body: body.toBase64() });
    return new Blob([Uint8Array.fromBase64(plaintext)]);
  }

  const officeIDs = (values, pattern) => Array.isArray(values) && values.length === 3 &&
    new Set(values).size === values.length && values.every(value => typeof value === 'string' && pattern.test(value));
  function validateOfficeCapability(data) {
    // Bindings from the first staging version only supported DOCX.
    const format = data?.format === undefined ? 'docx' : data.format;
    const hash = format === 'docx' ? data?.hash : data?.editorHash;
    if (typeof hash !== 'string' || !/^\/2\/integration\/edit\/[A-Za-z0-9+\-]{24}\/$/.test(hash) ||
        typeof data.room !== 'string' || !/^[a-f0-9]{32}$/.test(data.room) ||
        !officeIDs(data.rooms, /^[a-f0-9]{32}$/) || data.rooms.includes(data.room) ||
        !officeIDs(data.blobs, /^[a-f0-9]{48}$/) || !officeApplications.has(format)) {
      throw Error('Invalid Office editor binding');
    }
    return { hash, format, room: data.room, rooms: data.rooms, blobs: data.blobs };
  }

  async function editOffice(file, sourceNumber) {
    const document = filePath(file);
    const path = document + '/editor';
    const current = await api('GET', path);
    let binding = current.status === 404 ? undefined : await (await checked(current)).json();
    let bindingID, format;
    const refresh = async () => {
      const checkpoint = await (await checked(await request(witness + '/v1/logs/' + active.checkpoint.head.log))).json();
      wasm('refreshCheckpoint', checkpoint);
      active.checkpoint = checkpoint;
      return (await checked(await api('POST', path + '/session', { checkpoint, edit: true }))).json();
    };
    const editor = office = vaultOffice({
      origin: cryptpad, mount: officeMount, revisionPlaintextLimit,
      onReady() {
        status.textContent = format.toUpperCase() + ' editor ready';
        saveOffice.hidden = closeOffice.hidden = false;
      },
      onError(error) {
        if (editor !== office) return;
        saveOffice.disabled = false;
        status.textContent = error?.message ?? 'CryptPad could not open the Office file';
      },
      async onSave(blob) {
        if (!saveOffice.disabled || editor !== office) throw Error('No active Office save');
        try {
          // The server sequences revisions. Read only one ciphertext byte for the current manifest.
          const response = await checked(await api('GET', document + '/versions/latest',
            undefined, { Range: 'bytes=0-0' }));
          await response.body?.cancel();
          const latest = revisionManifest(response).number;
          if (!Number.isSafeInteger(latest) || latest < 1 || latest >= Number.MAX_SAFE_INTEGER ||
              editor !== office) throw Error('Could not find the current Office revision');
          const next = latest + 1;
          await writeRevision(file, next, blob, bindingID);
          if (editor !== office) return;
          version.value = next;
          status.textContent = 'Saved encrypted ' + format.toUpperCase() + ' revision ' + next;
        } finally {
          if (editor === office) saveOffice.disabled = false;
        }
      },
    });
    const renew = async () => {
      if (editor !== office) return;
      try {
        const { token } = await refresh();
        if (editor !== office) return;
        await editor.renew(token);
        if (editor === office) renewal = setTimeout(renew, 25000);
      } catch {
        if (editor !== office) return;
        closeEditor();
        status.textContent = 'Vault editor access ended. Reopen the vault to continue.';
      }
    };
    try {
      let source = null;
      if (!binding) {
        source = await readRevision(file, sourceNumber);
        const selectedFormat = officeFormat.value;
        const { hash, room, rooms, blobs } = await editor.prepare();
        // A DOCX-only client requires hash. Give new formats their own field so
        // that an older client refuses the session instead of opening it as DOCX.
        const payload = selectedFormat === 'docx' ? { hash, room, rooms, blobs } :
          { editorHash: hash, format: selectedFormat, room, rooms, blobs };
        const capability = validateOfficeCapability(payload);
        binding = wasm('bindEditor', { File: file, Source: sourceNumber, Rooms: [capability.room, ...capability.rooms],
          Blobs: capability.blobs, EditCapability: JSON.stringify(payload) });
        await checked(await api('PUT', path, { binding, checkpoint: active.checkpoint }));
      }
      const session = wasm('openEditor', { File: file, Binding: binding });
      if (!session.edit) throw Error('Office co-editing needs editor access');
      const capability = validateOfficeCapability(JSON.parse(session.capability));
      if (session.rooms.length !== capability.rooms.length + 1 ||
          ![capability.room, ...capability.rooms].every(room => session.rooms.includes(room)) ||
          binding.blobs?.length !== capability.blobs.length ||
          !capability.blobs.every(id => binding.blobs.includes(id))) throw Error('Office capability did not match its signed binding');
      format = capability.format;
      officeFormat.value = format;
      bindingID = session.binding;
      const { token } = await refresh();
      await editor.start({ ...capability, gateway, token }, source, user);
      renewal = setTimeout(renew, 25000);
    } catch (error) {
      if (editor === office) closeEditor();
      throw error;
    }
  }

  saveOffice.addEventListener('click', () => {
    if (!office || saveOffice.disabled) return;
    saveOffice.disabled = true;
    status.textContent = 'Saving encrypted Office revision…';
    office.save();
  });
  fileForm.addEventListener('submit', action(fileForm.querySelector('button'), async event => {
    const operation = event.submitter.value;
    const file = fileID.value.trim() || (operation === 'save' ? crypto.randomUUID() : '');
    const number = Number(version.value);
    if (!uuidPattern.test(file) || !Number.isSafeInteger(number) || number < 1) throw new Error('Choose a valid file ID and revision');
    await openVault(active.invite);
    if (operation === 'office' && cryptpad) {
      status.textContent = 'Opening Office editor…';
      await editOffice(file, number);
    } else if (operation === 'save') {
      const source = document.querySelector('#content').files[0];
      if (!source) throw new Error('Select a file to encrypt');
      await writeRevision(file, number, source);
      fileID.value = file;
      status.textContent = 'Saved encrypted revision ' + number;
    } else {
      const plaintext = await readRevision(file, number);
      download.href = URL.createObjectURL(plaintext);
      if (officeApplications.has(officeFormat.value)) download.download = 'vault-document.' + officeFormat.value;
      download.hidden = false;
      status.textContent = 'Verified encrypted revision ' + number;
    }
  }));

  if (sealed) {
    passphrase.autocomplete = 'current-password';
    keyForm.hidden = false;
    status.textContent = 'Unlock the vault key saved in this browser';
  } else if (code) {
    const pending = JSON.parse(sessionStorage.getItem(requestKey));
    sessionStorage.removeItem(requestKey);
    if (pending?.state !== returnedState || !returnedState || pending.drive !== drive || Date.now() - pending.started > 15 * 60000) {
      throw new Error('The link request expired. Start linking again.');
    }
    const publicKey = wasm('device');
    const body = new URLSearchParams({ code, verifier: pending.verifier, publicKey });
    const account = await (await checked(await request(drive + '/api/vault-browser/token', { method: 'POST', body }))).json();
    user = wasm('link', account);
    status.textContent = 'Browser linked. Set a passphrase to protect your local vault key.';
    passphrase.autocomplete = 'new-password';
    keyAction.textContent = 'Create vault key';
    keyForm.hidden = false;
  } else {
    status.textContent = 'Link a fresh KyDrive staging account';
    link.hidden = false;
  }
}

start().catch(error => {
  status.textContent = error.message;
});
