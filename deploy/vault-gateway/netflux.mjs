// CryptPad 2026.10.1 uses this reserved peer for history and RPC.
export const historyKeeper = '0123456789abcdef';
export const roomID = /^(?:[a-f0-9]{32}|[a-f0-9]{34})$/;
const historyCommands = new Set(['GET_HISTORY', 'GET_FULL_HISTORY', 'GET_HISTORY_RANGE']);

export function clientOperation(data, anchor, joined, joining) {
  const frame = JSON.parse(data.toString());
  if (!Array.isArray(frame) || !Number.isSafeInteger(frame[0]) ||
      frame[0] < 0 || frame.length > 4) return null;
  const [seq, type, target, payload] = frame;
  if ((type === 'PING' && frame.length === 2) || (type === '' && target === 'PONG' && frame.length === 3)) {
    return { room: anchor, edit: false };
  }
  if (!roomID.test(typeof target === 'string' ? target : '') && target !== historyKeeper) return null;
  switch (type) {
    case 'JOIN':
      if (frame.length === 3 && target !== historyKeeper && !joined.has(target) && !joining.has(seq) &&
          joining.size < 8 && ![...joining.values()].includes(target)) {
        return { room: target, edit: false, join: seq };
      }
      break;
    case 'LEAVE':
      if (frame.length === 4 && typeof payload === 'string' && joined.has(target)) {
        return { room: target, edit: false, leave: true };
      }
      break;
    case 'MSG':
      if (frame.length !== 4 || typeof payload !== 'string') break;
      if (target !== historyKeeper) {
        // Netflux interprets an unjoined target as a direct peer instead of a room.
        if (joined.has(target)) return { room: target, edit: true };
        break;
      }
      const history = JSON.parse(payload);
      if (!Array.isArray(history) || history.length < 2 || history.length > 3 ||
          !historyCommands.has(history[0]) || typeof history[1] !== 'string' || !roomID.test(history[1])) break;
      // GET_HISTORY can initialize missing room metadata even with an empty config.
      return { room: history[1], edit: history[0] === 'GET_HISTORY', history: true };
  }
  return null;
}

export function acceptServerFrame(data, state) {
  const frame = JSON.parse(data.toString());
  if (!Array.isArray(frame)) return false;
  const [seq, peerOrReply, type, target] = frame;
  switch (peerOrReply) {
    case 'JACK':
      if (!state.joining.has(seq) || state.joining.get(seq) !== type) return false;
      state.joining.delete(seq);
      state.joined.add(type);
      return true;
    case 'ERROR':
      state.joining.delete(seq);
      return true;
    case 'ACK':
      return true;
  }
  if (type === 'IDENT' && !state.identity && typeof target === 'string') {
    state.identity = target;
    return true;
  }
  if (type === 'JOIN' || type === 'LEAVE') return state.joined.has(target);
  if (type === 'MSG') {
    return state.joined.has(target) ||
      (peerOrReply === historyKeeper && target === state.identity && state.requestedHistory);
  }
  return type === 'PING' && peerOrReply === '';
}
