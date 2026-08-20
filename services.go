//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

// setServerIDForTest sets serverID and keeps s.invites.ServerID in sync;
// tests must use this instead of writing s.serverID directly, or invite
// creation/claiming breaks (s.invites.ServerID stays "" while serverID is set).
func (s *DataService) setServerIDForTest(id string) {
	s.serverID = id
	s.invites.ServerID = id
}

func (s *DataService) GetServerID() string {
	return s.serverID
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

func generateServerID() (string, error) {
	return crypto.NewID()
}

func generateUserID() (string, error) {
	return crypto.NewID()
}

func (s *DataService) InitServer(ctx context.Context, recoveryMode bool, baseURL string) error {
	var id, name string
	var dbBaseURL sql.NullString

	err := s.db.QueryRowContext(ctx, `SELECT id, name, base_url FROM servers WHERE self = TRUE`).Scan(&id, &name, &dbBaseURL)
	if err == sql.ErrNoRows {
		if recoveryMode {
			return recovery.ErrNoIdentityFound
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
		s.invites.ServerID = id
		return nil
	}
	if err != nil {
		return err
	}
	s.serverID = id
	s.invites.ServerID = id
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

	// identities.id for the new local user, minted here inside the signup
	// transaction so it never exists half-committed. Local identities are
	// always verified — no handshake needed to trust this server's own signup.
	selfIdentity := identity.CanonicalID(s.serverID, in.UserID)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO identities (id, remote_user_id, server_id, verified)
		VALUES ($1, $2, $3, TRUE)
	`, selfIdentity, in.UserID, s.serverID); err != nil {
		return nil, err
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

	// invited_by FKs identities(id): the inviter is always a local user, so
	// the same CanonicalID conversion applies here as for selfIdentity above.
	// in.Invite.CreatedBy arrives in userID@serverID form; strip it back to
	// bare before re-composing via identity.CanonicalID, which expects bare.
	var invitedBy any
	var inviteCreatorBare string
	if in.Invite != nil {
		inviteCreatorBare = identity.IdentityID(in.Invite.CreatedBy).UserID()
		invitedBy = identity.CanonicalID(s.serverID, inviteCreatorBare)
	}

	inviteGrantedRole := ""
	if in.Invite != nil {
		inviteGrantedRole = in.Invite.GrantedRole
	}
	signupRole := roles.SignupRole(in.UserID, inviteGrantedRole, in.Invite != nil, s.serverID)

	// created_at is set explicitly to memberSince — the value that was
	// signed by the server. Using the DB's DEFAULT would create a
	// race between what was signed and what is persisted, and would
	// silently truncate to whatever precision Postgres chooses.
	// users.id IS identities.id now (no separate identity_id column) —
	// insert selfIdentity as the PK directly.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (
			id, username, role, created_at, user_fingerprint,
			user_signature_id, server_signature_id, invited_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		selfIdentity, in.Username, signupRole, in.MemberSince, in.Fingerprint,
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
	`, in.Fingerprint, selfIdentity, in.PublicKeyArmor, in.KeyCreatedAt, in.KeyExpiresAt,
		keyServerSigID); err != nil {
		return nil, err
	}

	if in.Invite != nil {
		ok, err := s.invites.MarkClaimed(ctx, tx, inviteCreatorBare, in.Invite.ID, in.UserID, in.MemberSince)
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
	var inviterID, inviterUsername sql.NullString

	// users.id IS identities.id now, so the self-join is a plain
	// u.invited_by = inv.id match, not the old inv.identity_id indirection.
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.role, u.bio, u.created_at,
		       u.user_signature_id, u.server_signature_id,
		       inv.id, inv.username
		FROM users u
		LEFT JOIN users inv ON inv.id = u.invited_by
		WHERE u.id = $1
	`, selfIdentity).Scan(
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
// profile countersignature timestamp for cache invalidation. userID
// arrives already in userID@serverID form — see GetUserProfile's comment.
func (s *DataService) GetUserInfo(ctx context.Context, userID string) (*UserInfo, error) {
	selfIdentity := userID

	var info UserInfo
	var activeFP sql.NullString

	// reeds.user_id, user_followers.user_id, and user_following.user_id all
	// FK to identities(id), and u.id is that same form directly (identity_id
	// no longer exists as a separate column), so this is a plain u.id join.
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
// signing checks (update/delete/reed paths). userID arrives in
// userID@serverID form already.
func (s *DataService) GetActiveKeyFingerprint(ctx context.Context, userID string) (string, error) {
	selfIdentity := userID
	var fp sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT user_fingerprint FROM users WHERE id = $1`, selfIdentity).Scan(&fp)
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
// deletion.InsertAccountCert/account_removals instead). Note: deleting
// only the `users` row does not cascade to `identities` (FK direction is
// identities → users), so wiring this up needs DELETE FROM identities instead.
func (s *DataService) DeleteUser(ctx context.Context, userID string) error {
	selfIdentity := identity.CanonicalID(s.serverID, userID)

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

// FollowUser takes followerID and userID already in userID@serverID form
// (cross-server follows aren't wired up yet, so both are always this
// server's own accounts). user_following/user_followers FK to identities(id).
func (s *DataService) FollowUser(ctx context.Context, followerID, userID string) error {
	followerIdentity := followerID
	targetIdentity := userID

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_following (user_id, following_user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, followerIdentity, targetIdentity)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_followers (user_id, follower_user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, targetIdentity, followerIdentity)
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

	_, err = tx.ExecContext(ctx, `
		DELETE FROM user_followers
		WHERE user_id = $1 AND follower_user_id = $2
	`, targetIdentity, followerIdentity)
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

// userID arrives in userID@serverID form already.
func (s *DataService) GetPublicKey(ctx context.Context, userID string, fingerprint string) (*Key, error) {
	selfIdentity := userID

	var key Key
	var owner string
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
	`, selfIdentity, fingerprint).Scan(
		&key.Fingerprint, &owner, &key.Armor, &key.CreatedAt,
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
	// owner is already identities.id (userID@serverID); the wire shape's
	// UserID field holds that form directly, no bare-decode step.
	key.UserID = owner
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

// userID is the URL-path-identified subject, already in userID@serverID form.
func (s *DataService) GetKeyRevocation(ctx context.Context, userID, fingerprint string) (*KeyRevocation, error) {
	selfIdentity := userID

	var rev KeyRevocation
	var owner string
	var successor sql.NullString
	var reason sql.NullString
	var userSigID, serverSigID int64

	err := s.db.QueryRowContext(ctx, `
		SELECT rv.user_fingerprint, rv.owner, rv.reason, rv.successor,
		       rv.user_signature_id, rv.server_signature_id
		FROM user_key_revocations rv
		WHERE rv.owner = $1 AND rv.user_fingerprint = $2
	`, selfIdentity, fingerprint).Scan(
		&rev.Fingerprint, &owner, &reason, &successor,
		&userSigID, &serverSigID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	// owner is already in userID@serverID form — hold it as-is.
	rev.UserID = owner
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
	selfIdentity := identity.CanonicalID(s.serverID, userID)

	var exists bool

	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM user_keys
			WHERE owner = $1 AND fingerprint = $2
		)
	`, selfIdentity, fingerprint).Scan(&exists)
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
//
// userID is converted to identities.id form once, up front, and used
// everywhere an FK'd column (user_keys.owner, user_key_revocations.owner)
// is touched. The existence lock at the top locks the identities row
// (the actual FK target), not users(id), which is a satellite of it.
func (s *DataService) AddPublicKey(ctx context.Context, in AddPublicKeyInput) (*Key, error) {
	if in.PredecessorFingerprint == "" {
		return nil, ErrPredecessorRequired
	}
	fingerprint := in.Fingerprint
	selfIdentity := identity.CanonicalID(s.serverID, in.UserID)
	createdAt := in.CreatedAt
	expiresAt := in.ExpiresAt
	armor := in.Armor
	predecessor := in.PredecessorFingerprint

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
	`, selfIdentity, predecessor).Scan(new(int))
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
	`, predecessor, selfIdentity).Scan(&successor)
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
	`, selfIdentity).Scan(&hasActive)
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
	var owner string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO user_keys (
			fingerprint, owner, armor, created_at, expires_at,
			server_signature_id,
			predecessor_signature, predecessor_fingerprint
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING fingerprint, owner, armor, created_at
	`, fingerprint, selfIdentity, armor, createdAt, expiresAt,
		serverSignatureID,
		in.PredecessorSignature, predecessor,
	).Scan(&key.Fingerprint, &owner, &key.Armor, &key.CreatedAt)
	if err != nil {
		return nil, err
	}
	// owner is already in userID@serverID form — hold it as-is.
	key.UserID = owner
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

	// users.id is identities.id now — use the already-computed selfIdentity,
	// not the bare in.UserID.
	_, err = tx.ExecContext(ctx, `UPDATE users SET user_fingerprint = $1 WHERE id = $2`, fingerprint, selfIdentity)
	if err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE user_key_revocations
		SET successor = $1
		WHERE user_fingerprint = $2 AND owner = $3
	`, fingerprint, predecessor, selfIdentity)
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

	// A key is revoked iff a row exists in user_key_revocations. UserID
	// arrives in userID@serverID form already.
	selfIdentity := in.UserID
	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_key_revocations (
			user_fingerprint, owner, reason,
			user_signature_id, server_signature_id
		) VALUES ($1, $2, $3, $4, $5)
	`, in.Fingerprint, selfIdentity, in.Reason, userSigID, serverSigID)
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

// CanonicalAuthorID returns AuthorID@ServerID — the form every DB lookup,
// WS subscription key, and broadcast UserID field actually needs. AuthorID
// alone is bare; use this instead of AuthorID at any call site that isn't
// itself recomposing a wire ref (FormatReedRef) or calling
// identity.CanonicalID(ServerID, AuthorID) directly.
func (r ReedRef) CanonicalAuthorID() string {
	return string(identity.CanonicalID(r.ServerID, r.AuthorID))
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
//
// reed_replies.user_id composite-FKs to reeds(user_id, id) (see db.go), so
// it must carry the same value reeds.user_id does. parent is a full
// ReedRef, so parent.ServerID is used rather than assuming local.
func (s *DataService) ResolveThreadIDForParent(ctx context.Context, parent ReedRef) (string, error) {
	parentIdentity := identity.CanonicalID(parent.ServerID, parent.AuthorID)
	var threadID string
	err := s.db.QueryRowContext(ctx, `
		SELECT thread_id FROM reed_replies
		WHERE user_id = $1 AND reed_id = $2
	`, parentIdentity, parent.ReedID).Scan(&threadID)
	if err == sql.ErrNoRows {
		return FormatReedRef(parent), nil
	}
	if err != nil {
		return "", err
	}
	return threadID, nil
}

// InsertReply records a direct reply in reed_replies. replyUserID is the
// local, session-authenticated reply author, converted to userID@serverID
// form here, once, before delegating to insertReplyTx.
func (s *DataService) InsertReply(
	ctx context.Context,
	threadID string,
	parent ReedRef,
	replyUserID, replyReedID string,
	ts time.Time,
) (replyIndexed bool, err error) {
	ts = ts.UTC().Truncate(time.Second)
	replyIdentity := identity.CanonicalID(s.serverID, replyUserID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if err = s.insertReplyTx(ctx, tx, threadID, parent, replyIdentity, replyReedID, ts); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// insertReplyTx takes replyIdentity already in userID@serverID form (the
// caller has either converted a local userID via identity.CanonicalID, or
// is insertReedCoreTx's selfIdentity). parent's identity is built the same
// way ResolveThreadIDForParent does, from the full ReedRef.
func (s *DataService) insertReplyTx(
	ctx context.Context,
	tx *sql.Tx,
	threadID string,
	parent ReedRef,
	replyIdentity identity.IdentityID,
	replyReedID string,
	ts time.Time,
) error {
	parentIdentity := identity.CanonicalID(parent.ServerID, parent.AuthorID)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO reed_replies (
			thread_id, user_id, reed_id,
			parent_user_id, parent_reed_id,
			timestamp
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, threadID, replyIdentity, replyReedID,
		parentIdentity, parent.ReedID,
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
//
// parentUserID is the URL-path-identified reed author, already in
// userID@serverID form. reed_replies.user_id (scanned into ReplyListItem
// below) carries that same form, so the wire item's UserID field holds it
// directly with no decode step.
func (s *DataService) ListReplies(ctx context.Context, parentUserID, parentReedID string, limit int, before *time.Time) (*ReplyListResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	parentIdentity := parentUserID
	args := []any{parentIdentity, parentReedID}
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
		var userID string
		var reedID string
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
func checkReedTipTx(ctx context.Context, tx *sql.Tx, selfIdentity identity.IdentityID, previousID string) error {
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
		      WHERE rm.user_id = r.user_id AND rm.reed_id = r.id
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
// db.go's FOREIGN KEY clauses for reeds, reed_allocations, pending_fanout,
// reed_mentions). Mention targets (p.Mentions) are converted the same way,
// via identity.CanonicalID, since only local mentions are inserted today.
func (s *DataService) insertReedCoreTx(
	ctx context.Context,
	tx *sql.Tx,
	p createReedParams,
) (Reed, error) {
	if !crypto.IsValidUUIDv7(p.ReedID) {
		return Reed{}, fmt.Errorf("invalid reed ID")
	}

	// p.UserID arrives in userID@serverID form already; checkReedTipTx
	// requires identity.IdentityID, so this is a plain type conversion.
	selfIdentity := identity.IdentityID(p.UserID)

	if err := checkReedTipTx(ctx, tx, selfIdentity, p.PreviousID); err != nil {
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
	var createdOwner string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO reeds (
			id, user_id, private_key_fingerprint, signed_at,
			user_signature_id, server_signature_id, allocation_count
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1)
		RETURNING id, user_id, private_key_fingerprint, signed_at
	`, p.ReedID, selfIdentity, p.ServerFingerprint, ts, userSigID, serverSigID).Scan(
		&created.ID,
		&createdOwner,
		&created.Fingerprint,
		&created.Timestamp,
	)
	if err != nil {
		return Reed{}, err
	}
	// Reed.UserID is the wire shape (json:"userID") — holds this value
	// directly, no decode step.
	created.UserID = createdOwner

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_allocations (reed_id, holder_user_id, author_user_id)
		VALUES ($1, $2, $3)
	`, p.ReedID, selfIdentity, selfIdentity); err != nil {
		return Reed{}, fmt.Errorf("allocate reed to author: %w", err)
	}

	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO pending_fanout (user_id, reed_id, tags)
		VALUES ($1, $2, $3)
	`, selfIdentity, p.ReedID, pq.Array(tags)); err != nil {
		return Reed{}, fmt.Errorf("insert pending fanout: %w", err)
	}

	for _, m := range p.Mentions {
		// m.ServerID names whatever server the mention syntax pointed at,
		// which in principle could be foreign.
		mentionTarget := identity.CanonicalID(m.ServerID, m.AuthorID)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reed_mentions (
				mentioning_user_id, mentioning_reed_id,
				mentioned_user_id, mentioned_server_id
			) VALUES ($1, $2, $3, $4)
			ON CONFLICT (mentioning_reed_id, mentioned_server_id, mentioned_user_id) DO NOTHING
		`, selfIdentity, p.ReedID, mentionTarget, m.ServerID); err != nil {
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
// echoIndexed is true when a new reed_echoes row was inserted. isBlank
// records whether the echoing reed carried no commentary — see is_blank on
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

	// echoing_user_id is a direct FK to identities(id); p.UserID arrives in
	// userID@serverID form already. echoed_user_id has no FK (see db.go),
	// but CountEchoes/GetReedChorus filter on it in canonical form (it's
	// the URL-path-identified reed author elsewhere), so compose it the
	// same way here rather than storing echoTarget.AuthorID bare.
	selfIdentity := identity.IdentityID(p.UserID)
	echoedUserID := identity.CanonicalID(echoTarget.ServerID, echoTarget.AuthorID)
	res, err := tx.ExecContext(ctx, `
		INSERT INTO reed_echoes (echoing_user_id, echoing_reed_id, echoed_user_id, echoed_reed_id, is_blank, signed_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (echoing_user_id, echoing_reed_id) DO NOTHING
	`, selfIdentity, p.ReedID, echoedUserID, echoTarget.ReedID, isBlank, ts)
	if err != nil {
		return nil, false, fmt.Errorf("insert echo index: %w", err)
	}
	n, _ := res.RowsAffected()
	echoIndexed = n > 0 && echoedUserID != selfIdentity

	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &created, echoIndexed, nil
}

// ErrReedNotFound is returned by IsBlankEcho when (authorID, reedID) is not
// a live tip reed.
var ErrReedNotFound = errors.New("reed not found")

// IsBlankEcho reports whether reed (authorID, reedID) is itself a
// content-less echo — used to reject a reply/echo aimed at it instead of
// the underlying original. Returns false (not an error) when the reed
// exists but isn't an echo. Returns ErrReedNotFound when the reed doesn't
// exist — a missing reed is not the same as "not blank" and callers must
// not conflate the two.
//
// authorID accepts either shape: bare (ReedRef.AuthorID) or already in
// userID@serverID form (checkRippleParentReed's URL-sourced userID) —
// same dual-shape acceptance pattern as BindDevice.
func (s *DataService) IsBlankEcho(ctx context.Context, authorID, reedID string) (bool, error) {
	exists, err := s.ReedExists(ctx, authorID, reedID)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, ErrReedNotFound
	}

	// echoing_user_id is a direct FK to identities(id).
	selfIdentity := identity.CanonicalID(s.serverID, authorID)
	if bare, _, ok := identity.ParseIdentityID(identity.IdentityID(authorID)); ok {
		selfIdentity = identity.CanonicalID(s.serverID, bare)
	}
	var isBlank bool
	err = s.db.QueryRowContext(ctx, `
		SELECT is_blank FROM reed_echoes
		WHERE echoing_user_id = $1 AND echoing_reed_id = $2
	`, selfIdentity, reedID).Scan(&isBlank)
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

	// p.UserID arrives in userID@serverID form already; insertReplyTx
	// requires identity.IdentityID, so this is a plain type conversion.
	selfIdentity := identity.IdentityID(p.UserID)
	if err = s.insertReplyTx(ctx, tx, threadID, parent, selfIdentity, p.ReedID, ts); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &created, nil
}

// GetReedAttestation loads a tip reed and its stored signatures. userID
// arrives in userID@serverID form already, matching reeds.user_id.
func (s *DataService) GetReedAttestation(ctx context.Context, userID, reedID string) (*ReedAttestation, error) {
	selfIdentity := userID

	var att ReedAttestation
	var owner string
	err := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.user_id, r.private_key_fingerprint, r.signed_at,
			us.fingerprint, us.signature,
			ss.fingerprint, ss.signature, ss.signed_at
		FROM reeds r
		JOIN user_signatures us ON us.id = r.user_signature_id
		JOIN server_signatures ss ON ss.id = r.server_signature_id
		WHERE r.id = $1 AND r.user_id = $2
	`, reedID, selfIdentity).Scan(
		&att.ID,
		&owner,
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
	att.UserID = owner
	att.Timestamp = att.Timestamp.UTC().Truncate(time.Second)
	att.ServerSignedAt = att.ServerSignedAt.UTC().Truncate(time.Second)
	return &att, nil
}

// Reed ids are scoped to (user_id, id); there is no global author lookup by reed id alone.

// DeleteReed's userID is the local, session-authenticated author deleting
// their own reed, already in userID@serverID form.
func (s *DataService) DeleteReed(ctx context.Context, userID, reedID string) error {
	selfIdentity := userID
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reeds
		WHERE user_id = $1 AND id = $2
	`, selfIdentity, reedID)
	if err != nil {
		return err
	}

	return nil
}

// ReedExists reports whether (userID, reedID) is a live tip reed. userID
// accepts either shape (bare or userID@serverID), same as IsBlankEcho,
// since its only caller forwards whatever shape it itself received.
func (s *DataService) ReedExists(ctx context.Context, userID, reedID string) (bool, error) {
	selfIdentity := identity.CanonicalID(s.serverID, userID)
	if bare, _, ok := identity.ParseIdentityID(identity.IdentityID(userID)); ok {
		selfIdentity = identity.CanonicalID(s.serverID, bare)
	}
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
	`, selfIdentity, reedID).Scan(&exists)
	return exists, err
}

// MentionTargetValid reports whether userID exists, is not account-removed,
// and serverID is a known row in servers — the gate for a mention to be
// indexed. Checks `users`/`account_removals` directly rather than the
// `verified_identities` view; revisit once foreign mentions are wired up.
func (s *DataService) MentionTargetValid(ctx context.Context, userID, serverID string) (bool, error) {
	targetIdentity := identity.CanonicalID(serverID, userID)
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
	// account_removals.user_id FKs to identities(id), and u.id is that same
	// form directly now (identity_id no longer exists as a separate
	// column) — join against u.id on both sides.
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

// CountEchoes returns how many echoes point at the given reed. Self-echoes
// (a user echoing their own reed) are excluded.
//
// echoedUserID is the URL-path-identified reed author, already in
// userID@serverID form.
func (s *DataService) CountEchoes(ctx context.Context, echoedUserID, echoedReedID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT echoing_user_id) FROM reed_echoes
		WHERE echoed_user_id = $1 AND echoed_reed_id = $2
		AND echoing_user_id != echoed_user_id
	`, echoedUserID, echoedReedID).Scan(&n)
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
//
// echoedUserID is the URL-path-identified reed author, already in
// userID@serverID form.
func (s *DataService) GetReedChorus(ctx context.Context, echoedUserID, echoedReedID string, limit int, before *time.Time) (*EchoerListResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	// A user can echo the same target more than once (separate echoing
	// reeds), so group by echoing_user_id and take their earliest echo as
	// the row's timestamp — the chorus lists each person once.
	args := []any{echoedUserID, echoedReedID}
	query := `
		SELECT re.echoing_user_id, MIN(re.signed_at) AS first_echoed_at
		FROM reed_echoes re
		WHERE re.echoed_user_id = $1 AND re.echoed_reed_id = $2
		AND re.echoing_user_id != re.echoed_user_id
		AND NOT EXISTS (
			SELECT 1 FROM account_removals ar WHERE ar.user_id = re.echoing_user_id
		)
		GROUP BY re.echoing_user_id
	`
	if before != nil {
		args = append(args, before.UTC().Truncate(time.Second))
		query += fmt.Sprintf(`
			HAVING (MIN(re.signed_at), re.echoing_user_id) > ($%d, '')
		`, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(`
		ORDER BY first_echoed_at ASC, re.echoing_user_id ASC
		LIMIT $%d
	`, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// re.echoing_user_id is a direct FK to identities(id); the wire item's
	// UserID field holds that value directly, same as ListReplies.
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
//
// userID is the removed reed's own author, already in userID@serverID
// form, matching echoing_user_id directly. echoed_user_id has no FK (see
// db.go) but is stored canonical too (see CreateReedWithEcho), so the
// final DELETE (matching this reed as an echo TARGET) uses userID as-is.
func (s *DataService) DeleteEchoIndexForReed(ctx context.Context, userID, reedID string) ([]ReedRef, error) {
	selfIdentity := identity.IdentityID(userID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT echoed_user_id, echoed_reed_id
		FROM reed_echoes
		WHERE echoing_user_id = $1 AND echoing_reed_id = $2
	`, selfIdentity, reedID)
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
	`, selfIdentity, reedID); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_echoes WHERE echoed_user_id = $1 AND echoed_reed_id = $2
	`, selfIdentity, reedID); err != nil {
		return nil, err
	}
	return targets, nil
}

// DeleteEchoesByAuthor drops echo index rows created by userID (the echoing
// author). Returns distinct echoed targets whose counts may have changed.
// userID arrives in userID@serverID form already.
func (s *DataService) DeleteEchoesByAuthor(ctx context.Context, userID string) ([]ReedRef, error) {
	selfIdentity := identity.IdentityID(userID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT echoed_user_id, echoed_reed_id
		FROM reed_echoes
		WHERE echoing_user_id = $1
	`, selfIdentity)
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

	if _, err := s.db.ExecContext(ctx, `DELETE FROM reed_echoes WHERE echoing_user_id = $1`, selfIdentity); err != nil {
		return nil, err
	}
	return targets, nil
}

// DeleteMentionsForReed clears mention index rows contained in a removed
// reed. mentioning_user_id composite-FKs to reeds(user_id, id), same as
// reed_replies.user_id; userID arrives in userID@serverID form already.
func (s *DataService) DeleteMentionsForReed(ctx context.Context, userID, reedID string) error {
	selfIdentity := userID
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_mentions
		WHERE mentioning_user_id = $1 AND mentioning_reed_id = $2
	`, selfIdentity, reedID)
	return err
}

// DeleteMentionsByAuthor clears mention index rows on account removal: rows
// the removed user authored (mentioning), and rows mentioning the removed
// user on this server (mentioned) — both sides. userID arrives in
// userID@serverID form already, so selfIdentity is correct on both sides
// of the OR.
func (s *DataService) DeleteMentionsByAuthor(ctx context.Context, userID string) error {
	selfIdentity := userID
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM reed_mentions
		WHERE mentioning_user_id = $1
		   OR (mentioned_server_id = $2 AND mentioned_user_id = $1)
	`, selfIdentity, s.serverID)
	return err
}

// ReplyCountNotifyTargets returns every ancestor of parent (inclusive) whose
// subtree reply count changes when a direct reply to parent is added or removed.
//
// parentUserID is always local today. The walk stays in userID@serverID
// form internally and only decodes back to bare when building each
// ReedRef, to match every other ReedRef construction in this file
// (AuthorID holds the bare userID, ServerID is separate).
func (s *DataService) ReplyCountNotifyTargets(ctx context.Context, parentUserID, parentReedID string) ([]ReedRef, error) {
	var targets []ReedRef
	selfIdentity, reedID := identity.CanonicalID(s.serverID, parentUserID), parentReedID
	for {
		targets = append(targets, ReedRef{AuthorID: selfIdentity.UserID(), ServerID: selfIdentity.ServerID(), ReedID: reedID})
		var nextIdentity identity.IdentityID
		var nextReedID string
		err := s.db.QueryRowContext(ctx, `
			SELECT parent_user_id, parent_reed_id
			FROM reed_replies
			WHERE user_id = $1 AND reed_id = $2
		`, selfIdentity, reedID).Scan(&nextIdentity, &nextReedID)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return nil, err
		}
		selfIdentity, reedID = nextIdentity, nextReedID
	}
	return targets, nil
}

// ReplyCountNotifyTargetsForRemovedReply returns ancestors whose subtree count
// drops when replyUserID/replyReedID is removed. nil when not indexed as a reply.
func (s *DataService) ReplyCountNotifyTargetsForRemovedReply(ctx context.Context, replyUserID, replyReedID string) ([]ReedRef, error) {
	replyIdentity := identity.CanonicalID(s.serverID, replyUserID)
	var parentIdentity identity.IdentityID
	var parentReedID string
	err := s.db.QueryRowContext(ctx, `
		SELECT parent_user_id, parent_reed_id
		FROM reed_replies
		WHERE user_id = $1 AND reed_id = $2
	`, replyIdentity, replyReedID).Scan(&parentIdentity, &parentReedID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.ReplyCountNotifyTargets(ctx, parentIdentity.UserID(), parentReedID)
}

// ReplyCountNotifyTargetsForAuthor returns distinct ancestors whose subtree
// counts may change when all of userID's indexed replies are treated as removed.
func (s *DataService) ReplyCountNotifyTargetsForAuthor(ctx context.Context, userID string) ([]ReedRef, error) {
	selfIdentity := identity.CanonicalID(s.serverID, userID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT parent_user_id, parent_reed_id
		FROM reed_replies
		WHERE user_id = $1
	`, selfIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	var targets []ReedRef
	for rows.Next() {
		var parentIdentity identity.IdentityID
		var parentReedID string
		if err := rows.Scan(&parentIdentity, &parentReedID); err != nil {
			return nil, err
		}
		ancestors, err := s.ReplyCountNotifyTargets(ctx, parentIdentity.UserID(), parentReedID)
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
	selfIdentity := identity.CanonicalID(s.serverID, userID)
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
	`, selfIdentity, reedID).Scan(&count)
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

// userID is the URL-path-identified reed author, already in
// userID@serverID form.
func (s *DataService) GetReed(ctx context.Context, userID string, reedID string) (*Reed, error) {
	selfIdentity := userID
	var reed Reed
	var owner string
	err := s.db.QueryRowContext(ctx, `
	SELECT id, user_id, private_key_fingerprint, signed_at
		FROM reeds
		WHERE id = $1 AND user_id = $2
	`, reedID, selfIdentity,
	).Scan(
		&reed.ID,
		&owner,
		&reed.Fingerprint,
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
// userID arrives in userID@serverID form; deletion.GetCert's lookup param
// is bare, so decode before delegating.
func (s *DataService) GetReedRemoval(ctx context.Context, userID, reedID string) (*deletion.Cert, error) {
	bareUserID := userID
	if bare, _, ok := identity.ParseIdentityID(identity.IdentityID(userID)); ok {
		bareUserID = bare
	}
	return deletion.GetCert(ctx, s.db, bareUserID, reedID, s.serverID)
}

// InsertReedRemoval persists a reed-removal cert (idempotent / conflict).
// cert.UserID arrives in userID@serverID form; deletion.InsertCert's
// cert.UserID is bare, so decode before delegating.
func (s *DataService) InsertReedRemoval(ctx context.Context, cert deletion.Cert) error {
	if bare, _, ok := identity.ParseIdentityID(identity.IdentityID(cert.UserID)); ok {
		cert.UserID = bare
	}
	return deletion.InsertCert(ctx, s.db, cert, s.serverID)
}

// GetAccountRemoval returns the stored account-removal cert for userID.
// userID arrives in userID@serverID form; deletion.GetAccountCert's
// lookup param is bare, so decode before delegating.
func (s *DataService) GetAccountRemoval(ctx context.Context, userID string) (*deletion.AccountCert, error) {
	bareUserID := userID
	if bare, _, ok := identity.ParseIdentityID(identity.IdentityID(userID)); ok {
		bareUserID = bare
	}
	return deletion.GetAccountCert(ctx, s.db, bareUserID, s.serverID)
}

// InsertAccountRemoval persists an account-removal cert (idempotent / conflict).
// cert.UserID arrives in userID@serverID form; deletion.InsertAccountCert's
// cert.UserID is bare, so decode before delegating.
func (s *DataService) InsertAccountRemoval(ctx context.Context, cert deletion.AccountCert) error {
	if bare, _, ok := identity.ParseIdentityID(identity.IdentityID(cert.UserID)); ok {
		cert.UserID = bare
	}
	return deletion.InsertAccountCert(ctx, s.db, cert, s.serverID)
}

// HasAccountRemoval reports whether userID has an account-removal row.
// userID arrives in userID@serverID form; deletion.HasAccountRemoval's
// lookup param is bare, so decode before delegating.
func (s *DataService) HasAccountRemoval(ctx context.Context, userID string) (bool, error) {
	bareUserID := userID
	if bare, _, ok := identity.ParseIdentityID(identity.IdentityID(userID)); ok {
		bareUserID = bare
	}
	return deletion.HasAccountRemoval(ctx, s.db, bareUserID, s.serverID)
}

// ErrLikeConflict is returned when an existing like row differs from the
// cert being inserted (identical replay succeeds).
var ErrLikeConflict = errors.New("like conflict")

// GetReedLike returns the stored like cert for (likerID, authorID, reedID),
// or nil if the reed is not liked by that user. Both likerID and authorID
// arrive in userID@serverID form; loadLikeCertTx requires
// identity.IdentityID, so this is a plain type conversion.
func (s *DataService) GetReedLike(ctx context.Context, likerID, authorID, reedID string) (*LikeCert, error) {
	likerIdentity := identity.IdentityID(likerID)
	authorIdentity := identity.IdentityID(authorID)
	cert, err := s.loadLikeCertTx(ctx, s.db, likerIdentity, authorIdentity, reedID, false)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cert, nil
}

// InsertReedLike stores a like cert once and bumps reeds.like_count in
// the same TX. Same signatures → no-op (idempotent replay); different
// signatures for the same (likerID, authorID, reedID) → ErrLikeConflict.
// likerID and likerFingerprint key the reeds_liked row. Both likerID and
// cert.AuthorID arrive in userID@serverID form already; see GetReedLike's
// comment for the type-conversion reasoning.
func (s *DataService) InsertReedLike(ctx context.Context, likerID, likerFingerprint string, cert LikeCert) error {
	cert.ServerSignature.SignedAt = cert.ServerSignature.SignedAt.UTC().Truncate(time.Second)
	likerIdentity := identity.IdentityID(likerID)
	authorIdentity := identity.IdentityID(cert.AuthorID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing, err := s.loadLikeCertTx(ctx, tx, likerIdentity, authorIdentity, cert.ReedID, true)
	switch {
	case err == sql.ErrNoRows:
		userSigID, err := signing.InsertUserSignature(ctx, tx, cert.UserSignature.Fingerprint, cert.UserSignature.Armor)
		if err != nil {
			return err
		}
		serverSigID, err := signing.InsertServerSignature(ctx, tx, cert.ServerSignature.Fingerprint, cert.ServerSignature.Armor, cert.ServerSignature.SignedAt)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reeds_liked (
				liker_user_id, author_user_id, reed_id, liker_fingerprint,
				user_signature_id, server_signature_id
			) VALUES ($1, $2, $3, $4, $5, $6)
		`, likerIdentity, authorIdentity, cert.ReedID, likerFingerprint, userSigID, serverSigID); err != nil {
			return fmt.Errorf("insert reed like: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE reeds SET like_count = like_count + 1
			WHERE user_id = $1 AND id = $2
		`, authorIdentity, cert.ReedID); err != nil {
			return fmt.Errorf("bump like_count: %w", err)
		}
	case err != nil:
		return err
	default:
		if existing.UserSignature.Armor != cert.UserSignature.Armor ||
			existing.UserSignature.Fingerprint != cert.UserSignature.Fingerprint ||
			existing.ServerSignature.Armor != cert.ServerSignature.Armor ||
			existing.ServerSignature.Fingerprint != cert.ServerSignature.Fingerprint ||
			!existing.ServerSignature.SignedAt.Equal(cert.ServerSignature.SignedAt) {
			return ErrLikeConflict
		}
	}

	return tx.Commit()
}

// DeleteReedLike hard-deletes the like row for (likerID, authorID, reedID)
// if present and decrements reeds.like_count in the same TX. Deleting a
// nonexistent row is a no-op, returning deleted=false with no error.
// Both likerID and authorID arrive in userID@serverID form already.
func (s *DataService) DeleteReedLike(ctx context.Context, likerID, authorID, reedID string) (deleted bool, err error) {
	likerIdentity := identity.IdentityID(likerID)
	authorIdentity := identity.IdentityID(authorID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		DELETE FROM reeds_liked
		WHERE liker_user_id = $1 AND author_user_id = $2 AND reed_id = $3
	`, likerIdentity, authorIdentity, reedID)
	if err != nil {
		return false, fmt.Errorf("delete reed like: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE reeds SET like_count = GREATEST(0, like_count - 1)
		WHERE user_id = $1 AND id = $2
	`, authorIdentity, reedID); err != nil {
		return false, fmt.Errorf("decrement like_count: %w", err)
	}

	return true, tx.Commit()
}

// CountLikes returns the current like count for a reed, read from the
// denormalized reeds.like_count column (never COUNT(*) on a hot path).
// authorID names the reed's author, local today.
func (s *DataService) CountLikes(ctx context.Context, authorID, reedID string) (int, error) {
	authorIdentity := identity.CanonicalID(s.serverID, authorID)
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT like_count FROM reeds WHERE user_id = $1 AND id = $2
	`, authorIdentity, reedID).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return count, err
}

type likeQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// loadLikeCertTx takes likerID/authorID already in userID@serverID form —
// see callers (GetReedLike, InsertReedLike, DeleteReedLike), which convert
// once at their own boundary before calling in.
func (s *DataService) loadLikeCertTx(ctx context.Context, q likeQuerier, likerIdentity, authorIdentity identity.IdentityID, reedID string, forUpdate bool) (*LikeCert, error) {
	query := `
		SELECT liker_fingerprint, user_signature_id, server_signature_id
		FROM reeds_liked
		WHERE liker_user_id = $1 AND author_user_id = $2 AND reed_id = $3`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var likerFP string
	var userSigID, serverSigID int64
	err := q.QueryRowContext(ctx, query, likerIdentity, authorIdentity, reedID).Scan(&likerFP, &userSigID, &serverSigID)
	if err != nil {
		return nil, err
	}

	// signing helpers need DBTX; *sql.DB and *sql.Tx both satisfy it.
	dbtx, ok := q.(signing.DBTX)
	if !ok {
		return nil, fmt.Errorf("reed like load: querier is not signing.DBTX")
	}
	userRow, err := signing.GetUserSignature(ctx, dbtx, userSigID)
	if err != nil {
		return nil, err
	}
	serverRow, err := signing.GetServerSignature(ctx, dbtx, serverSigID)
	if err != nil {
		return nil, err
	}
	return &LikeCert{
		// AuthorID holds this value directly — no decode step.
		AuthorID: authorIdentity.String(),
		ReedID:   reedID,
		UserSignature: UserSignature{
			Fingerprint: userRow.Fingerprint,
			Armor:       userRow.Signature,
		},
		ServerSignature: ServerSignature{
			Fingerprint: serverRow.Fingerprint,
			Armor:       serverRow.Signature,
			SignedAt:    serverRow.SignedAt,
		},
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
	selfIdentity := userID
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id
		FROM reeds r
		WHERE r.user_id = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM reed_removals rr
		      WHERE rr.user_id = r.user_id AND rr.reed_id = r.id
		  )
		ORDER BY r.signed_at DESC, r.id DESC
	`, selfIdentity)
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
	deviceID, err := identity.ParseDeviceID(deviceID)
	if err != nil {
		return err
	}
	selfIdentity := identity.CanonicalID(s.serverID, userID)

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
// BindDeviceTx, which composes internally (identity.CanonicalID) — decode
// back to bare first via identity.ParseIdentityID to avoid double-composing,
// matching what BindDeviceTx expects from its other (Signup) caller.
func (s *DataService) BindDevice(ctx context.Context, userID, deviceID string, now time.Time) error {
	bareUserID := userID
	if bare, _, ok := identity.ParseIdentityID(identity.IdentityID(userID)); ok {
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
// side — populated on the responder by OutgoingFederationAttempt (see its
// doc comment); the initiator has no equivalent row until 03's approval
// step lands (spec: specs/federation/03_approval_established.md).
// No fingerprint field: peer.Fingerprint is never persisted to servers.signing_key
// (that column means this server's OWN signing key, joined against
// private_keys — see InitServerKey/GetServerSigningKeyArmor) or anywhere
// else queryable today.
type federationServerListRow struct {
	ID        string
	Name      string
	BaseURL   string
	Connected bool
	CreatedAt time.Time
}

// ListFederationServers returns known peer servers (self excluded) — the
// responder-side view of federation status while 03's approval workflow
// (federation_established, /pending, approve/reject) is still unbuilt.
func (s *DataService) ListFederationServers(ctx context.Context) ([]federationServerListRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(base_url, ''), connected, created_at
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
		if err := rows.Scan(&row.ID, &row.Name, &row.BaseURL, &row.Connected, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// errFederationServerNotFound is returned by ApproveFederationServer and
// RejectFederationServer when serverID doesn't match any peer row.
var errFederationServerNotFound = errors.New("federation server not found")

// ApproveFederationServer is the minimal "for now" approval action (no
// federation_attempt table yet — see specs/federation/03): flips connected
// to TRUE on an existing peer server row. A real second-admin-approval
// workflow, and the distinction between "who created the attempt" vs "who
// approved it," is future work; today any admin can approve.
func (s *DataService) ApproveFederationServer(ctx context.Context, serverID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE servers SET connected = TRUE WHERE id = $1 AND self = FALSE
	`, serverID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errFederationServerNotFound
	}
	return nil
}

// RejectFederationServer logs the rejection reason, then deletes the peer
// server row — federation_server_log cascade-deletes with it (see db.go's
// FK), so the reason line doesn't survive the reject. That's a known gap:
// once federation_attempt exists (specs/federation), logs move there and
// outlive the rejected attempt. For now, "eliminate the server entry" is
// literal: no live row is left for a rejected peer.
func (s *DataService) RejectFederationServer(ctx context.Context, serverID, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM servers WHERE id = $1 AND self = FALSE)
	`, serverID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errFederationServerNotFound
	}

	logID, err := crypto.NewID()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_log (id, level, message) VALUES ($1, $2, $3)
	`, logID, federationLogError, fmt.Sprintf("Rejected: %s", reason)); err != nil {
		return fmt.Errorf("insert federation log: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_server_log (server_id, log_id) VALUES ($1, $2)
	`, serverID, logID); err != nil {
		return fmt.Errorf("link federation server log: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM servers WHERE id = $1 AND self = FALSE
	`, serverID); err != nil {
		return fmt.Errorf("delete rejected server: %w", err)
	}

	return tx.Commit()
}

type federationServerLogRow struct {
	ID        string
	Level     string
	Message   string
	CreatedAt time.Time
}

// listFederationLog reads federation_log lines through one of its two
// junction tables — federation_server_log (junctionTable="federation_server_log",
// junctionCol="server_id") or federation_invitation_log
// (junctionCol="invitation_id") — see logFederationServer/
// logFederationInvitation's doc comments for which handshake steps write to
// which.
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

type federationInvitationListRow struct {
	ID                   string
	Name                 string
	Status               string
	CreatedBy            string
	CreatedByUsername    string
	Fingerprint          string
	CreatedAt            time.Time
	AcceptedAt           *time.Time
	ServerID             string
	ReviewedBy           string
	ReviewedByUsername   string
	ReviewedAt           *time.Time
	ConnectionCiphertext string
}

// InsertFederationInvitation records publicKey into public_keys (upserted,
// since the same peer key may already be on file) and inserts the
// invitation row referencing it by fingerprint, atomically. createdBy
// arrives in userID@serverID form already.
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
		INSERT INTO public_keys (fingerprint, armor) VALUES ($1, $2)
		ON CONFLICT (fingerprint) DO NOTHING
	`, fingerprint, publicKey); err != nil {
		return fmt.Errorf("insert remote public key: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO federation_invitation (
			id, name, secret_hash, fingerprint, created_by, status, created_at,
			connection_ciphertext
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, id, name, secretHash, fingerprint, createdByIdentity, federationStatusNew, createdAt.UTC(), connectionCiphertext); err != nil {
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
		SELECT fi.id, fi.name, fi.secret_hash, fi.fingerprint, pk.armor, fi.created_by, fi.status,
		       fi.created_at, fi.accepted_at, fi.server_id
		FROM federation_invitation fi
		JOIN public_keys pk ON pk.fingerprint = fi.fingerprint
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

// ListFederationInvitations joins users twice for display names
// (creator/reviewer), same pattern as GetUserProfile's invited_by
// self-join. reviewed_by is nullable, COALESCE'd to "" in SQL.
// ListFederationInvitations excludes accepted/approved invitations —
// once accepted, an invitation can no longer change state (it's a
// finished handshake, live or not), so it moves to living under the
// resulting server's own page (see ListFederationInvitationForServer)
// instead of cluttering the pending-invite list forever.
func (s *DataService) ListFederationInvitations(ctx context.Context) ([]federationInvitationListRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fi.id, fi.name, fi.status, fi.created_by, COALESCE(creator.username, ''),
		       fi.fingerprint, fi.created_at, fi.accepted_at, COALESCE(fi.server_id, ''),
		       COALESCE(fi.reviewed_by, ''), COALESCE(reviewer.username, ''), fi.reviewed_at,
		       COALESCE(fi.connection_ciphertext, '')
		FROM federation_invitation fi
		JOIN users creator ON creator.id = fi.created_by
		LEFT JOIN users reviewer ON reviewer.id = fi.reviewed_by
		WHERE fi.status NOT IN ($1, $2)
		ORDER BY fi.created_at DESC
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
			&row.CreatedByUsername,
			&row.Fingerprint,
			&row.CreatedAt,
			&acceptedAt,
			&row.ServerID,
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
		SELECT fi.id, fi.name, fi.status, fi.created_by, COALESCE(creator.username, ''),
		       fi.fingerprint, fi.created_at, fi.accepted_at, COALESCE(fi.server_id, ''),
		       COALESCE(fi.reviewed_by, ''), COALESCE(reviewer.username, ''), fi.reviewed_at,
		       COALESCE(fi.connection_ciphertext, '')
		FROM federation_invitation fi
		JOIN users creator ON creator.id = fi.created_by
		LEFT JOIN users reviewer ON reviewer.id = fi.reviewed_by
		WHERE fi.server_id = $1
	`, serverID).Scan(
		&row.ID,
		&row.Name,
		&row.Status,
		&row.CreatedBy,
		&row.CreatedByUsername,
		&row.Fingerprint,
		&row.CreatedAt,
		&acceptedAt,
		&row.ServerID,
		&row.ReviewedBy,
		&row.ReviewedByUsername,
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
// handshake, as recorded into the shared servers table.
type federationPeer struct {
	ServerID    string
	ServerName  string
	BaseURL     string
	Fingerprint string
	PublicKey   string
}

// RedeemFederationInvitation runs on the RESPONDER, before it even
// attempts the handshake: it records the peer server (self=FALSE,
// connected=FALSE) so there's somewhere to log against from the first
// moment, rather than only writing anything once the handshake already
// succeeded. peer.PublicKey is upserted into public_keys. Idempotent on
// peer.ServerID via ON CONFLICT DO NOTHING (retried pastes of the same
// connection string shouldn't error).
func (s *DataService) RedeemFederationInvitation(ctx context.Context, peer federationPeer, createdAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public_keys (fingerprint, armor) VALUES ($1, $2)
		ON CONFLICT (fingerprint) DO NOTHING
	`, peer.Fingerprint, peer.PublicKey); err != nil {
		return fmt.Errorf("insert remote public key: %w", err)
	}

	name := peer.ServerName
	if name == "" {
		name = peer.ServerID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO servers (id, name, self, base_url, connected, created_at)
		VALUES ($1, $2, FALSE, $3, FALSE, $4)
		ON CONFLICT (id) DO NOTHING
	`, peer.ServerID, name, peer.BaseURL, createdAt.UTC()); err != nil {
		return fmt.Errorf("insert federation peer: %w", err)
	}

	return tx.Commit()
}

// MarkFederationInvitationAccepted runs on the INITIATOR when a remote
// server's connect callback verifies successfully: it records the peer
// server (self=FALSE, connected=TRUE immediately — the initiator's own
// verification of the callback IS the confirmation, there's no further
// round trip needed on this side) and moves the invitation new -> accepted
// with server_id set, atomically. Returns errFederationInvitationNotFound
// if id doesn't exist, errFederationInvitationNotNew if it exists but
// isn't in status "new".
func (s *DataService) MarkFederationInvitationAccepted(ctx context.Context, inviteID string, peer federationPeer, acceptedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var status string
	if err := tx.QueryRowContext(ctx, `
		SELECT status FROM federation_invitation WHERE id = $1 FOR UPDATE
	`, inviteID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errFederationInvitationNotFound
		}
		return err
	}
	if status != federationStatusNew {
		return errFederationInvitationNotNew
	}

	// connected stays FALSE here: the handshake having verified (valid
	// signatures, reachable peer) is not the same as a second admin having
	// approved the connection (spec 03, not yet built) — connected must not
	// go TRUE until that approval step exists and actually runs.
	name := peer.ServerName
	if name == "" {
		name = peer.ServerID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO servers (id, name, self, base_url, connected, created_at)
		VALUES ($1, $2, FALSE, $3, FALSE, $4)
		ON CONFLICT (id) DO UPDATE SET base_url = EXCLUDED.base_url, name = EXCLUDED.name
	`, peer.ServerID, name, peer.BaseURL, acceptedAt.UTC()); err != nil {
		return fmt.Errorf("insert federation peer: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE federation_invitation
		SET status = $2, accepted_at = $3, connection_ciphertext = NULL, server_id = $4
		WHERE id = $1
	`, inviteID, federationStatusAccepted, acceptedAt.UTC(), peer.ServerID); err != nil {
		return err
	}

	return tx.Commit()
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
	logID, err := crypto.NewID()
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
// serverID via federation_server_log — used by the responder (which never
// has a local invitation row) from the moment it records the peer server,
// and by the initiator for server-level events once server_id is known
// (post-acceptance) — see logFederationInvitation.
func (s *DataService) logFederationServer(ctx context.Context, serverID, level, message string) error {
	logID, err := crypto.NewID()
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
	UserFingerprint string
	UserSignature   UserSignature
	ServerSignature ServerSignature
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
// reedAuthorID and userID arrive already in userID@serverID form.
// identity.BuildRippleServerPayload still expects bare, so
// reedAuthorIdentity.UserID()/selfIdentity.UserID() decode just for that
// call; the returned Ripple struct holds the wire form directly instead.
func (s *DataService) PostRipple(
	ctx context.Context,
	reedAuthorID, reedID, userID, content, threadID string,
	replyingTo *string,
	userFingerprint, userSigArmor string,
	countersign func(payload []byte, ts time.Time) (ServerSignature, error),
	now time.Time,
) (*Ripple, error) {
	now = now.UTC().Truncate(time.Second)
	reedAuthorIdentity := identity.IdentityID(reedAuthorID)
	selfIdentity := identity.IdentityID(userID)

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
	// Decode back to bare specifically for the signed payload builder —
	// see the function doc comment above.
	serverPayload := identity.BuildRippleServerPayload(
		serverID, reedAuthorIdentity.UserID(), reedID, selfIdentity.UserID(),
		userFingerprint, threadID, replyingToVal,
		userSigArmor, now,
	)
	serverSig, err := countersign(serverPayload, now)
	if err != nil {
		return nil, fmt.Errorf("countersign ripple: %w", err)
	}

	id := hex.EncodeToString(crypto.Hash(string(serverPayload)))

	userSigID, err := signing.InsertUserSignature(ctx, tx, userFingerprint, userSigArmor)
	if err != nil {
		return nil, err
	}
	serverSigID, err := signing.InsertServerSignature(ctx, tx, serverSig.Fingerprint, serverSig.Armor, serverSig.SignedAt)
	if err != nil {
		return nil, err
	}

	expiresAt := now.Add(7 * 24 * time.Hour)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ripples (reed_author_id, reed_id, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (reed_author_id, reed_id) DO UPDATE
		SET expires_at = EXCLUDED.expires_at
	`, reedAuthorIdentity, reedID, expiresAt); err != nil {
		return nil, fmt.Errorf("upsert ripples bookkeeping: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ripple_responses (
			id, reed_author_id, reed_id, thread_id, user_id,
			content, replying_to, deleted, posted_at,
			user_fingerprint, user_signature_id, server_signature_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, FALSE, $8, $9, $10, $11)
	`, id, reedAuthorIdentity, reedID, threadID, selfIdentity, content, replyingTo, now,
		userFingerprint, userSigID, serverSigID); err != nil {
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
		UserFingerprint: userFingerprint,
		UserSignature:   UserSignature{Fingerprint: userFingerprint, Armor: userSigArmor},
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
		SELECT rr.id, rr.reed_author_id, rr.reed_id, rr.thread_id, rr.user_id,
		       rr.content, rr.replying_to, rr.deleted, rr.posted_at,
		       rr.user_fingerprint, us.signature, ss.fingerprint, ss.signature, ss.signed_at
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
	r.ServerSignature.ServerID = s.GetServerID()
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
// GetRipple and ListRipples both select in. ServerSignature.ServerID is
// not a stored column (a ripple's countersignature is always this
// server's own) — callers set it from DataService.GetServerID() after
// scanning.
//
// rr.reed_author_id and rr.user_id are both FK'd (transitively and
// directly, respectively — see PostRipple's comment); Ripple's
// ReedAuthorID/UserID wire fields hold that same value directly,
// scanned as plain strings with no decode step.
func scanRipple(row rippleRowScanner) (*Ripple, error) {
	var r Ripple
	var replyingTo sql.NullString
	err := row.Scan(
		&r.ID, &r.ReedAuthorID, &r.ReedID, &r.ThreadID, &r.UserID, &r.Content,
		&replyingTo, &r.Deleted, &r.PostedAt,
		&r.UserFingerprint, &r.UserSignature.Armor,
		&r.ServerSignature.Fingerprint, &r.ServerSignature.Armor, &r.ServerSignature.SignedAt,
	)
	if err != nil {
		return nil, err
	}
	if replyingTo.Valid {
		r.ReplyingTo = &replyingTo.String
	}
	r.UserSignature.Fingerprint = r.UserFingerprint
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

// ListRipples returns ripple responses for (reedAuthorID, reedID) as a
// flat, already-ordered slice: threads ordered by the thread's own
// creation time (MIN(posted_at) for that thread_id) oldest first,
// responses within a thread ordered posted_at ASC. Includes soft-deleted
// rows and rows from removed-account authors unfiltered — both render
// as-is one layer up.
// reedAuthorID is the URL-path-identified reed author, already in
// userID@serverID form.
func (s *DataService) ListRipples(
	ctx context.Context,
	reedAuthorID, reedID string,
	limit int,
	before string,
) (*RippleListResult, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	reedAuthorIdentity := reedAuthorID
	args := []any{reedAuthorIdentity, reedID}
	query := `
		SELECT id, reed_author_id, reed_id, thread_id, user_id, content,
		       replying_to, deleted, posted_at,
		       user_fingerprint, user_sig, server_fingerprint, server_sig, server_signed_at,
		       thread_created_at
		FROM (
			SELECT rr.id, rr.reed_author_id, rr.reed_id, rr.thread_id, rr.user_id,
			       rr.content, rr.replying_to, rr.deleted, rr.posted_at,
			       rr.user_fingerprint, us.signature AS user_sig,
			       ss.fingerprint AS server_fingerprint, ss.signature AS server_sig,
			       ss.signed_at AS server_signed_at,
			       MIN(rr.posted_at) OVER (PARTITION BY rr.thread_id) AS thread_created_at
			FROM ripple_responses rr
			JOIN user_signatures us ON us.id = rr.user_signature_id
			JOIN server_signatures ss ON ss.id = rr.server_signature_id
			WHERE rr.reed_author_id = $1 AND rr.reed_id = $2
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

	serverID := s.GetServerID()
	var items []Ripple
	var threadCreatedAts []time.Time
	for rows.Next() {
		var threadCreatedAt time.Time
		r, err := scanRipple(rippleListRow{rows, &threadCreatedAt})
		if err != nil {
			return nil, err
		}
		r.ServerSignature.ServerID = serverID
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
// posted to this reed. reedAuthorID is already in userID@serverID form.
func (s *DataService) GetRipplesExpiresAt(ctx context.Context, reedAuthorID, reedID string) (time.Time, error) {
	reedAuthorIdentity := reedAuthorID
	var expiresAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT expires_at FROM ripples WHERE reed_author_id = $1 AND reed_id = $2
	`, reedAuthorIdentity, reedID).Scan(&expiresAt)
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
// userID@serverID form already but needs an identity.IdentityID-typed
// value for the equality check against actualOwner, hence the type conversion.
func (s *DataService) SoftDeleteRipple(ctx context.Context, id, ownerUserID string) (found, owned bool, err error) {
	ownerIdentity := identity.IdentityID(ownerUserID)
	var actualOwner identity.IdentityID
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
