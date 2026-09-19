// Canonical byte sequences for signed identity records and related
// countersignatures (keys, reeds, revocations, reed removals).
//
// An identity record has two signed byte sequences that overlap but are
// not identical:
//
//   - The USER payload covers only user-authored fields (username,
//     keyID, and bio as the envelope content). The user's
//     detached PGP signature over these bytes is `userSignature`.
//
//   - The SERVER payload covers a superset: user-authored fields + all
//     server-authored fields (userID, memberSince, serverID,
//     serverKeyFingerprint, signedAt, inviteID) + the userSignature
//     itself as a header. The server's detached PGP signature over these
//     bytes is `serverSignature`. `inviteID` is omitted when empty
//     (open signup with no invite).
//
// Including `userSignature` as a header inside the server payload welds
// the two attestations together: a compromised server cannot re-pair
// Alice's userSignature with a different set of server-authored fields
// (e.g. a fabricated memberSince) without breaking serverSignature.
//
// The two payloads carry distinct `type` header values
// (`identity-user` vs `identity-server`). This prevents any possibility
// of a user signature over the user payload being misinterpreted as a
// server signature over a truncated server payload — the header bytes
// differ up front.
//
// Both payloads flow through bytesToSign, which is the sole
// canonicalisation authority. Do not build these byte sequences any
// other way.
package main

import (
	"time"
)

// identityRecordTimeFormat is the canonical time format used for
// memberSince and signedAt headers in the signed bytes. UTC + RFC3339
// seconds resolution. Callers MUST pass timestamps already truncated to
// this precision so that what is signed equals what is later served.
const identityRecordTimeFormat = time.RFC3339

// userIdentityHeaders returns the header map covered by userSignature.
func userIdentityHeaders(username, keyID string) map[string]string {
	return map[string]string{
		"type":     "identity-user",
		"username": username,
		"keyID":    keyID,
	}
}

// buildUserIdentityPayload returns the exact bytes the user signs.
// `bio` may be empty; it is placed in the envelope's content section and
// is not escaped.
func buildUserIdentityPayload(username, keyID, bio string) []byte {
	return bytesToSign(
		userIdentityHeaders(
			username,
			keyID,
		),
		bio,
	)
}

// profileHeaders returns the header map covered by serverSignature.
// `userSignatureB64` binds the user's attestation into the server-signed
// bytes; without this header the server signature would not detect a
// server that re-pairs a genuine userSignature with fabricated
// server-authored fields.
//
// Timestamp formatting: memberSince and signedAt are formatted with
// identityRecordTimeFormat in UTC. Callers own the truncation of the
// input times to whole seconds — this function does not modify them.
func profileHeaders(
	userID,
	username,
	keyID,
	serverID,
	serverKeyFingerprint,
	userSignatureB64,
	inviteID,
	role string,
	memberSince,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 "identity-server",
		"userID":               userID,
		"username":             username,
		"keyID":                keyID,
		"memberSince":          memberSince.UTC().Format(identityRecordTimeFormat),
		"role":                 role,
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             signedAt.UTC().Format(identityRecordTimeFormat),
		"userSignature":        userSignatureB64,
		"inviteID":             inviteID,
	}
}

// buildProfilePayload returns the exact bytes the server signs.
// `bio` is the same string that appeared in the user payload's content
// section — the two payloads share the same content, they only differ
// in headers. `inviteID` is the claimed invite's id when set; empty omits
// the header (bytesToSign drops empty values). `role` is always present
// (root | admin | user) — server-local policy bound by the countersignature.
func buildProfilePayload(
	userID,
	username,
	keyID,
	serverID,
	serverKeyFingerprint,
	userSignatureB64,
	inviteID,
	role,
	bio string,
	memberSince,
	signedAt time.Time,
) []byte {
	return bytesToSign(
		profileHeaders(
			userID,
			username,
			keyID,
			serverID,
			serverKeyFingerprint,
			userSignatureB64,
			inviteID,
			role,
			memberSince,
			signedAt,
		),
		bio,
	)
}

// reedCountersignHeaders builds the header map that the server signs when
// countersigning a reed. SignReed and client-side verifyReed (SPA) /
// recovery verifyReedCountersig construct this identical map and feed it to
// bytesToSign; that single source of truth is what keeps the two
// sides in lockstep.
//
// reedID is the full canonical id (authorID@serverID/uuid) — it alone binds
// the reed's identity, so there's no separate authorID header. Binding the
// fingerprint lets a verifier with multiple historical server keys pick the
// right one and keeps the signer's own identity covered by the signature.
func reedCountersignHeaders(serverID, reedID, fingerprint string, ts time.Time) map[string]string {
	return map[string]string{
		"fingerprint": fingerprint,
		"serverID":    serverID,
		"reedID":      reedID,
		"timestamp":   ts.UTC().Format(time.RFC3339),
	}
}

// buildReedPayload returns the exact bytes the server countersigns for a
// reed. reedID is the full canonical id; content is the author's detached
// signature, so the countersignature covers both where the reed lives and
// the user's attestation of its body.
//
// `timestamp` must already be truncated to whole seconds so that what
// is signed matches what Postgres stores after any timestamp
// round-trip.
func buildReedPayload(
	serverID,
	reedID,
	fingerprint,
	signature string,
	timestamp time.Time,
) []byte {
	return bytesToSign(
		reedCountersignHeaders(
			serverID,
			reedID,
			fingerprint,
			timestamp,
		),
		signature,
	)
}

// publicKeyCountersignHeaders is the header map signed over a user
// public key. Content is the armored key.
func publicKeyCountersignHeaders(userID, keyID, serverID, serverKeyFingerprint string, ts time.Time) map[string]string {
	return map[string]string{
		"keyID":                keyID,
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             ts.UTC().Format(time.RFC3339),
		"userID":               userID,
	}
}

// buildPublicKeyPayload returns the exact bytes the server countersigns
// for a user's public key. Headers bind ownership and issuance
// (userID, user key id, serverID, server-key fingerprint,
// signedAt); content is the armored key itself, so a verifier can
// check that this server attested this specific key for this user.
//
// `timestamp` must already be truncated to whole seconds so that what
// is signed matches what Postgres stores after any timestamp
// round-trip.
func buildPublicKeyPayload(
	serverID,
	userID,
	userKeyID,
	serverFingerprint,
	publicKey string,
	timestamp time.Time,
) []byte {
	return bytesToSign(
		publicKeyCountersignHeaders(
			userID,
			userKeyID,
			serverID,
			serverFingerprint,
			timestamp,
		),
		publicKey,
	)
}

// userRevocationHeaders returns the header map the key owner signs when
// revoking. Content is the free-text reason (may be empty).
func userRevocationHeaders(userID, keyID string) map[string]string {
	return map[string]string{
		"type":   "revocation",
		"userID": userID,
		"keyID":  keyID,
	}
}

// buildUserRevocationPayload returns the exact bytes the key being
// revoked must sign to produce the wire `signature` field.
func buildUserRevocationPayload(userID, keyID, reason string) []byte {
	return bytesToSign(
		userRevocationHeaders(
			userID,
			keyID,
		),
		reason,
	)
}

// serverRevocationHeaders returns the header map the server countersigns.
// userSignatureB64 binds the user's attestation into the server-signed
// bytes, same pattern as identity records.
func serverRevocationHeaders(
	userID,
	keyID,
	serverID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 "revocation",
		"userID":               userID,
		"keyID":                keyID,
		"signedAt":             signedAt.UTC().Format(identityRecordTimeFormat),
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignatureB64,
	}
}

// buildServerRevocationPayload returns the exact bytes the server signs
// when countersigning a revocation. signedAt becomes server.timestamp
// on the wire.
func buildServerRevocationPayload(
	userID,
	keyID,
	reason,
	serverID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) []byte {
	return bytesToSign(
		serverRevocationHeaders(
			userID,
			keyID,
			serverID,
			serverKeyFingerprint,
			userSignatureB64,
			signedAt,
		),
		reason,
	)
}

// identityTypeReed is the wire and signed-header `type` for a single-reed
// removal certificate (JSON `"type": "reed"`). Account removals use a
// different value; do not invent aliases such as `reed_removal`.
const identityTypeReed = "reed"

// reedRemovalUserHeaders returns the header map the reed author signs when
// requesting removal. Content is empty.
func reedRemovalUserHeaders(serverID, reedID string) map[string]string {
	return map[string]string{
		"type":     identityTypeReed,
		"serverID": serverID,
		"reedID":   reedID,
	}
}

// buildReedRemovalUserPayload returns the exact bytes the reed author signs
// to produce the wire `signature` field on a reed-removal cert. reedID is
// the full canonical id.
func buildReedRemovalUserPayload(serverID, reedID string) []byte {
	return bytesToSign(
		reedRemovalUserHeaders(serverID, reedID),
		"",
	)
}

// reedRemovalServerHeaders returns the header map the server countersigns.
// userSignatureB64 binds the author's attestation into the server-signed
// bytes (same class as identity / revocation countersign). reedID is the
// full canonical id.
func reedRemovalServerHeaders(
	serverID,
	reedID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 identityTypeReed,
		"serverID":             serverID,
		"reedID":               reedID,
		"signedAt":             signedAt.UTC().Format(identityRecordTimeFormat),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignatureB64,
	}
}

// buildReedRemovalServerPayload returns the exact bytes the server signs
// when countersigning a reed removal. signedAt becomes server.timestamp
// on the wire.
//
// `signedAt` must already be truncated to whole seconds so that what is
// signed matches what Postgres stores after any timestamp round-trip.
func buildReedRemovalServerPayload(
	serverID,
	reedID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) []byte {
	return bytesToSign(
		reedRemovalServerHeaders(
			serverID,
			reedID,
			serverKeyFingerprint,
			userSignatureB64,
			signedAt,
		),
		"",
	)
}

// identityTypeReedLike is the wire and signed-header `type` for a
// reed-like certificate (JSON `"type": "reed_like"`).
const identityTypeReedLike = "reed_like"

// reedLikeUserHeaders returns the header map the liker signs. reedID is
// the target reed's full canonical id. keyID names the liker's own
// signing key, so the server verifies against that exact key — avoids a
// spurious verification failure if the liker rotates keys between signing
// and the server processing the request. Content is empty.
func reedLikeUserHeaders(reedID, keyID string) map[string]string {
	return map[string]string{
		"type":   identityTypeReedLike,
		"reedID": reedID,
		"keyID":  keyID,
	}
}

// buildReedLikeUserPayload returns the exact bytes the liker signs to
// produce the wire `signature` field on a reed-like cert.
func buildReedLikeUserPayload(reedID, keyID string) []byte {
	return bytesToSign(
		reedLikeUserHeaders(reedID, keyID),
		"",
	)
}

// reedLikeServerHeaders returns the header map the server countersigns.
// userSignatureB64 binds the liker's attestation into the server-signed
// bytes (same class as identity / revocation / reed-removal countersign).
func reedLikeServerHeaders(
	reedID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 identityTypeReedLike,
		"reedID":               reedID,
		"signedAt":             signedAt.UTC().Format(identityRecordTimeFormat),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignatureB64,
	}
}

// buildReedLikeServerPayload returns the exact bytes the server signs
// when countersigning a reed like. signedAt becomes server.timestamp on
// the wire.
//
// `signedAt` must already be truncated to whole seconds so that what is
// signed matches what Postgres stores after any timestamp round-trip.
func buildReedLikeServerPayload(
	reedID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) []byte {
	return bytesToSign(
		reedLikeServerHeaders(
			reedID,
			serverKeyFingerprint,
			userSignatureB64,
			signedAt,
		),
		"",
	)
}

// identityTypeAccount is the wire and signed-header `type` for account removal.
const identityTypeAccount = "account"

func accountRemovalUserHeaders(serverID, userID string) map[string]string {
	return map[string]string{
		"type":     identityTypeAccount,
		"serverID": serverID,
		"userID":   userID,
	}
}

// buildAccountRemovalUserPayload returns the bytes the user signs to remove
// their account. note is envelope content (may be empty; ≤140 enforced at API).
func buildAccountRemovalUserPayload(serverID, userID, note string) []byte {
	return bytesToSign(
		accountRemovalUserHeaders(serverID, userID),
		note,
	)
}

func accountRemovalServerHeaders(
	serverID,
	userID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 identityTypeAccount,
		"serverID":             serverID,
		"userID":               userID,
		"signedAt":             signedAt.UTC().Format(identityRecordTimeFormat),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignatureB64,
	}
}

// buildAccountRemovalServerPayload returns the bytes the server countersigns.
// note is the same content the user signed.
func buildAccountRemovalServerPayload(
	serverID,
	userID,
	note,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) []byte {
	return bytesToSign(
		accountRemovalServerHeaders(
			serverID,
			userID,
			serverKeyFingerprint,
			userSignatureB64,
			signedAt,
		),
		note,
	)
}

// identityTypeInviteUser / identityTypeInviteServer distinguish user vs
// server invite payloads (same split as identity-user / identity-server).
const (
	identityTypeInviteUser   = "invite-user"
	identityTypeInviteServer = "invite-server"
)

func inviteUserHeaders(serverID, userID, inviteID, tokenHash, grantedRole string, createdAt time.Time) map[string]string {
	return map[string]string{
		"type":        identityTypeInviteUser,
		"serverID":    serverID,
		"userID":      userID,
		"inviteID":    inviteID,
		"tokenHash":   tokenHash,
		"grantedRole": grantedRole,
		"createdAt":   createdAt.UTC().Format(identityRecordTimeFormat),
	}
}

// buildInviteUserPayload returns the bytes the issuer signs over invite id,
// createdAt, tokenHash (SHA-256 of the fragment secret), and grantedRole
// (user | admin). The secret itself is never signed or sent on create.
func buildInviteUserPayload(serverID, userID, inviteID, tokenHash, grantedRole string, createdAt time.Time) []byte {
	return bytesToSign(
		inviteUserHeaders(serverID, userID, inviteID, tokenHash, grantedRole, createdAt),
		"",
	)
}

func inviteServerHeaders(
	serverID,
	userID,
	inviteID,
	tokenHash,
	serverKeyFingerprint,
	userSignatureB64 string,
	createdAt,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 identityTypeInviteServer,
		"serverID":             serverID,
		"userID":               userID,
		"inviteID":             inviteID,
		"tokenHash":            tokenHash,
		"createdAt":            createdAt.UTC().Format(identityRecordTimeFormat),
		"signedAt":             signedAt.UTC().Format(identityRecordTimeFormat),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignatureB64,
	}
}

// buildInviteServerPayload returns the bytes the server countersigns for an
// invite. createdAt is the user-authored resource time; signedAt is when the
// server attested. Both must already be truncated to whole seconds.
func buildInviteServerPayload(
	serverID,
	userID,
	inviteID,
	tokenHash,
	serverKeyFingerprint,
	userSignatureB64 string,
	createdAt,
	signedAt time.Time,
) []byte {
	return bytesToSign(
		inviteServerHeaders(
			serverID,
			userID,
			inviteID,
			tokenHash,
			serverKeyFingerprint,
			userSignatureB64,
			createdAt,
			signedAt,
		),
		"",
	)
}

// buildNewProfilePayload is a convenience wrapper around
// buildProfilePayload for the initial signup record: bio is always empty
// (users can't set it before their account exists), and memberSince ==
// signedAt == the moment the record is minted. Later records produced by
// profile-update flows keep memberSince pinned and only advance signedAt,
// so they must call buildProfilePayload directly.
//
// `timestamp` must already be truncated to whole seconds so that what
// is signed matches what Postgres stores after any timestamp
// round-trip.
func buildNewProfilePayload(
	userID,
	username,
	keyID,
	serverID,
	serverKeyFingerprint,
	userSignatureB64,
	inviteID,
	role string,
	timestamp time.Time,
) []byte {
	return buildProfilePayload(
		userID,
		username,
		keyID,
		serverID,
		serverKeyFingerprint,
		userSignatureB64,
		inviteID,
		role,
		"",        // bio
		timestamp, // memberSince
		timestamp, // signedAt
	)
}

// buildFederationInvitationPayload returns the canonical bytes the
// initiator server signs for a federation invitation (distinct from user
// identity payloads — do not reuse identity-user/identity-server types).
func buildFederationInvitationPayload(inviteID, serverID, baseURL, fingerprint, secret string) []byte {
	return bytesToSign(map[string]string{
		"baseUrl":     baseURL,
		"fingerprint": fingerprint,
		"inviteId":    inviteID,
		"secret":      secret,
		"serverId":    serverID,
	}, "")
}

// buildFederationConnectPayload returns the canonical bytes the responder
// server signs when calling back to POST /federation/connect/{inviteId},
// binding its identity to the specific invite. No secret: the responder
// proves possession of the invite separately via the secret field on the
// connect request body, not by signing over it.
func buildFederationConnectPayload(inviteID, serverID, baseURL, fingerprint string) []byte {
	return bytesToSign(map[string]string{
		"baseUrl":     baseURL,
		"fingerprint": fingerprint,
		"inviteId":    inviteID,
		"serverId":    serverID,
	}, "")
}

// rippleUserHeaders returns the header map covered by a ripple response's
// userSignature. reedID is the full canonical id of the parent reed.
// threadID is always present (client-minted, see specs/ripples/00_design.md);
// replyingTo is omitted (and therefore dropped by bytesToSign) for a
// top-level post. No timestamp — client clocks are never signed over,
// same as every other user payload in this file.
func rippleUserHeaders(reedID, rippleAuthorID, keyID, threadID, replyingTo string) map[string]string {
	return map[string]string{
		"reedID":         reedID,
		"rippleAuthorID": rippleAuthorID,
		"keyID":          keyID,
		"threadID":       threadID,
		"replyingTo":     replyingTo,
	}
}

// buildRippleUserPayload returns the exact bytes a ripple's author signs.
// `content` is the ripple text, placed in the envelope's content section
// verbatim, unescaped. `replyingTo` may be empty for a top-level post.
func buildRippleUserPayload(reedID, rippleAuthorID, keyID, threadID, replyingTo, content string) []byte {
	return bytesToSign(
		rippleUserHeaders(reedID, rippleAuthorID, keyID, threadID, replyingTo),
		content,
	)
}

// rippleServerHeaders returns the header map covered by a ripple
// response's serverSignature: the same fields the user signed, plus
// serverID and a server-supplied timestamp. Binding reedID/rippleAuthorID/
// threadID/replyingTo kills cross-reed, cross-author, and cross-thread
// replay; binding the server-key fingerprint lets a verifier with
// multiple historical server keys pick the right one.
func rippleServerHeaders(serverID, reedID, rippleAuthorID, keyID, threadID, replyingTo string, ts time.Time) map[string]string {
	return map[string]string{
		"serverID":       serverID,
		"reedID":         reedID,
		"rippleAuthorID": rippleAuthorID,
		"keyID":          keyID,
		"threadID":       threadID,
		"replyingTo":     replyingTo,
		"timestamp":      ts.UTC().Format(identityRecordTimeFormat),
	}
}

// buildRippleServerPayload returns the exact bytes the server
// countersigns for a ripple response. Content is the author's detached
// signature (not the ripple text), mirroring buildReedPayload exactly —
// the countersignature covers both the ripple's identity and the user's
// attestation of it. The response's id is the hash of these bytes (see
// specs/ripples/00_design.md's Signing section) — frozen at creation,
// never recomputed.
//
// `timestamp` must already be truncated to whole seconds so that what is
// signed matches what Postgres stores after any timestamp round-trip.
func buildRippleServerPayload(serverID, reedID, rippleAuthorID, keyID, threadID, replyingTo, userSignatureB64 string, timestamp time.Time) []byte {
	return bytesToSign(
		rippleServerHeaders(serverID, reedID, rippleAuthorID, keyID, threadID, replyingTo, timestamp),
		userSignatureB64,
	)
}
