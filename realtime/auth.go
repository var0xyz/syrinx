package realtime

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"syrinx/crypto"
	"syrinx/encoding"
	"syrinx/identity"

	"github.com/rs/zerolog/log"
)

// AuthService handles WebSocket authentication
type AuthService struct {
	db       *sql.DB
	crypto   *crypto.Service
	serverID string
}

// NewAuthService creates a new auth service. serverID is this server's own
// id, needed to build the identities.id form ("userID@serverID") for every
// FK'd query below.
func NewAuthService(db *sql.DB, crypto *crypto.Service, serverID string) *AuthService {
	return &AuthService{
		db:       db,
		crypto:   crypto,
		serverID: serverID,
	}
}

// AuthenticateWebSocket authenticates a WebSocket connection using PGP
// signature. Parallel implementation of the HTTP path's
// signatureAuthMiddleware: publicKeyId is the canonical id of the key that
// signed the request (userID@serverID/fingerprint); userID is recovered
// from the verified key itself (identity.ParseKeyFingerprint), never
// trusted from a client-supplied query param.
func (as *AuthService) AuthenticateWebSocket(r *http.Request) (string, error) {
	// Extract required authentication parameters from query string
	// (WebSocket doesn't support custom headers in all browsers).
	publicKeyID := r.URL.Query().Get("publicKeyId")
	signature := r.URL.Query().Get("signature")
	timestamp := r.URL.Query().Get("timestamp")

	if publicKeyID == "" || signature == "" || timestamp == "" {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Bool("hasSignature", signature != "").
			Str("timestamp", timestamp).
			Msg("Missing authentication parameters")
		return "", fmt.Errorf("missing authentication parameters")
	}

	// Validate timestamp for replay protection
	if err := as.crypto.ValidateTimestamp(timestamp); err != nil {
		log.Error().
			Str("timestamp", timestamp).
			Err(err).
			Msg("Invalid timestamp")
		return "", fmt.Errorf("invalid timestamp: %w", err)
	}

	// Get public key for the fingerprint, along with its revocation state.
	publicKey, revoked, err := as.getPublicKey(r.Context(), publicKeyID)
	if err != nil {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Err(err).
			Msg("Error retrieving public key")
		return "", fmt.Errorf("error retrieving public key: %w", err)
	}

	if publicKey == "" {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Msg("Public key not found")
		return "", fmt.Errorf("public key not found")
	}

	// Reject websocket auth signed by a revoked key. Same threat model as
	// the HTTP signatureAuthMiddleware: an attacker holding a compromised
	// old key must not be able to open a live subscription (and thereby
	// receive fanout traffic, or later, submit signed messages) after the
	// legitimate owner has rotated and revoked. A subscription opened
	// before revocation stays open; producing a *new* auth handshake with
	// a revoked key is what we forbid.
	if revoked {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Msg("WebSocket auth rejected: key is revoked")
		return "", fmt.Errorf("key is revoked")
	}

	// WebSocket sessions are always per-end-user (no peer-server use case
	// today, unlike the HTTP proxy path) — a 2-part server-key id has no
	// userID to recover and is rejected here.
	userID, _, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(publicKeyID))
	if !ok {
		log.Error().Str("publicKeyId", publicKeyID).Msg("WebSocket auth rejected: not a user key")
		return "", fmt.Errorf("not a user key")
	}
	selfIdentity := identity.CanonicalID(as.serverID, userID)
	var removed bool
	if err := as.db.QueryRowContext(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM account_removals WHERE user_id = $1)
	`, selfIdentity).Scan(&removed); err != nil {
		return "", fmt.Errorf("error checking account removal: %w", err)
	}
	if removed {
		log.Error().Str("userID", string(selfIdentity)).Msg("WebSocket auth rejected: account removed")
		return "", fmt.Errorf("account removed")
	}

	// Decode base64 signature first
	decodedSignature, err := encoding.Base64Decode(signature)
	if err != nil {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Err(err).
			Msg("Failed to decode base64 signature")
		return "", fmt.Errorf("failed to decode base64 signature: %w", err)
	}

	// Verify signature against timestamp directly
	if err := as.crypto.VerifySignature(timestamp, decodedSignature, publicKey); err != nil {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Str("timestamp", timestamp).
			Err(err).
			Msg("Signature verification failed")
		return "", fmt.Errorf("signature verification failed: %w", err)
	}

	log.Info().
		Str("userID", string(selfIdentity)).
		Str("publicKeyId", publicKeyID).
		Msg("WebSocket authentication successful")

	return string(selfIdentity), nil
}

// getPublicKey retrieves a public key from the database along with its
// revocation state. A key is revoked iff a matching row exists in
// public_key_revocations. fingerprint arrives canonical and is self-scoping —
// the sole lookup key, same shape as DataService.GetPublicKey in the main
// package.
func (as *AuthService) getPublicKey(ctx context.Context, fingerprint string) (string, bool, error) {
	var armor string
	var revoked bool
	err := as.db.QueryRowContext(ctx, `
		SELECT pk.armor,
		       EXISTS(
			SELECT 1 FROM public_key_revocations rv
			WHERE rv.revoked_id = pk.id
		)
		FROM public_keys pk
		WHERE pk.id = $1
	`, fingerprint).Scan(&armor, &revoked)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}

	return armor, revoked, nil
}
