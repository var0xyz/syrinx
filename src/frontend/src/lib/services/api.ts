import type * as api from '$lib/types/api';
import { deviceIdHeader } from './deviceId';
import { requestSigner } from './request-signer';
import { authService } from './auth';
import { serverKeyProofHeader } from './serverKeyTrust';
import { verifyResponseEnvelope } from './responseVerifier';
import { appendFingerprint, parseKeyId } from '$lib/utils/identityRef';
import {
  handleDeviceMismatch,
  handleFinishRecoveryForbidden,
  isDeviceMismatchError,
  isFinishRecoveryForbiddenMessage,
} from './restoreFlow';
import {
  create,
  fromBinary,
  isMessage,
  toBinary,
  type DescMessage,
  type MessageInitShape,
  type MessageShape,
} from '@bufbuild/protobuf';
import {
  AccountRemovalCertSchema,
  BlockCertSchema,
  ErrorSchema,
  ReedRemovalCertSchema,
  RippleSchema,
  ServerSignatureSchema,
  ThreadRemovalSchema,
} from '$lib/proto/common_pb';
import {
  AddPublicKeyRequestSchema,
  BindDeviceResponseSchema,
  BlockListResponseSchema,
  BlockRequestSchema,
  CheckUsernameRequestSchema,
  CreateVouchRequestSchema,
  DeleteAccountRequestSchema,
  FollowListResponseSchema,
  KeyRevocationResponseSchema,
  PublicKeySchema,
  RecordBackupRequestSchema,
  ServerInfoSchema,
  SignupRequestSchema,
  UpdateUserRequestSchema,
  UserIDResponseSchema,
  UserInfoSchema,
  UserSchema,
  UserSearchResponseSchema,
  VouchListResponseSchema,
  VouchSchema,
  WithdrawVouchRequestSchema,
  type ServerInfo,
  type ServerKeyRevocation,
  type UserIDResponse,
  type UserSearchResponse,
} from '$lib/proto/identity_pb';
import {
  CreateThreadRequestSchema,
  DeleteRippleRequestSchema,
  EchoCountResponseSchema,
  EchoerListResponseSchema,
  LikeCertSchema,
  LikeRequestSchema,
  PinRequestSchema,
  PostRippleRequestSchema,
  ReceivedRippleListResponseSchema,
  RemovalRequestSchema,
  ReplyListResponseSchema,
  RippleListResponseSchema,
  SignReedRequestSchema,
  ThreadSignaturesSchema,
  UnlikeRequestSchema,
} from '$lib/proto/reed_pb';
import {
  CreateInviteRequestSchema,
  InviteCheckResponseSchema,
  InviteSchema,
  InviteStatusSchema,
  type Invite,
  type InviteStatus,
} from '$lib/proto/invites_pb';
import {
  AccountRecoveryBootstrapRequestSchema,
  AccountRecoveryBootstrapResponseSchema,
  ChallengeResponseSchema,
  ClaimIdentityRequestSchema,
  PeerIdentityRequestSchema,
  RecoveryFollowingRequestSchema,
  RecoveryProfileSchema,
  RecoveryReedRequestSchema,
  UserStatusResponseSchema,
} from '$lib/proto/recovery_pb';
import {
  FederationActionResponseSchema,
  FederationAttemptRequestSchema,
  FederationAttemptResponseSchema,
  FederationAttemptSchema,
  FederationAttemptStatusSchema,
  FederationCreateRequestSchema,
  FederationCreateResponseSchema,
  FederationInvitationListSchema,
  FederationInvitationResponseSchema,
  FederationListSchema,
  FederationLogsSchema,
  FederationReasonRequestSchema,
  FederationServerListSchema,
} from '$lib/proto/federation_pb';

export const PROTOBUF_CONTENT_TYPE = 'application/x-protobuf';

/** The certificate a refusal or removal is backed by: what an Error body
 * carries as its detail. */
export type RefusalDetail = api.AccountRemoval | api.ReedRemoval | api.ThreadRemoval | api.BlockCert;

/** A failed request: status and, when the server sent one, the detail. */
export type ApiError = Error & { status?: number; detail?: RefusalDetail; networkError?: boolean; tampered?: boolean };

export type UserStatus = 'complete' | 'unknown' | 'ongoing';

export type UserStatusProbeResult = {
  httpStatus: number;
  status?: UserStatus;
  error?: string;
};

const BASE_URL = '/api';

/** Called with every block certificate a request was refused with. Set at
 * startup: api.ts can't import the block service without a cycle. */
export type BlockedReporter = (cert: api.BlockCert) => void;
let blockedReporter: BlockedReporter | null = null;
export function setBlockedReporter(reporter: BlockedReporter): void {
  blockedReporter = reporter;
}

/** Decodes a protobuf response body as schema. */
export async function readMessage<Desc extends DescMessage>(res: Response, schema: Desc): Promise<MessageShape<Desc>> {
  return fromBinary(schema, new Uint8Array(await res.clone().arrayBuffer()));
}

/** A request whose body is init, encoded as schema. */
function protoBody<Desc extends DescMessage>(method: string, schema: Desc, init: MessageInitShape<Desc>): RequestInit {
  return {
    method,
    headers: { 'Content-Type': PROTOBUF_CONTENT_TYPE },
    body: toBinary(schema, create(schema, init)) as Uint8Array<ArrayBuffer>,
  };
}

/** A decoded Error body, or null when the body is empty or not an Error. */
async function readApiError(res: Response): Promise<{ message: string; detail?: RefusalDetail } | null> {
  try {
    const bytes = new Uint8Array(await res.arrayBuffer());
    if (bytes.length === 0) return null;
    const error = fromBinary(ErrorSchema, bytes);
    return { message: error.message, detail: error.detail.value };
  } catch {
    return null;
  }
}

function fallbackErrorMessage(res: Response): string {
  return res.status === 401
    ? 'Authentication failed. Please check your credentials.'
    : res.status === 403
      ? 'Forbidden'
      : res.status === 409
        ? 'Username is taken'
        : res.statusText || `HTTP ${res.status}`;
}

async function readApiErrorMessage(res: Response): Promise<string> {
  const error = await readApiError(res);
  return error?.message || fallbackErrorMessage(res);
}

export type UsernameAvailabilityResult =
  | { available: true }
  | { available: false; taken: true; message: string }
  | { available: false; taken: false; message: string; status: number };

/** userId must already be canonical (userID@serverID); fingerprint may be
 * bare or already a full canonical key id — passed through as-is either way. */
export function canonicalKeyId(userId: string, fingerprint: string): string {
  return parseKeyId(fingerprint) ? fingerprint : appendFingerprint(userId, fingerprint);
}

/**
 * Splits a canonical reed id (authorID@serverID/uuid) into the two URL
 * segments the server's /reeds/{userID}/{reedID} route expects. Mirrors
 * handlers.go's route-boundary reconstruction in reverse.
 */
function splitReedId(reedId: string): { userId: string; bareId: string } {
  const parsed = parseKeyId(reedId);
  if (!parsed) throw new Error(`Invalid canonical reed id: ${reedId}`);
  return { userId: `${parsed.userId}@${parsed.serverId}`, bareId: parsed.fingerprint };
}

// Unauthenticated endpoints that don't need signing.
// `/keys` (exact) is POST AddPublicKey — unauthenticated because a brand
// new signup key has no session yet. Every other /keys/{id}... route
// (GetKey, revoke, revocation) requires the caller's signature, mirroring
// signatureAuthMiddleware's excludePaths in middlewares.go: only the bare
// path is excluded, not the whole prefix.
const UNAUTHENTICATED_ENDPOINTS = [
  '/users/id',
  '/users/signup',
  '/users/status',
  '/check-username',
  '/server/info',
  '/recovery/identity/claim',
  '/account-recovery/challenge',
  '/account-recovery/bootstrap',
  '/invites/check',
];
const UNAUTHENTICATED_EXACT_PATHS = ['/keys'];

/** Signs (if needed), sends, and validates the response; request() then
 * decodes the body of an ok Response. */
async function requestRaw(
  path: string,
  init?: RequestInit,
  opts: { skipEnvelope?: boolean } = {}
): Promise<Response> {
  let signedInit = init;

  // Check if this is an authenticated request
  const isAuthenticated =
    !UNAUTHENTICATED_ENDPOINTS.some(endpoint => path.startsWith(endpoint)) &&
    !UNAUTHENTICATED_EXACT_PATHS.some(endpoint => path === endpoint);

  if (isAuthenticated) {
    try {
      // Check if request signer is initialized
      if (!requestSigner.isInitialized()) {
        // Try to get auth data from auth service
        const keyId = authService.getActiveKeyId();
        if (!keyId) {
          throw new Error('Cannot sign request: no active key available');
        }

        await requestSigner.initializeWorker(keyId);
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
    if (!isAuthenticated) {
      for (const [key, value] of Object.entries(serverKeyProofHeader())) {
        headers.set(key, value);
      }
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
    const err: ApiError = new Error('Unable to reach the server. Please check your connection and try again.');
    err.networkError = true;
    throw err;
  }

  // Every response is signed (see responseSignerMiddleware) — a missing
  // or invalid Signature is treated the same: fail closed.
  if (!opts.skipEnvelope && !(await verifyResponseEnvelope(res))) {
    const err: ApiError = new Error('Server response failed signature verification.');
    err.tampered = true;
    throw err;
  }

  if (!res.ok) {
    const error = await readApiError(res);
    if (res.status === 403 && isMessage(error?.detail, BlockCertSchema)) {
      blockedReporter?.(error.detail);
      const err: ApiError = new Error('Blocked');
      err.status = 403;
      err.detail = error.detail;
      throw err;
    }
    const message = error?.message || fallbackErrorMessage(res);
    if (res.status === 400 || res.status === 401 || res.status === 403) {
      if (res.status === 403) {
        if (isFinishRecoveryForbiddenMessage(message)) {
          handleFinishRecoveryForbidden();
        } else if (isDeviceMismatchError(message)) {
          handleDeviceMismatch();
        }
      }

      throw new Error(message);
    }
    const err: ApiError = new Error(error?.message || `HTTP ${res.status}`);
    err.status = res.status;
    err.detail = error?.detail;
    throw err;
  }

  return res;
}

/** Sends the request and decodes an ok response as schema. */
async function request<Desc extends DescMessage>(path: string, init: RequestInit | undefined, schema: Desc): Promise<MessageShape<Desc>> {
  return readMessage(await requestRaw(path, init), schema);
}

/** Sends the request, ignoring any response body. */
async function send(path: string, init?: RequestInit): Promise<void> {
  await requestRaw(path, init);
}

/** Shared by both username-availability checks: same request shape, same
 * 409-vs-other response branching. `signed` picks the authenticated (an
 * existing user checking a rename) vs. anonymous (signup) request path. */
async function checkUsername(
  path: string,
  fields: MessageInitShape<typeof CheckUsernameRequestSchema>,
  signed: boolean,
  signal?: AbortSignal,
): Promise<UsernameAvailabilityResult> {
  let init: RequestInit = { ...protoBody('POST', CheckUsernameRequestSchema, fields), signal };
  if (signed) {
    init = await requestSigner.signRequest(`${BASE_URL}${path}`, init);
  }

  const headers = new Headers(init.headers);
  for (const [key, value] of Object.entries(deviceIdHeader())) {
    headers.set(key, value);
  }
  if (!signed) {
    for (const [key, value] of Object.entries(serverKeyProofHeader())) {
      headers.set(key, value);
    }
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

/** A key's revocation: a user key's cert or a server key's revocation. */
export type AnyKeyRevocation = api.KeyRevocation | ServerKeyRevocation;

/** Decodes a GET /keys/{id}/revocation response read without the envelope
 * check (see getKeyRevocationUnverified). */
export async function readKeyRevocation(res: Response): Promise<AnyKeyRevocation | undefined> {
  return (await readMessage(res, KeyRevocationResponseSchema)).revocation.value;
}

/** Decodes a GET /keys/{id} response read without the envelope check. */
export async function readPublicKey(res: Response): Promise<api.PublicKey> {
  return readMessage(res, PublicKeySchema);
}

/** Decodes a GET /server/info response. */
export async function readServerInfo(res: Response): Promise<ServerInfo> {
  return readMessage(res, ServerInfoSchema);
}

/** limit and before as query parameters; before is a unix-seconds cursor,
 * except for ripples, whose cursor is opaque. */
function pageQuery(opts?: { limit?: number; before?: number | string }): string {
  const params = new URLSearchParams();
  if (opts?.limit != null) params.set('limit', String(opts.limit));
  if (opts?.before) params.set('before', String(opts.before));
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

export const apiService = {
  /**
   * Unauthenticated probe: POST countersigned profile, branch on HTTP status.
   * Does not throw on 404/409 — those are meaningful probe outcomes.
   */
  async probeUserStatus(profile: MessageInitShape<typeof RecoveryProfileSchema>): Promise<UserStatusProbeResult> {
    const res = await fetch(`${BASE_URL}/users/status`, {
      ...protoBody('POST', RecoveryProfileSchema, profile),
      headers: { 'Content-Type': PROTOBUF_CONTENT_TYPE, ...serverKeyProofHeader() },
    });

    if (res.status === 200 || res.status === 404 || res.status === 409) {
      try {
        const { status } = await readMessage(res, UserStatusResponseSchema);
        if (status === 'complete' || status === 'unknown' || status === 'ongoing') {
          return { httpStatus: res.status, status };
        }
      } catch {
        // fall through
      }
      return { httpStatus: res.status };
    }

    if (res.status === 400) {
      return { httpStatus: 400, error: (await readApiError(res))?.message || res.statusText || 'Bad Request' };
    }

    return {
      httpStatus: res.status,
      error: `Unexpected server response: HTTP ${res.status}`,
    };
  },

  async getUserID(): Promise<UserIDResponse> {
    return request('/users/id', { method: 'GET' }, UserIDResponseSchema);
  },

  async checkUsernameAvailability(
    username: string,
    invite: { inviteId?: string; inviteSecret?: string } = {},
    signal?: AbortSignal,
  ): Promise<UsernameAvailabilityResult> {
    return checkUsername('/check-username', { username, ...invite }, false, signal);
  },

  /** Authenticated counterpart for the profile-edit rename checker — no
   * invite/signup-mode gate, since the caller already has an account. */
  async checkUsernameAvailabilityForRename(
    username: string,
    signal?: AbortSignal,
  ): Promise<UsernameAvailabilityResult> {
    return checkUsername('/users/me/check-username', { username }, true, signal);
  },

  async signup(input: MessageInitShape<typeof SignupRequestSchema>): Promise<api.User> {
    return request('/users/signup', protoBody('POST', SignupRequestSchema, input), UserSchema);
  },

  async checkInvite(id: string, secret: string): Promise<{ valid: boolean }> {
    const q = new URLSearchParams({ id, secret });
    return request(`/invites/check?${q}`, { method: 'GET' }, InviteCheckResponseSchema);
  },

  async createInvite(body: MessageInitShape<typeof CreateInviteRequestSchema>): Promise<Invite> {
    return request('/invites', protoBody('POST', CreateInviteRequestSchema, body), InviteSchema);
  },

  // id carries a literal "/" (userID@serverID/reedID) — no encodeURIComponent,
  // same convention as canonicalKeyId above, or the signed path won't match.
  async getInviteStatus(id: string): Promise<InviteStatus> {
    return request(`/invites/${id}`, { method: 'GET' }, InviteStatusSchema);
  },

  async revokeInvite(id: string): Promise<void> {
    await send(`/invites/${id}`, { method: 'DELETE' });
  },

  async listFederationInvitations(): Promise<api.FederationInvitation[]> {
    return (await request('/federation/invitations', { method: 'GET' }, FederationInvitationListSchema)).invitations;
  },

  /** Invitations + attempts + servers together — the mesh tab's combined view. */
  async listFederation(): Promise<api.FederationList> {
    return request('/federation/list', { method: 'GET' }, FederationListSchema);
  },

  async createFederationInvitation(
    name: string,
    remotePublicKeyArmor: string,
  ): Promise<api.FederationCreateResponse> {
    return request(
      '/federation/invitations',
      protoBody('POST', FederationCreateRequestSchema, { name, remotePublicKeyArmor }),
      FederationCreateResponseSchema,
    );
  },

  async revokeFederationInvitation(inviteId: string): Promise<api.FederationActionResponse> {
    return request(
      `/federation/invitations/${encodeURIComponent(inviteId)}/revoke`,
      { method: 'POST' },
      FederationActionResponseSchema,
    );
  },

  // Named "attempt", not "accept" — pasting the string only starts an
  // attempt at redeeming the invitation; nothing is confirmed until the
  // initiator's connect callback verifies it.
  async attemptFederationConnection(connectionString: string): Promise<api.FederationAttemptStatus> {
    return request(
      '/federation/attempt',
      protoBody('POST', FederationAttemptRequestSchema, { connectionString }),
      FederationAttemptStatusSchema,
    );
  },

  async listFederationServers(): Promise<api.FederationServer[]> {
    return (await request('/federation/servers', { method: 'GET' }, FederationServerListSchema)).servers;
  },

  /** One log line per line — displayed verbatim, never parsed. */
  async getFederationServerLogs(serverId: string): Promise<string> {
    const logs = await request(`/federation/servers/${encodeURIComponent(serverId)}/logs`, { method: 'GET' }, FederationLogsSchema);
    return logs.text;
  },

  /** undefined when this server was the responder (no local invitation row). */
  async getFederationServerInvitation(serverId: string): Promise<api.FederationInvitation | undefined> {
    const resp = await request(
      `/federation/servers/${encodeURIComponent(serverId)}/invitation`,
      { method: 'GET' },
      FederationInvitationResponseSchema,
    );
    return resp.invitation;
  },

  /** undefined if no attempt row is found (shouldn't happen for a real server). */
  async getFederationServerAttempt(serverId: string): Promise<api.FederationAttempt | undefined> {
    const resp = await request(
      `/federation/servers/${encodeURIComponent(serverId)}/attempt`,
      { method: 'GET' },
      FederationAttemptResponseSchema,
    );
    return resp.attempt;
  },

  async getFederationAttempt(attemptId: string): Promise<api.FederationAttempt> {
    return request(`/federation/attempts/${encodeURIComponent(attemptId)}`, { method: 'GET' }, FederationAttemptSchema);
  },

  /** One log line per line — displayed verbatim, never parsed. */
  async getFederationAttemptLogs(attemptId: string): Promise<string> {
    const logs = await request(`/federation/attempts/${encodeURIComponent(attemptId)}/logs`, { method: 'GET' }, FederationLogsSchema);
    return logs.text;
  },

  async approveFederationAttempt(attemptId: string): Promise<api.FederationActionResponse> {
    return request(
      `/federation/attempts/${encodeURIComponent(attemptId)}/approve`,
      { method: 'POST' },
      FederationActionResponseSchema,
    );
  },

  async rejectFederationAttempt(attemptId: string, reason: string): Promise<void> {
    await send(
      `/federation/attempts/${encodeURIComponent(attemptId)}/reject`,
      protoBody('POST', FederationReasonRequestSchema, { reason }),
    );
  },

  /** Stages a disconnect — the peer stays connected until a second,
   * different admin calls confirmFederationServerDisconnect. */
  async requestFederationServerDisconnect(serverId: string, reason: string): Promise<void> {
    await send(
      `/federation/servers/${encodeURIComponent(serverId)}/revoke`,
      protoBody('POST', FederationReasonRequestSchema, { reason }),
    );
  },

  /** Finalizes a staged disconnect request. Server 403s if the confirming
   * admin is the same one who requested it (root exempt). */
  async confirmFederationServerDisconnect(serverId: string): Promise<void> {
    await send(`/federation/servers/${encodeURIComponent(serverId)}/revoke/confirm`, { method: 'POST' });
  },

  /** Withdraws a staged disconnect request before it's confirmed. */
  async cancelFederationServerDisconnect(serverId: string): Promise<void> {
    await send(`/federation/servers/${encodeURIComponent(serverId)}/revoke/cancel`, { method: 'POST' });
  },

  async purgeFederationServer(serverId: string): Promise<void> {
    await send(`/federation/servers/${encodeURIComponent(serverId)}/purge`, { method: 'POST' });
  },

  async getUserProfile(userId: string): Promise<api.User> {
    return request(`/users/${userId}/profile`, { method: 'GET' }, UserSchema);
  },

  async getUserInfo(userId: string): Promise<api.UserInfo> {
    return request(`/users/${userId}/info`, { method: 'GET' }, UserInfoSchema);
  },

  /** Username search across this server and its peers; after is the
   * previous page's opaque nextCursor. */
  async searchUsers(query: string, limit?: number, after?: string): Promise<UserSearchResponse> {
    const params = new URLSearchParams({ q: query });
    if (limit != null) params.set('limit', String(limit));
    if (after) params.set('after', after);
    return request(`/users/search?${params}`, { method: 'GET' }, UserSearchResponseSchema);
  },

  async getUserProfileWithStatus(userId: string): Promise<{
    status: number;
    user?: api.User;
    removal?: api.AccountRemoval;
    block?: api.BlockCert;
  }> {
    try {
      const user = await request(`/users/${userId}/profile`, { method: 'GET' }, UserSchema);
      return { status: 200, user };
    } catch (error) {
      return statusOf(error as ApiError);
    }
  },

  async getUserInfoWithStatus(userId: string): Promise<{
    status: number;
    info?: api.UserInfo;
    removal?: api.AccountRemoval;
    block?: api.BlockCert;
  }> {
    try {
      const info = await request(`/users/${userId}/info`, { method: 'GET' }, UserInfoSchema);
      return { status: 200, info };
    } catch (error) {
      return statusOf(error as ApiError);
    }
  },

  // updateUser mints a fresh signed identity record for the caller.
  // Every accepted request is a *full* replacement of the signed
  // user-authored fields — partial patches are no longer supported.
  // Callers must pass the complete post-edit tuple (username, bio) plus
  // an armored PGP detached signature over
  // `buildUserIdentityPayload(username, fingerprint, bio)`. The server
  // uses byte-equality between the submitted userSignature and the row's
  // stored user_signature as a no-op fast path, so a caller that
  // resubmits the current identity record's signature will get a 200 back
  // with no state change.
  async updateUser(userData: MessageInitShape<typeof UpdateUserRequestSchema>): Promise<api.User> {
    return request('/users/me', protoBody('PUT', UpdateUserRequestSchema, userData), UserSchema);
  },
  async deleteAccount(signature: string, note: string = ''): Promise<api.AccountRemoval> {
    return request('/users/me', protoBody('DELETE', DeleteAccountRequestSchema, { signature, note }), AccountRemovalCertSchema);
  },

  async createReed(
    reedId: string,
    signature: string,
    fields: {
      echoing?: string;
      replyingTo?: string;
      previousId?: string;
      tags?: string[];
      mentions?: string[];
    }
  ): Promise<api.ServerSignature> {
    return request(
      '/reeds',
      protoBody('POST', SignReedRequestSchema, { signature, reedId, ...fields }),
      ServerSignatureSchema,
    );
  },

  async getReedEchoCount(reedId: string): Promise<number> {
    const { userId, bareId } = splitReedId(reedId);
    return (await request(`/reeds/${userId}/${bareId}/echoes`, { method: 'GET' }, EchoCountResponseSchema)).count;
  },

  async listReplies(reedId: string, opts?: { limit?: number; before?: number }): Promise<api.ReplyListResponse> {
    const { userId, bareId } = splitReedId(reedId);
    return request(`/reeds/${userId}/${bareId}/replies${pageQuery(opts)}`, { method: 'GET' }, ReplyListResponseSchema);
  },

  async listEchoers(reedId: string, opts?: { limit?: number; before?: number }): Promise<api.EchoerListResponse> {
    const { userId, bareId } = splitReedId(reedId);
    return request(`/reeds/${userId}/${bareId}/chorus${pageQuery(opts)}`, { method: 'GET' }, EchoerListResponseSchema);
  },

  async deleteMention(reedID: string, reason: string): Promise<void> {
    const params = new URLSearchParams();
    if (reason) params.set('reason', reason);
    const qs = params.toString();
    await send(`/mentions/${reedID}${qs ? `?${qs}` : ''}`, { method: 'DELETE' });
  },

  /** The caller's ripples inbox: comments on their own reeds, plus replies
   * to a ripple they themselves authored — see GetReceivedRipples. */
  async getReceivedRipples(opts?: { limit?: number; before?: string }): Promise<api.ReceivedRippleListResponse> {
    return request(`/ripples${pageQuery(opts)}`, { method: 'GET' }, ReceivedRippleListResponseSchema);
  },

  /** Only a holder of the parent reed may list its ripples (server-side). */
  async listRipples(reedId: string, opts?: { limit?: number; before?: string }): Promise<api.RippleListResponse> {
    const { userId, bareId } = splitReedId(reedId);
    return request(`/reeds/${userId}/${bareId}/ripples${pageQuery(opts)}`, { method: 'GET' }, RippleListResponseSchema);
  },

  /** Only a holder of the parent reed may post a ripple (server-side). */
  // fields.fingerprint travels bare over the wire — the server joins it
  // with the authenticated caller's userID itself (see handlers.go's
  // PostRipple).
  async postRipple(
    reedId: string,
    fields: {
      content: string;
      threadId: string;
      replyingTo?: string;
      keyId: string;
      userSignature: string;
    }
  ): Promise<api.Ripple> {
    const { userId, bareId } = splitReedId(reedId);
    return request(
      `/reeds/${userId}/${bareId}/ripples`,
      protoBody('POST', PostRippleRequestSchema, {
        ...fields,
        // Only used server-side when this request is relayed to the
        // reed's home server (see handlers.go's resolveActingUser) — a
        // local caller's own session already provides this.
        userId: localStorage.getItem('userId') ?? '',
      }),
      RippleSchema,
    );
  },

  async deleteRipple(reedId: string, rippleHash: string): Promise<void> {
    const { userId, bareId } = splitReedId(reedId);
    await send(
      `/reeds/${userId}/${bareId}/ripples/${rippleHash}`,
      // userId is only used server-side when this request is relayed to the
      // reed's home server (see handlers.go's resolveActingUser).
      protoBody('DELETE', DeleteRippleRequestSchema, { userId: localStorage.getItem('userId') ?? '' }),
    );
  },

  async listFollowing(userId: string, opts?: { limit?: number; before?: number }): Promise<api.FollowListResponse> {
    return request(`/users/${userId}/following${pageQuery(opts)}`, { method: 'GET' }, FollowListResponseSchema);
  },

  async listFollowers(userId: string, opts?: { limit?: number; before?: number }): Promise<api.FollowListResponse> {
    return request(`/users/${userId}/followers${pageQuery(opts)}`, { method: 'GET' }, FollowListResponseSchema);
  },

  /**
   * Existence and removal state only: a live reed answers 204 with no body.
   * Callers tell the removal's kind apart by its message type.
   */
  async getReedOrRemoval(
    reedId: string
  ): Promise<
    | { kind: 'reed' }
    | { kind: 'gone'; removal: RefusalDetail }
    | { kind: 'not_found' }
  > {
    const { userId, bareId } = splitReedId(reedId);
    try {
      await send(`/reeds/${userId}/${bareId}`, { method: 'GET' });
      return { kind: 'reed' };
    } catch (err) {
      const error = err as ApiError;
      if (error?.status === 404) {
        return { kind: 'not_found' };
      }
      if (error?.status === 410 && error.detail) {
        return { kind: 'gone', removal: error.detail };
      }
      throw err;
    }
  },

  async deleteReed(reedId: string, signature: string): Promise<api.ReedRemoval> {
    const { userId, bareId } = splitReedId(reedId);
    return request(`/reeds/${userId}/${bareId}`, protoBody('DELETE', RemovalRequestSchema, { signature }), ReedRemovalCertSchema);
  },

  /** Countersigns and stores a whole thread; parts are in index order. */
  async createThread(body: MessageInitShape<typeof CreateThreadRequestSchema>): Promise<api.ThreadSignatures> {
    return request('/threads', protoBody('POST', CreateThreadRequestSchema, body), ThreadSignaturesSchema);
  },

  /** Removes a whole thread, by its head's ID. */
  async deleteThread(threadId: string, signature: string): Promise<api.ThreadRemoval> {
    return request(`/threads/${threadId}`, protoBody('DELETE', RemovalRequestSchema, { signature }), ThreadRemovalSchema);
  },

  async blockUser(userId: string, signature: string, keyId: string): Promise<api.BlockCert> {
    return request(
      `/users/${userId}/block`,
      protoBody('POST', BlockRequestSchema, { signature, fingerprint: parseKeyId(keyId)?.fingerprint ?? keyId }),
      BlockCertSchema,
    );
  },

  async unblockUser(userId: string): Promise<void> {
    await send(`/users/${userId}/block`, { method: 'DELETE' });
  },

  async listBlocks(): Promise<api.BlockCert[]> {
    return (await request('/blocks', { method: 'GET' }, BlockListResponseSchema)).blocks;
  },

  async likeReed(reedId: string, signature: string, keyId: string): Promise<api.ReedLike> {
    const { userId, bareId } = splitReedId(reedId);
    // fingerprint travels bare over the wire — the server joins it with the
    // authenticated caller's userID itself (see handlers.go's LikeReed).
    const bareFingerprint = parseKeyId(keyId)?.fingerprint ?? keyId;
    return request(
      `/reeds/${userId}/${bareId}/like`,
      protoBody('POST', LikeRequestSchema, {
        signature,
        fingerprint: bareFingerprint,
        // Only used server-side when this request is relayed to the reed's
        // home server (see handlers.go's resolveActingUser).
        likerId: localStorage.getItem('userId') ?? '',
      }),
      LikeCertSchema,
    );
  },

  async unlikeReed(reedId: string): Promise<void> {
    const { userId, bareId } = splitReedId(reedId);
    // likerId is only used server-side when relayed to the reed's home
    // server (see handlers.go's resolveActingUser).
    await send(
      `/reeds/${userId}/${bareId}/like`,
      protoBody('DELETE', UnlikeRequestSchema, { likerId: localStorage.getItem('userId') ?? '' }),
    );
  },

  /** The subject is whoever owns `subjectKeyId`; the server derives it. */
  async createVouch(subjectKeyId: string, voucherKeyId: string, signature: string, note: string): Promise<api.Vouch> {
    return request(
      '/vouches',
      protoBody('POST', CreateVouchRequestSchema, {
        subjectKeyId,
        voucherKeyId,
        signature,
        note,
        // Only read server-side when this request arrives relayed from a peer.
        voucherId: localStorage.getItem('userId') ?? '',
      }),
      VouchSchema,
    );
  },

  async withdrawVouch(subjectKeyId: string, voucherKeyId: string, signature: string): Promise<api.Vouch> {
    return request(
      `/vouches/${subjectKeyId}`,
      protoBody('DELETE', WithdrawVouchRequestSchema, {
        voucherKeyId,
        signature,
        voucherId: localStorage.getItem('userId') ?? '',
      }),
      VouchSchema,
    );
  },

  /** One vouch. Both ids are full and are sent as-is, never recomposed. */
  async getVouch(subjectUserId: string, vouchId: string): Promise<api.Vouch> {
    return request(`/users/${subjectUserId}/vouches/${vouchId}`, undefined, VouchSchema);
  },

  async getVouchesForUser(userId: string, cursor?: string): Promise<api.VouchListResponse> {
    const query = cursor ? `?cursor=${encodeURIComponent(cursor)}` : '';
    return request(`/users/${userId}/vouches${query}`, undefined, VouchListResponseSchema);
  },

  /** The caller's own vouches, withdrawn ones included, for the audit list. */
  async getMyVouches(cursor?: string): Promise<api.VouchListResponse> {
    const query = cursor ? `?cursor=${encodeURIComponent(cursor)}` : '';
    return request(`/vouches${query}`, undefined, VouchListResponseSchema);
  },

  async pinReed(reedId: string): Promise<void> {
    await send(`/reeds/${reedId}/pin`, { method: 'POST' });
  },

  async unpinReed(reedId: string): Promise<void> {
    await send(`/reeds/${reedId}/pin`, protoBody('DELETE', PinRequestSchema, { pinnerId: localStorage.getItem('userId') ?? '' }));
  },

  // getKeyRevocation/getPublicKey accept either a bare fingerprint or an
  // already-canonical key id and build the full canonical id
  // (userID@serverID/fingerprint) for the URL — GET /keys/{id:.+} takes
  // the whole id as one greedy path segment now, not a separate {userID}
  // plus a bare {fingerprint}. See main.go's route registration comment.
  /** A user key's revocation; a server key's is read through the rotation path. */
  async getKeyRevocation(userId: string, keyId: string): Promise<api.KeyRevocation> {
    const id = canonicalKeyId(userId, keyId);
    const resp = await request(`/keys/${id}/revocation`, { method: 'GET' }, KeyRevocationResponseSchema);
    if (resp.revocation.case !== 'user') throw new Error(`Not a user key revocation: ${id}`);
    return resp.revocation.value;
  },

  async followUser(targetUserId: string): Promise<void> {
    await send(`/users/${targetUserId}/follow`, { method: 'POST' });
  },

  async unfollowUser(targetUserId: string): Promise<void> {
    await send(`/users/${targetUserId}/follow`, { method: 'DELETE' });
  },

  /** GET /keys/{id}/revocation and GET /keys/{id} without checking the
   * response signature, for following a server key rotation: the response is
   * signed by a key not trusted yet (see serverKeyRotation.ts). */
  async getKeyRevocationUnverified(id: string): Promise<Response> {
    return requestRaw(`/keys/${id}/revocation`, { method: 'GET' }, { skipEnvelope: true });
  },

  async getPublicKeyUnverified(id: string): Promise<Response> {
    return requestRaw(`/keys/${id}`, { method: 'GET' }, { skipEnvelope: true });
  },

  /** id must already be a full canonical key id — userID@serverID/fingerprint
   * for a user key, fingerprint@serverID for a server's own key. GET /keys/{id}
   * serves any key, local or a federated peer's via server-side proxying. */
  async getPublicKey(id: string): Promise<api.PublicKey> {
    return request(`/keys/${id}`, { method: 'GET' }, PublicKeySchema);
  },

  /** Atomically revokes the predecessor key and registers the new one —
   * a separate revoke-then-add round trip leaves a window where the
   * caller has no valid key at all to sign anything with. */
  async addPublicKey(
    userId: string,
    publicKey: string,
    revokedKeyId: string,
    revokedKeySignature: string,
    newKeySignature: string,
    revocationReason: string,
    revocationUserSignature: string
  ): Promise<api.PublicKey> {
    // revokedKeyFingerprint travels bare over the wire — the server joins
    // it with userId itself (see handlers.go's AddPublicKey).
    const bareRevokedKeyFingerprint = parseKeyId(revokedKeyId)?.fingerprint ?? revokedKeyId;
    return request(
      '/keys',
      protoBody('POST', AddPublicKeyRequestSchema, {
        userId,
        publicKey,
        revokedKeyFingerprint: bareRevokedKeyFingerprint,
        revokedKeySignature,
        newKeySignature,
        revocationReason,
        revocationUserSignature,
      }),
      PublicKeySchema,
    );
  },

  /** Unauthenticated: GET a single-use account-recovery challenge nonce. */
  async getAccountRecoveryChallenge(): Promise<api.ChallengeResponse> {
    return request('/account-recovery/challenge', { method: 'GET' }, ChallengeResponseSchema);
  },

  /** Unauthenticated: prove active key possession; returns bootstrap payload. */
  async bootstrapAccountRecovery(
    body: MessageInitShape<typeof AccountRecoveryBootstrapRequestSchema>
  ): Promise<api.AccountRecoveryBootstrapResponse> {
    return request(
      '/account-recovery/bootstrap',
      protoBody('POST', AccountRecoveryBootstrapRequestSchema, body),
      AccountRecoveryBootstrapResponseSchema,
    );
  },

  /** Unauthenticated: GET a single-use recovery challenge nonce. */
  async getIdentityClaimChallenge(): Promise<api.ChallengeResponse> {
    return request('/recovery/identity/claim', { method: 'GET' }, ChallengeResponseSchema);
  },

  /** Unauthenticated: claim own identity with challenge + nested key chain. */
  async claimOwnIdentity(body: MessageInitShape<typeof ClaimIdentityRequestSchema>): Promise<api.RecoveryProfile> {
    return request('/recovery/identity/claim', protoBody('POST', ClaimIdentityRequestSchema, body), RecoveryProfileSchema);
  },

  /** Authenticated: report one peer identity with nested key chain. */
  async reportPeerIdentity(body: MessageInitShape<typeof PeerIdentityRequestSchema>): Promise<api.RecoveryProfile> {
    return request('/recovery/identity', protoBody('POST', PeerIdentityRequestSchema, body), RecoveryProfileSchema);
  },

  /** Authenticated: report one reed's countersigned metadata. */
  async reportRecoveryReed(body: MessageInitShape<typeof RecoveryReedRequestSchema>): Promise<void> {
    await send('/recovery/reeds', protoBody('POST', RecoveryReedRequestSchema, body));
  },

  /** Authenticated: report a page of following user IDs (≤100). */
  async reportRecoveryFollowing(userIds: string[]): Promise<void> {
    await send('/recovery/following', protoBody('POST', RecoveryFollowingRequestSchema, { userIds }));
  },

  /** Authenticated: clear ongoing_recoveries for the caller. */
  async completeRecovery(): Promise<void> {
    await send('/recovery/complete', { method: 'POST' });
  },

  /** Authenticated: bind this origin as the sole active device. */
  async bindDevice(): Promise<string> {
    return (await request('/users/device', { method: 'POST' }, BindDeviceResponseSchema)).deviceId;
  },

  /** Authenticated: report a successful local keys-only or full export. */
  async recordBackup(kind: 'identity' | 'full'): Promise<void> {
    await send('/users/me/backup', protoBody('POST', RecordBackupRequestSchema, { kind }));
  },
};

/** The status of a failed profile or info read, with the removal or block
 * behind a 410 or 403. */
function statusOf(error: ApiError): { status: number; removal?: api.AccountRemoval; block?: api.BlockCert } {
  if (error?.status === 410 && isMessage(error.detail, AccountRemovalCertSchema)) {
    return { status: 410, removal: error.detail };
  }
  if (error?.status === 403 && isMessage(error.detail, BlockCertSchema)) {
    return { status: 403, block: error.detail };
  }
  if (error?.status) {
    return { status: error.status };
  }
  const match = error?.message?.match(/HTTP (\d+)/);
  return { status: match ? parseInt(match[1]) : 0 };
}
