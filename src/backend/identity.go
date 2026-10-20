// Canonical payloads for signed records and their countersignatures. Every
// payload is a field set serialized by canonicalJSON (RFC 8785); signers and
// verifiers on both sides call the same builder. Don't build them any other way.
package main

import (
	"time"
)

// identityRecordTimeFormat is the format of every signed timestamp: UTC,
// RFC3339, whole seconds. Callers truncate before signing.
const identityRecordTimeFormat = time.RFC3339

func signedTime(t time.Time) string {
	return t.UTC().Format(identityRecordTimeFormat)
}

// buildUserIdentityPayload returns the bytes a user signs for their profile.
func buildUserIdentityPayload(username, keyID, bio string) []byte {
	return canonicalJSON(signedFields{
		"type":     "identity-user",
		"username": username,
		"keyID":    keyID,
		"bio":      bio,
	})
}

// buildProfilePayload returns the bytes the server countersigns for a
// profile. userSignature welds the user's attestation to the server-authored
// fields; inviteID is omitted for open signups. permissions must be sorted.
func buildProfilePayload(
	userID,
	username,
	keyID,
	serverID,
	serverKeyFingerprint,
	userSignature,
	inviteID,
	role string,
	permissions []string,
	bio string,
	memberSince,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 "identity-server",
		"userID":               userID,
		"username":             username,
		"keyID":                keyID,
		"memberSince":          signedTime(memberSince),
		"role":                 role,
		"permissions":          permissions,
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             signedTime(signedAt),
		"userSignature":        userSignature,
		"inviteID":             inviteID,
		"bio":                  bio,
	})
}

// buildReedPayload returns the bytes the server countersigns for a reed:
// its full canonical id, the author's key and detached signature, and the
// server key fingerprint and timestamp.
func buildReedPayload(
	serverID,
	reedID,
	fingerprint,
	authorKeyID,
	userSignature string,
	timestamp time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"authorKeyID":   authorKeyID,
		"fingerprint":   fingerprint,
		"serverID":      serverID,
		"reedID":        reedID,
		"timestamp":     signedTime(timestamp),
		"userSignature": userSignature,
	})
}

// buildPublicKeyPayload returns the bytes the server countersigns for a
// user's public key: ownership, issuance, and the armored key itself.
func buildPublicKeyPayload(
	serverID,
	userID,
	userKeyID,
	serverFingerprint,
	armor string,
	timestamp time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"keyID":                userKeyID,
		"serverID":             serverID,
		"serverKeyFingerprint": serverFingerprint,
		"signedAt":             signedTime(timestamp),
		"userID":               userID,
		"armor":                armor,
	})
}

// buildUserRevocationPayload returns the bytes the key being revoked signs.
func buildUserRevocationPayload(userID, keyID, reason string) []byte {
	return canonicalJSON(signedFields{
		"type":   "revocation",
		"userID": userID,
		"keyID":  keyID,
		"reason": reason,
	})
}

// buildServerRevocationPayload returns the bytes the server countersigns
// for a revocation. signedAt becomes server.timestamp on the wire.
func buildServerRevocationPayload(
	userID,
	keyID,
	reason,
	serverID,
	serverKeyFingerprint,
	userSignature string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 "revocation",
		"userID":               userID,
		"keyID":                keyID,
		"signedAt":             signedTime(signedAt),
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignature,
		"reason":               reason,
	})
}

// identityTypeReed is the `type` of a single-reed removal certificate, on
// the wire and in the signed payload. Account removals use another value.
const identityTypeReed = "reed"

// buildReedRemovalUserPayload returns the bytes a reed's author signs to
// remove it. reedID is the full canonical id.
func buildReedRemovalUserPayload(serverID, reedID string) []byte {
	return canonicalJSON(signedFields{
		"type":     identityTypeReed,
		"serverID": serverID,
		"reedID":   reedID,
	})
}

// buildReedRemovalServerPayload returns the bytes the server countersigns
// for a reed removal. signedAt becomes server.timestamp on the wire.
func buildReedRemovalServerPayload(
	serverID,
	reedID,
	authorKeyID,
	serverKeyFingerprint,
	userSignature string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeReed,
		"serverID":             serverID,
		"reedID":               reedID,
		"authorKeyID":          authorKeyID,
		"signedAt":             signedTime(signedAt),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignature,
	})
}

// identityTypeThread is the signed `type` of a thread record.
const identityTypeThread = "thread"

// buildThreadUserPayload returns the bytes the author signs to certify which
// reeds form threadID, in order. reedIDs[0] is the head.
func buildThreadUserPayload(serverID, threadID string, reedIDs []string) []byte {
	return canonicalJSON(signedFields{
		"type":     identityTypeThread,
		"serverID": serverID,
		"threadID": threadID,
		"reedIDs":  reedIDs,
	})
}

// buildThreadServerPayload returns the bytes the server countersigns for a
// thread record.
func buildThreadServerPayload(
	serverID,
	threadID,
	authorKeyID,
	serverKeyFingerprint,
	userSignature string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeThread,
		"serverID":             serverID,
		"threadID":             threadID,
		"authorKeyID":          authorKeyID,
		"signedAt":             signedTime(signedAt),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignature,
	})
}

// identityTypeThreadRemoval is the signed `type` of a thread removal.
const identityTypeThreadRemoval = "thread_removal"

// buildThreadRemovalUserPayload returns the bytes the author signs to remove
// threadID. threadSignature pins the removal to the one record they signed.
func buildThreadRemovalUserPayload(serverID, threadID, threadSignature string) []byte {
	return canonicalJSON(signedFields{
		"type":            identityTypeThreadRemoval,
		"serverID":        serverID,
		"threadID":        threadID,
		"threadSignature": threadSignature,
	})
}

// buildThreadRemovalServerPayload returns the bytes the server countersigns
// for a thread removal.
func buildThreadRemovalServerPayload(
	serverID,
	threadID,
	authorKeyID,
	serverKeyFingerprint,
	userSignature string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeThreadRemoval,
		"serverID":             serverID,
		"threadID":             threadID,
		"authorKeyID":          authorKeyID,
		"signedAt":             signedTime(signedAt),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignature,
	})
}

// identityTypeReedLike is the `type` of a reed-like certificate.
const identityTypeReedLike = "reed_like"

// buildReedLikeUserPayload returns the bytes a liker signs. keyID names the
// liker's signing key, so a rotation mid-request can't fail verification.
func buildReedLikeUserPayload(reedID, keyID string) []byte {
	return canonicalJSON(signedFields{
		"type":   identityTypeReedLike,
		"reedID": reedID,
		"keyID":  keyID,
	})
}

// buildReedLikeServerPayload returns the bytes the server countersigns for
// a like. signedAt becomes server.timestamp on the wire.
func buildReedLikeServerPayload(
	reedID,
	serverKeyFingerprint,
	userSignature string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeReedLike,
		"reedID":               reedID,
		"signedAt":             signedTime(signedAt),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignature,
	})
}

// identityTypeBlock is the `type` of a block certificate.
const identityTypeBlock = "block"

// buildBlockUserPayload returns the bytes a blocking user signs. keyID names the
// blocking user's signing key, as for likes.
func buildBlockUserPayload(userID, blockedUserID, keyID string) []byte {
	return canonicalJSON(signedFields{
		"type":          identityTypeBlock,
		"userID":        userID,
		"blockedUserID": blockedUserID,
		"keyID":         keyID,
	})
}

// buildBlockServerPayload returns the bytes the blocking user's server
// countersigns. signedAt becomes server.timestamp on the wire.
func buildBlockServerPayload(
	userID,
	blockedUserID,
	serverKeyFingerprint,
	userSignature string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeBlock,
		"userID":               userID,
		"blockedUserID":        blockedUserID,
		"signedAt":             signedTime(signedAt),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignature,
	})
}

// MaxVouchNoteChars caps a vouch's optional public memo.
const MaxVouchNoteChars = 140

// buildVouchUserPayload returns the bytes a voucher signs. Key ids are
// owner-prefixed, so both users are named; no client timestamp.
func buildVouchUserPayload(voucherKeyID, subjectKeyID, note string) []byte {
	return canonicalJSON(signedFields{
		"voucherKeyID": voucherKeyID,
		"subjectKeyID": subjectKeyID,
		"note":         note,
	})
}

// buildVouchServerPayload returns the bytes the server countersigns for a
// vouch. The note is covered through the voucher's signature.
func buildVouchServerPayload(
	subjectKeyID,
	serverKeyFingerprint,
	userSignature string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"subjectKeyID":         subjectKeyID,
		"signedAt":             signedTime(signedAt),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignature,
	})
}

// buildVouchWithdrawalUserPayload returns the bytes signed to withdraw a
// vouch, by whatever key is current now. The vouch id fixes everything else.
func buildVouchWithdrawalUserPayload(vouchID string) []byte {
	return canonicalJSON(signedFields{
		"vouchID": vouchID,
	})
}

// buildVouchWithdrawalServerPayload returns the bytes the server
// countersigns to attest a withdrawal.
func buildVouchWithdrawalServerPayload(
	vouchID,
	serverKeyFingerprint,
	userSignature string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"vouchID":              vouchID,
		"signedAt":             signedTime(signedAt),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignature,
	})
}

// identityTypeServerKeyRevocation is the signed `type` of a server key
// revocation, which names the key's successor.
const identityTypeServerKeyRevocation = "server-key-revocation"

// buildServerKeyRevocationPayload returns the bytes both the revoked key and
// its successor sign.
func buildServerKeyRevocationPayload(
	serverID,
	keyID,
	successor string,
	compromised bool,
	reason string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":        identityTypeServerKeyRevocation,
		"serverID":    serverID,
		"keyID":       keyID,
		"successor":   successor,
		"compromised": compromised,
		"signedAt":    signedTime(signedAt),
		"reason":      reason,
	})
}

// identityTypeAccount is the `type` of an account removal certificate.
const identityTypeAccount = "account"

// buildAccountRemovalUserPayload returns the bytes a user signs to remove
// their account. note may be empty (≤140 enforced at the API).
func buildAccountRemovalUserPayload(serverID, userID, note string) []byte {
	return canonicalJSON(signedFields{
		"type":     identityTypeAccount,
		"serverID": serverID,
		"userID":   userID,
		"note":     note,
	})
}

// buildAccountRemovalServerPayload returns the bytes the server countersigns
// for an account removal.
func buildAccountRemovalServerPayload(
	serverID,
	userID,
	note,
	serverKeyFingerprint,
	userSignature string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeAccount,
		"serverID":             serverID,
		"userID":               userID,
		"signedAt":             signedTime(signedAt),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignature,
		"note":                 note,
	})
}

// identityTypeInviteUser / identityTypeInviteServer split user and server
// invite payloads, like identity-user / identity-server.
const (
	identityTypeInviteUser   = "invite-user"
	identityTypeInviteServer = "invite-server"
)

// buildInviteUserPayload returns the bytes an issuer signs for an invite.
// tokenHash is SHA-256 of the fragment secret, which is never signed or sent.
func buildInviteUserPayload(serverID, userID, inviteID, tokenHash, grantedRole string, createdAt time.Time) []byte {
	return canonicalJSON(signedFields{
		"type":        identityTypeInviteUser,
		"serverID":    serverID,
		"userID":      userID,
		"inviteID":    inviteID,
		"tokenHash":   tokenHash,
		"grantedRole": grantedRole,
		"createdAt":   signedTime(createdAt),
	})
}

// buildInviteServerPayload returns the bytes the server countersigns for an
// invite. createdAt is the issuer's time; signedAt is the server's.
func buildInviteServerPayload(
	serverID,
	userID,
	inviteID,
	tokenHash,
	serverKeyFingerprint,
	userSignature string,
	createdAt,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeInviteServer,
		"serverID":             serverID,
		"userID":               userID,
		"inviteID":             inviteID,
		"tokenHash":            tokenHash,
		"createdAt":            signedTime(createdAt),
		"signedAt":             signedTime(signedAt),
		"serverKeyFingerprint": serverKeyFingerprint,
		"userSignature":        userSignature,
	})
}

// buildNewProfilePayload is buildProfilePayload for a signup: empty bio, and
// memberSince == signedAt. Later updates call buildProfilePayload directly.
func buildNewProfilePayload(
	userID,
	username,
	keyID,
	serverID,
	serverKeyFingerprint,
	userSignature,
	inviteID,
	role string,
	permissions []string,
	timestamp time.Time,
) []byte {
	return buildProfilePayload(
		userID,
		username,
		keyID,
		serverID,
		serverKeyFingerprint,
		userSignature,
		inviteID,
		role,
		permissions,
		"",        // bio
		timestamp, // memberSince
		timestamp, // signedAt
	)
}

// buildFederationInvitationPayload returns the bytes the initiating server
// signs for a federation invitation.
func buildFederationInvitationPayload(inviteID, serverID, baseURL, frontendURL, fingerprint, secret string) []byte {
	return canonicalJSON(signedFields{
		"baseUrl":     baseURL,
		"fingerprint": fingerprint,
		"frontendUrl": frontendURL,
		"inviteId":    inviteID,
		"secret":      secret,
		"serverId":    serverID,
	})
}

// buildFederationConnectPayload returns the bytes the responding server
// signs to bind its identity to an invite. The secret travels separately.
func buildFederationConnectPayload(inviteID, serverID, baseURL, frontendURL, fingerprint string) []byte {
	return canonicalJSON(signedFields{
		"baseUrl":     baseURL,
		"fingerprint": fingerprint,
		"frontendUrl": frontendURL,
		"inviteId":    inviteID,
		"serverId":    serverID,
	})
}

// buildRippleUserPayload returns the bytes a ripple's author signs. reedID is
// the parent reed's canonical id; replyingTo is empty for a top-level post.
func buildRippleUserPayload(reedID, rippleAuthorID, keyID, threadID, replyingTo, content string) []byte {
	return canonicalJSON(signedFields{
		"reedID":         reedID,
		"rippleAuthorID": rippleAuthorID,
		"keyID":          keyID,
		"threadID":       threadID,
		"replyingTo":     replyingTo,
		"content":        content,
	})
}

// buildRippleServerPayload returns the bytes the server countersigns for a
// ripple: the author's fields and signature plus serverID and timestamp.
// The ripple's id is the hash of these bytes, frozen at creation.
func buildRippleServerPayload(serverID, reedID, rippleAuthorID, keyID, threadID, replyingTo, userSignature string, timestamp time.Time) []byte {
	return canonicalJSON(signedFields{
		"serverID":       serverID,
		"reedID":         reedID,
		"rippleAuthorID": rippleAuthorID,
		"keyID":          keyID,
		"threadID":       threadID,
		"replyingTo":     replyingTo,
		"timestamp":      signedTime(timestamp),
		"userSignature":  userSignature,
	})
}

const identityTypeRealtimeAuth = "realtime-auth"

// buildRealtimeAuthPayload returns the bytes a client signs to open a
// WebSocket, bound to this server and user; nonce makes it single-use.
func buildRealtimeAuthPayload(serverID, userID, timestamp, nonce string) []byte {
	return canonicalJSON(signedFields{
		"type":      identityTypeRealtimeAuth,
		"serverID":  serverID,
		"userID":    userID,
		"timestamp": timestamp,
		"nonce":     nonce,
	})
}

// identityTypeAdminRequest is the `type` of a requester's signed admin
// request; the decision and the server's countersignature have their own.
const (
	identityTypeAdminRequest       = "admin-request"
	identityTypeAdminDecision      = "admin-decision"
	identityTypeAdminRequestServer = "admin-request-server"
	identityTypeAdminRequestOpened = "admin-request-opened"
)

// buildAdminRequestPayload returns the bytes an admin signs to open a
// request. targets must already be sorted.
func buildAdminRequestPayload(
	requestID,
	action,
	requesterID,
	keyID,
	reason string,
	targets []string,
	body string,
	createdAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":        identityTypeAdminRequest,
		"requestID":   requestID,
		"action":      action,
		"requesterID": requesterID,
		"keyID":       keyID,
		"reason":      reason,
		"targets":     targets,
		"body":        body,
		"createdAt":   signedTime(createdAt),
	})
}

// buildAdminDecisionPayload returns the bytes an admin signs to approve,
// reject or withdraw a request. Embedding the request's signature binds
// the decision to exactly that request.
func buildAdminDecisionPayload(requestID, decision, deciderID, keyID, requestSignature string) []byte {
	return canonicalJSON(signedFields{
		"type":             identityTypeAdminDecision,
		"requestID":        requestID,
		"decision":         decision,
		"deciderID":        deciderID,
		"keyID":            keyID,
		"requestSignature": requestSignature,
	})
}

// buildAdminRequestOpenedPayload returns the bytes the server countersigns
// when a request opens, stamping when it was proposed.
func buildAdminRequestOpenedPayload(
	requestID,
	requestSignature,
	serverID,
	serverKeyFingerprint string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeAdminRequestOpened,
		"requestID":            requestID,
		"requestSignature":     requestSignature,
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             signedTime(signedAt),
	})
}

// buildAdminRequestServerPayload returns the bytes the server countersigns
// when a request closes. decisionSignature is empty for a moot request.
func buildAdminRequestServerPayload(
	requestID,
	state,
	requestSignature,
	decisionSignature,
	serverID,
	serverKeyFingerprint string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeAdminRequestServer,
		"requestID":            requestID,
		"state":                state,
		"requestSignature":     requestSignature,
		"decisionSignature":    decisionSignature,
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             signedTime(signedAt),
	})
}

// identityTypeLedgerEntry is the `type` of a server-signed ledger entry.
const identityTypeLedgerEntry = "ledger-entry"

// buildLedgerEntryPayload returns the bytes the server signs for a ledger
// entry. prevID and prevSignature are the previous entry's, absent (0 and
// "") on the first. requesterID and requestedAt are absent off a request or
// admin invite. targets must already be sorted.
func buildLedgerEntryPayload(
	id, prevID uint64,
	prevSignature,
	kind,
	userID,
	requesterID string,
	requestedAt time.Time,
	message,
	content string,
	targets []string,
	serverID,
	serverKeyFingerprint string,
	signedAt time.Time,
) []byte {
	var prev any
	if prevID != 0 {
		prev = prevID
	}
	var requested string
	if !requestedAt.IsZero() {
		requested = signedTime(requestedAt)
	}
	return canonicalJSON(signedFields{
		"type":                 identityTypeLedgerEntry,
		"id":                   id,
		"prevID":               prev,
		"prevSignature":        prevSignature,
		"kind":                 kind,
		"userID":               userID,
		"requesterID":          requesterID,
		"requestedAt":          requested,
		"message":              message,
		"content":              content,
		"targets":              targets,
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             signedTime(signedAt),
	})
}

// identityTypeAccountKick is the `type` of a kick's server signature, so a
// self-deletion's can't stand in for one.
const identityTypeAccountKick = "account-kick"

// buildAccountKickServerPayload returns the bytes the server countersigns
// for a kick, binding the two admins' signatures.
func buildAccountKickServerPayload(
	serverID,
	userID,
	requestID,
	requestSignature,
	decisionSignature,
	serverKeyFingerprint string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeAccountKick,
		"serverID":             serverID,
		"userID":               userID,
		"requestID":            requestID,
		"requestSignature":     requestSignature,
		"decisionSignature":    decisionSignature,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             signedTime(signedAt),
	})
}

// identityTypeAdminRevocation is the `type` of a key revocation two admins
// signed for a lost device, in place of the key itself.
const (
	identityTypeAdminRevocation = "admin-revocation"
	keyRevocationReasonLost     = "lost"
)

// buildAdminRevocationServerPayload returns the bytes the server
// countersigns when a rebind revokes keyID. The reason is always "lost".
func buildAdminRevocationServerPayload(
	userID,
	keyID,
	requestID,
	requestSignature,
	decisionSignature,
	serverID,
	serverKeyFingerprint string,
	signedAt time.Time,
) []byte {
	return canonicalJSON(signedFields{
		"type":                 identityTypeAdminRevocation,
		"userID":               userID,
		"keyID":                keyID,
		"reason":               keyRevocationReasonLost,
		"requestID":            requestID,
		"requestSignature":     requestSignature,
		"decisionSignature":    decisionSignature,
		"serverID":             serverID,
		"serverKeyFingerprint": serverKeyFingerprint,
		"signedAt":             signedTime(signedAt),
	})
}

// identityTypeReedFlag is the `type` of a flag a member signs.
const identityTypeReedFlag = "reed-flag"

// buildReedFlagPayload returns the bytes a reporter signs to flag reedID,
// so its home server can check a relayed flag itself.
func buildReedFlagPayload(reedID, reporterID, keyID, note string, createdAt time.Time) []byte {
	return canonicalJSON(signedFields{
		"type":       identityTypeReedFlag,
		"reedID":     reedID,
		"reporterID": reporterID,
		"keyID":      keyID,
		"note":       note,
		"createdAt":  signedTime(createdAt),
	})
}
