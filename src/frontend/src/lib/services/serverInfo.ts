import { derived, writable } from 'svelte/store';
import type { FederatedServer, ServerInfo, SignupMode } from '$lib/types/server';
import type { PublicKey } from '$lib/types/api';
import { create } from '@bufbuild/protobuf';
import { PublicKeySchema } from '$lib/proto/identity_pb';
import { isOnline } from './pwa';
import { serverKeyProofHeader, getTrustedServerKey } from './serverKeyTrust';
import { formatServerKeyId } from '$lib/utils/identityRef';
import { readServerInfo } from './api';

export const serverInfo = writable<ServerInfo | null>(null);
export const serverInfoLoading = writable(true);
export const serverUnreachable = writable(false);

/** Set when a fetched server.id no longer matches the one this device
 * already trusted — a redeployed/reset/impersonating server, not a fetch
 * failure. Null once no mismatch is outstanding. */
export const serverIdMismatch = writable<{ known: string; fetched: string } | null>(null);

/** Set when the server rejects the key this device trusts, e.g. after a
 * redeploy minted a new one. Nothing local is changed; the user decides. */
export const serverKeyRejected = writable(false);

/** The operator's reason, when the stored key was rejected because the
 * server reported it compromised. Its successor must be confirmed by hand. */
export const serverKeyCompromise = writable<{ reason: string } | null>(null);

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
  [isOnline, serverUnreachable],
  ([$online, $unreachable]) => $online && $unreachable
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
 * Cache this server's own signing key in publicKeys if it's not already
 * there, sourced from the armor the user pasted out-of-band on first run
 * (see serverKeyTrust) rather than fetched over the wire — that pasted
 * armor is the trust anchor, so no further verification is needed here.
 */
async function ensureServerKeyCached(serverId: string, serverKeyId: string): Promise<void> {
  if (!serverId || !serverKeyId) return;
  try {
    const { publicKeyRepository } = await import('$lib/repositories/publicKey');
    if (await publicKeyRepository.hasPublicKey(serverKeyId)) return;

    const { dbService } = await import('./db');
    const { allowUnsigned } = await import('$lib/verifiers');

    const trusted = getTrustedServerKey();
    if (!trusted) return;
    if (formatServerKeyId(trusted.fingerprint, serverId) !== serverKeyId) {
      throw new Error('server key armor does not match serverKeyId');
    }

    const key: PublicKey = create(PublicKeySchema, { id: serverKeyId, armor: trusted.armor });
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

export async function refreshServerInfo(updateServerKey = true): Promise<ServerInfo | null> {
  if (!navigator.onLine) {
    serverUnreachable.set(false);
    serverInfoLoading.set(false);
    return null;
  }

  serverInfoLoading.set(true);

  try {
    // A reachable-but-slow/hanging server must not stall this indefinitely —
    // offline-first means the app finishes booting either way, background
    // work included.
    const response = await fetch('/api/server/info', {
      headers: serverKeyProofHeader(),
      signal: AbortSignal.timeout(8000),
    });

    if (response.status === 401) {
      // A rotated key is adopted silently; a compromised one never is.
      if (updateServerKey) {
        const { updateTrustedServerKey } = await import('./serverKeyRotation');
        const result = await updateTrustedServerKey();
        if (result.status === 'updated') return refreshServerInfo(false);
        serverKeyCompromise.set(result.status === 'compromised' ? { reason: result.reason } : null);
      }
      console.error('serverInfo: server rejected the trusted server key');
      serverKeyRejected.set(true);
      serverUnreachable.set(false);
      return null;
    }
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    serverKeyRejected.set(false);
    serverKeyCompromise.set(null);

    const data = await readServerInfo(response);
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
      serverUnreachable.set(false);
      return null;
    }
    serverIdMismatch.set(null);

    localStorage.setItem('serverId', info.id);
    localStorage.setItem('serverName', info.name);
    serverInfo.set(info);
    serverUnreachable.set(false);
    await ensureServerKeyCached(info.id, info.serverKeyId);
    await storeFederatedServers(data.federation);
    return info;
  } catch (error) {
    console.error('serverInfo: failed to fetch /api/server/info', error);
    serverUnreachable.set(true);
    return null;
  } finally {
    serverInfoLoading.set(false);
  }
}

/** Keeps the local list of peers in step with what /server/info reports.
 * Malformed entries are dropped rather than failing the whole refresh. */
async function storeFederatedServers(federation: FederatedServer[]): Promise<void> {
  const servers = federation.filter((e) => e.id !== '' && e.frontendUrl !== '');
  try {
    const { federatedServersRepository } = await import('$lib/repositories/federatedServers');
    await federatedServersRepository.replaceAll(servers);
  } catch (error) {
    console.error('serverInfo: failed to store federated servers', error);
  }
}
