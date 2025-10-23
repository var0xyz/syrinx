package main

// contextKey is a type for context keys to avoid collisions
type contextKey string

const requestIDKey contextKey = "requestID"
const userIDKey contextKey = "userID"
