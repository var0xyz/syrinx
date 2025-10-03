package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"golang.org/x/crypto/bcrypt"
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

func (s *DataService) CreateUser(username, password string) (*User, error) {
	// Check if username already exists
	var exists bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)", username).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("username already exists")
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	// Insert into users table
	var user User
	err = s.db.QueryRow(`
		INSERT INTO users (username, password)
		VALUES ($1, $2) RETURNING id, username, created_at
	`, username, string(hashedPassword)).Scan(&user.ID, &user.Username, &user.CreatedAt)

	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (s *DataService) UpdateUsername(userID uuid.UUID, username string) error {
	_, err := s.db.Exec(`
		UPDATE users
		SET username = $1
		WHERE id = $2
	`, username, userID)
	if err != nil {
		return err
	}
	return nil
}

func (s *DataService) GetUserByUsername(username string) (*User, error) {
	var user User
	err := s.db.QueryRow(`
		SELECT id, username, password, created_at
		FROM users
		WHERE username = $1
	`, username).Scan(&user.ID, &user.Username, &user.Password, &user.CreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &user, nil
}

func (s *DataService) DeleteUser(userID uuid.UUID) error {
	_, err := s.db.Exec("DELETE FROM users WHERE id = $1", userID)
	if err != nil {
		return err
	}
	return nil
}

func (s *DataService) SavePasswordResetNonce(userID uuid.UUID, nonce string) (string, error) {
	// Set expiration to 1 hour from now
	expiresAt := time.Now().Add(1 * time.Hour)

	// Insert password reset record by nonce
	_, err := s.db.Exec(
		"INSERT INTO password_reset_nonces (user_id, nonce, expires_at) VALUES ($1, $2, $3) RETURNING id, user_id, nonce, expires_at",
		userID, nonce, expiresAt,
	)
	if err != nil {
		return "", err
	}

	return nonce, nil
}

func (s *DataService) GetPasswordResetNonce(nonce string, userID uuid.UUID) (*PasswordResetNonce, error) {
	var reset PasswordResetNonce

	// Get reset record by nonce
	err := s.db.QueryRow(`
		SELECT id, user_id, nonce, expires_at
		FROM password_reset_nonces
		WHERE nonce = $1 AND user_id = $2
	`, nonce, userID,
	).Scan(&reset.ID, &reset.UserID, &reset.Nonce, &reset.ExpiresAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("invalid reset nonce")
		}
		return nil, err
	}

	return &reset, nil
}

func (s *DataService) GetLatestPasswordResetNonce(username string) (*PasswordResetNonce, error) {
	var nonce PasswordResetNonce

	err := s.db.QueryRow(`
		SELECT id, user_id, nonce, created_at, expires_at
		FROM password_reset_nonces
		WHERE user_id = $1 AND expires_at > CURRENT_TIMESTAMP
		ORDER BY created_at DESC LIMIT 1
	`, username).Scan(&nonce.ID, &nonce.UserID, &nonce.Nonce, &nonce.CreatedAt, &nonce.ExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &nonce, nil
}

func (s *DataService) UpdatePassword(userID uuid.UUID, hashedPassword string) error {
	_, err := s.db.Exec("UPDATE users SET password = $1 WHERE id = $2", hashedPassword, userID)
	if err != nil {
		return err
	}

	// The moment one of the nonces is used we invalidate all the others. This
	// could potentially be exploited to fill the DB with nonce records, but since
	// the nonce-creation endpoint is throttled, this risk should be mitigated.
	_, err = s.db.Exec("DELETE FROM password_reset_nonces WHERE user_id = $1", userID)
	if err != nil {
		return err
	}

	return nil
}

func (s *DataService) DeleteAllNoncesForUser(userID uuid.UUID) error {
	_, err := s.db.Exec("DELETE FROM password_reset_nonces WHERE user_id = $1", userID)
	if err != nil {
		return err
	}
	return nil
}

func (s *DataService) CreateProfile(userID uuid.UUID) (*Profile, error) {
	_, err := s.db.Exec("INSERT INTO profiles (user_id) VALUES ($1)", userID)
	if err != nil {
		return nil, err
	}

	profile := &Profile{UserID: userID}
	profile.PublicKeys = make([]string, 0)
	return profile, nil
}

func (s *DataService) GetProfile(userID uuid.UUID) (*Profile, error) {
	var profile Profile
	var avatarURL sql.NullString
	var bio sql.NullString
	var defaultIdentityID sql.NullString

	profile.PublicKeys = make([]string, 0)

	err := s.db.QueryRow(`
		SELECT p.id, p.user_id, p.avatar_url, p.bio, p.default_identity_id, u.username
		FROM profiles p
		JOIN users u ON p.user_id = u.id
		WHERE user_id = $1
	`,
		userID,
	).Scan(&profile.ID, &profile.UserID, &avatarURL, &bio, &defaultIdentityID, &profile.Username)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if avatarURL.Valid {
		profile.AvatarURL = avatarURL.String
	}

	if bio.Valid {
		profile.Bio = bio.String
	}

	if defaultIdentityID.Valid {
		parsedDefaultIdentityID := uuid.MustParse(defaultIdentityID.String)
		profile.DefaultIdentityID = &parsedDefaultIdentityID
	}

	serverKey, err := s.GetServerPublicKey(userID)
	if err != nil {
		return nil, err
	}
	profile.ServerKey = serverKey.Fingerprint

	keys, err := s.GetPublicKeysByUserID(userID)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		profile.PublicKeys = append(profile.PublicKeys, key.Fingerprint)
	}

	return &profile, nil
}

func (s *DataService) UpdateProfile(userID uuid.UUID, avatarURL, bio string) error {
	_, err := s.db.Exec(`
		UPDATE profiles
		SET avatar_url = $1, bio = $2
		WHERE user_id = $3
	`, avatarURL, bio, userID)
	if err != nil {
		return err
	}

	return nil
}

func (s *DataService) SetDefaultIdentity(userID uuid.UUID, identityID uuid.UUID) error {
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

func (s *DataService) SaveServerKey(key *ServerKey) error {
	_, err := s.db.Exec(`
		INSERT INTO server_keys (fingerprint, user_id, identity, public_key, private_key, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, key.Fingerprint, key.UserID, key.Identity, key.PublicKey, key.PrivateKey, key.CreatedAt, key.ExpiresAt)
	if err != nil {
		return err
	}

	return nil
}

func (s *DataService) GetServerPrivateKey(userID uuid.UUID) (string, string, error) {
	var fingerprint string
	var privateKey string

	err := s.db.QueryRow(`
		SELECT fingerprint, private_key
		FROM server_keys
		WHERE user_id = $1
	`, userID).Scan(&fingerprint, &privateKey)
	if err != nil {
		return "", "", err
	}

	return fingerprint, privateKey, nil
}

func (s *DataService) GetServerPublicKey(userID uuid.UUID) (*PublicKey, error) {
	var key PublicKey
	var identity sql.NullString

	err := s.db.QueryRow(`
		SELECT fingerprint, user_id, identity, public_key, created_at, expires_at
		FROM server_keys
		WHERE user_id = $1
	`, userID).Scan(&key.Fingerprint, &key.UserID, &identity, &key.Armor, &key.CreatedAt, &key.ExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	// Handle NULL identity
	if identity.Valid {
		key.Identities = []PublicKeyIdentity{{
			ID: uuid.Nil,
			// PublicKeyFingerprint: key.Fingerprint,
			Value: identity.String,
		}}
	} else {
		key.Identities = []PublicKeyIdentity{}
	}

	return &key, nil
}

func (s *DataService) GetPublicKeysByUserID(userID uuid.UUID) ([]PublicKey, error) {
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

		identities, err := s.GetPublicKeyIdentities(key.Fingerprint)
		if err != nil {
			return nil, err
		}
		key.Identities = identities

		keys = append(keys, key)
	}

	return keys, nil
}

func (s *DataService) GetPublicKey(id string, userID uuid.UUID) (*PublicKey, error) {
	var key PublicKey

	err := s.db.QueryRow(`
		SELECT fingerprint, user_id, armor, created_at, expires_at
		FROM public_keys
		WHERE user_id = $1 AND fingerprint = $2
	`, userID, id).Scan(&key.Fingerprint, &key.UserID, &key.Armor, &key.CreatedAt, &key.ExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	identities, err := s.GetPublicKeyIdentities(id)
	if err != nil {
		return nil, err
	}
	key.Identities = identities

	return &key, nil
}

func (s *DataService) GetPublicKeyByFingerprint(fingerprint string) (*PublicKey, error) {
	var key PublicKey

	err := s.db.QueryRow(`
		SELECT fingerprint, user_id, armor, created_at, expires_at
		FROM public_keys
		WHERE fingerprint = $1
	`, fingerprint).Scan(&key.Fingerprint, &key.UserID, &key.Armor, &key.CreatedAt, &key.ExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	identities, err := s.GetPublicKeyIdentities(fingerprint)
	if err != nil {
		return nil, err
	}
	key.Identities = identities

	return &key, nil
}

func (s *DataService) GetPublicKeyForIdentity(userID uuid.UUID, identityID uuid.UUID) (*PublicKey, error) {
	var publicKey PublicKey

	err := s.db.QueryRow(`
		SELECT pk.fingerprint, pk.user_id, pk.armor, pk.created_at, pk.expires_at
		FROM public_keys pk
		INNER JOIN public_key_identities pki ON pk.fingerprint = pki.public_key_fingerprint
		WHERE pki.id = $1 AND pk.user_id = $2
	`, identityID, userID).Scan(
		&publicKey.Fingerprint,
		&publicKey.UserID,
		&publicKey.Armor,
		&publicKey.CreatedAt,
		&publicKey.ExpiresAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	identities, err := s.GetPublicKeyIdentities(publicKey.Fingerprint)
	if err != nil {
		return nil, err
	}
	publicKey.Identities = identities

	return &publicKey, nil
}

func (s *DataService) GetPublicKeyIdentity(fingerprint string, identityID uuid.UUID) (*PublicKeyIdentity, error) {
	var identity PublicKeyIdentity
	err := s.db.QueryRow(`
		SELECT id, value
		FROM public_key_identities
		WHERE public_key_fingerprint = $1 AND id = $2
	`, fingerprint, identityID).Scan(&identity.ID, &identity.Value)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &identity, nil
}

func (s *DataService) GetPublicKeyIdentities(fingerprint string) ([]PublicKeyIdentity, error) {
	var identities []PublicKeyIdentity
	var identity PublicKeyIdentity
	rows, err := s.db.Query(`
		SELECT id, value
		FROM public_key_identities
		WHERE public_key_fingerprint = $1
	`, fingerprint,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		err := rows.Scan(&identity.ID, &identity.Value)
		if err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}

	return identities, nil
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

func (s *DataService) AddPublicKey(fingerprint string, userID uuid.UUID, createdAt time.Time, expiresAt *time.Time, armor string) (*PublicKey, error) {
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

func (s *DataService) DeletePublicKey(fingerprint string, userID uuid.UUID) error {
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
		JOIN public_key_identities
			ON public_keys.fingerprint = public_key_identities.public_key_fingerprint
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

func (s *DataService) getUserIdentities(userID uuid.UUID) ([]PublicKeyIdentity, error) {
	var identities []PublicKeyIdentity

	rows, err := s.db.Query(`
		SELECT id, value
		FROM public_key_identities
		WHERE public_key_fingerprint IN (
			SELECT fingerprint
			FROM public_keys
			WHERE user_id = $1
		)
	`, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return []PublicKeyIdentity{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var identity PublicKeyIdentity
		err := rows.Scan(&identity.ID, &identity.Value)
		if err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}

	return identities, nil
}

func (s *DataService) AddPublicKeyIdentity(fingerprint string, value string) (*PublicKeyIdentity, error) {
	var identity PublicKeyIdentity
	err := s.db.QueryRow(`
		INSERT INTO public_key_identities (public_key_fingerprint, value)
		VALUES ($1, $2)
		RETURNING id, value
	`, fingerprint, value,
	).Scan(&identity.ID, &identity.Value)
	if err != nil {
		return nil, err
	}

	return &identity, nil
}

func (s *DataService) CreateReed(userID uuid.UUID, identityID uuid.UUID) (*Reed, error) {
	var serverPublicKeyFingerprint string
	err := s.db.QueryRow(`
		SELECT fingerprint
		FROM server_keys
		WHERE user_id = $1
	`, userID).Scan(&serverPublicKeyFingerprint)
	if err != nil {
		return nil, err
	}

	var userPublicKeyFingerprint string
	err = s.db.QueryRow(`
		SELECT public_keys.fingerprint
		FROM public_keys
		JOIN public_key_identities
			ON public_keys.fingerprint = public_key_identities.public_key_fingerprint
		WHERE public_key_identities.id = $1
	`, identityID).Scan(&userPublicKeyFingerprint)
	if err != nil {
		return nil, err
	}

	var reed Reed
	err = s.db.QueryRow(`
		INSERT INTO reeds (user_id, identity_id, user_fingerprint, server_fingerprint)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, created_at, identity_id, user_fingerprint, server_fingerprint
	`, userID, identityID, userPublicKeyFingerprint, serverPublicKeyFingerprint).Scan(
		&reed.ID,
		&reed.UserID,
		&reed.CreatedAt,
		&reed.Identity,
		&reed.UserFingerprint,
		&reed.ServerFingerprint,
	)
	if err != nil {
		return nil, err
	}

	return &reed, nil
}

func (s *DataService) GetUserPublicKey(fingerprint string) (*PublicKey, error) {
	var key PublicKey
	err := s.db.QueryRow(`
		SELECT fingerprint, user_id, armor, created_at, expires_at
		FROM public_keys
		WHERE fingerprint = $1
	`, fingerprint).Scan(&key.Fingerprint, &key.UserID, &key.Armor, &key.CreatedAt, &key.ExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &key, nil
}

func (s *DataService) GetReedsByUserID(userID uuid.UUID) ([]Reed, error) {
	var reeds []Reed

	rows, err := s.db.Query(`
		SELECT id, user_id, created_at, identity_id, user_fingerprint, server_fingerprint
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
			&reed.CreatedAt,
			&reed.Identity,
			&reed.UserFingerprint,
			&reed.ServerFingerprint,
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

func (s *DataService) GetReed(userID uuid.UUID, reedID uuid.UUID) (*Reed, error) {
	var reed Reed
	err := s.db.QueryRow(`
	SELECT id, user_id, created_at, user_fingerprint, server_fingerprint
		FROM reeds
		WHERE id = $1 AND user_id = $2
	`, reedID, userID,
	).Scan(&reed.ID, &reed.UserID, &reed.CreatedAt, &reed.UserFingerprint, &reed.ServerFingerprint)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &reed, nil
}

func (s *DataService) DeleteReed(reedID uuid.UUID) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete reed keys first (foreign key constraint)
	_, err = tx.Exec(`
		DELETE FROM post_keys
		WHERE post_id = $1
	`, reedID)
	if err != nil {
		return err
	}

	// Delete the reed
	_, err = tx.Exec(`
		DELETE FROM posts
		WHERE id = $1
	`, reedID)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

// ================= //
//   CryptoService   //
// ================= //

type CryptoService struct {
}

func NewCryptoService() *CryptoService {
	return &CryptoService{}
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

func (s *CryptoService) ExtractKeyIDFromClearsignedMessage(clearsignedMessage string) (uint64, error) {
	block, _ := clearsign.Decode([]byte(clearsignedMessage))
	if block == nil {
		return 0, fmt.Errorf("invalid clearsigned message")
	}
	if block.ArmoredSignature == nil {
		return 0, fmt.Errorf("signature block missing")
	}

	// Read the signature to extract the signer's key ID
	signature, err := io.ReadAll(block.ArmoredSignature.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read signature: %w", err)
	}

	// Parse the signature packet to get the key ID
	packets := packet.NewReader(bytes.NewReader(signature))
	for {
		p, err := packets.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, fmt.Errorf("failed to parse signature packet: %w", err)
		}

		if sig, ok := p.(*packet.Signature); ok {
			return *sig.IssuerKeyId, nil
		}
	}

	return 0, fmt.Errorf("no signature found in clearsigned message")
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

func (s *CryptoService) ParseEntitiesFromArmoredKeyRing(clearsignedReed string) ([]byte, []byte, error) {
	block, _ := clearsign.Decode([]byte(clearsignedReed))
	if block == nil {
		return nil, nil, fmt.Errorf("invalid clearsigned reed")
	}
	if block.ArmoredSignature == nil {
		return nil, nil, fmt.Errorf("signature block missing")
	}

	signature, err := io.ReadAll(block.ArmoredSignature.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read signature: %w", err)
	}

	return block.Bytes, signature, nil
}

func (s *CryptoService) ReadArmoredKeyRing(key *PublicKey) (openpgp.EntityList, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(key.Armor))
	if err != nil {
		return nil, err
	}

	return entities, nil
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

func (s *CryptoService) CreateServerKey(userID uuid.UUID, entityEmail string) (*ServerKey, error) {
	// Create a new entity with a primary key pair
	entity, err := openpgp.NewEntity(userID.String(), "", entityEmail, nil)
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

	// Serialize the private key
	var privateBuf bytes.Buffer
	err = entity.SerializePrivate(&privateBuf, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize private key: %w", err)
	}

	// Armor encode the private key
	var privateArmored bytes.Buffer
	privateW, err := armor.Encode(&privateArmored, openpgp.PrivateKeyType, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create private key armor encoder: %w", err)
	}

	_, err = privateW.Write(privateBuf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to write armored private key: %w", err)
	}

	err = privateW.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close private key armor encoder: %w", err)
	}

	expiresAt := s.ExtractKeyExpirationTime(entity, time.Now())

	privateKey := &ServerKey{
		Fingerprint: hex.EncodeToString(entity.PrimaryKey.Fingerprint),
		UserID:      userID,
		PublicKey:   publicArmored.String(),
		PrivateKey:  privateArmored.String(),
		CreatedAt:   time.Now(),
		ExpiresAt:   expiresAt,
		Identity:    identity,
	}

	return privateKey, nil
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
	var buf bytes.Buffer
	messageReader := strings.NewReader(message)

	writer, err := clearsign.Encode(&buf, entity.PrivateKey, nil)
	if err != nil {
		return "", err
	}

	_, err = io.Copy(writer, messageReader)
	if err != nil {
		writer.Close()
		return "", err
	}

	err = writer.Close()
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (s *CryptoService) VerifySignature(clearsignedReed string, publicKey *PublicKey) error {
	entities, err := s.ReadArmoredKeyRing(publicKey)
	if err != nil {
		return err
	}

	signed, signature, err := s.ParseEntitiesFromArmoredKeyRing(clearsignedReed)
	if err != nil {
		return err
	}

	signedReader := bytes.NewReader(signed)
	signatureReader := bytes.NewReader(signature)
	if _, err := openpgp.CheckDetachedSignature(entities, signedReader, signatureReader, nil); err != nil {
		return err
	}

	return nil
}

func (s *CryptoService) ValidatePassword(user *User, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	return err == nil
}

func (s *CryptoService) GetDummyNonce() string {
	nonce, err := s.GenerateNonce()
	if err != nil {
		return ""
	}

	return nonce
}

func (s *CryptoService) HashPassword(password string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	return string(hashedPassword), nil
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
	Author   string
	Date     string
	Origin   string
	Replying string
	Echoing  string
}

func (s *MarkdownService) ExtractReedHeader(reed string) ReedHeader {
	lines := strings.Split(reed, "\n")
	inHeader := false
	var date,
		author,
		origin,
		replying,
		echoing string
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
		}

		if line == "---" && !inHeader {
			inHeader = true
		}
	}

	header := ReedHeader{
		Date:     strings.TrimSpace(date),
		Author:   strings.TrimSpace(author),
		Origin:   strings.TrimSpace(origin),
		Replying: strings.TrimSpace(replying),
		Echoing:  strings.TrimSpace(echoing),
	}

	return header
}

func (s *MarkdownService) ValidateReedHeader(reed string) error {
	mandatoryFoundCount := 0
	mandatoryHeaders := []string{"date", "author", "origin"}
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
