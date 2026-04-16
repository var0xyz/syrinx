package crypto

import (
	"bytes"
	gocrypto "crypto"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// Service implements the Crypto interface
type Service struct{}

// NewService creates a new crypto service
func NewService() *Service {
	return &Service{}
}

// GenerateNonce generates a random nonce
func (s *Service) GenerateNonce() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// ExtractCreationTime extracts the creation time from a public key
func (s *Service) ExtractCreationTime(publicKey interface{}) time.Time {
	if pk, ok := publicKey.(*packet.PublicKey); ok {
		return pk.CreationTime
	}
	return time.Time{}
}

// ExtractExpirationTime extracts expiration time from an identity
func (s *Service) ExtractExpirationTime(identity *openpgp.Identity, createdAt time.Time) *time.Time {
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

// ExtractKeyExpirationTime extracts key expiration time from an entity
func (s *Service) ExtractKeyExpirationTime(entity *openpgp.Entity, createdAt time.Time) *time.Time {
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

// ExtractPublicKeyArmor extracts armored public key from an entity
func (s *Service) ExtractPublicKeyArmor(entity *openpgp.Entity) (string, error) {
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

// FindEntityByIdentity finds an entity by user ID
func (s *Service) FindEntityByIdentity(entities openpgp.EntityList, uid string) *openpgp.Entity {
	for _, entity := range entities {
		for _, identity := range entity.Identities {
			if identity.Name == uid {
				return entity
			}
		}
	}

	return nil
}

// CreateKeyPair creates a new OpenPGP key pair
func (s *Service) CreateKeyPair(userID string, email string, comment string) (*KeyPair, error) {
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

// Sign creates a detached signature of the message
func (s *Service) Sign(message, privateKey string) (string, error) {
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

// VerifySignature verifies a detached signature
func (s *Service) VerifySignature(message, signature, publicKey string) error {
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

// VerifyDetachedSignature verifies a detached signature (alias for VerifySignature)
func (s *Service) VerifyDetachedSignature(message, signature, publicKey string) error {
	return s.VerifySignature(message, signature, publicKey)
}

// VerifySignedChallenge verifies a signed challenge using the provided public key
func (s *Service) VerifySignedChallenge(signature, publicKey, challenge string) error {
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

// ValidateTimestamp validates the timestamp for replay protection
// Accepts timestamps within ±5 minutes of current time
func (s *Service) ValidateTimestamp(timestampStr string) error {
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

// ValidatePublicKey validates a public key format
func (s *Service) ValidatePublicKey(publicKey string) error {
	_, err := openpgp.ReadArmoredKeyRing(strings.NewReader(publicKey))
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	return nil
}

// ExtractKeyMetadata extracts metadata from a public key
func (s *Service) ExtractKeyMetadata(publicKey string) (*CryptographicKey, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(publicKey))
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	if len(entities) == 0 {
		return nil, fmt.Errorf("no entities found in public key")
	}

	entity := entities[0]
	fingerprint := hex.EncodeToString(entity.PrimaryKey.Fingerprint)
	creationTime := s.ExtractCreationTime(entity.PrimaryKey)
	keyExpirationTime := s.ExtractKeyExpirationTime(entity, creationTime)

	// Extract the public key armor
	extractedPublicKeyArmor, err := s.ExtractPublicKeyArmor(entity)
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

func (s *Service) ExtractEntity(publicKey string) (*openpgp.Entity, error) {
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

// ValidateAndExtractPublicKey validates a public key and its signature, then extracts metadata
func (s *Service) ValidateAndExtractPublicKey(publicKey, signature string) (*CryptographicKey, error) {
	err := s.VerifySignedChallenge(signature, publicKey, publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to validate challenge signature: %w", err)
	}

	// Extract key metadata
	entity, err := s.ExtractEntity(publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to extract entity: %w", err)
	}

	fingerprint := hex.EncodeToString(entity.PrimaryKey.Fingerprint)
	creationTime := s.ExtractCreationTime(entity.PrimaryKey)
	keyExpirationTime := s.ExtractKeyExpirationTime(entity, creationTime)

	// Extract the public key armor
	extractedPublicKeyArmor, err := s.ExtractPublicKeyArmor(entity)
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

// ReadArmoredKeyRing reads an armored key ring
func (s *Service) ReadArmoredKeyRing(armoredData string) (openpgp.EntityList, error) {
	return openpgp.ReadArmoredKeyRing(strings.NewReader(armoredData))
}

// EncryptPrivateKey encrypts an unencrypted private key with a passphrase.
// SerializePrivate cannot be used here because it re-signs identities, which
// requires the private key to be decrypted. Instead, packets are written
// individually using the existing (valid) self-signatures.
func (s *Service) EncryptPrivateKey(privateKeyArmor, passphrase string) (string, error) {
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

// AddIdentity adds a new User ID with the given name to a decrypted private key.
// Returns the updated armor unchanged if the name is already an identity on the key.
func (s *Service) AddIdentity(decryptedPrivateKeyArmor, name string) (string, error) {
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

// DecryptPrivateKey decrypts a passphrase-protected private key
func (s *Service) DecryptPrivateKey(encryptedKey, passphrase string) (string, error) {
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
		return "", fmt.Errorf("failed to write armored private key: %w", err)
	}

	err = armorWriter.Close()
	if err != nil {
		return "", fmt.Errorf("failed to close armor encoder: %w", err)
	}

	return armoredBuf.String(), nil
}
