package main

// contextKey is a type for context keys to avoid collisions
type contextKey string

const requestIDKey contextKey = "requestID"
const userIDKey contextKey = "userID"

// peerServerIDKey holds the calling server's id for peer-authenticated
// federation runtime requests (specs/federation/04) — set by
// signatureAuthMiddleware's authenticateAsPeer branch, distinct from
// userIDKey (no local user session).
const peerServerIDKey contextKey = "peerServerID"

// rootUserID is the reserved bare userID for the operator root account.
// This stays a bare compile-time constant since the full identity
// ("1@serverID") can't be — serverID is only known at runtime. Lives in
// this untagged file (not roles.go) because mailbox.go, which has no
// build tag, needs it across all three binary variants.
const rootUserID = "1"
