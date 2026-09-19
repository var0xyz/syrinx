//go:build !ops && !ripplescleanup

package main

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Sentinel errors returned by DataService.AddPublicKey. The handler maps
// these to 4xx responses; anything else is treated as a 500.
var (
	ErrUserNotFound               = errors.New("user not found")
	ErrUsernameTaken              = errors.New("username already taken")
	ErrKeyAlreadyExists           = errors.New("public key fingerprint already registered")
	ErrPredecessorRequired        = errors.New("predecessor fingerprint is required")
	ErrPredecessorNotFound        = errors.New("predecessor key not found for this user")
	ErrPredecessorNotRevoked      = errors.New("predecessor key is not revoked")
	ErrPredecessorAlreadyReplaced = errors.New("predecessor key already has a successor")
	ErrActiveKeyExists            = errors.New("user already has an active key")
	// ErrReedFork is returned by CreateReed/CreateReedWithEcho/
	// CreateReedWithReply when the client's PreviousID does not match the
	// author's current tip (see specs/recovery/16_reed_tip_check.md). The
	// handler maps this to 409 so the client can refresh its tip and retry.
	ErrReedFork = errors.New("reed fork: previousID does not match current tip")
)

func isUsernameUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Code != "23505" {
		return false
	}
	switch pqErr.Constraint {
	case "users_username_key", "idx_lower_users_username":
		return true
	}
	return false
}

func isReedUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Code != "23505" {
		return false
	}
	switch pqErr.Constraint {
	case "reeds_pkey", "reeds_id_key":
		return true
	}
	return false
}

type Services struct {
	db     *DataService
	crypto *cryptoService
	log    *LoggingService
}

// =============== //
//   UserService   //
// =============== //

type DataService struct {
	db         *sql.DB
	serverName string
	serverID   string
}

func NewDataService(db *sql.DB, serverName string) *DataService {
	return &DataService{
		db:         db,
		serverName: serverName,
	}
}

// setServerIDForTest sets serverID; tests must use this instead of writing
// s.serverID directly (kept as its own helper for parity with earlier
// callers, even though it's now a one-line assignment).
func (s *DataService) setServerIDForTest(id string) {
	s.serverID = id
}

func (s *DataService) GetServerID() string {
	return s.serverID
}

// SendMailboxMessage delegates to the package-level SendMailboxMessage in
// db.go (a free function, not a DataService method, because ops.go's
// `ops` build tag excludes this file — this wrapper exists purely for
// ergonomic use from handlers.go via h.services.db).
func (s *DataService) SendMailboxMessage(ctx context.Context, cryptoSvc *cryptoService, userID string, category MailboxCategory, kind, message, link, senderUserID string, meta any) (id, ciphertext string, err error) {
	return SendMailboxMessage(ctx, s.db, cryptoSvc, userID, category, kind, message, link, senderUserID, meta)
}

// UserServerSignedAt returns the identity countersignature time for userID.
// Returns sql.ErrNoRows when the user does not exist.
func (s *DataService) UserServerSignedAt(ctx context.Context, userID string) (time.Time, error) {
	selfIdentity := userID
	var ts time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT ss.signed_at
		FROM users u
		JOIN server_signatures ss ON ss.id = u.server_signature_id
		WHERE u.id = $1
	`, selfIdentity).Scan(&ts)
	if err != nil {
		return time.Time{}, err
	}
	return ts.UTC().Truncate(time.Second), nil
}

// IsUnclaimed reports whether userID is still in the peer-seeded gauge.
func (s *DataService) IsUnclaimed(ctx context.Context, userID string) (bool, error) {
	selfIdentity := userID
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM unclaimed_accounts WHERE user_id = $1)
	`, selfIdentity).Scan(&exists)
	return exists, err
}

// IsOngoing reports whether userID is mid-recovery import.
func (s *DataService) IsOngoing(ctx context.Context, userID string) (bool, error) {
	selfIdentity := userID
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM ongoing_recoveries WHERE user_id = $1)
	`, selfIdentity).Scan(&exists)
	return exists, err
}

// InsertUnclaimed records a peer-seeded account awaiting owner claim.
func (s *DataService) InsertUnclaimed(ctx context.Context, serverID, userID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO unclaimed_accounts (user_id)
		VALUES ($1)
		ON CONFLICT DO NOTHING
	`, canonicalID(serverID, userID))
	return err
}

// DeleteUnclaimed removes a user from the unclaimed gauge (e.g. after own claim).
func (s *DataService) DeleteUnclaimed(ctx context.Context, serverID, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM unclaimed_accounts WHERE user_id = $1`, canonicalID(serverID, userID))
	return err
}

// InsertOngoing marks a claimant as mid-import (import gate).
func (s *DataService) InsertOngoing(ctx context.Context, serverID, userID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ongoing_recoveries (user_id)
		VALUES ($1)
		ON CONFLICT DO NOTHING
	`, canonicalID(serverID, userID))
	return err
}

// DeleteOngoing clears the import gate for a user (e.g. after /complete).
func (s *DataService) DeleteOngoing(ctx context.Context, serverID, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ongoing_recoveries WHERE user_id = $1`, canonicalID(serverID, userID))
	return err
}

// CountUnclaimed returns how many peer-seeded accounts still await claim.
// No user filter needed — this is a bare row count.
func (s *DataService) CountUnclaimed(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM unclaimed_accounts`).Scan(&n)
	return n, err
}

func generateServerID() (string, error) {
	return newCryptoID()
}

func generateUserID() (string, error) {
	return newCryptoID()
}

func (s *DataService) InitServer(ctx context.Context, recoveryMode bool, baseURL string) error {
	var id, name string
	var dbBaseURL sql.NullString

	err := s.db.QueryRowContext(ctx, `SELECT id, name, base_url FROM servers WHERE self = TRUE`).Scan(&id, &name, &dbBaseURL)
	if err == sql.ErrNoRows {
		if recoveryMode {
			return errRecoveryNoIdentityFound
		}
		id, err = generateServerID()
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO servers (id, name, self, base_url) VALUES ($1, $2, TRUE, $3)`, id, s.serverName, baseURL)
		if err != nil {
			return err
		}
		s.serverID = id
		return nil
	}
	if err != nil {
		return err
	}
	s.serverID = id
	if name != s.serverName || dbBaseURL.String != baseURL {
		_, err = s.db.ExecContext(ctx, `UPDATE servers SET name = $1, base_url = $2 WHERE self = TRUE`, s.serverName, baseURL)
		return err
	}

	return nil
}

// ProcessRevocations scans the {cwd}/revocations directory for .rvk files.
// Each file revokes the named key. InitServerKey will create a new one if needed.
// Called at startup before InitServerKey.
func (s *DataService) ProcessRevocations(ctx context.Context) error {
	revocationsDir := filepath.Join(".", "revocations")

	entries, err := os.ReadDir(revocationsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read revocations directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".rvk" {
			continue
		}

		fingerprint := strings.TrimSuffix(entry.Name(), ".rvk")
		keyID := string(canonicalID(s.serverID, fingerprint))
		rvkPath := filepath.Join(revocationsDir, entry.Name())

		reasonBytes, err := os.ReadFile(rvkPath)
		if err != nil {
			return fmt.Errorf("failed to read revocation file %s: %w", entry.Name(), err)
		}

		reason := strings.TrimSpace(string(reasonBytes))
		if reason == "" {
			log.Panic().
				Str("file", entry.Name()).
				Msg("Revocation file is empty — revoke reason must not be empty")
		}

		// Verify the key exists
		var exists bool
		err = s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM private_keys WHERE id = $1)`,
			keyID,
		).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check key existence: %w", err)
		}
		if !exists {
			log.Panic().
				Str("fingerprint", fingerprint).
				Msg("Revocation file references unknown key fingerprint")
		}

		if err := s.RevokeServerPrivateKey(ctx, keyID, reason); err != nil {
			return fmt.Errorf("failed to revoke key: %w", err)
		}

		if err := os.Remove(rvkPath); err != nil {
			log.Warn().Str("file", rvkPath).Err(err).Msg("Failed to delete .rvk file after processing")
		}

		log.Info().
			Str("fingerprint", fingerprint).
			Str("reason", reason).
			Msg("Key revoked")
	}

	return nil
}

// InitServerKey ensures an active (non-revoked) server signing key exists.
// If the current signing key is revoked or missing, a new one is created.
// Returns the decrypted Key (armor + fingerprint) for use by the signing middleware.
func (s *DataService) InitServerKey(ctx context.Context, cryptoSvc *cryptoService, passphrase string) (*ServerSigningKey, error) {
	var fingerprint string
	var encryptedArmor string
	var createdAt time.Time

	var keyID string
	err := s.db.QueryRowContext(ctx, `
		SELECT pk.id, pk.armor, pk.created_at
		FROM servers sv
		JOIN public_keys pub ON pub.id = sv.signing_key
		JOIN private_keys pk ON pk.id = pub.id
		WHERE sv.self = TRUE AND pk.revoked_at IS NULL
	`).Scan(&keyID, &encryptedArmor, &createdAt)
	if err == nil {
		if fp, _, ok := parseIdentityID(identityID(keyID)); ok {
			fingerprint = fp
		}
	}

	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to query server signing key: %w", err)
	}

	if err == sql.ErrNoRows {
		// No active signing key — generate one
		keyPair, err := cryptoSvc.createKeyPair(s.serverID, "", "")
		if err != nil {
			return nil, fmt.Errorf("failed to create server key pair: %w", err)
		}

		encryptedPrivate, err := cryptoSvc.encryptPrivateKey(keyPair.PrivateKey, passphrase)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt server private key: %w", err)
		}

		now := time.Now().UTC().Truncate(time.Second)

		// The server's own public half becomes a normal public_keys row —
		// every row in that table carries a countersignature, so the
		// server countersigns its own key with itself. Same payload shape
		// as any user key's countersignature (buildPublicKeyPayload).
		// This key has no owner (it's the trust anchor itself, distinct
		// from the root USER account, which gets its own separate key via
		// normal signup) — pass its own id as "userID" too, since the
		// header just needs to bind SOME identity consistently between
		// what's signed and what's later verified; there is no owner
		// identity to bind instead.
		keyID := string(canonicalID(s.serverID, keyPair.Fingerprint))
		selfPayload := buildPublicKeyPayload(
			s.serverID, keyID, keyID, keyPair.Fingerprint,
			keyPair.PublicKey, now,
		)
		selfSigArmor, err := cryptoSvc.sign(string(selfPayload), keyPair.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to self-countersign server public key: %w", err)
		}

		if err := s.SaveServerKeyPair(ctx, keyID, encryptedPrivate, keyPair.PublicKey, selfSigArmor, now); err != nil {
			return nil, fmt.Errorf("failed to save server key pair: %w", err)
		}

		if err := s.SetServerSigningKey(ctx, keyID); err != nil {
			return nil, fmt.Errorf("failed to set signing key: %w", err)
		}

		log.Info().
			Str("fingerprint", keyPair.Fingerprint).
			Msg("Generated new server signing key")

		return &ServerSigningKey{Fingerprint: keyPair.Fingerprint, Armor: keyPair.PrivateKey, CreatedAt: time.Now()}, nil
	}

	// Active key found — decrypt it
	decryptedArmor, err := cryptoSvc.decryptPrivateKey(encryptedArmor, passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt server signing key (wrong passphrase?): %w", err)
	}

	// If the server name changed, add a new identity to the key
	updatedArmor, err := cryptoSvc.addIdentity(decryptedArmor, s.serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to add identity to server signing key: %w", err)
	}
	if updatedArmor != decryptedArmor {
		newEncrypted, err := cryptoSvc.encryptPrivateKey(updatedArmor, passphrase)
		if err != nil {
			return nil, fmt.Errorf("failed to re-encrypt server signing key after identity update: %w", err)
		}
		if _, err = s.db.ExecContext(ctx,
			`UPDATE private_keys SET armor = $1 WHERE id = $2`,
			newEncrypted, keyID,
		); err != nil {
			return nil, fmt.Errorf("failed to persist updated server signing key: %w", err)
		}
		log.Info().
			Str("name", s.serverName).
			Str("fingerprint", fingerprint).
			Msg("Added new identity to server signing key")
		decryptedArmor = updatedArmor
	}

	log.Info().
		Str("fingerprint", fingerprint).
		Msg("Loaded existing server signing key")

	return &ServerSigningKey{Fingerprint: fingerprint, Armor: decryptedArmor, CreatedAt: createdAt}, nil
}

// SaveServerKeyPair persists a freshly generated server signing key: the
// encrypted private half into private_keys (unaffected by the public_keys
// unification), and the public half into the unified public_keys table as
// an ownerless row, countersigned by itself (selfSigArmor, produced by the
// caller — InitServerKey — since only it holds the decrypted private key
// needed to produce that signature).
func (s *DataService) SaveServerKeyPair(ctx context.Context, keyID, privateArmor, publicArmor, selfSigArmor string, signedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO private_keys (id, armor) VALUES ($1, $2)`,
		keyID, privateArmor,
	)
	if err != nil {
		return err
	}

	serverSignatureID, err := insertServerSignature(ctx, tx, keyID, selfSigArmor, signedAt)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO public_keys (id, armor, server_signature_id) VALUES ($1, $2, $3)`,
		keyID, publicArmor, serverSignatureID,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *DataService) SetServerSigningKey(ctx context.Context, keyID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET signing_key = $1 WHERE self = TRUE`, keyID)
	return err
}

func (s *DataService) GetServerSigningKeyArmor(ctx context.Context) (string, error) {
	var armor string
	err := s.db.QueryRowContext(ctx, `
		SELECT pk.armor
		FROM private_keys pk
		JOIN servers s ON s.signing_key = pk.id
		WHERE s.self = TRUE
	`).Scan(&armor)
	if err != nil {
		return "", err
	}
	return armor, nil
}

// GetServerPublicKeyByFingerprint returns the armored PGP public key that
// matches the given bare fingerprint of this server's OWN (local, current
// or historical) signing key, or "" if no such key exists.
//
// Verifiers use this to select the historical server signing key that
// produced a given reed countersignature (id lives on
// server_signatures.private_key_id, reachable via reeds.server_signature_id;
// the matching public_keys row, same id, is the verifier's input).
func (s *DataService) GetServerPublicKeyByFingerprint(ctx context.Context, fingerprint string) (string, error) {
	var armor string
	err := s.db.QueryRowContext(ctx,
		`SELECT armor FROM public_keys WHERE id = $1`,
		string(canonicalID(s.serverID, fingerprint)),
	).Scan(&armor)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return armor, nil
}

func (s *DataService) RevokeServerPrivateKey(ctx context.Context, keyID, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE private_keys
		SET revoked_at = NOW(), revoke_reason = $2
		WHERE id = $1
	`, keyID, reason)
	return err
}

// SignupInput bundles everything Signup needs. It is populated by the
// Signup handler after it has allocated a userID, verified the user's
// self-signature over their key, reconstructed the user identity
// payload, verified UserSignatureB64 against PublicKeyArmor, and
// produced ProfileSignature / PublicKeySignature.
type SignupInput struct {
	UserID         string
	Username       string
	PublicKeyArmor string
	// Fingerprint arrives canonical ("userID@serverID/fingerprint")
	// already — the handler builds it via appendEntity before
	// signing the identity/public-key payloads, since the same canonical
	// value must appear in the signed bytes.
	Fingerprint        string
	KeyCreatedAt       time.Time
	UserSignatureB64   string
	MemberSince        time.Time
	ProfileSignature   ServerSignature
	PublicKeySignature ServerSignature
	// Invite is the pending invite row to consume (nil when signing up without one).
	Invite   *inviteRecord
	DeviceID string
}

// GetPendingInvite resolves invite canonical id + fragment secret for
// pre-signup policy checks. Returns nil invite when unknown or hash mismatch.
func (s *DataService) GetPendingInvite(ctx context.Context, inviteID, secret string) (*inviteRecord, error) {
	inviteID = strings.TrimSpace(inviteID)
	secret = strings.TrimSpace(secret)
	if inviteID == "" || secret == "" {
		return nil, nil
	}
	return getPendingInvite(ctx, s.db, s.db, inviteID, hashSecret(secret))
}

// Signup materialises a fresh identity record: it writes the users row
// (with both signatures + the server key fingerprint stored alongside
// them) and inserts the initial user_keys row — all in one transaction.
// When an invite is consumed, MarkClaimed runs in the same TX.
//
// Callers own userID allocation and signature verification; this
// function just persists.
//
// A collision on `users.id` at ~68 bits of entropy is vanishingly
// unlikely (birthday-bound). If it ever happens, the INSERT's UNIQUE
// PRIMARY KEY violation surfaces as an error and the caller can retry
// the whole signup (which will pick a fresh random userID and re-sign
// the server payload against it). We deliberately do not pre-check
// existence — that would only widen the window between the check and
// the INSERT during which a duplicate could sneak in from another
// concurrent signup.
func (s *DataService) Signup(ctx context.Context, in SignupInput) (*User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if in.Invite != nil && in.Invite.Status() != "pending" {
		return nil, errInvalidInvite
	}

	// identities.id for the new local user, minted here inside the signup
	// transaction so it never exists half-committed.
	selfIdentity := canonicalID(s.serverID, in.UserID)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO identities (id, server_id)
		VALUES ($1, $2)
	`, selfIdentity, s.serverID); err != nil {
		return nil, err
	}

	userSignatureID, err := insertUserSignature(ctx, tx, in.Fingerprint, in.UserSignatureB64)
	if err != nil {
		return nil, err
	}
	serverSignatureID, err := insertServerSignature(ctx, tx,
		in.ProfileSignature.ID,
		in.ProfileSignature.Armor,
		in.ProfileSignature.SignedAt,
	)
	if err != nil {
		return nil, err
	}

	var inviteID any
	if in.Invite != nil {
		inviteID = in.Invite.ID
	}

	inviteGrantedRole := ""
	if in.Invite != nil {
		inviteGrantedRole = in.Invite.GrantedRole
	}
	signupRole := signupRole(in.UserID, inviteGrantedRole, in.Invite != nil, s.serverID)

	keyServerSigID, err := insertServerSignature(ctx, tx,
		in.PublicKeySignature.ID,
		in.PublicKeySignature.Armor,
		in.PublicKeySignature.SignedAt,
	)
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public_keys (
			id, owner, armor, created_at,
			server_signature_id
		) VALUES ($1, $2, $3, $4, $5)
	`, in.Fingerprint, selfIdentity, in.PublicKeyArmor, in.KeyCreatedAt,
		keyServerSigID); err != nil {
		return nil, err
	}

	// created_at is set explicitly to memberSince — the value that was
	// signed by the server. Using the DB's DEFAULT would create a
	// race between what was signed and what is persisted, and would
	// silently truncate to whatever precision Postgres chooses.
	// users.id IS identities.id now (no separate identity_id column) —
	// insert selfIdentity as the PK directly. active_key_id references
	// the public_keys row just inserted above.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (
			id, username, role, created_at, active_key_id,
			user_signature_id, server_signature_id, invite_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		selfIdentity, in.Username, signupRole, in.MemberSince, in.Fingerprint,
		userSignatureID, serverSignatureID, inviteID,
	); err != nil {
		if isUsernameUniqueViolation(err) {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}

	if in.Invite != nil {
		ok, err := s.markInviteClaimed(ctx, tx, in.Invite.ID, in.UserID, in.MemberSince)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errInvalidInvite
		}
	}

	if err := bumpActiveUsers(ctx, tx, 1); err != nil {
		return nil, err
	}

	if in.DeviceID != "" {
		if err := s.BindDeviceTx(ctx, tx, in.UserID, in.DeviceID, in.MemberSince); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// GetUserProfile requires userID in userID@serverID form; selfIdentity
	// is exactly that, so reuse it rather than passing bare in.UserID.
	return s.GetUserProfile(ctx, selfIdentity.String())
}

// GetUserProfile returns the signed identity record (no unsigned hints).
// userID arrives already in userID@serverID form (handlers.go passes the
// URL path value straight through).
func (s *DataService) GetUserProfile(ctx context.Context, userID string) (*User, error) {
	selfIdentity := userID

	var user User
	var bio sql.NullString
	var userSignatureID, serverSignatureID int64
	var inviteID, inviterID, inviterUsername sql.NullString

	// invite_id -> invites.id gives the invite; invites.created_by -> users
	// gives the inviter for display.
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.role, u.bio, u.created_at,
		       u.user_signature_id, u.server_signature_id,
		       i.id, inv.id, inv.username
		FROM users u
		LEFT JOIN invites i ON i.id = u.invite_id
		LEFT JOIN users inv ON inv.id = i.created_by
		WHERE u.id = $1
	`, selfIdentity).Scan(
		&user.ID,
		&user.Username,
		&user.Role,
		&bio,
		&user.CreatedAt,
		&userSignatureID,
		&serverSignatureID,
		&inviteID,
		&inviterID,
		&inviterUsername,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if bio.Valid {
		user.Bio = bio.String
	}
	if inviteID.Valid {
		user.Invite = &Invite{
			ID:       inviteID.String,
			UserID:   inviterID.String,
			Username: inviterUsername.String,
		}
	}

	userSig, err := getUserSignatureWire(ctx, s.db, userSignatureID)
	if err != nil {
		return nil, err
	}
	user.UserSignature = userSig

	serverSig, err := getServerSignatureWire(ctx, s.db, serverSignatureID)
	if err != nil {
		return nil, err
	}
	user.ServerSignature = serverSig

	return &user, nil
}

// GetUserInfo returns unsigned, mutable hints for a user plus the
// profile countersignature timestamp for cache invalidation. userID
// arrives already in userID@serverID form — see GetUserProfile's comment.
func (s *DataService) GetUserInfo(ctx context.Context, userID string) (*UserInfo, error) {
	selfIdentity := userID

	var info UserInfo
	var activeKeyID sql.NullString

	// reeds.user_id, user_followers.user_id, and user_following.user_id all
	// FK to identities(id), and u.id is that same form directly (identity_id
	// no longer exists as a separate column), so this is a plain u.id join.
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id,
		       u.active_key_id,
		       ss.signed_at,
		       EXISTS (
		           SELECT 1 FROM reeds r
		           WHERE r.user_id = u.id
		             AND NOT EXISTS (
		                 SELECT 1 FROM reed_removals rr WHERE rr.reed_id = r.id
		             )
		       ) AS has_reeds,
		       (SELECT COUNT(*)::int FROM user_followers uf
		           WHERE uf.user_id = u.id
		             AND NOT EXISTS (
		                 SELECT 1 FROM account_removals ar WHERE ar.user_id = uf.follower_user_id
		             )),
		       (SELECT COUNT(*)::int FROM user_following ufl
		           WHERE ufl.user_id = u.id
		             AND NOT EXISTS (
		                 SELECT 1 FROM account_removals ar WHERE ar.user_id = ufl.following_user_id
		             ))
		FROM users u
		JOIN server_signatures ss ON ss.id = u.server_signature_id
		WHERE u.id = $1
	`, selfIdentity).Scan(
		&info.ID,
		&activeKeyID,
		&info.ProfileTimestamp,
		&info.HasReeds,
		&info.FollowersCount,
		&info.FollowingCount,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if activeKeyID.Valid {
		info.ActiveKeyID = activeKeyID.String
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT pr.reed_id FROM pinned_reeds pr
		WHERE pr.user_id = $1
		  AND NOT EXISTS (SELECT 1 FROM reed_removals rm WHERE rm.reed_id = pr.reed_id)
		ORDER BY pr.pinned_at DESC, pr.reed_id DESC
		LIMIT 3
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var reedID string
		if err := rows.Scan(&reedID); err != nil {
			return nil, err
		}
		info.PinnedReedIDs = append(info.PinnedReedIDs, reedID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &info, nil
}

// GetActiveKeyFingerprint returns users.active_key_id for internal signing
// checks (update/delete/reed paths). userID arrives in userID@serverID
// form already.
func (s *DataService) GetActiveKeyFingerprint(ctx context.Context, userID string) (string, error) {
	selfIdentity := userID
	var keyID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT active_key_id FROM users WHERE id = $1`,
		selfIdentity,
	).Scan(&keyID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	if !keyID.Valid {
		return "", nil
	}
	return keyID.String, nil
}

// GetUserRole returns users.role for authorization checks. The RootUserID
// comparison itself happens in callers outside services.go (roles.go/
// handlers.go); userID arrives in userID@serverID form already.
func (s *DataService) GetUserRole(ctx context.Context, userID string) (string, error) {
	selfIdentity := userID
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM users WHERE id = $1`, selfIdentity).Scan(&role)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return role, nil
}

// UpdateUserInput carries everything needed to persist a fresh signed
// identity record produced by a profile edit. Every field is populated
// on every accepted update — this is a full replacement of the signed
// user-authored fields plus new attestation rows.
type UpdateUserInput struct {
	UserID   string
	Username string
	Bio      string
	// Fingerprint arrives canonical already — see SignupInput.Fingerprint.
	Fingerprint      string
	UserSignatureB64 string
	ProfileSignature ServerSignature
}

// UpdateUser writes a fresh signed identity record for an existing user.
// It updates username/bio alongside new signature rows and
// FKs in one transaction so a mid-write crash can never split the
// signature from the fields it covers.
//
// The caller owns signature verification and countersigning — this
// function just persists. UpdateUserInput.UserID arrives in
// userID@serverID form already.
func (s *DataService) UpdateUser(ctx context.Context, in UpdateUserInput) error {
	selfIdentity := in.UserID

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var oldUserSignatureID, oldServerSignatureID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT user_signature_id, server_signature_id FROM users WHERE id = $1
	`, selfIdentity).Scan(&oldUserSignatureID, &oldServerSignatureID); err != nil {
		return err
	}

	userSignatureID, err := insertUserSignature(ctx, tx, in.Fingerprint, in.UserSignatureB64)
	if err != nil {
		return err
	}
	serverSignatureID, err := insertServerSignature(ctx, tx,
		in.ProfileSignature.ID,
		in.ProfileSignature.Armor,
		in.ProfileSignature.SignedAt,
	)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE users
		SET username = $1,
		    bio = $2,
		    user_signature_id = $3,
		    server_signature_id = $4
		WHERE id = $5
	`,
		in.Username, in.Bio,
		userSignatureID, serverSignatureID,
		selfIdentity,
	)
	if err != nil {
		if isUsernameUniqueViolation(err) {
			return ErrUsernameTaken
		}
		return err
	}

	// The superseded profile signature no longer describes the live
	// profile — delete it now that the users row points at the new one.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM user_signatures WHERE id = $1
	`, oldUserSignatureID); err != nil {
		return fmt.Errorf("delete superseded profile user signature: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM server_signatures WHERE id = $1
	`, oldServerSignatureID); err != nil {
		return fmt.Errorf("delete superseded profile server signature: %w", err)
	}

	return tx.Commit()
}

func (s *DataService) UsernameExists(ctx context.Context, username string) (bool, error) {
	var exists bool

	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users WHERE LOWER(username) = LOWER($1)
		)
	`, username).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

// DeleteUser has no callers today (account removal goes through
// insertAccountRemovalCert/account_removals instead). Note: deleting
// only the `users` row does not cascade to `identities` (FK direction is
// identities → users), so wiring this up needs DELETE FROM identities instead.
func (s *DataService) DeleteUser(ctx context.Context, userID string) error {
	selfIdentity := canonicalID(s.serverID, userID)

	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, "DELETE FROM users WHERE id = $1", selfIdentity)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// ErrFollowTargetNotFound is returned by FollowUser when userID has no
// identities row yet (see UpsertRemoteIdentity).
var ErrFollowTargetNotFound = errors.New("follow target not found")

// FollowUser takes followerID/userID already in userID@serverID form.
// user_followers is only written when userID is local — see
// RecordRemoteFollower for the remote case.
func (s *DataService) FollowUser(ctx context.Context, followerID, userID string) error {
	followerIdentity := followerID
	targetIdentity := userID

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var targetExists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM identities WHERE id = $1)
	`, targetIdentity).Scan(&targetExists); err != nil {
		return err
	}
	if !targetExists {
		return ErrFollowTargetNotFound
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_following (user_id, following_user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, followerIdentity, targetIdentity)
	if err != nil {
		return err
	}

	if _, embeddedServerID, ok := parseIdentityID(identityID(targetIdentity)); ok && embeddedServerID == s.serverID {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO user_followers (user_id, follower_user_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, targetIdentity, followerIdentity)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// RecordRemoteFollower is FollowUser's mirror on the receiving end of a
// federated follow: only user_followers is written (userID isn't this
// server's user, so it has no user_following of its own to maintain).
func (s *DataService) RecordRemoteFollower(ctx context.Context, userID, followerID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_followers (user_id, follower_user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, userID, followerID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// Same reasoning as FollowUser above — both params already in userID@serverID form.
func (s *DataService) UnfollowUser(ctx context.Context, followerID, userID string) error {
	followerIdentity := followerID
	targetIdentity := userID

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		DELETE FROM user_following
		WHERE user_id = $1 AND following_user_id = $2
	`, followerIdentity, targetIdentity)
	if err != nil {
		return err
	}

	if _, embeddedServerID, ok := parseIdentityID(identityID(targetIdentity)); ok && embeddedServerID == s.serverID {
		_, err = tx.ExecContext(ctx, `
			DELETE FROM user_followers
			WHERE user_id = $1 AND follower_user_id = $2
		`, targetIdentity, followerIdentity)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

var ErrPinTargetNotFound = errors.New("pin target not found")
var ErrPinLimitReached = errors.New("pin limit reached")

const maxPinnedReeds = 3

// PinReed requires reedID be a reeds row owned by pinningUserID. Re-pinning
// an already-pinned reed bumps pinned_at instead of erroring.
func (s *DataService) PinReed(ctx context.Context, pinningUserID, reedID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var ownerID string
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM reeds WHERE id = $1`, reedID).Scan(&ownerID)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrPinTargetNotFound
		}
		return err
	}
	if ownerID != pinningUserID {
		return ErrPinTargetNotFound
	}

	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pinned_reeds WHERE user_id = $1 AND reed_id != $2
	`, pinningUserID, reedID).Scan(&count); err != nil {
		return err
	}
	if count >= maxPinnedReeds {
		return ErrPinLimitReached
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pinned_reeds (user_id, reed_id, pinned_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (user_id, reed_id) DO UPDATE SET pinned_at = CURRENT_TIMESTAMP
	`, pinningUserID, reedID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UnpinReed is a no-op (not an error) if reedID wasn't pinned.
func (s *DataService) UnpinReed(ctx context.Context, pinningUserID, reedID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM pinned_reeds WHERE user_id = $1 AND reed_id = $2
	`, pinningUserID, reedID)
	return err
}

// RemoveRemoteFollower is UnfollowUser's mirror on the receiving end of a
// federated unfollow — see RecordRemoteFollower.
func (s *DataService) RemoveRemoteFollower(ctx context.Context, userID, followerID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM user_followers
		WHERE user_id = $1 AND follower_user_id = $2
	`, userID, followerID)
	return err
}

func (s *DataService) SetDefaultIdentity(ctx context.Context, userID string, identityID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE profiles
		SET default_identity_id = $1
		WHERE user_id = $2
	`, identityID, userID)
	if err != nil {
		return err
	}

	return nil
}

// id arrives canonical ("userID@serverID/fingerprint" or
// "fingerprint@serverID") and is self-scoping — it is the sole lookup key.
func (s *DataService) GetPublicKey(ctx context.Context, id string) (*Key, error) {
	var key Key
	var owner sql.NullString
	var revoked bool
	var serverSignatureID int64
	var predID sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT pk.id, pk.owner, pk.armor, pk.created_at,
		       pk.server_signature_id, pk.predecessor_id,
		       EXISTS(
			SELECT 1 FROM public_key_revocations rv
			WHERE rv.key_id = pk.id
		       )
		FROM public_keys pk
		WHERE pk.id = $1
	`, id).Scan(
		&key.ID, &owner, &key.Armor, &key.CreatedAt,
		&serverSignatureID, &predID,
		&revoked,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	// owner is populated for local identities (userID@serverID directly);
	// for remote/federated ones it's NULL, so recover the owner from the
	// canonical id itself instead.
	if owner.Valid {
		key.UserID = owner.String
	} else if ownerID, ownerServer, _, ok := parseKeyFingerprint(identityID(key.ID)); ok {
		key.UserID = string(canonicalID(ownerServer, ownerID))
	}
	serverSig, err := getServerSignatureWire(ctx, s.db, serverSignatureID)
	if err != nil {
		return nil, err
	}
	key.ServerSignature = serverSig
	key.Revoked = revoked
	if predID.Valid {
		key.Predecessor = &predID.String
	}

	return &key, nil
}

func (s *DataService) IsPublicKeyRevoked(ctx context.Context, key *Key) (bool, error) {
	return key.Revoked, nil
}

// id arrives canonical and is self-scoping — the sole lookup key.
func (s *DataService) GetKeyRevocation(ctx context.Context, id string) (*KeyRevocation, error) {
	var rev KeyRevocation
	var successor sql.NullString
	var reason sql.NullString
	var userSigID, serverSigID int64
	var successorSigID sql.NullInt64

	err := s.db.QueryRowContext(ctx, `
		SELECT rv.key_id, rv.reason, rv.successor,
		       rv.user_signature_id, rv.server_signature_id, rv.successor_signature_id
		FROM public_key_revocations rv
		WHERE rv.key_id = $1
	`, id).Scan(
		&rev.ID, &reason, &successor,
		&userSigID, &serverSigID, &successorSigID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	// owner isn't stored on this table — recover it from the canonical id
	// itself (same derivation as GetPublicKey).
	if ownerID, ownerServer, _, ok := parseKeyFingerprint(identityID(rev.ID)); ok {
		rev.UserID = string(canonicalID(ownerServer, ownerID))
	}
	rev.Reason = reason.String
	if successor.Valid && successor.String != "" {
		s := successor.String
		rev.Successor = &s
	}
	if successorSigID.Valid {
		successorSigRow, err := getUserSignatureRow(ctx, s.db, successorSigID.Int64)
		if err != nil {
			return nil, err
		}
		armor := successorSigRow.Signature
		rev.SuccessorSignature = &armor
	}

	userSig, err := getUserSignatureWire(ctx, s.db, userSigID)
	if err != nil {
		return nil, err
	}
	rev.UserSignature = userSig

	serverSig, err := getServerSignatureWire(ctx, s.db, serverSigID)
	if err != nil {
		return nil, err
	}
	rev.ServerSignature = serverSig
	return &rev, nil
}

// id arrives canonical and is self-scoping.
func (s *DataService) PublicKeyExists(ctx context.Context, id string) (bool, error) {
	var exists bool

	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM public_keys
			WHERE id = $1
		)
	`, id).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

// AddPublicKey inserts a replacement key for a user whose previous key
// has already been revoked (rotation). Signup's first-key write stays in
// Signup — this method is the rotation path only.
//
// Integrity checks (all inside one transaction, with row locks):
//  1. predecessor is required
//  2. owner exists
//  3. the new id is not already registered to anyone
//  4. predecessor exists under this owner and is revoked
//  5. predecessor does not already have a successor
//  6. the user has no other active (non-revoked) key
type AddPublicKeyInput struct {
	// ID and PredecessorID arrive canonical ("userID@serverID/fingerprint")
	// already — callers build them via appendEntity(selfIdentity,
	// bareFingerprint) before this is called, since the same canonical
	// value must also appear in the signed payloads built ahead of this
	// call.
	ID        string
	UserID    string
	CreatedAt time.Time
	Armor     string
	Server    ServerSignature

	PredecessorID string
	// PredecessorSignature is the OLD (predecessor) key's detached
	// signature over this new key's armor — the rotation handoff proof.
	// It's written onto the PREDECESSOR's revocation row as
	// successor_signature_id, not onto this new key's own row (that's
	// revocation-certificate data, see public_key_revocations' schema
	// comment in db.go).
	PredecessorSignature string

	// RevocationReason/RevocationUserSignature/RevocationServer revoke
	// PredecessorID in the same transaction as the new key insert — a
	// caller signed in the window between a separate revoke-then-add
	// would have no valid key at all. RevocationUserSignature is the
	// predecessor key's own detached signature over the revocation payload.
	RevocationReason        string
	RevocationUserSignature string
	RevocationServer        ServerSignature
}

// On success it inserts the key, points users.active_key_id at it, and
// writes the successor pointer + successor signature on the predecessor's
// revocation row.
//
// in.UserID arrives already in userID@serverID form (handlers.go passes
// the form value straight through, same convention as GetUserProfile/
// GetActiveKeyFingerprint/UpdateUser elsewhere in this file) and is used
// directly everywhere an FK'd column (public_keys.owner) is touched. The
// existence lock at the top locks the identities row (the actual FK
// target), not users(id), which is a satellite of it.
func (s *DataService) AddPublicKey(ctx context.Context, in AddPublicKeyInput) (*Key, error) {
	if in.PredecessorID == "" {
		return nil, ErrPredecessorRequired
	}
	id := in.ID
	selfIdentity := identityID(in.UserID)
	createdAt := in.CreatedAt
	armor := in.Armor
	predecessor := in.PredecessorID

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Lock the identities row so concurrent rotations for the same owner
	// serialize. Also confirms the owner exists.
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM identities WHERE id = $1 FOR UPDATE
	`, selfIdentity).Scan(new(int))
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	// Global uniqueness: ids identify key material, so two users must
	// never register the same one.
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM public_keys WHERE id = $1
	`, id).Scan(new(int))
	if err == nil {
		return nil, ErrKeyAlreadyExists
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	// Lock the predecessor key row. Rotation is only allowed against a
	// key this owner already holds (id is canonical and self-scoping, so
	// owner doesn't need to appear in the WHERE); revocation is confirmed
	// via the revocations table next.
	err = tx.QueryRowContext(ctx, `
		SELECT 1
		FROM public_keys
		WHERE id = $1
		FOR UPDATE
	`, predecessor).Scan(new(int))
	if err == sql.ErrNoRows {
		return nil, ErrPredecessorNotFound
	}
	if err != nil {
		return nil, err
	}

	// A predecessor may be replaced at most once. Re-rotation against
	// the same revoked key would fork the successor chain.
	var successor sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT successor
		FROM public_key_revocations
		WHERE key_id = $1
		FOR UPDATE
	`, predecessor).Scan(&successor)
	switch {
	case err == sql.ErrNoRows:
		// Not yet revoked — revoke it now, in this same transaction, so
		// there is never a window where the predecessor is revoked but no
		// successor exists yet (a request signed in that gap would have no
		// valid key at all).
		revUserSigID, err := insertUserSignature(ctx, tx, predecessor, in.RevocationUserSignature)
		if err != nil {
			return nil, err
		}
		revServerSigID, err := insertServerSignature(ctx, tx,
			in.RevocationServer.ID,
			in.RevocationServer.Armor,
			in.RevocationServer.SignedAt,
		)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO public_key_revocations (
				key_id, reason,
				user_signature_id, server_signature_id
			) VALUES ($1, $2, $3, $4)
		`, predecessor, in.RevocationReason, revUserSigID, revServerSigID); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if successor.Valid && successor.String != "" {
			return nil, ErrPredecessorAlreadyReplaced
		}
	}

	// Even with a correctly revoked predecessor, refuse if any other
	// active key is still present for this user. Active = no row in
	// public_key_revocations. owner is still the right scoping column
	// here (this check is local-user-only — AddPublicKey is a local
	// rotation endpoint), not derivable from a single id the way the
	// lookups above are.
	var hasActive bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM public_keys pk
			WHERE pk.owner = $1
			  AND NOT EXISTS (
				SELECT 1 FROM public_key_revocations rv
				WHERE rv.key_id = pk.id
			  )
		)
	`, selfIdentity).Scan(&hasActive)
	if err != nil {
		return nil, err
	}
	if hasActive {
		return nil, ErrActiveKeyExists
	}

	serverSignatureID, err := insertServerSignature(ctx, tx,
		in.Server.ID,
		in.Server.Armor,
		in.Server.SignedAt,
	)
	if err != nil {
		return nil, err
	}

	var key Key
	var owner string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO public_keys (
			id, owner, armor, created_at,
			server_signature_id, predecessor_id
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, owner, armor, created_at
	`, id, selfIdentity, armor, createdAt,
		serverSignatureID, predecessor,
	).Scan(&key.ID, &owner, &key.Armor, &key.CreatedAt)
	if err != nil {
		return nil, err
	}
	// owner is already in userID@serverID form — hold it as-is.
	key.UserID = owner
	key.ServerSignature = in.Server
	if in.PredecessorSignature != "" {
		key.Predecessor = &predecessor
	}

	// users.id is identities.id now — use the already-computed selfIdentity,
	// not the bare in.UserID.
	_, err = tx.ExecContext(ctx, `UPDATE users SET active_key_id = $1 WHERE id = $2`, id, selfIdentity)
	if err != nil {
		return nil, err
	}

	// The handoff proof (predecessor's signature over this new key's
	// armor) is stored as a user_signatures row, then referenced from the
	// PREDECESSOR's revocation row — not this new key's own row.
	var successorSigID any
	if in.PredecessorSignature != "" {
		sigID, err := insertUserSignature(ctx, tx, predecessor, in.PredecessorSignature)
		if err != nil {
			return nil, err
		}
		successorSigID = sigID
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE public_key_revocations
		SET successor = $1, successor_signature_id = $2
		WHERE key_id = $3
	`, id, successorSigID, predecessor)
	if err != nil {
		return nil, err
	}

	return &key, tx.Commit()
}

// ReedRef is a parsed echoing/replying target: userID@serverID/reedID.
type ReedRef struct {
	AuthorID string
	ServerID string
	ReedID   string
}

// CanonicalAuthorID returns AuthorID@ServerID — the form every DB lookup,
// WS subscription key, and broadcast UserID field actually needs. AuthorID
// alone is bare; use this instead of AuthorID at any call site that isn't
// itself recomposing a wire ref (FormatReedRef) or calling
// canonicalID(ServerID, AuthorID) directly.
func (r ReedRef) CanonicalAuthorID() string {
	return string(canonicalID(r.ServerID, r.AuthorID))
}

// ParseReedRef parses "userID@serverID/reedID". Returns ok=false for empty or malformed input.
func ParseReedRef(raw string) (ReedRef, bool) {
	author, serverID, reedID, ok := parseKeyFingerprint(identityID(strings.TrimSpace(raw)))
	if !ok {
		return ReedRef{}, false
	}
	return ReedRef{AuthorID: author, ServerID: serverID, ReedID: reedID}, true
}

// FormatReedRef returns the canonical wire form userID@serverID/reedID.
func FormatReedRef(ref ReedRef) string {
	return string(canonicalID(ref.ServerID, ref.AuthorID, ref.ReedID))
}

// ReedAttestation is tip reed metadata plus stored user/server signatures.
type ReedAttestation struct {
	Reed
	UserKeyID         string
	UserSignature     string
	ServerFingerprint string
	ServerSignature   string
	ServerSignedAt    time.Time
}

// createReedParams is the shared insert payload for SignReed persistence.
type createReedParams struct {
	ReedID             string
	UserID             string
	UserKeyID          string
	UserSignatureB64   string
	ServerFingerprint  string
	ServerSignatureB64 string
	Timestamp          time.Time
	Tags               []string
	Mentions           []string
	// PreviousID is the reed the client believes is the author's current
	// tip. Empty means "author has zero reeds" — see checkReedTip.
	PreviousID string
}

// ResolveThreadIDForParent returns the canonical thread id for a reply to parent P.
// When P is the thread root (no reed_replies row for P), thread id = ref(P).
// Otherwise thread id is inherited from P's reply row.
func (s *DataService) ResolveThreadIDForParent(ctx context.Context, parent ReedRef) (string, error) {
	var threadID string
	err := s.db.QueryRowContext(ctx, `
		SELECT thread_id FROM reed_replies
		WHERE reed_id = $1
	`, FormatReedRef(parent)).Scan(&threadID)
	if err == sql.ErrNoRows {
		return FormatReedRef(parent), nil
	}
	if err != nil {
		return "", err
	}
	return threadID, nil
}

// InsertReply records a direct reply in reed_replies. replyReedID is canonical.
func (s *DataService) InsertReply(
	ctx context.Context,
	threadID string,
	parent ReedRef,
	replyReedID string,
	ts time.Time,
) (replyIndexed bool, err error) {
	ts = ts.UTC().Truncate(time.Second)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if err = s.insertReplyTx(ctx, tx, threadID, parent, replyReedID, ts); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// insertReplyTx takes replyIdentity already in userID@serverID form (the
// caller has either converted a local userID via canonicalID, or
// is insertReedCoreTx's selfIdentity). parent's identity is built the same
// way ResolveThreadIDForParent does, from the full ReedRef.
func (s *DataService) insertReplyTx(
	ctx context.Context,
	tx *sql.Tx,
	threadID string,
	parent ReedRef,
	replyReedID string,
	ts time.Time,
) error {
	// parent_reed_id and reed_id both FK to reed_identities, which may need
	// a row minted here for whichever side is foreign to this server:
	// parent_reed_id when a local reed replies to a foreign one
	// (CreateReedWithReply's case); reed_id when a foreign server is
	// telling us about a reply to one of OUR reeds (the peer-notify leg's
	// case) — a local reed already has its row from its own creation.
	parentReedID := FormatReedRef(parent)
	if parent.ServerID != s.serverID {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reed_identities (id, server_id)
			VALUES ($1, $2)
			ON CONFLICT (id) DO NOTHING
		`, parentReedID, parent.ServerID); err != nil {
			return fmt.Errorf("insert reply parent reed identity: %w", err)
		}
	}
	if _, replyServerID, _, ok := parseKeyFingerprint(identityID(replyReedID)); ok && replyServerID != s.serverID {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reed_identities (id, server_id)
			VALUES ($1, $2)
			ON CONFLICT (id) DO NOTHING
		`, replyReedID, replyServerID); err != nil {
			return fmt.Errorf("insert reply reed identity: %w", err)
		}
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO reed_replies (
			thread_id, reed_id,
			parent_reed_id,
			timestamp
		)
		VALUES ($1, $2, $3, $4)
	`, threadID, replyReedID,
		parentReedID,
		ts)
	if err != nil {
		return fmt.Errorf("insert reply index: %w", err)
	}
	return nil
}

// ReplyListItem is one direct reply in a paginated list response.
type ReplyListItem struct {
	UserID string `json:"userID"`
	ReedID string `json:"reedID"`
}

// ReplyListResponse is the body of GET /reeds/{userID}/{reedID}/replies.
type ReplyListResponse struct {
	Replies []ReplyListItem `json:"replies"`
	HasMore bool            `json:"hasMore"`
}

// ListReplies returns visible direct replies to parentReedID, oldest first.
//
// parentReedID is canonical (authorID@serverID/uuid); each reply's own
// author is recovered from its reed_id via parseKeyFingerprint
// for the wire item's UserID field.
func (s *DataService) ListReplies(ctx context.Context, parentReedID string, limit int, before *time.Time) (*ReplyListResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	args := []any{parentReedID}
	// LEFT JOIN, not JOIN: a reply may itself be foreign (reed_id has no
	// local reeds row), in which case r.user_id is NULL and the
	// account_removals check below is trivially satisfied — this server
	// has no way to know a foreign author's removal status anyway, so
	// "can't tell" correctly falls through to "don't filter it out".
	query := `
		SELECT rr2.reed_id, rr2.timestamp
		FROM reed_replies rr2
		LEFT JOIN reeds r ON r.id = rr2.reed_id
		WHERE rr2.parent_reed_id = $1
		AND NOT EXISTS (
			SELECT 1 FROM reed_removals rr
			WHERE rr.reed_id = rr2.reed_id
		)
		AND NOT EXISTS (
			SELECT 1 FROM account_removals ar WHERE ar.user_id = r.user_id
		)
	`
	if before != nil {
		args = append(args, before.UTC().Truncate(time.Second))
		query += fmt.Sprintf(`
			AND (rr2.timestamp, rr2.reed_id) > ($%d, '')
		`, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(`
		ORDER BY rr2.timestamp ASC, rr2.reed_id ASC
		LIMIT $%d
	`, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ReplyListItem
	for rows.Next() {
		var reedID string
		var _ts time.Time
		if err := rows.Scan(&reedID, &_ts); err != nil {
			return nil, err
		}
		userID, serverID, _, _ := parseKeyFingerprint(identityID(reedID))
		items = append(items, ReplyListItem{
			UserID: string(canonicalID(serverID, userID)),
			ReedID: reedID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if items == nil {
		items = []ReplyListItem{}
	}
	return &ReplyListResponse{Replies: items, HasMore: hasMore}, nil
}

// FollowListItem is one row of a following/followers list.
type FollowListItem struct {
	UserID     string    `json:"userID"`
	FollowedAt time.Time `json:"followedAt"`
}

// FollowListResponse is the body of GET /users/{userID}/following and
// GET /users/{userID}/followers.
type FollowListResponse struct {
	Users   []FollowListItem `json:"users"`
	HasMore bool             `json:"hasMore"`
}

// ListFollowing returns userID's following list, oldest-followed first.
func (s *DataService) ListFollowing(ctx context.Context, userID string, limit int, before *time.Time) (*FollowListResponse, error) {
	return s.listFollowEdge(ctx, "user_following", "following_user_id", userID, limit, before)
}

// ListFollowers returns userID's followers list, oldest-followed first.
func (s *DataService) ListFollowers(ctx context.Context, userID string, limit int, before *time.Time) (*FollowListResponse, error) {
	return s.listFollowEdge(ctx, "user_followers", "follower_user_id", userID, limit, before)
}

// listFollowEdge is the shared keyset-paginated query behind ListFollowing /
// ListFollowers — same table shape, only the table/column name differs.
// user_id and otherCol are both direct FKs to identities(id); userID is
// the URL-path-identified subject, already in userID@serverID form, and
// the scanned edge ids are used as-is for the FollowListItem wire shape.
func (s *DataService) listFollowEdge(ctx context.Context, table, otherCol, userID string, limit int, before *time.Time) (*FollowListResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	selfIdentity := userID
	args := []any{selfIdentity}
	query := fmt.Sprintf(`
		SELECT e.%[1]s, e.created_at
		FROM %[2]s e
		WHERE e.user_id = $1
		AND NOT EXISTS (
			SELECT 1 FROM account_removals ar WHERE ar.user_id = e.%[1]s
		)
	`, otherCol, table)
	if before != nil {
		args = append(args, before.UTC().Truncate(time.Second))
		query += fmt.Sprintf(`
			AND (e.created_at, e.%s) > ($%d, '')
		`, otherCol, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(`
		ORDER BY e.created_at ASC, e.%s ASC
		LIMIT $%d
	`, otherCol, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []FollowListItem
	for rows.Next() {
		var id string
		var followedAt time.Time
		if err := rows.Scan(&id, &followedAt); err != nil {
			return nil, err
		}
		items = append(items, FollowListItem{UserID: id, FollowedAt: followedAt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if items == nil {
		items = []FollowListItem{}
	}
	return &FollowListResponse{Users: items, HasMore: hasMore}, nil
}

// checkReedTipTx enforces the history-fork safeguard (see
// specs/recovery/16_reed_tip_check.md): previousID must name the author's
// current tip (newest non-removed reed by signed_at, id DESC tie-break), or
// be empty when the author has zero reeds. Locks the author's identities row
// first so concurrent creates for the same author serialize — caller must
// run this and the subsequent INSERT INTO reeds in the same transaction,
// otherwise the check is only advisory under a dual-tab/dual-device race.
//
// selfIdentity is the identities.id for the author — callers
// (insertReedCoreTx) construct it once and pass it down.
func checkReedTipTx(ctx context.Context, tx *sql.Tx, selfIdentity identityID, previousID string) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM identities WHERE id = $1 FOR UPDATE
	`, selfIdentity).Scan(new(int)); err != nil {
		if err == sql.ErrNoRows {
			return ErrUserNotFound
		}
		return err
	}

	var tip string
	err := tx.QueryRowContext(ctx, `
		SELECT r.id FROM reeds r
		WHERE r.user_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rm
		      WHERE rm.reed_id = r.id
		  )
		ORDER BY r.signed_at DESC, r.id DESC
		LIMIT 1
	`, selfIdentity).Scan(&tip)

	switch {
	case err == sql.ErrNoRows:
		if previousID != "" {
			return ErrReedFork
		}
		return nil
	case err != nil:
		return err
	case previousID != tip:
		return ErrReedFork
	default:
		return nil
	}
}

// insertReedCoreTx converts p.UserID to identities.id form once, up
// front, and uses it for every column that FKs to identities(id) (see
// db.go's FOREIGN KEY clauses for reeds, reed_allocations, reed_mentions).
// Mention targets (p.Mentions) are converted the same way, via
// canonicalID, since only local mentions are inserted today.
//
// p.ReedID is already canonical (authorID@serverID/uuid) — callers build
// it via appendEntity before constructing createReedParams.
func (s *DataService) insertReedCoreTx(
	ctx context.Context,
	tx *sql.Tx,
	p createReedParams,
) (Reed, error) {
	bareReedID := p.ReedID
	if _, _, suffix, ok := parseKeyFingerprint(identityID(p.ReedID)); ok {
		bareReedID = suffix
	}
	if !isValidUUIDv7(bareReedID) {
		return Reed{}, fmt.Errorf("invalid reed ID")
	}

	// p.UserID arrives in userID@serverID form already; checkReedTipTx
	// requires identityID, so this is a plain type conversion.
	selfIdentity := identityID(p.UserID)

	if err := checkReedTipTx(ctx, tx, selfIdentity, p.PreviousID); err != nil {
		return Reed{}, err
	}

	ts := p.Timestamp.UTC().Truncate(time.Second)

	// insertUserSignature/insertServerSignature still use non-context
	// queries internally, so these two inserts land as root spans rather than
	// nested under ctx's request span — a known gap, not a bug (see
	// specs/observability/04_context_threading.md).
	userSigID, err := insertUserSignature(ctx, tx, p.UserKeyID, p.UserSignatureB64)
	if err != nil {
		return Reed{}, err
	}
	serverSigID, err := insertServerSignature(ctx, tx, p.ServerFingerprint, p.ServerSignatureB64, ts)
	if err != nil {
		return Reed{}, err
	}

	// reed_identities row must exist before reeds.id can FK to it — this
	// is the "identities layer" for reeds (mirrors how users.id FKs
	// identities.id). Every local reed gets one here; a foreign reed gets
	// one via the cross-server relay bridge instead (realtime package).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_identities (id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, p.ReedID, s.serverID); err != nil {
		return Reed{}, fmt.Errorf("insert reed identity: %w", err)
	}

	var created Reed
	var createdOwner string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO reeds (
			id, user_id, signed_at,
			user_signature_id, server_signature_id
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, signed_at
	`, p.ReedID, selfIdentity, ts, userSigID, serverSigID).Scan(
		&created.ID,
		&createdOwner,
		&created.Timestamp,
	)
	if err != nil {
		return Reed{}, err
	}
	// Reed.UserID is the wire shape (json:"userID") — holds this value
	// directly, no decode step.
	created.UserID = createdOwner

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_allocations (reed_id, holder_user_id)
		VALUES ($1, $2)
	`, p.ReedID, selfIdentity); err != nil {
		return Reed{}, fmt.Errorf("allocate reed to author: %w", err)
	}

	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO pending_fanout (reed_id, tags)
		VALUES ($1, $2)
	`, p.ReedID, pq.Array(tags)); err != nil {
		return Reed{}, fmt.Errorf("insert pending fanout: %w", err)
	}

	for _, mentionedUserID := range p.Mentions {
		if err := insertMentionRow(ctx, tx, p.ReedID, mentionedUserID); err != nil {
			return Reed{}, fmt.Errorf("insert mention index: %w", err)
		}
	}

	return created, nil
}

// CreateReed inserts reed metadata, author allocation, pending fanout stash,
// and attestation rows used for SignReed replay.
func (s *DataService) CreateReed(ctx context.Context, p createReedParams) (*Reed, error) {
	p.Timestamp = p.Timestamp.UTC().Truncate(time.Second)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	created, err := s.insertReedCoreTx(ctx, tx, p)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &created, nil
}

// CreateReedWithEcho inserts a reed and indexes it as an echo of echoTarget.
// echoIndexed is true when a new reed_echoes row was inserted for a
// different author than the target (self-echoes are excluded from counts/
// notifications, same as CountEchoes/GetReedChorus). isBlank records
// whether the echoing reed carried no commentary — see is_blank on
// reed_echoes.
func (s *DataService) CreateReedWithEcho(
	ctx context.Context,
	p createReedParams,
	echoTarget ReedRef,
	isBlank bool,
) (reed *Reed, echoIndexed bool, err error) {
	p.Timestamp = p.Timestamp.UTC().Truncate(time.Second)
	ts := p.Timestamp

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	created, err := s.insertReedCoreTx(ctx, tx, p)
	if err != nil {
		return nil, false, err
	}

	// echoing_reed_id's reed_identities row already exists (minted by
	// insertReedCoreTx above, same as every local reed); echoed_reed_id's
	// may need one minted here for a foreign target no one on this server
	// has referenced before (a local target already has one, minted at
	// its own creation time).
	echoedReedID := FormatReedRef(echoTarget)
	if echoTarget.ServerID != s.serverID {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reed_identities (id, server_id)
			VALUES ($1, $2)
			ON CONFLICT (id) DO NOTHING
		`, echoedReedID, echoTarget.ServerID); err != nil {
			return nil, false, fmt.Errorf("insert echo target reed identity: %w", err)
		}
	}

	// echoing_author_id is p.UserID itself — always local, this account is
	// signed in on this server to call SignReed at all. echoed_author_id
	// may be foreign, in which case it needs an identities row (same
	// UpsertRemoteIdentity-style lazy creation used everywhere else a
	// foreign identity is first referenced) before the FK below can hold.
	echoedAuthorID := echoTarget.CanonicalAuthorID()
	if echoTarget.ServerID != s.serverID {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO identities (id, server_id)
			VALUES ($1, $2)
			ON CONFLICT (id) DO NOTHING
		`, echoedAuthorID, echoTarget.ServerID); err != nil {
			return nil, false, fmt.Errorf("insert echo target author identity: %w", err)
		}
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO reed_echoes (
			echoing_reed_id, echoed_reed_id,
			echoing_author_id, echoed_author_id,
			is_blank, signed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (echoing_reed_id) DO NOTHING
	`, p.ReedID, echoedReedID, p.UserID, echoedAuthorID, isBlank, ts)
	if err != nil {
		return nil, false, fmt.Errorf("insert echo index: %w", err)
	}
	n, _ := res.RowsAffected()
	echoIndexed = n > 0 && p.UserID != echoedAuthorID

	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &created, echoIndexed, nil
}

// InsertForeignEcho records that echoingReedID (authored on a peer)
// echoes echoedReedID, one of THIS server's own reeds — the home-server
// side of the echo-notify peer leg. echoedReedID/echoedAuthorID are
// already local (this server's own reed and its author, both already
// have identities/reed_identities rows from the reed's own creation);
// echoingReedID/echoingAuthorID are foreign and get theirs minted here,
// same low "legitimate reference" bar used everywhere else a foreign
// identity is first referenced by this server. Idempotent: a retried
// notify is a harmless no-op (ON CONFLICT on reed_echoes' PK).
func (s *DataService) InsertForeignEcho(ctx context.Context, echoingReedID, echoedReedID, echoingAuthorID, echoedAuthorID string, isBlank bool, ts time.Time) error {
	_, echoingServerID, _, ok := parseKeyFingerprint(identityID(echoingReedID))
	if !ok {
		return fmt.Errorf("malformed echoing reed id: %s", echoingReedID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_identities (id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, echoingReedID, echoingServerID); err != nil {
		return fmt.Errorf("insert echoing reed identity: %w", err)
	}

	_, echoingIdentityServerID, ok := parseIdentityID(identityID(echoingAuthorID))
	if !ok {
		return fmt.Errorf("malformed echoing author id: %s", echoingAuthorID)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO identities (id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, echoingAuthorID, echoingIdentityServerID); err != nil {
		return fmt.Errorf("insert echoing author identity: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_echoes (
			echoing_reed_id, echoed_reed_id,
			echoing_author_id, echoed_author_id,
			is_blank, signed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (echoing_reed_id) DO NOTHING
	`, echoingReedID, echoedReedID, echoingAuthorID, echoedAuthorID, isBlank, ts.UTC().Truncate(time.Second)); err != nil {
		return fmt.Errorf("insert foreign echo: %w", err)
	}

	return tx.Commit()
}

// ErrReedNotFound is returned by IsBlankEcho when reedID is not a live tip reed.
var ErrReedNotFound = errors.New("reed not found")

// IsBlankEcho reports whether reedID is itself a content-less echo — used
// to reject a reply/echo aimed at it instead of the underlying original.
// Returns false (not an error) when the reed exists but isn't an echo.
// Returns ErrReedNotFound when the reed doesn't exist — a missing reed is
// not the same as "not blank" and callers must not conflate the two.
func (s *DataService) IsBlankEcho(ctx context.Context, reedID string) (bool, error) {
	exists, err := s.ReedExists(ctx, reedID)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, ErrReedNotFound
	}

	var isBlank bool
	err = s.db.QueryRowContext(ctx, `
		SELECT is_blank FROM reed_echoes
		WHERE echoing_reed_id = $1
	`, reedID).Scan(&isBlank)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return isBlank, nil
}

// CreateReedWithReply inserts a reed and indexes it as a direct reply to parent.
func (s *DataService) CreateReedWithReply(
	ctx context.Context,
	p createReedParams,
	threadID string,
	parent ReedRef,
) (reed *Reed, err error) {
	p.Timestamp = p.Timestamp.UTC().Truncate(time.Second)
	ts := p.Timestamp

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	created, err := s.insertReedCoreTx(ctx, tx, p)
	if err != nil {
		return nil, err
	}

	if err = s.insertReplyTx(ctx, tx, threadID, parent, p.ReedID, ts); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &created, nil
}

// GetReedAttestation loads a tip reed and its stored signatures. reedID is canonical.
func (s *DataService) GetReedAttestation(ctx context.Context, reedID string) (*ReedAttestation, error) {
	var att ReedAttestation
	var owner string
	err := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.user_id, r.signed_at,
			us.public_key_id, us.signature,
			ss.private_key_id, ss.signature, ss.signed_at
		FROM reeds r
		JOIN user_signatures us ON us.id = r.user_signature_id
		JOIN server_signatures ss ON ss.id = r.server_signature_id
		WHERE r.id = $1
	`, reedID).Scan(
		&att.ID,
		&owner,
		&att.Timestamp,
		&att.UserKeyID,
		&att.UserSignature,
		&att.ServerFingerprint,
		&att.ServerSignature,
		&att.ServerSignedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	att.UserID = owner
	att.Timestamp = att.Timestamp.UTC().Truncate(time.Second)
	att.ServerSignedAt = att.ServerSignedAt.UTC().Truncate(time.Second)
	return &att, nil
}

// DeleteReed's reedID is canonical; the caller has already checked the
// session-authenticated user owns it.
func (s *DataService) DeleteReed(ctx context.Context, reedID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reeds WHERE id = $1
	`, reedID)
	if err != nil {
		return err
	}

	return nil
}

// ReedExists reports whether reedID is a live tip reed.
func (s *DataService) ReedExists(ctx context.Context, reedID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM reeds r
			WHERE r.id = $1
			  AND NOT EXISTS (
			      SELECT 1 FROM reed_removals rr
			      WHERE rr.reed_id = r.id
			  )
			  AND NOT EXISTS (
			      SELECT 1 FROM account_removals ar WHERE ar.user_id = r.user_id
			  )
		)
	`, reedID).Scan(&exists)
	return exists, err
}

// MentionTargetValid reports whether userID exists, is not account-removed,
// and serverID is a known row in servers — the gate for a mention to be
// indexed. Checks `users`/`account_removals` directly, so a foreign mention
// target with no local `users` row is never valid yet.
func (s *DataService) MentionTargetValid(ctx context.Context, userID, serverID string) (bool, error) {
	targetIdentity := canonicalID(serverID, userID)
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users u
			WHERE u.id = $1
			  AND EXISTS (
			      SELECT 1 FROM servers s WHERE s.id = $2
			  )
			  AND NOT EXISTS (
			      SELECT 1 FROM account_removals ar WHERE ar.user_id = u.id
			  )
		)
	`, targetIdentity, serverID).Scan(&exists)
	return exists, err
}

// insertMentionRow records one (mentioningReedID, mentionedUserID) row.
// q is signingDBTX (satisfied by both *sql.Tx and *sql.DB — the same
// interface already used for the reed-like loader below) so the same
// statement serves insertReedCoreTx's in-transaction local insert and the
// mention-notify federation handler's standalone insert.
func insertMentionRow(ctx context.Context, q signingDBTX, mentioningReedID, mentionedUserID string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO reed_mentions (mentioning_reed_id, mentioned_user_id)
		VALUES ($1, $2)
		ON CONFLICT (mentioning_reed_id, mentioned_user_id) DO NOTHING
	`, mentioningReedID, mentionedUserID)
	return err
}

// InsertMentionRow is insertMentionRow against this service's own *sql.DB —
// the mention-notify federation handler's entry point (no transaction of
// its own to join, unlike a local publish).
func (s *DataService) InsertMentionRow(ctx context.Context, mentioningReedID, mentionedUserID string) error {
	return insertMentionRow(ctx, s.db, mentioningReedID, mentionedUserID)
}

// MentionListItem is one row of a mentioned user's pull inbox. AuthorID is
// parsed from the canonical reed id, no join needed.
type MentionListItem struct {
	ReedID    string    `json:"reedID"`
	AuthorID  string    `json:"authorID"`
	CreatedAt time.Time `json:"createdAt"`
}

// MentionListResponse is the GET .../mentions response shape.
type MentionListResponse struct {
	Mentions []MentionListItem `json:"mentions"`
	HasMore  bool              `json:"hasMore"`
}

// GetMentionsForUser returns mentionedUserID's pull inbox, oldest first.
// limit is clamped to [1, 100], defaulting to 50.
func (s *DataService) GetMentionsForUser(ctx context.Context, mentionedUserID string, limit int, before *time.Time) (*MentionListResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	args := []any{mentionedUserID}
	query := `
		SELECT mentioning_reed_id, created_at
		FROM reed_mentions
		WHERE mentioned_user_id = $1
	`
	if before != nil {
		args = append(args, before.UTC().Truncate(time.Second))
		query += fmt.Sprintf(" AND (created_at, mentioning_reed_id) > ($%d, '')", len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY created_at ASC, mentioning_reed_id ASC LIMIT $%d", len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MentionListItem
	for rows.Next() {
		var reedID string
		var createdAt time.Time
		if err := rows.Scan(&reedID, &createdAt); err != nil {
			return nil, err
		}
		authorID := reedID
		if a, ok := authorOf(identityID(reedID)); ok {
			authorID = string(a)
		}
		items = append(items, MentionListItem{ReedID: reedID, AuthorID: authorID, CreatedAt: createdAt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return &MentionListResponse{Mentions: items, HasMore: hasMore}, nil
}

// DeleteMentionEntry removes one (reedID, mentionedUserID) row. Scoping to
// the caller's own id is the handler layer's job. Returns false, not an
// error, if no matching row existed.
func (s *DataService) DeleteMentionEntry(ctx context.Context, reedID, mentionedUserID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_mentions
		WHERE mentioning_reed_id = $1 AND mentioned_user_id = $2
	`, reedID, mentionedUserID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// UserSearchResult is one row in a GET /users/search response — minimal
// fields only, no keys, no bio. ServerName is the servers.name a viewer can
// read to disambiguate two identically-named usernames on different
// servers (servers.id is an opaque short id, not meant for display).
type UserSearchResult struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	ServerName string `json:"serverName"`
}

// SearchUsers returns users whose username contains query (case-insensitive
// substring match), excluding account-removed users and excludeUserID (the
// caller themselves — pass "" to not exclude anyone), ordered by username.
func (s *DataService) SearchUsers(ctx context.Context, query, excludeUserID string, limit int) ([]UserSearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	// account_removals.user_id FKs to identities(id), and u.id is that same
	// form directly now (identity_id no longer exists as a separate
	// column) — join against u.id on both sides. identities.server_id FKs
	// to servers.id, so join through it to servers.name for display.
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.username, s.name
		FROM users u
		JOIN identities i ON i.id = u.id
		JOIN servers s ON s.id = i.server_id
		WHERE u.username ILIKE '%' || $1 || '%'
		  AND u.id != $2
		  AND NOT EXISTS (
		      SELECT 1 FROM account_removals ar WHERE ar.user_id = u.id
		  )
		ORDER BY u.username ASC
		LIMIT $3
	`, query, excludeUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []UserSearchResult{}
	for rows.Next() {
		var r UserSearchResult
		if err := rows.Scan(&r.ID, &r.Username, &r.ServerName); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// CountEchoes returns how many echoes point at the given reed. Self-echoes
// (a user echoing their own reed) are excluded. Echoer identity is the
// author embedded in echoing_reed_id, recovered via the reeds join.
func (s *DataService) CountEchoes(ctx context.Context, echoedReedID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(echo_count, 0) FROM reed_stats WHERE reed_id = $1
	`, echoedReedID).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// EchoerListItem is one echoer in a paginated list response.
type EchoerListItem struct {
	UserID   string    `json:"userID"`
	EchoedAt time.Time `json:"echoedAt"`
}

// EchoerListResponse is the body of GET /reeds/{userID}/{reedID}/echoers.
type EchoerListResponse struct {
	Users   []EchoerListItem `json:"users"`
	HasMore bool             `json:"hasMore"`
}

// GetReedChorus returns the users who echoed the given reed, oldest first.
// Self-echoes (a user echoing their own reed) are excluded.
func (s *DataService) GetReedChorus(ctx context.Context, echoedReedID string, limit int, before *time.Time) (*EchoerListResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	// A user can echo the same target more than once (separate echoing
	// reeds), so group by echoing_author_id and take their earliest echo
	// as the row's timestamp — the chorus lists each person once.
	// echoing_author_id is stored directly on reed_echoes (not derived via
	// a reeds join, which would only match a local echoer). Local-only
	// account_removals rows still filter fine against it since it's the
	// same canonical id form identities.id/users.id share.
	args := []any{echoedReedID}
	query := `
		SELECT echoing_author_id, MIN(signed_at) AS first_echoed_at
		FROM reed_echoes
		WHERE echoed_reed_id = $1
		AND echoing_author_id != echoed_author_id
		AND NOT EXISTS (
			SELECT 1 FROM account_removals ar WHERE ar.user_id = echoing_author_id
		)
		GROUP BY echoing_author_id
	`
	if before != nil {
		args = append(args, before.UTC().Truncate(time.Second))
		query += fmt.Sprintf(`
			HAVING (MIN(signed_at), echoing_author_id) > ($%d, '')
		`, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(`
		ORDER BY first_echoed_at ASC, echoing_author_id ASC
		LIMIT $%d
	`, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []EchoerListItem
	for rows.Next() {
		var userID string
		var echoedAt time.Time
		if err := rows.Scan(&userID, &echoedAt); err != nil {
			return nil, err
		}
		items = append(items, EchoerListItem{UserID: userID, EchoedAt: echoedAt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if items == nil {
		items = []EchoerListItem{}
	}
	return &EchoerListResponse{Users: items, HasMore: hasMore}, nil
}

// DeleteEchoIndexForReed clears echo index rows when a reed is removed.
// Returns distinct echoed targets whose counts may have changed (excluding
// the removed reed itself, which no longer has live tip subscribers).
// reedID is the removed reed's own canonical id, matching echoing_reed_id.
func (s *DataService) DeleteEchoIndexForReed(ctx context.Context, reedID string) ([]ReedRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT echoed_reed_id
		FROM reed_echoes
		WHERE echoing_reed_id = $1
	`, reedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ReedRef
	for rows.Next() {
		var echoedReedID string
		if err := rows.Scan(&echoedReedID); err != nil {
			return nil, err
		}
		ref, ok := ParseReedRef(echoedReedID)
		if !ok {
			continue
		}
		targets = append(targets, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_echoes WHERE echoing_reed_id = $1
	`, reedID); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_echoes WHERE echoed_reed_id = $1
	`, reedID); err != nil {
		return nil, err
	}
	return targets, nil
}

// DeleteEchoesByAuthor drops echo index rows created by userID (the echoing
// author). Returns distinct echoed targets whose counts may have changed.
// userID arrives in userID@serverID form already; echoing_reed_id rows
// authored by userID are matched by canonical id prefix.
func (s *DataService) DeleteEchoesByAuthor(ctx context.Context, userID string) ([]ReedRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT re.echoed_reed_id
		FROM reed_echoes re
		JOIN reeds r ON r.id = re.echoing_reed_id
		WHERE r.user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ReedRef
	for rows.Next() {
		var echoedReedID string
		if err := rows.Scan(&echoedReedID); err != nil {
			return nil, err
		}
		ref, ok := ParseReedRef(echoedReedID)
		if !ok {
			continue
		}
		targets = append(targets, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_echoes WHERE echoing_reed_id IN (
			SELECT id FROM reeds WHERE user_id = $1
		)
	`, userID); err != nil {
		return nil, err
	}
	return targets, nil
}

// DeleteMentionsForReed clears mention index rows contained in a removed
// reed. reedID is canonical, matching mentioning_reed_id directly.
func (s *DataService) DeleteMentionsForReed(ctx context.Context, reedID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_mentions WHERE mentioning_reed_id = $1
	`, reedID)
	return err
}

// DeleteMentionsByAuthor clears mention index rows on account removal: rows
// the removed user authored (mentioning), and rows mentioning the removed
// user (mentioned) — both sides. userID arrives in userID@serverID form
// already, so it's a direct match against mentioned_user_id with no
// separate server check needed. Mentioning rows are matched by canonical
// id prefix via the reeds join (mentioning_reed_id has no user_id column
// of its own anymore).
func (s *DataService) DeleteMentionsByAuthor(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_mentions
		WHERE mentioning_reed_id IN (SELECT id FROM reeds WHERE user_id = $1)
		   OR mentioned_user_id = $1
	`, userID)
	return err
}

// ReplyCountNotifyTargets returns every ancestor of parentReedID (inclusive)
// whose subtree reply count changes when a direct reply to it is added or removed.
func (s *DataService) ReplyCountNotifyTargets(ctx context.Context, parentReedID string) ([]ReedRef, error) {
	var targets []ReedRef
	reedID := parentReedID
	for {
		ref, ok := ParseReedRef(reedID)
		if !ok {
			return nil, fmt.Errorf("malformed reed id: %s", reedID)
		}
		targets = append(targets, ref)
		var nextReedID string
		err := s.db.QueryRowContext(ctx, `
			SELECT parent_reed_id
			FROM reed_replies
			WHERE reed_id = $1
		`, reedID).Scan(&nextReedID)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return nil, err
		}
		reedID = nextReedID
	}
	return targets, nil
}

// ReplyCountNotifyTargetsForRemovedReply returns ancestors whose subtree count
// drops when replyReedID is removed. nil when not indexed as a reply.
func (s *DataService) ReplyCountNotifyTargetsForRemovedReply(ctx context.Context, replyReedID string) ([]ReedRef, error) {
	var parentReedID string
	err := s.db.QueryRowContext(ctx, `
		SELECT parent_reed_id
		FROM reed_replies
		WHERE reed_id = $1
	`, replyReedID).Scan(&parentReedID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.ReplyCountNotifyTargets(ctx, parentReedID)
}

// DeleteForeignReplyReference removes a foreign reply's reed_replies row —
// the home-server side of the reply-removal-notify peer leg. Returns
// false (not an error) if no such row exists, matching DeleteReedLike's
// own "already gone is a no-op" convention.
func (s *DataService) DeleteForeignReplyReference(ctx context.Context, replyReedID string) (deleted bool, err error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_replies WHERE reed_id = $1
	`, replyReedID)
	if err != nil {
		return false, fmt.Errorf("delete foreign reply reference: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteForeignEchoReference removes a foreign echo's reed_echoes row —
// the home-server side of the echo-removal-notify peer leg. Returns
// false (not an error) if no such row exists, same "already gone is a
// no-op" convention as DeleteForeignReplyReference/DeleteReedLike.
func (s *DataService) DeleteForeignEchoReference(ctx context.Context, echoingReedID string) (deleted bool, err error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_echoes WHERE echoing_reed_id = $1
	`, echoingReedID)
	if err != nil {
		return false, fmt.Errorf("delete foreign echo reference: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReplyCountNotifyTargetsForAuthor returns distinct ancestors whose subtree
// counts may change when all of userID's indexed replies are treated as removed.
func (s *DataService) ReplyCountNotifyTargetsForAuthor(ctx context.Context, userID string) ([]ReedRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT rr.parent_reed_id
		FROM reed_replies rr
		JOIN reeds r ON r.id = rr.reed_id
		WHERE r.user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	var targets []ReedRef
	for rows.Next() {
		var parentReedID string
		if err := rows.Scan(&parentReedID); err != nil {
			return nil, err
		}
		ancestors, err := s.ReplyCountNotifyTargets(ctx, parentReedID)
		if err != nil {
			return nil, err
		}
		for _, t := range ancestors {
			key := t.AuthorID + "/" + t.ReedID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			targets = append(targets, t)
		}
	}
	return targets, rows.Err()
}

// GetSubtreeReplyCount returns descendant reply count beneath reedID,
// maintained incrementally by reed_stats triggers (db.go) rather than
// recomputed here on every call.
func (s *DataService) GetSubtreeReplyCount(ctx context.Context, reedID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(reply_count, 0) FROM reed_stats WHERE reed_id = $1
	`, reedID).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return count, err
}

// ReedOrRemovalResult is returned by GetReedOrRemovalCert. Account removal wins
// over reed removal; a tombstone is returned without loading the reed row.
// All fields nil means the reed row does not exist.
type ReedOrRemovalResult struct {
	Reed           *Reed
	AccountRemoval *accountRemovalCert
	ReedRemoval    *reedRemovalCert
}

// GetReed loads a live tip reed by its canonical id.
func (s *DataService) GetReed(ctx context.Context, reedID string) (*Reed, error) {
	var reed Reed
	var owner string
	err := s.db.QueryRowContext(ctx, `
	SELECT id, user_id, signed_at
		FROM reeds
		WHERE id = $1
	`, reedID,
	).Scan(
		&reed.ID,
		&owner,
		&reed.Timestamp,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	reed.UserID = owner

	return &reed, nil
}

// GetReedOrRemovalCert loads tip reed metadata when neither the account nor the
// reed has a removal cert. Tombstones are returned in the result instead of
// the reed row. reedID is canonical; its embedded author is used for the
// account-removal check.
func (s *DataService) GetReedOrRemovalCert(ctx context.Context, reedID string) (ReedOrRemovalResult, error) {
	var out ReedOrRemovalResult

	userID, _, _, ok := parseKeyFingerprint(identityID(reedID))
	if !ok {
		return out, fmt.Errorf("malformed reed id: %s", reedID)
	}

	accountRemoval, err := s.GetAccountRemoval(ctx, userID)
	if err != nil {
		return out, err
	}
	if accountRemoval != nil {
		out.AccountRemoval = accountRemoval
		return out, nil
	}

	removal, err := s.GetReedRemoval(ctx, reedID)
	if err != nil {
		return out, err
	}
	if removal != nil {
		out.ReedRemoval = removal
		return out, nil
	}

	reed, err := s.GetReed(ctx, reedID)
	if err != nil {
		return out, err
	}
	out.Reed = reed
	return out, nil
}

// GetReedRemoval returns the stored reed-removal cert for reedID (canonical).
func (s *DataService) GetReedRemoval(ctx context.Context, reedID string) (*reedRemovalCert, error) {
	return getReedRemovalCert(ctx, s.db, reedID, s.serverID)
}

// InsertReedRemoval persists a reed-removal cert (idempotent / conflict).
func (s *DataService) InsertReedRemoval(ctx context.Context, cert reedRemovalCert) error {
	return insertReedRemovalCert(ctx, s.db, cert, s.serverID)
}

// GetAccountRemoval returns the stored account-removal cert for userID.
// userID arrives in userID@serverID form; getAccountRemovalCert's
// lookup param is bare, so decode before delegating.
func (s *DataService) GetAccountRemoval(ctx context.Context, userID string) (*accountRemovalCert, error) {
	bareUserID := userID
	if bare, _, ok := parseIdentityID(identityID(userID)); ok {
		bareUserID = bare
	}
	return getAccountRemovalCert(ctx, s.db, bareUserID, s.serverID)
}

// InsertAccountRemoval persists an account-removal cert (idempotent / conflict).
// cert.UserID arrives in userID@serverID form; insertAccountRemovalCert's
// cert.UserID is bare, so decode before delegating.
func (s *DataService) InsertAccountRemoval(ctx context.Context, cert accountRemovalCert) error {
	if bare, _, ok := parseIdentityID(identityID(cert.UserID)); ok {
		cert.UserID = bare
	}
	return insertAccountRemovalCert(ctx, s.db, cert, s.serverID)
}

// InsertForeignAccountRemoval persists an account-removal cert for a user
// this server doesn't host, told to us by a peer holding that author's
// content. cert.UserID is already the full canonical form.
func (s *DataService) InsertForeignAccountRemoval(ctx context.Context, cert accountRemovalCert) error {
	return insertForeignAccountRemovalCert(ctx, s.db, cert)
}

// GetForeignHolderServersForAuthor returns distinct peer server IDs known
// to hold a copy of any reed authored by userID — the set that needs to
// hear about an account removal for that author.
func (s *DataService) GetForeignHolderServersForAuthor(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT rsa.server_id
		FROM reeds r
		JOIN reed_server_allocations rsa ON rsa.reed_id = r.id
		WHERE r.user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var serverIDs []string
	for rows.Next() {
		var serverID string
		if err := rows.Scan(&serverID); err != nil {
			return nil, err
		}
		serverIDs = append(serverIDs, serverID)
	}
	return serverIDs, rows.Err()
}

// GetForeignHolderServersForReed returns distinct peer server IDs known to
// hold a copy of reedID — the set that needs to hear about that reed's
// removal. Unlike GetForeignHolderServersForAuthor (every reed by an
// author), this is scoped to one reed.
func (s *DataService) GetForeignHolderServersForReed(ctx context.Context, reedID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT server_id FROM reed_server_allocations WHERE reed_id = $1
	`, reedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var serverIDs []string
	for rows.Next() {
		var serverID string
		if err := rows.Scan(&serverID); err != nil {
			return nil, err
		}
		serverIDs = append(serverIDs, serverID)
	}
	return serverIDs, rows.Err()
}

// HasAccountRemoval reports whether userID has an account-removal row.
// userID arrives in userID@serverID form; hasAccountRemoval's
// lookup param is bare, so decode before delegating.
func (s *DataService) HasAccountRemoval(ctx context.Context, userID string) (bool, error) {
	bareUserID := userID
	if bare, _, ok := parseIdentityID(identityID(userID)); ok {
		bareUserID = bare
	}
	return hasAccountRemoval(ctx, s.db, bareUserID, s.serverID)
}

// ErrLikeConflict is returned when an existing like row differs from the
// cert being inserted (identical replay succeeds).
var ErrLikeConflict = errors.New("like conflict")

// GetReedLike returns the stored like cert for (likerID, reedID), or nil if
// the reed is not liked by that user. likerID arrives in userID@serverID
// form; loadLikeCertTx requires identityID, so this is a plain
// type conversion. reedID is canonical.
func (s *DataService) GetReedLike(ctx context.Context, likerID, reedID string) (*LikeCert, error) {
	likerIdentity := identityID(likerID)
	cert, err := s.loadLikeCertTx(ctx, s.db, likerIdentity, reedID, false)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cert, nil
}

// InsertReedLike stores a like cert once. Same signatures → no-op
// (idempotent replay); different signatures for the same (likerID, reedID)
// → ErrLikeConflict. likerID and likerFingerprint key the reeds_liked row;
// cert.ReedID is canonical.
func (s *DataService) InsertReedLike(ctx context.Context, likerID, likerFingerprint string, cert LikeCert) error {
	cert.ServerSignature.SignedAt = cert.ServerSignature.SignedAt.UTC().Truncate(time.Second)
	likerIdentity := identityID(likerID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing, err := s.loadLikeCertTx(ctx, tx, likerIdentity, cert.ReedID, true)
	switch {
	case err == sql.ErrNoRows:
		userSigID, err := insertUserSignature(ctx, tx, cert.UserSignature.ID, cert.UserSignature.Armor)
		if err != nil {
			return err
		}
		serverSigID, err := insertServerSignature(ctx, tx, cert.ServerSignature.ID, cert.ServerSignature.Armor, cert.ServerSignature.SignedAt)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reeds_liked (
				liker_user_id, reed_id, liker_public_key_id,
				user_signature_id, server_signature_id
			) VALUES ($1, $2, $3, $4, $5)
		`, likerIdentity, cert.ReedID, likerFingerprint, userSigID, serverSigID); err != nil {
			return fmt.Errorf("insert reed like: %w", err)
		}
	case err != nil:
		return err
	default:
		if existing.UserSignature.Armor != cert.UserSignature.Armor ||
			existing.UserSignature.ID != cert.UserSignature.ID ||
			existing.ServerSignature.Armor != cert.ServerSignature.Armor ||
			existing.ServerSignature.ID != cert.ServerSignature.ID ||
			!existing.ServerSignature.SignedAt.Equal(cert.ServerSignature.SignedAt) {
			return ErrLikeConflict
		}
	}

	return tx.Commit()
}

// DeleteReedLike hard-deletes the like row for (likerID, reedID) if present.
// Deleting a nonexistent row is a no-op, returning deleted=false with no
// error. likerID arrives in userID@serverID form already; reedID is canonical.
func (s *DataService) DeleteReedLike(ctx context.Context, likerID, reedID string) (deleted bool, err error) {
	likerIdentity := identityID(likerID)

	res, err := s.db.ExecContext(ctx, `
		DELETE FROM reeds_liked
		WHERE liker_user_id = $1 AND reed_id = $2
	`, likerIdentity, reedID)
	if err != nil {
		return false, fmt.Errorf("delete reed like: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// CountLikes returns the current like count for a reed. reedID is canonical.
func (s *DataService) CountLikes(ctx context.Context, reedID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(like_count, 0) FROM reed_stats WHERE reed_id = $1
	`, reedID).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return count, err
}

type likeQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// loadLikeCertTx takes likerID already in userID@serverID form — see
// callers (GetReedLike, InsertReedLike, DeleteReedLike), which convert
// once at their own boundary before calling in. reedID is canonical.
func (s *DataService) loadLikeCertTx(ctx context.Context, q likeQuerier, likerIdentity identityID, reedID string, forUpdate bool) (*LikeCert, error) {
	query := `
		SELECT liker_public_key_id, user_signature_id, server_signature_id
		FROM reeds_liked
		WHERE liker_user_id = $1 AND reed_id = $2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var likerFP string
	var userSigID, serverSigID int64
	err := q.QueryRowContext(ctx, query, likerIdentity, reedID).Scan(&likerFP, &userSigID, &serverSigID)
	if err != nil {
		return nil, err
	}

	// signing helpers need signingDBTX; *sql.DB and *sql.Tx both satisfy it.
	dbtx, ok := q.(signingDBTX)
	if !ok {
		return nil, fmt.Errorf("reed like load: querier is not signingDBTX")
	}
	userSig, err := getUserSignatureWire(ctx, dbtx, userSigID)
	if err != nil {
		return nil, err
	}
	serverSig, err := getServerSignatureWire(ctx, dbtx, serverSigID)
	if err != nil {
		return nil, err
	}
	authorID, ok := authorOf(identityID(reedID))
	if !ok {
		return nil, fmt.Errorf("malformed reed id: %s", reedID)
	}
	return &LikeCert{
		AuthorID:        string(authorID),
		ReedID:          reedID,
		UserSignature:   userSig,
		ServerSignature: serverSig,
	}, nil
}

// ==================== //
//   Account recovery   //
// ==================== //

// ListUserFollowing returns user ids this user follows. userID is the
// local, session-recovering account owner, already in userID@serverID
// form; the returned list holds that same form directly, no decode step.
func (s *DataService) ListUserFollowing(ctx context.Context, userID string) ([]string, error) {
	selfIdentity := userID
	rows, err := s.db.QueryContext(ctx, `
		SELECT following_user_id
		FROM user_following
		WHERE user_id = $1
		ORDER BY following_user_id
	`, selfIdentity)
	if err != nil {
		return nil, fmt.Errorf("list following: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListUserReeds returns non-removed reed ids for userID, tip first. userID
// is the same local, session-recovering account owner as ListUserFollowing,
// already in userID@serverID form.
func (s *DataService) ListUserReeds(ctx context.Context, userID string) (tipReedID *string, reedIDs []string, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id
		FROM reeds r
		WHERE r.user_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.reed_id = r.id
		  )
		ORDER BY r.signed_at DESC, r.id DESC
	`, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("list own reeds: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, nil, err
		}
		reedIDs = append(reedIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(reedIDs) == 0 {
		return nil, nil, nil
	}
	first := reedIDs[0]
	tipReedID = &first
	return tipReedID, reedIDs, nil
}

// ================== //
//   LoggingService   //
// ================== //

type LoggingService struct {
}

func NewLoggingService() *LoggingService {
	return &LoggingService{}
}

// GetLogger creates a logger with request context including request ID
func (s *LoggingService) GetLogger(ctx context.Context) *zerolog.Logger {
	logger := log.Logger

	// Add request ID if available in context
	if requestID, ok := ctx.Value("request_id").(string); ok {
		logger = logger.With().Str("request_id", requestID).Logger()
	}

	return &logger
}

// =================== //
//   MarkdownService   //
// =================== //

// Caps mirror spa/src/lib/utils/reedContent.ts.
const (
	MaxReedVisibleChars = 140
	MaxReedRawChars     = 1400
)

var (
	reLink    = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	reFence   = regexp.MustCompile("(?s)```[^\\n]*\\n?(.*?)\\n```")
	reCode    = regexp.MustCompile("`([^`]+)`")
	reStrike  = regexp.MustCompile(`~([^~]+)~`)
	reItalic  = regexp.MustCompile(`_([^_]+)_`)
	reBold    = regexp.MustCompile(`\*([^*]+)\*`)
	reHashtag = regexp.MustCompile(`(^|\s)#(\S)`)
)

// CountMarkdownCharacters strips formatting syntax before counting runes
// (aligned with the SPA visible-character budget).
func CountMarkdownCharacters(text string) int {
	if text == "" {
		return 0
	}
	result := text
	result = reLink.ReplaceAllString(result, "$1")
	result = reFence.ReplaceAllString(result, "$1")
	result = reCode.ReplaceAllString(result, "$1")
	result = reStrike.ReplaceAllString(result, "$1")
	result = reItalic.ReplaceAllString(result, "$1")
	result = reBold.ReplaceAllString(result, "$1")
	result = reHashtag.ReplaceAllString(result, "$1$2")
	return utf8.RuneCountInString(result)
}

// ================= //
//   DeviceService   //
// ================= //

var (
	errDeviceMismatch = errors.New("device mismatch")
)

// GetActiveDeviceID's userID arrives in userID@serverID form already.
// (BindDeviceTx, Signup's own device-binding helper, is unaffected — it
// still takes the bare in.UserID Signup itself uses.)
func (s *DataService) GetActiveDeviceID(ctx context.Context, userID string) (string, error) {
	selfIdentity := userID
	var deviceID string
	err := s.db.QueryRowContext(ctx, `
		SELECT device_id FROM user_devices
		WHERE user_id = $1 AND revoked_at IS NULL
	`, selfIdentity).Scan(&deviceID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return deviceID, nil
}

// BindDeviceTx takes a bare local userID and converts to identities.id
// form internally before touching user_devices, which FKs to identities(id).
func (s *DataService) BindDeviceTx(ctx context.Context, tx *sql.Tx, userID, deviceID string, now time.Time) error {
	deviceID, err := parseDeviceID(deviceID)
	if err != nil {
		return err
	}
	selfIdentity := canonicalID(s.serverID, userID)

	if _, err := tx.ExecContext(ctx, `
		UPDATE user_devices SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL
	`, selfIdentity, now); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_devices (user_id, device_id, linked_at, revoked_at)
		VALUES ($1, $2, $3, NULL)
	`, selfIdentity, deviceID, now); err != nil {
		return err
	}

	return nil
}

// BindDevice's userID arrives in userID@serverID form already. It calls
// BindDeviceTx, which composes internally (canonicalID) — decode
// back to bare first via parseIdentityID to avoid double-composing,
// matching what BindDeviceTx expects from its other (Signup) caller.
func (s *DataService) BindDevice(ctx context.Context, userID, deviceID string, now time.Time) error {
	bareUserID := userID
	if bare, _, ok := parseIdentityID(identityID(userID)); ok {
		bareUserID = bare
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.BindDeviceTx(ctx, tx, bareUserID, deviceID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// CheckActiveDevice's userID arrives in userID@serverID form already (see
// GetActiveDeviceID's comment above).
func (s *DataService) CheckActiveDevice(ctx context.Context, userID, presented string) error {
	presented, err := parseDeviceID(presented)
	if err != nil {
		return err
	}
	active, err := s.GetActiveDeviceID(ctx, userID)
	if err != nil {
		return err
	}
	if active == "" || active != presented {
		return errDeviceMismatch
	}
	return nil
}

// ===================== //
//   FederationService   //
// ===================== //

// federationInvitation.status transitions:
//
//	new -> accepted        (responder's /connect callback verifies)
//	new -> canceled        (revoked before anyone redeemed it)
//	accepted -> approved   (second local admin approves — see 03)
//	accepted -> rejected   (second local admin rejects — see 03)
//	approved -> revoked    (an established connection is torn down — see 05)
const (
	federationStatusNew      = "new"
	federationStatusAccepted = "accepted"
	federationStatusApproved = "approved"
	federationStatusRejected = "rejected"
	federationStatusCanceled = "canceled"
	federationStatusRevoked  = "revoked"
)

var (
	errFederationInvitationNotFound     = errors.New("federation invitation not found")
	errFederationInvitationNotRevocable = errors.New("federation invitation cannot be revoked")
	errFederationInvitationExists       = errors.New("federation invitation already exists")
	errFederationInvitationNotNew       = errors.New("federation invitation is not new")
)

type federationInvitation struct {
	ID          string
	Name        string
	SecretHash  []byte
	Fingerprint string
	PublicKey   string
	CreatedBy   string
	Status      string
	CreatedAt   time.Time
	AcceptedAt  *time.Time
	ServerID    string
}

// federationServerListRow is a peer server row as seen from this server's
// side. servers rows only exist once a federation_attempt has been
// APPROVED (see ApproveFederationAttempt) — connected is always TRUE for
// any row this query returns; kept as a column rather than assumed so a
// future de-establish/revoke step (specs/federation/05) has somewhere to
// flip it without a schema change.
// No fingerprint field: peer.Fingerprint is never persisted to servers.signing_key
// (that column means this server's OWN signing key, joined against
// private_keys — see InitServerKey/GetServerSigningKeyArmor) or anywhere
// else queryable today.
type federationServerListRow struct {
	ID                    string
	Name                  string
	BaseURL               string
	Connected             bool
	CreatedAt             time.Time
	Revoked               bool
	RevokedAt             *time.Time
	RevokedBy             string
	RevokedReason         string
	DisconnectPending     bool
	DisconnectRequestedAt *time.Time
	DisconnectRequestedBy string
	DisconnectReason      string
}

// ListFederationServers returns all peer servers, revoked or not (self excluded).
func (s *DataService) ListFederationServers(ctx context.Context) ([]federationServerListRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(base_url, ''), connected, created_at,
			revoked_at, COALESCE(revoked_by, ''), COALESCE(revoked_reason, ''),
			disconnect_requested_at, COALESCE(disconnect_requested_by, ''),
			COALESCE(disconnect_reason, '')
		FROM servers
		WHERE self = FALSE
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []federationServerListRow
	for rows.Next() {
		var row federationServerListRow
		var revokedAt, disconnectRequestedAt sql.NullTime
		if err := rows.Scan(&row.ID, &row.Name, &row.BaseURL, &row.Connected, &row.CreatedAt,
			&revokedAt, &row.RevokedBy, &row.RevokedReason,
			&disconnectRequestedAt, &row.DisconnectRequestedBy,
			&row.DisconnectReason); err != nil {
			return nil, err
		}
		if revokedAt.Valid {
			t := revokedAt.Time.UTC()
			row.RevokedAt = &t
			row.Revoked = true
		}
		if disconnectRequestedAt.Valid {
			t := disconnectRequestedAt.Time.UTC()
			row.DisconnectRequestedAt = &t
			row.DisconnectPending = true
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// VerifyFederationPeer is the runtime trust check for peer-authenticated
// requests (specs/federation/04): serverID must be an established
// (self=FALSE), non-revoked peer, and the caller's claimed fingerprint must
// match the one pinned at approval (see ApproveFederationAttempt, which
// points servers.key_id at the promoted public_keys row). On success also
// returns that row's armor, so callers can verify the request signature
// without a live fetch to the peer. Returns ok=false — not an error — for
// "not peered," "revoked," or "fingerprint doesn't match": callers should
// respond 401 in all three cases without distinguishing why (don't help an
// attacker enumerate which check failed).
func (s *DataService) VerifyFederationPeer(ctx context.Context, serverID, fingerprint string) (ok bool, armor string, err error) {
	var pinnedKeyID, keyArmor sql.NullString
	var revokedAt sql.NullTime
	err = s.db.QueryRowContext(ctx, `
		SELECT sv.key_id, sv.revoked_at, pk.armor
		FROM servers sv
		LEFT JOIN public_keys pk ON pk.id = sv.key_id
		WHERE sv.id = $1 AND sv.self = FALSE
	`, serverID).Scan(&pinnedKeyID, &revokedAt, &keyArmor)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	keyID := string(canonicalID(serverID, fingerprint))
	if revokedAt.Valid || !pinnedKeyID.Valid || pinnedKeyID.String != keyID || !keyArmor.Valid {
		return false, "", nil
	}
	return true, keyArmor.String, nil
}

// PeerServer is a known, approved, non-revoked federation peer, resolved
// for the profile/key proxy (handlers.go's proxyToPeer). serverID must
// have a servers row created by ApproveFederationAttempt — this is
// deliberately NOT federation_attempt/federation_invitation, which are
// pre-approval staging tables.
type PeerServer struct {
	ID      string
	BaseURL string
}

// GetServerByID resolves an approved peer's base URL for proxying, or nil
// if serverID is unknown, not yet approved, or revoked — the caller's
// signal to 404 rather than proxy.
func (s *DataService) GetServerByID(ctx context.Context, serverID string) (*PeerServer, error) {
	var peer PeerServer
	err := s.db.QueryRowContext(ctx, `
		SELECT id, base_url FROM servers WHERE id = $1 AND self = FALSE AND revoked_at IS NULL
	`, serverID).Scan(&peer.ID, &peer.BaseURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &peer, nil
}

// GetServerBaseURLAnyState resolves a peer's base URL regardless of
// revoked state — unlike GetServerByID, which deliberately excludes
// revoked peers as the outbound trust gate. Used only to reach a peer
// we've just revoked, to tell it we're disconnecting.
func (s *DataService) GetServerBaseURLAnyState(ctx context.Context, serverID string) (string, error) {
	var baseURL string
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(base_url, '') FROM servers WHERE id = $1 AND self = FALSE
	`, serverID).Scan(&baseURL)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return baseURL, nil
}

// ListConnectedPeers returns every approved, non-revoked, currently
// connected peer with a base URL — the fanout target list for cross-server
// user search. Excludes revoked or never-connected/disconnected peers,
// unlike ListFederationServers (the admin Mesh UI's broader listing, which
// includes those so an operator can see them).
func (s *DataService) ListConnectedPeers(ctx context.Context) ([]PeerServer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, base_url FROM servers
		WHERE self = FALSE AND revoked_at IS NULL AND connected = TRUE
		  AND base_url IS NOT NULL AND base_url != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var peers []PeerServer
	for rows.Next() {
		var p PeerServer
		if err := rows.Scan(&p.ID, &p.BaseURL); err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	return peers, rows.Err()
}

// UpsertRemoteIdentity records a minimal identities row for a foreign user
// after a successful proxied profile/info fetch, so a later FollowUser has
// something to reference. Idempotent.
func (s *DataService) UpsertRemoteIdentity(ctx context.Context, canonicalID, remoteServerID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO identities (id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, canonicalID, remoteServerID)
	return err
}

// UpsertReedIdentity idempotently records that reedID exists, without
// claiming its content has been verified — the same low bar a locally
// authored reed clears just by being signed (see insertReedCoreTx). Used
// when this server needs to reference a foreign reed (e.g. mirroring a
// like) without holding a copy of the reed itself.
func (s *DataService) UpsertReedIdentity(ctx context.Context, reedID string) error {
	_, serverID, _, ok := parseKeyFingerprint(identityID(reedID))
	if !ok {
		return fmt.Errorf("malformed reed id: %s", reedID)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reed_identities (id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, reedID, serverID)
	return err
}

// CachePeerUserKey persists a foreign user key this server fetched and
// verified live from its owning peer (see Handlers.fetchAndCachePeerUserKey)
// so a future peer-relayed like/ripple from the same user doesn't need
// another round trip. Caller is responsible for verifying sig before
// calling this — it trusts everything passed in. Idempotent: a second
// fetch of the same key (e.g. a racing concurrent like) is a harmless
// no-op, matching UpsertRemoteIdentity's own ON CONFLICT DO NOTHING bar.
func (s *DataService) CachePeerUserKey(ctx context.Context, keyID, ownerCanonicalID, ownerServerID, armor string, serverSig ServerSignature) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO identities (id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, ownerCanonicalID, ownerServerID); err != nil {
		return err
	}

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public_keys WHERE id = $1)`, keyID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit()
	}

	serverSignatureID, err := insertServerSignature(ctx, tx, serverSig.ID, serverSig.Armor, serverSig.SignedAt)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO public_keys (id, owner, armor, server_signature_id) VALUES ($1, $2, $3, $4)`,
		keyID, ownerCanonicalID, armor, serverSignatureID,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// errFederationAttemptNotFound is returned by ApproveFederationAttempt and
// RejectFederationAttempt when attemptID doesn't match any row, and by
// GetFederationAttempt.
var errFederationAttemptNotFound = errors.New("federation attempt not found")

// errFederationAttemptNotPending is returned by ApproveFederationAttempt and
// RejectFederationAttempt when the attempt has already been decided —
// approve/reject are one-shot, not idempotent re-decisions.
var errFederationAttemptNotPending = errors.New("federation attempt is not pending")

// errFederationServerNotFound is returned by RevokeFederationServer and
// PurgeFederationServer when serverID doesn't match any peer row.
var errFederationServerNotFound = errors.New("federation server not found")

// errFederationServerAlreadyRevoked is returned by RevokeFederationServer
// when the peer is already revoked — revoke is one-shot, not idempotent.
var errFederationServerAlreadyRevoked = errors.New("federation server already revoked")

// errFederationServerNotRevoked is returned by PurgeFederationServer when
// the peer hasn't been revoked yet — a purge must be preceded by revoke.
var errFederationServerNotRevoked = errors.New("federation server must be disconnected before it can be deleted")

// errFederationSameApprover is returned when the acting admin is the same
// one who initiated the action (invite creator approving, or requester
// confirming their own disconnect). Root is exempt from both checks.
var errFederationSameApprover = errors.New("a different admin must approve this")

// errFederationDisconnectAlreadyRequested is returned by
// RequestFederationServerDisconnect when a disconnect request is already
// pending for this peer.
var errFederationDisconnectAlreadyRequested = errors.New("disconnect already requested for this server")

// errFederationDisconnectNotRequested is returned by
// ConfirmFederationServerDisconnect when no disconnect request is pending
// for this peer.
var errFederationDisconnectNotRequested = errors.New("no disconnect request is pending for this server")

type federationAttemptRow struct {
	ID               string
	RemoteServerID   string
	RemoteServerName string
	BaseURL          string
	Fingerprint      string
	InvitationID     string
	ServerID         string
	CreatedAt        time.Time
	Status           string
	ApprovedBy       string
	ApprovedAt       *time.Time
	RejectedBy       string
	RejectedAt       *time.Time
	RejectedReason   string
}

const federationAttemptSelectCols = `
	fa.id, fa.remote_server_id, fa.remote_server_name, fa.base_url, fa.fingerprint,
	COALESCE(fa.invitation_id, ''), COALESCE(fa.server_id, ''), fa.created_at, fa.status,
	COALESCE(fa.approved_by, ''), fa.approved_at,
	COALESCE(fa.rejected_by, ''), fa.rejected_at,
	COALESCE(fa.rejected_reason, '')
`

const federationAttemptFromJoin = `
	FROM federation_attempt fa
`

func scanFederationAttemptRow(scanner interface {
	Scan(dest ...any) error
}) (federationAttemptRow, error) {
	var row federationAttemptRow
	var approvedAt, rejectedAt sql.NullTime
	err := scanner.Scan(
		&row.ID, &row.RemoteServerID, &row.RemoteServerName, &row.BaseURL, &row.Fingerprint,
		&row.InvitationID, &row.ServerID, &row.CreatedAt, &row.Status,
		&row.ApprovedBy, &approvedAt,
		&row.RejectedBy, &rejectedAt,
		&row.RejectedReason,
	)
	if err != nil {
		return federationAttemptRow{}, err
	}
	if approvedAt.Valid {
		t := approvedAt.Time.UTC()
		row.ApprovedAt = &t
	}
	if rejectedAt.Valid {
		t := rejectedAt.Time.UTC()
		row.RejectedAt = &t
	}
	return row, nil
}

// ListFederationAttempts returns pending and rejected attempts, newest
// first. Approved attempts are excluded — once approved, a servers row
// exists (see ApproveFederationAttempt) and that's what the mesh list
// shows instead; the attempt row itself is still kept forever for its
// audit trail (see GetFederationAttemptForServer).
func (s *DataService) ListFederationAttempts(ctx context.Context) ([]federationAttemptRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+federationAttemptSelectCols+federationAttemptFromJoin+`
		WHERE fa.status != 'approved'
		ORDER BY fa.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []federationAttemptRow
	for rows.Next() {
		row, err := scanFederationAttemptRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetFederationAttempt returns nil, not an error, if attemptID doesn't exist.
func (s *DataService) GetFederationAttempt(ctx context.Context, attemptID string) (*federationAttemptRow, error) {
	row, err := scanFederationAttemptRow(s.db.QueryRowContext(ctx, `
		SELECT `+federationAttemptSelectCols+federationAttemptFromJoin+`
		WHERE fa.id = $1
	`, attemptID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ApproveFederationAttempt is the ONLY place a servers row is created — a
// peer only becomes a real, addressable server once a second admin
// approves the attempt that verified the handshake. Promotes the peer's
// key (received and stored on federation_attempt during the handshake)
// into public_keys — owner NULL (not a local identity), countersigned by
// this server the same way any other public_keys row is, binding "we
// received and approved this exact key during a verified handshake."
// Creates servers (connected=TRUE from the start, since approval IS the
// confirmation now — contrast the old model where a server row existed
// pre-approval with connected=FALSE) pointing key_id at the promoted row,
// backfills federation_attempt.server_id and, if this attempt has a local
// invitation (initiator side), federation_invitation's server_id and
// status too. callerIsRoot bypasses the different-admin check.
func (s *DataService) ApproveFederationAttempt(
	ctx context.Context,
	attemptID, approvedBy string,
	approvedAt time.Time,
	callerIsRoot bool,
	countersign func(payload []byte, ts time.Time) (ServerSignature, error),
) (serverID string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var remoteServerID, remoteServerName, baseURL, fingerprint, publicKeyArmor, invitationID string
	var status string
	if err := tx.QueryRowContext(ctx, `
		SELECT remote_server_id, remote_server_name, base_url, fingerprint, public_key_armor,
			COALESCE(invitation_id, ''), status
		FROM federation_attempt WHERE id = $1 FOR UPDATE
	`, attemptID).Scan(&remoteServerID, &remoteServerName, &baseURL, &fingerprint, &publicKeyArmor, &invitationID, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errFederationAttemptNotFound
		}
		return "", err
	}
	if status != "pending" {
		return "", errFederationAttemptNotPending
	}
	// Only the initiator side has a local federation_invitation row to
	// compare created_by against — the responder side has no local
	// "creator" to restrict against, so any local admin may approve it.
	if !callerIsRoot && invitationID != "" {
		var invitationCreatedBy string
		if err := tx.QueryRowContext(ctx, `
			SELECT created_by FROM federation_invitation WHERE id = $1
		`, invitationID).Scan(&invitationCreatedBy); err != nil {
			return "", err
		}
		if invitationCreatedBy == approvedBy {
			return "", errFederationSameApprover
		}
	}

	// keyID pins the trust root for peer-authenticated runtime requests
	// (specs/federation/04) — same canonical shape as any other key id.
	// ON CONFLICT DO NOTHING: re-approving after a revoke/reconnect with
	// the same key hits the same row; a key's armor never changes once set.
	keyID := string(canonicalID(remoteServerID, fingerprint))
	keyPayload := buildPublicKeyPayload(
		s.serverID, keyID, keyID, fingerprint, publicKeyArmor, approvedAt.UTC(),
	)
	serverSig, err := countersign(keyPayload, approvedAt.UTC())
	if err != nil {
		return "", fmt.Errorf("countersign peer key: %w", err)
	}
	serverSignatureID, err := insertServerSignature(ctx, tx, serverSig.ID, serverSig.Armor, serverSig.SignedAt)
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public_keys (id, owner, armor, created_at, server_signature_id)
		VALUES ($1, NULL, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING
	`, keyID, publicKeyArmor, approvedAt.UTC(), serverSignatureID); err != nil {
		return "", fmt.Errorf("promote peer key: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO servers (id, name, self, base_url, connected, key_id, created_at)
		VALUES ($1, $2, FALSE, $3, TRUE, $4, $5)
		ON CONFLICT (id) DO UPDATE SET base_url = EXCLUDED.base_url, name = EXCLUDED.name,
			connected = TRUE, key_id = EXCLUDED.key_id,
			revoked_at = NULL, revoked_by = NULL, revoked_reason = NULL
	`, remoteServerID, remoteServerName, baseURL, keyID, approvedAt.UTC()); err != nil {
		return "", fmt.Errorf("insert federation peer: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE federation_attempt
		SET status = 'approved', server_id = $2, approved_by = $3, approved_at = $4
		WHERE id = $1
	`, attemptID, remoteServerID, approvedBy, approvedAt.UTC()); err != nil {
		return "", fmt.Errorf("update federation attempt: %w", err)
	}

	if invitationID != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE federation_invitation
			SET status = $2, server_id = $3
			WHERE id = $1
		`, invitationID, federationStatusApproved, remoteServerID); err != nil {
			return "", fmt.Errorf("update federation invitation: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return remoteServerID, nil
}

// RevokeFederationServer disconnects a peer and clears any pending
// disconnect-request staging. revokedBy is nil when the peer notified us
// first. Called by the peer-notify path and ConfirmFederationServerDisconnect.
func (s *DataService) RevokeFederationServer(ctx context.Context, serverID string, revokedBy *string, reason string, revokedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE servers
		SET revoked_at = $2, revoked_by = $3, revoked_reason = $4,
			disconnect_requested_at = NULL, disconnect_requested_by = NULL, disconnect_reason = NULL
		WHERE id = $1 AND self = FALSE AND revoked_at IS NULL
	`, serverID, revokedAt.UTC(), revokedBy, reason)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM servers WHERE id = $1 AND self = FALSE)
		`, serverID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errFederationServerNotFound
		}
		return errFederationServerAlreadyRevoked
	}
	return nil
}

// RequestFederationServerDisconnect stages a disconnect — the peer stays
// trusted until a second admin calls ConfirmFederationServerDisconnect.
// The request itself is the same for every role, root included.
func (s *DataService) RequestFederationServerDisconnect(ctx context.Context, serverID, requestedBy, reason string, requestedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE servers
		SET disconnect_requested_at = $2, disconnect_requested_by = $3, disconnect_reason = $4
		WHERE id = $1 AND self = FALSE AND revoked_at IS NULL AND disconnect_requested_at IS NULL
	`, serverID, requestedAt.UTC(), requestedBy, reason)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var revoked, pending bool
		if err := s.db.QueryRowContext(ctx, `
			SELECT revoked_at IS NOT NULL, disconnect_requested_at IS NOT NULL
			FROM servers WHERE id = $1 AND self = FALSE
		`, serverID).Scan(&revoked, &pending); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errFederationServerNotFound
			}
			return err
		}
		if revoked {
			return errFederationServerAlreadyRevoked
		}
		return errFederationDisconnectAlreadyRequested
	}
	return nil
}

// ConfirmFederationServerDisconnect finalizes a staged disconnect,
// requiring confirmedBy to differ from the requester (root exempt). Returns
// the staged reason so the caller can pass it to the peer notification.
func (s *DataService) ConfirmFederationServerDisconnect(ctx context.Context, serverID, confirmedBy string, confirmedAt time.Time, callerIsRoot bool) (reason string, err error) {
	var requestedBy string
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(disconnect_requested_by, ''), COALESCE(disconnect_reason, '')
		FROM servers WHERE id = $1 AND self = FALSE
	`, serverID).Scan(&requestedBy, &reason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errFederationServerNotFound
		}
		return "", err
	}
	if requestedBy == "" {
		return "", errFederationDisconnectNotRequested
	}
	if !callerIsRoot && requestedBy == confirmedBy {
		return "", errFederationSameApprover
	}
	if err := s.RevokeFederationServer(ctx, serverID, &confirmedBy, reason, confirmedAt); err != nil {
		return "", err
	}
	return reason, nil
}

// CancelFederationServerDisconnect clears a staged disconnect request
// without revoking anything — lets an admin back out before a second admin
// confirms.
func (s *DataService) CancelFederationServerDisconnect(ctx context.Context, serverID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE servers
		SET disconnect_requested_at = NULL, disconnect_requested_by = NULL, disconnect_reason = NULL
		WHERE id = $1 AND self = FALSE AND disconnect_requested_at IS NOT NULL
	`, serverID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errFederationDisconnectNotRequested
	}
	return nil
}

// PurgeFederationServer permanently deletes a revoked peer's row and
// every identity/reed it owns — everything cascades from
// reed_identities/identities, which cascade from servers itself.
// federation_attempt/federation_invitation/federation_log rows referencing
// this server are untouched (audit trail, kept forever like account
// removals keep their own record).
func (s *DataService) PurgeFederationServer(ctx context.Context, serverID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var revokedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT revoked_at FROM servers WHERE id = $1 AND self = FALSE FOR UPDATE
	`, serverID).Scan(&revokedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errFederationServerNotFound
		}
		return err
	}
	if !revokedAt.Valid {
		return errFederationServerNotRevoked
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM reed_identities WHERE server_id = $1`, serverID); err != nil {
		return fmt.Errorf("purge reed_identities: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM identities WHERE server_id = $1`, serverID); err != nil {
		return fmt.Errorf("purge identities: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM servers WHERE id = $1`, serverID); err != nil {
		return fmt.Errorf("purge servers row: %w", err)
	}

	return tx.Commit()
}

// RejectFederationAttempt sets status=rejected with reason — the attempt
// row is never deleted, so the log lines already written against it (see
// logFederationAttempt) stay intact, unlike the earlier servers-row-based
// design where rejecting cascade-deleted its own log.
func (s *DataService) RejectFederationAttempt(ctx context.Context, attemptID, rejectedBy, reason string, rejectedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE federation_attempt
		SET status = 'rejected', rejected_by = $2, rejected_at = $3, rejected_reason = $4
		WHERE id = $1 AND status = 'pending'
	`, attemptID, rejectedBy, rejectedAt.UTC(), reason)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		attempt, err := s.GetFederationAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		if attempt == nil {
			return errFederationAttemptNotFound
		}
		return errFederationAttemptNotPending
	}
	return nil
}

type federationServerLogRow struct {
	ID        string
	Level     string
	Message   string
	CreatedAt time.Time
}

// listFederationLog reads federation_log lines through one of its three
// junction tables — federation_server_log (junctionTable="federation_server_log",
// junctionCol="server_id"), federation_invitation_log
// (junctionCol="invitation_id"), or federation_attempt_log
// (junctionCol="attempt_id") — see logFederationServer/
// logFederationInvitation/logFederationAttempt's doc comments for which
// handshake steps write to which.
func (s *DataService) listFederationLog(ctx context.Context, junctionTable, junctionCol, id string) ([]federationServerLogRow, error) {
	query := fmt.Sprintf(`
		SELECT fl.id, fl.level, fl.message, fl.created_at
		FROM %s j
		JOIN federation_log fl ON fl.id = j.log_id
		WHERE j.%s = $1
		ORDER BY fl.created_at ASC
	`, junctionTable, junctionCol)
	rows, err := s.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []federationServerLogRow
	for rows.Next() {
		var row federationServerLogRow
		if err := rows.Scan(&row.ID, &row.Level, &row.Message, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *DataService) ListFederationServerLogs(ctx context.Context, serverID string) ([]federationServerLogRow, error) {
	return s.listFederationLog(ctx, "federation_server_log", "server_id", serverID)
}

func (s *DataService) ListFederationInvitationLogs(ctx context.Context, invitationID string) ([]federationServerLogRow, error) {
	return s.listFederationLog(ctx, "federation_invitation_log", "invitation_id", invitationID)
}

func (s *DataService) ListFederationAttemptLogs(ctx context.Context, attemptID string) ([]federationServerLogRow, error) {
	return s.listFederationLog(ctx, "federation_attempt_log", "attempt_id", attemptID)
}

type federationInvitationListRow struct {
	ID                   string
	Name                 string
	Status               string
	CreatedBy            string
	Fingerprint          string
	CreatedAt            time.Time
	AcceptedAt           *time.Time
	ServerID             string
	ReviewedBy           string
	ReviewedAt           *time.Time
	ConnectionCiphertext string
}

// InsertFederationInvitation inserts the invitation row. fingerprint and
// publicKey are the peer's claimed key, supplied out-of-band by the admin
// — unverified bootstrap material that stays on this row (see db.go's
// federation_invitation schema comment), not in public_keys, until
// approval promotes it. createdBy arrives in userID@serverID form already.
func (s *DataService) InsertFederationInvitation(
	ctx context.Context,
	id, name, createdBy, fingerprint, publicKey string,
	secretHash []byte,
	connectionCiphertext string,
	createdAt time.Time,
) error {
	createdByIdentity := createdBy

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_invitation (
			id, name, secret_hash, fingerprint, public_key_armor, created_by,
			status, created_at, connection_ciphertext
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, id, name, secretHash, fingerprint, publicKey, createdByIdentity, federationStatusNew, createdAt.UTC(), connectionCiphertext); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return errFederationInvitationExists
		}
		return err
	}

	return tx.Commit()
}

// GetFederationInvitation scans created_by directly; federationInvitation.
// CreatedBy (a wire-facing field) holds that same value, no decode step.
func (s *DataService) GetFederationInvitation(ctx context.Context, id string) (*federationInvitation, error) {
	var inv federationInvitation
	var createdBy string
	var acceptedAt sql.NullTime
	var serverID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT fi.id, fi.name, fi.secret_hash, fi.fingerprint, fi.public_key_armor, fi.created_by, fi.status,
		       fi.created_at, fi.accepted_at, fi.server_id
		FROM federation_invitation fi
		WHERE fi.id = $1
	`, id).Scan(
		&inv.ID,
		&inv.Name,
		&inv.SecretHash,
		&inv.Fingerprint,
		&inv.PublicKey,
		&createdBy,
		&inv.Status,
		&inv.CreatedAt,
		&acceptedAt,
		&serverID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	inv.CreatedBy = createdBy
	if acceptedAt.Valid {
		t := acceptedAt.Time.UTC()
		inv.AcceptedAt = &t
	}
	if serverID.Valid {
		inv.ServerID = serverID.String
	}
	return &inv, nil
}

// ListFederationInvitations excludes accepted/approved invitations —
// once accepted, an invitation can no longer change state (it's a
// finished handshake, live or not), so it moves to living under the
// resulting server's own page (see ListFederationInvitationForServer)
// instead of cluttering the pending-invite list forever.
func (s *DataService) ListFederationInvitations(ctx context.Context) ([]federationInvitationListRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, status, created_by,
		       fingerprint, created_at, accepted_at, COALESCE(server_id, ''),
		       COALESCE(reviewed_by, ''), reviewed_at,
		       COALESCE(connection_ciphertext, '')
		FROM federation_invitation
		WHERE status NOT IN ($1, $2)
		ORDER BY created_at DESC
	`, federationStatusAccepted, federationStatusApproved)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []federationInvitationListRow
	for rows.Next() {
		var row federationInvitationListRow
		var acceptedAt, reviewedAt sql.NullTime
		if err := rows.Scan(
			&row.ID,
			&row.Name,
			&row.Status,
			&row.CreatedBy,
			&row.Fingerprint,
			&row.CreatedAt,
			&acceptedAt,
			&row.ServerID,
			&row.ReviewedBy,
			&reviewedAt,
			&row.ConnectionCiphertext,
		); err != nil {
			return nil, err
		}
		if acceptedAt.Valid {
			t := acceptedAt.Time.UTC()
			row.AcceptedAt = &t
		}
		if reviewedAt.Valid {
			t := reviewedAt.Time.UTC()
			row.ReviewedAt = &t
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetFederationInvitationForServer returns the (accepted/approved)
// invitation that produced serverID, or nil if this server was the
// responder — the responder never has a local invitation row for a
// connection it accepted (see OutgoingFederationAttempt's doc comment), so
// nil is the expected, non-error result there, not a lookup failure.
func (s *DataService) GetFederationInvitationForServer(ctx context.Context, serverID string) (*federationInvitationListRow, error) {
	var row federationInvitationListRow
	var acceptedAt, reviewedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, status, created_by,
		       fingerprint, created_at, accepted_at, COALESCE(server_id, ''),
		       COALESCE(reviewed_by, ''), reviewed_at,
		       COALESCE(connection_ciphertext, '')
		FROM federation_invitation
		WHERE server_id = $1
	`, serverID).Scan(
		&row.ID,
		&row.Name,
		&row.Status,
		&row.CreatedBy,
		&row.Fingerprint,
		&row.CreatedAt,
		&acceptedAt,
		&row.ServerID,
		&row.ReviewedBy,
		&reviewedAt,
		&row.ConnectionCiphertext,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if acceptedAt.Valid {
		t := acceptedAt.Time.UTC()
		row.AcceptedAt = &t
	}
	if reviewedAt.Valid {
		t := reviewedAt.Time.UTC()
		row.ReviewedAt = &t
	}
	return &row, nil
}

// GetFederationAttemptForServer returns the approved attempt that produced
// serverID, or nil if none is found (shouldn't happen for a real servers
// row, since ApproveFederationAttempt always backfills federation_attempt.
// server_id, but the caller treats it as "no attempt info" rather than an
// error either way).
func (s *DataService) GetFederationAttemptForServer(ctx context.Context, serverID string) (*federationAttemptRow, error) {
	row, err := scanFederationAttemptRow(s.db.QueryRowContext(ctx, `
		SELECT `+federationAttemptSelectCols+federationAttemptFromJoin+`
		WHERE fa.server_id = $1
	`, serverID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// RevokeFederationInvitation's reviewedBy is the local admin revoking the
// invitation, already in userID@serverID form.
func (s *DataService) RevokeFederationInvitation(ctx context.Context, id, reviewedBy string, reviewedAt time.Time) error {
	reviewedByIdentity := reviewedBy
	res, err := s.db.ExecContext(ctx, `
		UPDATE federation_invitation
		SET status = $2, connection_ciphertext = NULL,
		    reviewed_by = $4, reviewed_at = $5
		WHERE id = $1 AND status = $3
	`, id, federationStatusCanceled, federationStatusNew, reviewedByIdentity, reviewedAt.UTC())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		inv, err := s.GetFederationInvitation(ctx, id)
		if err != nil {
			return err
		}
		if inv == nil {
			return errFederationInvitationNotFound
		}
		return errFederationInvitationNotRevocable
	}
	return nil
}

// federationPeer describes the remote server on the other end of a
// handshake, as claimed in its handshake payload — captured onto
// federation_attempt, not servers (which doesn't get a row until the
// attempt is approved — see ApproveFederationAttempt, which promotes
// PublicKeyArmor into public_keys). Fingerprint is the peer's pinned
// trust root.
type federationPeer struct {
	ServerID       string
	ServerName     string
	BaseURL        string
	Fingerprint    string
	PublicKeyArmor string
}

// CreateFederationAttempt runs on the RESPONDER, before it even attempts
// the handshake: it inserts a pending federation_attempt row
// (invitation_id NULL — the responder never has a local invitation row)
// so there's somewhere to log against from the first moment, rather than
// only writing anything once the handshake already succeeded. Returns the
// new attempt id.
func (s *DataService) CreateFederationAttempt(ctx context.Context, peer federationPeer, createdAt time.Time) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	name := peer.ServerName
	if name == "" {
		name = peer.ServerID
	}
	attemptID, err := newCryptoID()
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_attempt (id, remote_server_id, remote_server_name, base_url, fingerprint, public_key_armor, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, attemptID, peer.ServerID, name, peer.BaseURL, peer.Fingerprint, peer.PublicKeyArmor, createdAt.UTC()); err != nil {
		return "", fmt.Errorf("insert federation attempt: %w", err)
	}

	return attemptID, tx.Commit()
}

// MarkFederationInvitationAccepted runs on the INITIATOR when a remote
// server's connect callback verifies successfully: it creates a pending
// federation_attempt row (invitation_id set — the initiator has a local
// invitation row) and moves the invitation new -> accepted, atomically.
// server_id on both rows stays NULL until ApproveFederationAttempt.
// Returns the new attempt id. Returns errFederationInvitationNotFound if id
// doesn't exist, errFederationInvitationNotNew if it exists but isn't in
// status "new".
func (s *DataService) MarkFederationInvitationAccepted(ctx context.Context, inviteID string, peer federationPeer, acceptedAt time.Time) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// public_key_armor is the peer's key as stored on the invitation — the
	// initiator already holds it (it's what the connect payload was
	// encrypted to), so the connect callback doesn't need to resend it.
	var status, publicKeyArmor string
	if err := tx.QueryRowContext(ctx, `
		SELECT status, public_key_armor FROM federation_invitation WHERE id = $1 FOR UPDATE
	`, inviteID).Scan(&status, &publicKeyArmor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errFederationInvitationNotFound
		}
		return "", err
	}
	if status != federationStatusNew {
		return "", errFederationInvitationNotNew
	}

	name := peer.ServerName
	if name == "" {
		name = peer.ServerID
	}
	attemptID, err := newCryptoID()
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_attempt (id, remote_server_id, remote_server_name, base_url, fingerprint, public_key_armor, invitation_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, attemptID, peer.ServerID, name, peer.BaseURL, peer.Fingerprint, publicKeyArmor, inviteID, acceptedAt.UTC()); err != nil {
		return "", fmt.Errorf("insert federation attempt: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE federation_invitation
		SET status = $2, accepted_at = $3, connection_ciphertext = NULL
		WHERE id = $1
	`, inviteID, federationStatusAccepted, acceptedAt.UTC()); err != nil {
		return "", err
	}

	return attemptID, tx.Commit()
}

// federationLogLevel values — CHECK-constrained on federation_log.level.
const (
	federationLogInfo  = "info"
	federationLogError = "error"
)

// logFederationInvitation records a federation_log line and links it to
// invitationID via federation_invitation_log. The handshake spans two
// servers and happens asynchronously (connect callbacks, outbound POSTs
// that can fail or time out) — this is how an admin sees what actually
// happened to their invite instead of it silently stalling.
func (s *DataService) logFederationInvitation(ctx context.Context, invitationID, level, message string) error {
	logID, err := newCryptoID()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_log (id, level, message) VALUES ($1, $2, $3)
	`, logID, level, message); err != nil {
		return fmt.Errorf("insert federation log: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_invitation_log (invitation_id, log_id) VALUES ($1, $2)
	`, invitationID, logID); err != nil {
		return fmt.Errorf("link federation invitation log: %w", err)
	}
	return tx.Commit()
}

// logFederationServer records a federation_log line and links it to
// serverID via federation_server_log — for activity AFTER a servers row
// exists (i.e. after a federation_attempt was approved; see
// ApproveFederationAttempt). Pre-approval activity uses
// logFederationAttempt instead.
func (s *DataService) logFederationServer(ctx context.Context, serverID, level, message string) error {
	logID, err := newCryptoID()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_log (id, level, message) VALUES ($1, $2, $3)
	`, logID, level, message); err != nil {
		return fmt.Errorf("insert federation log: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_server_log (server_id, log_id) VALUES ($1, $2)
	`, serverID, logID); err != nil {
		return fmt.Errorf("link federation server log: %w", err)
	}
	return tx.Commit()
}

// logFederationAttempt records a federation_log line and links it to
// attemptID via federation_attempt_log — used by both the responder
// (CreateFederationAttempt) and initiator (MarkFederationInvitationAccepted)
// from the moment their federation_attempt row exists, through
// approve/reject. Unlike logFederationServer, this survives rejection —
// federation_attempt is never deleted.
func (s *DataService) logFederationAttempt(ctx context.Context, attemptID, level, message string) error {
	logID, err := newCryptoID()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_log (id, level, message) VALUES ($1, $2, $3)
	`, logID, level, message); err != nil {
		return fmt.Errorf("insert federation log: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_attempt_log (attempt_id, log_id) VALUES ($1, $2)
	`, attemptID, logID); err != nil {
		return fmt.Errorf("link federation attempt log: %w", err)
	}
	return tx.Commit()
}

// ==================== //
//       Ripples        //
// ==================== //

var ErrRippleNotFound = errors.New("ripple not found")

// ErrRippleThreadMismatch is returned by PostRipple when replyingTo is set
// but the caller's submitted threadID doesn't match the referenced
// response's stored thread_id. The signature alone only proves author
// intent, not consistency with the actual parent — see
// specs/ripples/00_design.md's Thread shape.
var ErrRippleThreadMismatch = errors.New("ripple thread mismatch")

// Ripple is a single ripple response, including its signatures. The id
// is the hex-SHA256 hash of the signed server payload (see
// specs/ripples/00_design.md's Signing section) — frozen at creation,
// never recomputed on soft-delete.
type Ripple struct {
	ID              string
	ReedAuthorID    string
	ReedID          string
	ThreadID        string
	UserID          string
	Content         string
	ReplyingTo      *string
	Deleted         bool
	PostedAt        time.Time
	UserKeyID       string
	UserSignature   UserSignature
	ServerSignature ServerSignature
	// serverFingerprint holds the canonical countersigning key id between
	// scanRipple and the caller, which copies it into ServerSignature.ID
	// (see scanRipple's comment).
	serverFingerprint string
}

// RippleListResult is the paginated output of ListRipples.
type RippleListResult struct {
	Ripples    []Ripple
	HasMore    bool
	NextCursor string
}

// PostRipple verifies, countersigns, hashes, and persists a new ripple
// response, bumping the reed's shared expires_at in the same transaction.
//
// Callers (the HTTP handler) are responsible for verifying userSigArmor
// against the caller's active public key BEFORE calling this — this
// mirrors SignReed's division of labor (handler verifies the user
// signature, store builds+persists the countersignature). This method
// only re-checks the one thing that needs a database lookup: if
// replyingTo is set, the submitted threadID must equal the referenced
// response's stored thread_id (ErrRippleThreadMismatch otherwise).
//
// now is the single server-side timestamp used for the server payload's
// `timestamp` header, posted_at, and expires_at (= now + 7 days) — one
// clock reading for the whole request, no client-supplied timestamp
// anywhere in this flow.
// reedID and userID arrive already canonical/userID@serverID form.
func (s *DataService) PostRipple(
	ctx context.Context,
	reedID, userID, content, threadID string,
	replyingTo *string,
	userFingerprint, userSigArmor string,
	countersign func(payload []byte, ts time.Time) (ServerSignature, error),
	now time.Time,
) (*Ripple, error) {
	now = now.UTC().Truncate(time.Second)
	reedAuthorIdentity, ok := authorOf(identityID(reedID))
	if !ok {
		return nil, fmt.Errorf("malformed reed id: %s", reedID)
	}
	selfIdentity := identityID(userID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if replyingTo != nil {
		var parentThreadID string
		if err := tx.QueryRowContext(ctx, `
			SELECT thread_id FROM ripple_responses WHERE id = $1
		`, *replyingTo).Scan(&parentThreadID); err != nil {
			return nil, fmt.Errorf("resolve replyingTo thread: %w", err)
		}
		if parentThreadID != threadID {
			return nil, ErrRippleThreadMismatch
		}
	}

	replyingToVal := ""
	if replyingTo != nil {
		replyingToVal = *replyingTo
	}
	serverID := s.GetServerID()
	serverPayload := buildRippleServerPayload(
		serverID, reedID, userID,
		userFingerprint, threadID, replyingToVal,
		userSigArmor, now,
	)
	serverSig, err := countersign(serverPayload, now)
	if err != nil {
		return nil, fmt.Errorf("countersign ripple: %w", err)
	}

	id := hex.EncodeToString(cryptoHash(string(serverPayload)))

	userSigID, err := insertUserSignature(ctx, tx, userFingerprint, userSigArmor)
	if err != nil {
		return nil, err
	}
	serverSigID, err := insertServerSignature(ctx, tx, serverSig.ID, serverSig.Armor, serverSig.SignedAt)
	if err != nil {
		return nil, err
	}

	expiresAt := now.Add(7 * 24 * time.Hour)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ripples (reed_id, expires_at)
		VALUES ($1, $2)
		ON CONFLICT (reed_id) DO UPDATE
		SET expires_at = EXCLUDED.expires_at
	`, reedID, expiresAt); err != nil {
		return nil, fmt.Errorf("upsert ripples bookkeeping: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ripple_responses (
			id, reed_id, thread_id, user_id,
			content, replying_to, deleted, posted_at,
			user_signature_id, server_signature_id
		) VALUES ($1, $2, $3, $4, $5, $6, FALSE, $7, $8, $9)
	`, id, reedID, threadID, selfIdentity, content, replyingTo, now,
		userSigID, serverSigID); err != nil {
		return nil, fmt.Errorf("insert ripple response: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &Ripple{
		ID:              id,
		ReedAuthorID:    reedAuthorIdentity.String(),
		ReedID:          reedID,
		ThreadID:        threadID,
		UserID:          selfIdentity.String(),
		Content:         content,
		ReplyingTo:      replyingTo,
		Deleted:         false,
		PostedAt:        now,
		UserKeyID:       userFingerprint,
		UserSignature:   UserSignature{ID: userFingerprint, Armor: userSigArmor},
		ServerSignature: serverSig,
	}, nil
}

// GetRipple loads one ripple response by id regardless of deleted state.
// Returns ErrRippleNotFound if no row matches. No account-removal
// filtering here — a commenter's own account being removed doesn't
// affect their past responses on other reeds; blocking access to a whole
// reed's ripples because its author is removed is handled by the
// parent-reed check in the HTTP handler, not here.
func (s *DataService) GetRipple(ctx context.Context, id string) (*Ripple, error) {
	r, err := scanRipple(s.db.QueryRowContext(ctx, `
		SELECT rr.id, rr.reed_id, rr.thread_id, rr.user_id,
		       rr.content, rr.replying_to, rr.deleted, rr.posted_at,
		       us.public_key_id, us.signature, ss.private_key_id, ss.signature, ss.signed_at
		FROM ripple_responses rr
		JOIN user_signatures us ON us.id = rr.user_signature_id
		JOIN server_signatures ss ON ss.id = rr.server_signature_id
		WHERE rr.id = $1
	`, id))
	if err == sql.ErrNoRows {
		return nil, ErrRippleNotFound
	}
	if err != nil {
		return nil, err
	}
	r.ServerSignature.ID = r.serverFingerprint
	return r, nil
}

// rippleRowScanner abstracts *sql.Row/*sql.Rows so scanRipple works for
// both GetRipple's single-row query and ListRipples' multi-row query.
type rippleRowScanner interface {
	Scan(dest ...any) error
}

// rippleListRow adapts a *sql.Rows result to rippleRowScanner for
// ListRipples, which selects one extra trailing column
// (thread_created_at) beyond scanRipple's fixed set — appended to
// whatever destinations scanRipple passes in, so the same scan function
// serves both GetRipple (no extra column) and ListRipples (one extra).
type rippleListRow struct {
	rows            *sql.Rows
	threadCreatedAt *time.Time
}

func (r rippleListRow) Scan(dest ...any) error {
	return r.rows.Scan(append(dest, r.threadCreatedAt)...)
}

// scanRipple scans one ripple_responses row joined against its
// user_signatures/server_signatures rows, in the exact column order
// GetRipple and ListRipples both select in. The server signature's key id
// is not a stored column of Ripple itself — callers copy the returned
// canonical key id into ServerSignature.ID after scanning.
//
// rr.reed_id and rr.user_id are both FK'd (transitively and directly,
// respectively — see PostRipple's comment); Ripple's ReedID/UserID wire
// fields hold that same value directly, scanned as plain strings with no
// decode step. ReedAuthorID is derived from ReedID after scanning.
func scanRipple(row rippleRowScanner) (*Ripple, error) {
	var r Ripple
	var replyingTo sql.NullString
	err := row.Scan(
		&r.ID, &r.ReedID, &r.ThreadID, &r.UserID, &r.Content,
		&replyingTo, &r.Deleted, &r.PostedAt,
		&r.UserKeyID, &r.UserSignature.Armor,
		&r.serverFingerprint, &r.ServerSignature.Armor, &r.ServerSignature.SignedAt,
	)
	if err != nil {
		return nil, err
	}
	if replyingTo.Valid {
		r.ReplyingTo = &replyingTo.String
	}
	if authorUserID, authorServerID, _, ok := parseKeyFingerprint(identityID(r.ReedID)); ok {
		r.ReedAuthorID = string(canonicalID(authorServerID, authorUserID))
	}
	r.UserSignature.ID = r.UserKeyID
	r.ServerSignature.SignedAt = r.ServerSignature.SignedAt.UTC().Truncate(time.Second)
	return &r, nil
}

// rippleCursor is the decoded form of the opaque ripples-list pagination
// cursor. Ordering is (thread creation time, thread id, posted at, id) —
// a single timestamp can't disambiguate thread groups, unlike ListReplies'
// plain RFC3339 cursor.
type rippleCursor struct {
	ThreadCreatedAt time.Time `json:"threadCreatedAt"`
	ThreadID        string    `json:"threadID"`
	PostedAt        time.Time `json:"postedAt"`
	ID              string    `json:"id"`
}

func encodeRippleCursor(c rippleCursor) string {
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeRippleCursor(s string) (*rippleCursor, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor encoding: %w", err)
	}
	var c rippleCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("invalid cursor payload: %w", err)
	}
	return &c, nil
}

// ListRipples returns ripple responses for reedID as a flat, already-ordered
// slice: threads ordered by the thread's own creation time (MIN(posted_at)
// for that thread_id) oldest first, responses within a thread ordered
// posted_at ASC. Includes soft-deleted rows and rows from removed-account
// authors unfiltered — both render as-is one layer up. reedID is canonical.
func (s *DataService) ListRipples(
	ctx context.Context,
	reedID string,
	limit int,
	before string,
) (*RippleListResult, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	args := []any{reedID}
	query := `
		SELECT id, reed_id, thread_id, user_id, content,
		       replying_to, deleted, posted_at,
		       user_fingerprint, user_sig, server_fingerprint, server_sig, server_signed_at,
		       thread_created_at
		FROM (
			SELECT rr.id, rr.reed_id, rr.thread_id, rr.user_id,
			       rr.content, rr.replying_to, rr.deleted, rr.posted_at,
			       us.public_key_id AS user_fingerprint, us.signature AS user_sig,
			       ss.private_key_id AS server_fingerprint, ss.signature AS server_sig,
			       ss.signed_at AS server_signed_at,
			       MIN(rr.posted_at) OVER (PARTITION BY rr.thread_id) AS thread_created_at
			FROM ripple_responses rr
			JOIN user_signatures us ON us.id = rr.user_signature_id
			JOIN server_signatures ss ON ss.id = rr.server_signature_id
			WHERE rr.reed_id = $1
		) t
	`
	if before != "" {
		c, err := decodeRippleCursor(before)
		if err != nil {
			return nil, err
		}
		args = append(args, c.ThreadCreatedAt, c.ThreadID, c.PostedAt, c.ID)
		query += fmt.Sprintf(`
			WHERE (thread_created_at, thread_id, posted_at, id) > ($%d, $%d, $%d, $%d)
		`, len(args)-3, len(args)-2, len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(`
		ORDER BY thread_created_at ASC, thread_id ASC, posted_at ASC, id ASC
		LIMIT $%d
	`, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Ripple
	var threadCreatedAts []time.Time
	for rows.Next() {
		var threadCreatedAt time.Time
		r, err := scanRipple(rippleListRow{rows, &threadCreatedAt})
		if err != nil {
			return nil, err
		}
		r.ServerSignature.ID = r.serverFingerprint
		items = append(items, *r)
		threadCreatedAts = append(threadCreatedAts, threadCreatedAt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
		threadCreatedAts = threadCreatedAts[:limit]
	}

	nextCursor := ""
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = encodeRippleCursor(rippleCursor{
			ThreadCreatedAt: threadCreatedAts[len(items)-1],
			ThreadID:        last.ThreadID,
			PostedAt:        last.PostedAt,
			ID:              last.ID,
		})
	}

	if items == nil {
		items = []Ripple{}
	}
	return &RippleListResult{Ripples: items, HasMore: hasMore, NextCursor: nextCursor}, nil
}

// GetRipplesExpiresAt returns the reed's shared expires_at from the
// ripples bookkeeping row, or the zero time if no ripple has ever been
// posted to this reed. reedID is canonical.
func (s *DataService) GetRipplesExpiresAt(ctx context.Context, reedID string) (time.Time, error) {
	var expiresAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT expires_at FROM ripples WHERE reed_id = $1
	`, reedID).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	return expiresAt, err
}

// SoftDeleteRipple flips deleted=true and content='[DELETED]' for id,
// only if ownerUserID matches ripple_responses.user_id. found reports
// whether the row exists at all; owned reports whether ownerUserID
// matches (only meaningful when found is true). Does not touch
// ripples.expires_at. Idempotent: deleting an already-deleted row
// succeeds again as a no-op.
//
// ripple_responses.user_id is a direct FK; ownerUserID arrives in
// userID@serverID form already but needs an identityID-typed
// value for the equality check against actualOwner, hence the type conversion.
func (s *DataService) SoftDeleteRipple(ctx context.Context, id, ownerUserID string) (found, owned bool, err error) {
	ownerIdentity := identityID(ownerUserID)
	var actualOwner identityID
	err = s.db.QueryRowContext(ctx, `
		SELECT user_id FROM ripple_responses WHERE id = $1
	`, id).Scan(&actualOwner)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if actualOwner != ownerIdentity {
		return true, false, nil
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE ripple_responses
		SET deleted = TRUE, content = '[DELETED]'
		WHERE id = $1
	`, id); err != nil {
		return true, true, err
	}
	return true, true, nil
}

// ============ //
//   coverage   //
// ============ //

// coveragePercent returns floor(100 * holders / activeUsers), capped at 100.
func coveragePercent(holders, activeUsers int) int {
	if activeUsers <= 0 {
		return 0
	}
	p := (100 * holders) / activeUsers
	if p > 100 {
		return 100
	}
	return p
}

// bumpActiveUsers adjusts the singleton active-user counter in the same TX.
func bumpActiveUsers(ctx context.Context, tx *sql.Tx, delta int) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE network_stats
		SET active_users = GREATEST(0, active_users + $1)
		WHERE id = TRUE
	`, delta)
	return err
}

// getActiveUsers reads the network-wide active user count.
func getActiveUsers(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT active_users FROM network_stats WHERE id = TRUE`).Scan(&n)
	return n, err
}

// ========== //
//   signing   //
// ========== //

// signingDBTX is the subset of *sql.DB / *sql.Tx needed by signature store helpers.
type signingDBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// userSignatureRow is one row in user_signatures. PublicKeyID names which
// key (public_keys.id) produced the signature — the wire shape still calls
// this "fingerprint", since on the wire it's identifying a signer, an
// orthogonal concept to a canonical key id.
type userSignatureRow struct {
	ID          int64
	PublicKeyID string
	Signature   string
}

// serverSignatureRow is one row in server_signatures. PrivateKeyID names
// which key (private_keys.id / public_keys.id — the two share ids)
// produced the countersignature, same pattern as userSignatureRow.PublicKeyID.
type serverSignatureRow struct {
	ID           int64
	PrivateKeyID string
	Signature    string
	SignedAt     time.Time
}

// insertUserSignature inserts a user attestation row and returns its id.
func insertUserSignature(ctx context.Context, db signingDBTX, publicKeyID, signature string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO user_signatures (public_key_id, signature)
		VALUES ($1, $2)
		RETURNING id
	`, publicKeyID, signature).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert user_signatures: %w", err)
	}
	return id, nil
}

// insertServerSignature inserts a server countersignature row and returns
// its id. signedAt is stored UTC truncated to seconds.
func insertServerSignature(ctx context.Context, db signingDBTX, privateKeyID, signature string, signedAt time.Time) (int64, error) {
	signedAt = signedAt.UTC().Truncate(time.Second)
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO server_signatures (private_key_id, signature, signed_at)
		VALUES ($1, $2, $3)
		RETURNING id
	`, privateKeyID, signature, signedAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert server_signatures: %w", err)
	}
	return id, nil
}

// getUserSignatureWire loads a user_signatures row by id and returns it
// directly as the wire UserSignature block (skipping the intermediate
// row/wire split the old signing package used, since every caller here
// immediately unpacked one into the other).
func getUserSignatureWire(ctx context.Context, db signingDBTX, id int64) (UserSignature, error) {
	var row userSignatureRow
	err := db.QueryRowContext(ctx, `
		SELECT id, public_key_id, signature
		FROM user_signatures
		WHERE id = $1
	`, id).Scan(
		&row.ID,
		&row.PublicKeyID,
		&row.Signature,
	)
	if err != nil {
		return UserSignature{}, err
	}
	return UserSignature{ID: row.PublicKeyID, Armor: row.Signature}, nil
}

// getServerSignatureWire loads a server_signatures row by id and returns
// it directly as the wire ServerSignature block — see getUserSignatureWire.
func getServerSignatureWire(ctx context.Context, db signingDBTX, id int64) (ServerSignature, error) {
	var row serverSignatureRow
	err := db.QueryRowContext(ctx, `
		SELECT id, private_key_id, signature, signed_at
		FROM server_signatures
		WHERE id = $1
	`, id).Scan(
		&row.ID,
		&row.PrivateKeyID,
		&row.Signature,
		&row.SignedAt,
	)
	if err != nil {
		return ServerSignature{}, err
	}
	return ServerSignature{
		ID:       row.PrivateKeyID,
		Armor:    row.Signature,
		SignedAt: row.SignedAt.UTC().Truncate(time.Second),
	}, nil
}

// getUserSignatureRow loads a user_signatures row by id without wire
// conversion — for callers that need the raw row (e.g. just the armor).
func getUserSignatureRow(ctx context.Context, db signingDBTX, id int64) (*userSignatureRow, error) {
	var row userSignatureRow
	err := db.QueryRowContext(ctx, `
		SELECT id, public_key_id, signature
		FROM user_signatures
		WHERE id = $1
	`, id).Scan(
		&row.ID,
		&row.PublicKeyID,
		&row.Signature,
	)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// getServerSignatureRow loads a server_signatures row by id without wire
// conversion — for callers that need the raw row (e.g. removal certs,
// which expose PrivateKeyID as ServerFingerprint directly).
func getServerSignatureRow(ctx context.Context, db signingDBTX, id int64) (*serverSignatureRow, error) {
	var row serverSignatureRow
	err := db.QueryRowContext(ctx, `
		SELECT id, private_key_id, signature, signed_at
		FROM server_signatures
		WHERE id = $1
	`, id).Scan(
		&row.ID,
		&row.PrivateKeyID,
		&row.Signature,
		&row.SignedAt,
	)
	if err != nil {
		return nil, err
	}
	row.SignedAt = row.SignedAt.UTC().Truncate(time.Second)
	return &row, nil
}

// ============ //
//   deletion   //
// ============ //

// errRemovalConflict is returned when an existing removal row differs
// from the cert being inserted (identical replay succeeds). Used for
// reed and account removals.
var errRemovalConflict = errors.New("removal conflict")

// reedRemovalCert is the reed-removal attestation (in-memory / wire-facing shape).
//
// UserID asymmetry (same as accountRemovalCert): insertReedRemovalCert/
// getReedRemovalCert's userID params are bare, but every cert RETURNED by
// getReedRemovalCert/loadReedCertTx holds the full "userID@serverID" form.
type reedRemovalCert struct {
	ReedID            string
	UserID            string
	UserSignature     string
	UserKeyID         string
	ServerSignature   string
	ServerFingerprint string
	ServerSignedAt    time.Time
}

// insertReedRemovalCert stores a reed-removal cert once. Same signatures →
// no-op; different signatures for the same reedID → errRemovalConflict.
// cert.ReedID is canonical (embeds the author), so no separate user_id
// column is needed.
func insertReedRemovalCert(ctx context.Context, db *sql.DB, cert reedRemovalCert, serverID string) error {
	cert.ServerSignedAt = cert.ServerSignedAt.UTC().Truncate(time.Second)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing, err := loadReedCertTx(ctx, tx, cert.ReedID, true)
	switch {
	case err == sql.ErrNoRows:
		userSigID, err := insertUserSignature(
			ctx, tx, cert.UserKeyID, cert.UserSignature,
		)
		if err != nil {
			return err
		}
		serverSigID, err := insertServerSignature(
			ctx, tx, cert.ServerFingerprint, cert.ServerSignature, cert.ServerSignedAt,
		)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reed_removals (
				reed_id, public_key_id,
				user_signature_id, server_signature_id
			) VALUES ($1, $2, $3, $4)
		`, cert.ReedID, cert.UserKeyID, userSigID, serverSigID); err != nil {
			return fmt.Errorf("insert reed removal: %w", err)
		}
	case err != nil:
		return err
	default:
		if existing.UserSignature != cert.UserSignature ||
			existing.UserKeyID != cert.UserKeyID ||
			existing.ServerSignature != cert.ServerSignature ||
			existing.ServerFingerprint != cert.ServerFingerprint ||
			!existing.ServerSignedAt.Equal(cert.ServerSignedAt) {
			return errRemovalConflict
		}
	}

	return tx.Commit()
}

// getReedRemovalCert returns the stored cert for reedID (canonical), or nil if none.
func getReedRemovalCert(ctx context.Context, db *sql.DB, reedID, serverID string) (*reedRemovalCert, error) {
	cert, err := loadReedCertTx(ctx, db, reedID, false)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cert, nil
}

type reedQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func loadReedCertTx(ctx context.Context, q reedQuerier, reedID string, forUpdate bool) (*reedRemovalCert, error) {
	query := `
		SELECT public_key_id, user_signature_id, server_signature_id
		FROM reed_removals
		WHERE reed_id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var userFP string
	var userSigID, serverSigID int64
	err := q.QueryRowContext(ctx, query, reedID).Scan(&userFP, &userSigID, &serverSigID)
	if err != nil {
		return nil, err
	}
	authorID, ok := authorOf(identityID(reedID))
	if !ok {
		return nil, fmt.Errorf("malformed reed id: %s", reedID)
	}
	return assembleReedCert(ctx, q, reedID, string(authorID), userFP, userSigID, serverSigID)
}

func assembleReedCert(ctx context.Context, q reedQuerier, reedID, userID, userFP string, userSigID, serverSigID int64) (*reedRemovalCert, error) {
	// signing helpers need signingDBTX; *sql.DB and *sql.Tx both work.
	dbtx, ok := q.(signingDBTX)
	if !ok {
		return nil, fmt.Errorf("reed removal load: querier is not signingDBTX")
	}
	userRow, err := getUserSignatureRow(ctx, dbtx, userSigID)
	if err != nil {
		return nil, err
	}
	serverRow, err := getServerSignatureRow(ctx, dbtx, serverSigID)
	if err != nil {
		return nil, err
	}
	return &reedRemovalCert{
		ReedID:            reedID,
		UserID:            userID,
		UserKeyID:         userFP,
		UserSignature:     userRow.Signature,
		ServerSignature:   serverRow.Signature,
		ServerFingerprint: serverRow.PrivateKeyID,
		ServerSignedAt:    serverRow.SignedAt,
	}, nil
}

// maxAccountNoteLen is the goodbye note limit (API + DB).
const maxAccountNoteLen = 140

// accountRemovalCert is the account-removal attestation (in-memory / wire-facing).
//
// UserID is bare on insertAccountRemovalCert's input, but full
// "userID@serverID" form on any accountRemovalCert returned by
// getAccountRemovalCert/loadAccountCertTx.
type accountRemovalCert struct {
	UserID            string
	Note              string
	UserSignature     string
	UserKeyID         string
	ServerSignature   string
	ServerFingerprint string
	ServerSignedAt    time.Time
}

// validateAccountNote returns an error if note exceeds maxAccountNoteLen.
func validateAccountNote(note string) error {
	if utf8.RuneCountInString(note) > maxAccountNoteLen {
		return fmt.Errorf("note exceeds %d characters", maxAccountNoteLen)
	}
	return nil
}

// insertAccountRemovalCert stores an account-removal cert once. Same
// signatures → no-op; different signatures for the same userID →
// errRemovalConflict. cert.UserID is bare and is converted internally
// before touching account_removals.
func insertAccountRemovalCert(ctx context.Context, db *sql.DB, cert accountRemovalCert, serverID string) error {
	if err := validateAccountNote(cert.Note); err != nil {
		return err
	}
	cert.ServerSignedAt = cert.ServerSignedAt.UTC().Truncate(time.Second)
	selfIdentity := canonicalID(serverID, cert.UserID)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing, err := loadAccountCertTx(ctx, tx, selfIdentity, true)
	switch {
	case err == sql.ErrNoRows:
		userSigID, err := insertUserSignature(
			ctx, tx, cert.UserKeyID, cert.UserSignature,
		)
		if err != nil {
			return err
		}
		serverSigID, err := insertServerSignature(
			ctx, tx, cert.ServerFingerprint, cert.ServerSignature, cert.ServerSignedAt,
		)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO account_removals (
				user_id, note, public_key_id,
				user_signature_id, server_signature_id
			) VALUES ($1, $2, $3, $4, $5)
		`, selfIdentity, cert.Note, cert.UserKeyID, userSigID, serverSigID); err != nil {
			return fmt.Errorf("insert account removal: %w", err)
		}
		// Clear the username so it becomes reclaimable by a future signup.
		// users.id IS identities.id directly — must bind selfIdentity, not
		// bare cert.UserID, or this always-false comparison clears zero rows.
		var profileUserSigID, profileServerSigID int64
		if err := tx.QueryRowContext(ctx, `
			SELECT user_signature_id, server_signature_id FROM users WHERE id = $1
		`, selfIdentity).Scan(&profileUserSigID, &profileServerSigID); err != nil {
			return fmt.Errorf("load profile signature ids: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET username = NULL, user_signature_id = NULL, server_signature_id = NULL
			WHERE id = $1
		`, selfIdentity); err != nil {
			return fmt.Errorf("clear profile on removal: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM user_signatures WHERE id = $1
		`, profileUserSigID); err != nil {
			return fmt.Errorf("delete stale profile user signature: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM server_signatures WHERE id = $1
		`, profileServerSigID); err != nil {
			return fmt.Errorf("delete stale profile server signature: %w", err)
		}

		if err := bumpActiveUsers(ctx, tx, -1); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if existing.Note != cert.Note ||
			existing.UserSignature != cert.UserSignature ||
			existing.UserKeyID != cert.UserKeyID ||
			existing.ServerSignature != cert.ServerSignature ||
			existing.ServerFingerprint != cert.ServerFingerprint ||
			!existing.ServerSignedAt.Equal(cert.ServerSignedAt) {
			return errRemovalConflict
		}
	}

	return tx.Commit()
}

// insertForeignAccountRemovalCert stores an account-removal cert for a
// user this server doesn't host — a peer holding content from that
// author told us about it. cert.UserID is already the full canonical
// form (unlike insertAccountRemovalCert's bare input). Only writes the
// account_removals row: no username reclaim, no signature cleanup, no
// active-user count change, since none of those apply to an account
// this server never owned.
func insertForeignAccountRemovalCert(ctx context.Context, db *sql.DB, cert accountRemovalCert) error {
	if err := validateAccountNote(cert.Note); err != nil {
		return err
	}
	cert.ServerSignedAt = cert.ServerSignedAt.UTC().Truncate(time.Second)
	selfIdentity := identityID(cert.UserID)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing, err := loadAccountCertTx(ctx, tx, selfIdentity, true)
	switch {
	case err == sql.ErrNoRows:
		userSigID, err := insertUserSignature(
			ctx, tx, cert.UserKeyID, cert.UserSignature,
		)
		if err != nil {
			return err
		}
		serverSigID, err := insertServerSignature(
			ctx, tx, cert.ServerFingerprint, cert.ServerSignature, cert.ServerSignedAt,
		)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO account_removals (
				user_id, note, public_key_id,
				user_signature_id, server_signature_id
			) VALUES ($1, $2, $3, $4, $5)
		`, selfIdentity, cert.Note, cert.UserKeyID, userSigID, serverSigID); err != nil {
			return fmt.Errorf("insert foreign account removal: %w", err)
		}
	case err != nil:
		return err
	default:
		if existing.Note != cert.Note ||
			existing.UserSignature != cert.UserSignature ||
			existing.UserKeyID != cert.UserKeyID ||
			existing.ServerSignature != cert.ServerSignature ||
			existing.ServerFingerprint != cert.ServerFingerprint ||
			!existing.ServerSignedAt.Equal(cert.ServerSignedAt) {
			return errRemovalConflict
		}
	}

	return tx.Commit()
}

// getAccountRemovalCert returns the stored account-removal cert, or nil
// if none. userID (the lookup param) is bare; the RETURNED cert's UserID
// field is the full "userID@serverID" form — see accountRemovalCert's
// doc comment.
func getAccountRemovalCert(ctx context.Context, db *sql.DB, userID, serverID string) (*accountRemovalCert, error) {
	selfIdentity := canonicalID(serverID, userID)
	cert, err := loadAccountCertTx(ctx, db, selfIdentity, false)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cert, nil
}

// hasAccountRemoval reports whether userID has an account-removal cert.
// userID is bare; serverID is this server's own id.
func hasAccountRemoval(ctx context.Context, db *sql.DB, userID, serverID string) (bool, error) {
	selfIdentity := canonicalID(serverID, userID)
	var one int
	err := db.QueryRowContext(ctx, `
		SELECT 1 FROM account_removals WHERE user_id = $1
	`, selfIdentity).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func loadAccountCertTx(ctx context.Context, q reedQuerier, selfIdentity identityID, forUpdate bool) (*accountRemovalCert, error) {
	query := `
		SELECT note, public_key_id, user_signature_id, server_signature_id
		FROM account_removals
		WHERE user_id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var note, userFP string
	var userSigID, serverSigID int64
	err := q.QueryRowContext(ctx, query, selfIdentity).Scan(&note, &userFP, &userSigID, &serverSigID)
	if err != nil {
		return nil, err
	}
	dbtx, ok := q.(signingDBTX)
	if !ok {
		return nil, fmt.Errorf("account removal load: querier is not signingDBTX")
	}
	userRow, err := getUserSignatureRow(ctx, dbtx, userSigID)
	if err != nil {
		return nil, err
	}
	serverRow, err := getServerSignatureRow(ctx, dbtx, serverSigID)
	if err != nil {
		return nil, err
	}
	return &accountRemovalCert{
		UserID:            string(selfIdentity),
		Note:              note,
		UserKeyID:         userFP,
		UserSignature:     userRow.Signature,
		ServerSignature:   serverRow.Signature,
		ServerFingerprint: serverRow.PrivateKeyID,
		ServerSignedAt:    serverRow.SignedAt,
	}, nil
}

// =========== //
//   invites   //
// =========== //

// inviteSignupMode is the deploy-time registration policy.
type inviteSignupMode string

const (
	signupModeOpen   inviteSignupMode = "open"
	signupModeInvite inviteSignupMode = "invite"
	signupModeClosed inviteSignupMode = "closed"
)

// maxInvitesUnlimited means no per-user invite minting cap.
const maxInvitesUnlimited = -1

// inviteCreateSkew is how far a client-supplied createdAt may drift from
// server now on create.
const inviteCreateSkew = 5 * time.Minute

// hashSecret returns SHA-256(secret). Create sends this digest (hex); the
// server stores it. Redeem sends the raw secret; the server hashes and
// compares. The secret itself never appears on create.
func hashSecret(secret string) []byte {
	return cryptoHash(secret)
}

// encodeHashHex encodes a 32-byte digest as lowercase hex (wire / signed header).
func encodeHashHex(digest []byte) string {
	return hex.EncodeToString(digest)
}

// decodeHashHex parses a 32-byte digest from hex.
func decodeHashHex(s string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	if len(b) != cryptoHashSize {
		return nil, fmt.Errorf("hash must be %d bytes", cryptoHashSize)
	}
	return b, nil
}

// newInviteSecret returns a URL-fragment-safe raw secret (≥256 bits).
func newInviteSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := cryptorand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// newInviteID returns a random UUIDv7 — the bare entity component of a
// canonical invite id (creatorID@serverID/uuid), same convention as reed ids.
func newInviteID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// Invite revoke / insert outcome errors for handlers.
var (
	errInviteNotFound       = errors.New("invite not found")
	errInviteNotOwner       = errors.New("invite not owned by caller")
	errInviteAlreadyClaimed = errors.New("invite already claimed")
	errInviteAlreadyRevoked = errors.New("invite already revoked")
	errInviteExists         = errors.New("invite already exists")
	errInviteRequired       = errors.New("invite required")
	errInvalidInvite        = errors.New("invalid or claimed invite")
)

// inviteRecord is the durable invite row (never includes the raw token).
// CreatedBy/ClaimedBy hold the full "userID@serverID" form; ClaimedBy is
// exposed via inviteStatusResponse.ClaimedBy on GET /api/invites/{id}.
// UserSignature is the inviter's attestation over the invite fields.
type inviteRecord struct {
	ID            string
	CreatedBy     string
	CreatedAt     time.Time
	GrantedRole   string
	ClaimedAt     *time.Time
	ClaimedBy     *string
	RevokedAt     *time.Time
	UserSignature userSignatureRow
}

// Status derives the invite read-model status (revoked wins over claimed).
func (inv inviteRecord) Status() string {
	if inv.RevokedAt != nil {
		return "revoked"
	}
	if inv.ClaimedAt != nil {
		return "claimed"
	}
	return "pending"
}

// resolvedInvite is the invite (if any) to consume during signup.
type resolvedInvite struct {
	InviteID string
}

// resolveSignup applies SIGNUP_MODE invite policy given an optional invite
// row looked up by id + hashSecret(secret) (nil if absent).
//
// When inviteID/secret are provided, inv must be pending and inv.ID must
// equal inviteID.
//
// Policy:
//   - invite mode → id+secret required
//   - open mode → optional
//   - closed mode is rejected by the handler before this runs
//   - if credentials are provided they must resolve to a pending invite
//
// First account: deploy with SIGNUP_MODE=open, then switch to invite or closed.
func resolveSignup(mode inviteSignupMode, inviteID, secret string, inv *inviteRecord) (resolvedInvite, error) {
	id := strings.TrimSpace(inviteID)
	sec := strings.TrimSpace(secret)
	hasCreds := id != "" || sec != ""

	if !hasCreds {
		if mode == signupModeInvite {
			return resolvedInvite{}, errInviteRequired
		}
		return resolvedInvite{}, nil
	}
	if id == "" || sec == "" {
		return resolvedInvite{}, errInvalidInvite
	}
	if inv == nil || inv.Status() != "pending" || inv.ID != id {
		return resolvedInvite{}, errInvalidInvite
	}
	return resolvedInvite{InviteID: inv.ID}, nil
}

func (s *DataService) countInvitesByCreator(ctx context.Context, creatorID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM invites WHERE created_by = $1
	`, creatorID).Scan(&n)
	return n, err
}

func (s *DataService) insertInvite(
	ctx context.Context,
	id, creatorID string,
	tokenHash []byte,
	createdAt time.Time,
	grantedRole string,
	userKeyID, userSignatureArmor string,
) error {
	if grantedRole == "" {
		grantedRole = "user"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	userSignatureID, err := insertUserSignature(ctx, tx, userKeyID, userSignatureArmor)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO invites (id, created_by, token_hash, created_at, granted_role, user_signature_id)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, creatorID, tokenHash, createdAt.UTC(), grantedRole, userSignatureID)
	if isUniqueViolation(err) {
		return errInviteExists
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *DataService) getInviteByID(ctx context.Context, id string) (*inviteRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, granted_role, claimed_at, claimed_by, revoked_at, user_signature_id
		FROM invites
		WHERE id = $1
	`, id)
	return scanInvite(ctx, s.db, row)
}

func (s *DataService) getInviteByTokenHash(ctx context.Context, hash []byte) (*inviteRecord, error) {
	return getInviteByTokenHash(ctx, s.db, s.db, hash)
}

func (s *DataService) getInviteByTokenHashTx(ctx context.Context, tx *sql.Tx, hash []byte) (*inviteRecord, error) {
	return getInviteByTokenHash(ctx, tx, tx, hash)
}

type inviteTokenHashQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// getInviteByTokenHash has no creatorID in scope (token_hash is globally
// unique) — created_by/claimed_by come back in full form from the row
// itself, no conversion needed on the query side here.
func getInviteByTokenHash(ctx context.Context, q inviteTokenHashQuerier, sigDB signingDBTX, hash []byte) (*inviteRecord, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, granted_role, claimed_at, claimed_by, revoked_at, user_signature_id
		FROM invites
		WHERE token_hash = $1
	`, hash)
	return scanInvite(ctx, sigDB, row)
}

func (s *DataService) getPendingInviteTx(ctx context.Context, tx *sql.Tx, id string, hash []byte) (*inviteRecord, error) {
	return getPendingInvite(ctx, tx, tx, id, hash)
}

func getPendingInvite(ctx context.Context, q inviteTokenHashQuerier, sigDB signingDBTX, id string, hash []byte) (*inviteRecord, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, created_by, created_at, granted_role, claimed_at, claimed_by, revoked_at, user_signature_id
		FROM invites
		WHERE id = $1 AND token_hash = $2
	`, id, hash)
	inv, err := scanInvite(ctx, sigDB, row)
	if err != nil || inv == nil {
		return inv, err
	}
	if inv.Status() != "pending" {
		return nil, nil
	}
	return inv, nil
}

// markInviteClaimed claims an unused, unrevoked invite inside tx. inviteID
// is canonical; claimedBy is a bare userID. Returns whether a row was updated.
func (s *DataService) markInviteClaimed(
	ctx context.Context,
	tx *sql.Tx,
	inviteID, claimedBy string,
	claimedAt time.Time,
) (bool, error) {
	claimedByIdentity := canonicalID(s.serverID, claimedBy)
	res, err := tx.ExecContext(ctx, `
		UPDATE invites
		SET claimed_at = $2, claimed_by = $3
		WHERE id = $1
		  AND claimed_at IS NULL AND revoked_at IS NULL
	`, inviteID, claimedAt.UTC(), claimedByIdentity)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// revokeInvite marks an unused invite revoked. inviteID is canonical;
// callerID must match the invite's creator.
func (s *DataService) revokeInvite(
	ctx context.Context,
	inviteID, callerID string,
	revokedAt time.Time,
) error {
	var createdBy string
	var claimedAt, existingRevoked sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT created_by, claimed_at, revoked_at
		FROM invites WHERE id = $1
	`, inviteID).Scan(&createdBy, &claimedAt, &existingRevoked)
	if err == sql.ErrNoRows {
		return errInviteNotFound
	}
	if err != nil {
		return err
	}
	if createdBy != callerID {
		return errInviteNotOwner
	}
	if claimedAt.Valid {
		return errInviteAlreadyClaimed
	}
	if existingRevoked.Valid {
		return errInviteAlreadyRevoked
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE invites
		SET revoked_at = $2
		WHERE id = $1 AND claimed_at IS NULL AND revoked_at IS NULL
	`, inviteID, revokedAt.UTC())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errInviteNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}

type inviteScannable interface {
	Scan(dest ...any) error
}

// scanInvite scans created_by/claimed_by as identityID (the row's actual
// stored form) and keeps that form on inviteRecord's wire-facing fields,
// no decode to bare. Loads the inviter's persisted signature via sigDB.
func scanInvite(ctx context.Context, sigDB signingDBTX, row inviteScannable) (*inviteRecord, error) {
	var inv inviteRecord
	var createdBy identityID
	var claimedAt, revokedAt sql.NullTime
	var claimedBy sql.NullString
	var userSignatureID int64
	err := row.Scan(
		&inv.ID,
		&createdBy,
		&inv.CreatedAt,
		&inv.GrantedRole,
		&claimedAt,
		&claimedBy,
		&revokedAt,
		&userSignatureID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	inv.CreatedBy = string(createdBy)
	if claimedAt.Valid {
		t := claimedAt.Time.UTC()
		inv.ClaimedAt = &t
	}
	if claimedBy.Valid {
		s := claimedBy.String
		inv.ClaimedBy = &s
	}
	if revokedAt.Valid {
		t := revokedAt.Time.UTC()
		inv.RevokedAt = &t
	}
	sigRow, err := getUserSignatureRow(ctx, sigDB, userSignatureID)
	if err != nil {
		return nil, err
	}
	inv.UserSignature = *sigRow
	return &inv, nil
}

// ============ //
//   recovery   //
// ============ //

const maxRecoveryFollowingBatch = 100

// recoverySaveIdentityResult describes what a save did to the users row.
type recoverySaveIdentityResult struct {
	Created bool
	Updated bool // profile columns written (create or newer-wins)
	// Rejected is true when profile.Username collided with an existing
	// holder whose server_signed_at was newer or equal — the incoming
	// submission was discarded, nothing was written.
	Rejected bool
}

// errRecoveryUsernameCollisionLoss signals that the incoming profile lost a
// username collision and must not be persisted.
var errRecoveryUsernameCollisionLoss = fmt.Errorf("incoming profile lost username collision")

// Every subject handled by the recovery save/reed/follow functions below
// (profile.ID, follow targets, reed authors/reporters) is a bare userID
// local to serverID; every identity minted or looked up here uses
// canonicalID(serverID, userID) — cross-server subjects aren't handled here.

// saveOwnIdentity upserts a verified own-claim identity + nest. Clears
// unclaimed_accounts and records the user in ongoing_recoveries.
func saveOwnIdentity(ctx context.Context, db *sql.DB, serverID string, profile recoveryProfile, flat []recoveryFlatKey, deviceID string) (*recoverySaveIdentityResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	selfIdentity := canonicalID(serverID, profile.ID)

	res, err := upsertRecoveryIdentity(ctx, tx, serverID, profile, flat)
	if err != nil {
		return nil, err
	}
	if res.Rejected {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return res, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM unclaimed_accounts WHERE user_id = $1`, selfIdentity); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ongoing_recoveries (user_id) VALUES ($1)
		ON CONFLICT DO NOTHING
	`, selfIdentity); err != nil {
		return nil, err
	}
	if err := drainRecoveryPendingFollows(ctx, tx, profile.ID, selfIdentity); err != nil {
		return nil, err
	}
	if deviceID != "" {
		if err := bindRecoveryClaimDeviceTx(ctx, tx, selfIdentity, deviceID, profile.ServerSignature.Timestamp.UTC()); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// savePeerIdentity upserts a verified peer-reported identity + nest.
// Newly created rows are inserted into unclaimed_accounts; already-claimed
// accounts are never re-marked unclaimed.
func savePeerIdentity(ctx context.Context, db *sql.DB, serverID string, profile recoveryProfile, flat []recoveryFlatKey) (*recoverySaveIdentityResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	selfIdentity := canonicalID(serverID, profile.ID)

	res, err := upsertRecoveryIdentity(ctx, tx, serverID, profile, flat)
	if err != nil {
		return nil, err
	}
	if res.Rejected {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return res, nil
	}
	if res.Created {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO unclaimed_accounts (user_id) VALUES ($1)
			ON CONFLICT DO NOTHING
		`, selfIdentity); err != nil {
			return nil, err
		}
	}
	if err := drainRecoveryPendingFollows(ctx, tx, profile.ID, selfIdentity); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

func upsertRecoveryIdentity(ctx context.Context, tx *sql.Tx, serverID string, profile recoveryProfile, flat []recoveryFlatKey) (*recoverySaveIdentityResult, error) {
	if len(flat) == 0 {
		return nil, fmt.Errorf("empty key nest")
	}
	incomingSignedAt := profile.ServerSignature.Timestamp.UTC().Truncate(time.Second)
	selfIdentity := canonicalID(serverID, profile.ID)
	// flat's fingerprints are bare (see insertRecoveryKeys' comment);
	// canonicalize for users.active_key_id and the signature-attestation
	// rows, which want the same canonical form as every other table.
	activeFP := string(appendEntity(selfIdentity, flat[len(flat)-1].Key.Fingerprint))

	// Lock/check the identities row, not users — identities is the actual FK
	// target. users.id IS identities.id directly, so the join is on u.id.
	var existingSignedAt time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT ss.signed_at
		FROM identities i
		JOIN users u ON u.id = i.id
		JOIN server_signatures ss ON ss.id = u.server_signature_id
		WHERE i.id = $1
		FOR UPDATE OF i
	`, selfIdentity).Scan(&existingSignedAt)

	created := false
	updated := false

	switch {
	case err == sql.ErrNoRows:
		// Mint the identities row before public_keys/users — both
		// FK to it, and neither exists yet on this branch. ON CONFLICT DO
		// NOTHING: a stale identities row surviving an earlier partial run
		// is safe to leave in place.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO identities (id, server_id)
			VALUES ($1, $2)
			ON CONFLICT (id) DO NOTHING
		`, selfIdentity, serverID); err != nil {
			return nil, fmt.Errorf("insert identity: %w", err)
		}
		if err := insertRecoveryKeys(ctx, tx, selfIdentity, flat); err != nil {
			return nil, err
		}
		if err := insertRecoveryUser(ctx, tx, serverID, profile, activeFP, incomingSignedAt); err != nil {
			if errors.Is(err, errRecoveryUsernameCollisionLoss) {
				return &recoverySaveIdentityResult{Rejected: true}, nil
			}
			return nil, err
		}
		created = true
		updated = true
	case err != nil:
		return nil, err
	default:
		if err := insertRecoveryKeys(ctx, tx, selfIdentity, flat); err != nil {
			return nil, err
		}
		wrote, err := updateRecoveryUserIfNewer(ctx, tx, selfIdentity, profile, activeFP, existingSignedAt, incomingSignedAt)
		if err != nil {
			if errors.Is(err, errRecoveryUsernameCollisionLoss) {
				return &recoverySaveIdentityResult{Rejected: true}, nil
			}
			return nil, err
		}
		updated = wrote
	}

	return &recoverySaveIdentityResult{Created: created, Updated: updated}, nil
}

// insertRecoveryUser inserts the users row (its satellite identities row is
// already minted by the caller). active_key_id references a public_keys
// row inserted by the caller's insertRecoveryKeys call, before this runs.
func insertRecoveryUser(ctx context.Context, tx *sql.Tx, serverID string, profile recoveryProfile, activeFP string, signedAt time.Time) error {
	selfIdentity := canonicalID(serverID, profile.ID)

	username, err := claimRecoveryUsername(ctx, tx, selfIdentity, profile.Username, signedAt)
	if err != nil {
		return err
	}
	if err := validateProfileRole(profile.ID, profile.Role, serverID); err != nil {
		return err
	}

	// profile.UserSignature.KeyID is already the full canonical key
	// id (unlike flat's bare fingerprints) — use it as-is when present.
	keyID := activeFP
	if profile.UserSignature.KeyID != "" {
		keyID = profile.UserSignature.KeyID
	}
	userSignatureID, err := insertUserSignature(ctx, tx, keyID, profile.UserSignature.Armor)
	if err != nil {
		return err
	}
	serverKeyID := string(canonicalID(serverID, profile.ServerSignature.Fingerprint))
	serverSignatureID, err := insertServerSignature(ctx, tx, serverKeyID, profile.ServerSignature.Armor, signedAt)
	if err != nil {
		return err
	}
	// invite_id is already canonical (creatorID@serverID/uuid) — no
	// conversion needed, unlike a bare user id.
	var inviteID any
	if id := recoveryProfileInviteID(profile); id != "" {
		inviteID = id
	}
	// users.id IS identities.id directly — selfIdentity is the sole PK
	// value, same pattern as Signup's INSERT.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO users (
			id, username, role, created_at, active_key_id, bio,
			user_signature_id, server_signature_id, invite_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		selfIdentity, username, profile.Role, profile.MemberSince.UTC().Truncate(time.Second),
		activeFP, nullIfEmptyString(profile.Bio),
		userSignatureID, serverSignatureID, inviteID,
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return bumpActiveUsers(ctx, tx, 1)
}

func updateRecoveryUserIfNewer(
	ctx context.Context,
	tx *sql.Tx,
	selfIdentity identityID,
	profile recoveryProfile,
	activeFP string,
	existingSignedAt time.Time,
	incomingSignedAt time.Time,
) (bool, error) {
	if !incomingSignedAt.After(existingSignedAt) {
		return false, nil
	}
	username, err := claimRecoveryUsername(ctx, tx, selfIdentity, profile.Username, incomingSignedAt)
	if err != nil {
		return false, err
	}
	// profile.UserSignature.KeyID is already the full canonical key
	// id (unlike flat's bare fingerprints) — use it as-is when present.
	keyID := activeFP
	if profile.UserSignature.KeyID != "" {
		keyID = profile.UserSignature.KeyID
	}
	userSignatureID, err := insertUserSignature(ctx, tx, keyID, profile.UserSignature.Armor)
	if err != nil {
		return false, err
	}
	serverKeyID := string(canonicalID(selfIdentity.ServerID(), profile.ServerSignature.Fingerprint))
	serverSignatureID, err := insertServerSignature(ctx, tx, serverKeyID, profile.ServerSignature.Armor, incomingSignedAt)
	if err != nil {
		return false, err
	}
	// Profile fields only — role/username/bio/fingerprint/signatures. This
	// updates the existing satellite users row in place; the identities row
	// is untouched. WHERE id = $7 targets users.id, which IS identities.id
	// — must bind selfIdentity, not bare profile.ID, or this updates zero rows.
	_, err = tx.ExecContext(ctx, `
		UPDATE users SET
			username = $1,
			bio = $2,
			role = $3,
			active_key_id = $4,
			user_signature_id = $5,
			server_signature_id = $6
		WHERE id = $7
	`,
		username, nullIfEmptyString(profile.Bio),
		profile.Role, activeFP, userSignatureID, serverSignatureID, selfIdentity,
	)
	if err != nil {
		return false, fmt.Errorf("update user: %w", err)
	}
	return true, nil
}

// insertRecoveryKeys writes flat's keys/revocations to public_keys/
// public_key_revocations. flat's fingerprints arrive BARE —
// verifyRecoveryKeyCountersig/verifyRecoveryRevocation checked them against
// bytes the SPA's recoveryKeyNest.ts actually signed, which (per this
// section's deliberate bare-userID exception) pairs a bare fingerprint with
// a bare userID, so the wire/verification layer must stay bare here too. DB
// storage still wants the canonical form like every other table, so
// canonicalize against owner right at this boundary, after verification
// and before any INSERT. owner is always a real, local identity here —
// this section handles only local subjects.
func insertRecoveryKeys(ctx context.Context, tx *sql.Tx, owner identityID, flat []recoveryFlatKey) error {
	canonicalFP := func(bare string) string {
		return string(appendEntity(owner, bare))
	}
	// Server signature ServerID on the wire is optional
	// (verifyRecoveryKeyCountersig only checks it if non-empty);
	// owner.ServerID() is always populated and is what verification
	// actually binds against, so use it here.
	ownerServerID := owner.ServerID()
	for i, fk := range flat {
		fingerprint := canonicalFP(fk.Key.Fingerprint)
		var predID interface{}
		if fk.PredecessorFingerprint != "" {
			predID = canonicalFP(fk.PredecessorFingerprint)
		}
		keyServerKeyID := string(canonicalID(ownerServerID, fk.Key.ServerSignature.Fingerprint))
		serverSigID, err := insertServerSignature(ctx, tx,
			keyServerKeyID,
			fk.Key.ServerSignature.Armor,
			fk.Key.ServerSignature.Timestamp,
		)
		if err != nil {
			return fmt.Errorf("insert key server signature %s: %w", fk.Key.Fingerprint, err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO public_keys (
				id, owner, armor, created_at,
				server_signature_id, predecessor_id
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (id) DO NOTHING
		`,
			fingerprint, owner, fk.Key.Armor,
			fk.Key.CreatedAt.UTC().Truncate(time.Second),
			serverSigID, predID,
		)
		if err != nil {
			return fmt.Errorf("insert key %s: %w", fk.Key.Fingerprint, err)
		}

		if fk.Revocation != nil {
			revocationFP := canonicalFP(fk.Revocation.Fingerprint)
			userSigID, err := insertUserSignature(ctx, tx, revocationFP, fk.Revocation.UserSignature.Armor)
			if err != nil {
				return fmt.Errorf("insert revocation user signature %s: %w", fk.Key.Fingerprint, err)
			}
			revServerKeyID := string(canonicalID(ownerServerID, fk.Revocation.ServerSignature.Fingerprint))
			serverSigID, err := insertServerSignature(ctx, tx,
				revServerKeyID,
				fk.Revocation.ServerSignature.Armor,
				fk.Revocation.ServerSignature.Timestamp,
			)
			if err != nil {
				return fmt.Errorf("insert revocation server signature %s: %w", fk.Key.Fingerprint, err)
			}
			_, err = tx.ExecContext(ctx, `
				INSERT INTO public_key_revocations (
					key_id, owner, reason,
					user_signature_id, server_signature_id
				) VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (key_id) DO NOTHING
			`,
				revocationFP, owner, fk.Revocation.Reason,
				userSigID, serverSigID,
			)
			if err != nil {
				return fmt.Errorf("insert revocation %s: %w", fk.Key.Fingerprint, err)
			}
		}

		// After inserting a newer key, point the predecessor revocation's
		// successor + the handoff signature proof onto that same row.
		if i > 0 {
			var successorSigID interface{}
			if fk.PredecessorSignature != "" {
				sigID, err := insertUserSignature(ctx, tx, canonicalFP(flat[i-1].Key.Fingerprint), fk.PredecessorSignature)
				if err != nil {
					return fmt.Errorf("insert successor signature for %s: %w", flat[i-1].Key.Fingerprint, err)
				}
				successorSigID = sigID
			}
			_, err := tx.ExecContext(ctx, `
				UPDATE public_key_revocations
				SET successor = $1, successor_signature_id = $2
				WHERE key_id = $3
				  AND (successor IS NULL OR successor = '')
			`, fingerprint, successorSigID, canonicalFP(flat[i-1].Key.Fingerprint))
			if err != nil {
				return fmt.Errorf("set successor for %s: %w", flat[i-1].Key.Fingerprint, err)
			}
		}
	}
	return nil
}

// claimRecoveryUsername resolves a username collision by deleting whichever
// side has the older (or equal) server_signed_at. If the incoming profile
// loses, the existing holder is left untouched and
// errRecoveryUsernameCollisionLoss is returned — callers must abort the
// whole upsert without writing anything. If the incoming profile wins (or
// there is no collision), the holder row (if any) is hard-deleted — ON
// DELETE CASCADE removes its keys, signatures, and recovery/social
// bookkeeping — and username is returned unchanged for the caller to store.
func claimRecoveryUsername(ctx context.Context, tx *sql.Tx, selfIdentity identityID, username string, signedAt time.Time) (string, error) {
	var holderIdentityID string
	var holderSignedAt time.Time
	// users.id IS identities.id directly now, so both the self-exclusion
	// comparison and the selected holder id must use that form — comparing
	// bare here would make "u.id <> $2" always-true, wrongly treating a
	// same-identity re-report as a collision.
	err := tx.QueryRowContext(ctx, `
		SELECT u.id, ss.signed_at
		FROM users u
		JOIN server_signatures ss ON ss.id = u.server_signature_id
		WHERE LOWER(u.username) = LOWER($1) AND u.id <> $2
		FOR UPDATE OF u
	`, username, selfIdentity).Scan(&holderIdentityID, &holderSignedAt)
	if err == sql.ErrNoRows {
		return username, nil
	}
	if err != nil {
		return "", err
	}

	holderWins := !signedAt.After(holderSignedAt)
	if holderWins {
		return "", errRecoveryUsernameCollisionLoss
	}

	// Incoming wins: the holder is a different, older, provably-signed
	// identity — not a duplicate of the incoming one. Deleting it is
	// destructive by design: a renamed-in-place row would carry a username
	// that no longer matches what its owner signed, permanently breaking
	// verification instead.
	//
	// Deletes FROM identities, not FROM users: identities is the actual FK
	// root, so ON DELETE CASCADE removes the satellite users row and
	// everything else (keys, signatures, recovery/social bookkeeping) in one shot.
	if _, err := tx.ExecContext(ctx, `DELETE FROM identities WHERE id = $1`, holderIdentityID); err != nil {
		return "", fmt.Errorf("delete username collision loser %s: %w", holderIdentityID, err)
	}
	if err := bumpActiveUsers(ctx, tx, -1); err != nil {
		return "", err
	}
	return username, nil
}

func nullIfEmptyString(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// drainRecoveryPendingFollows moves pending edges targeting targetUserID
// into the real follow tables, then deletes those pending rows.
// targetUserID is bare (pending_follows has no FK); targetIdentity is the
// same subject's identities.id, used for the fully-FK'd destination tables.
func drainRecoveryPendingFollows(ctx context.Context, tx *sql.Tx, targetUserID string, targetIdentity identityID) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_following (user_id, following_user_id)
		SELECT follower_user_id, $2
		FROM pending_follows
		WHERE following_user_id = $1
		ON CONFLICT DO NOTHING
	`, targetUserID, targetIdentity); err != nil {
		return fmt.Errorf("drain pending following: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_followers (user_id, follower_user_id)
		SELECT $2, follower_user_id
		FROM pending_follows
		WHERE following_user_id = $1
		ON CONFLICT DO NOTHING
	`, targetUserID, targetIdentity); err != nil {
		return fmt.Errorf("drain pending followers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM pending_follows WHERE following_user_id = $1
	`, targetUserID); err != nil {
		return fmt.Errorf("delete pending follows: %w", err)
	}
	return nil
}

// bindRecoveryClaimDeviceTx binds the claiming device in the own-identity
// claim transaction. Device binding is local-account-only, so ownerIdentity
// is always a local selfIdentity — same convention as BindDeviceTx.
func bindRecoveryClaimDeviceTx(ctx context.Context, tx *sql.Tx, ownerIdentity identityID, deviceID string, now time.Time) error {
	deviceID, err := parseDeviceID(deviceID)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE user_devices SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL
	`, ownerIdentity, now); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_devices (user_id, device_id, linked_at, revoked_at)
		VALUES ($1, $2, $3, NULL)
	`, ownerIdentity, deviceID, now)
	return err
}

// errRecoveryReedConflict is returned when an existing reed row's metadata
// does not match the countersigned submission (should be impossible under
// the bind).
var errRecoveryReedConflict = errors.New("reed metadata conflict")

// errRecoveryAuthorNotFound is returned when the reed author has no
// identities row.
var errRecoveryAuthorNotFound = errors.New("reed author not found")

// saveRecoveryReed inserts reed metadata if missing; rejects conflicting
// metadata; always upserts an allocation for reporterUserID. Caller must
// have verified the countersignature. Checks identities, not users, so a
// provisional row still works for a remote author. reedID is canonical
// (authorID@serverID/uuid); the author identity is recovered from it.
func saveRecoveryReed(ctx context.Context,
	db *sql.DB,
	serverID string,
	reedID, fingerprint string,
	signedAt time.Time,
	reporterUserID string,
	userFingerprint, userSignatureB64 string,
	serverSignatureB64 string,
) error {
	signedAt = signedAt.UTC().Truncate(time.Second)
	authorBare, authorServerID, _, ok := parseKeyFingerprint(identityID(reedID))
	if !ok {
		return fmt.Errorf("malformed reed id: %s", reedID)
	}
	authorIdentity := canonicalID(authorServerID, authorBare)
	reporterIdentity := canonicalID(serverID, reporterUserID)
	keyID := string(canonicalID(serverID, fingerprint))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identities WHERE id = $1)`, authorIdentity).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errRecoveryAuthorNotFound
	}

	var existingAuthor, existingKeyID string
	var existingAt time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT r.user_id, ss.private_key_id, r.signed_at
		FROM reeds r
		JOIN server_signatures ss ON ss.id = r.server_signature_id
		WHERE r.id = $1
		FOR UPDATE OF r
	`, reedID).Scan(&existingAuthor, &existingKeyID, &existingAt)

	switch {
	case err == sql.ErrNoRows:
		userSigID, err := insertUserSignature(ctx, tx, userFingerprint, userSignatureB64)
		if err != nil {
			return err
		}
		serverSigID, err := insertServerSignature(ctx, tx, keyID, serverSignatureB64, signedAt)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reeds (
				id, user_id, signed_at,
				user_signature_id, server_signature_id
			)
			VALUES ($1, $2, $3, $4, $5)
		`, reedID, authorIdentity, signedAt, userSigID, serverSigID); err != nil {
			return fmt.Errorf("insert reed: %w", err)
		}
	case err != nil:
		return err
	default:
		existingAt = existingAt.UTC().Truncate(time.Second)
		if existingAuthor != string(authorIdentity) || existingKeyID != keyID || !existingAt.Equal(signedAt) {
			log.Error().
				Str("reedID", reedID).
				Str("existingAuthor", existingAuthor).
				Str("existingKeyID", existingKeyID).
				Str("existingAt", existingAt.Format(time.RFC3339)).
				Str("incomingAuthor", string(authorIdentity)).
				Str("incomingKeyID", keyID).
				Str("incomingAt", signedAt.Format(time.RFC3339)).
				Msg("[ERR] recovery reed conflict")
			return errRecoveryReedConflict
		}
	}

	// reed_allocations.holder_user_id is a direct FK to identities(id);
	// reed_id FKs to reeds(id).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_allocations (reed_id, holder_user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, reedID, reporterIdentity); err != nil {
		return fmt.Errorf("insert reed allocation: %w", err)
	}

	return tx.Commit()
}

// saveRecoveryFollowing writes follow edges for followerUserID. Existing
// targets go into user_following / user_followers; missing targets go into
// pending_follows. Caller must reject self-follows before calling.
// followerUserID/targetIDs arrive already canonical (userID@serverID).
func saveRecoveryFollowing(ctx context.Context, db *sql.DB, serverID string, followerUserID string, targetIDs []string) error {
	if len(targetIDs) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	followerIdentity := identityID(followerUserID)

	// Check identities, not users, same reason as saveRecoveryReed above.
	existing := make(map[string]bool, len(targetIDs))
	targetIdentities := make(map[string]identityID, len(targetIDs))
	canonicalTargets := make([]string, 0, len(targetIDs))
	for _, targetID := range targetIDs {
		if targetID == "" {
			continue
		}
		targetIdentity := identityID(targetID)
		targetIdentities[targetID] = targetIdentity
		canonicalTargets = append(canonicalTargets, string(targetIdentity))
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM identities WHERE id = ANY($1)
	`, pq.Array(canonicalTargets))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		existing[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, targetID := range targetIDs {
		if targetID == "" {
			continue
		}
		targetIdentity := targetIdentities[targetID]
		if existing[string(targetIdentity)] {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO user_following (user_id, following_user_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, followerIdentity, targetIdentity); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO user_followers (user_id, follower_user_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, targetIdentity, followerIdentity); err != nil {
				return err
			}
			continue
		}
		// pending_follows.following_user_id has no FK (target may not
		// exist yet) and stays bare, matching drainRecoveryPendingFollows'
		// lookup.
		bareTargetID, _, ok := parseIdentityID(targetIdentities[targetID])
		if !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO pending_follows (follower_user_id, following_user_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, followerIdentity, bareTargetID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// recoveryErrorMessage is the JSON shape of a generic recovery error body.
type recoveryErrorMessage struct {
	Error string `json:"error"`
}

// recoveryAllowedDuringImport reports whether path may be used while the
// caller is in ongoing_recoveries. path is the request URL path (e.g.
// /api/server/info).
func recoveryAllowedDuringImport(path string) bool {
	if path == "/api/server/info" {
		return true
	}
	if path == "/api/users/status" {
		return true
	}
	if strings.HasPrefix(path, "/api/recovery/") {
		return true
	}
	if strings.HasPrefix(path, "/api/server/keys/") {
		return true
	}
	return false
}

// recoveryImportGateMiddleware returns the import-gate middleware. userIDKey
// is the context key signature-auth uses for the authenticated user id.
// isOngoing reports whether that user is mid-import. Authenticated users
// mid-import get 403 on non-allowlisted paths. OPTIONS always passes.
func recoveryImportGateMiddleware(userIDKey any, isOngoing func(context.Context, string) (bool, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			userID, ok := r.Context().Value(userIDKey).(string)
			if !ok || userID == "" {
				next.ServeHTTP(w, r)
				return
			}

			if recoveryAllowedDuringImport(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			ongoing, err := isOngoing(r.Context(), userID)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			if ongoing {
				writeResponse(w, http.StatusForbidden, recoveryErrorMessage{Error: "Finish recovery import first."})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ============ //
//   realtime   //
// ============ //

// MarkUserOnline marks a user as online in the database.
func (s *DataService) MarkUserOnline(ctx context.Context, userID string) error {
	selfIdentity := identityID(userID)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO online_users (user_id)
		VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE
		SET created_at = CURRENT_TIMESTAMP
	`, selfIdentity)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("[ERR] Failed to mark user as online")
		return err
	}
	return nil
}

// SetSyncRequestID stores the client-provided sync request ID for a user.
func (s *DataService) SetSyncRequestID(ctx context.Context, userID, requestID string) error {
	selfIdentity := identityID(userID)
	_, err := s.db.ExecContext(ctx, `
		UPDATE online_users SET sync_request_id = $1 WHERE user_id = $2
	`, requestID, selfIdentity)
	return err
}

// GetSyncRequestID returns the stored sync request ID for a user, or "" if not set.
func (s *DataService) GetSyncRequestID(ctx context.Context, userID string) (string, error) {
	selfIdentity := identityID(userID)
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(sync_request_id, '') FROM online_users WHERE user_id = $1
	`, selfIdentity).Scan(&id)
	return id, err
}

// MarkUserOffline marks a user as offline in the database.
func (s *DataService) MarkUserOffline(ctx context.Context, userID string) error {
	selfIdentity := identityID(userID)
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM online_users WHERE user_id = $1
	`, selfIdentity)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("[ERR] Failed to mark user as offline")
		return err
	}
	return nil
}

// GetRealtimeUserPublicKey retrieves a user's public key by canonical,
// self-scoping fingerprint — same shape as GetPublicKey.
func (s *DataService) GetRealtimeUserPublicKey(ctx context.Context, fingerprint string) (string, error) {
	var armor string
	err := s.db.QueryRowContext(ctx, `
		SELECT armor
		FROM public_keys
		WHERE id = $1
	`, fingerprint).Scan(&armor)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return armor, nil
}

// GetRealtimeUsername returns the current username for display on ephemeral
// deliveries. users.id IS identities.id directly, and userID here already
// arrives in that form, so this queries users.id directly, no join needed.
func (s *DataService) GetRealtimeUsername(ctx context.Context, userID string) (string, error) {
	var name sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, userID).Scan(&name); err != nil {
		return "", err
	}
	if !name.Valid {
		return "", sql.ErrNoRows
	}
	return name.String, nil
}

// SubscribeToBroadcast adds a user to the broadcast subscriptions table.
// broadcast_subscriptions.user_id has no direct FK to identities but is
// composite-FK'd to online_users(user_id), which is itself FK'd to identities(id).
func (s *DataService) SubscribeToBroadcast(ctx context.Context, userID string) error {
	selfIdentity := identityID(userID)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO broadcast_subscriptions (user_id)
		VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE
		SET created_at = CURRENT_TIMESTAMP
	`, selfIdentity)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("[ERR] Failed to subscribe user to broadcast")
		return err
	}
	return nil
}

// UnsubscribeFromBroadcast removes a user from the broadcast subscriptions table.
func (s *DataService) UnsubscribeFromBroadcast(ctx context.Context, userID string) error {
	selfIdentity := identityID(userID)
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM broadcast_subscriptions WHERE user_id = $1
	`, selfIdentity)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("[ERR] Failed to unsubscribe user from broadcast")
		return err
	}
	return nil
}

// GetOnlineFollowers returns the IDs of online users who follow the given author.
// online_users.user_id and user_followers.user_id/follower_user_id are all
// direct FKs to identities(id).
func (s *DataService) GetOnlineFollowers(ctx context.Context, authorID string) ([]string, error) {
	authorIdentity := identityID(authorID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT ou.user_id
		FROM online_users ou
		JOIN user_followers uf ON ou.user_id = uf.follower_user_id
		WHERE uf.user_id = $1
	`, authorIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var followers []string
	for rows.Next() {
		var userID identityID
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		followers = append(followers, string(userID))
	}
	return followers, nil
}

// GetOnlineAdmins returns the IDs of online users with role admin or root,
// excluding excludeUserID (the reed's own author, if they're an admin —
// they already hold via the author-dispatch path).
func (s *DataService) GetOnlineAdmins(ctx context.Context, excludeUserID string) ([]string, error) {
	excludeIdentity := identityID(excludeUserID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT ou.user_id
		FROM online_users ou
		JOIN users u ON ou.user_id = u.id
		WHERE u.role IN ($1, $2)
		  AND ou.user_id != $3
	`, roleRoot, roleAdmin, excludeIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var admins []string
	for rows.Next() {
		var userID identityID
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		admins = append(admins, string(userID))
	}
	return admins, nil
}

// pendingEvent represents a pending relay event stored in the database.
type pendingEvent struct {
	EventID         string
	RequestID       string
	RequesterUserID string
	EventName       string
}

// pendingReedEvent is a pending_events row with its pending_reed_events subject.
type pendingReedEvent struct {
	pendingEvent
	UserID string // author
	ReedID string
}

// pendingAccountEvent is a pending_events row with its pending_account_events subject.
type pendingAccountEvent struct {
	pendingEvent
	UserID string // removed account
}

// pendingSubject is the ACK/lookup view: reed and/or account fields depending on event_name.
type pendingSubject struct {
	pendingEvent
	UserID string // author (reed) or removed account
	ReedID string // set for reed events only
}

// CreatePendingReedEvent inserts pending_events + pending_reed_events (FK to reeds).
// requesterUserID is the viewer; authorUserID + reedID identify the reed subject,
// already in userID@serverID form. requesterUserID == "" means the event is
// foreign-attributed — no local online_users row can back it, so it's
// stored as SQL NULL; foreign_relay_requests is the source of truth for
// who it's really for (see CreateForeignRelayRequest).
func (s *DataService) CreatePendingReedEvent(ctx context.Context, eventID, requestID, requesterUserID string, eventName realtimeEventName, reedID string) error {
	requester := sql.NullString{String: requesterUserID, Valid: requesterUserID != ""}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name)
		VALUES ($1, $2, $3, $4)
	`, eventID, requestID, requester, eventName)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_reed_events (event_id, reed_id)
		VALUES ($1, $2)
	`, eventID, reedID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CreatePendingAccountEvent inserts pending_events + pending_account_events.
// pending_account_events.user_id is a direct FK to identities(id).
func (s *DataService) CreatePendingAccountEvent(ctx context.Context, eventID, requestID, requesterUserID, removedUserID string) error {
	requesterIdentity := identityID(requesterUserID)
	removedIdentity := identityID(removedUserID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name)
		VALUES ($1, $2, $3, $4)
	`, eventID, requestID, requesterIdentity, accountRemovedEvent)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_account_events (event_id, user_id)
		VALUES ($1, $2)
	`, eventID, removedIdentity)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CreateProfileSubscriptionEvent inserts a reed pending event tied to a
// profile subscription. requesterUserID == "" means foreign-attributed —
// see CreatePendingReedEvent's doc comment for the NULL convention.
func (s *DataService) CreateProfileSubscriptionEvent(ctx context.Context, eventID, requestID, requesterUserID string, eventName realtimeEventName, reedID, subscriptionID string) error {
	requester := sql.NullString{String: requesterUserID, Valid: requesterUserID != ""}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_events (event_id, request_id, requester_user_id, event_name, subscription_id)
		VALUES ($1, $2, $3, $4, $5)
	`, eventID, requestID, requester, eventName, subscriptionID)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pending_reed_events (event_id, reed_id)
		VALUES ($1, $2)
	`, eventID, reedID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetPendingSubject loads a pending event and its typed child subject by
// event ID. The user-id columns are scanned into identityID and kept in
// that form (cast to string, no .UserID() decode).
func (s *DataService) GetPendingSubject(ctx context.Context, eventID string) (*pendingSubject, error) {
	var pe pendingSubject
	var requester sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name
		FROM pending_events pe
		WHERE pe.event_id = $1
	`, eventID).Scan(&pe.EventID, &pe.RequestID, &requester, &pe.EventName)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pe.RequesterUserID = requester.String

	var subjectID identityID
	if realtimeEventName(pe.EventName) == accountRemovedEvent {
		err = s.db.QueryRowContext(ctx, `
			SELECT user_id FROM pending_account_events WHERE event_id = $1
		`, eventID).Scan(&subjectID)
		if err == nil {
			pe.UserID = string(subjectID)
		}
	} else {
		err = s.db.QueryRowContext(ctx, `
			SELECT reed_id FROM pending_reed_events WHERE event_id = $1
		`, eventID).Scan(&pe.ReedID)
		if err == nil {
			pe.UserID = reedAuthorIdentity(pe.ReedID)
		}
	}
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &pe, nil
}

// GetPendingReedEvent loads a reed-subject pending event (nil if missing or account event).
// requester_user_id is kept in userID@serverID form on return; pe.UserID
// (author) is derived from the canonical reed_id.
func (s *DataService) GetPendingReedEvent(ctx context.Context, eventID string) (*pendingReedEvent, error) {
	var pe pendingReedEvent
	var requester sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.reed_id
		FROM pending_events pe
		JOIN pending_reed_events pre ON pre.event_id = pe.event_id
		WHERE pe.event_id = $1
	`, eventID).Scan(&pe.EventID, &pe.RequestID, &requester, &pe.EventName, &pe.ReedID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pe.RequesterUserID = requester.String
	pe.UserID = reedAuthorIdentity(pe.ReedID)
	return &pe, nil
}

// reedAuthorIdentity extracts the userID@serverID author identity embedded
// in a canonical reed id. Empty string if reedID is malformed.
func reedAuthorIdentity(reedID string) string {
	authorID, ok := authorOf(identityID(reedID))
	if !ok {
		return ""
	}
	return string(authorID)
}

// DeletePendingEvent deletes a pending event by event ID (cascades to child
// subject tables) and reports its event_name for telemetry — empty string
// if no such row existed (already deleted by a concurrent caller).
func (s *DataService) DeletePendingEvent(ctx context.Context, eventID string) (eventName string, err error) {
	err = s.db.QueryRowContext(ctx, `
		DELETE FROM pending_events WHERE event_id = $1
		RETURNING event_name
	`, eventID).Scan(&eventName)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return eventName, err
}

// deletedPendingEvent identifies one row DeletePendingEventsByUser removed,
// for per-event telemetry.
type deletedPendingEvent struct {
	EventID   string
	EventName string
}

// DeletePendingEventsByUser deletes all pending events for a given requester
// user ID (e.g. the user went offline) and reports which ones were removed.
func (s *DataService) DeletePendingEventsByUser(ctx context.Context, userID string) ([]deletedPendingEvent, error) {
	selfIdentity := identityID(userID)
	rows, err := s.db.QueryContext(ctx, `
		DELETE FROM pending_events WHERE requester_user_id = $1
		RETURNING event_id, event_name
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deleted []deletedPendingEvent
	for rows.Next() {
		var d deletedPendingEvent
		if err := rows.Scan(&d.EventID, &d.EventName); err != nil {
			return nil, err
		}
		deleted = append(deleted, d)
	}
	return deleted, rows.Err()
}

// DeleteProfileSubscriptionsByViewer deletes all profile subscriptions for a given viewer.
func (s *DataService) DeleteProfileSubscriptionsByViewer(ctx context.Context, userID string) error {
	selfIdentity := identityID(userID)
	_, err := s.db.ExecContext(ctx, `DELETE FROM profile_subscriptions WHERE viewer_user_id = $1`, selfIdentity)
	return err
}

// viewerSubscription is one of a viewer's own active profile subscriptions
// (the reverse of profileSubscriber, which is keyed by author instead).
type viewerSubscription struct {
	SubscriptionID string
	AuthorUserID   string
}

// GetProfileSubscriptionsByViewer lists a viewer's active subscriptions —
// used on disconnect to notify any foreign authors' home servers before
// DeleteProfileSubscriptionsByViewer removes the local rows.
func (s *DataService) GetProfileSubscriptionsByViewer(ctx context.Context, userID string) ([]viewerSubscription, error) {
	selfIdentity := identityID(userID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT subscription_id, author_user_id
		FROM profile_subscriptions
		WHERE viewer_user_id = $1
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []viewerSubscription
	for rows.Next() {
		var sub viewerSubscription
		var author identityID
		if err := rows.Scan(&sub.SubscriptionID, &author); err != nil {
			return nil, err
		}
		sub.AuthorUserID = string(author)
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// reedCoverageTarget identifies a reed whose holder count changed.
type reedCoverageTarget struct {
	AuthorUserID string
	ReedID       string
}

// AllocateReed records that holderUserID now holds reedID. Returns true
// when a new allocation row was inserted. holderUserID must be a genuine
// local user — reed_allocations.holder_user_id is a direct FK to users(id).
func (s *DataService) AllocateReed(ctx context.Context, reedID, holderUserID string) (bool, error) {
	holderIdentity := identityID(holderUserID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO reed_allocations (reed_id, holder_user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, reedID, holderIdentity)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// DeleteReedAllocation removes a single holder's allocation for a reed.
// Returns true when a row was deleted.
func (s *DataService) DeleteReedAllocation(ctx context.Context, reedID, holderUserID string) (bool, error) {
	holderIdentity := identityID(holderUserID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		DELETE FROM reed_allocations
		WHERE reed_id = $1 AND holder_user_id = $2
	`, reedID, holderIdentity)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetReedCoverage returns holder count and network coverage percent for a
// tip reed, read from the reed_coverage view (db.go) — a join of
// reed_stats.holder_count against network_stats.active_users, both
// already-cheap counters, so the view needs no trigger of its own.
func (s *DataService) GetReedCoverage(ctx context.Context, reedID string) (holders, percent int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT holder_count, coverage_percent FROM reed_coverage WHERE reed_id = $1
	`, reedID).Scan(&holders, &percent)
	if err == sql.ErrNoRows {
		activeUsers, aErr := getActiveUsers(ctx, s.db)
		if aErr != nil {
			return 0, 0, aErr
		}
		return 0, coveragePercent(0, activeUsers), nil
	}
	if err != nil {
		return 0, 0, err
	}
	return holders, percent, nil
}

// GetReedCoveragePercent returns network coverage percent for a tip reed.
func (s *DataService) GetReedCoveragePercent(ctx context.Context, reedID string) (percent int, err error) {
	_, percent, err = s.GetReedCoverage(ctx, reedID)
	return percent, err
}

// GetReedStatsSnapshot returns echoes, coverage, subtree reply count, and
// like count for subscribe ACK.
func (s *DataService) GetReedStatsSnapshot(ctx context.Context, reedID string) (echoes, coveragePct, replies, likes int, err error) {
	coveragePct, err = s.GetReedCoveragePercent(ctx, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	echoes, err = s.CountEchoes(ctx, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	replies, err = s.GetSubtreeReplyCount(ctx, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	likes, err = s.CountLikes(ctx, reedID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return echoes, coveragePct, replies, likes, nil
}

// ReplyParent returns the immediate parent_reed_id that reedID replies to,
// if it's indexed as a reply at all — ok is false when it isn't.
func (s *DataService) ReplyParent(ctx context.Context, reedID string) (parentReedID string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT parent_reed_id
		FROM reed_replies
		WHERE reed_id = $1
	`, reedID).Scan(&parentReedID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return parentReedID, true, nil
}

// replyRecord is one reed's full reed_replies row.
type replyRecord struct {
	ParentReedID string
	ThreadID     string
	Timestamp    time.Time
}

// GetReplyRecord loads reedID's own reed_replies row (parent + thread +
// timestamp in one query) — used to notify a foreign parent's home
// server of the reply once, rather than three separate lookups.
func (s *DataService) GetReplyRecord(ctx context.Context, reedID string) (*replyRecord, error) {
	var rec replyRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT parent_reed_id, thread_id, timestamp
		FROM reed_replies
		WHERE reed_id = $1
	`, reedID).Scan(&rec.ParentReedID, &rec.ThreadID, &rec.Timestamp)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// InsertForeignReply records that a peer-authored reedID replies to
// parentReedID (local to this server). Idempotent (ON CONFLICT DO NOTHING
// on reed_id, the PK). Upserts a reed_identities row for the reply reedID first.
func (s *DataService) InsertForeignReply(ctx context.Context, parentReedID, replyReedID, threadID string, ts time.Time) error {
	_, replyServerID, _, ok := parseKeyFingerprint(identityID(replyReedID))
	if !ok {
		return fmt.Errorf("malformed reply reed id: %s", replyReedID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_identities (id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, replyReedID, replyServerID); err != nil {
		return fmt.Errorf("insert foreign reply reed identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_replies (thread_id, reed_id, parent_reed_id, timestamp)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (reed_id) DO NOTHING
	`, threadID, replyReedID, parentReedID, ts.UTC().Truncate(time.Second)); err != nil {
		return fmt.Errorf("insert foreign reply: %w", err)
	}
	return tx.Commit()
}

// GetNextPendingForHolder returns the oldest undispatched reed pending for reeds held by holderUserID.
func (s *DataService) GetNextPendingForHolder(ctx context.Context, holderUserID string) (*pendingReedEvent, error) {
	holderIdentity := identityID(holderUserID)
	var pe pendingReedEvent
	var requester sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		JOIN reed_allocations ra ON ra.reed_id = pre.reed_id
		WHERE ra.holder_user_id = $1
		  AND pe.dispatched_at IS NULL
		ORDER BY pe.created_at
		LIMIT 1
	`, holderIdentity).Scan(&pe.EventID, &pe.RequestID, &requester, &pe.EventName, &pe.ReedID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pe.RequesterUserID = requester.String
	pe.UserID = reedAuthorIdentity(pe.ReedID)
	return &pe, nil
}

// MarkEventDispatched marks an event as dispatched. Returns true if the update claimed the row
// (i.e. it was still undispatched), false if another replica already claimed it.
func (s *DataService) MarkEventDispatched(ctx context.Context, eventID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE pending_events SET dispatched_at = NOW()
		WHERE event_id = $1 AND dispatched_at IS NULL
	`, eventID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// ResetDispatchedAt clears dispatched_at for an event, making it eligible for dispatch again.
func (s *DataService) ResetDispatchedAt(ctx context.Context, eventID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE pending_events SET dispatched_at = NULL WHERE event_id = $1
	`, eventID)
	return err
}

// GetOnlineHolders reports whether a reed has any holders and returns one online holder
// for relay dispatch when available. Callers must delete stale holder rows (e.g. the
// requester) before calling when appropriate.
func (s *DataService) GetOnlineHolders(ctx context.Context, reedID string) (hasHolders bool, holder string, err error) {
	var onlineHolder sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM reed_allocations WHERE reed_id = $1
			),
			(
				SELECT ou.user_id
				FROM reed_allocations ra
				JOIN online_users ou ON ou.user_id = ra.holder_user_id
				WHERE ra.reed_id = $1
				LIMIT 1
			)
	`, reedID).Scan(&hasHolders, &onlineHolder)
	if err != nil {
		return false, "", err
	}
	if onlineHolder.Valid {
		holder = onlineHolder.String
	}
	return hasHolders, holder, nil
}

// GetOnlineReedHolder returns the user ID of one online holder of the given reed,
// or an empty string if no holder is currently online.
func (s *DataService) GetOnlineReedHolder(ctx context.Context, reedID string) (string, error) {
	var userID identityID
	err := s.db.QueryRowContext(ctx, `
		SELECT ou.user_id FROM online_users ou
		JOIN reed_allocations ra ON ra.holder_user_id = ou.user_id
		WHERE ra.reed_id = $1
		LIMIT 1
	`, reedID).Scan(&userID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(userID), nil
}

// RecordServerHolder upserts "peer server serverID holds a copy of
// reedID," idempotent per (reed_id, server_id) — multiple users on the
// same peer collapse to one row, since the fallback delegates to the peer as a whole.
func (s *DataService) RecordServerHolder(ctx context.Context, reedID, serverID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reed_server_allocations (reed_id, server_id)
		VALUES ($1, $2)
		ON CONFLICT (reed_id, server_id) DO NOTHING
	`, reedID, serverID)
	return err
}

// GetForeignHolderServers returns peer server IDs known to hold a copy of
// reedID, oldest-recorded-first, capped so a widely-relayed reed can't
// blow up a sequential fallback loop's latency.
func (s *DataService) GetForeignHolderServers(ctx context.Context, reedID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT server_id FROM reed_server_allocations
		WHERE reed_id = $1
		ORDER BY delivered_at ASC
		LIMIT 5
	`, reedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var serverIDs []string
	for rows.Next() {
		var serverID string
		if err := rows.Scan(&serverID); err != nil {
			return nil, err
		}
		serverIDs = append(serverIDs, serverID)
	}
	return serverIDs, rows.Err()
}

// ClaimPendingFanout removes the pending_fanout row if present. Returns true when
// this call claimed fanout (row deleted), plus any pipe tags stashed at SignReed.
// Concurrent READY messages only claim once. pending_fanout.reed_id FKs to reeds(id).
func (s *DataService) ClaimPendingFanout(ctx context.Context, reedID string) (claimed bool, tags []string, err error) {
	var id string
	var tagArray pq.StringArray
	err = s.db.QueryRowContext(ctx, `
		DELETE FROM pending_fanout
		WHERE reed_id = $1
		RETURNING reed_id, tags
	`, reedID).Scan(&id, &tagArray)
	if err == sql.ErrNoRows {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, []string(tagArray), nil
}

// GetPendingEventsForUser returns all pending reed events for reeds held by the given user.
func (s *DataService) GetPendingEventsForUser(ctx context.Context, userID string) ([]pendingReedEvent, error) {
	selfIdentity := identityID(userID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		JOIN reed_allocations ra ON ra.reed_id = pre.reed_id
		WHERE ra.holder_user_id = $1
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []pendingReedEvent
	for rows.Next() {
		var prr pendingReedEvent
		var requester sql.NullString
		if err := rows.Scan(
			&prr.EventID,
			&prr.RequestID,
			&requester,
			&prr.EventName,
			&prr.ReedID,
		); err != nil {
			return nil, err
		}
		prr.RequesterUserID = requester.String
		prr.UserID = reedAuthorIdentity(prr.ReedID)
		results = append(results, prr)
	}
	return results, nil
}

// GetPendingRequestsForRequester returns pending reed events initiated by the given user
// (reed relay retry only — not account events).
func (s *DataService) GetPendingRequestsForRequester(ctx context.Context, requesterUserID string) ([]pendingReedEvent, error) {
	requesterIdentity := identityID(requesterUserID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT pe.event_id, pe.request_id, pe.requester_user_id, pe.event_name, pre.reed_id
		FROM pending_reed_events pre
		JOIN pending_events pe ON pe.event_id = pre.event_id
		WHERE pe.requester_user_id = $1
	`, requesterIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []pendingReedEvent
	for rows.Next() {
		var prr pendingReedEvent
		var requester sql.NullString
		if err := rows.Scan(
			&prr.EventID,
			&prr.RequestID,
			&requester,
			&prr.EventName,
			&prr.ReedID,
		); err != nil {
			return nil, err
		}
		prr.RequesterUserID = requester.String
		prr.UserID = reedAuthorIdentity(prr.ReedID)
		results = append(results, prr)
	}
	return results, nil
}

// GetMissingReedIDsForViewer returns IDs of reeds by authorID that viewerID does not yet have,
// excluding any IDs the viewer already holds locally (ownedIDs) or via reed_allocations.
func (s *DataService) GetMissingReedIDsForViewer(ctx context.Context, authorID, viewerID string, ownedIDs []string) ([]string, error) {
	if ownedIDs == nil {
		ownedIDs = []string{}
	}
	authorIdentity := identityID(authorID)
	viewerIdentity := identityID(viewerID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id FROM reeds r
		WHERE r.user_id = $1
		  AND r.id <> ALL($3)
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_allocations ra
		      WHERE ra.reed_id = r.id AND ra.holder_user_id = $2
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.reed_id = r.id
		  )
	`, authorIdentity, viewerIdentity, pq.Array(ownedIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// unallocatedReed holds a reed ID and its author, used when computing delivery diffs.
type unallocatedReed struct {
	ReedID   string
	AuthorID string
}

// GetMissingOut returns all reeds from authors that userID follows
// which are not yet present in reed_allocations for that user.
// user_following.user_id/following_user_id are both direct FKs to identities(id).
func (s *DataService) GetMissingOut(ctx context.Context, userID string) ([]unallocatedReed, error) {
	selfIdentity := identityID(userID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.user_id
		FROM reeds r
		JOIN user_following uf ON uf.following_user_id = r.user_id
		WHERE uf.user_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_allocations ra
		      WHERE ra.reed_id = r.id AND ra.holder_user_id = $1
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.reed_id = r.id
		  )
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []unallocatedReed
	for rows.Next() {
		var reedID string
		var authorIdentity identityID
		if err := rows.Scan(&reedID, &authorIdentity); err != nil {
			return nil, err
		}
		results = append(results, unallocatedReed{ReedID: reedID, AuthorID: string(authorIdentity)})
	}
	return results, nil
}

// GetUnallocatedReeds returns IDs of reeds by authorID that viewerID does not have in reed_allocations.
func (s *DataService) GetUnallocatedReeds(ctx context.Context, authorID, viewerID string) ([]string, error) {
	authorIdentity := identityID(authorID)
	viewerIdentity := identityID(viewerID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id FROM reeds r
		WHERE r.user_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_allocations ra
		      WHERE ra.reed_id = r.id AND ra.holder_user_id = $2
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.reed_id = r.id
		  )
	`, authorIdentity, viewerIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetUnallocatedReedsForServer is GetUnallocatedReeds' server-scoped
// counterpart: used where the "viewer" is a whole peer server rather than
// a genuine local user, since reed_server_allocations has no per-user granularity.
func (s *DataService) GetUnallocatedReedsForServer(ctx context.Context, authorID, serverID string) ([]string, error) {
	authorIdentity := identityID(authorID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id FROM reeds r
		WHERE r.user_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_server_allocations rsa
		      WHERE rsa.reed_id = r.id AND rsa.server_id = $2
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.reed_id = r.id
		  )
	`, authorIdentity, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// CreateProfileSubscription records an active profile feed subscription for
// a viewer, returning the effective subscription_id. Idempotent per
// (viewer_user_id, author_user_id) — reuses the existing id on conflict, so callers must use the returned id, not the one they passed in.
func (s *DataService) CreateProfileSubscription(ctx context.Context, subscriptionID, viewerUserID, authorUserID string) (string, error) {
	viewerIdentity := identityID(viewerUserID)
	authorIdentity := identityID(authorUserID)
	var effectiveID string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO profile_subscriptions (subscription_id, viewer_user_id, author_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (viewer_user_id, author_user_id)
			DO UPDATE SET viewer_user_id = EXCLUDED.viewer_user_id
		RETURNING subscription_id
	`, subscriptionID, viewerIdentity, authorIdentity).Scan(&effectiveID)
	return effectiveID, err
}

// GetProfileSubscription returns the subscription ID for an active (viewer, author) pair.
// Returns an empty string when no subscription exists.
func (s *DataService) GetProfileSubscription(ctx context.Context, viewerUserID, authorUserID string) (string, error) {
	viewerIdentity := identityID(viewerUserID)
	authorIdentity := identityID(authorUserID)
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT subscription_id FROM profile_subscriptions
		WHERE viewer_user_id = $1 AND author_user_id = $2
	`, viewerIdentity, authorIdentity).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// DeleteProfileSubscription deletes a subscription by ID, cascading to its pending_events.
func (s *DataService) DeleteProfileSubscription(ctx context.Context, subscriptionID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM profile_subscriptions WHERE subscription_id = $1
	`, subscriptionID)
	return err
}

// CreateReedSubscription records an active reed-stats subscription for a
// viewer. reed_subscriptions.reed_id FKs to reed_identities (not reeds
// directly), so the subscribed reed may be local or foreign.
func (s *DataService) CreateReedSubscription(ctx context.Context, subscriptionID, viewerUserID, reedID string) error {
	viewerIdentity := identityID(viewerUserID)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reed_subscriptions (subscription_id, viewer_user_id, reed_id)
		VALUES ($1, $2, $3)
	`, subscriptionID, viewerIdentity, reedID)
	return err
}

// GetReedSubscription returns the subscription ID for an active (viewer,
// reed) pair. Returns an empty string when no subscription exists.
func (s *DataService) GetReedSubscription(ctx context.Context, viewerUserID, reedID string) (string, error) {
	viewerIdentity := identityID(viewerUserID)
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT subscription_id FROM reed_subscriptions
		WHERE viewer_user_id = $1 AND reed_id = $2
	`, viewerIdentity, reedID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// DeleteReedSubscription deletes a reed-stats subscription by ID.
func (s *DataService) DeleteReedSubscription(ctx context.Context, subscriptionID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_subscriptions WHERE subscription_id = $1
	`, subscriptionID)
	return err
}

// DeleteReedSubscriptionsByViewer deletes all reed-stats subscriptions for a given viewer.
func (s *DataService) DeleteReedSubscriptionsByViewer(ctx context.Context, userID string) error {
	selfIdentity := identityID(userID)
	_, err := s.db.ExecContext(ctx, `DELETE FROM reed_subscriptions WHERE viewer_user_id = $1`, selfIdentity)
	return err
}

// reedSubscriber represents an active reed-stats subscription.
type reedSubscriber struct {
	SubscriptionID string
	ViewerUserID   string
}

// GetReedSubscribers returns all active reed-stats subscriptions for the given reed.
func (s *DataService) GetReedSubscribers(ctx context.Context, reedID string) ([]reedSubscriber, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT subscription_id, viewer_user_id
		FROM reed_subscriptions
		WHERE reed_id = $1
	`, reedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscribers []reedSubscriber
	for rows.Next() {
		var sub reedSubscriber
		var viewer identityID
		if err := rows.Scan(&sub.SubscriptionID, &viewer); err != nil {
			return nil, err
		}
		sub.ViewerUserID = string(viewer)
		subscribers = append(subscribers, sub)
	}
	return subscribers, rows.Err()
}

// viewerReedSubscription is one of a viewer's own active reed-stats
// subscriptions (the reverse of reedSubscriber, which is keyed by reed).
type viewerReedSubscription struct {
	SubscriptionID string
	ReedID         string
}

// GetReedSubscriptionsByViewer lists a viewer's active reed-stats
// subscriptions — used on disconnect to notify any foreign reeds' home
// servers before DeleteReedSubscriptionsByViewer removes the local rows.
func (s *DataService) GetReedSubscriptionsByViewer(ctx context.Context, userID string) ([]viewerReedSubscription, error) {
	selfIdentity := identityID(userID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT subscription_id, reed_id
		FROM reed_subscriptions
		WHERE viewer_user_id = $1
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []viewerReedSubscription
	for rows.Next() {
		var sub viewerReedSubscription
		if err := rows.Scan(&sub.SubscriptionID, &sub.ReedID); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// profileSubscriber represents an active profile feed subscription.
type profileSubscriber struct {
	SubscriptionID string
	ViewerUserID   string
}

// GetProfileSubscribers returns all active profile subscriptions for the given author.
func (s *DataService) GetProfileSubscribers(ctx context.Context, authorID string) ([]profileSubscriber, error) {
	authorIdentity := identityID(authorID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT subscription_id, viewer_user_id
		FROM profile_subscriptions
		WHERE author_user_id = $1
	`, authorIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscribers []profileSubscriber
	for rows.Next() {
		var subscriber profileSubscriber
		var viewer identityID
		if err := rows.Scan(&subscriber.SubscriptionID, &viewer); err != nil {
			return nil, err
		}
		subscriber.ViewerUserID = string(viewer)
		subscribers = append(subscribers, subscriber)
	}
	return subscribers, nil
}

// GetBroadcastSubscribers returns up to 100 broadcast subscribers for the
// given author, throttled to one delivery/second and excluding followers.
// NOTE: the last_delivery UPDATE is not serialised across replicas — concurrent replicas can double-deliver to up to 100 users; acceptable, harmless.
func (s *DataService) GetBroadcastSubscribers(ctx context.Context, authorID string) ([]string, error) {
	authorIdentity := identityID(authorID)
	rows, err := s.db.QueryContext(ctx, `
		WITH eligible AS (
			SELECT bs.user_id
			FROM broadcast_subscriptions bs
			WHERE bs.user_id != $1
			  AND (bs.last_delivery IS NULL OR bs.last_delivery < NOW() - INTERVAL '1 second')
			  AND NOT EXISTS (
				SELECT 1 FROM user_following uf
				WHERE uf.user_id = bs.user_id AND uf.following_user_id = $1
			  )
			ORDER BY bs.last_delivery ASC NULLS FIRST
			LIMIT 100
		),
		updated AS (
			UPDATE broadcast_subscriptions
			SET last_delivery = NOW()
			WHERE user_id IN (SELECT user_id FROM eligible)
		)
		SELECT user_id FROM eligible
	`, authorIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscribers []string
	for rows.Next() {
		var userID identityID
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		subscribers = append(subscribers, string(userID))
	}
	return subscribers, nil
}

// missingRemoval is a reed_allocations ∩ reed_removals row for catch-up.
type missingRemoval struct {
	ReedID string
	UserID string
	Cert   reedRemovalWire
}

// GetMissingRemovals returns removal certs for reeds this user still holds.
// missingRemoval.UserID comes from cert.UserID, in userID@serverID form.
func (s *DataService) GetMissingRemovals(ctx context.Context, userID string) ([]missingRemoval, error) {
	selfIdentity := identityID(userID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT rr.reed_id
		FROM reed_allocations ra
		JOIN reed_removals rr ON rr.reed_id = ra.reed_id
		WHERE ra.holder_user_id = $1
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	serverID := s.serverID

	var out []missingRemoval
	for rows.Next() {
		var reedID string
		if err := rows.Scan(&reedID); err != nil {
			return nil, err
		}
		cert, err := getReedRemovalCert(ctx, s.db, reedID, serverID)
		if err != nil || cert == nil {
			return nil, err
		}
		out = append(out, missingRemoval{
			ReedID: reedID,
			UserID: cert.UserID,
			Cert:   newReedRemovalWire(serverID, *cert),
		})
	}
	return out, rows.Err()
}

// GetReedRemovalWire loads a removal cert for WS delivery. reedID is canonical.
func (s *DataService) GetReedRemovalWire(ctx context.Context, reedID string) (reedRemovalWire, error) {
	cert, err := getReedRemovalCert(ctx, s.db, reedID, s.serverID)
	if err != nil || cert == nil {
		return reedRemovalWire{}, err
	}
	return newReedRemovalWire(s.serverID, *cert), nil
}

// missingAccountRemoval is a catch-up row: viewer still follows or holds
// allocations for a removed author's reeds.
type missingAccountRemoval struct {
	UserID string
	Cert   accountRemovalWire
}

// GetMissingAccountRemovals returns account_removals that still apply to
// viewer (follow ∪ allocations for that author's reeds). account_removals.user_id
// is written in the same userID@serverID form the EXISTS subqueries expect.
func (s *DataService) GetMissingAccountRemovals(ctx context.Context, viewerUserID string) ([]missingAccountRemoval, error) {
	selfIdentity := identityID(viewerUserID)
	serverID := s.serverID
	rows, err := s.db.QueryContext(ctx, `
		SELECT ar.user_id
		FROM account_removals ar
		WHERE EXISTS (
			SELECT 1 FROM user_following uf
			WHERE uf.user_id = $1 AND uf.following_user_id = ar.user_id
		) OR EXISTS (
			SELECT 1 FROM reed_allocations ra
			JOIN reeds r ON r.id = ra.reed_id
			WHERE ra.holder_user_id = $1 AND r.user_id = ar.user_id
		)
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []missingAccountRemoval
	for rows.Next() {
		var removedIdentity identityID
		if err := rows.Scan(&removedIdentity); err != nil {
			return nil, err
		}
		// getAccountRemovalCert takes a bare userID + serverID — decode
		// only for this call; UserID stays in userID@serverID form.
		cert, err := getAccountRemovalCert(ctx, s.db, removedIdentity.UserID(), serverID)
		if err != nil || cert == nil {
			return nil, err
		}
		out = append(out, missingAccountRemoval{
			UserID: cert.UserID,
			Cert:   newAccountRemovalWire(serverID, *cert),
		})
	}
	return out, rows.Err()
}

// GetAccountRemovalWire loads an account-removal cert for WS delivery.
// userID arrives in userID@serverID form; getAccountRemovalCert takes a
// bare userID + serverID — decoded here.
func (s *DataService) GetAccountRemovalWire(ctx context.Context, userID string) (accountRemovalWire, error) {
	cert, err := getAccountRemovalCert(ctx, s.db, identityID(userID).UserID(), s.serverID)
	if err != nil || cert == nil {
		return accountRemovalWire{}, err
	}
	return newAccountRemovalWire(s.serverID, *cert), nil
}

// ClearPeerStateForRemovedAccount drops follow edges and allocations so
// catch-up no longer re-delivers the account cert to this viewer. Returns
// reeds whose holder counts changed.
func (s *DataService) ClearPeerStateForRemovedAccount(ctx context.Context, viewerUserID, removedUserID string) ([]reedCoverageTarget, error) {
	viewerIdentity := identityID(viewerUserID)
	removedIdentity := identityID(removedUserID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	stmts := []struct {
		q  string
		a1 identityID
		a2 identityID
	}{
		{`DELETE FROM user_following WHERE user_id = $1 AND following_user_id = $2`, viewerIdentity, removedIdentity},
		{`DELETE FROM user_following WHERE user_id = $1 AND following_user_id = $2`, removedIdentity, viewerIdentity},
		{`DELETE FROM user_followers WHERE user_id = $1 AND follower_user_id = $2`, removedIdentity, viewerIdentity},
		{`DELETE FROM user_followers WHERE user_id = $1 AND follower_user_id = $2`, viewerIdentity, removedIdentity},
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s.q, s.a1, s.a2); err != nil {
			return nil, err
		}
	}

	rows, err := tx.QueryContext(ctx, `
		DELETE FROM reed_allocations ra
		USING reeds r
		WHERE ra.reed_id = r.id AND ra.holder_user_id = $1 AND r.user_id = $2
		RETURNING r.user_id, ra.reed_id
	`, viewerIdentity, removedIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []reedCoverageTarget
	for rows.Next() {
		var t reedCoverageTarget
		var author identityID
		if err := rows.Scan(&author, &t.ReedID); err != nil {
			return nil, err
		}
		t.AuthorUserID = string(author)
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return targets, nil
}

// foreignPendingEvent is an originating-server foreign_pending_events row:
// the mapping from a local pending_events.event_id to the outstanding
// registration on the reed's home server (which peer to call back, and what id THEY know this event by).
type foreignPendingEvent struct {
	EventID      string
	HomeServerID string
	PeerEventID  string
}

// CreateForeignPendingEvent records, on the originating server, that
// eventID's local pending_events row corresponds to peerEventID on
// homeServerID. eventID must already exist in pending_events (FK).
func (s *DataService) CreateForeignPendingEvent(ctx context.Context, eventID, homeServerID, peerEventID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO foreign_pending_events (event_id, home_server_id, peer_event_id)
		VALUES ($1, $2, $3)
	`, eventID, homeServerID, peerEventID)
	return err
}

// GetForeignPendingEvent resolves the originating server's own event_id to
// its foreign_pending_events row — the reverse direction of
// GetForeignPendingEventByPeerEventID.
func (s *DataService) GetForeignPendingEvent(ctx context.Context, eventID string) (*foreignPendingEvent, error) {
	var fpe foreignPendingEvent
	err := s.db.QueryRowContext(ctx, `
		SELECT event_id, home_server_id, peer_event_id
		FROM foreign_pending_events
		WHERE event_id = $1
	`, eventID).Scan(&fpe.EventID, &fpe.HomeServerID, &fpe.PeerEventID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &fpe, nil
}

// GetForeignPendingEventByPeerEventID resolves a home server's callback
// back to the originating server's local event. The homeServerID filter
// doubles as an ownership check that only the registered peer may resolve it.
func (s *DataService) GetForeignPendingEventByPeerEventID(ctx context.Context, peerEventID, homeServerID string) (*foreignPendingEvent, error) {
	var fpe foreignPendingEvent
	err := s.db.QueryRowContext(ctx, `
		SELECT event_id, home_server_id, peer_event_id
		FROM foreign_pending_events
		WHERE peer_event_id = $1 AND home_server_id = $2
	`, peerEventID, homeServerID).Scan(&fpe.EventID, &fpe.HomeServerID, &fpe.PeerEventID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &fpe, nil
}

// GetForeignPendingEventsByRequester lists a requester's outstanding
// cross-server relay requests, for disconnect-cleanup to notify each home
// server before the requester's pending_events rows cascade-delete.
func (s *DataService) GetForeignPendingEventsByRequester(ctx context.Context, requesterUserID string) ([]foreignPendingEvent, error) {
	requesterIdentity := identityID(requesterUserID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT fpe.event_id, fpe.home_server_id, fpe.peer_event_id
		FROM foreign_pending_events fpe
		JOIN pending_events pe ON pe.event_id = fpe.event_id
		WHERE pe.requester_user_id = $1
	`, requesterIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []foreignPendingEvent
	for rows.Next() {
		var fpe foreignPendingEvent
		if err := rows.Scan(&fpe.EventID, &fpe.HomeServerID, &fpe.PeerEventID); err != nil {
			return nil, err
		}
		out = append(out, fpe)
	}
	return out, rows.Err()
}

// foreignRelayRequest is a home-server foreign_relay_requests row: which
// peer+user a sentinel-attributed pending_events row was really
// registered on behalf of.
type foreignRelayRequest struct {
	EventID            string
	RequestingServerID string
	RequestingUserID   string
}

// CreateForeignRelayRequest records, on the home server, which peer+user
// eventID's sentinel-attributed pending_events row represents.
func (s *DataService) CreateForeignRelayRequest(ctx context.Context, eventID, requestingServerID, requestingUserID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO foreign_relay_requests (event_id, requesting_server_id, requesting_user_id)
		VALUES ($1, $2, $3)
	`, eventID, requestingServerID, requestingUserID)
	return err
}

// GetForeignRelayRequest returns nil if eventID is an ordinary local
// event (no cross-server registration exists for it).
func (s *DataService) GetForeignRelayRequest(ctx context.Context, eventID string) (*foreignRelayRequest, error) {
	var frr foreignRelayRequest
	err := s.db.QueryRowContext(ctx, `
		SELECT event_id, requesting_server_id, requesting_user_id
		FROM foreign_relay_requests
		WHERE event_id = $1
	`, eventID).Scan(&frr.EventID, &frr.RequestingServerID, &frr.RequestingUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &frr, nil
}

// DeleteMailboxMessage deletes one message, scoped to userID so a client
// can only ack/delete its own mail.
func (s *DataService) DeleteMailboxMessage(ctx context.Context, id, userID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM user_mailbox WHERE id = $1 AND user_id = $2
	`, id, userID)
	return err
}
