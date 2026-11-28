const roomID = /^(?:[a-f0-9]{32}|[a-f0-9]{34})$/;
const blobID = /^[a-f0-9]{48}$/;

// The forwarding boundary treats a timeout, denial or unavailable authority as a refusal.
async function authorize(endpoint, token, operation) {
  if (!token) return false;
  try {
    const response = await fetch(endpoint, {
      method: 'POST',
      headers: { authorization: 'Bearer ' + token, 'content-type': 'application/json' },
      body: JSON.stringify(operation),
      redirect: 'error',
      signal: AbortSignal.timeout(1500),
    });
    await response.body?.cancel();
    return response.status === 204;
  } catch {
    return false;
  }
}

export function authorizeVaultRoom(endpoint, token, room, edit) {
  return typeof room === 'string' && roomID.test(room) &&
    authorize(endpoint, token, { room, edit });
}

export function authorizeVaultBlob(endpoint, token, blob, edit = false) {
  return typeof blob === 'string' && blobID.test(blob) &&
    authorize(endpoint, token, { blob, edit });
}
