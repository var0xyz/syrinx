package main

import (
	"bytes"
	gocrypto "crypto"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/google/uuid"
)

// idAlphabet is the character set for entity IDs.
const idAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// idLength is the length of server, user, and invite IDs.
const idLength = 8

// newCryptoID returns a cryptographically random ID of idLength characters from idAlphabet.
func newCryptoID() (string, error) {
	result := make([]byte, idLength)
	alphabetLen := big.NewInt(int64(len(idAlphabet)))
	for i := range result {
		idx, err := cryptorand.Int(cryptorand.Reader, alphabetLen)
		if err != nil {
			return "", err
		}
		result[i] = idAlphabet[idx.Int64()]
	}
	return string(result), nil
}

// isValidCryptoID reports whether id has the correct length and alphabet.
func isValidCryptoID(id string) bool {
	if len(id) != idLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// isValidUUIDv7 reports whether id is a UUID version 7 (time-ordered reed id).
func isValidUUIDv7(id string) bool {
	u, err := uuid.Parse(id)
	if err != nil {
		return false
	}
	return u.Version() == 7
}

// cryptographicKey represents a cryptographic key with metadata
type cryptographicKey struct {
	Fingerprint string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	Armor       string
}

// cryptoKeyPair represents a public/private key pair
type cryptoKeyPair struct {
	Fingerprint string
	UserID      string
	PublicKey   string
	PrivateKey  string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	Identity    string
}

// cryptoHashSize is the SHA-256 digest length in bytes.
const cryptoHashSize = sha256.Size

// cryptoHash returns the SHA-256 digest of data.
func cryptoHash(data string) []byte {
	sum := sha256.Sum256([]byte(data))
	return sum[:]
}

// cryptoService implements OpenPGP key/signature/encryption operations.
type cryptoService struct{}

// newCryptoService creates a new crypto service
func newCryptoService() *cryptoService {
	return &cryptoService{}
}

// extractCreationTime extracts the creation time from a public key
func (s *cryptoService) extractCreationTime(publicKey interface{}) time.Time {
	if pk, ok := publicKey.(*packet.PublicKey); ok {
		return pk.CreationTime
	}
	return time.Time{}
}

// extractKeyExpirationTime extracts key expiration time from an entity
func (s *cryptoService) extractKeyExpirationTime(entity *openpgp.Entity, createdAt time.Time) *time.Time {
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

// extractPublicKeyArmor extracts armored public key from an entity
func (s *cryptoService) extractPublicKeyArmor(entity *openpgp.Entity) (string, error) {
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

// createKeyPair creates a new OpenPGP key pair
func (s *cryptoService) createKeyPair(userID string, email string, comment string) (*cryptoKeyPair, error) {
	// Create a new entity with a primary key pair
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

	expiresAt := s.extractKeyExpirationTime(entity, time.Now())

	keyPair := &cryptoKeyPair{
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

// sign creates a detached signature of the message
func (s *cryptoService) sign(message, privateKey string) (string, error) {
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

	return armoredBuf.String(), nil
}

// encrypt encrypts plaintext to an OpenPGP public key (armored PGP message).
func (s *cryptoService) encrypt(plaintext []byte, publicKeyArmor string) (string, error) {
	entity, err := s.extractEntity(publicKeyArmor)
	if err != nil {
		return "", err
	}

	var out bytes.Buffer
	armorWriter, err := armor.Encode(&out, "PGP MESSAGE", nil)
	if err != nil {
		return "", fmt.Errorf("armor encode: %w", err)
	}
	encrypted, err := openpgp.Encrypt(armorWriter, openpgp.EntityList{entity}, nil, nil, nil)
	if err != nil {
		_ = armorWriter.Close()
		return "", fmt.Errorf("encrypt: %w", err)
	}
	if _, err := encrypted.Write(plaintext); err != nil {
		_ = encrypted.Close()
		_ = armorWriter.Close()
		return "", fmt.Errorf("encrypt write: %w", err)
	}
	if err := encrypted.Close(); err != nil {
		_ = armorWriter.Close()
		return "", fmt.Errorf("encrypt close: %w", err)
	}
	if err := armorWriter.Close(); err != nil {
		return "", fmt.Errorf("armor close: %w", err)
	}
	return out.String(), nil
}

// decrypt decrypts an armored PGP message with the matching private key.
func (s *cryptoService) decrypt(ciphertextArmor, privateKeyArmor string) ([]byte, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(privateKeyArmor))
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	if len(entities) == 0 {
		return nil, fmt.Errorf("no private key")
	}

	block, err := armor.Decode(strings.NewReader(ciphertextArmor))
	if err != nil {
		return nil, fmt.Errorf("decode armor: %w", err)
	}

	md, err := openpgp.ReadMessage(block.Body, openpgp.EntityList{entities[0]}, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}

	plaintext, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return nil, fmt.Errorf("read plaintext: %w", err)
	}
	return plaintext, nil
}

// extractFingerprintFromArmor returns the primary key fingerprint (hex) from armor.
func (s *cryptoService) extractFingerprintFromArmor(publicKeyArmor string) (string, error) {
	entity, err := s.extractEntity(publicKeyArmor)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(entity.PrimaryKey.Fingerprint), nil
}

// encryptSymmetric returns an ASCII-armored OpenPGP message encrypting
// plaintext with password (gpg -c style). The password is never stored.
func (s *cryptoService) encryptSymmetric(plaintext []byte, password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("bundle password must not be empty")
	}

	var out bytes.Buffer
	armorWriter, err := armor.Encode(&out, "PGP MESSAGE", nil)
	if err != nil {
		return "", fmt.Errorf("armor encode: %w", err)
	}

	// Non-nil config required: SymmetricallyEncrypt dereferences AEAD() when
	// deciding cipher suite; a nil *Config panics.
	config := &packet.Config{}
	plaintextWriter, err := openpgp.SymmetricallyEncrypt(armorWriter, []byte(password), nil, config)
	if err != nil {
		return "", fmt.Errorf("symmetric encrypt: %w", err)
	}
	if _, err := plaintextWriter.Write(plaintext); err != nil {
		_ = plaintextWriter.Close()
		return "", fmt.Errorf("write plaintext: %w", err)
	}
	if err := plaintextWriter.Close(); err != nil {
		return "", fmt.Errorf("close plaintext: %w", err)
	}
	if err := armorWriter.Close(); err != nil {
		return "", fmt.Errorf("close armor: %w", err)
	}
	return out.String(), nil
}

// decryptSymmetric decrypts an ASCII-armored OpenPGP message produced by
// encryptSymmetric. Wrong passwords fail closed without returning plaintext.
func (s *cryptoService) decryptSymmetric(armoredCiphertext, password string) ([]byte, error) {
	if password == "" {
		return nil, fmt.Errorf("bundle password must not be empty")
	}

	block, err := armor.Decode(bytes.NewReader([]byte(armoredCiphertext)))
	if err != nil {
		return nil, fmt.Errorf("armor decode: %w", err)
	}

	tried := false
	md, err := openpgp.ReadMessage(block.Body, nil, func(_ []openpgp.Key, _ bool) ([]byte, error) {
		if tried {
			return nil, fmt.Errorf("wrong password")
		}
		tried = true
		return []byte(password), nil
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed")
	}

	plain, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed")
	}
	return plain, nil
}

// verifySignature verifies a detached signature
func (s *cryptoService) verifySignature(message, signature, publicKey string) error {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(publicKey))
	if err != nil {
		return err
	}

	// Verify the detached signature against the message content
	_, err = openpgp.CheckArmoredDetachedSignature(entities, strings.NewReader(message), strings.NewReader(signature), nil)
	if err != nil {
		return err
	}

	return nil
}

// verifyDetachedSignature verifies a detached signature (alias for verifySignature)
func (s *cryptoService) verifyDetachedSignature(message, signature, publicKey string) error {
	return s.verifySignature(message, signature, publicKey)
}

// verifySignedChallenge verifies a signed challenge using the provided public key
func (s *cryptoService) verifySignedChallenge(signature, publicKey, challenge string) error {
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

// validateTimestamp validates the timestamp for replay protection
// Accepts timestamps within ±5 minutes of current time
func (s *cryptoService) validateTimestamp(timestampStr string) error {
	// Parse timestamp
	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp format: %w", err)
	}

	// Get current time
	now := time.Now().Unix()

	// Check if timestamp is within acceptable window (±5 minutes = 300 seconds)
	const timeWindow = 300
	timeDiff := now - timestamp

	if timeDiff > timeWindow || timeDiff < -timeWindow {
		return fmt.Errorf("timestamp too old or too far in future: diff=%d seconds, window=%d", timeDiff, timeWindow)
	}

	return nil
}

func (s *cryptoService) extractEntity(publicKey string) (*openpgp.Entity, error) {
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

	return entities[0], nil
}

// validateAndExtractPublicKey validates a public key and its signature, then extracts metadata
func (s *cryptoService) validateAndExtractPublicKey(publicKey, signature string) (*cryptographicKey, error) {
	err := s.verifySignedChallenge(signature, publicKey, publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to validate challenge signature: %w", err)
	}

	// Extract key metadata
	entity, err := s.extractEntity(publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to extract entity: %w", err)
	}

	fingerprint := hex.EncodeToString(entity.PrimaryKey.Fingerprint)
	creationTime := s.extractCreationTime(entity.PrimaryKey)
	keyExpirationTime := s.extractKeyExpirationTime(entity, creationTime)

	// Extract the public key armor
	extractedPublicKeyArmor, err := s.extractPublicKeyArmor(entity)
	if err != nil {
		return nil, fmt.Errorf("error extracting public key armor: %w", err)
	}

	return &cryptographicKey{
		Fingerprint: fingerprint,
		CreatedAt:   creationTime,
		ExpiresAt:   keyExpirationTime,
		Armor:       extractedPublicKeyArmor,
	}, nil
}

// encryptPrivateKey encrypts an unencrypted private key with a passphrase.
// SerializePrivate cannot be used here because it re-signs identities, which
// requires the private key to be decrypted. Instead, packets are written
// individually using the existing (valid) self-signatures.
func (s *cryptoService) encryptPrivateKey(privateKeyArmor, passphrase string) (string, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(privateKeyArmor))
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}
	if len(entities) == 0 {
		return "", fmt.Errorf("no entities found in private key")
	}

	entity := entities[0]

	if entity.PrivateKey != nil && !entity.PrivateKey.Encrypted {
		if err := entity.PrivateKey.Encrypt([]byte(passphrase)); err != nil {
			return "", fmt.Errorf("failed to encrypt private key: %w", err)
		}
	}

	for _, subkey := range entity.Subkeys {
		if subkey.PrivateKey != nil && !subkey.PrivateKey.Encrypted {
			if err := subkey.PrivateKey.Encrypt([]byte(passphrase)); err != nil {
				return "", fmt.Errorf("failed to encrypt subkey: %w", err)
			}
		}
	}

	var buf bytes.Buffer
	if err := serializeEncryptedEntity(&buf, entity); err != nil {
		return "", fmt.Errorf("failed to serialize encrypted private key: %w", err)
	}

	var armoredBuf bytes.Buffer
	armorWriter, err := armor.Encode(&armoredBuf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create armor encoder: %w", err)
	}

	if _, err := armorWriter.Write(buf.Bytes()); err != nil {
		armorWriter.Close()
		return "", fmt.Errorf("failed to write armored private key: %w", err)
	}

	if err := armorWriter.Close(); err != nil {
		return "", fmt.Errorf("failed to close armor encoder: %w", err)
	}

	return armoredBuf.String(), nil
}

// serializeEncryptedEntity writes an entity's packets without re-signing identities.
// This is necessary when the private key is already encrypted and cannot sign.
func serializeEncryptedEntity(w io.Writer, e *openpgp.Entity) error {
	if err := e.PrivateKey.Serialize(w); err != nil {
		return err
	}

	for _, ident := range e.Identities {
		if err := ident.UserId.Serialize(w); err != nil {
			return err
		}
		if err := ident.SelfSignature.Serialize(w); err != nil {
			return err
		}
		for _, sig := range ident.Signatures {
			if err := sig.Serialize(w); err != nil {
				return err
			}
		}
	}

	for _, sig := range e.Revocations {
		if err := sig.Serialize(w); err != nil {
			return err
		}
	}

	for _, subkey := range e.Subkeys {
		if err := subkey.PrivateKey.Serialize(w); err != nil {
			return err
		}
		if err := subkey.Sig.Serialize(w); err != nil {
			return err
		}
		for _, sig := range subkey.Revocations {
			if err := sig.Serialize(w); err != nil {
				return err
			}
		}
	}

	return nil
}

// addIdentity adds a new User ID with the given name to a decrypted private key.
// Returns the updated armor unchanged if the name is already an identity on the key.
func (s *cryptoService) addIdentity(decryptedPrivateKeyArmor, name string) (string, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(decryptedPrivateKeyArmor))
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}
	if len(entities) == 0 {
		return "", fmt.Errorf("no entities found in private key")
	}

	entity := entities[0]

	uid := packet.NewUserId(name, "", "")
	if uid == nil {
		return "", fmt.Errorf("failed to build user ID for %q", name)
	}

	// Already present — nothing to do
	if _, exists := entity.Identities[uid.Id]; exists {
		return decryptedPrivateKeyArmor, nil
	}

	selfSig := &packet.Signature{
		Version:      4,
		SigType:      packet.SigTypePositiveCert,
		PubKeyAlgo:   entity.PrimaryKey.PubKeyAlgo,
		Hash:         gocrypto.SHA256,
		IssuerKeyId:  &entity.PrimaryKey.KeyId,
		CreationTime: time.Now(),
	}
	if err := selfSig.SignUserId(uid.Id, entity.PrimaryKey, entity.PrivateKey, nil); err != nil {
		return "", fmt.Errorf("failed to sign new identity: %w", err)
	}

	entity.Identities[uid.Id] = &openpgp.Identity{
		Name:          uid.Id,
		UserId:        uid,
		SelfSignature: selfSig,
		// SerializePrivate re-signs SelfSignature but emits ident.Signatures.
		// Existing identities from the keyring keep SelfSignature in that
		// slice; new ones must too or the User ID packet is written with no
		// signature and is dropped on the next read.
		Signatures: []*packet.Signature{selfSig},
	}

	var buf bytes.Buffer
	if err := entity.SerializePrivate(&buf, nil); err != nil {
		return "", fmt.Errorf("failed to serialize key with new identity: %w", err)
	}

	var armoredBuf bytes.Buffer
	armorWriter, err := armor.Encode(&armoredBuf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create armor encoder: %w", err)
	}
	if _, err := armorWriter.Write(buf.Bytes()); err != nil {
		armorWriter.Close()
		return "", fmt.Errorf("failed to write armored key: %w", err)
	}
	if err := armorWriter.Close(); err != nil {
		return "", fmt.Errorf("failed to close armor encoder: %w", err)
	}

	return armoredBuf.String(), nil
}

// decryptPrivateKey decrypts a passphrase-protected private key
func (s *cryptoService) decryptPrivateKey(encryptedKey, passphrase string) (string, error) {
	// Parse the armored private key
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(encryptedKey))
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	if len(entities) == 0 {
		return "", fmt.Errorf("no entities found in private key")
	}

	entity := entities[0]

	// Decrypt the primary key with the passphrase
	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		err = entity.PrivateKey.Decrypt([]byte(passphrase))
		if err != nil {
			return "", fmt.Errorf("failed to decrypt private key: %w", err)
		}
	}

	// Decrypt all subkeys (required for signing/encryption subkeys)
	for _, subkey := range entity.Subkeys {
		if subkey.PrivateKey != nil && subkey.PrivateKey.Encrypted {
			err = subkey.PrivateKey.Decrypt([]byte(passphrase))
			if err != nil {
				return "", fmt.Errorf("failed to decrypt subkey: %w", err)
			}
		}
	}

	// Serialize the decrypted private key
	var buf bytes.Buffer
	err = entity.SerializePrivate(&buf, nil)
	if err != nil {
		return "", fmt.Errorf("failed to serialize decrypted private key: %w", err)
	}

	// Armor encode the decrypted private key
	var armoredBuf bytes.Buffer
	armorWriter, err := armor.Encode(&armoredBuf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create armor encoder: %w", err)
	}

	_, err = armorWriter.Write(buf.Bytes())
	if err != nil {
		armorWriter.Close()
		return "", fmt.Errorf("failed to close armor encoder: %w", err)
	}

	err = armorWriter.Close()
	if err != nil {
		return "", fmt.Errorf("failed to close armor encoder: %w", err)
	}

	return armoredBuf.String(), nil
}
