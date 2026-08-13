//go:build !ops && !ripplescleanup

package main

import (
	"regexp"
	"strings"
)

// mentionTokenPattern matches the locked mention wire form:
// ~<userID>@<serverID>. userID/serverID are alphanumeric runs of ANY
// length ≥ 1 — IDs are NOT fixed-width (e.g. the root user's id is "1";
// foreign servers may mint IDs of any length). Server/user IDs containing
// punctuation are never mentionable by design — the alphanumeric class IS
// the boundary, matched greedily, same idea as a #hashtag's \S+ run. No
// closing delimiter, so `~` can still be strikethrough everywhere else
// (see the SPA parser reedMarkdown.ts readMention for the matching rule).
var mentionTokenPattern = regexp.MustCompile(`~([a-zA-Z0-9]+)@([a-zA-Z0-9]+)`)

// ExtractMentions parses well-formed ~<userID>@<serverID> mention tokens out
// of reed content. Deduplicates by (ServerID, AuthorID) per reed and drops
// self-mentions (authorID == the reed's own author). Never returns a
// mention whose fields are empty.
func ExtractMentions(content, authorID string) []ReedRef {
	if content == "" {
		return nil
	}
	matches := mentionTokenPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[ReedRef]struct{}, len(matches))
	out := make([]ReedRef, 0, len(matches))
	for _, m := range matches {
		userID := strings.TrimSpace(m[1])
		serverID := strings.TrimSpace(m[2])
		if serverID == "" || userID == "" {
			continue
		}
		if userID == authorID {
			continue
		}
		ref := ReedRef{ServerID: serverID, AuthorID: userID}
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
