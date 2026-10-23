//go:build !ops

package main

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// ensureRecoveryCollisionSchema extends the signup-test schema with the
// tables saveOwnIdentity/savePeerIdentity touch: network_stats (coverage
// counter), unclaimed_accounts, ongoing_recoveries,
// user_devices (own-claim device binding), username_collisions, and
// the self server row server_self points at.
func ensureRecoveryCollisionSchema(db *sql.DB) error {
	stmts := []string{
		`DROP TABLE IF EXISTS username_collisions CASCADE`,
		`DROP TABLE IF EXISTS user_devices CASCADE`,
		`DROP TABLE IF EXISTS ongoing_recoveries CASCADE`,
		`DROP TABLE IF EXISTS unclaimed_accounts CASCADE`,
		`DROP TABLE IF EXISTS network_stats CASCADE`,
		// public_keys/public_key_revocations are already created by
		// openSignupTestDB with the shape insertRecoveryKeys/
		// claimRecoveryUsername rely on (owner FKs identities(id) ON
		// DELETE CASCADE), so no recreation needed here.
		`CREATE TABLE network_stats (
			id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
			active_users INT NOT NULL DEFAULT 0
		)`,
		`INSERT INTO network_stats (id, active_users) VALUES (TRUE, 0)`,
		`CREATE TABLE unclaimed_accounts (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE ongoing_recoveries (
			user_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO servers (id, name, self, frontend_url) VALUES ('test', 'test', TRUE, 'https://test.example')`,
		`INSERT INTO server_self (server_id) VALUES ('test')`,
		`CREATE TABLE username_collisions (
			loser_id VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
			username VARCHAR(255) NOT NULL,
			winner_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			server_signature_id INT NOT NULL REFERENCES server_signatures(id)
		)`,
		`CREATE TABLE user_devices (
			user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			linked_at TIMESTAMPTZ NOT NULL,
			revoked_at TIMESTAMPTZ NULL,
			PRIMARY KEY (user_id, device_id, linked_at)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func openRecoveryCollisionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openSignupTestDB(t)
	if err := ensureRecoveryCollisionSchema(db); err != nil {
		t.Fatalf("recovery schema: %v", err)
	}
	return db
}

// testRecoveryKeyID is userID's one key in these fixtures, as a full key id.
func testRecoveryKeyID(userID string) string {
	return string(appendEntity(canonicalID("test", userID), userID+"-key"))
}

func testRecoveryProfile(userID, username string, signedAt time.Time) recoveryProfile {
	return recoveryProfile{
		ID:          string(canonicalID("test", userID)),
		Username:    username,
		Role:        "user",
		MemberSince: signedAt,
		UserSignature: recoveryUserSignature{
			KeyID: testRecoveryKeyID(userID),
			Armor: "user-sig-" + userID,
		},
		ServerSignature: recoveryServerSignature{
			ServerID:    "test",
			Fingerprint: "server-key",
			Armor:       "server-sig-" + userID,
			Timestamp:   signedAt,
		},
	}
}

func testRecoveryFlatKey(userID string, createdAt time.Time) []recoveryFlatKey {
	return []recoveryFlatKey{{
		Key: recoveryKeyWire{
			Fingerprint: testRecoveryKeyID(userID),
			UserID:      string(canonicalID("test", userID)),
			Armor:       "pubkey-" + userID,
			CreatedAt:   createdAt,
			ServerSignature: recoveryServerSignature{
				ServerID:    "test",
				Fingerprint: "server-key",
				Armor:       "key-server-sig-" + userID,
				Timestamp:   createdAt,
			},
		},
	}}
}

// testSigner stands in for the server key: it records what it signed.
func testSigner(signed *[][]byte) ledgerSigner {
	return func(payload []byte, ts time.Time) (ServerSignature, error) {
		*signed = append(*signed, payload)
		return ServerSignature{ID: "server-key@test", Armor: "sig", SignedAt: ts}, nil
	}
}

func setFirstBootAt(t *testing.T, db *sql.DB, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`UPDATE server_self SET first_boot_at = $1`, at); err != nil {
		t.Fatal(err)
	}
}

// usernameOf is userID's stored username, "" when NULL.
func usernameOf(t *testing.T, db *sql.DB, userID string) string {
	t.Helper()
	var name sql.NullString
	if err := db.QueryRow(`SELECT username FROM users WHERE id = $1`, userID).Scan(&name); err != nil {
		t.Fatalf("username of %s: %v", userID, err)
	}
	return name.String
}

// collisionOf is the user loserID lost a username to, "" when none.
func collisionOf(t *testing.T, db *sql.DB, loserID string) (username, winnerID string) {
	t.Helper()
	err := db.QueryRow(`SELECT username, winner_id FROM username_collisions WHERE loser_id = $1`, loserID).Scan(&username, &winnerID)
	if err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	return username, winnerID
}

// A user from before the wipe takes their username back from someone who
// signed up after it, however long the newer user held it.
func TestRecoveryUsername_PreWipeBeatsNewer(t *testing.T) {
	db := openRecoveryCollisionTestDB(t)
	ctx := t.Context()
	var signed [][]byte
	sign := testSigner(&signed)
	boot := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	setFirstBootAt(t, db, boot)

	newer := testRecoveryProfile("newer1", "bob", boot.Add(time.Hour))
	if _, err := savePeerIdentity(ctx, db, "test", sign, newer, testRecoveryFlatKey("newer1", boot.Add(time.Hour))); err != nil {
		t.Fatalf("seed newer holder: %v", err)
	}
	old := testRecoveryProfile("old1", "bob", boot.Add(-time.Hour))
	res, err := saveOwnIdentity(ctx, db, "test", sign, old, testRecoveryFlatKey("old1", boot.Add(-time.Hour)), "")
	if err != nil || res.Rejected || !res.Created {
		t.Fatalf("claim = %+v (%v)", res, err)
	}

	if got := usernameOf(t, db, "old1@test"); got != "bob" {
		t.Fatalf("pre-wipe user's username = %q", got)
	}
	if got := usernameOf(t, db, "newer1@test"); got != "" {
		t.Fatalf("newer user keeps username %q", got)
	}
	if name, winner := collisionOf(t, db, "newer1@test"); name != "bob" || winner != "old1@test" {
		t.Fatalf("collision = %q won by %q", name, winner)
	}
	if len(signed) != 1 || !strings.Contains(string(signed[0]), `"type":"username-collision"`) {
		t.Fatalf("signed %q", signed)
	}
}

// Between two records from before the wipe the newest wins; the older one
// is kept, without a username.
func TestRecoveryUsername_NewestPreWipeWins(t *testing.T) {
	db := openRecoveryCollisionTestDB(t)
	ctx := t.Context()
	var signed [][]byte
	sign := testSigner(&signed)
	boot := time.Now().UTC().Truncate(time.Second)
	setFirstBootAt(t, db, boot)

	holder := testRecoveryProfile("holder2", "carol", boot.Add(-time.Hour))
	if _, err := savePeerIdentity(ctx, db, "test", sign, holder, testRecoveryFlatKey("holder2", boot.Add(-time.Hour))); err != nil {
		t.Fatalf("seed holder: %v", err)
	}
	stale := testRecoveryProfile("stale2", "carol", boot.Add(-2*time.Hour))
	res, err := saveOwnIdentity(ctx, db, "test", sign, stale, testRecoveryFlatKey("stale2", boot.Add(-2*time.Hour)), "")
	if err != nil || res.Rejected || !res.Created {
		t.Fatalf("claim = %+v (%v)", res, err)
	}

	if got := usernameOf(t, db, "holder2@test"); got != "carol" {
		t.Fatalf("newest record lost its username: %q", got)
	}
	if got := usernameOf(t, db, "stale2@test"); got != "" {
		t.Fatalf("older record took username %q", got)
	}
	if name, winner := collisionOf(t, db, "stale2@test"); name != "carol" || winner != "holder2@test" {
		t.Fatalf("collision = %q won by %q", name, winner)
	}
	var keys int
	if err := db.QueryRow(`SELECT COUNT(*) FROM public_keys WHERE owner = $1`, "stale2@test").Scan(&keys); err != nil || keys != 1 {
		t.Fatalf("loser's keys = %d (%v)", keys, err)
	}
}

// Equal timestamps leave the name with its holder.
func TestRecoveryUsername_TieGoesToHolder(t *testing.T) {
	db := openRecoveryCollisionTestDB(t)
	ctx := t.Context()
	var signed [][]byte
	sign := testSigner(&signed)
	tie := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	holder := testRecoveryProfile("holder3", "dave", tie)
	if _, err := savePeerIdentity(ctx, db, "test", sign, holder, testRecoveryFlatKey("holder3", tie)); err != nil {
		t.Fatalf("seed holder: %v", err)
	}
	claimant := testRecoveryProfile("claimant3", "dave", tie)
	if _, err := saveOwnIdentity(ctx, db, "test", sign, claimant, testRecoveryFlatKey("claimant3", tie), ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if usernameOf(t, db, "holder3@test") != "dave" || usernameOf(t, db, "claimant3@test") != "" {
		t.Fatal("tie moved the username")
	}
}

// A claim for an ID a signup took after the wipe, with another key,
// replaces that newer account.
func TestRecoveryUserID_PreWipeReplacesNewer(t *testing.T) {
	db := openRecoveryCollisionTestDB(t)
	ctx := t.Context()
	var signed [][]byte
	sign := testSigner(&signed)
	boot := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	setFirstBootAt(t, db, boot)

	newer := testRecoveryProfile("same4", "erin", boot.Add(time.Hour))
	newerKeys := testRecoveryFlatKey("same4", boot.Add(time.Hour))
	newerKeys[0].Key.Fingerprint += "-newer"
	newer.UserSignature.KeyID = newerKeys[0].Key.Fingerprint
	if _, err := savePeerIdentity(ctx, db, "test", sign, newer, newerKeys); err != nil {
		t.Fatalf("seed newer account: %v", err)
	}

	old := testRecoveryProfile("same4", "frank", boot.Add(-time.Hour))
	res, err := saveOwnIdentity(ctx, db, "test", sign, old, testRecoveryFlatKey("same4", boot.Add(-time.Hour)), "")
	if err != nil || res.Rejected || !res.Created {
		t.Fatalf("claim = %+v (%v)", res, err)
	}
	if got := usernameOf(t, db, "same4@test"); got != "frank" {
		t.Fatalf("username = %q", got)
	}
	var keys int
	if err := db.QueryRow(`SELECT COUNT(*) FROM public_keys WHERE owner = $1 AND id = $2`, "same4@test", newerKeys[0].Key.Fingerprint).Scan(&keys); err != nil || keys != 0 {
		t.Fatalf("newer account's key kept: %d (%v)", keys, err)
	}
}

// An ID held from before the wipe by another key is refused.
func TestRecoveryUserID_PreWipeHolderKept(t *testing.T) {
	db := openRecoveryCollisionTestDB(t)
	ctx := t.Context()
	var signed [][]byte
	sign := testSigner(&signed)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	setFirstBootAt(t, db, at.Add(time.Minute))

	holder := testRecoveryProfile("same5", "gina", at)
	if _, err := savePeerIdentity(ctx, db, "test", sign, holder, testRecoveryFlatKey("same5", at)); err != nil {
		t.Fatalf("seed holder: %v", err)
	}
	other := testRecoveryProfile("same5", "hank", at.Add(time.Second))
	otherKeys := testRecoveryFlatKey("same5", at.Add(time.Second))
	otherKeys[0].Key.Fingerprint += "-other"
	res, err := saveOwnIdentity(ctx, db, "test", sign, other, otherKeys, "")
	if err != nil || !res.Rejected {
		t.Fatalf("claim = %+v (%v)", res, err)
	}
	if got := usernameOf(t, db, "same5@test"); got != "gina" {
		t.Fatalf("username = %q", got)
	}
}

// When a recovered claim deletes the newer account that won a username,
// the user who lost it gets it back.
func TestRecoveryUserID_ReplacedWinnerReturnsUsername(t *testing.T) {
	db := openRecoveryCollisionTestDB(t)
	ctx := t.Context()
	var signed [][]byte
	sign := testSigner(&signed)
	boot := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	setFirstBootAt(t, db, boot)

	loser := testRecoveryProfile("loser6", "ivy", boot.Add(time.Hour))
	if _, err := savePeerIdentity(ctx, db, "test", sign, loser, testRecoveryFlatKey("loser6", boot.Add(time.Hour))); err != nil {
		t.Fatalf("seed loser: %v", err)
	}
	winner := testRecoveryProfile("same6", "ivy", boot.Add(2*time.Hour))
	winnerKeys := testRecoveryFlatKey("same6", boot.Add(2*time.Hour))
	winnerKeys[0].Key.Fingerprint += "-newer"
	winner.UserSignature.KeyID = winnerKeys[0].Key.Fingerprint
	if _, err := savePeerIdentity(ctx, db, "test", sign, winner, winnerKeys); err != nil {
		t.Fatalf("seed winner: %v", err)
	}
	if name, _ := collisionOf(t, db, "loser6@test"); name != "ivy" {
		t.Fatalf("loser's collision = %q", name)
	}

	old := testRecoveryProfile("same6", "jack", boot.Add(-time.Hour))
	res, err := saveOwnIdentity(ctx, db, "test", sign, old, testRecoveryFlatKey("same6", boot.Add(-time.Hour)), "")
	if err != nil || res.Rejected || !res.Created {
		t.Fatalf("claim = %+v (%v)", res, err)
	}
	if got := usernameOf(t, db, "loser6@test"); got != "ivy" {
		t.Fatalf("loser's username = %q", got)
	}
	if name, _ := collisionOf(t, db, "loser6@test"); name != "" {
		t.Fatalf("collision kept: %q", name)
	}
}
