package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sqids/sqids-go"
)

type Services struct {
	db     *DataService
	crypto *CryptoService
	log    *LoggingService
	md     *MarkdownService
}

// =============== //
//   UserService   //
// =============== //

type DataService struct {
	db *sql.DB
}

func NewDataService(db *sql.DB) *DataService {
	return &DataService{db: db}
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

	id, err := sqidsEncoder.Encode([]uint64{count})
	if err != nil {
		return nil, err
	}

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
		SELECT id, username, avatar_url, bio, server_key_fingerprint, created_at
		FROM users
		WHERE id = $1
	`, userID).Scan(
		&user.ID,
		&user.Username,
		&avatarURL,
		&bio,
		&serverKeyFingerprint,
		&user.CreatedAt,
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

func (s *DataService) SaveKeyPair(key *KeyPair) (*PublicKey, error) {
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

func (s *DataService) DeletePublicKey(fingerprint string, userID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var defaultIdentityID uuid.UUID
	err = tx.QueryRow(`
         SELECT default_identity_id
         FROM profiles
         WHERE user_id = $1
	`, userID).Scan(&defaultIdentityID)
	if err != nil {
		return err
	}

	var defaultIdentityFingerprint string
	err = tx.QueryRow(`
	    SELECT public_keys.fingerprint
		FROM public_keys
		JOIN identities
			ON public_keys.fingerprint = identities.public_key_fingerprint
		WHERE public_key_identities.id = $1
	`, defaultIdentityID).Scan(&defaultIdentityFingerprint)
	if err != nil {
		return err
	}

	if defaultIdentityFingerprint == fingerprint {
		_, err = tx.Exec(`
			UPDATE profiles
			SET default_identity_id = NULL
			WHERE user_id = $1
		`, userID)
		if err != nil {
			return err
		}
	}

	_, err = tx.Exec(`
		DELETE FROM public_keys
		WHERE user_id = $1 AND fingerprint = $2
	`, userID, fingerprint)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}

func (s *DataService) CreateReed(reedID string, userID string, userFingerprint string, serverFingerprint string) (*Reed, error) {
	var reed Reed
	err := s.db.QueryRow(`
		INSERT INTO reeds (id, user_id, user_fingerprint, server_fingerprint)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, user_fingerprint, server_fingerprint, signed_at
	`, reedID, userID, userFingerprint, serverFingerprint).Scan(
		&reed.ID,
		&reed.UserID,
		&reed.UserFingerprint,
		&reed.ServerFingerprint,
		&reed.SignedAt,
	)
	if err != nil {
		return nil, err
	}
	return &reed, nil
}

func (s *DataService) GetReedsByUserID(userID string) ([]Reed, error) {
	var reeds []Reed

	rows, err := s.db.Query(`
		SELECT id, user_id, user_fingerprint, server_fingerprint, signed_at
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
			&reed.UserFingerprint,
			&reed.ServerFingerprint,
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
	SELECT id, user_id, user_fingerprint, server_fingerprint, signed_at
		FROM reeds
		WHERE id = $1 AND user_id = $2
	`, reedID, userID,
	).Scan(
		&reed.ID,
		&reed.UserID,
		&reed.UserFingerprint,
		&reed.ServerFingerprint,
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

type CryptoService struct {
}

func NewCryptoService(privateKeyFile string) *CryptoService {
	// Check if the required private key file exists
	if _, err := os.Stat(privateKeyFile); os.IsNotExist(err) {
		panic(fmt.Sprintf("required private key file '%s' does not exist", privateKeyFile))
	}

	return &CryptoService{}
}

type CryptographicKey struct {
	Fingerprint string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	Armor       string
}

type KeyPair struct {
	Fingerprint string
	UserID      string
	PublicKey   string
	PrivateKey  string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	Identity    string
}

type SignedReed struct {
	ID       string    `json:"id"`
	UserID   string    `json:"userID"`
	SignedAt time.Time `json:"signedAt"`
	Identity string    `json:"identity"`

	UserFingerprint   string `json:"userFingerprint"`
	ServerFingerprint string `json:"serverFingerprint"`

	Signature string `json:"signature"`
}

func (s *CryptoService) GenerateNonce() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func (s *CryptoService) ExtractMessageFromSignature(signature string) string {
	lines := strings.Split(signature, "\n")
	inBlock := false
	inMessage := false
	message := ""

	for _, line := range lines {
		if line == "-----BEGIN PGP SIGNED MESSAGE-----" {
			inBlock = true
			continue
		}
		if line == "-----BEGIN PGP SIGNATURE-----" {
			// We went too far
			break
		}
		if inBlock {
			if line == "" {
				inMessage = true
				continue
			}
			if inMessage {
				if strings.HasPrefix(line, "- ") {
					message += strings.TrimPrefix(line, "- ") + "\n"
				} else {
					message += line + "\n"
				}
			}
		}
	}

	return message
}

func (s *CryptoService) ExtractEntitiesFromMessage(message string) ([]*openpgp.Entity, error) {
	_, err := openpgp.ReadArmoredKeyRing(strings.NewReader(message))
	if err != nil {
		return nil, fmt.Errorf("error parsing message: %w", err)
	}

	lines := strings.Split(message, "\n")
	inKey := false
	var entities []*openpgp.Entity
	var key []byte

	for _, line := range lines {
		if line == "-----BEGIN PGP PUBLIC KEY BLOCK-----" {
			key = append(key, []byte(line+"\n")...)
			inKey = true
			continue
		}
		if inKey {
			key = append(key, []byte(line+"\n")...)
			if line == "-----END PGP PUBLIC KEY BLOCK-----" {
				keyRing, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(key))
				if err != nil {
					return nil, err
				}
				entities = append(entities, keyRing...)
				inKey = false
				key = nil
			}
		}
	}

	return entities, nil
}

func (s *CryptoService) ExtractCreationTime(publicKey *packet.PublicKey) time.Time {
	return publicKey.CreationTime
}

func (s *CryptoService) ExtractExpirationTime(identity *openpgp.Identity, createdAt time.Time) *time.Time {
	var keyLifetimeSecs uint32
	if identity.SelfSignature != nil {
		if identity.SelfSignature.KeyLifetimeSecs != nil {
			keyLifetimeSecs = *identity.SelfSignature.KeyLifetimeSecs
		}
	}

	var expiresAt *time.Time
	if keyLifetimeSecs > 0 {
		expirationTime := createdAt.Add(time.Duration(keyLifetimeSecs) * time.Second)
		expiresAt = &expirationTime
	}

	return expiresAt
}

func (s *CryptoService) ExtractKeyExpirationTime(entity *openpgp.Entity, createdAt time.Time) *time.Time {
	// Check if any identity has a self-signature with key lifetime
	// The key lifetime is typically set in the identity self-signatures
	for _, identity := range entity.Identities {
		if identity.SelfSignature != nil && identity.SelfSignature.KeyLifetimeSecs != nil {
			keyLifetimeSecs := *identity.SelfSignature.KeyLifetimeSecs
			if keyLifetimeSecs > 0 {
				expirationTime := createdAt.Add(time.Duration(keyLifetimeSecs) * time.Second)
				return &expirationTime
			}
		}
	}

	// If no key lifetime found, return nil (key doesn't expire)
	return nil
}

func (s *CryptoService) ExtractPublicKeyArmorFromEntity(entity *openpgp.Entity) (string, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return "", err
	}

	err = entity.Serialize(w)
	if err != nil {
		w.Close()
		return "", err
	}

	err = w.Close()
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (s *CryptoService) FindEntityByIdentity(entities openpgp.EntityList, uid string) *openpgp.Entity {
	for _, entity := range entities {
		for _, identity := range entity.Identities {
			if identity.Name == uid {
				return entity
			}
		}
	}

	return nil
}

// TODO: Encrypt each user's server key with our own public key. Our public key
// won't be stored in the database, meaning that if the DB is compromised, the
// server keys of all users will be encrypted. The attacker would also need
// access to the FS, or to read the key from memory.
func (s *CryptoService) CreateServerKey(userID string, email string) (*KeyPair, error) {
	// Create a new entity with a primary key pair
	comment := "syrinx.var0.xyz"
	entity, err := openpgp.NewEntity(userID, comment, email, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create entity: %w", err)
	}

	// Sign all the identities
	var identity string
	for _, id := range entity.Identities {
		err := id.SelfSignature.SignUserId(id.UserId.Id, entity.PrimaryKey, entity.PrivateKey, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to sign identity: %w", err)
		}
		identity = id.UserId.Id
	}

	// Serialize the public key
	var publicBuf bytes.Buffer
	err = entity.Serialize(&publicBuf)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize public key: %w", err)
	}

	// Armor encode the public key
	var publicArmored bytes.Buffer
	publicW, err := armor.Encode(&publicArmored, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create public key armor encoder: %w", err)
	}

	_, err = publicW.Write(publicBuf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to write armored public key: %w", err)
	}

	err = publicW.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close public key armor encoder: %w", err)
	}

	// Serialize the private key first
	var privateBuf bytes.Buffer
	err = entity.SerializePrivate(&privateBuf, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize private key: %w", err)
	}

	// Server keys are stored unencrypted since the server controls them
	privateKeyData := privateBuf.Bytes()

	// Armor encode the private key
	var privateArmored bytes.Buffer
	privateW, err := armor.Encode(&privateArmored, openpgp.PrivateKeyType, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create private key armor encoder: %w", err)
	}

	_, err = privateW.Write(privateKeyData)
	if err != nil {
		return nil, fmt.Errorf("failed to write armored private key: %w", err)
	}

	err = privateW.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close private key armor encoder: %w", err)
	}

	expiresAt := s.ExtractKeyExpirationTime(entity, time.Now())

	keyPair := &KeyPair{
		Fingerprint: hex.EncodeToString(entity.PrimaryKey.Fingerprint),
		UserID:      userID,
		PublicKey:   publicArmored.String(),
		PrivateKey:  privateArmored.String(),
		CreatedAt:   time.Now(),
		ExpiresAt:   expiresAt,
		Identity:    identity,
	}

	return keyPair, nil
}

func (s *CryptoService) Sign(message string, privateKey string) (string, error) {
	// Parse the armored private key
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(privateKey))
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	if len(entities) == 0 {
		return "", fmt.Errorf("no entities found in private key")
	}

	entity := entities[0]

	// Generate a detached signature
	var sigBuf bytes.Buffer
	err = openpgp.DetachSign(&sigBuf, entity, strings.NewReader(message), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create detached signature: %w", err)
	}

	// Armor encode the signature
	var armoredBuf bytes.Buffer
	armorWriter, err := armor.Encode(&armoredBuf, openpgp.SignatureType, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create armor encoder: %w", err)
	}

	_, err = armorWriter.Write(sigBuf.Bytes())
	if err != nil {
		armorWriter.Close()
		return "", fmt.Errorf("failed to write armored signature: %w", err)
	}

	err = armorWriter.Close()
	if err != nil {
		return "", fmt.Errorf("failed to close armor encoder: %w", err)
	}

	// Remove PGP signature delimiters and clean up whitespace
	armoredSignature := armoredBuf.String()

	// Remove the armor delimiters
	cleanedSignature := strings.TrimSpace(armoredSignature)
	cleanedSignature = strings.TrimPrefix(cleanedSignature, "-----BEGIN PGP SIGNATURE-----")
	cleanedSignature = strings.TrimSuffix(cleanedSignature, "-----END PGP SIGNATURE-----")
	cleanedSignature = strings.TrimSpace(cleanedSignature)

	return cleanedSignature, nil
}

func (s *CryptoService) VerifySignature(reed string, signature string, publicKey string) error {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(publicKey))
	if err != nil {
		return err
	}

	// Verify the detached signature against the reed content
	_, err = openpgp.CheckArmoredDetachedSignature(entities, strings.NewReader(reed), strings.NewReader(signature), nil)
	if err != nil {
		return err
	}

	return nil
}

// VerifySignedChallenge verifies a signed challenge using the provided public key
func (s *CryptoService) VerifySignedChallenge(signature string, publicKey string, challenge string) error {
	// Read the public key entities
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(publicKey))
	if err != nil {
		return fmt.Errorf("failed to read public key: %w", err)
	}

	if len(entities) == 0 {
		return fmt.Errorf("no entities found in public key")
	}

	// Decode ASCII armor to get binary signature
	block, err := armor.Decode(strings.NewReader(signature))
	if err != nil {
		return fmt.Errorf("failed to decode ASCII-armored signature: %w", err)
	}

	// This is a detached signature, verify it against the challenge
	_, err = openpgp.CheckDetachedSignature(entities, strings.NewReader(challenge), block.Body, nil)
	if err != nil {
		return fmt.Errorf("detached signature verification failed: %w", err)
	}

	return nil
}

func (s *CryptoService) GetDummyNonce() string {
	nonce, err := s.GenerateNonce()
	if err != nil {
		return ""
	}

	return nonce
}

// ValidateAndExtractPublicKey validates a public key and its signature, then extracts metadata
func (s *CryptoService) ValidateAndExtractPublicKey(publicKey, signature string) (*CryptographicKey, error) {
	// Parse the public key
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(publicKey))
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	if len(entities) == 0 {
		return nil, fmt.Errorf("no entities found in public key")
	}

	// Security: Only process the key that was used to sign
	if len(entities) > 1 {
		return nil, fmt.Errorf("multiple keys found in keyring. Please upload only the single key that signed the request")
	}

	// Verify the signature using the public key
	err = s.VerifySignedChallenge(signature, publicKey, publicKey)
	if err != nil {
		return nil, fmt.Errorf("invalid signature: the public key was not signed by the provided private key: %w", err)
	}

	// Extract key metadata
	entity := entities[0]
	fingerprint := hex.EncodeToString(entity.PrimaryKey.Fingerprint)
	creationTime := s.ExtractCreationTime(entity.PrimaryKey)
	keyExpirationTime := s.ExtractKeyExpirationTime(entity, creationTime)

	// Extract the public key armor
	extractedPublicKeyArmor, err := s.ExtractPublicKeyArmorFromEntity(entity)
	if err != nil {
		return nil, fmt.Errorf("error extracting public key armor: %w", err)
	}

	return &CryptographicKey{
		Fingerprint: fingerprint,
		CreatedAt:   creationTime,
		ExpiresAt:   keyExpirationTime,
		Armor:       extractedPublicKeyArmor,
	}, nil
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
