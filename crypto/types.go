package crypto

import "time"

// CryptographicKey represents a cryptographic key with metadata
type CryptographicKey struct {
	Fingerprint string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	Armor       string
}

// KeyPair represents a public/private key pair
type KeyPair struct {
	Fingerprint string
	UserID      string
	PublicKey   string
	PrivateKey  string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	Identity    string
}
