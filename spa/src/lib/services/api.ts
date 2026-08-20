import type * as api from '$lib/types/api';
import { deviceIdHeader } from './deviceId';
import { requestSigner } from './request-signer';
import { authService } from './auth';
import {
  handleDeviceMismatch,
  handleFinishRecoveryForbidden,
  isDeviceMismatchError,
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
  userSignature?: string;
  userID: string;
  userIDSignature: string;
  userIDFingerprint: string;
  inviteID?: string;
  inviteCreatorID?: string;
  inviteSecret?: string;
};

export type UserStatus = 'complete' | 'unknown' | 'ongoing';

export type UserStatusProbeResult = {
  httpStatus: number;
  status?: UserStatus;
  error?: string;
};

const BASE_URL = '/api';

async function readApiErrorMessage(res: Response): Promise<string> {
  let message =
    res.status === 401
      ? 'Authentication failed. Please check your credentials.'
      : res.status === 403
        ? 'Forbidden'
        : res.status === 409
          ? 'Username is taken'
          : res.statusText || `HTTP ${res.status}`;
  try {
    const raw = await res.text();
    if (!raw.trim()) {
      return message;
    }
    try {
      const errorData = JSON.parse(raw);
      if (typeof errorData === 'string' && errorData) {
        return errorData;
      }
      if (typeof errorData === 'object' && errorData !== null) {
        if (typeof errorData.error === 'string') {
          return errorData.error;
        }
        return raw.trim();
      }
      return raw.trim();
    } catch {
      return raw.trim();
    }
  } catch {
    return message;
  }
}

export type UsernameAvailabilityResult =
  | { available: true }
  | { available: false; taken: true; message: string }
  | { available: false; taken: false; message: string; status: number };

// Unauthenticated endpoints that don't need signing.
// `/server/keys` is public verification material — required so clients can
// verify countersignatures even when their active user key is revoked
// (e.g. mid-rotation, right after RevokeKey).
const UNAUTHENTICATED_ENDPOINTS = [
  '/users/id',
  '/users/signup',
  '/users/status',
  '/check-username',
  '/keys',
  '/server/info',
  '/server/keys',
  '/recovery/identity/claim',
  '/account-recovery/challenge',
  '/account-recovery/bootstrap',
  '/invites/check',
];

/** Signs (if needed), sends, and validates the response — shared by request()
 * (JSON body) and requestText() (plain text body); each only differs in how
 * it reads the body once this returns an ok Response. */
async function requestRaw(path: string, init?: RequestInit): Promise<Response> {
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

  const withDeviceHeaders = (init?: RequestInit): RequestInit => {
    const headers = new Headers(init?.headers);
    for (const [key, value] of Object.entries(deviceIdHeader())) {
      headers.set(key, value);
    }
    return { ...init, headers };
  };

  signedInit = withDeviceHeaders(signedInit);

  let res: Response;
  try {
    res = await fetch(`${BASE_URL}${path}`, signedInit);
  } catch (error) {
    // fetch() itself only throws for network-level failures (offline, DNS,
    // connection refused, CORS) — never for a non-2xx response, which is
    // handled separately below. The raw error here is a browser-specific
    // string like "Failed to fetch" that means nothing to a user.
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw error;
    }
    const err = new Error('Unable to reach the server. Please check your connection and try again.') as Error & {
      status?: number;
      networkError?: boolean;
    };
    err.networkError = true;
    throw err;
  }

  if (!res.ok) {
    if (res.status === 400 || res.status === 401 || res.status === 403) {
      const message = await readApiErrorMessage(res);

      if (res.status === 403) {
        if (isFinishRecoveryForbiddenMessage(message)) {
          handleFinishRecoveryForbidden();
        } else if (isDeviceMismatchError(message)) {
          handleDeviceMismatch();
        }
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

  return res;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await requestRaw(path, init);
  try {
    return (await res.json()) as T;
  } catch {
    return undefined as T;
  }
}

/** Like request(), but reads the body as plain text instead of JSON —
 * for endpoints whose response is displayed verbatim, never parsed. */
async function requestText(path: string, init?: RequestInit): Promise<string> {
  const res = await requestRaw(path, init);
  return res.text();
}

/** Shared by both username-availability checks: same request shape, same
 * 409-vs-other response branching. `signed` picks the authenticated (an
 * existing user checking a rename) vs. anonymous (signup) request path. */
async function checkUsername(
  path: string,
  formFields: Record<string, string>,
  signed: boolean,
  signal?: AbortSignal,
): Promise<UsernameAvailabilityResult> {
  const body = new URLSearchParams();
  for (const [key, value] of Object.entries(formFields)) {
    if (value) {
      body.append(key, value);
    }
  }

  let init: RequestInit = {
    method: 'POST',
    body,
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    signal,
  };
  if (signed) {
    init = await requestSigner.signRequest(`${BASE_URL}${path}`, init);
  }

  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  for (const [key, value] of Object.entries(deviceIdHeader())) {
    headers.set(key, value);
  }

  const res = await fetch(`${BASE_URL}${path}`, { ...init, headers });

  if (res.ok) {
    return { available: true };
  }

  const message = await readApiErrorMessage(res);
  if (res.status === 409) {
    return { available: false, taken: true, message };
  }
  return { available: false, taken: false, message, status: res.status };
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

  async getUserID(): Promise<{ userID: string; signature: string; fingerprint: string }> {
    return request<{ userID: string; signature: string; fingerprint: string }>('/users/id', {
      method: 'GET',
    });
  },

  async checkUsernameAvailability(
    username: string,
    extraFormFields: Record<string, string> = {},
    signal?: AbortSignal,
  ): Promise<UsernameAvailabilityResult> {
    return checkUsername('/check-username', { username, ...extraFormFields }, false, signal);
  },

  /** Authenticated counterpart for the profile-edit rename checker — no
   * invite/signup-mode gate, since the caller already has an account. */
  async checkUsernameAvailabilityForRename(
    username: string,
    signal?: AbortSignal,
  ): Promise<UsernameAvailabilityResult> {
    return checkUsername('/users/me/check-username', { username }, true, signal);
  },

  async signup(input: SignupInput): Promise<api.User> {
    const formData = new URLSearchParams();
    formData.append('username', input.username);
    formData.append('publicKey', input.publicKey);
    formData.append('signature', input.signature);
    formData.append('userID', input.userID);
    formData.append('userIDSignature', input.userIDSignature);
    formData.append('userIDFingerprint', input.userIDFingerprint);
    if (input.userSignature) {
      formData.append('userSignature', input.userSignature);
    }
    if (input.inviteID) {
      formData.append('inviteID', input.inviteID);
    }
    if (input.inviteCreatorID) {
      formData.append('inviteCreatorID', input.inviteCreatorID);
    }
    if (input.inviteSecret) {
      formData.append('inviteSecret', input.inviteSecret);
    }

    return request<api.User>('/users/signup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async checkInvite(creatorId: string, id: string, secret: string): Promise<{ valid: boolean }> {
    const q = new URLSearchParams({ uid: creatorId, iid: id, secret });
    return request<{ valid: boolean }>(`/invites/check?${q}`, {
      method: 'GET'
    });
  },

  async createInvite(body: {
    id: string;
    tokenHash: string;
    createdAt: string;
    grantedRole?: 'user' | 'admin';
    userSignature: api.UserSignature;
  }): Promise<{
    id: string;
    tokenHash: string;
    createdAt: string;
    grantedRole: 'user' | 'admin';
    userSignature: api.UserSignature;
    serverSignature: api.ServerSignature;
  }> {
    return request('/invites', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  },

  async getInviteStatus(id: string): Promise<{
    id: string;
    createdAt: string;
    status: 'pending' | 'claimed' | 'revoked';
    claimedAt: string | null;
    claimedBy: string | null;
    revokedAt: string | null;
  }> {
    return request(`/invites/${encodeURIComponent(id)}`, { method: 'GET' });
  },

  async revokeInvite(id: string): Promise<void> {
    await request(`/invites/${encodeURIComponent(id)}`, { method: 'DELETE' });
  },

  async listFederationInvitations(): Promise<api.FederationInvitation[]> {
    return request<api.FederationInvitation[]>('/federation/invitations', { method: 'GET' });
  },

  async createFederationInvitation(
    name: string,
    remotePublicKeyArmor: string,
  ): Promise<api.FederationInvitationCreateResponse> {
    return request<api.FederationInvitationCreateResponse>('/federation/invitations', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, remotePublicKeyArmor }),
    });
  },

  async revokeFederationInvitation(inviteId: string): Promise<{ inviteId: string; status: 'canceled' }> {
    return request(`/federation/invitations/${encodeURIComponent(inviteId)}/revoke`, {
      method: 'POST',
    });
  },

  // Named "attempt", not "accept" — pasting the string only starts an
  // attempt at redeeming the invitation; nothing is confirmed until the
  // initiator's connect callback verifies it.
  async attemptFederationConnection(connectionString: string): Promise<api.FederationAttemptResponse> {
    return request<api.FederationAttemptResponse>('/federation/attempt', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ connectionString }),
    });
  },

  async listFederationServers(): Promise<api.FederationServer[]> {
    return request<api.FederationServer[]>('/federation/servers', { method: 'GET' });
  },

  /** Plain text, one log line per line — displayed verbatim, never parsed. */
  async getFederationServerLogs(serverId: string): Promise<string> {
    return requestText(`/federation/servers/${encodeURIComponent(serverId)}/logs`, {
      method: 'GET',
    });
  },

  /** null when this server was the responder (no local invitation row). */
  async getFederationServerInvitation(
    serverId: string
  ): Promise<api.FederationInvitation | null> {
    return request<api.FederationInvitation | null>(
      `/federation/servers/${encodeURIComponent(serverId)}/invitation`,
      { method: 'GET' }
    );
  },

  async approveFederationServer(serverId: string): Promise<void> {
    await request(`/federation/servers/${encodeURIComponent(serverId)}/approve`, {
      method: 'POST',
    });
  },

  async rejectFederationServer(serverId: string, reason: string): Promise<void> {
    await request(`/federation/servers/${encodeURIComponent(serverId)}/reject`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reason }),
    });
  },

  async whoami() {
    return request<{ id: string; username: string }>('/users/me', { method: 'GET' });
  },

  async getUserProfile(userId: string): Promise<api.User> {
    return request<api.User>(`/users/${userId}/profile`, { method: 'GET' });
  },

  async getUserInfo(userId: string): Promise<api.UserInfo> {
    return request<api.UserInfo>(`/users/${userId}/info`, { method: 'GET' });
  },

  async searchUsers(query: string, limit?: number): Promise<{ users: { id: string; username: string }[] }> {
    const params = new URLSearchParams({ q: query });
    if (limit != null) params.set('limit', String(limit));
    return request<{ users: { id: string; username: string }[] }>(
      `/users/search?${params}`,
      { method: 'GET' },
    );
  },

  async getUserProfileWithStatus(userId: string): Promise<{
    status: number;
    user?: api.User;
    removal?: api.AccountRemoval;
  }> {
    try {
      const user = await request<api.User>(`/users/${userId}/profile`, { method: 'GET' });
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

  async getUserInfoWithStatus(userId: string): Promise<{
    status: number;
    info?: api.UserInfo;
    removal?: api.AccountRemoval;
  }> {
    try {
      const info = await request<api.UserInfo>(`/users/${userId}/info`, { method: 'GET' });
      return { status: 200, info };
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
  // Callers must pass the complete post-edit tuple (username, bio) plus
  // a base64(armored PGP) detached signature over
  // `buildUserIdentityPayload(username, fingerprint, bio)`. The server
  // uses byte-equality between the submitted userSignature and the row's
  // stored user_signature as a no-op fast path, so a caller that
  // resubmits the current identity record's signature will get a 200 back
  // with no state change.
  async updateUser(userData: {
    username: string;
    bio: string;
    userSignature: string;
  }): Promise<api.User> {
    const formData = new URLSearchParams();
    formData.append('username', userData.username);
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

  async createReed(
    reedId: string,
    signature: string,
    fields: {
      content: string;
      echoing?: string;
      replying?: string;
      previousID?: string;
    }
  ): Promise<SignReedResponse> {
    const formData = new URLSearchParams();
    formData.append('signature', signature);
    formData.append('reedID', reedId);
    formData.append('content', fields.content ?? '');
    if (fields.echoing) formData.append('echoing', fields.echoing);
    if (fields.replying) formData.append('replying', fields.replying);
    if (fields.previousID) formData.append('previousID', fields.previousID);

    return request('/reeds', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async getReedEchoCount(userId: string, reedId: string): Promise<number> {
    return request(`/reeds/${userId}/${reedId}/echoes`, { method: 'GET' });
  },

  async listReplies(
    userId: string,
    reedId: string,
    opts?: { limit?: number; before?: string },
  ): Promise<api.ReplyListResponse> {
    const params = new URLSearchParams();
    if (opts?.limit != null) params.set('limit', String(opts.limit));
    if (opts?.before) params.set('before', opts.before);
    const qs = params.toString();
    const path = `/reeds/${userId}/${reedId}/replies${qs ? `?${qs}` : ''}`;
    return request<api.ReplyListResponse>(path, { method: 'GET' });
  },

  async getReed(userId: string, reedId: string): Promise<any> {
    return request(`/reeds/${userId}/${reedId}`, { method: 'GET' });
  },

  async listEchoers(
    userId: string,
    reedId: string,
    opts?: { limit?: number; before?: string },
  ): Promise<api.EchoerListResponse> {
    const params = new URLSearchParams();
    if (opts?.limit != null) params.set('limit', String(opts.limit));
    if (opts?.before) params.set('before', opts.before);
    const qs = params.toString();
    const path = `/reeds/${userId}/${reedId}/chorus${qs ? `?${qs}` : ''}`;
    return request<api.EchoerListResponse>(path, { method: 'GET' });
  },

  /**
   * Listing ripples requires proving possession of the parent reed —
   * `serverSignatureArmor` (the reed's own base64 server-signature armor,
   * only visible on a copy of the reed itself) is sent as the request
   * body. QUERY is the standard-track HTTP method for a safe, body-bearing
   * request; if a given environment doesn't support it end-to-end, swap
   * the method/path below for the commented POST .../ripples/proof
   * fallback (kept in sync with main.go's route registration).
   */
  async listRipples(
    userId: string,
    reedId: string,
    serverSignatureArmor: string,
    opts?: { limit?: number; before?: string },
  ): Promise<api.RippleListResponse> {
    const params = new URLSearchParams();
    if (opts?.limit != null) params.set('limit', String(opts.limit));
    if (opts?.before) params.set('before', opts.before);
    const qs = params.toString();
    const path = `/reeds/${userId}/${reedId}/ripples${qs ? `?${qs}` : ''}`;
    return request<api.RippleListResponse>(path, { method: 'QUERY', body: serverSignatureArmor });
    // Fallback if QUERY isn't supported end-to-end:
    // const path = `/reeds/${userId}/${reedId}/ripples/proof${qs ? `?${qs}` : ''}`;
    // return request<api.RippleListResponse>(path, { method: 'POST', body: serverSignatureArmor });
  },

  /** Posting a ripple requires the same proof of possession of the parent
   * reed as listing them — see listRipples and the server's
   * checkReedPossession. `proof` is the reed's base64 server-signature
   * armor. */
  async postRipple(
    userId: string,
    reedId: string,
    fields: {
      content: string;
      threadID: string;
      replyingTo?: string;
      proof: string;
      fingerprint: string;
      userSignature: string;
    }
  ): Promise<api.Ripple> {
    return request<api.Ripple>(`/reeds/${userId}/${reedId}/ripples`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        content: fields.content,
        threadID: fields.threadID,
        replyingTo: fields.replyingTo ?? null,
        proof: fields.proof,
        fingerprint: fields.fingerprint,
        userSignature: fields.userSignature,
      }),
    });
  },

  async deleteRipple(rippleHash: string): Promise<void> {
    return request<void>(`/ripples/${rippleHash}`, { method: 'DELETE' });
  },

  async listFollowing(
    userId: string,
    opts?: { limit?: number; before?: string },
  ): Promise<api.FollowListResponse> {
    const params = new URLSearchParams();
    if (opts?.limit != null) params.set('limit', String(opts.limit));
    if (opts?.before) params.set('before', opts.before);
    const qs = params.toString();
    const path = `/users/${userId}/following${qs ? `?${qs}` : ''}`;
    return request<api.FollowListResponse>(path, { method: 'GET' });
  },

  async listFollowers(
    userId: string,
    opts?: { limit?: number; before?: string },
  ): Promise<api.FollowListResponse> {
    const params = new URLSearchParams();
    if (opts?.limit != null) params.set('limit', String(opts.limit));
    if (opts?.before) params.set('before', opts.before);
    const qs = params.toString();
    const path = `/users/${userId}/followers${qs ? `?${qs}` : ''}`;
    return request<api.FollowListResponse>(path, { method: 'GET' });
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

  async likeReed(
    authorId: string,
    reedId: string,
    signature: string,
    fingerprint: string
  ): Promise<api.ReedLike> {
    const formData = new URLSearchParams();
    formData.append('signature', signature);
    formData.append('fingerprint', fingerprint);
    return request<api.ReedLike>(`/reeds/${authorId}/${reedId}/like`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString(),
    });
  },

  async unlikeReed(authorId: string, reedId: string): Promise<void> {
    return request<void>(`/reeds/${authorId}/${reedId}/like`, { method: 'DELETE' });
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

    const key = await request<api.PublicKey>(`/users/${userId}/keys/${fingerprint}/revoke`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
    return { ...key, armor: atob(key.armor) };
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
    const key = await request<api.PublicKey>(`/users/${userID}/keys/${fingerprint}`, { method: 'GET' });
    return { ...key, armor: atob(key.armor) };
  },

  /** Fetch a historical server signing public key by fingerprint (cached in publicKeys). */
  async getServerPublicKey(fingerprint: string): Promise<{ fingerprint: string; armor: string }> {
    const fp = fingerprint.trim();
    const { dbService } = await import('./db');
    const cached = await dbService.get<{ fingerprint: string; armor: string }>('publicKeys', fp);
    if (cached?.armor) {
      return { fingerprint: cached.fingerprint, armor: cached.armor };
    }

    const wireKey = await request<{ fingerprint: string; armor: string }>(
      `/server/keys/${fp}`,
      { method: 'GET' }
    );
    const key = { fingerprint: wireKey.fingerprint, armor: atob(wireKey.armor) };
    try {
      const { allowUnsigned } = await import('$lib/verifiers');
      await dbService.put('publicKeys', key, allowUnsigned);
    } catch (error) {
      console.error('Failed to cache server public key:', error);
    }
    return key;
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

    const key = await request<api.PublicKey>('/keys', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
    return { ...key, armor: atob(key.armor) };
  },

  /** Unauthenticated: GET account-recovery challenge (unix seconds). */
  async getAccountRecoveryChallenge(): Promise<api.AccountRecoveryChallenge> {
    return request<api.AccountRecoveryChallenge>('/account-recovery/challenge', {
      method: 'GET',
    });
  },

  /** Unauthenticated: prove active key possession; returns bootstrap payload. */
  async bootstrapAccountRecovery(
    body: api.AccountRecoveryBootstrapRequest
  ): Promise<api.AccountRecoveryBootstrapResponse> {
    return request<api.AccountRecoveryBootstrapResponse>('/account-recovery/bootstrap', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
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

  /** Authenticated: bind this origin as the sole active device. */
  async bindDevice(): Promise<string> {
    return request<string>('/users/device', {
      method: 'POST',
    });
  },

  /** Authenticated: report a successful local keys-only or full export. */
  async recordBackup(kind: 'identity' | 'full'): Promise<void> {
    await request('/users/me/backup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ kind }),
    });
  },
};
