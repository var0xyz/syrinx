package main

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

// errMissingDevice is returned when a device id header or field is empty or not a UUID.
var errMissingDevice = errors.New("missing or invalid device id")

// parseDeviceID validates and canonicalises a client device id (UUID string).
func parseDeviceID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errMissingDevice
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", errMissingDevice
	}
	return parsed.String(), nil
}

// identityID is the server-qualified form of "a user": always
// "{userID}@{serverID}", the FK value stored in every table that
// references a user. Construct it via canonicalID/parseIdentityID.
type identityID string

// idSeparator joins userID and serverID inside an identityID. idAlphabet
// (the character set for server/user/invite IDs) never contains "@", so
// splitting is unambiguous.
const idSeparator = "@"

// canonicalID builds the identity id for a user on serverID — serverID
// first, userID second — and formats it as the wire/DB form
// "{userID}@{serverID}". Used both for "this server" (pass the caller's
// own DataService.GetServerID()) and for remote/federated identities.
//
// entityID is optional (variadic so existing two-arg call sites don't
// break; pass zero or exactly one value — more than one panics). When
// given and non-empty, it's appended as "/{entityID}", producing a ref to
// something the user owns ("{userID}@{serverID}/{entityID}") rather than
// a bare user identity — e.g. a user key fingerprint or a reed id.
func canonicalID(serverID, userID string, entityID ...string) identityID {
	if len(entityID) > 1 {
		panic("canonicalID: at most one entityID may be passed")
	}
	id := userID + idSeparator + serverID
	if len(entityID) == 1 && entityID[0] != "" {
		id += "/" + entityID[0]
	}
	return identityID(id)
}

// parseIdentityID splits an id back into its bare userID and serverID
// parts — needed wherever the bare userID must be recovered, most
// importantly this file's own wire payload builders. It only
// understands the 2-part "{userID}@{serverID}" form; use
// parseKeyFingerprint for the 3-part "{userID}@{serverID}/{fingerprint}"
// form.
func parseIdentityID(id identityID) (userID, serverID string, ok bool) {
	s := string(id)
	i := strings.LastIndex(s, idSeparator)
	if i < 0 || i == 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// parseKeyFingerprint splits a canonical key id
// "{userID}@{serverID}/{fingerprint}" into its three parts. Fingerprints
// never contain "/", so splitting on the last "/" is unambiguous.
func parseKeyFingerprint(id identityID) (userID, serverID, fingerprint string, ok bool) {
	s := string(id)
	i := strings.LastIndex(s, "/")
	if i < 0 || i == len(s)-1 {
		return "", "", "", false
	}
	userID, serverID, ok = parseIdentityID(identityID(s[:i]))
	if !ok {
		return "", "", "", false
	}
	return userID, serverID, s[i+1:], true
}

// appendEntity returns userIdentity + "/" + entityID, for building a
// canonical key/reed ref from an already-canonical user identity without
// a parse-then-reassemble round trip.
func appendEntity(userIdentity identityID, entityID string) identityID {
	return identityID(string(userIdentity) + "/" + entityID)
}

// authorOf strips the trailing "/{entityID}" from a 3-part canonical id
// (a key fingerprint or reed id), returning the 2-part canonical user
// identity that owns it. Unlike parseKeyFingerprint, it never discards
// serverID — the caller can't accidentally end up with a bare userID.
func authorOf(id identityID) (identityID, bool) {
	userID, serverID, _, ok := parseKeyFingerprint(id)
	if !ok {
		return "", false
	}
	return canonicalID(serverID, userID), true
}

// UserID returns the bare userID half of id, discarding serverID. Panics
// on a malformed id — a panic here indicates a programming error, not bad input.
func (id identityID) UserID() string {
	userID, _, ok := parseIdentityID(id)
	if !ok {
		panic("identity: malformed identityID: " + string(id))
	}
	return userID
}

// ServerID returns the serverID half of id. Panics under the same
// condition as UserID.
func (id identityID) ServerID() string {
	_, serverID, ok := parseIdentityID(id)
	if !ok {
		panic("identity: malformed identityID: " + string(id))
	}
	return serverID
}

// String satisfies fmt.Stringer so identityID prints and %s-formats as its
// wire/DB form instead of a Go-quoted type name.
func (id identityID) String() string {
	return string(id)
}
