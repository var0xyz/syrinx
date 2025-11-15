package crypto

import (
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// Crypto defines the interface for cryptographic operations
type Crypto interface {
	// Key Operations
	CreateKeyPair(userID, email, comment string) (*KeyPair, error)
	ValidatePublicKey(publicKey string) error
	ExtractKeyMetadata(publicKey string) (*CryptographicKey, error)
	ValidateAndExtractPublicKey(publicKey, signature string) (*CryptographicKey, error)

	// Signature Operations
	Sign(message, privateKey string) (string, error)
	VerifySignature(message, signature, publicKey string) error
	VerifyDetachedSignature(message, signature, publicKey string) error
	VerifySignedChallenge(signature, publicKey, challenge string) error

	// Utility Operations
	ValidateTimestamp(timestampStr string) error
	GenerateNonce() (string, error)

	// Key Parsing
	ReadArmoredKeyRing(armoredData string) (openpgp.EntityList, error)
	ExtractPublicKeyArmor(entity *openpgp.Entity) (string, error)
	FindEntityByIdentity(entities openpgp.EntityList, uid string) *openpgp.Entity

	// Key Metadata Extraction
	ExtractCreationTime(publicKey interface{}) time.Time
	ExtractExpirationTime(identity *openpgp.Identity, createdAt time.Time) *time.Time
	ExtractKeyExpirationTime(entity *openpgp.Entity, createdAt time.Time) *time.Time

	// Private Key Operations
	DecryptPrivateKey(encryptedKey, passphrase string) (string, error)
}
