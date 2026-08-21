package identity

import "strings"

// IdentityID is the server-qualified form of "a user": always
// "{userID}@{serverID}", the FK value stored in every table that
// references a user. Construct it via CanonicalID/ParseIdentityID.
type IdentityID string

// idSeparator joins userID and serverID inside an IdentityID. crypto.Alphabet
// (the character set for server/user/invite IDs) never contains "@", so
// splitting is unambiguous.
const idSeparator = "@"

// CanonicalID builds the identity id for a user on serverID — serverID
// first, userID second — and formats it as the wire/DB form
// "{userID}@{serverID}". Used both for "this server" (pass the caller's
// own DataService.GetServerID()) and for remote/federated identities.
//
// entityID is optional (variadic so existing two-arg call sites don't
// break; pass zero or exactly one value — more than one panics). When
// given and non-empty, it's appended as "/{entityID}", producing a ref to
// something the user owns ("{userID}@{serverID}/{entityID}") rather than
// a bare user identity — e.g. a user key fingerprint. Reed refs use their
// own FormatReedRef, not this path.
func CanonicalID(serverID, userID string, entityID ...string) IdentityID {
	if len(entityID) > 1 {
		panic("identity.CanonicalID: at most one entityID may be passed")
	}
	id := userID + idSeparator + serverID
	if len(entityID) == 1 && entityID[0] != "" {
		id += "/" + entityID[0]
	}
	return IdentityID(id)
}

// ParseIdentityID splits an id back into its bare userID and serverID
// parts — needed wherever the bare userID must be recovered, most
// importantly this package's own wire payload builders. It only
// understands the 2-part "{userID}@{serverID}" form; use
// ParseKeyFingerprint for the 3-part "{userID}@{serverID}/{fingerprint}"
// form.
func ParseIdentityID(id IdentityID) (userID, serverID string, ok bool) {
	s := string(id)
	i := strings.LastIndex(s, idSeparator)
	if i < 0 || i == 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// ParseKeyFingerprint splits a canonical key id
// "{userID}@{serverID}/{fingerprint}" into its three parts. Fingerprints
// never contain "/", so splitting on the last "/" is unambiguous.
func ParseKeyFingerprint(id IdentityID) (userID, serverID, fingerprint string, ok bool) {
	s := string(id)
	i := strings.LastIndex(s, "/")
	if i < 0 || i == len(s)-1 {
		return "", "", "", false
	}
	userID, serverID, ok = ParseIdentityID(IdentityID(s[:i]))
	if !ok {
		return "", "", "", false
	}
	return userID, serverID, s[i+1:], true
}

// AppendEntity returns userIdentity + "/" + entityID, for building a
// canonical key/reed ref from an already-canonical user identity without
// a parse-then-reassemble round trip.
func AppendEntity(userIdentity IdentityID, entityID string) IdentityID {
	return IdentityID(string(userIdentity) + "/" + entityID)
}

// UserID returns the bare userID half of id, discarding serverID. Panics
// on a malformed id — a panic here indicates a programming error, not bad input.
func (id IdentityID) UserID() string {
	userID, _, ok := ParseIdentityID(id)
	if !ok {
		panic("identity: malformed IdentityID: " + string(id))
	}
	return userID
}

// ServerID returns the serverID half of id. Panics under the same
// condition as UserID.
func (id IdentityID) ServerID() string {
	_, serverID, ok := ParseIdentityID(id)
	if !ok {
		panic("identity: malformed IdentityID: " + string(id))
	}
	return serverID
}

// String satisfies fmt.Stringer so IdentityID prints and %s-formats as its
// wire/DB form instead of a Go-quoted type name.
func (id IdentityID) String() string {
	return string(id)
}
