// Package identity holds canonical byte sequences for signed identity
// records and related countersignatures (keys, reeds, revocations,
// reed removals).
//
// An identity record has two signed byte sequences that overlap but are
// not identical:
//
//   - The USER payload covers only user-authored fields (username,
//     fingerprint, avatarURL, and bio as the envelope content). The user's
//     detached PGP signature over these bytes is `userSignature`.
//
//   - The SERVER payload covers a superset: user-authored fields + all
//     server-authored fields (userID, memberSince, serverID,
//     serverKeyFingerprint, signedAt) + the userSignature itself as a
//     header. The server's detached PGP signature over these bytes is
//     `serverSignature`.
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

// Algorithm is the value stored in the response's `server.algorithm`
// field and, by convention, describes both signatures: an armored PGP
// detached signature transported as base64 on the wire.
const Algorithm = "PGP+base64"

// UserIdentitySignedFields lists the logical fields covered by
// BuildUserIdentityPayload (headers + bio content). Informational for
// storage / wire signedFields.
var UserIdentitySignedFields = []string{
	"type", "username", "fingerprint", "avatarURL", "bio",
}

// ProfileSignedFields lists the logical fields covered by
// BuildProfilePayload (headers + bio content).
var ProfileSignedFields = []string{
	"type", "userID", "username", "fingerprint", "avatarURL",
	"memberSince", "serverID", "serverKeyFingerprint", "signedAt",
	"userSignature", "bio",
}

// PublicKeySignedFields lists the logical fields covered by
// BuildPublicKeyPayload (headers + armor content).
var PublicKeySignedFields = []string{
	"fingerprint", "serverID", "serverKeyFingerprint", "signedAt",
	"userID", "armor",
}

// UserRevocationSignedFields lists the logical fields covered by
// BuildUserRevocationPayload (headers + reason content).
var UserRevocationSignedFields = []string{
	"type", "userID", "fingerprint", "reason",
}

// ServerRevocationSignedFields lists the logical fields covered by
// BuildServerRevocationPayload (headers + reason content).
var ServerRevocationSignedFields = []string{
	"type", "userID", "fingerprint", "signedAt", "serverID",
	"serverKeyFingerprint", "userSignature", "reason",
}

// ReedRemovalUserSignedFields lists fields covered by
// BuildReedRemovalUserPayload.
var ReedRemovalUserSignedFields = []string{
	"type", "serverID", "userID", "reedID",
}

// ReedRemovalServerSignedFields lists fields covered by
// BuildReedRemovalServerPayload.
var ReedRemovalServerSignedFields = []string{
	"type", "serverID", "userID", "reedID", "signedAt",
	"serverKeyFingerprint", "userSignature",
}

// AccountRemovalUserSignedFields lists fields covered by
// BuildAccountRemovalUserPayload (headers + note content).
var AccountRemovalUserSignedFields = []string{
	"type", "serverID", "userID", "note",
}

// AccountRemovalServerSignedFields lists fields covered by
// BuildAccountRemovalServerPayload (headers + note content).
var AccountRemovalServerSignedFields = []string{
	"type", "serverID", "userID", "signedAt",
	"serverKeyFingerprint", "userSignature", "note",
}

// recordTimeFormat is the canonical time format used for memberSince
// and signedAt headers in the signed bytes. UTC + RFC3339 seconds
// resolution. Callers MUST pass timestamps already truncated to this
// precision so that what is signed equals what is later served.
const recordTimeFormat = time.RFC3339

// userIdentityHeaders returns the header map covered by userSignature.
// `avatarURL` is omitted from the signed bytes when empty, matching the
// envelope's "absent == empty" convention (see signing.BytesToSign).
func userIdentityHeaders(username, fingerprint, avatarURL string) map[string]string {
	return map[string]string{
		"type":        "identity-user",
		"username":    username,
		"fingerprint": fingerprint,
		"avatarURL":   avatarURL,
	}
}

// BuildUserIdentityPayload returns the exact bytes the user signs.
// `bio` may be empty; it is placed in the envelope's content section and
// is not escaped.
func BuildUserIdentityPayload(username, fingerprint, avatarURL, bio string) []byte {
	return signing.BytesToSign(
		userIdentityHeaders(
			username,
			fingerprint,
			avatarURL,
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
	avatarURL,
	serverID,
	serverKeyFingerprint,
	userSignatureB64 string,
	memberSince,
	signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 "identity-server",
		"userID":               userID,
		"username":             username,
		"fingerprint":          fingerprint,
		"avatarURL":            avatarURL,
		"memberSince":          memberSince.UTC().Format(recordTimeFormat),
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             signedAt.UTC().Format(recordTimeFormat),
		"userSignature":        userSignatureB64,
	}
}

// BuildProfilePayload returns the exact bytes the server signs.
// `bio` is the same string that appeared in the user payload's content
// section — the two payloads share the same content, they only differ
// in headers.
func BuildProfilePayload(
	userID,
	username,
	fingerprint,
	avatarURL,
	serverID,
	serverKeyFingerprint,
	userSignatureB64,
	bio string,
	memberSince,
	signedAt time.Time,
) []byte {
	return signing.BytesToSign(
		profileHeaders(
			userID,
			username,
			fingerprint,
			avatarURL,
			serverID,
			serverKeyFingerprint,
			userSignatureB64,
			memberSince,
			signedAt,
		),
		bio,
	)
}

// ReedCountersignHeaders builds the header map that the server signs when
// countersigning a reed. Both SignReed and VerifySignature construct this
// identical map and feed it to signing.BytesToSign; that single source of
// truth is what keeps the two sides in lockstep.
//
// The header set binds `algorithm`, `id`, `timestamp`, `reedID`, `authorID`,
// and the server signing-key `fingerprint`. Binding reedID/authorID kills
// the cross-reed and cross-author replay classes; binding the fingerprint
// lets a verifier with multiple historical server keys pick the right one
// and keeps the signer's own identity covered by the signature.
func ReedCountersignHeaders(serverID, reedID, authorID, fingerprint string, ts time.Time) map[string]string {
	return map[string]string{
		"algorithm":   "PGP+base64",
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

// BuildNewProfilePayload is a convenience wrapper around
// BuildProfilePayload for the initial signup record: avatarURL
// and bio are always empty (users can't set them before their account
// exists), and memberSince == signedAt == the moment the record is
// minted. Later records produced by profile-update flows keep
// memberSince pinned and only advance signedAt, so they must call
// BuildProfilePayload directly.
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
	userSignatureB64 string,
	timestamp time.Time,
) []byte {
	return BuildProfilePayload(
		userID,
		username,
		fingerprint,
		"", // avatarURL
		serverID,
		serverKeyFingerprint,
		userSignatureB64,
		"",        // bio
		timestamp, // memberSince
		timestamp, // signedAt
	)
}
