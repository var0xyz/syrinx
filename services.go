//go:build !ops

package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"syrinx/crypto"
	"syrinx/deletion"
	"syrinx/identity"
	"syrinx/recovery"
	"syrinx/signing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/uuid25/go-uuid25"
)

// Sentinel errors returned by DataService.AddPublicKey. The handler maps
// these to 4xx responses; anything else is treated as a 500.
var (
	ErrUserNotFound               = errors.New("user not found")
	ErrKeyAlreadyExists           = errors.New("public key fingerprint already registered")
	ErrPredecessorRequired        = errors.New("predecessor fingerprint is required")
	ErrPredecessorNotFound        = errors.New("predecessor key not found for this user")
	ErrPredecessorNotRevoked      = errors.New("predecessor key is not revoked")
	ErrPredecessorAlreadyReplaced = errors.New("predecessor key already has a successor")
	ErrActiveKeyExists            = errors.New("user already has an active key")
)

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
}

func NewDataService(db *sql.DB, serverName string) *DataService {
	return &DataService{
		db:         db,
		serverName: serverName,
	}
}

func (s *DataService) GetServerID() string {
	return s.serverID
}

// UserServerSignedAt returns the identity countersignature time for userID.
// Returns sql.ErrNoRows when the user does not exist.
func (s *DataService) UserServerSignedAt(userID string) (time.Time, error) {
	var ts time.Time
	err := s.db.QueryRow(`
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
func (s *DataService) IsUnclaimed(userID string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM unclaimed_accounts WHERE user_id = $1)
	`, userID).Scan(&exists)
	return exists, err
}

// IsOngoing reports whether userID is mid-recovery import.
func (s *DataService) IsOngoing(userID string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM ongoing_recoveries WHERE user_id = $1)
	`, userID).Scan(&exists)
	return exists, err
}

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func generateID(n int) (string, error) {
	result := make([]byte, n)
	alphabetLen := big.NewInt(int64(len(alphabet)))
	for i := range result {
		idx, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", err
		}
		result[i] = alphabet[idx.Int64()]
	}
	return string(result), nil
}

func generateServerID() (string, error) {
	return generateID(8)
}

func generateUserID() (string, error) {
	return generateID(12)
}

func (s *DataService) InitServer(recoveryMode bool) error {
	var id, name string

	err := s.db.QueryRow(`SELECT id, name FROM servers WHERE self = TRUE`).Scan(&id, &name)
	if err == sql.ErrNoRows {
		if recoveryMode {
			return recovery.ErrNoIdentityFound
		}
		id, err = generateServerID()
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`INSERT INTO servers (id, name, self) VALUES ($1, $2, TRUE)`, id, s.serverName)
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
		_, err = s.db.Exec(`UPDATE servers SET name = $1 WHERE self = TRUE`, s.serverName)
		return err
	}

	return nil
}

// ProcessRevocations scans the {cwd}/revocations directory for .rvk files.
// Each file revokes the named key. InitServerKey will create a new one if needed.
// Called at startup before InitServerKey.
func (s *DataService) ProcessRevocations() error {
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
		err = s.db.QueryRow(
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

		if err := s.RevokeServerPrivateKey(fingerprint, reason); err != nil {
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
func (s *DataService) InitServerKey(cryptoSvc *crypto.Service, passphrase string) (*Key, error) {
	var fingerprint string
	var encryptedArmor string
	var createdAt time.Time

	err := s.db.QueryRow(`
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

		if err := s.SaveServerKeyPair(keyPair.Fingerprint, encryptedPrivate, keyPair.PublicKey); err != nil {
			return nil, fmt.Errorf("failed to save server key pair: %w", err)
		}

		if err := s.SetServerSigningKey(keyPair.Fingerprint); err != nil {
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
		if _, err = s.db.Exec(
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

func (s *DataService) SaveServerKeyPair(fingerprint, privateArmor, publicArmor string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO private_keys (fingerprint, armor) VALUES ($1, $2)`,
		fingerprint, privateArmor,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(
		`INSERT INTO public_keys (fingerprint, armor) VALUES ($1, $2)`,
		fingerprint, publicArmor,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *DataService) SetServerSigningKey(fingerprint string) error {
	_, err := s.db.Exec(`UPDATE servers SET signing_key = $1 WHERE self = TRUE`, fingerprint)
	return err
}

func (s *DataService) GetServerSigningKeyArmor() (string, error) {
	var armor string
	err := s.db.QueryRow(`
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
func (s *DataService) GetServerPublicKeyByFingerprint(fingerprint string) (string, error) {
	var armor string
	err := s.db.QueryRow(
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

func (s *DataService) RevokeServerPrivateKey(fingerprint, reason string) error {
	_, err := s.db.Exec(`
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
	ProfileSignature   Signature
	PublicKeySignature Signature
}

// Signup materialises a fresh identity record: it writes the users row
// (with both signatures + the server key fingerprint stored alongside
// them) and inserts the initial user_keys row — all in one transaction.
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
func (s *DataService) Signup(in SignupInput) (*User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	userSignatureID, err := signing.InsertUserSignature(
		tx, in.Fingerprint, in.UserSignatureB64, identity.UserIdentitySignedFields,
	)
	if err != nil {
		return nil, err
	}
	serverSignatureID, err := signing.InsertServerSignature(
		tx,
		in.ProfileSignature.Fingerprint,
		in.ProfileSignature.Armor,
		in.ProfileSignature.SignedAt,
		identity.ProfileSignedFields,
	)
	if err != nil {
		return nil, err
	}

	// created_at is set explicitly to memberSince — the value that was
	// signed by the server. Using the DB's DEFAULT would create a
	// race between what was signed and what is persisted, and would
	// silently truncate to whatever precision Postgres chooses.
	if _, err := tx.Exec(`
		INSERT INTO users (
			id, username, created_at, user_fingerprint,
			user_signature_id, server_signature_id
		) VALUES ($1, $2, $3, $4, $5, $6)
	`,
		in.UserID, in.Username, in.MemberSince, in.Fingerprint,
		userSignatureID, serverSignatureID,
	); err != nil {
		return nil, err
	}

	keyServerSigID, err := signing.InsertServerSignature(
		tx,
		in.PublicKeySignature.Fingerprint,
		in.PublicKeySignature.Armor,
		in.PublicKeySignature.SignedAt,
		identity.PublicKeySignedFields,
	)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`
		INSERT INTO user_keys (
			fingerprint, owner, armor, created_at, expires_at,
			server_signature_id
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, in.Fingerprint, in.UserID, in.PublicKeyArmor, in.KeyCreatedAt, in.KeyExpiresAt,
		keyServerSigID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return s.GetUser(in.UserID)
}

func (s *DataService) GetUser(userID string) (*User, error) {
	var user User
	var avatarURL, bio, activeFP sql.NullString
	var userSignatureID, serverSignatureID int64

	err := s.db.QueryRow(`
		SELECT u.id, u.username, u.avatar_url, u.bio, u.created_at,
		       u.user_fingerprint,
		       u.user_signature_id, u.server_signature_id,
		       EXISTS (
		           SELECT 1 FROM reeds r
		           WHERE r.user_id = u.id
		             AND NOT EXISTS (
		                 SELECT 1 FROM reed_removals rr WHERE rr.reed_id = r.id
		             )
		       ) AS has_reeds
		FROM users u
		WHERE u.id = $1
	`, userID).Scan(
		&user.ID,
		&user.Username,
		&avatarURL,
		&bio,
		&user.CreatedAt,
		&activeFP,
		&userSignatureID,
		&serverSignatureID,
		&user.HasReeds,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if avatarURL.Valid {
		user.AvatarURL = avatarURL.String
	}
	if bio.Valid {
		user.Bio = bio.String
	}
	if activeFP.Valid {
		user.ActiveKeyFingerprint = activeFP.String
	}

	userRow, err := signing.GetUserSignature(s.db, userSignatureID)
	if err != nil {
		return nil, err
	}
	uw := signing.UserWire(userRow)
	user.UserSignatureB64 = uw.Signature
	user.SignatureFingerprint = uw.SignatureFingerprint
	user.SignedFields = uw.SignedFields

	serverRow, err := signing.GetServerSignature(s.db, serverSignatureID)
	if err != nil {
		return nil, err
	}
	sw := signing.ServerWire(serverRow, s.serverID)
	user.Server = Signature{
		ServerID:     sw.ID,
		Fingerprint:  sw.Fingerprint,
		Algorithm:    sw.Algorithm,
		Armor:        sw.Signature,
		SignedAt:     sw.Timestamp,
		SignedFields: sw.SignedFields,
	}

	return &user, nil
}

// UpdateUserInput carries everything needed to persist a fresh signed
// identity record produced by a profile edit. Every field is populated
// on every accepted update — this is a full replacement of the signed
// user-authored fields plus new attestation rows.
type UpdateUserInput struct {
	UserID           string
	Username         string
	AvatarURL        string
	Bio              string
	Fingerprint      string
	UserSignatureB64 string
	ProfileSignature Signature
}

// UpdateUser writes a fresh signed identity record for an existing user.
// It updates username/avatar_url/bio alongside new signature rows and
// FKs in one transaction so a mid-write crash can never split the
// signature from the fields it covers.
//
// The caller owns signature verification and countersigning — this
// function just persists.
func (s *DataService) UpdateUser(in UpdateUserInput) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	userSignatureID, err := signing.InsertUserSignature(
		tx, in.Fingerprint, in.UserSignatureB64, identity.UserIdentitySignedFields,
	)
	if err != nil {
		return err
	}
	serverSignatureID, err := signing.InsertServerSignature(
		tx,
		in.ProfileSignature.Fingerprint,
		in.ProfileSignature.Armor,
		in.ProfileSignature.SignedAt,
		identity.ProfileSignedFields,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		UPDATE users
		SET username = $1,
		    avatar_url = $2,
		    bio = $3,
		    user_signature_id = $4,
		    server_signature_id = $5
		WHERE id = $6
	`,
		in.Username, in.AvatarURL, in.Bio,
		userSignatureID, serverSignatureID,
		in.UserID,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *DataService) UsernameExists(username string) (bool, error) {
	var exists bool

	err := s.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM users WHERE LOWER(username) = LOWER($1)
		)
	`, username).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

func (s *DataService) DeleteUser(userID string) error {
	// Start transaction
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM users WHERE id = $1", userID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *DataService) FollowUser(followerID, userID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO user_following (user_id, following_user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, followerID, userID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO user_followers (user_id, follower_user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, userID, followerID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *DataService) UnfollowUser(followerID, userID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		DELETE FROM user_following
		WHERE user_id = $1 AND following_user_id = $2
	`, followerID, userID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		DELETE FROM user_followers
		WHERE user_id = $1 AND follower_user_id = $2
	`, userID, followerID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *DataService) SetDefaultIdentity(userID string, identityID uuid.UUID) error {
	_, err := s.db.Exec(`
		UPDATE profiles
		SET default_identity_id = $1
		WHERE user_id = $2
	`, identityID, userID)
	if err != nil {
		return err
	}

	return nil
}

func (s *DataService) GetPublicKey(userID string, fingerprint string) (*Key, error) {
	var key Key
	var revoked bool
	var serverSignatureID int64
	var predSig, predFP sql.NullString

	err := s.db.QueryRow(`
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
	serverRow, err := signing.GetServerSignature(s.db, serverSignatureID)
	if err != nil {
		return nil, err
	}
	sw := signing.ServerWire(serverRow, s.serverID)
	key.Server = Signature{
		ServerID:     sw.ID,
		Fingerprint:  sw.Fingerprint,
		Algorithm:    sw.Algorithm,
		Armor:        sw.Signature,
		SignedAt:     sw.Timestamp,
		SignedFields: sw.SignedFields,
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

func (s *DataService) IsPublicKeyRevoked(key *Key) (bool, error) {
	return key.Revoked, nil
}

func (s *DataService) GetKeyRevocation(userID, fingerprint string) (*KeyRevocation, error) {
	var rev KeyRevocation
	var successor sql.NullString
	var userSignature, serverSignature, serverFP sql.NullString
	var serverSignedAt sql.NullTime
	var reason sql.NullString

	err := s.db.QueryRow(`
		SELECT rv.user_fingerprint, rv.owner, rv.reason, rv.successor,
		       rv.user_signature, rv.server_signature, rv.server_fingerprint,
		       rv.server_signed_at
		FROM user_key_revocations rv
		WHERE rv.owner = $1 AND rv.user_fingerprint = $2
	`, userID, fingerprint).Scan(
		&rev.Fingerprint, &rev.UserID, &reason, &successor,
		&userSignature, &serverSignature, &serverFP, &serverSignedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !userSignature.Valid || !serverSignature.Valid || !serverFP.Valid || !serverSignedAt.Valid {
		return nil, fmt.Errorf("revocation for key %s user %s is missing signatures", fingerprint, userID)
	}
	rev.Reason = reason.String
	rev.Signature = userSignature.String
	if successor.Valid && successor.String != "" {
		s := successor.String
		rev.Successor = &s
	}
	rev.Server = Signature{
		ServerID:    s.serverID,
		Fingerprint: serverFP.String,
		Algorithm:   identity.Algorithm,
		Armor:       serverSignature.String,
		SignedAt:    serverSignedAt.Time,
	}
	return &rev, nil
}

func (s *DataService) PublicKeyExists(fingerprint string, userID string) (bool, error) {
	var exists bool

	err := s.db.QueryRow(`
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
	Server      Signature

	PredecessorFingerprint string
	PredecessorSignature   string
}

// On success it inserts the key, points users.user_fingerprint at it, and
// writes the successor pointer on the predecessor's revocation row.
func (s *DataService) AddPublicKey(in AddPublicKeyInput) (*Key, error) {
	if in.PredecessorFingerprint == "" {
		return nil, ErrPredecessorRequired
	}
	fingerprint := in.Fingerprint
	userID := in.UserID
	createdAt := in.CreatedAt
	expiresAt := in.ExpiresAt
	armor := in.Armor
	predecessor := in.PredecessorFingerprint

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Lock the user row so concurrent rotations for the same owner
	// serialize. Also confirms the owner exists.
	err = tx.QueryRow(`
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
	err = tx.QueryRow(`
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
	err = tx.QueryRow(`
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
	err = tx.QueryRow(`
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
	err = tx.QueryRow(`
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

	serverSignatureID, err := signing.InsertServerSignature(
		tx,
		in.Server.Fingerprint,
		in.Server.Armor,
		in.Server.SignedAt,
		identity.PublicKeySignedFields,
	)
	if err != nil {
		return nil, err
	}

	var key Key
	err = tx.QueryRow(`
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
	key.Server = in.Server
	if key.Server.SignedFields == nil {
		key.Server.SignedFields = append([]string(nil), identity.PublicKeySignedFields...)
	}
	if key.Server.Algorithm == "" {
		key.Server.Algorithm = identity.Algorithm
	}
	if key.Server.ServerID == "" {
		key.Server.ServerID = s.serverID
	}
	if in.PredecessorSignature != "" {
		key.Predecessor = &KeyPredecessor{
			Fingerprint: predecessor,
			Signature:   in.PredecessorSignature,
		}
	}

	_, err = tx.Exec(`UPDATE users SET user_fingerprint = $1 WHERE id = $2`, fingerprint, userID)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(`
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
	Server           Signature
}

func (s *DataService) RevokeKey(in RevokeKeyInput) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// A key is revoked iff a row exists in user_key_revocations.
	_, err = tx.Exec(`
		INSERT INTO user_key_revocations (
			user_fingerprint, owner, reason,
			user_signature, server_signature, server_fingerprint, server_signed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, in.Fingerprint, in.UserID, in.Reason,
		in.UserSignatureB64, in.Server.Armor, in.Server.Fingerprint, in.Server.SignedAt)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// validateUUID25 validates that the provided string is a valid UUID25 encoding of a UUID v7
func validateUUID25(uuid25Str string) error {
	// Check if it's a valid UUID25 format (25 characters)
	if len(uuid25Str) != 25 {
		return fmt.Errorf("UUID25 must be exactly 25 characters, got %d", len(uuid25Str))
	}

	// Parse UUID25 to get the original UUID
	u25, err := uuid25.Parse(uuid25Str)
	if err != nil {
		return fmt.Errorf("invalid UUID25 format: %w", err)
	}

	// Convert to standard UUID (hyphenated format)
	decodedUUID := u25.ToHyphenated()

	// Parse the decoded UUID
	parsedUUID, err := uuid.Parse(decodedUUID)
	if err != nil {
		return fmt.Errorf("invalid UUID format after decoding: %w", err)
	}

	// Check if it's version 7
	if parsedUUID.Version() != 7 {
		return fmt.Errorf("UUID must be version 7, got version %d", parsedUUID.Version())
	}

	return nil
}

func (s *DataService) CreateReed(reedID string, userID string, fingerprint string, timestamp time.Time) (*Reed, error) {
	// Validate that the reed ID is a valid UUID25 encoding of a UUID v7
	err := validateUUID25(reedID)
	if err != nil {
		return nil, fmt.Errorf("invalid reed ID: %w", err)
	}

	var reed Reed
	err = s.db.QueryRow(`
		INSERT INTO reeds (id, user_id, private_key_fingerprint, signed_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, private_key_fingerprint, signed_at
	`, reedID, userID, fingerprint, timestamp).Scan(
		&reed.ID,
		&reed.UserID,
		&reed.Fingerprint,
		&reed.Timestamp,
	)
	if err != nil {
		return nil, err
	}

	s.db.Exec(`
		INSERT INTO reed_allocations (reed_id, user_id)
		VALUES ($1, $2)
	`, reedID, userID)

	return &reed, nil
}

func (s *DataService) GetReed(userID string, reedID string) (*Reed, error) {
	var reed Reed
	err := s.db.QueryRow(`
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

func (s *DataService) DeleteReed(reedID string) error {
	_, err := s.db.Exec(`
		DELETE FROM reeds
		WHERE id = $1
	`, reedID)
	if err != nil {
		return err
	}

	return nil
}

// GetReedRemoval returns the stored reed-removal cert for (userID, reedID).
func (s *DataService) GetReedRemoval(userID, reedID string) (*deletion.Cert, error) {
	return deletion.GetCert(s.db, userID, reedID)
}

// InsertReedRemoval persists a reed-removal cert (idempotent / conflict).
func (s *DataService) InsertReedRemoval(cert deletion.Cert) error {
	return deletion.InsertCert(s.db, cert)
}

// GetAccountRemoval returns the stored account-removal cert for userID.
func (s *DataService) GetAccountRemoval(userID string) (*deletion.AccountCert, error) {
	return deletion.GetAccountCert(s.db, userID)
}

// InsertAccountRemoval persists an account-removal cert (idempotent / conflict).
func (s *DataService) InsertAccountRemoval(cert deletion.AccountCert) error {
	return deletion.InsertAccountCert(s.db, cert)
}

// HasAccountRemoval reports whether userID has an account-removal row.
func (s *DataService) HasAccountRemoval(userID string) (bool, error) {
	return deletion.HasAccountRemoval(s.db, userID)
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

type MarkdownService struct {
}

func NewMarkdownService() *MarkdownService {
	return &MarkdownService{}
}

type ReedHeader struct {
	Author string
	Date   string
	Origin string

	// Social
	Replying string
	Echoing  string

	// Crypto
	UserFingerprint   string
	ServerFingerprint string
}

func (s *MarkdownService) ExtractReedHeader(reed string) ReedHeader {
	lines := strings.Split(reed, "\n")
	inHeader := false
	var date,
		author,
		origin,
		replying,
		echoing,
		userFingerprint,
		serverFingerprint string
	for _, line := range lines {
		if inHeader {
			if line == "---" {
				break
			}
			if strings.HasPrefix(line, "date:") {
				date, _ = strings.CutPrefix(line, "date:")
			}
			if strings.HasPrefix(line, "author:") {
				author, _ = strings.CutPrefix(line, "author:")
			}
			if strings.HasPrefix(line, "origin:") {
				origin, _ = strings.CutPrefix(line, "origin:")
			}
			if strings.HasPrefix(line, "replying:") {
				replying, _ = strings.CutPrefix(line, "replying:")
			}
			if strings.HasPrefix(line, "echoing:") {
				echoing, _ = strings.CutPrefix(line, "echoing:")
			}
			if strings.HasPrefix(line, "userFingerprint:") {
				userFingerprint, _ = strings.CutPrefix(line, "userFingerprint:")
			}
			if strings.HasPrefix(line, "serverFingerprint:") {
				serverFingerprint, _ = strings.CutPrefix(line, "serverFingerprint:")
			}
		}

		if line == "---" && !inHeader {
			inHeader = true
		}
	}

	header := ReedHeader{
		Date:   strings.TrimSpace(date),
		Author: strings.TrimSpace(author),
		Origin: strings.TrimSpace(origin),

		Replying: strings.TrimSpace(replying),
		Echoing:  strings.TrimSpace(echoing),

		UserFingerprint:   strings.TrimSpace(userFingerprint),
		ServerFingerprint: strings.TrimSpace(serverFingerprint),
	}

	return header
}

func (s *MarkdownService) ValidateReedHeader(reed string) error {
	mandatoryFoundCount := 0
	mandatoryHeaders := []string{
		"date",
		"author",
		"origin",
		"key",
		"algorithm",
		"signature",
	}
	optionalHeaders := []string{
		"replying",
		"echoing",
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
