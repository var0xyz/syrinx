package identity

import "strings"

// IdentityID is the server-qualified form of "a user": always
// "{userID}@{serverID}", the FK value stored in every table that
// references a user. Construct it via LocalID/RemoteID/ParseIdentityID.
type IdentityID string

// idSeparator joins userID and serverID inside an IdentityID. crypto.Alphabet
// (the character set for server/user/invite IDs) never contains "@", so
// splitting is unambiguous.
const idSeparator = "@"

// LocalID builds the identity id for a user on serverID (pass the caller's
// own DataService.GetServerID() for "this server").
func LocalID(userID, serverID string) IdentityID {
	return IdentityID(userID + idSeparator + serverID)
}

// RemoteID is an alias of LocalID for readability at remote-identity call
// sites (recovery peer report, federation).
func RemoteID(userID, serverID string) IdentityID {
	return LocalID(userID, serverID)
}

// ParseIdentityID splits an id back into its bare userID and serverID
// parts — needed wherever the bare userID must be recovered, most
// importantly this package's own wire payload builders.
func ParseIdentityID(id IdentityID) (userID, serverID string, ok bool) {
	s := string(id)
	i := strings.LastIndex(s, idSeparator)
	if i < 0 || i == 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
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
