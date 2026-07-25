import type * as api from '$lib/types/api';
import { requestSigner } from './request-signer';
import { authService } from './auth';
import {
  handleFinishRecoveryForbidden,
  isFinishRecoveryForbiddenMessage,
} from './restoreFlow';

export type SignReedResponse = {
  serverID: string;
  fingerprint: string;
  timestamp: string;
  armor: string;
};

export type SignupInput = {
  username: string;
  publicKey: string;
  signature: string;
};

export type UserStatus = 'complete' | 'unknown' | 'ongoing';

export type UserStatusProbeResult = {
  httpStatus: number;
  status?: UserStatus;
  error?: string;
};

const BASE_URL = '/api';

// Unauthenticated endpoints that don't need signing.
// `/server/keys` is public verification material — required so clients can
// verify countersignatures even when their active user key is revoked
// (e.g. mid-rotation, right after RevokeKey).
const UNAUTHENTICATED_ENDPOINTS = [
  '/users/signup',
  '/users/status',
  '/check-username',
  '/keys',
  '/server/info',
  '/server/keys',
  '/recovery/identity/claim',
];

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let signedInit = init;

  // Check if this is an authenticated request
  const isAuthenticated = !UNAUTHENTICATED_ENDPOINTS.some(endpoint => path.startsWith(endpoint));

  if (isAuthenticated) {
    try {
      // Check if request signer is initialized
      if (!requestSigner.isInitialized()) {
        // Try to get auth data from auth service
        const fingerprint = authService.getActiveKeyFingerprint();
        const passphrase = authService.getPassphrase();
        if (!fingerprint || !passphrase) {
          throw new Error('Cannot sign request: active key or passphrase not available');
        }

        await requestSigner.initializeWorker(fingerprint, passphrase);
      }

      // Sign the request
      signedInit = await requestSigner.signRequest(`${BASE_URL}${path}`, init);
    } catch (error) {
      console.error('Failed to sign request:', error);
      throw error;
    }
  }

  const res = await fetch(`${BASE_URL}${path}`, signedInit);

  if (!res.ok) {
    if (res.status === 400 || res.status === 401 || res.status === 403) {
      let message =
        res.status === 401
          ? 'Authentication failed. Please check your credentials.'
          : res.status === 403
            ? 'Forbidden'
            : res.statusText || 'Bad Request';
      try {
        const raw = await res.text();
        if (raw.trim()) {
          try {
            const errorData = JSON.parse(raw);
            message =
              typeof errorData === 'string' && errorData
                ? errorData
                : raw.trim();
          } catch {
            message = raw.trim();
          }
        }
      } catch {
        // Empty / unreadable body — keep default.
      }

      if (
        res.status === 403 &&
        isFinishRecoveryForbiddenMessage(message)
      ) {
        handleFinishRecoveryForbidden();
      }

      throw new Error(message);
    }
    const err = new Error(`HTTP ${res.status}`) as Error & { status?: number; body?: unknown };
    err.status = res.status;
    try {
      err.body = await res.json();
    } catch {
      // no JSON body
    }
    throw err;
  }

  try {
    return (await res.json()) as T;
  } catch {
    return undefined as T;
  }
}

export const apiService = {
  /**
   * Unauthenticated probe: POST countersigned profile, branch on HTTP status.
   * Does not throw on 404/409 — those are meaningful probe outcomes.
   */
  async probeUserStatus(profile: api.User): Promise<UserStatusProbeResult> {
    const res = await fetch(`${BASE_URL}/users/status`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(profile),
    });

    if (res.status === 200 || res.status === 404 || res.status === 409) {
      try {
        const body = (await res.json()) as { status?: string };
        const status = body?.status;
        if (status === 'complete' || status === 'unknown' || status === 'ongoing') {
          return { httpStatus: res.status, status };
        }
      } catch {
        // fall through
      }
      return { httpStatus: res.status };
    }

    if (res.status === 400) {
      let message = res.statusText || 'Bad Request';
      try {
        const errorData = await res.json();
        if (typeof errorData === 'string' && errorData) {
          message = errorData;
        }
      } catch {
        // Non-JSON body — keep default.
      }
      return { httpStatus: 400, error: message };
    }

    return {
      httpStatus: res.status,
      error: `Unexpected server response: HTTP ${res.status}`,
    };
  },

  async signup(input: SignupInput): Promise<api.User> {
    const formData = new URLSearchParams();
    formData.append('username', input.username);
    formData.append('publicKey', input.publicKey);
    formData.append('signature', input.signature);

    return request<api.User>('/users/signup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },
  async whoami() {
    return request<{ id: string; username: string }>('/users/me', { method: 'GET' });
  },
  async getUser(userId: string): Promise<api.User> {
    return request<api.User>(`/users/${userId}`, {
      method: 'GET'
    });
  },

  async getUserWithStatus(userId: string): Promise<{
    status: number;
    user?: api.User;
    removal?: api.AccountRemoval;
  }> {
    try {
      const user = await request<api.User>(`/users/${userId}`, { method: 'GET' });
      return { status: 200, user };
    } catch (error: any) {
      if (error?.status === 410 && error.body?.type === 'account') {
        return { status: 410, removal: error.body as api.AccountRemoval };
      }
      if (error?.status) {
        return { status: error.status };
      }
      const match = error?.message?.match(/HTTP (\d+)/);
      return { status: match ? parseInt(match[1]) : 0 };
    }
  },
  // updateUser mints a fresh signed identity record for the caller.
  // Every accepted request is a *full* replacement of the signed
  // user-authored fields — partial patches are no longer supported.
  // Callers must pass the complete post-edit tuple
  // (username, avatarURL, bio) plus a base64(armored PGP) detached
  // signature over `buildUserIdentityPayload(username, fingerprint,
  // avatarURL, bio)`. The server uses byte-equality between the
  // submitted userSignature and the row's stored user_signature as a
  // no-op fast path, so a caller that resubmits the current identity
  // record's signature will get a 200 back with no state change.
  async updateUser(userData: {
    username: string;
    avatarURL: string;
    bio: string;
    userSignature: string;
  }): Promise<api.User> {
    const formData = new URLSearchParams();
    formData.append('username', userData.username);
    formData.append('avatarURL', userData.avatarURL);
    formData.append('bio', userData.bio);
    formData.append('userSignature', userData.userSignature);

    return request<api.User>('/users/me', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },
  async deleteAccount(signature: string, note: string = ''): Promise<api.AccountRemoval> {
    const formData = new URLSearchParams();
    formData.append('signature', signature);
    formData.append('note', note);
    return request<api.AccountRemoval>('/users/me', {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString(),
    });
  },
  async createUserKeys(userId: string, email: string): Promise<{ success: boolean }> {
    const formData = new URLSearchParams();
    formData.append('email', email);

    return request<{ success: boolean }>(`/users/${userId}/keys`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async createReed(reedId: string, signature: string): Promise<SignReedResponse> {
    const formData = new URLSearchParams();
    formData.append('signature', signature);
    formData.append('reedID', reedId);

    return request('/reeds', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async getReed(userId: string, reedId: string): Promise<any> {
    return request(`/reeds/${userId}/${reedId}`, { method: 'GET' });
  },

  /**
   * GET reed with 410 handling. Account certs are returned as-is for 09;
   * callers must switch on `removal.type` and not treat account as reed.
   */
  async getReedOrRemoval(
    userId: string,
    reedId: string
  ): Promise<
    | { kind: 'reed'; reed: any }
    | { kind: 'gone'; removal: api.ReedRemoval | { type: string } }
    | { kind: 'not_found' }
  > {
    try {
      const reed = await request(`/reeds/${userId}/${reedId}`, { method: 'GET' });
      return { kind: 'reed', reed };
    } catch (err: any) {
      if (err?.status === 404) {
        return { kind: 'not_found' };
      }
      if (err?.status === 410 && err.body && typeof err.body === 'object') {
        return { kind: 'gone', removal: err.body };
      }
      throw err;
    }
  },

  // Ask the server to verify a stored (userSignature, serverSignature) pair
  // against the canonical payload for the reed. Used by e2e tests and,
  // eventually, by clients that want a second opinion on their own history.
  async verifyReed(userId: string, reedId: string, userSignature: string, serverSignature: string): Promise<void> {
    const formData = new FormData();
    formData.append('userSignature', userSignature);
    formData.append('serverSignature', serverSignature);
    return request(`/reeds/${userId}/${reedId}/verify`, {
      method: 'POST',
      body: formData,
    });
  },

  async deleteReed(
    userId: string,
    reedId: string,
    signature: string
  ): Promise<api.ReedRemoval> {
    const formData = new URLSearchParams();
    formData.append('signature', signature);
    return request<api.ReedRemoval>(`/reeds/${userId}/${reedId}`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString(),
    });
  },

  async revokeKey(
    userId: string,
    fingerprint: string,
    reason: string,
    userSignature: string
  ): Promise<api.PublicKey> {
    const formData = new URLSearchParams();
    formData.append('reason', reason);
    formData.append('userSignature', userSignature);

    return request<api.PublicKey>(`/users/${userId}/keys/${fingerprint}/revoke`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async getKeyRevocation(userId: string, fingerprint: string): Promise<api.KeyRevocation> {
    return request<api.KeyRevocation>(
      `/users/${userId}/keys/${fingerprint}/revocation`,
      { method: 'GET' }
    );
  },

  async followUser(targetUserId: string): Promise<void> {
    return request<void>(`/users/${targetUserId}/follow`, { method: 'POST' });
  },

  async unfollowUser(targetUserId: string): Promise<void> {
    return request<void>(`/users/${targetUserId}/follow`, { method: 'DELETE' });
  },

  async getPublicKey(userID: string, fingerprint: string): Promise<api.PublicKey> {
    return request<api.PublicKey>(`/users/${userID}/keys/${fingerprint}`, { method: 'GET' });
  },

  /** Fetch a historical server signing public key by fingerprint. */
  async getServerPublicKey(fingerprint: string): Promise<{ fingerprint: string; armor: string }> {
    return request<{ fingerprint: string; armor: string }>(
      `/server/keys/${fingerprint}`,
      { method: 'GET' }
    );
  },

  async addPublicKey(
    userID: string,
    publicKey: string,
    revokedKeyFingerprint: string,
    revokedKeySignature: string,
    newKeySignature: string
  ): Promise<api.PublicKey> {
    const formData = new URLSearchParams();
    formData.append('userID', userID);
    formData.append('publicKey', publicKey);
    formData.append('revokedKeyFingerprint', revokedKeyFingerprint);
    formData.append('revokedKeySignature', revokedKeySignature);
    formData.append('newKeySignature', newKeySignature);

    return request<api.PublicKey>('/keys', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  /** Unauthenticated: GET recovery challenge (unix seconds). */
  async getIdentityClaimChallenge(): Promise<api.IdentityClaimChallenge> {
    return request<api.IdentityClaimChallenge>('/recovery/identity/claim', {
      method: 'GET',
    });
  },

  /** Unauthenticated: claim own identity with challenge + nested key chain. */
  async claimOwnIdentity(body: api.IdentityClaimRequest): Promise<api.User> {
    return request<api.User>('/recovery/identity/claim', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  },

  /** Authenticated: report one peer identity with nested key chain. */
  async reportPeerIdentity(body: api.PeerIdentityRequest): Promise<api.User> {
    return request<api.User>('/recovery/identity', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  },

  /** Authenticated: report one reed's countersigned metadata. */
  async reportRecoveryReed(body: api.RecoveryReedRequest): Promise<void> {
    await request<void>('/recovery/reeds', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  },

  /** Authenticated: report a page of following user IDs (≤100). */
  async reportRecoveryFollowing(
    body: api.RecoveryFollowingRequest
  ): Promise<void> {
    await request<void>('/recovery/following', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  },

  /** Authenticated: clear ongoing_recoveries for the caller. */
  async completeRecovery(): Promise<void> {
    await request<void>('/recovery/complete', {
      method: 'POST',
    });
  },
};
