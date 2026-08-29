import type * as api from '$lib/types/api';
import { apiService } from './api';
import { authService } from './auth';
import { cryptoService } from './crypto';
import { privateKeyRepository } from '$lib/repositories/privateKey';
import { invitesRepository } from '$lib/repositories/invites';
import { buildInviteUserPayload } from './signing';
import { signedAtHeader } from './verify';
import { get } from 'svelte/store';
import { serverInfo } from './serverInfo';
import { generateId } from '$lib/utils/id';

function newInviteSecret(): string {
  const buf = new Uint8Array(32);
  crypto.getRandomValues(buf);
  let binary = '';
  for (const b of buf) binary += String.fromCharCode(b);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** SHA-256(secret) as lowercase hex — matches invites.HashSecret. */
export async function hashInviteSecret(secret: string): Promise<string> {
  const data = new TextEncoder().encode(secret);
  const digest = await crypto.subtle.digest('SHA-256', data);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

/** Share URL: invite id + creator id in query, secret in fragment (never sent on navigation). */
export function inviteShareURL(
  id: string,
  secret: string,
  creatorId: string,
  origin = window.location.origin,
): string {
  const q = new URLSearchParams({ iid: id, uid: creatorId });
  return `${origin}/signup?${q}#${secret}`;
}

/**
 * Mint id+secret locally, sign id+createdAt+tokenHash, countersign on server.
 * Secret never leaves the browser on create.
 */
export async function createSignedInvite(grantAdmin = false): Promise<api.Invite> {
  const user = await authService.getCurrentUser();
  if (!user?.id) {
    throw new Error('Not signed in');
  }

  const info = get(serverInfo);
  const serverID = info?.id || localStorage.getItem('serverId');
  if (!serverID) {
    throw new Error('Server ID not available');
  }

  const fingerprint = authService.getActiveKeyFingerprint();
  const passphrase = authService.getPassphrase();
  if (!fingerprint || !passphrase) {
    throw new Error('Active key or passphrase not available');
  }

  const privateKey = await privateKeyRepository.getPrivateKey(fingerprint);
  if (!privateKey?.armor) {
    throw new Error('Private key not found');
  }

  const id = generateId();
  const secret = newInviteSecret();
  const tokenHash = await hashInviteSecret(secret);
  const createdAt = signedAtHeader(new Date());
  const grantedRole = grantAdmin ? 'admin' : 'user';
  const userPayload = buildInviteUserPayload(
    serverID,
    user.id,
    id,
    tokenHash,
    createdAt,
    grantedRole
  );
  const sigArmor = await cryptoService.signMessage(
    userPayload,
    privateKey.armor,
    passphrase
  );
  const userSignature = {
    keyID: fingerprint,
    armor: btoa(sigArmor),
  };

  const created = await apiService.createInvite({
    id,
    tokenHash,
    createdAt,
    grantedRole,
    userSignature,
  });

  const invite: api.Invite = {
    id: created.id,
    tokenHash: created.tokenHash,
    grantedRole: created.grantedRole,
    secret,
    createdAt: created.createdAt,
    status: 'pending',
    claimedAt: null,
    claimedBy: null,
    revokedAt: null,
    userSignature: created.userSignature,
    serverSignature: created.serverSignature,
  };
  await invitesRepository.put(invite);
  return invite;
}

/**
 * Refresh status for pending local invites only; claimed/revoked stay put.
 * Invokes onInviteUpdated as each pending invite finishes so the UI can update in place.
 */
export async function refreshPendingInviteStatuses(
  onInviteUpdated?: (invite: api.Invite) => void
): Promise<api.Invite[]> {
  const local = await invitesRepository.getAll();
  const terminal = local.filter((invite) => invite.status !== 'pending');
  const pending = local.filter((invite) => invite.status === 'pending');

  const refreshed = await Promise.all(
    pending.map(async (invite) => {
      try {
        const status = await apiService.getInviteStatus(invite.id);
        const next: api.Invite = {
          ...invite,
          status: status.status,
          claimedAt: status.claimedAt,
          claimedBy: status.claimedBy,
          revokedAt: status.revokedAt,
        };
        if (next.status !== 'pending') {
          delete next.secret;
        }
        await invitesRepository.putStatus(next);
        onInviteUpdated?.(next);
        return next;
      } catch (err) {
        console.error('[refreshPendingInviteStatuses]', invite.id, err);
        onInviteUpdated?.(invite);
        return invite;
      }
    })
  );

  return [...terminal, ...refreshed].sort(
    (a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime()
  );
}

export async function revokeLocalInvite(id: string): Promise<api.Invite | null> {
  await apiService.revokeInvite(id);
  const existing = await invitesRepository.get(id);
  if (!existing) return null;
  const next: api.Invite = {
    ...existing,
    status: 'revoked',
    revokedAt: signedAtHeader(new Date()),
  };
  delete next.secret;
  await invitesRepository.putStatus(next);
  return next;
}
