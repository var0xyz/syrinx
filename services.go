package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"syrinx/crypto"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sqids/sqids-go"
	"github.com/uuid25/go-uuid25"
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

const serverIDAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func generateServerID() (string, error) {
	result := make([]byte, 8)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(serverIDAlphabet))))
		if err != nil {
			return "", err
		}
		result[i] = serverIDAlphabet[n.Int64()]
	}
	return string(result), nil
}

func (s *DataService) InitServer() error {
	var id, name string

	err := s.db.QueryRow(`SELECT id, name FROM servers WHERE self = TRUE`).Scan(&id, &name)
	if err == sql.ErrNoRows {
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

func (s *DataService) RevokeServerPrivateKey(fingerprint, reason string) error {
	_, err := s.db.Exec(`
		UPDATE private_keys
		SET revoked_at = NOW(), revoke_reason = $2
		WHERE fingerprint = $1
	`, fingerprint, reason)
	return err
}

func (s *DataService) CreateUser(username string) (*User, error) {
	// Start transaction
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Increment and get new count
	var count uint64
	err = tx.QueryRow(`
		UPDATE user_count SET count = count + 1 WHERE id = 1 RETURNING count
	`).Scan(&count)
	if err != nil {
		return nil, err
	}

	// Generate Sqids ID
	sqidsEncoder, err := sqids.New()
	if err != nil {
		return nil, err
	}

	// Add a random number to the list
	randomNum, err := rand.Int(rand.Reader, big.NewInt(1<<32))
	if err != nil {
		return nil, err
	}

	log.Info().
		Uint64("count", count).
		Uint64("randomNum", randomNum.Uint64()).
		Msg("Generating Sqids ID")

	id, err := sqidsEncoder.Encode([]uint64{count, randomNum.Uint64()})
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to generate Sqids ID")
		return nil, err
	}
	log.Info().
		Str("id", id).
		Str("serverName", s.serverName).
		Msg("Sqids ID generated successfully")

	// Insert user with generated ID
	var avatarURL sql.NullString
	var bio sql.NullString

	var user User
	err = tx.QueryRow(`
		INSERT INTO users (id, username)
		VALUES ($1, $2) RETURNING id, username, avatar_url, bio, created_at
	`, id, username).Scan(
		&user.ID,
		&user.Username,
		&avatarURL,
		&bio,
		&user.CreatedAt,
	)

	if err != nil {
		return nil, err
	}

	if avatarURL.Valid {
		user.AvatarURL = avatarURL.String
	}

	if bio.Valid {
		user.Bio = bio.String
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &user, nil
}

func (s *DataService) GetUser(userID string) (*User, error) {
	var user User
	var avatarURL sql.NullString
	var bio sql.NullString
	var fingerprint sql.NullString

	err := s.db.QueryRow(`
		SELECT id, username, avatar_url, bio, created_at,
			EXISTS (
				SELECT 1
				FROM reeds
				WHERE user_id = id
				LIMIT 1
			) AS has_reeds,
			fingerprint
		FROM users
		WHERE id = $1
	`, userID).Scan(
		&user.ID,
		&user.Username,
		&avatarURL,
		&bio,
		&user.CreatedAt,
		&user.HasReeds,
		&fingerprint,
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
	if fingerprint.Valid {
		user.Fingerprint = fingerprint.String
	}

	return &user, nil
}

func (s *DataService) UpdateUser(user *User) error {
	_, err := s.db.Exec(`
		UPDATE users
		SET username = $1, avatar_url = $2, bio = $3
		WHERE id = $4
	`, user.Username, user.AvatarURL, user.Bio, user.ID)
	if err != nil {
		return err
	}

	return nil
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

func (s *DataService) GetUserByUsername(username string) (*User, error) {
	var user User
	var avatarURL sql.NullString

	err := s.db.QueryRow(`
		SELECT id, username, avatar_url, bio, created_at
		FROM users
		WHERE LOWER(username) = LOWER($1)
	`, username).Scan(
		&user.ID,
		&user.Username,
		&avatarURL,
		&user.Bio,
		&user.CreatedAt,
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

	return &user, nil
}

func (s *DataService) DeleteUser(userID string) error {
	_, err := s.db.Exec("DELETE FROM users WHERE id = $1", userID)
	if err != nil {
		return err
	}
	return nil
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
	var revokedAt sql.NullTime
	var revokedReason sql.NullString

	err := s.db.QueryRow(`
		SELECT uk.fingerprint, uk.armor, uk.created_at, rv.revoked_at, rv.reason
		FROM user_keys uk
		LEFT JOIN user_key_revocations rv ON rv.fingerprint = uk.fingerprint AND rv.owner = uk.owner
		WHERE uk.owner = $1 AND uk.fingerprint = $2
	`, userID, fingerprint).Scan(&key.Fingerprint, &key.Armor, &key.CreatedAt, &revokedAt, &revokedReason)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if revokedAt.Valid {
		key.Revoked = &Revoke{Timestamp: revokedAt.Time, Reason: revokedReason.String}
	}

	return &key, nil
}

func (s *DataService) IsPublicKeyRevoked(key *Key) (bool, error) {
	return key.Revoked != nil, nil
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

func (s *DataService) AddPublicKey(fingerprint string, userID string, createdAt time.Time, expiresAt *time.Time, armor string) (*Key, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var key Key
	err = tx.QueryRow(`
		INSERT INTO user_keys (fingerprint, owner, armor, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING fingerprint, armor, created_at
	`, fingerprint, userID, armor, createdAt, expiresAt,
	).Scan(&key.Fingerprint, &key.Armor, &key.CreatedAt)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(`UPDATE users SET fingerprint = $1 WHERE id = $2`, fingerprint, userID)
	if err != nil {
		return nil, err
	}

	return &key, tx.Commit()
}

func (s *DataService) RevokeKey(fingerprint string, userID string, reason string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Mark user public key as revoked
	_, err = tx.Exec(`
		UPDATE user_keys
		SET revoked = TRUE
		WHERE fingerprint = $1 AND owner = $2
	`, fingerprint, userID)
	if err != nil {
		return err
	}

	// Insert revocation record
	_, err = tx.Exec(`
		INSERT INTO user_key_revocations (fingerprint, owner, reason, revoked_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
		ON CONFLICT (fingerprint, owner) DO UPDATE
		SET reason = $3
	`, fingerprint, userID, reason)
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
		&reed.SignedAt,
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

func (s *DataService) GetReedsByUserID(userID string) ([]Reed, error) {
	var reeds []Reed

	rows, err := s.db.Query(`
		SELECT id, user_id, private_key_fingerprint, signed_at
		FROM reeds
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var reed Reed
		err := rows.Scan(
			&reed.ID,
			&reed.UserID,
			&reed.Fingerprint,
			&reed.SignedAt,
		)
		if err != nil {
			return nil, err
		}

		reeds = append(reeds, reed)
	}

	if len(reeds) == 0 {
		return []Reed{}, nil
	}

	return reeds, nil
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
		&reed.SignedAt,
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
