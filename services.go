//go:build !ops

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"syrinx/coverage"
	"syrinx/crypto"
	"syrinx/deletion"
	"syrinx/identity"
	"syrinx/invites"
	"syrinx/recovery"
	"syrinx/roles"
	"syrinx/signing"

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
	crypto *crypto.Service
	log    *LoggingService
	md     *MarkdownService
}

// =============== //
//   UserService   //
// =============== //

type DataService struct {
	db         *sql.DB
	serverName string
	serverID   string
	invites    *invites.Store
}

func NewDataService(db *sql.DB, serverName string) *DataService {
	return &DataService{
		db:         db,
		serverName: serverName,
		invites:    &invites.Store{DB: db},
	}
}

func (s *DataService) GetServerID() string {
	return s.serverID
}

// UserServerSignedAt returns the identity countersignature time for userID.
// Returns sql.ErrNoRows when the user does not exist.
func (s *DataService) UserServerSignedAt(ctx context.Context, userID string) (time.Time, error) {
	var ts time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT ss.signed_at
		FROM users u
		JOIN server_signatures ss ON ss.id = u.server_signature_id
		WHERE u.id = $1
	`, userID).Scan(&ts)
	if err != nil {
		return time.Time{}, err
	}
	return ts.UTC().Truncate(time.Second), nil
}

// IsUnclaimed reports whether userID is still in the peer-seeded gauge.
func (s *DataService) IsUnclaimed(ctx context.Context, userID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM unclaimed_accounts WHERE user_id = $1)
	`, userID).Scan(&exists)
	return exists, err
}

// IsOngoing reports whether userID is mid-recovery import.
func (s *DataService) IsOngoing(ctx context.Context, userID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM ongoing_recoveries WHERE user_id = $1)
	`, userID).Scan(&exists)
	return exists, err
}

func generateServerID() (string, error) {
	return crypto.NewID()
}

func generateUserID() (string, error) {
	return crypto.NewID()
}

func (s *DataService) InitServer(ctx context.Context, recoveryMode bool) error {
	var id, name string

	err := s.db.QueryRowContext(ctx, `SELECT id, name FROM servers WHERE self = TRUE`).Scan(&id, &name)
	if err == sql.ErrNoRows {
		if recoveryMode {
			return recovery.ErrNoIdentityFound
		}
		id, err = generateServerID()
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO servers (id, name, self) VALUES ($1, $2, TRUE)`, id, s.serverName)
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
	if name != s.serverName {
		_, err = s.db.ExecContext(ctx, `UPDATE servers SET name = $1 WHERE self = TRUE`, s.serverName)
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
			`SELECT EXISTS(SELECT 1 FROM private_keys WHERE fingerprint = $1)`,
			fingerprint,
		).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check key existence: %w", err)
		}
		if !exists {
			log.Panic().
				Str("fingerprint", fingerprint).
				Msg("Revocation file references unknown key fingerprint")
		}

		if err := s.RevokeServerPrivateKey(ctx, fingerprint, reason); err != nil {
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
func (s *DataService) InitServerKey(ctx context.Context, cryptoSvc *crypto.Service, passphrase string) (*Key, error) {
	var fingerprint string
	var encryptedArmor string
	var createdAt time.Time

	err := s.db.QueryRowContext(ctx, `
		SELECT pk.fingerprint, pk.armor, pk.created_at
		FROM servers sv
		JOIN private_keys pk ON pk.fingerprint = sv.signing_key
		WHERE sv.self = TRUE AND pk.revoked_at IS NULL
	`).Scan(&fingerprint, &encryptedArmor, &createdAt)

	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to query server signing key: %w", err)
	}

	if err == sql.ErrNoRows {
		// No active signing key — generate one
		keyPair, err := cryptoSvc.CreateKeyPair(s.serverID, "", "")
		if err != nil {
			return nil, fmt.Errorf("failed to create server key pair: %w", err)
		}

		encryptedPrivate, err := cryptoSvc.EncryptPrivateKey(keyPair.PrivateKey, passphrase)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt server private key: %w", err)
		}

		if err := s.SaveServerKeyPair(ctx, keyPair.Fingerprint, encryptedPrivate, keyPair.PublicKey); err != nil {
			return nil, fmt.Errorf("failed to save server key pair: %w", err)
		}

		if err := s.SetServerSigningKey(ctx, keyPair.Fingerprint); err != nil {
			return nil, fmt.Errorf("failed to set signing key: %w", err)
		}

		log.Info().
			Str("fingerprint", keyPair.Fingerprint).
			Msg("Generated new server signing key")

		return &Key{Fingerprint: keyPair.Fingerprint, Armor: keyPair.PrivateKey, CreatedAt: time.Now()}, nil
	}

	// Active key found — decrypt it
	decryptedArmor, err := cryptoSvc.DecryptPrivateKey(encryptedArmor, passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt server signing key (wrong passphrase?): %w", err)
	}

	// If the server name changed, add a new identity to the key
	updatedArmor, err := cryptoSvc.AddIdentity(decryptedArmor, s.serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to add identity to server signing key: %w", err)
	}
	if updatedArmor != decryptedArmor {
		newEncrypted, err := cryptoSvc.EncryptPrivateKey(updatedArmor, passphrase)
		if err != nil {
			return nil, fmt.Errorf("failed to re-encrypt server signing key after identity update: %w", err)
		}
		if _, err = s.db.ExecContext(ctx,
			`UPDATE private_keys SET armor = $1 WHERE fingerprint = $2`,
			newEncrypted, fingerprint,
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

	return &Key{Fingerprint: fingerprint, Armor: decryptedArmor, CreatedAt: createdAt}, nil
}

func (s *DataService) SaveServerKeyPair(ctx context.Context, fingerprint, privateArmor, publicArmor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO private_keys (fingerprint, armor) VALUES ($1, $2)`,
		fingerprint, privateArmor,
	)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO public_keys (fingerprint, armor) VALUES ($1, $2)`,
		fingerprint, publicArmor,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *DataService) SetServerSigningKey(ctx context.Context, fingerprint string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET signing_key = $1 WHERE self = TRUE`, fingerprint)
	return err
}

func (s *DataService) GetServerSigningKeyArmor(ctx context.Context) (string, error) {
	var armor string
	err := s.db.QueryRowContext(ctx, `
		SELECT pk.armor
		FROM private_keys pk
		JOIN servers s ON s.signing_key = pk.fingerprint
		WHERE s.self = TRUE
	`).Scan(&armor)
	if err != nil {
		return "", err
	}
	return armor, nil
}

// GetServerPublicKeyByFingerprint returns the armored PGP public key that
// matches the given fingerprint, or "" if no such key exists.
//
// Verifiers use this to select the historical server signing key that
// produced a given reed countersignature: reeds store the fingerprint of the
// key used at signing time (`reeds.private_key_fingerprint`), which is a FK
// into `private_keys`; the matching entry in `public_keys` is the verifier's
// input. This is required because the reed server block binds the
// fingerprint into the countersigned payload, and by any future recovery
// import path that must verify against a restored historical key.
func (s *DataService) GetServerPublicKeyByFingerprint(ctx context.Context, fingerprint string) (string, error) {
	var armor string
	err := s.db.QueryRowContext(ctx,
		`SELECT armor FROM public_keys WHERE fingerprint = $1`,
		fingerprint,
	).Scan(&armor)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return armor, nil
}

func (s *DataService) RevokeServerPrivateKey(ctx context.Context, fingerprint, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE private_keys
		SET revoked_at = NOW(), revoke_reason = $2
		WHERE fingerprint = $1
	`, fingerprint, reason)
	return err
}

// SignupInput bundles everything Signup needs. It is populated by the
// Signup handler after it has allocated a userID, verified the user's
// self-signature over their key, reconstructed the user identity
// payload, verified UserSignatureB64 against PublicKeyArmor, and
// produced ProfileSignature / PublicKeySignature.
type SignupInput struct {
	UserID             string
	Username           string
	PublicKeyArmor     string
	Fingerprint        string
	KeyCreatedAt       time.Time
	KeyExpiresAt       *time.Time
	UserSignatureB64   string
	MemberSince        time.Time
	ProfileSignature   ServerSignature
	PublicKeySignature ServerSignature
	// Invite is the pending invite row to consume (nil when signing up without one).
	Invite   *invites.Invite
	DeviceID string
}

// GetPendingInvite resolves invite composite key + fragment secret for
// pre-signup policy checks. Returns nil invite when unknown or hash mismatch.
func (s *DataService) GetPendingInvite(ctx context.Context, creatorID, inviteID, secret string) (*invites.Invite, error) {
	creatorID = strings.TrimSpace(creatorID)
	inviteID = strings.TrimSpace(inviteID)
	secret = strings.TrimSpace(secret)
	if creatorID == "" || inviteID == "" || secret == "" {
		return nil, nil
	}
	return s.invites.GetPendingInvite(ctx, creatorID, inviteID, invites.HashSecret(secret))
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
		return nil, invites.ErrInvalidInvite
	}

	userSignatureID, err := signing.InsertUserSignature(ctx, tx, in.Fingerprint, in.UserSignatureB64)
	if err != nil {
		return nil, err
	}
	serverSignatureID, err := signing.InsertServerSignature(ctx, tx,
		in.ProfileSignature.Fingerprint,
		in.ProfileSignature.Armor,
		in.ProfileSignature.SignedAt,
	)
	if err != nil {
		return nil, err
	}

	var invitedBy any
	if in.Invite != nil {
		invitedBy = in.Invite.CreatedBy
	}

	inviteGrantedRole := ""
	if in.Invite != nil {
		inviteGrantedRole = in.Invite.GrantedRole
	}
	signupRole := roles.SignupRole(in.UserID, inviteGrantedRole, in.Invite != nil)

	// created_at is set explicitly to memberSince — the value that was
	// signed by the server. Using the DB's DEFAULT would create a
	// race between what was signed and what is persisted, and would
	// silently truncate to whatever precision Postgres chooses.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (
			id, username, role, created_at, user_fingerprint,
			user_signature_id, server_signature_id, invited_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		in.UserID, in.Username, signupRole, in.MemberSince, in.Fingerprint,
		userSignatureID, serverSignatureID, invitedBy,
	); err != nil {
		if isUsernameUniqueViolation(err) {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}

	keyServerSigID, err := signing.InsertServerSignature(ctx, tx,
		in.PublicKeySignature.Fingerprint,
		in.PublicKeySignature.Armor,
		in.PublicKeySignature.SignedAt,
	)
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_keys (
			fingerprint, owner, armor, created_at, expires_at,
			server_signature_id
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, in.Fingerprint, in.UserID, in.PublicKeyArmor, in.KeyCreatedAt, in.KeyExpiresAt,
		keyServerSigID); err != nil {
		return nil, err
	}

	if in.Invite != nil {
		ok, err := s.invites.MarkClaimed(ctx, tx, in.Invite.CreatedBy, in.Invite.ID, in.UserID, in.MemberSince)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, invites.ErrInvalidInvite
		}
	}

	if err := coverage.BumpActiveUsers(ctx, tx, 1); err != nil {
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

	return s.GetUserProfile(ctx, in.UserID)
}

// GetUserProfile returns the signed identity record (no unsigned hints).
func (s *DataService) GetUserProfile(ctx context.Context, userID string) (*User, error) {
	var user User
	var bio sql.NullString
	var userSignatureID, serverSignatureID int64
	var inviterID, inviterUsername sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.role, u.bio, u.created_at,
		       u.user_signature_id, u.server_signature_id,
		       inv.id, inv.username
		FROM users u
		LEFT JOIN users inv ON inv.id = u.invited_by
		WHERE u.id = $1
	`, userID).Scan(
		&user.ID,
		&user.Username,
		&user.Role,
		&bio,
		&user.CreatedAt,
		&userSignatureID,
		&serverSignatureID,
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
	if inviterID.Valid {
		user.InvitedBy = &InvitedBy{
			ID:       inviterID.String,
			Username: inviterUsername.String,
		}
	}

	userRow, err := signing.GetUserSignature(ctx, s.db, userSignatureID)
	if err != nil {
		return nil, err
	}
	uw := signing.UserWire(userRow)
	user.UserSignature = UserSignature{
		Fingerprint: uw.Fingerprint,
		Armor:       uw.Armor,
	}

	serverRow, err := signing.GetServerSignature(ctx, s.db, serverSignatureID)
	if err != nil {
		return nil, err
	}
	sw := signing.ServerWire(serverRow, s.serverID)
	user.ServerSignature = ServerSignature{
		ServerID:    sw.ServerID,
		Fingerprint: sw.Fingerprint,
		Armor:       sw.Armor,
		SignedAt:    sw.Timestamp,
	}

	return &user, nil
}

// GetUserInfo returns unsigned, mutable hints for a user plus the
// profile countersignature timestamp for cache invalidation.
func (s *DataService) GetUserInfo(ctx context.Context, userID string) (*UserInfo, error) {
	var info UserInfo
	var activeFP sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT u.id,
		       u.user_fingerprint,
		       ss.signed_at,
		       EXISTS (
		           SELECT 1 FROM reeds r
		           WHERE r.user_id = u.id
		             AND NOT EXISTS (
		                 SELECT 1 FROM reed_removals rr WHERE rr.reed_id = r.id
		             )
		       ) AS has_reeds,
		       (SELECT COUNT(*)::int FROM user_followers uf WHERE uf.user_id = u.id),
		       (SELECT COUNT(*)::int FROM user_following ufl WHERE ufl.user_id = u.id)
		FROM users u
		JOIN server_signatures ss ON ss.id = u.server_signature_id
		WHERE u.id = $1
	`, userID).Scan(
		&info.ID,
		&activeFP,
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
	if activeFP.Valid {
		info.ActiveKeyFingerprint = activeFP.String
	}
	return &info, nil
}

// GetActiveKeyFingerprint returns users.user_fingerprint for internal
// signing checks (update/delete/reed paths).
func (s *DataService) GetActiveKeyFingerprint(ctx context.Context, userID string) (string, error) {
	var fp sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT user_fingerprint FROM users WHERE id = $1`, userID).Scan(&fp)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	if !fp.Valid {
		return "", nil
	}
	return fp.String, nil
}

// GetUserRole returns users.role for authorization checks.
func (s *DataService) GetUserRole(ctx context.Context, userID string) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM users WHERE id = $1`, userID).Scan(&role)
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
	UserID           string
	Username         string
	Bio              string
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
// function just persists.
func (s *DataService) UpdateUser(ctx context.Context, in UpdateUserInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	userSignatureID, err := signing.InsertUserSignature(ctx, tx, in.Fingerprint, in.UserSignatureB64)
	if err != nil {
		return err
	}
	serverSignatureID, err := signing.InsertServerSignature(ctx, tx,
		in.ProfileSignature.Fingerprint,
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
		in.UserID,
	)
	if err != nil {
		if isUsernameUniqueViolation(err) {
			return ErrUsernameTaken
		}
		return err
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

func (s *DataService) DeleteUser(ctx context.Context, userID string) error {
	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *DataService) FollowUser(ctx context.Context, followerID, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_following (user_id, following_user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, followerID, userID)
	if err != nil {
		return err
	}

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

func (s *DataService) UnfollowUser(ctx context.Context, followerID, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		DELETE FROM user_following
		WHERE user_id = $1 AND following_user_id = $2
	`, followerID, userID)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		DELETE FROM user_followers
		WHERE user_id = $1 AND follower_user_id = $2
	`, userID, followerID)
	if err != nil {
		return err
	}

	return tx.Commit()
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

func (s *DataService) GetPublicKey(ctx context.Context, userID string, fingerprint string) (*Key, error) {
	var key Key
	var revoked bool
	var serverSignatureID int64
	var predSig, predFP sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT uk.fingerprint, uk.owner, uk.armor, uk.created_at,
		       uk.server_signature_id,
		       uk.predecessor_signature, uk.predecessor_fingerprint,
		       EXISTS(
			SELECT 1 FROM user_key_revocations rv
			WHERE rv.user_fingerprint = uk.fingerprint AND rv.owner = uk.owner
		       )
		FROM user_keys uk
		WHERE uk.owner = $1 AND uk.fingerprint = $2
	`, userID, fingerprint).Scan(
		&key.Fingerprint, &key.UserID, &key.Armor, &key.CreatedAt,
		&serverSignatureID,
		&predSig, &predFP,
		&revoked,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	serverRow, err := signing.GetServerSignature(ctx, s.db, serverSignatureID)
	if err != nil {
		return nil, err
	}
	sw := signing.ServerWire(serverRow, s.serverID)
	key.ServerSignature = ServerSignature{
		ServerID:    sw.ServerID,
		Fingerprint: sw.Fingerprint,
		Armor:       sw.Armor,
		SignedAt:    sw.Timestamp,
	}
	key.Revoked = revoked
	if predSig.Valid && predFP.Valid {
		key.Predecessor = &KeyPredecessor{
			Fingerprint: predFP.String,
			Signature:   predSig.String,
		}
	}

	return &key, nil
}

func (s *DataService) IsPublicKeyRevoked(ctx context.Context, key *Key) (bool, error) {
	return key.Revoked, nil
}

func (s *DataService) GetKeyRevocation(ctx context.Context, userID, fingerprint string) (*KeyRevocation, error) {
	var rev KeyRevocation
	var successor sql.NullString
	var reason sql.NullString
	var userSigID, serverSigID int64

	err := s.db.QueryRowContext(ctx, `
		SELECT rv.user_fingerprint, rv.owner, rv.reason, rv.successor,
		       rv.user_signature_id, rv.server_signature_id
		FROM user_key_revocations rv
		WHERE rv.owner = $1 AND rv.user_fingerprint = $2
	`, userID, fingerprint).Scan(
		&rev.Fingerprint, &rev.UserID, &reason, &successor,
		&userSigID, &serverSigID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rev.Reason = reason.String
	if successor.Valid && successor.String != "" {
		s := successor.String
		rev.Successor = &s
	}

	userRow, err := signing.GetUserSignature(ctx, s.db, userSigID)
	if err != nil {
		return nil, err
	}
	uw := signing.UserWire(userRow)
	rev.UserSignature = UserSignature{
		Fingerprint: uw.Fingerprint,
		Armor:       uw.Armor,
	}

	serverRow, err := signing.GetServerSignature(ctx, s.db, serverSigID)
	if err != nil {
		return nil, err
	}
	sw := signing.ServerWire(serverRow, s.serverID)
	rev.ServerSignature = ServerSignature{
		ServerID:    sw.ServerID,
		Fingerprint: sw.Fingerprint,
		Armor:       sw.Armor,
		SignedAt:    sw.Timestamp,
	}
	return &rev, nil
}

func (s *DataService) PublicKeyExists(ctx context.Context, fingerprint string, userID string) (bool, error) {
	var exists bool

	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM user_keys
			WHERE owner = $1 AND fingerprint = $2
		)
	`, userID, fingerprint).Scan(&exists)
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
//  3. the new fingerprint is not already registered to anyone
//  4. predecessor exists under this owner and is revoked
//  5. predecessor does not already have a successor
//  6. the user has no other active (non-revoked) key
type AddPublicKeyInput struct {
	Fingerprint string
	UserID      string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	Armor       string
	Server      ServerSignature

	PredecessorFingerprint string
	PredecessorSignature   string
}

// On success it inserts the key, points users.user_fingerprint at it, and
// writes the successor pointer on the predecessor's revocation row.
func (s *DataService) AddPublicKey(ctx context.Context, in AddPublicKeyInput) (*Key, error) {
	if in.PredecessorFingerprint == "" {
		return nil, ErrPredecessorRequired
	}
	fingerprint := in.Fingerprint
	userID := in.UserID
	createdAt := in.CreatedAt
	expiresAt := in.ExpiresAt
	armor := in.Armor
	predecessor := in.PredecessorFingerprint

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Lock the user row so concurrent rotations for the same owner
	// serialize. Also confirms the owner exists.
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM users WHERE id = $1 FOR UPDATE
	`, userID).Scan(new(int))
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	// Global uniqueness: fingerprints identify key material, so two
	// users must never register the same one.
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM user_keys WHERE fingerprint = $1
	`, fingerprint).Scan(new(int))
	if err == nil {
		return nil, ErrKeyAlreadyExists
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	// Lock the predecessor key row. Rotation is only allowed against a
	// key this owner already holds; revocation is confirmed via the
	// revocations table next.
	err = tx.QueryRowContext(ctx, `
		SELECT 1
		FROM user_keys
		WHERE owner = $1 AND fingerprint = $2
		FOR UPDATE
	`, userID, predecessor).Scan(new(int))
	if err == sql.ErrNoRows {
		return nil, ErrPredecessorNotFound
	}
	if err != nil {
		return nil, err
	}

	// A predecessor may be replaced at most once. Re-rotation against
	// the same revoked key would fork the successor chain. A missing
	// revocations row means the key was never revoked.
	var successor sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT successor
		FROM user_key_revocations
		WHERE user_fingerprint = $1 AND owner = $2
		FOR UPDATE
	`, predecessor, userID).Scan(&successor)
	if err == sql.ErrNoRows {
		return nil, ErrPredecessorNotRevoked
	}
	if err != nil {
		return nil, err
	}
	if successor.Valid && successor.String != "" {
		return nil, ErrPredecessorAlreadyReplaced
	}

	// Even with a correctly revoked predecessor, refuse if any other
	// active key is still present for this user. Active = no row in
	// user_key_revocations.
	var hasActive bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM user_keys uk
			WHERE uk.owner = $1
			  AND NOT EXISTS (
				SELECT 1 FROM user_key_revocations rv
				WHERE rv.user_fingerprint = uk.fingerprint AND rv.owner = uk.owner
			  )
		)
	`, userID).Scan(&hasActive)
	if err != nil {
		return nil, err
	}
	if hasActive {
		return nil, ErrActiveKeyExists
	}

	serverSignatureID, err := signing.InsertServerSignature(ctx, tx,
		in.Server.Fingerprint,
		in.Server.Armor,
		in.Server.SignedAt,
	)
	if err != nil {
		return nil, err
	}

	var key Key
	err = tx.QueryRowContext(ctx, `
		INSERT INTO user_keys (
			fingerprint, owner, armor, created_at, expires_at,
			server_signature_id,
			predecessor_signature, predecessor_fingerprint
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING fingerprint, owner, armor, created_at
	`, fingerprint, userID, armor, createdAt, expiresAt,
		serverSignatureID,
		in.PredecessorSignature, predecessor,
	).Scan(&key.Fingerprint, &key.UserID, &key.Armor, &key.CreatedAt)
	if err != nil {
		return nil, err
	}
	key.ServerSignature = in.Server
	if key.ServerSignature.ServerID == "" {
		key.ServerSignature.ServerID = s.serverID
	}
	if in.PredecessorSignature != "" {
		key.Predecessor = &KeyPredecessor{
			Fingerprint: predecessor,
			Signature:   in.PredecessorSignature,
		}
	}

	_, err = tx.ExecContext(ctx, `UPDATE users SET user_fingerprint = $1 WHERE id = $2`, fingerprint, userID)
	if err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE user_key_revocations
		SET successor = $1
		WHERE user_fingerprint = $2 AND owner = $3
	`, fingerprint, predecessor, userID)
	if err != nil {
		return nil, err
	}

	return &key, tx.Commit()
}

// RevokeKeyInput bundles a signed revocation attestation for persistence.
type RevokeKeyInput struct {
	Fingerprint      string
	UserID           string
	Reason           string
	UserSignatureB64 string
	Server           ServerSignature
}

func (s *DataService) RevokeKey(ctx context.Context, in RevokeKeyInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	userSigID, err := signing.InsertUserSignature(ctx, tx, in.Fingerprint, in.UserSignatureB64)
	if err != nil {
		return err
	}
	serverSigID, err := signing.InsertServerSignature(ctx, tx,
		in.Server.Fingerprint,
		in.Server.Armor,
		in.Server.SignedAt,
	)
	if err != nil {
		return err
	}

	// A key is revoked iff a row exists in user_key_revocations.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_key_revocations (
			user_fingerprint, owner, reason,
			user_signature_id, server_signature_id
		) VALUES ($1, $2, $3, $4, $5)
	`, in.Fingerprint, in.UserID, in.Reason, userSigID, serverSigID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// ReedRef is a parsed echoing/replying target: userID@serverID/reedID.
type ReedRef struct {
	AuthorID string
	ServerID string
	ReedID   string
}

// ParseReedRef parses "userID@serverID/reedID". Returns ok=false for empty or malformed input.
func ParseReedRef(raw string) (ReedRef, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ReedRef{}, false
	}
	at := strings.Index(raw, "@")
	if at <= 0 {
		return ReedRef{}, false
	}
	slash := strings.Index(raw[at+1:], "/")
	if slash <= 0 {
		return ReedRef{}, false
	}
	slash += at + 1
	if slash >= len(raw)-1 {
		return ReedRef{}, false
	}
	author := strings.TrimSpace(raw[:at])
	serverID := strings.TrimSpace(raw[at+1 : slash])
	reedID := strings.TrimSpace(raw[slash+1:])
	if author == "" || serverID == "" || reedID == "" {
		return ReedRef{}, false
	}
	return ReedRef{AuthorID: author, ServerID: serverID, ReedID: reedID}, true
}

// FormatReedRef returns the canonical wire form userID@serverID/reedID.
func FormatReedRef(ref ReedRef) string {
	return ref.AuthorID + "@" + ref.ServerID + "/" + ref.ReedID
}

// ReedAttestation is tip reed metadata plus stored user/server signatures.
type ReedAttestation struct {
	Reed
	UserFingerprint   string
	UserSignature     string
	ServerFingerprint string
	ServerSignature   string
	ServerSignedAt    time.Time
}

// createReedParams is the shared insert payload for SignReed persistence.
type createReedParams struct {
	ReedID             string
	UserID             string
	UserFingerprint    string
	UserSignatureB64   string
	ServerFingerprint  string
	ServerSignatureB64 string
	Timestamp          time.Time
	Tags               []string
	Mentions           []ReedRef
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
		WHERE user_id = $1 AND reed_id = $2
	`, parent.AuthorID, parent.ReedID).Scan(&threadID)
	if err == sql.ErrNoRows {
		return FormatReedRef(parent), nil
	}
	if err != nil {
		return "", err
	}
	return threadID, nil
}

// InsertReply records a direct reply in reed_replies.
func (s *DataService) InsertReply(
	ctx context.Context,
	threadID string,
	parent ReedRef,
	replyUserID, replyReedID string,
	ts time.Time,
) (replyIndexed bool, err error) {
	ts = ts.UTC().Truncate(time.Second)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if err = s.insertReplyTx(ctx, tx, threadID, parent, replyUserID, replyReedID, ts); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *DataService) insertReplyTx(
	ctx context.Context,
	tx *sql.Tx,
	threadID string,
	parent ReedRef,
	replyUserID, replyReedID string,
	ts time.Time,
) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO reed_replies (
			thread_id, user_id, reed_id,
			parent_user_id, parent_reed_id,
			timestamp
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, threadID, replyUserID, replyReedID,
		parent.AuthorID, parent.ReedID,
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

// ListReplies returns visible direct replies to parentUser/parentReed, oldest first.
func (s *DataService) ListReplies(ctx context.Context, parentUserID, parentReedID string, limit int, before *time.Time) (*ReplyListResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	args := []any{parentUserID, parentReedID}
	query := `
		SELECT user_id, reed_id, timestamp
		FROM reed_replies rr2
		WHERE rr2.parent_user_id = $1 AND rr2.parent_reed_id = $2
		AND NOT EXISTS (
			SELECT 1 FROM reed_removals rr
			WHERE rr.user_id = rr2.user_id AND rr.reed_id = rr2.reed_id
		)
		AND NOT EXISTS (
			SELECT 1 FROM account_removals ar WHERE ar.user_id = rr2.user_id
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
		var userID, reedID string
		var _ts time.Time
		if err := rows.Scan(&userID, &reedID, &_ts); err != nil {
			return nil, err
		}
		items = append(items, ReplyListItem{
			UserID: userID,
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
func (s *DataService) listFollowEdge(ctx context.Context, table, otherCol, userID string, limit int, before *time.Time) (*FollowListResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	args := []any{userID}
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
// be empty when the author has zero reeds. Locks the author's users row
// first so concurrent creates for the same author serialize — caller must
// run this and the subsequent INSERT INTO reeds in the same transaction,
// otherwise the check is only advisory under a dual-tab/dual-device race.
func checkReedTipTx(ctx context.Context, tx *sql.Tx, userID, previousID string) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM users WHERE id = $1 FOR UPDATE
	`, userID).Scan(new(int)); err != nil {
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
		      WHERE rm.user_id = r.user_id AND rm.reed_id = r.id
		  )
		ORDER BY r.signed_at DESC, r.id DESC
		LIMIT 1
	`, userID).Scan(&tip)

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

func (s *DataService) insertReedCoreTx(
	ctx context.Context,
	tx *sql.Tx,
	p createReedParams,
) (Reed, error) {
	if !crypto.IsValidUUIDv7(p.ReedID) {
		return Reed{}, fmt.Errorf("invalid reed ID")
	}

	if err := checkReedTipTx(ctx, tx, p.UserID, p.PreviousID); err != nil {
		return Reed{}, err
	}

	ts := p.Timestamp.UTC().Truncate(time.Second)

	// signing.InsertUserSignature/InsertServerSignature still use non-context
	// queries internally, so these two inserts land as root spans rather than
	// nested under ctx's request span — a known gap, not a bug (see
	// specs/observability/04_context_threading.md).
	userSigID, err := signing.InsertUserSignature(ctx, tx, p.UserFingerprint, p.UserSignatureB64)
	if err != nil {
		return Reed{}, err
	}
	serverSigID, err := signing.InsertServerSignature(ctx, tx, p.ServerFingerprint, p.ServerSignatureB64, ts)
	if err != nil {
		return Reed{}, err
	}

	var created Reed
	err = tx.QueryRowContext(ctx, `
		INSERT INTO reeds (
			id, user_id, private_key_fingerprint, signed_at,
			user_signature_id, server_signature_id, allocation_count
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1)
		RETURNING id, user_id, private_key_fingerprint, signed_at
	`, p.ReedID, p.UserID, p.ServerFingerprint, ts, userSigID, serverSigID).Scan(
		&created.ID,
		&created.UserID,
		&created.Fingerprint,
		&created.Timestamp,
	)
	if err != nil {
		return Reed{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_allocations (reed_id, holder_user_id, author_user_id)
		VALUES ($1, $2, $3)
	`, p.ReedID, p.UserID, p.UserID); err != nil {
		return Reed{}, fmt.Errorf("allocate reed to author: %w", err)
	}

	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO pending_fanout (user_id, reed_id, tags)
		VALUES ($1, $2, $3)
	`, p.UserID, p.ReedID, pq.Array(tags)); err != nil {
		return Reed{}, fmt.Errorf("insert pending fanout: %w", err)
	}

	for _, m := range p.Mentions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reed_mentions (
				mentioning_user_id, mentioning_reed_id,
				mentioned_user_id, mentioned_server_id
			) VALUES ($1, $2, $3, $4)
			ON CONFLICT (mentioning_reed_id, mentioned_server_id, mentioned_user_id) DO NOTHING
		`, p.UserID, p.ReedID, m.AuthorID, m.ServerID); err != nil {
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
// echoIndexed is true when a new reed_echoes row was inserted.
func (s *DataService) CreateReedWithEcho(
	ctx context.Context,
	p createReedParams,
	echoTarget ReedRef,
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

	res, err := tx.ExecContext(ctx, `
		INSERT INTO reed_echoes (echoing_user_id, echoing_reed_id, echoed_user_id, echoed_reed_id, signed_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (echoing_user_id, echoing_reed_id) DO NOTHING
	`, p.UserID, p.ReedID, echoTarget.AuthorID, echoTarget.ReedID, ts)
	if err != nil {
		return nil, false, fmt.Errorf("insert echo index: %w", err)
	}
	n, _ := res.RowsAffected()
	echoIndexed = n > 0

	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &created, echoIndexed, nil
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

	if err = s.insertReplyTx(ctx, tx, threadID, parent, p.UserID, p.ReedID, ts); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &created, nil
}

// GetReedAttestation loads a tip reed and its stored signatures.
func (s *DataService) GetReedAttestation(ctx context.Context, userID, reedID string) (*ReedAttestation, error) {
	var att ReedAttestation
	err := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.user_id, r.private_key_fingerprint, r.signed_at,
			us.fingerprint, us.signature,
			ss.fingerprint, ss.signature, ss.signed_at
		FROM reeds r
		JOIN user_signatures us ON us.id = r.user_signature_id
		JOIN server_signatures ss ON ss.id = r.server_signature_id
		WHERE r.id = $1 AND r.user_id = $2
	`, reedID, userID).Scan(
		&att.ID,
		&att.UserID,
		&att.Fingerprint,
		&att.Timestamp,
		&att.UserFingerprint,
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
	att.Timestamp = att.Timestamp.UTC().Truncate(time.Second)
	att.ServerSignedAt = att.ServerSignedAt.UTC().Truncate(time.Second)
	return &att, nil
}

// Reed ids are scoped to (user_id, id); there is no global author lookup by reed id alone.

func (s *DataService) DeleteReed(ctx context.Context, userID, reedID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reeds
		WHERE user_id = $1 AND id = $2
	`, userID, reedID)
	if err != nil {
		return err
	}

	return nil
}

// ReedExists reports whether (userID, reedID) is a live tip reed.
func (s *DataService) ReedExists(ctx context.Context, userID, reedID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM reeds r
			WHERE r.user_id = $1 AND r.id = $2
			  AND NOT EXISTS (
			      SELECT 1 FROM reed_removals rr
			      WHERE rr.user_id = r.user_id AND rr.reed_id = r.id
			  )
			  AND NOT EXISTS (
			      SELECT 1 FROM account_removals ar WHERE ar.user_id = r.user_id
			  )
		)
	`, userID, reedID).Scan(&exists)
	return exists, err
}

// MentionTargetValid reports whether userID exists, is not account-removed,
// and serverID is a known row in servers (self or a federated peer) — the
// gate for a local (this-server) mention to be indexed.
func (s *DataService) MentionTargetValid(ctx context.Context, userID, serverID string) (bool, error) {
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
	`, userID, serverID).Scan(&exists)
	return exists, err
}

// UserSearchResult is one row in a GET /users/search response — minimal
// fields only, no keys, no bio.
type UserSearchResult struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// SearchUsers returns users whose username contains query (case-insensitive
// substring match), excluding account-removed users, ordered by username.
func (s *DataService) SearchUsers(ctx context.Context, query string, limit int) ([]UserSearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.username
		FROM users u
		WHERE u.username ILIKE '%' || $1 || '%'
		  AND NOT EXISTS (
		      SELECT 1 FROM account_removals ar WHERE ar.user_id = u.id
		  )
		ORDER BY u.username ASC
		LIMIT $2
	`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []UserSearchResult{}
	for rows.Next() {
		var r UserSearchResult
		if err := rows.Scan(&r.ID, &r.Username); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// CountEchoes returns how many echoes point at the given reed.
func (s *DataService) CountEchoes(ctx context.Context, echoedUserID, echoedReedID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM reed_echoes
		WHERE echoed_user_id = $1 AND echoed_reed_id = $2
	`, echoedUserID, echoedReedID).Scan(&n)
	return n, err
}

// DeleteEchoIndexForReed clears echo index rows when a reed is removed.
// Returns distinct echoed targets whose counts may have changed (excluding
// the removed reed itself, which no longer has live tip subscribers).
func (s *DataService) DeleteEchoIndexForReed(ctx context.Context, userID, reedID string) ([]ReedRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT echoed_user_id, echoed_reed_id
		FROM reed_echoes
		WHERE echoing_user_id = $1 AND echoing_reed_id = $2
	`, userID, reedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ReedRef
	for rows.Next() {
		var t ReedRef
		if err := rows.Scan(&t.AuthorID, &t.ReedID); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_echoes WHERE echoing_user_id = $1 AND echoing_reed_id = $2
	`, userID, reedID); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_echoes WHERE echoed_user_id = $1 AND echoed_reed_id = $2
	`, userID, reedID); err != nil {
		return nil, err
	}
	return targets, nil
}

// DeleteEchoesByAuthor drops echo index rows created by userID (the echoing author).
// Returns distinct echoed targets whose counts may have changed.
func (s *DataService) DeleteEchoesByAuthor(ctx context.Context, userID string) ([]ReedRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT echoed_user_id, echoed_reed_id
		FROM reed_echoes
		WHERE echoing_user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ReedRef
	for rows.Next() {
		var t ReedRef
		if err := rows.Scan(&t.AuthorID, &t.ReedID); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM reed_echoes WHERE echoing_user_id = $1`, userID); err != nil {
		return nil, err
	}
	return targets, nil
}

// DeleteMentionsForReed clears mention index rows contained in a removed reed.
func (s *DataService) DeleteMentionsForReed(ctx context.Context, userID, reedID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_mentions
		WHERE mentioning_user_id = $1 AND mentioning_reed_id = $2
	`, userID, reedID)
	return err
}

// DeleteMentionsByAuthor clears mention index rows on account removal: rows
// the removed user authored (mentioning), and rows mentioning the removed
// user on this server (mentioned) — both sides, per spec lock.
func (s *DataService) DeleteMentionsByAuthor(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_mentions
		WHERE mentioning_user_id = $1
		   OR (mentioned_server_id = $2 AND mentioned_user_id = $1)
	`, userID, s.serverID)
	return err
}

// ReplyCountNotifyTargets returns every ancestor of parent (inclusive) whose
// subtree reply count changes when a direct reply to parent is added or removed.
func (s *DataService) ReplyCountNotifyTargets(ctx context.Context, parentUserID, parentReedID string) ([]ReedRef, error) {
	var targets []ReedRef
	userID, reedID := parentUserID, parentReedID
	for {
		targets = append(targets, ReedRef{AuthorID: userID, ReedID: reedID})
		var nextUserID, nextReedID string
		err := s.db.QueryRowContext(ctx, `
			SELECT parent_user_id, parent_reed_id
			FROM reed_replies
			WHERE user_id = $1 AND reed_id = $2
		`, userID, reedID).Scan(&nextUserID, &nextReedID)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return nil, err
		}
		userID, reedID = nextUserID, nextReedID
	}
	return targets, nil
}

// ReplyCountNotifyTargetsForRemovedReply returns ancestors whose subtree count
// drops when replyUserID/replyReedID is removed. nil when not indexed as a reply.
func (s *DataService) ReplyCountNotifyTargetsForRemovedReply(ctx context.Context, replyUserID, replyReedID string) ([]ReedRef, error) {
	var parentUserID, parentReedID string
	err := s.db.QueryRowContext(ctx, `
		SELECT parent_user_id, parent_reed_id
		FROM reed_replies
		WHERE user_id = $1 AND reed_id = $2
	`, replyUserID, replyReedID).Scan(&parentUserID, &parentReedID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.ReplyCountNotifyTargets(ctx, parentUserID, parentReedID)
}

// ReplyCountNotifyTargetsForAuthor returns distinct ancestors whose subtree
// counts may change when all of userID's indexed replies are treated as removed.
func (s *DataService) ReplyCountNotifyTargetsForAuthor(ctx context.Context, userID string) ([]ReedRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT parent_user_id, parent_reed_id
		FROM reed_replies
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	var targets []ReedRef
	for rows.Next() {
		var parentUserID, parentReedID string
		if err := rows.Scan(&parentUserID, &parentReedID); err != nil {
			return nil, err
		}
		ancestors, err := s.ReplyCountNotifyTargets(ctx, parentUserID, parentReedID)
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

// GetSubtreeReplyCount returns live descendant reply count beneath userID/reedID.
func (s *DataService) GetSubtreeReplyCount(ctx context.Context, userID, reedID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		WITH RECURSIVE descendants AS (
			SELECT rr.user_id, rr.reed_id
			FROM reed_replies rr
			WHERE rr.parent_user_id = $1 AND rr.parent_reed_id = $2
			AND NOT EXISTS (
				SELECT 1 FROM reed_removals rm
				WHERE rm.user_id = rr.user_id AND rm.reed_id = rr.reed_id
			)
			AND NOT EXISTS (
				SELECT 1 FROM account_removals ar WHERE ar.user_id = rr.user_id
			)
			UNION ALL
			SELECT rr.user_id, rr.reed_id
			FROM reed_replies rr
			INNER JOIN descendants d
				ON rr.parent_user_id = d.user_id AND rr.parent_reed_id = d.reed_id
			WHERE NOT EXISTS (
				SELECT 1 FROM reed_removals rm
				WHERE rm.user_id = rr.user_id AND rm.reed_id = rr.reed_id
			)
			AND NOT EXISTS (
				SELECT 1 FROM account_removals ar WHERE ar.user_id = rr.user_id
			)
		)
		SELECT COUNT(*) FROM descendants
	`, userID, reedID).Scan(&count)
	return count, err
}

// ReedOrRemovalResult is returned by GetReedOrRemovalCert. Account removal wins
// over reed removal; a tombstone is returned without loading the reed row.
// All fields nil means the reed row does not exist.
type ReedOrRemovalResult struct {
	Reed           *Reed
	AccountRemoval *deletion.AccountCert
	ReedRemoval    *deletion.Cert
}

func (s *DataService) GetReed(ctx context.Context, userID string, reedID string) (*Reed, error) {
	var reed Reed
	err := s.db.QueryRowContext(ctx, `
	SELECT id, user_id, private_key_fingerprint, signed_at
		FROM reeds
		WHERE id = $1 AND user_id = $2
	`, reedID, userID,
	).Scan(
		&reed.ID,
		&reed.UserID,
		&reed.Fingerprint,
		&reed.Timestamp,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &reed, nil
}

// GetReedOrRemovalCert loads tip reed metadata when neither the account nor the
// reed has a removal cert. Tombstones are returned in the result instead of
// the reed row.
func (s *DataService) GetReedOrRemovalCert(ctx context.Context, userID, reedID string) (ReedOrRemovalResult, error) {
	var out ReedOrRemovalResult

	accountRemoval, err := s.GetAccountRemoval(ctx, userID)
	if err != nil {
		return out, err
	}
	if accountRemoval != nil {
		out.AccountRemoval = accountRemoval
		return out, nil
	}

	removal, err := s.GetReedRemoval(ctx, userID, reedID)
	if err != nil {
		return out, err
	}
	if removal != nil {
		out.ReedRemoval = removal
		return out, nil
	}

	reed, err := s.GetReed(ctx, userID, reedID)
	if err != nil {
		return out, err
	}
	out.Reed = reed
	return out, nil
}

// GetReedRemoval returns the stored reed-removal cert for (userID, reedID).
func (s *DataService) GetReedRemoval(ctx context.Context, userID, reedID string) (*deletion.Cert, error) {
	return deletion.GetCert(ctx, s.db, userID, reedID)
}

// InsertReedRemoval persists a reed-removal cert (idempotent / conflict).
func (s *DataService) InsertReedRemoval(ctx context.Context, cert deletion.Cert) error {
	return deletion.InsertCert(ctx, s.db, cert)
}

// GetAccountRemoval returns the stored account-removal cert for userID.
func (s *DataService) GetAccountRemoval(ctx context.Context, userID string) (*deletion.AccountCert, error) {
	return deletion.GetAccountCert(ctx, s.db, userID)
}

// InsertAccountRemoval persists an account-removal cert (idempotent / conflict).
func (s *DataService) InsertAccountRemoval(ctx context.Context, cert deletion.AccountCert) error {
	return deletion.InsertAccountCert(ctx, s.db, cert)
}

// HasAccountRemoval reports whether userID has an account-removal row.
func (s *DataService) HasAccountRemoval(ctx context.Context, userID string) (bool, error) {
	return deletion.HasAccountRemoval(ctx, s.db, userID)
}

// ==================== //
//   Account recovery   //
// ==================== //

// ListUserFollowing returns user ids this user follows.
func (s *DataService) ListUserFollowing(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT following_user_id
		FROM user_following
		WHERE user_id = $1
		ORDER BY following_user_id
	`, userID)
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

// ListUserReeds returns non-removed reed ids for userID, tip first.
func (s *DataService) ListUserReeds(ctx context.Context, userID string) (tipReedID *string, reedIDs []string, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id
		FROM reeds r
		WHERE r.user_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.user_id = r.user_id AND rr.reed_id = r.id
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

// ReedContentWithinLimits reports whether content is within raw and visible caps.
// Raw uses byte length to match JavaScript string.length for BMP text.
func ReedContentWithinLimits(body string) bool {
	if len(body) > MaxReedRawChars {
		return false
	}
	if CountMarkdownCharacters(body) > MaxReedVisibleChars {
		return false
	}
	return true
}

// ReedAsMarkdown builds the canonical signed markdown envelope (must match SPA reedAsMarkdown).
func ReedAsMarkdown(id, userID, content, echoing, replying, threadId string) string {
	headers := map[string]string{
		"id":     id,
		"userID": userID,
	}
	if replying != "" {
		headers["replying"] = replying
	}
	if echoing != "" {
		headers["echoing"] = echoing
	}
	if threadId != "" {
		headers["threadId"] = threadId
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(headers[k])
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	b.WriteString(content)
	return b.String()
}

// hashtagExtract matches SPA Reed.extractTags: (^|\s)#\S+
var hashtagExtract = regexp.MustCompile(`(^|\s)#\S+`)

// ExtractTags returns normalized unique hashtags from content (no leading #,
// lowercase, first-appearance order). Mirrors spa/src/lib/types/reed.ts extractTags.
func ExtractTags(content string) []string {
	matches := hashtagExtract.FindAllString(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		tag := strings.ToLower(strings.TrimSpace(m)[1:]) // drop leading #
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type MarkdownService struct {
}

func NewMarkdownService() *MarkdownService {
	return &MarkdownService{}
}

type ReedHeader struct {
	ID     string
	UserID string

	// Social
	Replying string
	Echoing  string
	ThreadID string
}

func (s *MarkdownService) ExtractReedHeader(reed string) ReedHeader {
	lines := strings.Split(reed, "\n")
	inHeader := false
	var id,
		userID,
		replying,
		echoing,
		threadID string
	for _, line := range lines {
		if inHeader {
			if line == "---" {
				break
			}
			if strings.HasPrefix(line, "id:") {
				id, _ = strings.CutPrefix(line, "id:")
			}
			if strings.HasPrefix(line, "userID:") {
				userID, _ = strings.CutPrefix(line, "userID:")
			}
			if strings.HasPrefix(line, "replying:") {
				replying, _ = strings.CutPrefix(line, "replying:")
			}
			if strings.HasPrefix(line, "echoing:") {
				echoing, _ = strings.CutPrefix(line, "echoing:")
			}
			if strings.HasPrefix(line, "threadId:") {
				threadID, _ = strings.CutPrefix(line, "threadId:")
			}
		}

		if line == "---" && !inHeader {
			inHeader = true
		}
	}

	header := ReedHeader{
		ID:       strings.TrimSpace(id),
		UserID:   strings.TrimSpace(userID),
		Replying: strings.TrimSpace(replying),
		Echoing:  strings.TrimSpace(echoing),
		ThreadID: strings.TrimSpace(threadID),
	}

	return header
}

func (s *MarkdownService) ValidateReedHeader(reed string) error {
	mandatoryFoundCount := 0
	mandatoryHeaders := []string{
		"id",
		"userID",
	}
	optionalHeaders := []string{
		"replying",
		"echoing",
		"threadId",
	}

	lines := strings.Split(reed, "\n")
	inHeader := false
	for _, line := range lines {
		if inHeader {
			if line == "---" {
				break
			}
			if strings.Contains(line, ": ") {
				headerName := strings.Split(line, ": ")[0]
				inMandatory := slices.Contains(mandatoryHeaders, headerName)
				inOptional := slices.Contains(optionalHeaders, headerName)
				if inMandatory {
					mandatoryFoundCount++
				} else {
					if !inOptional {
						return fmt.Errorf("unrecognized header: %s", headerName)
					}
				}
			} else {
				return fmt.Errorf("invalid header format: %s", line)
			}
		}
		if line == "---" && !inHeader {
			inHeader = true
		}
	}

	if mandatoryFoundCount != len(mandatoryHeaders) {
		return fmt.Errorf("mandatory headers missing: %v", mandatoryHeaders)
	}

	return nil
}

func (s *MarkdownService) ExtractReedContent(reed string) string {
	lines := strings.Split(reed, "\n")
	inHeader := false
	inContent := false
	var content string
	for _, line := range lines {
		if inContent {
			content += line + "\n"
		}
		if line == "---" {
			if inContent {
				continue
			}
			inHeader = !inHeader
			inContent = !inHeader
		}
	}

	return strings.TrimSpace(content)
}

func (s *MarkdownService) ParseMarkdown(reed string) string {
	// Remove markdown formatting characters and links
	// Handle *bold* and _italic_ and ~strikethrough~
	reed = regexp.MustCompile(`[*_~](.*?)[*_~]`).ReplaceAllString(reed, "$1")

	// Handle links [text](url)
	reed = regexp.MustCompile(`\[(.*?)\]\(.*?\)`).ReplaceAllString(reed, "$1")

	return reed
}

// ================= //
//   DeviceService   //
// ================= //

var (
	errDeviceMismatch = errors.New("device mismatch")
)

func (s *DataService) GetActiveDeviceID(ctx context.Context, userID string) (string, error) {
	var deviceID string
	err := s.db.QueryRowContext(ctx, `
		SELECT device_id FROM user_devices
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID).Scan(&deviceID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return deviceID, nil
}

func (s *DataService) BindDeviceTx(ctx context.Context, tx *sql.Tx, userID, deviceID string, now time.Time) error {
	deviceID, err := identity.ParseDeviceID(deviceID)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE user_devices SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID, now); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_devices (user_id, device_id, linked_at, revoked_at)
		VALUES ($1, $2, $3, NULL)
	`, userID, deviceID, now); err != nil {
		return err
	}

	return nil
}

func (s *DataService) BindDevice(ctx context.Context, userID, deviceID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.BindDeviceTx(ctx, tx, userID, deviceID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *DataService) CheckActiveDevice(ctx context.Context, userID, presented string) error {
	presented, err := identity.ParseDeviceID(presented)
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

const (
	federationStatusNew      = "new"
	federationStatusAccepted = "accepted"
	federationStatusApproved = "approved"
	federationStatusRevoked  = "revoked"
)

var (
	errFederationInvitationNotFound     = errors.New("federation invitation not found")
	errFederationInvitationNotRevocable = errors.New("federation invitation cannot be revoked")
	errFederationInvitationExists       = errors.New("federation invitation already exists")
)

type federationInvitation struct {
	ID                string
	Name              string
	SecretHash        []byte
	RemoteFingerprint string
	CreatedBy         string
	Status            string
	CreatedAt         time.Time
	AcceptedAt        *time.Time
	ApprovedAt        *time.Time
}

type federationInvitationListRow struct {
	ID                   string
	Name                 string
	Status               string
	CreatedBy            string
	CreatedByUsername    string
	RemoteFingerprint    string
	CreatedAt            time.Time
	AcceptedAt           *time.Time
	ApprovedAt           *time.Time
	ReviewedBy           string
	ReviewedByUsername   string
	ReviewedAt           *time.Time
	ConnectionCiphertext string
}

func (s *DataService) InsertFederationInvitation(
	ctx context.Context,
	id, name, createdBy, remoteFingerprint string,
	secretHash []byte,
	connectionCiphertext string,
	createdAt time.Time,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO federation_invitation (
			id, name, secret_hash, remote_fingerprint, created_by, status, created_at,
			connection_ciphertext
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, id, name, secretHash, remoteFingerprint, createdBy, federationStatusNew, createdAt.UTC(), connectionCiphertext)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return errFederationInvitationExists
		}
		return err
	}
	return nil
}

func (s *DataService) GetFederationInvitation(ctx context.Context, id string) (*federationInvitation, error) {
	var inv federationInvitation
	var acceptedAt, approvedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, secret_hash, remote_fingerprint, created_by, status,
		       created_at, accepted_at, approved_at
		FROM federation_invitation
		WHERE id = $1
	`, id).Scan(
		&inv.ID,
		&inv.Name,
		&inv.SecretHash,
		&inv.RemoteFingerprint,
		&inv.CreatedBy,
		&inv.Status,
		&inv.CreatedAt,
		&acceptedAt,
		&approvedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if acceptedAt.Valid {
		t := acceptedAt.Time.UTC()
		inv.AcceptedAt = &t
	}
	if approvedAt.Valid {
		t := approvedAt.Time.UTC()
		inv.ApprovedAt = &t
	}
	return &inv, nil
}

func (s *DataService) ListFederationInvitations(ctx context.Context) ([]federationInvitationListRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fi.id, fi.name, fi.status, fi.created_by, creator.username,
		       fi.remote_fingerprint, fi.created_at, fi.accepted_at, fi.approved_at,
		       COALESCE(fi.reviewed_by, ''), COALESCE(reviewer.username, ''), fi.reviewed_at,
		       COALESCE(fi.connection_ciphertext, '')
		FROM federation_invitation fi
		JOIN users creator ON creator.id = fi.created_by
		LEFT JOIN users reviewer ON reviewer.id = fi.reviewed_by
		ORDER BY fi.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []federationInvitationListRow
	for rows.Next() {
		var row federationInvitationListRow
		var acceptedAt, approvedAt, reviewedAt sql.NullTime
		if err := rows.Scan(
			&row.ID,
			&row.Name,
			&row.Status,
			&row.CreatedBy,
			&row.CreatedByUsername,
			&row.RemoteFingerprint,
			&row.CreatedAt,
			&acceptedAt,
			&approvedAt,
			&row.ReviewedBy,
			&row.ReviewedByUsername,
			&reviewedAt,
			&row.ConnectionCiphertext,
		); err != nil {
			return nil, err
		}
		if acceptedAt.Valid {
			t := acceptedAt.Time.UTC()
			row.AcceptedAt = &t
		}
		if approvedAt.Valid {
			t := approvedAt.Time.UTC()
			row.ApprovedAt = &t
		}
		if reviewedAt.Valid {
			t := reviewedAt.Time.UTC()
			row.ReviewedAt = &t
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *DataService) RevokeFederationInvitation(ctx context.Context, id, reviewedBy string, reviewedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE federation_invitation
		SET status = $2, connection_ciphertext = NULL,
		    reviewed_by = $4, reviewed_at = $5
		WHERE id = $1 AND status = $3
	`, id, federationStatusRevoked, federationStatusNew, reviewedBy, reviewedAt.UTC())
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

func (s *DataService) AcceptFederationInvitation(ctx context.Context, id string, acceptedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE federation_invitation
		SET status = $2, accepted_at = $3, connection_ciphertext = NULL
		WHERE id = $1 AND status = $4
	`, id, federationStatusAccepted, acceptedAt.UTC(), federationStatusNew)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errFederationInvitationNotFound
	}
	return nil
}
