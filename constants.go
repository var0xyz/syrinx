package main

// contextKey is a type for context keys to avoid collisions
type contextKey string

const requestIDKey contextKey = "requestID"
const userIDKey contextKey = "userID"

// peerServerIDKey holds the calling server's id for peer-authenticated
// federation runtime requests (specs/federation/04) — set by
// peerAuthMiddleware, distinct from userIDKey (no local user session).
const peerServerIDKey contextKey = "peerServerID"
