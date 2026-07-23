package realtime

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"

	"syrinx/crypto"

	"github.com/rs/zerolog/log"
)

// AuthService handles WebSocket authentication
type AuthService struct {
	db     *sql.DB
	crypto *crypto.Service
}

// NewAuthService creates a new auth service
func NewAuthService(db *sql.DB, crypto *crypto.Service) *AuthService {
	return &AuthService{
		db:     db,
		crypto: crypto,
	}
}

// AuthenticateWebSocket authenticates a WebSocket connection using PGP signature
func (as *AuthService) AuthenticateWebSocket(r *http.Request) (string, error) {
	// Extract required authentication parameters from query string
	// (WebSocket doesn't support custom headers in all browsers)
	userID := r.URL.Query().Get("userID")
	fingerprint := r.URL.Query().Get("fingerprint")
	signature := r.URL.Query().Get("signature")
	algorithm := r.URL.Query().Get("algorithm")
	timestamp := r.URL.Query().Get("timestamp")

	if userID == "" || fingerprint == "" || signature == "" || algorithm == "" || timestamp == "" {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Bool("hasSignature", signature != "").
			Str("algorithm", algorithm).
			Str("timestamp", timestamp).
			Msg("Missing authentication parameters")
		return "", fmt.Errorf("missing authentication parameters")
	}

	// Validate algorithm
	if algorithm != "PGP+base64" {
		log.Error().
			Str("algorithm", algorithm).
			Msg("Unsupported signature algorithm")
		return "", fmt.Errorf("unsupported signature algorithm: %s", algorithm)
	}

	// Validate timestamp for replay protection
	if err := as.crypto.ValidateTimestamp(timestamp); err != nil {
		log.Error().
			Str("timestamp", timestamp).
			Err(err).
			Msg("Invalid timestamp")
		return "", fmt.Errorf("invalid timestamp: %w", err)
	}

	// Get public key for the user and fingerprint, along with its
	// revocation state.
	publicKey, revoked, err := as.getPublicKey(userID, fingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Err(err).
			Msg("Error retrieving public key")
		return "", fmt.Errorf("error retrieving public key: %w", err)
	}

	if publicKey == "" {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
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
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Msg("WebSocket auth rejected: key is revoked")
		return "", fmt.Errorf("key is revoked")
	}

	var removed bool
	if err := as.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM account_removals WHERE user_id = $1)
	`, userID).Scan(&removed); err != nil {
		return "", fmt.Errorf("error checking account removal: %w", err)
	}
	if removed {
		log.Error().Str("userID", userID).Msg("WebSocket auth rejected: account removed")
		return "", fmt.Errorf("account removed")
	}

	// Decode base64 signature first
	decodedSignature, err := as.decodeBase64Signature(signature)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Err(err).
			Msg("Failed to decode base64 signature")
		return "", fmt.Errorf("failed to decode base64 signature: %w", err)
	}

	// Verify signature against timestamp directly
	if err := as.crypto.VerifySignature(timestamp, decodedSignature, publicKey); err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Str("timestamp", timestamp).
			Err(err).
			Msg("Signature verification failed")
		return "", fmt.Errorf("signature verification failed: %w", err)
	}

	log.Info().
		Str("userID", userID).
		Str("fingerprint", fingerprint).
		Msg("WebSocket authentication successful")

	return userID, nil
}

// getPublicKey retrieves a public key from the database along with its
// revocation state. A key is revoked iff a matching row exists in
// user_key_revocations.
func (as *AuthService) getPublicKey(userID, fingerprint string) (string, bool, error) {
	var armor string
	var revoked bool
	err := as.db.QueryRow(`
		SELECT uk.armor,
		       EXISTS(
			SELECT 1 FROM user_key_revocations rv
			WHERE rv.user_fingerprint = uk.fingerprint AND rv.owner = uk.owner
		)
		FROM user_keys uk
		WHERE uk.owner = $1 AND uk.fingerprint = $2
	`, userID, fingerprint).Scan(&armor, &revoked)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}

	return armor, revoked, nil
}

// decodeBase64Signature decodes a base64-encoded signature
func (as *AuthService) decodeBase64Signature(encodedSignature string) (string, error) {
	// Decode base64
	decoded, err := base64.StdEncoding.DecodeString(encodedSignature)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}
	return string(decoded), nil
}
