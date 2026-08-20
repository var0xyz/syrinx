// Package identity holds canonical byte sequences for signed identity
// records and related countersignatures (keys, reeds, revocations,
// reed removals).
//
// An identity record has two signed byte sequences that overlap but are
// not identical:
//
//   - The USER payload covers only user-authored fields (username,
//     fingerprint, and bio as the envelope content). The user's
//     detached PGP signature over these bytes is `userSignature`.
//
//   - The SERVER payload covers a superset: user-authored fields + all
//     server-authored fields (userID, memberSince, serverID,
//     serverKeyFingerprint, signedAt, invitedBy) + the userSignature
//     itself as a header. The server's detached PGP signature over these
//     bytes is `serverSignature`. `invitedBy` is omitted when empty
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
// Both payloads flow through signing.BytesToSign, which is the sole
// canonicalisation authority. Do not build these byte sequences any
// other way.
package identity

import (
	"time"

	"syrinx/signing"
)

// recordTimeFormat is the canonical time format used for memberSince
// and signedAt headers in the signed bytes. UTC + RFC3339 seconds
// resolution. Callers MUST pass timestamps already truncated to this
// precision so that what is signed equals what is later served.
const recordTimeFormat = time.RFC3339

// userIdentityHeaders returns the header map covered by userSignature.
func userIdentityHeaders(username, fingerprint string) map[string]string {
	return map[string]string{
		"type":        "identity-user",
		"username":    username,
		"fingerprint": fingerprint,
	}
}

// BuildUserIdentityPayload returns the exact bytes the user signs.
// `bio` may be empty; it is placed in the envelope's content section and
// is not escaped.
func BuildUserIdentityPayload(username, fingerprint, bio string) []byte {
	return signing.BytesToSign(
		userIdentityHeaders(
			username,
			fingerprint,
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
// recordTimeFormat in UTC. Callers own the truncation of the
// input times to whole seconds — this function does not modify them.
func profileHeaders(
	userID,
	username,
	fingerprint,
	serverID,
	serverKeyFingerprint,
	userSignatureB64,
	invitedBy,
	role string,
	memberSince,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 "identity-server",
		"userID":               userID,
		"username":             username,
		"fingerprint":          fingerprint,
		"memberSince":          memberSince.UTC().Format(recordTimeFormat),
		"role":                 role,
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             signedAt.UTC().Format(recordTimeFormat),
		"userSignature":        userSignatureB64,
		"invitedBy":            invitedBy,
	}
}

// BuildProfilePayload returns the exact bytes the server signs.
// `bio` is the same string that appeared in the user payload's content
// section — the two payloads share the same content, they only differ
// in headers. `invitedBy` is the inviter's userID when set; empty omits
// the header (BytesToSign drops empty values). `role` is always present
// (root | admin | user) — server-local policy bound by the countersignature.
func BuildProfilePayload(
	userID,
	username,
	fingerprint,
	serverID,
	serverKeyFingerprint,
	userSignatureB64,
	invitedBy,
	role,
	bio string,
	memberSince,
	signedAt time.Time,
) []byte {
	return signing.BytesToSign(
		profileHeaders(
			userID,
			username,
			fingerprint,
			serverID,
			serverKeyFingerprint,
			userSignatureB64,
			invitedBy,
			role,
			memberSince,
			signedAt,
		),
		bio,
	)
}

// ReedCountersignHeaders builds the header map that the server signs when
// countersigning a reed. SignReed and client-side verifyReed (SPA) /
// recovery verifyReedCountersig construct this identical map and feed it to
// signing.BytesToSign; that single source of truth is what keeps the two
// sides in lockstep.
//
// The header set binds `serverID`, `timestamp`, `reedID`, `authorID`, and
// the server signing-key `fingerprint`. Binding reedID/authorID kills the
// cross-reed and cross-author replay classes; binding the fingerprint lets
// a verifier with multiple historical server keys pick the right one and
// keeps the signer's own identity covered by the signature.
func ReedCountersignHeaders(serverID, reedID, authorID, fingerprint string, ts time.Time) map[string]string {
	return map[string]string{
		"authorID":    authorID,
		"fingerprint": fingerprint,
		"serverID":    serverID,
		"reedID":      reedID,
		"timestamp":   ts.UTC().Format(time.RFC3339),
	}
}

// BuildReedPayload returns the exact bytes the server countersigns for a
// reed. Headers bind the reed's identity (serverID, reedID, authorID,
// server-key fingerprint, timestamp); content is the author's detached
// signature, so the countersignature covers both where the reed lives
// and the user's attestation of its body.
//
// `timestamp` must already be truncated to whole seconds so that what
// is signed matches what Postgres stores after any timestamp
// round-trip.
func BuildReedPayload(
	serverID,
	userID,
	reedID,
	fingerprint,
	signature string,
	timestamp time.Time,
) []byte {
	return signing.BytesToSign(
		ReedCountersignHeaders(
			serverID,
			reedID,
			userID,
			fingerprint,
			timestamp,
		),
		signature,
	)
}

// PublicKeyCountersignHeaders is the header map signed over a user
// public key. Content is the armored key.
func PublicKeyCountersignHeaders(userID, fingerprint, serverID, serverKeyFingerprint string, ts time.Time) map[string]string {
	return map[string]string{
		"fingerprint":          fingerprint,
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             ts.UTC().Format(time.RFC3339),
		"userID":               userID,
	}
}

// BuildPublicKeyPayload returns the exact bytes the server countersigns
// for a user's public key. Headers bind ownership and issuance
// (userID, user fingerprint, serverID, server-key fingerprint,
// signedAt); content is the armored key itself, so a verifier can
// check that this server attested this specific key for this user.
//
// `timestamp` must already be truncated to whole seconds so that what
// is signed matches what Postgres stores after any timestamp
// round-trip.
func BuildPublicKeyPayload(
	serverID,
	userID,
	userFingerprint,
	serverFingerprint,
	publicKey string,
	timestamp time.Time,
) []byte {
	return signing.BytesToSign(
		PublicKeyCountersignHeaders(
			userID,
			userFingerprint,
			serverID,
			serverFingerprint,
			timestamp,
		),
		publicKey,
	)
}

// userRevocationHeaders returns the header map the key owner signs when
// revoking. Content is the free-text reason (may be empty).
func userRevocationHeaders(userID, fingerprint string) map[string]string {
	return map[string]string{
		"type":        "revocation",
		"userID":      userID,
		"fingerprint": fingerprint,
	}
}

// BuildUserRevocationPayload returns the exact bytes the key being
// revoked must sign to produce the wire `signature` field.
func BuildUserRevocationPayload(userID, fingerprint, reason string) []byte {
	return signing.BytesToSign(
		userRevocationHeaders(
			userID,
			fingerprint,
		),
		reason,
	)
}

// serverRevocationHeaders returns the header map the server countersigns.
// userSignatureB64 binds the user's attestation into the server-signed
// bytes, same pattern as identity records.
func serverRevocationHeaders(
	userID,
	fingerprint,
	serverID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 "revocation",
		"userID":               userID,
		"fingerprint":          fingerprint,
		"signedAt":             signedAt.UTC().Format(recordTimeFormat),
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignatureB64,
	}
}

// BuildServerRevocationPayload returns the exact bytes the server signs
// when countersigning a revocation. signedAt becomes server.timestamp
// on the wire.
func BuildServerRevocationPayload(
	userID,
	fingerprint,
	reason,
	serverID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) []byte {
	return signing.BytesToSign(
		serverRevocationHeaders(
			userID,
			fingerprint,
			serverID,
			serverKeyFingerprint,
			userSignatureB64,
			signedAt,
		),
		reason,
	)
}

// TypeReed is the wire and signed-header `type` for a single-reed removal
// certificate (JSON `"type": "reed"`). Account removals use a different
// value; do not invent aliases such as `reed_removal`.
const TypeReed = "reed"

// reedRemovalUserHeaders returns the header map the reed author signs when
// requesting removal. Content is empty.
func reedRemovalUserHeaders(serverID, userID, reedID string) map[string]string {
	return map[string]string{
		"type":     TypeReed,
		"serverID": serverID,
		"userID":   userID,
		"reedID":   reedID,
	}
}

// BuildReedRemovalUserPayload returns the exact bytes the reed author signs
// to produce the wire `signature` field on a reed-removal cert.
func BuildReedRemovalUserPayload(serverID, userID, reedID string) []byte {
	return signing.BytesToSign(
		reedRemovalUserHeaders(serverID, userID, reedID),
		"",
	)
}

// reedRemovalServerHeaders returns the header map the server countersigns.
// userSignatureB64 binds the author's attestation into the server-signed
// bytes (same class as identity / revocation countersign).
func reedRemovalServerHeaders(
	serverID,
	userID,
	reedID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 TypeReed,
		"serverID":             serverID,
		"userID":               userID,
		"reedID":               reedID,
		"signedAt":             signedAt.UTC().Format(recordTimeFormat),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignatureB64,
	}
}

// BuildReedRemovalServerPayload returns the exact bytes the server signs
// when countersigning a reed removal. signedAt becomes server.timestamp
// on the wire.
//
// `signedAt` must already be truncated to whole seconds so that what is
// signed matches what Postgres stores after any timestamp round-trip.
func BuildReedRemovalServerPayload(
	serverID,
	userID,
	reedID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) []byte {
	return signing.BytesToSign(
		reedRemovalServerHeaders(
			serverID,
			userID,
			reedID,
			serverKeyFingerprint,
			userSignatureB64,
			signedAt,
		),
		"",
	)
}

// TypeReedLike is the wire and signed-header `type` for a reed-like
// certificate (JSON `"type": "reed_like"`).
const TypeReedLike = "reed_like"

// reedLikeUserHeaders returns the header map the liker signs. authorID
// and reedID identify the target reed (same composite reference shape as
// every other reed reference in this codebase). fingerprint names the
// liker's own signing key, so the server verifies against that exact key
// — avoids a spurious verification failure if the liker rotates keys
// between signing and the server processing the request. Content is
// empty.
func reedLikeUserHeaders(serverID, authorID, reedID, fingerprint string) map[string]string {
	return map[string]string{
		"type":        TypeReedLike,
		"serverID":    serverID,
		"authorID":    authorID,
		"reedID":      reedID,
		"fingerprint": fingerprint,
	}
}

// BuildReedLikeUserPayload returns the exact bytes the liker signs to
// produce the wire `signature` field on a reed-like cert.
func BuildReedLikeUserPayload(serverID, authorID, reedID, fingerprint string) []byte {
	return signing.BytesToSign(
		reedLikeUserHeaders(serverID, authorID, reedID, fingerprint),
		"",
	)
}

// reedLikeServerHeaders returns the header map the server countersigns.
// userSignatureB64 binds the liker's attestation into the server-signed
// bytes (same class as identity / revocation / reed-removal countersign).
func reedLikeServerHeaders(
	serverID,
	authorID,
	reedID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 TypeReedLike,
		"serverID":             serverID,
		"authorID":             authorID,
		"reedID":               reedID,
		"signedAt":             signedAt.UTC().Format(recordTimeFormat),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignatureB64,
	}
}

// BuildReedLikeServerPayload returns the exact bytes the server signs
// when countersigning a reed like. signedAt becomes server.timestamp on
// the wire.
//
// `signedAt` must already be truncated to whole seconds so that what is
// signed matches what Postgres stores after any timestamp round-trip.
func BuildReedLikeServerPayload(
	serverID,
	authorID,
	reedID,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) []byte {
	return signing.BytesToSign(
		reedLikeServerHeaders(
			serverID,
			authorID,
			reedID,
			serverKeyFingerprint,
			userSignatureB64,
			signedAt,
		),
		"",
	)
}

// TypeAccount is the wire and signed-header `type` for account removal.
const TypeAccount = "account"

func accountRemovalUserHeaders(serverID, userID string) map[string]string {
	return map[string]string{
		"type":     TypeAccount,
		"serverID": serverID,
		"userID":   userID,
	}
}

// BuildAccountRemovalUserPayload returns the bytes the user signs to remove
// their account. note is envelope content (may be empty; ≤140 enforced at API).
func BuildAccountRemovalUserPayload(serverID, userID, note string) []byte {
	return signing.BytesToSign(
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
		"type":                 TypeAccount,
		"serverID":             serverID,
		"userID":               userID,
		"signedAt":             signedAt.UTC().Format(recordTimeFormat),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignatureB64,
	}
}

// BuildAccountRemovalServerPayload returns the bytes the server countersigns.
// note is the same content the user signed.
func BuildAccountRemovalServerPayload(
	serverID,
	userID,
	note,
	serverKeyFingerprint,
	userSignatureB64 string,
	signedAt time.Time,
) []byte {
	return signing.BytesToSign(
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

// TypeInviteUser / TypeInviteServer distinguish user vs server invite payloads
// (same split as identity-user / identity-server).
const (
	TypeInviteUser   = "invite-user"
	TypeInviteServer = "invite-server"
)

func inviteUserHeaders(serverID, userID, inviteID, tokenHash, grantedRole string, createdAt time.Time) map[string]string {
	return map[string]string{
		"type":        TypeInviteUser,
		"serverID":    serverID,
		"userID":      userID,
		"inviteID":    inviteID,
		"tokenHash":   tokenHash,
		"grantedRole": grantedRole,
		"createdAt":   createdAt.UTC().Format(recordTimeFormat),
	}
}

// BuildInviteUserPayload returns the bytes the issuer signs over invite id,
// createdAt, tokenHash (SHA-256 of the fragment secret), and grantedRole
// (user | admin). The secret itself is never signed or sent on create.
func BuildInviteUserPayload(serverID, userID, inviteID, tokenHash, grantedRole string, createdAt time.Time) []byte {
	return signing.BytesToSign(
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
		"type":                 TypeInviteServer,
		"serverID":             serverID,
		"userID":               userID,
		"inviteID":             inviteID,
		"tokenHash":            tokenHash,
		"createdAt":            createdAt.UTC().Format(recordTimeFormat),
		"signedAt":             signedAt.UTC().Format(recordTimeFormat),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignatureB64,
	}
}

// BuildInviteServerPayload returns the bytes the server countersigns for an
// invite. createdAt is the user-authored resource time; signedAt is when the
// server attested. Both must already be truncated to whole seconds.
func BuildInviteServerPayload(
	serverID,
	userID,
	inviteID,
	tokenHash,
	serverKeyFingerprint,
	userSignatureB64 string,
	createdAt,
	signedAt time.Time,
) []byte {
	return signing.BytesToSign(
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

// BuildNewProfilePayload is a convenience wrapper around
// BuildProfilePayload for the initial signup record: bio is always empty
// (users can't set it before their account exists), and memberSince ==
// signedAt == the moment the record is minted. Later records produced by
// profile-update flows keep memberSince pinned and only advance signedAt,
// so they must call BuildProfilePayload directly.
//
// `timestamp` must already be truncated to whole seconds so that what
// is signed matches what Postgres stores after any timestamp
// round-trip.
func BuildNewProfilePayload(
	userID,
	username,
	fingerprint,
	serverID,
	serverKeyFingerprint,
	userSignatureB64,
	invitedBy,
	role string,
	timestamp time.Time,
) []byte {
	return BuildProfilePayload(
		userID,
		username,
		fingerprint,
		serverID,
		serverKeyFingerprint,
		userSignatureB64,
		invitedBy,
		role,
		"",        // bio
		timestamp, // memberSince
		timestamp, // signedAt
	)
}

// BuildFederationInvitationPayload returns the canonical bytes the
// initiator server signs for a federation invitation (distinct from user
// identity payloads — do not reuse identity-user/identity-server types).
func BuildFederationInvitationPayload(inviteID, serverID, baseURL, fingerprint, secret string) []byte {
	return signing.BytesToSign(map[string]string{
		"baseUrl":     baseURL,
		"fingerprint": fingerprint,
		"inviteId":    inviteID,
		"secret":      secret,
		"serverId":    serverID,
	}, "")
}

// BuildFederationConnectPayload returns the canonical bytes the responder
// server signs when calling back to POST /federation/connect/{inviteId},
// binding its identity to the specific invite. No secret: the responder
// proves possession of the invite separately via the secret field on the
// connect request body, not by signing over it.
func BuildFederationConnectPayload(inviteID, serverID, baseURL, fingerprint string) []byte {
	return signing.BytesToSign(map[string]string{
		"baseUrl":     baseURL,
		"fingerprint": fingerprint,
		"inviteId":    inviteID,
		"serverId":    serverID,
	}, "")
}

// BuildFederationPeerRequestPayload returns the canonical bytes an
// established peer signs on every runtime, peer-authenticated request
// (specs/federation/04) — e.g. GET /api/federation/users/{userID}/identity.
// method+path bind the signature to this specific request (so a captured
// signature can't be replayed against a different endpoint); timestamp is
// the usual replay-window guard (see ValidateTimestamp).
func BuildFederationPeerRequestPayload(serverID, method, path, timestamp string) []byte {
	return signing.BytesToSign(map[string]string{
		"method":    method,
		"path":      path,
		"serverId":  serverID,
		"timestamp": timestamp,
	}, "")
}

// rippleUserHeaders returns the header map covered by a ripple response's
// userSignature. threadID is always present (client-minted, see
// specs/ripples/00_design.md); replyingTo is omitted (and therefore
// dropped by BytesToSign) for a top-level post. No timestamp — client
// clocks are never signed over, same as every other user payload in this
// package.
func rippleUserHeaders(reedAuthorID, reedID, rippleAuthorID, fingerprint, threadID, replyingTo string) map[string]string {
	return map[string]string{
		"reedAuthorID":   reedAuthorID,
		"reedID":         reedID,
		"rippleAuthorID": rippleAuthorID,
		"fingerprint":    fingerprint,
		"threadID":       threadID,
		"replyingTo":     replyingTo,
	}
}

// BuildRippleUserPayload returns the exact bytes a ripple's author signs.
// `content` is the ripple text, placed in the envelope's content section
// verbatim, unescaped. `replyingTo` may be empty for a top-level post.
func BuildRippleUserPayload(reedAuthorID, reedID, rippleAuthorID, fingerprint, threadID, replyingTo, content string) []byte {
	return signing.BytesToSign(
		rippleUserHeaders(reedAuthorID, reedID, rippleAuthorID, fingerprint, threadID, replyingTo),
		content,
	)
}

// rippleServerHeaders returns the header map covered by a ripple
// response's serverSignature: the same fields the user signed, plus
// serverID and a server-supplied timestamp. Binding reedAuthorID/reedID/
// rippleAuthorID/threadID/replyingTo kills cross-reed, cross-author, and
// cross-thread replay; binding the server-key fingerprint lets a
// verifier with multiple historical server keys pick the right one.
func rippleServerHeaders(serverID, reedAuthorID, reedID, rippleAuthorID, fingerprint, threadID, replyingTo string, ts time.Time) map[string]string {
	return map[string]string{
		"serverID":       serverID,
		"reedAuthorID":   reedAuthorID,
		"reedID":         reedID,
		"rippleAuthorID": rippleAuthorID,
		"fingerprint":    fingerprint,
		"threadID":       threadID,
		"replyingTo":     replyingTo,
		"timestamp":      ts.UTC().Format(recordTimeFormat),
	}
}

// BuildRippleServerPayload returns the exact bytes the server
// countersigns for a ripple response. Content is the author's detached
// signature (not the ripple text), mirroring BuildReedPayload exactly —
// the countersignature covers both the ripple's identity and the user's
// attestation of it. The response's id is the hash of these bytes (see
// specs/ripples/00_design.md's Signing section) — frozen at creation,
// never recomputed.
//
// `timestamp` must already be truncated to whole seconds so that what is
// signed matches what Postgres stores after any timestamp round-trip.
func BuildRippleServerPayload(serverID, reedAuthorID, reedID, rippleAuthorID, fingerprint, threadID, replyingTo, userSignatureB64 string, timestamp time.Time) []byte {
	return signing.BytesToSign(
		rippleServerHeaders(serverID, reedAuthorID, reedID, rippleAuthorID, fingerprint, threadID, replyingTo, timestamp),
		userSignatureB64,
	)
}
