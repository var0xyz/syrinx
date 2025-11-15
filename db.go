package main

import (
	"database/sql"
	"log"
	"time"
)

// /////// //
//   API   //
// /////// //

type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	AvatarURL string    `json:"avatarURL"`
	Bio       string    `json:"bio"`
	CreatedAt time.Time `json:"memberSince"`

	// This is the fingerprint of the private key that the server generated for
	// this user. When a user creates a reed, this value needs to be included
	// in the reed's header. When a user verifies a reed, this value will be
	// used to find the private key that was used to sign the reed and verify
	// its signature.
	ServerKeyFingerprint string `json:"serverKeyFingerprint"`
}

type PublicKey struct {
	Fingerprint string     `json:"fingerprint"`
	UserID      string     `json:"userID"`
	Armor       string     `json:"armor"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

type PrivateKey struct {
	Fingerprint string     `json:"fingerprint"`
	UserID      string     `json:"userID"`
	Armor       string     `json:"armor"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

type Reed struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userID"`
	Fingerprint string    `json:"fingerprint"`
	SignedAt    time.Time `json:"signedAt"`
}

// /////// //
//   P2P   //
// /////// //

func InitDB(db *sql.DB) error {
	log.Println("Initializing database schema...")

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // This will be ignored if tx.Commit() is called

	// /////// //
	//   API   //
	// /////// //
	createUserCountTable := `
	CREATE TABLE IF NOT EXISTS user_count (
		id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
		count BIGINT DEFAULT 0
	);`
	createUsersTable := `
	CREATE TABLE IF NOT EXISTS users (
		id VARCHAR(255) PRIMARY KEY,
		username VARCHAR(255) UNIQUE NOT NULL,
		avatar_url VARCHAR(255),
		bio TEXT,
		server_key_fingerprint VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	createUserIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_lower_users_username
		ON users(LOWER(username));
	`

	createPrivateKeysTable := `
	CREATE TABLE IF NOT EXISTS private_keys (
		fingerprint VARCHAR(255) PRIMARY KEY,
		user_id VARCHAR(255) REFERENCES users(id) ON DELETE CASCADE,
		armor TEXT NOT NULL,
		revoked BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMP
	);`
	createPrivateKeyIndexes := `
	CREATE INDEX IF NOT EXISTS idx_private_keys_user_id
		ON private_keys(user_id);
	`

	createPublicKeysTable := `
	CREATE TABLE IF NOT EXISTS public_keys (
		fingerprint VARCHAR(255) PRIMARY KEY,
		user_id VARCHAR(255) NOT NULL,
		revoked BOOLEAN DEFAULT FALSE,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMP,

		FOREIGN KEY (user_id)
			REFERENCES users(id) ON DELETE CASCADE
	);`
	createPublicKeyIndexes := `
	CREATE INDEX IF NOT EXISTS idx_public_keys_user_id
		ON public_keys(user_id);
	`

	createRevokationsTable := `
	CREATE TABLE IF NOT EXISTS revokations (
		fingerprint VARCHAR(255) PRIMARY KEY,
		user_id VARCHAR(255) REFERENCES users(id) ON DELETE CASCADE,
		reason TEXT,
		description TEXT,
		revoked_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createReedsTable := `
	CREATE TABLE IF NOT EXISTS reeds (
		id VARCHAR(255) UNIQUE NOT NULL,
		user_id VARCHAR(255) REFERENCES users(id) ON DELETE CASCADE,
		fingerprint VARCHAR(255) REFERENCES private_keys(fingerprint),
		signed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (id, user_id)
	);`
	createReedIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reeds_user_id
		ON reeds(user_id);
	CREATE INDEX IF NOT EXISTS idx_reeds_signed_at
		ON reeds(signed_at);
	`

	// ////////// //
	//   Social   //
	// ////////// //

	createUserFollowersTable := `
	CREATE TABLE IF NOT EXISTS user_followers (
		user_id VARCHAR(255),
		follower_user_id VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (user_id, follower_user_id)
	);`
	createUserFollowerIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_followers_user_id
		ON user_followers(user_id);
	`

	createUserFollowingTable := `
	CREATE TABLE IF NOT EXISTS user_following (
		user_id VARCHAR(255),
		following_user_id VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (user_id, following_user_id)
	);`
	createUserFollowingIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_following_user_id
		ON user_following(user_id);
	`

	// /////// //
	//   P2P   //
	// /////// //

	createOnlineUsersTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS online_users (
		user_id VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createBroadcastSubscriptionsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS broadcast_subscriptions (
		user_id VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	createBroadcastSubscriptionIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_broadcast_subscriptions_user_id
		ON broadcast_subscriptions(user_id);
	`

	createReedAllocationsTable := `
	CREATE TABLE IF NOT EXISTS reed_allocations (
		reed_id VARCHAR(255) REFERENCES reeds(id) ON DELETE CASCADE,
		user_id VARCHAR(255) REFERENCES users(id) ON DELETE CASCADE,
		delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (reed_id, user_id)
	);`
	createReedAllocationIndexes := `
	CREATE INDEX IF NOT EXISTS idx_reed_allocations_reed_id
		ON reed_allocations(reed_id);
	`

	// Initialize user_count table with first row
	initUserCountTable := `
	INSERT INTO user_count (id, count) VALUES (1, 0) ON CONFLICT (id) DO NOTHING;`

	queries := []string{

		// API
		createUserCountTable,
		initUserCountTable,

		createUsersTable,
		createUserIndexes,

		createPrivateKeysTable,
		createPrivateKeyIndexes,

		createPublicKeysTable,
		createPublicKeyIndexes,

		createRevokationsTable,

		createReedsTable,
		createReedIndexes,

		// Social
		createUserFollowersTable,
		createUserFollowerIndexes,

		createUserFollowingTable,
		createUserFollowingIndexes,

		// P2P
		createOnlineUsersTable,
		createBroadcastSubscriptionsTable,
		createBroadcastSubscriptionIndexes,

		createReedAllocationsTable,
		createReedAllocationIndexes,
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
