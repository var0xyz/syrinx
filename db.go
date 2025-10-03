package main

import (
	"database/sql"
	"log"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	Password  string    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
}

type Profile struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"userID"`
	AvatarURL string    `json:"avatarURL"`
	Bio       string    `json:"bio"`
	Username  string    `json:"username"`

	DefaultIdentityID *uuid.UUID `json:"defaultIdentityID"`

	ServerKey  string   `json:"serverKey"`
	PublicKeys []string `json:"publicKeys"`
}

type PasswordResetNonce struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"userID"`
	Nonce     string    `json:"nonce"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type ServerKey struct {
	Fingerprint string     `json:"fingerprint"`
	UserID      uuid.UUID  `json:"userID"`
	PublicKey   string     `json:"publicKey"`
	PrivateKey  string     `json:"privateKey"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
	Identity    string     `json:"identity"`
}

type ServerPublicKey struct {
	Fingerprint string     `json:"fingerprint"`
	UserID      uuid.UUID  `json:"userID"`
	Armor       string     `json:"armor"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
	Identity    string     `json:"identity"`
}

type PublicKey struct {
	Fingerprint string     `json:"fingerprint"`
	UserID      uuid.UUID  `json:"userID"`
	Armor       string     `json:"armor"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`

	Identities []PublicKeyIdentity `json:"identities"`
}

type PublicKeyIdentity struct {
	ID    uuid.UUID `json:"id"`
	Value string    `json:"value"`
}

type Reed struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"userID"`
	CreatedAt time.Time `json:"createdAt"`
	Identity  string    `json:"identity"`

	UserFingerprint   string `json:"userFingerprint"`
	ServerFingerprint string `json:"serverFingerprint"`
}

// GenerateUUIDV7 generates a new UUID v7 (time-ordered UUID)
func GenerateUUIDV7() (uuid.UUID, error) {
	return uuid.NewV7()
}

func InitDB(db *sql.DB) error {
	log.Println("Initializing database schema with UUID v7 support...")

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // This will be ignored if tx.Commit() is called

	createUsersTable := `
	CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v7(),
		username VARCHAR(255) UNIQUE NOT NULL,
		password VARCHAR(255) NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	createUserIndexes := `
	CREATE INDEX IF NOT EXISTS idx_users_username
		ON users(username);
	`

	createServerKeysTable := `
	CREATE TABLE IF NOT EXISTS server_keys (
		fingerprint VARCHAR(255) PRIMARY KEY,
		user_id UUID REFERENCES users(id),
		identity VARCHAR(255),
		public_key TEXT NOT NULL,
		private_key TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMP
	);`
	createServerKeysIndexes := `
	CREATE INDEX IF NOT EXISTS idx_server_keys_user_id
		ON server_keys(user_id);
	`

	createPublicKeysTable := `
	CREATE TABLE IF NOT EXISTS public_keys (
		fingerprint VARCHAR(255) PRIMARY KEY,
		user_id UUID NOT NULL,
		armor TEXT NOT NULL,
		created_at TIMESTAMP,
		expires_at TIMESTAMP,

		FOREIGN KEY (user_id)
			REFERENCES users(id) ON DELETE CASCADE
	);`
	createPublicKeyIndexes := `
	CREATE INDEX IF NOT EXISTS idx_public_keys_user_id
		ON public_keys(user_id);
	`

	createPublicKeyIdentitiesTable := `
	CREATE TABLE IF NOT EXISTS public_key_identities (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v7(),
		public_key_fingerprint VARCHAR(255) NOT NULL,
		value VARCHAR(255),

		FOREIGN KEY (public_key_fingerprint)
			REFERENCES public_keys(fingerprint) ON DELETE CASCADE
	);`

	createProfilesTable := `
	CREATE TABLE IF NOT EXISTS profiles (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v7(),
		user_id UUID REFERENCES users(id) ON DELETE CASCADE,
		avatar_url VARCHAR(255),
		bio TEXT,
		default_identity_id UUID,

		FOREIGN KEY (default_identity_id)
			REFERENCES public_key_identities(id)
	);`
	createProfileIndexes := `
	CREATE INDEX IF NOT EXISTS idx_profiles_user_id
		ON profiles(user_id);
	`

	createPasswordResetNoncesTable := `
	CREATE TABLE IF NOT EXISTS password_reset_nonces (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v7(),
		user_id UUID REFERENCES users(id) ON DELETE CASCADE,
		nonce VARCHAR(255) UNIQUE NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMP NOT NULL
	);`
	createPasswordResetNonceIndexes := `
	CREATE INDEX IF NOT EXISTS idx_password_reset_nonces_nonce
		ON password_reset_nonces(nonce);
	CREATE INDEX IF NOT EXISTS idx_password_reset_nonces_user_id
		ON password_reset_nonces(user_id);
	`

	createReedsTable := `
	CREATE TABLE IF NOT EXISTS reeds (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v7(),
		user_id UUID REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		identity_id UUID,
		user_fingerprint VARCHAR(255),
		server_fingerprint VARCHAR(255),

		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		FOREIGN KEY (identity_id) REFERENCES public_key_identities(id),
		FOREIGN KEY (user_fingerprint) REFERENCES public_keys(fingerprint) ON DELETE CASCADE,
		FOREIGN KEY (server_fingerprint) REFERENCES server_keys(fingerprint)
	);`
	createReedIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reeds_id
		ON reeds(id);
	CREATE INDEX IF NOT EXISTS idx_reeds_user_id
		ON reeds(user_id);
	`

	queries := []string{
		createUsersTable,
		createUserIndexes,

		createServerKeysTable,
		createServerKeysIndexes,

		createPublicKeysTable,
		createPublicKeyIndexes,
		createPublicKeyIdentitiesTable,

		createProfilesTable,
		createProfileIndexes,

		createPasswordResetNoncesTable,
		createPasswordResetNonceIndexes,

		createReedsTable,
		createReedIndexes,
	}

	for i, query := range queries {
		log.Printf("Executing query %d/%d", i+1, len(queries))
		if _, err := tx.Exec(query); err != nil {
			log.Printf("Error executing query %d: %v", i+1, err)
			log.Println(query)
			return err
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return err
	}

	log.Println("Database schema initialized successfully!")
	return nil
}
