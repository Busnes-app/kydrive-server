import assert from 'node:assert/strict';
import test from 'node:test';
import { acceptServerFrame, clientOperation, historyKeeper } from './netflux.mjs';

const room = 'a0'.repeat(16);
const other = 'b0'.repeat(16);
const frame = value => Buffer.from(JSON.stringify(value));

test('scopes writes to joined rooms and rejects peer RPC', () => {
  const joined = new Set([room]);
  const pending = new Map();
  const op = value => clientOperation(frame(value), room, joined, pending);
  assert.deepEqual(op([1, 'MSG', room, 'ciphertext']), { room, edit: true });
  assert.equal(op([2, 'MSG', other, 'ciphertext']), null);
  assert.deepEqual(op([3, 'MSG', historyKeeper, JSON.stringify(['abc', ['IS_NEW_CHANNEL', other]])]),
    { room: other, edit: false, history: true });
  assert.deepEqual(op([3, 'MSG', historyKeeper, JSON.stringify(['abc', ['GET_METADATA', room]])]),
    { room, edit: false, history: true });
  assert.equal(op([3, 'MSG', historyKeeper, JSON.stringify(['abc', ['GET_FILE_SIZE', other]])]), null);
  assert.equal(op([4, 'JOIN', historyKeeper]), null);
  assert.equal(op([5, 'UNKNOWN', room]), null);
  assert.equal(op([6, 'MSG', room, ['unexpected']]), null);
  assert.equal(op([0, '', 'PONG']), null);
  assert.deepEqual(op([8, 'PING']), { room, edit: false });
});

test('checks the room inside history and treats metadata creation as a write', () => {
  const op = (command, target, ...args) => clientOperation(
    frame([7, 'MSG', historyKeeper, JSON.stringify([command, target, ...args])]), room, new Set(), new Map(),
  );
  assert.deepEqual(op('GET_HISTORY', other, { txid: 'fixture' }), { room: other, edit: true, history: true });
  assert.deepEqual(op('GET_FULL_HISTORY', other), { room: other, edit: false, history: true });
  assert.deepEqual(op('GET_HISTORY_RANGE', other, { count: 1 }), { room: other, edit: false, history: true });
  assert.equal(op('GET_HISTORY', ['unscoped']), null);
});

test('forwards only acknowledged room events and requested history replies', () => {
  const state = { identity: '', joining: new Map([[3, room]]), joined: new Set(), requestedHistory: false };
  const accept = value => acceptServerFrame(frame(value), state);
  assert.ok(accept([0, '', 'IDENT', 'fixture-peer']));
  assert.equal(accept([3, 'JACK', other]), false);
  assert.ok(accept([3, 'JACK', room]));
  assert.ok(accept([0, 'peer', 'MSG', room, 'ciphertext']));
  assert.equal(accept([0, 'peer', 'MSG', other, 'ciphertext']), false);
  assert.equal(accept([0, historyKeeper, 'MSG', 'fixture-peer', 'history']), false);
  state.requestedHistory = true;
  assert.ok(accept([0, historyKeeper, 'MSG', 'fixture-peer', 'history']));
  assert.equal(accept([0, 'other-peer', 'MSG', 'fixture-peer', 'unscoped']), false);
});

test('permits only the matching echo of an upstream heartbeat', () => {
  const state = { joining: new Map(), joined: new Set() };
  const op = value => clientOperation(frame(value), room, state.joined, state.joining, state.pong);
  assert.ok(acceptServerFrame(frame(['', '', 'PING', 123]), state));
  assert.equal(op(['', '', 'PONG', 124]), null);
  assert.deepEqual(op(['', '', 'PONG', 123]), { room, edit: false, pong: true });
});
