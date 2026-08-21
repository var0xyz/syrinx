/**
 * Produces deterministic byte sequences to feed into PGP signing and
 * verification.
 *
 * Design: opaque signing input, not a document
 *
 * `bytesToSign` returns bytes that look like a markdown envelope with a
 * front-matter header block:
 *
 *     ---
 *     <sortedKey>: <value>
 *     <sortedKey>: <value>
 *     ---
 *     <content>
 *
 * This shape is convenient for humans reading a hex dump in a debugger, but
 * the bytes are NOT a document. Nothing in Syrinx ever parses them back into
 * headers/content. They exist for one purpose: to be signed on one side and
 * re-produced identically on the other side for verification.
 *
 * Consequences of that design choice:
 *
 *  1. No escaping. Values are inserted verbatim. If a value contains a
 *     literal '\n', ':', or '---' sequence, those bytes appear in the output
 *     as-is. This is safe because no code splits the output on '\n' or on
 *     ': ' to recover fields — the receiver rebuilds the same input map and
 *     calls bytesToSign again.
 *
 *  2. Empty-string values are omitted (whole line dropped). Absent and
 *     empty are equivalent by convention.
 *
 *  3. Keys are sorted ASCII byte-lexicographically to match Go's
 *     sort.Strings and Array.prototype.sort with the default comparator —
 *     the server has a mirror function `BytesToSign` (Go package `signing`)
 *     and the two MUST be byte-identical for signature verification to
 *     work.
 *
 *  4. The return type is Uint8Array, not string, to nudge callers away
 *     from treating the output as text or trying to parse it.
 *
 * A future contributor "hardening" this helper by adding an escape table
 * would silently break signature compatibility with every record already
 * signed against the current bytes. Do not do that.
 */

/**
 * Builds the canonical byte sequence for a signed record.
 *
 * Rules (see file doc for rationale):
 *   - Headers are sorted ASCII byte-lexicographically.
 *   - Header entries with an empty-string value are omitted.
 *   - Line separator is a single '\n' (LF).
 *   - Each header line is exactly '<key>: <value>'.
 *   - The output is:  '---\n' + joined headers + '\n---\n' + content
 *     with no trailing newline added; if content itself ends with '\n'
 *     that is preserved verbatim.
 *   - No escaping. Values are inserted as-is.
 */
export function bytesToSign(headers: Record<string, string>, content: string): Uint8Array {
  const keys = Object.keys(headers)
    .filter((k) => headers[k] !== '')
    .sort();

  const parts: string[] = ['---\n'];
  for (let i = 0; i < keys.length; i++) {
    if (i > 0) parts.push('\n');
    parts.push(keys[i], ': ', headers[keys[i]]);
  }
  if (keys.length > 0) parts.push('\n');
  parts.push('---\n', content);

  return new TextEncoder().encode(parts.join(''));
}

/**
 * String form of `bytesToSign`, provided for cases where a UTF-8 string is
 * more convenient than a byte array (e.g. logging, passing to PGP libraries
 * that accept strings directly). The bytes are identical to
 * `bytesToSign(...)` under UTF-8 encoding.
 */
export function stringToSign(headers: Record<string, string>, content: string): string {
  return new TextDecoder().decode(bytesToSign(headers, content));
}

/**
 * Builds the canonical user-identity payload — the exact byte sequence a
 * user signs to produce `userSignature` for their signed identity record.
 *
 * Mirror of `buildUserIdentityPayload` in the Go module `identity.go`. The
 * two functions MUST stay byte-identical: the server verifies the user's
 * signature against bytes it rebuilds from the same header set, and any
 * drift here silently breaks signup and profile updates for every client
 * using this SPA.
 *
 * Headers (sorted by `bytesToSign` at signing time):
 *   - type:        "identity-user"
 *   - username:    the account username
 *   - fingerprint: the key producing this signature (self-describing)
 *
 * Content: `bio` (verbatim, unescaped, may span multiple lines or be empty).
 */
export function buildUserIdentityPayload(
  username: string,
  fingerprint: string,
  bio: string
): string {
  return stringToSign(
    {
      type: 'identity-user',
      username,
      fingerprint
    },
    bio
  );
}

export function buildNewUserIdentityPayload(
  username: string,
  fingerprint: string,
): string {
  return buildUserIdentityPayload(
    username,
    fingerprint,
    "",
  );
}

/** Mirror of BuildProfilePayload in identity.go (server identity countersign). */
export function buildProfilePayload(
  userID: string,
  username: string,
  fingerprint: string,
  serverID: string,
  serverKeyFingerprint: string,
  userSignatureB64: string,
  invitedBy: string,
  role: string,
  bio: string,
  memberSince: string,
  signedAt: string
): string {
  return stringToSign(
    {
      type: 'identity-server',
      userID,
      username,
      fingerprint,
      memberSince,
      role,
      serverID,
      serverKeyFingerprint,
      signedAt,
      userSignature: userSignatureB64,
      invitedBy
    },
    bio
  );
}

/**
 * Mirror of BuildReedPayload / ReedCountersignHeaders in identity.go.
 * reedID is the full canonical id. Content is the author userSignature
 * wire value (base64 armor), same as POST /reeds `signature` form field.
 */
export function buildReedPayload(
  serverID: string,
  reedID: string,
  serverKeyFingerprint: string,
  userSignatureB64: string,
  timestamp: string
): string {
  return stringToSign(
    {
      fingerprint: serverKeyFingerprint,
      serverID,
      reedID,
      timestamp
    },
    userSignatureB64
  );
}

/** Mirror of publicKeyCountersignHeaders + armor in handlers.go. */
export function buildPublicKeyPayload(
  userID: string,
  fingerprint: string,
  serverID: string,
  serverKeyFingerprint: string,
  armor: string,
  signedAt: string
): string {
  return stringToSign(
    {
      fingerprint,
      serverID,
      serverKeyFingerprint,
      signedAt,
      userID
    },
    armor
  );
}

/** Mirror of buildUserRevocationPayload in identity.go. */
export function buildUserRevocationPayload(
  userID: string,
  fingerprint: string,
  reason: string
): string {
  return stringToSign(
    {
      type: 'revocation',
      userID,
      fingerprint
    },
    reason
  );
}

/** Mirror of buildServerRevocationPayload in identity.go. */
export function buildServerRevocationPayload(
  userID: string,
  fingerprint: string,
  reason: string,
  serverID: string,
  serverKeyFingerprint: string,
  userSignatureB64: string,
  signedAt: string
): string {
  return stringToSign(
    {
      type: 'revocation',
      userID,
      fingerprint,
      signedAt,
      serverID,
      serverKeyFingerprint,
      userSignature: userSignatureB64
    },
    reason
  );
}

/** Mirror of BuildReedRemovalUserPayload in identity.go (`type: reed`). reedID is the full canonical id. */
export function buildReedRemovalUserPayload(
  serverID: string,
  reedID: string
): string {
  return stringToSign(
    {
      type: 'reed',
      serverID,
      reedID
    },
    ''
  );
}

/** Mirror of BuildReedRemovalServerPayload in identity.go. */
export function buildReedRemovalServerPayload(
  serverID: string,
  reedID: string,
  serverKeyFingerprint: string,
  userSignatureB64: string,
  signedAt: string
): string {
  return stringToSign(
    {
      type: 'reed',
      serverID,
      reedID,
      signedAt,
      serverKeyFingerprint,
      userSignature: userSignatureB64
    },
    ''
  );
}

/** Mirror of BuildReedLikeUserPayload in identity.go (`type: reed_like`). reedID is the full canonical id. */
export function buildReedLikeUserPayload(
  serverID: string,
  reedID: string,
  fingerprint: string
): string {
  return stringToSign(
    {
      type: 'reed_like',
      serverID,
      reedID,
      fingerprint
    },
    ''
  );
}

/** Mirror of BuildReedLikeServerPayload in identity.go. */
export function buildReedLikeServerPayload(
  serverID: string,
  reedID: string,
  serverKeyFingerprint: string,
  userSignatureB64: string,
  signedAt: string
): string {
  return stringToSign(
    {
      type: 'reed_like',
      serverID,
      reedID,
      signedAt,
      serverKeyFingerprint,
      userSignature: userSignatureB64
    },
    ''
  );
}

/** Mirror of BuildAccountRemovalUserPayload (`type: account`, note as content). */
export function buildAccountRemovalUserPayload(
  serverID: string,
  userID: string,
  note: string
): string {
  return stringToSign(
    {
      type: 'account',
      serverID,
      userID
    },
    note
  );
}

/** Mirror of BuildAccountRemovalServerPayload in identity.go. */
export function buildAccountRemovalServerPayload(
  serverID: string,
  userID: string,
  note: string,
  serverKeyFingerprint: string,
  userSignatureB64: string,
  signedAt: string
): string {
  return stringToSign(
    {
      type: 'account',
      serverID,
      userID,
      signedAt,
      serverKeyFingerprint,
      userSignature: userSignatureB64
    },
    note
  );
}

/** Mirror of BuildInviteUserPayload in identity.go (`type: invite-user`). */
export function buildInviteUserPayload(
  serverID: string,
  userID: string,
  inviteID: string,
  tokenHash: string,
  createdAt: string,
  grantedRole: string = 'user'
): string {
  return stringToSign(
    {
      type: 'invite-user',
      serverID,
      userID,
      inviteID,
      tokenHash,
      grantedRole,
      createdAt
    },
    ''
  );
}

/** Mirror of BuildInviteServerPayload in identity.go (`type: invite-server`). */
export function buildInviteServerPayload(
  serverID: string,
  userID: string,
  inviteID: string,
  tokenHash: string,
  serverKeyFingerprint: string,
  userSignatureB64: string,
  createdAt: string,
  signedAt: string
): string {
  return stringToSign(
    {
      type: 'invite-server',
      serverID,
      userID,
      inviteID,
      tokenHash,
      createdAt,
      signedAt,
      serverKeyFingerprint,
      userSignature: userSignatureB64
    },
    ''
  );
}

/**
 * Mirror of BuildRippleUserPayload / rippleUserHeaders in identity.go. The
 * exact bytes a ripple's author signs. reedID is the full canonical id of
 * the parent reed. threadID is always present (client-minted, see
 * specs/ripples/00_design.md); replyingTo is omitted for a top-level post
 * (empty string is dropped by bytesToSign). No timestamp — client clocks
 * are never signed over.
 */
export function buildRippleUserPayload(
  reedID: string,
  rippleAuthorID: string,
  fingerprint: string,
  threadID: string,
  replyingTo: string,
  content: string
): string {
  return stringToSign(
    {
      reedID,
      rippleAuthorID,
      fingerprint,
      threadID,
      replyingTo
    },
    content
  );
}

/**
 * Mirror of BuildRippleServerPayload / rippleServerHeaders in identity.go.
 * Same fields the user signed, plus serverID and the server-supplied
 * timestamp. Content is the author's detached signature (base64 armor),
 * not the ripple text — mirrors buildReedPayload exactly. `fingerprint`
 * here is the ripple author's signing-key fingerprint (same value passed
 * to buildRippleUserPayload), not the server key's.
 */
export function buildRippleServerPayload(
  serverID: string,
  reedID: string,
  rippleAuthorID: string,
  fingerprint: string,
  threadID: string,
  replyingTo: string,
  userSignatureB64: string,
  timestamp: string
): string {
  return stringToSign(
    {
      serverID,
      reedID,
      rippleAuthorID,
      fingerprint,
      threadID,
      replyingTo,
      timestamp
    },
    userSignatureB64
  );
}
