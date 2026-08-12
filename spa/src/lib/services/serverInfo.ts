import { derived, writable } from 'svelte/store';
import type { ServerInfo, SignupMode } from '$lib/types/server';
import { isOnline } from './pwa';

export const serverInfo = writable<ServerInfo | null>(null);
export const serverInfoLoading = writable(true);
export const serverInfoFetchFailed = writable(false);

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

function normalizeSignupMode(value: unknown): SignupMode {
  if (value === 'open' || value === 'invite' || value === 'closed') {
    return value;
  }
  return 'open';
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
    };

    localStorage.setItem('serverId', info.id);
    localStorage.setItem('serverName', info.name);
    serverInfo.set(info);
    serverInfoFetchFailed.set(false);
    return info;
  } catch (error) {
    console.error('serverInfo: failed to fetch /api/server/info', error);
    serverInfoFetchFailed.set(true);
    return null;
  } finally {
    serverInfoLoading.set(false);
  }
}
