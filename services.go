package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
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
	var serverKeyFingerprint sql.NullString

	var user User
	err = tx.QueryRow(`
		INSERT INTO users (id, username)
		VALUES ($1, $2) RETURNING id, username, avatar_url, bio, server_key_fingerprint, created_at
	`, id, username).Scan(
		&user.ID,
		&user.Username,
		&avatarURL,
		&bio,
		&serverKeyFingerprint,
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

	if serverKeyFingerprint.Valid {
		user.ServerKeyFingerprint = serverKeyFingerprint.String
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &user, nil
}

func (s *DataService) GetUser(userID string) (*User, error) {
	var user User
	var serverKeyFingerprint sql.NullString
	var avatarURL sql.NullString
	var bio sql.NullString

	err := s.db.QueryRow(`
		SELECT u.id, u.username, u.avatar_url, u.bio, u.server_key_fingerprint, u.created_at,
			EXISTS (SELECT 1 FROM reeds r WHERE r.user_id = u.id LIMIT 1) as has_reeds
		FROM users u
		WHERE id = $1
	`, userID).Scan(
		&user.ID,
		&user.Username,
		&avatarURL,
		&bio,
		&serverKeyFingerprint,
		&user.CreatedAt,
		&user.HasReeds,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if serverKeyFingerprint.Valid {
		user.ServerKeyFingerprint = serverKeyFingerprint.String
	}
	if avatarURL.Valid {
		user.AvatarURL = avatarURL.String
	}
	if bio.Valid {
		user.Bio = bio.String
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

func (s *DataService) UpdateDefaultServerKeyForUser(user *User, fingerprint string) error {
	_, err := s.db.Exec(`
		UPDATE users
		SET server_key_fingerprint = $1
		WHERE id = $2
	`, fingerprint, user.ID)
	if err != nil {
		return err
	}

	// Update the user struct in-place
	user.ServerKeyFingerprint = fingerprint
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
		SELECT id, username, avatar_url, bio, server_key_fingerprint, created_at
		FROM users
		WHERE LOWER(username) = LOWER($1)
	`, username).Scan(
		&user.ID,
		&user.Username,
		&avatarURL,
		&user.Bio,
		&user.ServerKeyFingerprint,
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

func (s *DataService) SaveKeyPair(key *crypto.KeyPair) (*PublicKey, error) {
	_, err := s.db.Exec(`
		INSERT INTO private_keys (fingerprint, user_id, armor, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, key.Fingerprint, key.UserID, key.PrivateKey, key.ExpiresAt, key.CreatedAt)
	if err != nil {
		return nil, err
	}

	var publicKey PublicKey

	err = s.db.QueryRow(`
		INSERT INTO public_keys (fingerprint, user_id, armor, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING fingerprint, user_id, armor, created_at, expires_at
	`, key.Fingerprint, key.UserID, key.PublicKey, key.ExpiresAt, key.CreatedAt).Scan(
		&publicKey.Fingerprint,
		&publicKey.UserID,
		&publicKey.Armor,
		&publicKey.CreatedAt,
		&publicKey.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}

	return &publicKey, nil
}

func (s *DataService) GetPrivateKey(userID string, fingerprint string) (*PrivateKey, error) {
	var privateKey PrivateKey

	err := s.db.QueryRow(`
		SELECT fingerprint, user_id, armor, created_at, expires_at
		FROM private_keys
		WHERE user_id = $1 AND fingerprint = $2
	`, userID, fingerprint).Scan(
		&privateKey.Fingerprint,
		&privateKey.UserID,
		&privateKey.Armor,
		&privateKey.CreatedAt,
		&privateKey.ExpiresAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &privateKey, nil
}

func (s *DataService) GetPublicKeysByUserID(userID string) ([]PublicKey, error) {
	var keys []PublicKey

	rows, err := s.db.Query(`
		SELECT fingerprint, user_id, armor, created_at, expires_at
		FROM public_keys
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var key PublicKey

		err := rows.Scan(&key.Fingerprint, &key.UserID, &key.Armor, &key.CreatedAt, &key.ExpiresAt)
		if err != nil {
			return nil, err
		}

		keys = append(keys, key)
	}

	return keys, nil
}

func (s *DataService) GetPublicKey(userID string, fingerprint string) (*PublicKey, error) {
	var key PublicKey

	err := s.db.QueryRow(`
		SELECT fingerprint, user_id, armor, created_at, expires_at
		FROM public_keys
		WHERE user_id = $1 AND fingerprint = $2
	`, userID, fingerprint).Scan(&key.Fingerprint, &key.UserID, &key.Armor, &key.CreatedAt, &key.ExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &key, nil
}

func (s *DataService) IsPublicKeyRevoked(key *PublicKey) (bool, error) {
	var revoked bool

	err := s.db.QueryRow(`
		SELECT revoked FROM public_keys WHERE fingerprint = $1
	`, key.Fingerprint).Scan(&revoked)
	if err != nil {
		return false, err
	}

	return revoked, nil
}

func (s *DataService) PublicKeyExists(fingerprint string) (bool, error) {
	var exists bool

	err := s.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM public_keys
			WHERE fingerprint = $1
		)
	`, fingerprint).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

func (s *DataService) AddPublicKey(fingerprint string, userID string, createdAt time.Time, expiresAt *time.Time, armor string) (*PublicKey, error) {
	var publicKey PublicKey
	err := s.db.QueryRow(`
			INSERT INTO public_keys (fingerprint, user_id, armor, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING fingerprint, user_id, armor, created_at, expires_at
		`, fingerprint, userID, armor, createdAt, expiresAt,
	).Scan(&publicKey.Fingerprint, &publicKey.UserID, &publicKey.Armor, &publicKey.CreatedAt, &publicKey.ExpiresAt)
	if err != nil {
		return nil, err
	}

	return &publicKey, nil
}

func (s *DataService) RevokeKey(fingerprint string, userID string, reason string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Mark public key as revoked
	_, err = tx.Exec(`
		UPDATE public_keys
		SET revoked = TRUE
		WHERE fingerprint = $1 AND user_id = $2
	`, fingerprint, userID)
	if err != nil {
		return err
	}

	// Mark private key as revoked
	_, err = tx.Exec(`
		UPDATE private_keys
		SET revoked = TRUE
		WHERE fingerprint = $1 AND user_id = $2
	`, fingerprint, userID)
	if err != nil {
		return err
	}

	// Insert revocation record
	_, err = tx.Exec(`
		INSERT INTO revokations (fingerprint, user_id, reason, revoked_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
		ON CONFLICT (fingerprint) DO UPDATE
		SET reason = $3
	`, fingerprint, userID, reason)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
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

func (s *DataService) CreateReed(reedID string, userID string, serverID string, fingerprint string, timestamp time.Time) (*Reed, error) {
	// Validate that the reed ID is a valid UUID25 encoding of a UUID v7
	err := validateUUID25(reedID)
	if err != nil {
		return nil, fmt.Errorf("invalid reed ID: %w", err)
	}

	var reed Reed
	err = s.db.QueryRow(`
		INSERT INTO reeds (id, user_id, server_id, fingerprint, signed_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, fingerprint, signed_at
	`, reedID, userID, serverID, fingerprint, timestamp).Scan(
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
		SELECT id, user_id, fingerprint, signed_at
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
	SELECT id, user_id, fingerprint, signed_at
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

// ================= //
//   CryptoService   //
// ================= //

// CryptoService has been moved to the crypto package
// All crypto operations are now handled by crypto.Service

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
