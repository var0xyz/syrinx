package main

import (
	"database/sql"
	"log"
	"time"
)

// /////// //
//   API   //
// /////// //

type Server struct {
	Id         string    `json:"id"`
	Name       string    `json:"name"`
	Self       bool      `json:"self"`
	SigningKey Key       `json:"signingKey"`
	CreatedAt  time.Time `json:"createdAt"`
}

type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	AvatarURL   string    `json:"avatarURL"`
	Bio         string    `json:"bio"`
	CreatedAt   time.Time `json:"memberSince"`
	HasReeds    bool      `json:"hasReeds"`
	Fingerprint string    `json:"fingerprint"`
	Server      string    `json:"server"`
}

type Key struct {
	Fingerprint string     `json:"fingerprint"`
	Armor       string     `json:"armor"`
	CreatedAt   time.Time  `json:"createdAt"`
	Revoked     *Revoke    `json:"revoked"`
}

type Revoke struct {
	Timestamp time.Time `json:"timestamp"`
	Reason string `json:"reason"`
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
	createServersTable := `
	CREATE TABLE IF NOT EXISTS servers (
		id VARCHAR(16) UNIQUE,
		name VARCHAR(255) PRIMARY KEY,
		self BOOLEAN NOT NULL DEFAULT FALSE,
		signing_key VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

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
		fingerprint VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createUserIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_lower_users_username
		ON users(LOWER(username));
	`

	// Server-owned private keys
	createPrivateKeysTable := `
	CREATE TABLE IF NOT EXISTS private_keys (
		fingerprint VARCHAR(255) PRIMARY KEY,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		revoked_at TIMESTAMP,
		revoke_reason TEXT
	);`

	// Server-owned public keys
	createPublicKeysTable := `
	CREATE TABLE IF NOT EXISTS public_keys (
		fingerprint VARCHAR(255) PRIMARY KEY,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	// Client-managed public keys
	createUserKeysTable := `
	CREATE TABLE IF NOT EXISTS user_keys (
		fingerprint VARCHAR(255),
		owner VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		revoked BOOLEAN DEFAULT FALSE,
		armor TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMP,

		PRIMARY KEY (owner, fingerprint)
	);`

	createUserKeyIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_keys_owner
		ON user_keys(owner);
	`

	createUserKeyRevocationsTable := `
	CREATE TABLE IF NOT EXISTS user_key_revocations (
		fingerprint VARCHAR(255),
		owner VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		reason TEXT,
		revoked_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (fingerprint, owner),
		FOREIGN KEY (fingerprint, owner) REFERENCES user_keys(fingerprint, owner)
	);`

	createReedsTable := `
	CREATE TABLE IF NOT EXISTS reeds (
		id VARCHAR(255) UNIQUE NOT NULL,
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		private_key_fingerprint VARCHAR(255) NOT NULL REFERENCES private_keys(fingerprint),
		signed_at TIMESTAMP NOT NULL,

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
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		follower_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

		PRIMARY KEY (user_id, follower_user_id)
	);`

	createUserFollowerIndexes := `
	CREATE INDEX IF NOT EXISTS idx_user_followers_user_id
		ON user_followers(user_id);
	`

	createUserFollowingTable := `
	CREATE TABLE IF NOT EXISTS user_following (
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		following_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
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
		user_id    VARCHAR(255) PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createBroadcastSubscriptionsTable := `
	CREATE UNLOGGED TABLE IF NOT EXISTS broadcast_subscriptions (
		user_id    VARCHAR(255) PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	createBroadcastSubscriptionIndexes := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_broadcast_subscriptions_user_id
		ON broadcast_subscriptions(user_id);
	`

	createReedAllocationsTable := `
	CREATE TABLE IF NOT EXISTS reed_allocations (
		reed_id VARCHAR(255) NOT NULL REFERENCES reeds(id) ON DELETE CASCADE,
		user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
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
		// Servers
		createServersTable,

		// API
		createUserCountTable,
		initUserCountTable,

		createUsersTable,
		createUserIndexes,

		createPrivateKeysTable,
		createPublicKeysTable,

		createUserKeysTable,
		createUserKeyIndexes,

		createUserKeyRevocationsTable,

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
