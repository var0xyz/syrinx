import { derived, writable } from 'svelte/store';
import type { ServerInfo, SignupMode } from '$lib/types/server';
import type { PublicKey } from '$lib/types/api';
import { isOnline } from './pwa';

export const serverInfo = writable<ServerInfo | null>(null);
export const serverInfoLoading = writable(true);
export const serverInfoFetchFailed = writable(false);

/** Set when a fetched server.id no longer matches the one this device
 * already trusted — a redeployed/reset/impersonating server, not a fetch
 * failure. Null once no mismatch is outstanding. */
export const serverIdMismatch = writable<{ known: string; fetched: string } | null>(null);

export const isSignupOpen = derived(
  serverInfo,
  ($info) => $info?.signupMode === 'open'
);

export const isSignupClosed = derived(
  serverInfo,
  ($info) => $info?.signupMode === 'closed'
);

export const signupMode = derived(
  serverInfo,
  ($info): SignupMode | null => $info?.signupMode ?? null
);

export const isRecoveryMode = derived(
  serverInfo,
  ($info) => $info?.recoveryMode === true
);

/** Device is online but GET /api/server/info failed. */
export const isServerUnreachable = derived(
  [isOnline, serverInfoFetchFailed],
  ([$online, $failed]) => $online && $failed
);

/** This origin now answers as a server id different from the one this
 * device already trusted. */
export const hasServerIdMismatch = derived(
  serverIdMismatch,
  ($mismatch) => $mismatch !== null
);

function normalizeSignupMode(value: unknown): SignupMode {
  if (value === 'open' || value === 'invite' || value === 'closed') {
    return value;
  }
  return 'open';
}

/**
 * Fetch + cache this server's own signing key if it's not already in
 * publicKeys. Trust is established by the connection itself (same origin
 * this app was served from) — verifyPublicKey's serverSignature check
 * would be circular for the server's own key (it self-countersigns, so
 * "verifying" it here just means checking a key against itself over the
 * same channel it arrived on: no protection against a swapped key, which
 * could just as easily carry a forged self-signature).
 */
async function ensureServerKeyCached(serverId: string, serverKeyId: string): Promise<void> {
  if (!serverId || !serverKeyId) return;
  try {
    const { publicKeyRepository } = await import('$lib/repositories/publicKey');
    if (await publicKeyRepository.hasPublicKey(serverKeyId)) return;

    const { apiService } = await import('./api');
    const { cryptoService } = await import('./crypto');
    const { dbService } = await import('./db');
    const { allowUnsigned } = await import('$lib/verifiers');
    const { formatServerKeyId } = await import('$lib/utils/identityRef');

    const armor = await apiService.getOwnServerKey();
    const fingerprint = await cryptoService.fingerprintFromArmor(armor);
    if (formatServerKeyId(fingerprint, serverId) !== serverKeyId) {
      throw new Error('server key armor does not match serverKeyId');
    }

    const key: PublicKey = {
      id: serverKeyId,
      userID: '',
      armor,
      revoked: false,
      predecessor: null,
      serverSignature: { id: '', armor: '', timestamp: '' },
    };
    await dbService.put('publicKeys', key, allowUnsigned);
  } catch (error) {
    console.error('serverInfo: failed to cache own server key', error);
  }
}

function normalizeMaxInvites(value: unknown): number {
  if (typeof value === 'number' && Number.isInteger(value) && (value === -1 || value >= 1)) {
    return value;
  }
  if (typeof value === 'string' && value.trim() !== '') {
    const n = Number(value);
    if (Number.isInteger(n) && (n === -1 || n >= 1)) {
      return n;
    }
  }
  return -1;
}

export async function refreshServerInfo(): Promise<ServerInfo | null> {
  if (!navigator.onLine) {
    serverInfoFetchFailed.set(false);
    serverInfoLoading.set(false);
    return null;
  }

  serverInfoLoading.set(true);

  try {
    // A reachable-but-slow/hanging server must not stall this indefinitely —
    // offline-first means the app finishes booting either way, background
    // work included.
    const response = await fetch('/api/server/info', { signal: AbortSignal.timeout(8000) });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }

    const data = await response.json();
    const info: ServerInfo = {
      id: data.id,
      name: data.name,
      recoveryMode: !!data.recoveryMode,
      signupMode: normalizeSignupMode(data.signupMode),
      maxInvitesPerUser: normalizeMaxInvites(data.maxInvitesPerUser),
      serverKeyId: typeof data.serverKeyId === 'string' ? data.serverKeyId : '',
    };

    // A previously-trusted serverId that no longer matches means this
    // origin now answers as a different server (redeploy/reset, or worse) —
    // never silently adopt it. Leave the known id in place so any
    // in-progress trust check (e.g. accountRecovery's assertServerMatch)
    // still compares against what this device actually trusted, not
    // against the new value it would otherwise just have been overwritten
    // with.
    const knownServerId = localStorage.getItem('serverId');
    if (knownServerId && knownServerId !== info.id) {
      console.error(
        `serverInfo: server id changed from ${knownServerId} to ${info.id} — refusing to adopt it`
      );
      serverIdMismatch.set({ known: knownServerId, fetched: info.id });
      serverInfoFetchFailed.set(false);
      return null;
    }
    serverIdMismatch.set(null);

    localStorage.setItem('serverId', info.id);
    localStorage.setItem('serverName', info.name);
    serverInfo.set(info);
    serverInfoFetchFailed.set(false);
    await ensureServerKeyCached(info.id, info.serverKeyId);
    return info;
  } catch (error) {
    console.error('serverInfo: failed to fetch /api/server/info', error);
    serverInfoFetchFailed.set(true);
    return null;
  } finally {
    serverInfoLoading.set(false);
  }
}
