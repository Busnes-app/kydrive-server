import { createReadStream, createWriteStream, readFileSync } from 'node:fs';
import { link, mkdtemp, rm, stat } from 'node:fs/promises';
import { request as httpRequest } from 'node:http';
import { createServer, request as httpsRequest } from 'node:https';
import { once } from 'node:events';
import { pipeline } from 'node:stream/promises';
import { WebSocket, WebSocketServer } from 'ws';
import { authorizeVaultBlob, authorizeVaultRoom } from '../../scripts/vault-relay-authorize.mjs';
import { acceptServerFrame, clientOperation } from './netflux.mjs';

const frameLimit = 1 << 20;
const socketLimit = 32;
const protocol = 'kydrive-vault';
const tokenPattern = /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/;
const openPath = /^\/cryptpad_websocket\?room=([a-f0-9]{32}|[a-f0-9]{34})$/;
const checkpointPath = /^\/checkpoint\/([a-f0-9]{48})$/;
const checkpointRoot = '/app/checkpoints/';
const checkpointLimit = 32 << 20;
let checkpointWrites = 0;
const blobPath = /^\/blob\/([a-f0-9]{2})\/([a-f0-9]{48})$/;

function origin(value, protocols) {
  const url = new URL(value);
  if (!protocols.includes(url.protocol) || url.href !== url.origin + '/') {
    throw new Error('expected an origin with an approved scheme');
  }
  return url.origin;
}

const clientOrigins = (process.env.KY_VAULT_CLIENT_ORIGINS || '').split(',')
  .map(value => origin(value, ['https:', 'http:']));
if (clientOrigins.length > 2 || clientOrigins.some(value => value.startsWith('http:') &&
    !['localhost', '127.0.0.1', '[::1]'].includes(new URL(value).hostname))) {
  throw new Error('TLS is required for remote client origins');
}
const upstream = origin(process.env.KY_VAULT_RELAY_UPSTREAM, ['http:', 'https:']);
const authority = origin(process.env.KY_VAULT_RELAY_AUTHORITY, ['https:']) + '/api/vault-relay/check';
const upstreamSocket = upstream.replace(/^http/, 'ws') + '/cryptpad_websocket';

function header(req, name) {
  const values = req.headersDistinct[name] || [];
  return values.length === 1 ? values[0] : '';
}
function validToken(token) {
  return token.length <= 2048 && tokenPattern.test(token);
}
function httpToken(req) {
  return /^Bearer (\S+)$/.exec(header(req, 'authorization'))?.[1] || '';
}
function socketToken(req) {
  const parts = header(req, 'sec-websocket-protocol').split(',').map(value => value.trim());
  if (parts.length !== 2 || parts[0] !== protocol || !parts[1].startsWith('grant.')) return '';
  return parts[1].slice('grant.'.length);
}
function refuse(res, status) {
  res.writeHead(status, { 'cache-control': 'no-store', 'content-length': '0' }).end();
}
function cors(req, res) {
  if (req.headersDistinct.origin === undefined) return true;
  const source = header(req, 'origin');
  if (!clientOrigins.includes(source)) return false;
  res.setHeader('access-control-allow-origin', source);
  res.setHeader('vary', 'Origin');
  return true;
}

async function serveCheckpoint(req, res, blob) {
  const writing = req.method === 'PUT';
  if (!cors(req, res)) return refuse(res, 403);
  if (req.method === 'OPTIONS') {
    if (!clientOrigins.includes(header(req, 'origin')) ||
        !['GET', 'PUT'].includes(header(req, 'access-control-request-method'))) return refuse(res, 403);
    res.setHeader('access-control-allow-methods', 'GET, PUT');
    res.setHeader('access-control-allow-headers', 'Authorization, Content-Type');
    return refuse(res, 204);
  }
  const length = header(req, 'content-length');
  if ((!writing && req.method !== 'GET') || req.headers['transfer-encoding'] ||
      (!writing && length && length !== '0')) return refuse(res, 405);
  if (writing && (!/^[1-9][0-9]{0,7}$/.test(length) || Number(length) > checkpointLimit ||
      header(req, 'content-type') !== 'application/octet-stream')) return refuse(res, 400);
  const token = httpToken(req);
  if (!validToken(token) || !await authorizeVaultBlob(authority, token, blob, writing)) return refuse(res, 403);

  const file = checkpointRoot + blob;
  if (!writing) {
    const info = await stat(file).catch(error => {
      if (error.code !== 'ENOENT') throw error;
    });
    if (!info) return refuse(res, 404);
    res.writeHead(200, {
      'content-length': info.size, 'content-type': 'application/octet-stream',
      'content-disposition': 'attachment', 'x-content-type-options': 'nosniff', 'cache-control': 'no-store',
    });
    await pipeline(createReadStream(file), res);
    return;
  }
  if (checkpointWrites >= 4) return refuse(res, 503);
  checkpointWrites++;
  let temp;
  try {
    temp = await mkdtemp(checkpointRoot + '.upload-');
    const staged = temp + '/ciphertext';
    await pipeline(req, createWriteStream(staged, { flags: 'wx', mode: 0o600 }),
      { signal: AbortSignal.timeout(15_000) });
    if ((await stat(staged)).size !== Number(length)) return refuse(res, 400);
    if (!await authorizeVaultBlob(authority, token, blob, true)) return refuse(res, 403);
    try {
      // Publish only complete uploads and never replace a checkpoint another editor published.
      await link(staged, file);
    } catch (error) {
      if (error.code === 'EEXIST') return refuse(res, 409);
      throw error;
    }
    return refuse(res, 201);
  } finally {
    checkpointWrites--;
    if (temp) await rm(temp, { recursive: true, force: true });
  }
}

async function serveBlob(req, res) {
  const path = blobPath.exec(req.url);
  if (!path || !path[2].startsWith(path[1])) return refuse(res, 404);
  if (!cors(req, res)) return refuse(res, 403);
  if (req.method === 'OPTIONS') {
    if (!clientOrigins.includes(header(req, 'origin')) ||
        !['GET', 'HEAD'].includes(header(req, 'access-control-request-method'))) return refuse(res, 403);
    res.setHeader('access-control-allow-methods', 'GET, HEAD');
    res.setHeader('access-control-allow-headers', 'Authorization, Range');
    res.setHeader('access-control-expose-headers', 'Content-Range, Accept-Ranges');
    return refuse(res, 204);
  }
  if (!['GET', 'HEAD'].includes(req.method) || req.headers['transfer-encoding'] ||
      (req.headers['content-length'] && req.headers['content-length'] !== '0')) return refuse(res, 405);

  const token = httpToken(req);
  const blob = path[2];
  if (!validToken(token) || !await authorizeVaultBlob(authority, token, blob)) return refuse(res, 403);
  const range = header(req, 'range');
  if (req.headersDistinct.range && !/^bytes=(?:[0-9]{1,15}-[0-9]{0,15}|-[0-9]{1,15})$/.test(range)) return refuse(res, 416);
  const url = new URL(req.url, upstream);
  const request = url.protocol === 'https:' ? httpsRequest : httpRequest;
  const out = request(url, { method: req.method, headers: range ? { range } : {} });
  res.on('close', () => out.destroy());
  out.setTimeout(15_000, () => out.destroy());
  out.end();
  const [incoming] = await once(out, 'response');
  // Only file bytes and range metadata cross this boundary.
  if (![200, 206, 416].includes(incoming.statusCode)) {
    incoming.destroy();
    return refuse(res, incoming.statusCode === 404 ? 404 : 502);
  }
  const reply = {
    'content-type': 'application/octet-stream',
    'content-disposition': 'attachment',
    'cache-control': 'no-store',
    'x-content-type-options': 'nosniff',
    'access-control-expose-headers': 'Content-Range, Accept-Ranges',
  };
  for (const name of ['content-length', 'content-range', 'accept-ranges']) {
    if (incoming.headers[name] !== undefined) reply[name] = incoming.headers[name];
  }
  res.writeHead(incoming.statusCode, reply);
  await pipeline(incoming, res);
}

// Pause input during authorization. Bound buffers even if several frames were
// decoded from a single TCP read before pause took effect.
function forward(source, target, allow, stop) {
  let queued = 0;
  let pending = 0;
  let order = Promise.resolve();
  source.on('message', (data, binary) => {
    if (binary || queued + data.length > 2 * frameLimit || pending >= 128) return stop();
    source.pause();
    queued += data.length;
    pending++;
    order = order.then(async () => {
      if (source.readyState !== WebSocket.OPEN || target.readyState !== WebSocket.OPEN) return;
      const allowed = await allow(data);
      if (!allowed || target.readyState !== WebSocket.OPEN ||
          source.readyState !== WebSocket.OPEN || target.bufferedAmount + data.length > frameLimit) {
        return stop();
      }
      if (allowed === true) target.send(data, { binary: false });
    }).catch(stop).finally(() => {
      queued -= data.length;
      if (--pending === 0 && source.readyState === WebSocket.OPEN) source.resume();
    });
  });
}

const wss = new WebSocketServer({
  noServer: true, maxPayload: frameLimit, perMessageDeflate: false,
  handleProtocols: () => protocol,
});
function relayScope(token) {
  const { Org, Vault, File, User, Device, Binding, Edit } =
    JSON.parse(Buffer.from(token.split('.')[0], 'base64url').toString());
  return JSON.stringify([Org, Vault, File, User, Device, Binding, Edit]);
}

function bridge(client, relay, token, anchor) {
  const scope = relayScope(token);
  const state = { joined: new Set(), joining: new Map(), identity: '', requestedHistory: false };
  let stopped = false;
  const stop = () => {
    if (stopped) return;
    stopped = true;
    relay.terminate();
    if (client.readyState === WebSocket.OPEN) {
      client.close(1008, 'Vault authorization refused');
      client.resume();
      setTimeout(() => client.terminate(), 500).unref();
    }
  };
  client.on('error', stop);
  relay.on('error', stop);
  client.on('close', () => relay.terminate());
  relay.on('close', stop);

  forward(client, relay, async data => {
    if (data[0] === 123) { // Gateway control messages are objects. Netflux frames are arrays.
      const renewal = JSON.parse(data.toString());
      const { grant } = renewal;
      if (Object.keys(renewal).length !== 1 || typeof grant !== 'string' || !validToken(grant) ||
          !await authorizeVaultRoom(authority, grant, anchor, false) ||
          relayScope(grant) !== scope) return false;
      token = grant;
      return 'renewed';
    }
    const op = clientOperation(data, anchor, state.joined, state.joining, state.pong);
    if (!op || !await authorizeVaultRoom(authority, token, op.room, op.edit)) return false;
    if (op.join !== undefined) state.joining.set(op.join, op.room);
    if (op.leave) state.joined.delete(op.room);
    if (op.history) state.requestedHistory = true;
    if (op.pong) state.pong = undefined;
    return true;
  }, stop);
  forward(relay, client, data =>
    acceptServerFrame(data, state) && authorizeVaultRoom(authority, token, anchor, false), stop);
}

const server = createServer({
  cert: readFileSync(process.env.KY_VAULT_GATEWAY_CERT),
  key: readFileSync(process.env.KY_VAULT_GATEWAY_KEY),
  minVersion: 'TLSv1.3',
  maxHeaderSize: 16 << 10,
  requestTimeout: 15_000,
  headersTimeout: 5_000,
}, (req, res) => {
  const checkpoint = checkpointPath.exec(req.url);
  const response = checkpoint ? serveCheckpoint(req, res, checkpoint[1]) : serveBlob(req, res);
  response.catch(() => {
    if (res.headersSent) res.destroy();
    else refuse(res, 502);
  });
});
server.maxConnections = 128;

function rejectUpgrade(socket, status) {
  if (!socket.destroyed) socket.end('HTTP/1.1 ' + status + '\r\nConnection: close\r\nContent-Length: 0\r\n\r\n');
}

let handshakes = 0;
server.on('upgrade', async (req, socket, head) => {
  socket.on('error', () => socket.destroy());
  const anchor = openPath.exec(req.url)?.[1];
  const source = header(req, 'origin');
  const token = socketToken(req);
  if (!anchor || !clientOrigins.includes(source) || !validToken(token) || head.length) {
    return rejectUpgrade(socket, '403 Forbidden');
  }
  if (handshakes + wss.clients.size >= socketLimit) return rejectUpgrade(socket, '503 Service Unavailable');
  handshakes++;
  socket.pause();
  try {
    if (!await authorizeVaultRoom(authority, token, anchor, false)) return rejectUpgrade(socket, '403 Forbidden');
    if (socket.destroyed) return;
    const relay = new WebSocket(upstreamSocket, {
      origin: source, maxPayload: frameLimit, perMessageDeflate: false,
      followRedirects: false, handshakeTimeout: 3000,
    });
    relay.on('error', () => relay.terminate());
    socket.once('close', () => relay.terminate());
    await new Promise((resolve, reject) => {
      relay.once('error', reject);
      relay.once('close', () => reject(Error('Upstream socket closed during upgrade')));
      relay.once('open', () => {
        try {
          // IDENT can arrive in the same read as the upstream upgrade response.
          // Install the forwarding listeners before returning from the open event.
          if (socket.destroyed) relay.terminate();
          else wss.handleUpgrade(req, socket, head, client => {
            bridge(client, relay, token, anchor);
            client.resume();
          });
          resolve();
        } catch (error) {
          relay.terminate();
          reject(error);
        }
      });
    });
  } catch {
    rejectUpgrade(socket, '502 Bad Gateway');
  } finally {
    handshakes--;
  }
});
server.listen(9443, '0.0.0.0');
