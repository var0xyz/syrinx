/**
 * Canonical payloads for signed records. Every payload is a field set
 * serialized as RFC 8785 (JCS) JSON, byte-identical to canonicalJSON and the
 * build*Payload functions in the server's utils.go / identity.go.
 */
import canonicalize from 'canonicalize';

export type SignedFields = Record<string, unknown>;

function isEmptySignedValue(value: unknown): boolean {
  return value === undefined || value === null || value === '' || (Array.isArray(value) && value.length === 0);
}

/** The RFC 8785 string signed over fields. Empty strings, null/undefined and
 * empty lists are dropped, so absent and empty sign the same; false and 0
 * stay. Mirror of canonicalJSON in utils.go. */
export function canonicalJSON(fields: SignedFields): string {
  const kept: SignedFields = {};
  for (const [key, value] of Object.entries(fields)) {
    if (!isEmptySignedValue(value)) kept[key] = value;
  }
  const out = canonicalize(kept);
  if (out === undefined) throw new Error('canonicalJSON: nothing to serialize');
  return out;
}

/** Mirror of buildUserIdentityPayload in identity.go. */
export function buildUserIdentityPayload(username: string, keyID: string, bio: string): string {
  return canonicalJSON({ type: 'identity-user', username, keyID, bio });
}

export function buildNewUserIdentityPayload(username: string, keyID: string): string {
  return buildUserIdentityPayload(username, keyID, '');
}

/** Mirror of buildProfilePayload in identity.go. */
export function buildProfilePayload(
  userID: string,
  username: string,
  keyID: string,
  serverID: string,
  serverKeyFingerprint: string,
  userSignature: string,
  inviteID: string,
  role: string,
  bio: string,
  memberSince: string,
  signedAt: string
): string {
  return canonicalJSON({
    type: 'identity-server',
    userID,
    username,
    keyID,
    memberSince,
    role,
    serverID,
    serverKeyFingerprint,
    signedAt,
    userSignature,
    inviteID,
    bio
  });
}

/** The bytes a reed's author signs. Built only here: the server never sees
 * reed content, peers verify it. replying/thread are left out when absent. */
export function buildReedUserPayload(reed: {
  id: string;
  userID: string;
  replying?: { to: string; root: string } | null;
  echoing?: string | null;
  thread?: { head: string; index: number } | null;
  content?: string;
}): string {
  return canonicalJSON({
    id: reed.id,
    userID: reed.userID,
    replying: reed.replying ? { to: reed.replying.to, root: reed.replying.root } : undefined,
    echoing: reed.echoing,
    thread: reed.thread ? { head: reed.thread.head, index: reed.thread.index } : undefined,
    content: reed.content
  });
}

/** Mirror of buildReedPayload in identity.go. reedID is the full canonical id;
 * userSignature is the author's detached signature over the reed. */
export function buildReedPayload(
  serverID: string,
  reedID: string,
  serverKeyFingerprint: string,
  authorKeyID: string,
  userSignature: string,
  timestamp: string
): string {
  return canonicalJSON({
    authorKeyID,
    fingerprint: serverKeyFingerprint,
    serverID,
    reedID,
    timestamp,
    userSignature
  });
}

/** Mirror of buildPublicKeyPayload in identity.go. */
export function buildPublicKeyPayload(
  userID: string,
  keyID: string,
  serverID: string,
  serverKeyFingerprint: string,
  armor: string,
  signedAt: string
): string {
  return canonicalJSON({ keyID, serverID, serverKeyFingerprint, signedAt, userID, armor });
}

/** Mirror of buildUserRevocationPayload in identity.go. */
export function buildUserRevocationPayload(userID: string, keyID: string, reason: string): string {
  return canonicalJSON({ type: 'revocation', userID, keyID, reason });
}

/** Mirror of buildServerRevocationPayload in identity.go. */
export function buildServerRevocationPayload(
  userID: string,
  keyID: string,
  reason: string,
  serverID: string,
  serverKeyFingerprint: string,
  userSignature: string,
  signedAt: string
): string {
  return canonicalJSON({
    type: 'revocation',
    userID,
    keyID,
    signedAt,
    serverID,
    serverKeyFingerprint,
    userSignature,
    reason
  });
}

/** Mirror of buildReedRemovalUserPayload in identity.go. */
export function buildReedRemovalUserPayload(serverID: string, reedID: string): string {
  return canonicalJSON({ type: 'reed', serverID, reedID });
}

/** Mirror of buildReedRemovalServerPayload in identity.go. */
export function buildReedRemovalServerPayload(
  serverID: string,
  reedID: string,
  authorKeyID: string,
  serverKeyFingerprint: string,
  userSignature: string,
  signedAt: string
): string {
  return canonicalJSON({
    type: 'reed',
    serverID,
    reedID,
    authorKeyID,
    signedAt,
    serverKeyFingerprint,
    userSignature
  });
}

/** Mirror of buildServerKeyRevocationPayload in identity.go. */
export function buildServerKeyRevocationPayload(
  serverID: string,
  keyID: string,
  successor: string,
  compromised: boolean,
  reason: string,
  signedAt: string
): string {
  return canonicalJSON({
    type: 'server-key-revocation',
    serverID,
    keyID,
    successor,
    compromised,
    signedAt,
    reason
  });
}

/** Mirror of buildThreadUserPayload in identity.go. reedIDs[0] is the head. */
export function buildThreadUserPayload(serverID: string, threadID: string, reedIDs: string[]): string {
  return canonicalJSON({ type: 'thread', serverID, threadID, reedIDs });
}

/** Mirror of buildThreadServerPayload in identity.go. */
export function buildThreadServerPayload(
  serverID: string,
  threadID: string,
  authorKeyID: string,
  serverKeyFingerprint: string,
  userSignature: string,
  signedAt: string
): string {
  return canonicalJSON({
    type: 'thread',
    serverID,
    threadID,
    authorKeyID,
    signedAt,
    serverKeyFingerprint,
    userSignature
  });
}

/** Mirror of buildThreadRemovalUserPayload in identity.go. */
export function buildThreadRemovalUserPayload(
  serverID: string,
  threadID: string,
  threadSignature: string
): string {
  return canonicalJSON({ type: 'thread_removal', serverID, threadID, threadSignature });
}

/** Mirror of buildThreadRemovalServerPayload in identity.go. */
export function buildThreadRemovalServerPayload(
  serverID: string,
  threadID: string,
  authorKeyID: string,
  serverKeyFingerprint: string,
  userSignature: string,
  signedAt: string
): string {
  return canonicalJSON({
    type: 'thread_removal',
    serverID,
    threadID,
    authorKeyID,
    signedAt,
    serverKeyFingerprint,
    userSignature
  });
}

/** Mirror of buildReedLikeUserPayload in identity.go. */
export function buildReedLikeUserPayload(reedID: string, keyID: string): string {
  return canonicalJSON({ type: 'reed_like', reedID, keyID });
}

/** Mirror of buildReedLikeServerPayload in identity.go. */
export function buildReedLikeServerPayload(
  reedID: string,
  serverKeyFingerprint: string,
  userSignature: string,
  signedAt: string
): string {
  return canonicalJSON({ type: 'reed_like', reedID, signedAt, serverKeyFingerprint, userSignature });
}

/** Mirror of buildBlockUserPayload in identity.go. */
export function buildBlockUserPayload(userID: string, blockedUserID: string, keyID: string): string {
  return canonicalJSON({ type: 'block', userID, blockedUserID, keyID });
}

/** Mirror of buildBlockServerPayload in identity.go. */
export function buildBlockServerPayload(
  userID: string,
  blockedUserID: string,
  serverKeyFingerprint: string,
  userSignature: string,
  signedAt: string
): string {
  return canonicalJSON({ type: 'block', userID, blockedUserID, signedAt, serverKeyFingerprint, userSignature });
}

/** Mirror of buildVouchUserPayload in identity.go. Key ids are
 * owner-prefixed, so both users are named; no client timestamp. */
export function buildVouchUserPayload(voucherKeyID: string, subjectKeyID: string, note: string): string {
  return canonicalJSON({ voucherKeyID, subjectKeyID, note });
}

/** Mirror of buildVouchServerPayload in identity.go. */
export function buildVouchServerPayload(
  subjectKeyID: string,
  serverKeyFingerprint: string,
  userSignature: string,
  signedAt: string
): string {
  return canonicalJSON({ subjectKeyID, signedAt, serverKeyFingerprint, userSignature });
}

/** Mirror of buildVouchWithdrawalUserPayload in identity.go. */
export function buildVouchWithdrawalUserPayload(vouchID: string): string {
  return canonicalJSON({ vouchID });
}

/** Mirror of buildVouchWithdrawalServerPayload in identity.go. */
export function buildVouchWithdrawalServerPayload(
  vouchID: string,
  serverKeyFingerprint: string,
  userSignature: string,
  signedAt: string
): string {
  return canonicalJSON({ vouchID, signedAt, serverKeyFingerprint, userSignature });
}

/** Mirror of buildAccountRemovalUserPayload in identity.go. */
export function buildAccountRemovalUserPayload(serverID: string, userID: string, note: string): string {
  return canonicalJSON({ type: 'account', serverID, userID, note });
}

/** Mirror of buildAccountRemovalServerPayload in identity.go. */
export function buildAccountRemovalServerPayload(
  serverID: string,
  userID: string,
  note: string,
  serverKeyFingerprint: string,
  userSignature: string,
  signedAt: string
): string {
  return canonicalJSON({
    type: 'account',
    serverID,
    userID,
    signedAt,
    serverKeyFingerprint,
    userSignature,
    note
  });
}

/** Mirror of buildInviteUserPayload in identity.go. */
export function buildInviteUserPayload(
  serverID: string,
  userID: string,
  inviteID: string,
  tokenHash: string,
  createdAt: string,
  grantedRole: string = 'user'
): string {
  return canonicalJSON({
    type: 'invite-user',
    serverID,
    userID,
    inviteID,
    tokenHash,
    grantedRole,
    createdAt
  });
}

/** Mirror of buildInviteServerPayload in identity.go. */
export function buildInviteServerPayload(
  serverID: string,
  userID: string,
  inviteID: string,
  tokenHash: string,
  serverKeyFingerprint: string,
  userSignature: string,
  createdAt: string,
  signedAt: string
): string {
  return canonicalJSON({
    type: 'invite-server',
    serverID,
    userID,
    inviteID,
    tokenHash,
    createdAt,
    signedAt,
    serverKeyFingerprint,
    userSignature
  });
}

/** Mirror of buildRippleUserPayload in identity.go. replyingTo is empty for
 * a top-level post; no timestamp, client clocks are never signed over. */
export function buildRippleUserPayload(
  reedID: string,
  rippleAuthorID: string,
  keyID: string,
  threadID: string,
  replyingTo: string,
  content: string
): string {
  return canonicalJSON({ reedID, rippleAuthorID, keyID, threadID, replyingTo, content });
}

/** Mirror of buildRippleServerPayload in identity.go. keyID is the ripple
 * author's key, not the server's. */
export function buildRippleServerPayload(
  serverID: string,
  reedID: string,
  rippleAuthorID: string,
  keyID: string,
  threadID: string,
  replyingTo: string,
  userSignature: string,
  timestamp: string
): string {
  return canonicalJSON({
    serverID,
    reedID,
    rippleAuthorID,
    keyID,
    threadID,
    replyingTo,
    timestamp,
    userSignature
  });
}

/** Mirror of buildRealtimeAuthPayload in identity.go. */
export function buildRealtimeAuthPayload(serverID: string, userID: string, timestamp: string): string {
  return canonicalJSON({ type: 'realtime-auth', serverID, userID, timestamp });
}
