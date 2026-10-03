package main

import (
	"fmt"
	"time"
)

// contextKey is a type for context keys to avoid collisions
type contextKey string

const requestIDKey contextKey = "requestID"
const userIDKey contextKey = "userID"

// peerServerIDKey holds the calling server's id for peer-authenticated
// federation runtime requests — set by
// signatureAuthMiddleware's authenticateAsPeer branch, distinct from
// userIDKey (no local user session).
const peerServerIDKey contextKey = "peerServerID"

// rootUserID is the reserved bare userID for the operator root account.
// This stays a bare compile-time constant since the full identity
// ("1@serverID") can't be — serverID is only known at runtime. Lives in
// this untagged file (not roles.go) because mailbox.go, which has no
// build tag, needs it across all three binary variants.
const rootUserID = "1"

// rippleTTL is how long a reed's ripple thread lives after its last post.
// Shared by the server and the ripples-cleanup binary.
const rippleTTL = 30 * 24 * time.Hour

// rippleCutoffSQL: threads last active at or before this instant are gone,
// even if the cleanup job hasn't deleted them yet.
var rippleCutoffSQL = fmt.Sprintf("NOW() - make_interval(secs => %d)", int64(rippleTTL/time.Second))
