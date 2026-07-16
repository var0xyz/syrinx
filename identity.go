// identity.go — canonical byte sequences for signed identity records.
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
package main

import (
	"time"

	"syrinx/signing"
)

// identityAlgorithm is the value stored in the response's `server.algorithm`
// field and, by convention, describes both signatures: an armored PGP
// detached signature transported as base64 on the wire.
const identityAlgorithm = "PGP+base64"

// identityRecordTimeFormat is the canonical time format used for
// memberSince and signedAt headers in the signed bytes. UTC + RFC3339
// seconds resolution. Callers MUST pass timestamps already truncated to
// this precision so that what is signed equals what is later served.
const identityRecordTimeFormat = time.RFC3339

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

// buildUserIdentityPayload returns the exact bytes the user signs.
// `bio` may be empty; it is placed in the envelope's content section and
// is not escaped.
func buildUserIdentityPayload(username, fingerprint, avatarURL, bio string) []byte {
	return signing.BytesToSign(userIdentityHeaders(username, fingerprint, avatarURL), bio)
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
	userID, username, fingerprint, avatarURL,
	serverID, serverKeyFingerprint, userSignatureB64 string,
	memberSince, signedAt time.Time,
) map[string]string {
	return map[string]string{
		"type":                 "identity-server",
		"userID":               userID,
		"username":             username,
		"fingerprint":          fingerprint,
		"avatarURL":            avatarURL,
		"memberSince":          memberSince.UTC().Format(identityRecordTimeFormat),
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             signedAt.UTC().Format(identityRecordTimeFormat),
		"userSignature":        userSignatureB64,
	}
}

// buildProfilePayload returns the exact bytes the server signs.
// `bio` is the same string that appeared in the user payload's content
// section — the two payloads share the same content, they only differ
// in headers.
func buildProfilePayload(
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
			userID, username, fingerprint, avatarURL,
			serverID, serverKeyFingerprint, userSignatureB64,
			memberSince, signedAt,
		),
		bio,
	)
}

// buildReedPayload returns the exact bytes the server countersigns for a
// reed. Headers bind the reed's identity (serverID, reedID, authorID,
// server-key fingerprint, timestamp); content is the author's detached
// signature, so the countersignature covers both where the reed lives
// and the user's attestation of its body.
//
// `timestamp` must already be truncated to whole seconds so that what
// is signed matches what Postgres stores after any timestamp
// round-trip.
func buildReedPayload(
	serverID,
	userID,
	reedID,
	fingerprint,
	signature string,
	timestamp time.Time,
) []byte {
	return signing.BytesToSign(
		reedCountersignHeaders(
			serverID,
			reedID,
			userID,
			fingerprint,
			timestamp,
		),
		signature,
	)
}

// buildPublicKeyPayload returns the exact bytes the server countersigns
// for a user's public key. Headers bind ownership and issuance
// (userID, user fingerprint, serverID, server-key fingerprint,
// signedAt); content is the armored key itself, so a verifier can
// check that this server attested this specific key for this user.
//
// `timestamp` must already be truncated to whole seconds so that what
// is signed matches what Postgres stores after any timestamp
// round-trip.
func buildPublicKeyPayload(
	serverID,
	userID,
	userFingerprint,
	serverFingerprint,
	publicKey string,
	timestamp time.Time,
) []byte {
	return signing.BytesToSign(
		publicKeyCountersignHeaders(
			userID,
			userFingerprint,
			serverID,
			serverFingerprint,
			timestamp,
		),
		publicKey,
	)
}

// buildNewProfilePayload is a convenience wrapper around
// buildProfilePayload for the initial signup record: avatarURL
// and bio are always empty (users can't set them before their account
// exists), and memberSince == signedAt == the moment the record is
// minted. Later records produced by profile-update flows keep
// memberSince pinned and only advance signedAt, so they must call
// buildProfilePayload directly.
//
// `timestamp` must already be truncated to whole seconds so that what
// is signed matches what Postgres stores after any timestamp
// round-trip.
func buildNewProfilePayload(
	userID,
	username,
	fingerprint,
	serverID,
	serverKeyFingerprint,
	userSignatureB64 string,
	timestamp time.Time,
) []byte {
	return buildProfilePayload(
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
