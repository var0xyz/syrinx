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
// signatureAuthMiddleware; the userID query param already arrives in
// "userID@serverID" form — do not re-compose it via identity.CanonicalID.
func (as *AuthService) AuthenticateWebSocket(r *http.Request) (string, error) {
	// Extract required authentication parameters from query string
	// (WebSocket doesn't support custom headers in all browsers). userID is
	// already in "userID@serverID" form — see doc comment above. fingerprint
	// is deliberately bare (symmetric with the HTTP path's
	// X-Syrinx-Fingerprint header and URL {fingerprint} path vars — userID
	// already carries the full canonical prefix), joined below.
	userID := r.URL.Query().Get("userID")
	fingerprint := r.URL.Query().Get("fingerprint")
	signature := r.URL.Query().Get("signature")
	timestamp := r.URL.Query().Get("timestamp")

	if userID == "" || fingerprint == "" || signature == "" || timestamp == "" {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
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

	canonicalFingerprint := string(identity.AppendEntity(identity.IdentityID(userID), fingerprint))

	// Get public key for the user and fingerprint, along with its
	// revocation state.
	publicKey, revoked, err := as.getPublicKey(r.Context(), canonicalFingerprint)
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

	// userID is already in identities.id form (see doc comment above) —
	// used directly, not composed via identity.CanonicalID.
	selfIdentity := identity.IdentityID(userID)
	var removed bool
	if err := as.db.QueryRowContext(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM account_removals WHERE user_id = $1)
	`, selfIdentity).Scan(&removed); err != nil {
		return "", fmt.Errorf("error checking account removal: %w", err)
	}
	if removed {
		log.Error().Str("userID", userID).Msg("WebSocket auth rejected: account removed")
		return "", fmt.Errorf("account removed")
	}

	// Decode base64 signature first
	decodedSignature, err := encoding.Base64Decode(signature)
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
// user_key_revocations. fingerprint arrives canonical and is self-scoping —
// the sole lookup key, same shape as DataService.GetPublicKey in the main
// package.
func (as *AuthService) getPublicKey(ctx context.Context, fingerprint string) (string, bool, error) {
	var armor string
	var revoked bool
	err := as.db.QueryRowContext(ctx, `
		SELECT uk.armor,
		       EXISTS(
			SELECT 1 FROM user_key_revocations rv
			WHERE rv.user_fingerprint = uk.fingerprint
		)
		FROM user_keys uk
		WHERE uk.fingerprint = $1
	`, fingerprint).Scan(&armor, &revoked)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}

	return armor, revoked, nil
}
