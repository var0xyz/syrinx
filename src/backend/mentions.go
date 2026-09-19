//go:build !ops && !ripplescleanup

package main

import "strings"

// ValidateMentionClaims format-validates a client-claimed userID@serverID
// mention list. authorID is canonical (userID@serverID). It can't confirm
// a claim really appears in content the server never sees — that's the
// mentioned client's job on decrypt.
func ValidateMentionClaims(claims []string, authorID string) []ReedRef {
	if len(claims) == 0 {
		return nil
	}
	seen := make(map[ReedRef]struct{}, len(claims))
	out := make([]ReedRef, 0, len(claims))
	for _, claim := range claims {
		userID, serverID, ok := strings.Cut(strings.TrimSpace(claim), "@")
		if !ok || userID == "" || serverID == "" {
			continue
		}
		ref := ReedRef{ServerID: serverID, AuthorID: userID}
		if ref.CanonicalAuthorID() == authorID {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
