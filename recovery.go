package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// recoveryUserStatusResponse is the POST /api/users/status JSON body.
type recoveryUserStatusResponse struct {
	Status string `json:"status"` // complete | unknown | ongoing
}

const (
	recoveryUserStatusComplete = "complete"
	recoveryUserStatusUnknown  = "unknown"
	recoveryUserStatusOngoing  = "ongoing"
)

var (
	recoveryUserStatusCompleteResponse = recoveryUserStatusResponse{Status: recoveryUserStatusComplete}
	recoveryUserStatusUnknownResponse  = recoveryUserStatusResponse{Status: recoveryUserStatusUnknown}
	recoveryUserStatusOngoingResponse  = recoveryUserStatusResponse{Status: recoveryUserStatusOngoing}
)

// errRecoveryNoIdentityFound is returned when RECOVERY_MODE is on but no
// self server row exists (operator must run ops import-identity first).
var errRecoveryNoIdentityFound = fmt.Errorf(
	"RECOVERY_MODE requires a restored server identity; run: ops import-identity <path-to-bundle>",
)

const bundleVersion = 1

const challengeMaxAge = 60 * time.Second

// recoveryBundle is the plaintext identity export (before symmetric encryption).
type recoveryBundle struct {
	Version      int                 `json:"version"`
	ExportedAt   time.Time           `json:"exportedAt"`
	ServerID     string              `json:"serverID"`
	ServerName   string              `json:"serverName"`
	SigningKeyID string              `json:"signingKeyID"`
	Keys         []recoveryBundleKey `json:"keys"`
}

// recoveryBundleKey is one server signing key (active or rotated/revoked).
// PrivateKeyArmor remains passphrase-wrapped; export never decrypts it.
type recoveryBundleKey struct {
	ID              string     `json:"id"`
	PrivateKeyArmor string     `json:"privateKeyArmor"`
	PublicKeyArmor  string     `json:"publicKeyArmor"`
	CreatedAt       time.Time  `json:"createdAt"`
	RevokedAt       *time.Time `json:"revokedAt"`
	RevokeReason    *string    `json:"revokeReason"`
}

// defaultExportFilename returns syrinx-<serverID>-<YYYYMMDDTHHMMSSZ>.sxi.gpg.
func defaultExportFilename(serverID string, exportedAt time.Time) string {
	ts := exportedAt.UTC().Format("20060102T150405Z")
	return fmt.Sprintf("syrinx-%s-%s.sxi.gpg", serverID, ts)
}

// exportFromDB builds a bundle from the self server row and full key history.
// exportedAt should be UTC truncated to seconds; it becomes recoveryBundle.ExportedAt.
func exportFromDB(ctx context.Context, db *sql.DB, exportedAt time.Time) (*recoveryBundle, error) {
	exportedAt = exportedAt.UTC().Truncate(time.Second)

	var serverID, serverName, signingFP string
	err := db.QueryRowContext(ctx, `
		SELECT id, name, signing_key FROM servers WHERE self = TRUE
	`).Scan(&serverID, &serverName, &signingFP)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no self server row")
	}
	if err != nil {
		return nil, fmt.Errorf("load self server: %w", err)
	}
	if signingFP == "" {
		return nil, fmt.Errorf("self server has no signing_key")
	}
	if serverName == "" {
		return nil, fmt.Errorf("self server has no name")
	}

	rows, err := db.QueryContext(ctx, `
		SELECT pk.id, pk.armor, pub.armor, pk.created_at, pk.revoked_at, pk.revoke_reason
		FROM private_keys pk
		JOIN public_keys pub ON pub.id = pk.id
		ORDER BY pk.created_at ASC, pk.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	defer rows.Close()

	var keys []recoveryBundleKey
	for rows.Next() {
		var k recoveryBundleKey
		var revokedAt sql.NullTime
		var reason sql.NullString
		if err := rows.Scan(
			&k.ID, &k.PrivateKeyArmor, &k.PublicKeyArmor, &k.CreatedAt, &revokedAt, &reason,
		); err != nil {
			return nil, fmt.Errorf("scan key: %w", err)
		}
		k.CreatedAt = k.CreatedAt.UTC()
		if revokedAt.Valid {
			t := revokedAt.Time.UTC()
			k.RevokedAt = &t
		}
		if reason.Valid {
			r := reason.String
			k.RevokeReason = &r
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no server keys to export")
	}

	b := &recoveryBundle{
		Version:      bundleVersion,
		ExportedAt:   exportedAt,
		ServerID:     serverID,
		ServerName:   serverName,
		SigningKeyID: signingFP,
		Keys:         keys,
	}
	if err := validateBundleShape(b); err != nil {
		return nil, err
	}
	return b, nil
}

// validateBundleShape checks structural integrity of a decrypted bundle.
func validateBundleShape(b *recoveryBundle) error {
	if b == nil {
		return fmt.Errorf("bundle is nil")
	}
	if b.Version != bundleVersion {
		return fmt.Errorf("unsupported bundle version %d", b.Version)
	}
	if b.ServerID == "" {
		return fmt.Errorf("serverID is empty")
	}
	if b.ServerName == "" {
		return fmt.Errorf("serverName is empty")
	}
	if b.SigningKeyID == "" {
		return fmt.Errorf("signingKeyID is empty")
	}
	if len(b.Keys) == 0 {
		return fmt.Errorf("keys is empty")
	}
	seen := make(map[string]struct{}, len(b.Keys))
	foundSigning := false
	for i, k := range b.Keys {
		if k.ID == "" {
			return fmt.Errorf("keys[%d]: id is empty", i)
		}
		if k.PrivateKeyArmor == "" {
			return fmt.Errorf("keys[%d]: privateKeyArmor is empty", i)
		}
		if k.PublicKeyArmor == "" {
			return fmt.Errorf("keys[%d]: publicKeyArmor is empty", i)
		}
		if k.CreatedAt.IsZero() {
			return fmt.Errorf("keys[%d]: createdAt is zero", i)
		}
		if _, ok := seen[k.ID]; ok {
			return fmt.Errorf("duplicate key id %s", k.ID)
		}
		seen[k.ID] = struct{}{}
		if k.ID == b.SigningKeyID {
			foundSigning = true
		}
	}
	if !foundSigning {
		return fmt.Errorf("signingKeyID %s not present in keys", b.SigningKeyID)
	}
	return nil
}

// validateBundleDecrypt checks that every private armor decrypts with passphrase.
func validateBundleDecrypt(b *recoveryBundle, cryptoSvc *cryptoService, passphrase string) error {
	if err := validateBundleShape(b); err != nil {
		return err
	}
	for i, k := range b.Keys {
		if _, err := cryptoSvc.decryptPrivateKey(k.PrivateKeyArmor, passphrase); err != nil {
			return fmt.Errorf("keys[%d] (%s): wrong server key passphrase or corrupt armor", i, k.ID)
		}
	}
	return nil
}

// marshalBundleJSON encodes the bundle as JSON (second-precision times).
// Key armor is base64-encoded on the way out — the bundle's in-memory
// recoveryBundleKey fields are plain armor (matching the DB), but the
// serialized file, like every other signed/keyed artifact that crosses a
// wire or file boundary, carries base64.
func marshalBundleJSON(b *recoveryBundle) ([]byte, error) {
	if err := validateBundleShape(b); err != nil {
		return nil, err
	}
	wire := *b
	wire.Keys = make([]recoveryBundleKey, len(b.Keys))
	for i, k := range b.Keys {
		wire.Keys[i] = k
		wire.Keys[i].PrivateKeyArmor = base64Encode(k.PrivateKeyArmor)
		wire.Keys[i].PublicKeyArmor = base64Encode(k.PublicKeyArmor)
	}
	return json.MarshalIndent(&wire, "", "  ")
}

// parseBundleJSON decodes a serialized bundle, base64-decoding key armor
// back to plain armor before shape validation — the mirror of
// marshalBundleJSON's encode step.
func parseBundleJSON(data []byte) (*recoveryBundle, error) {
	var b recoveryBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("invalid bundle JSON")
	}
	for i, k := range b.Keys {
		priv, err := base64Decode(k.PrivateKeyArmor)
		if err != nil {
			return nil, fmt.Errorf("keys[%d]: invalid privateKeyArmor encoding", i)
		}
		pub, err := base64Decode(k.PublicKeyArmor)
		if err != nil {
			return nil, fmt.Errorf("keys[%d]: invalid publicKeyArmor encoding", i)
		}
		b.Keys[i].PrivateKeyArmor = priv
		b.Keys[i].PublicKeyArmor = pub
	}
	if err := validateBundleShape(&b); err != nil {
		return nil, err
	}
	return &b, nil
}

// setIdentityBackupAt records a successful export time on the self server row.
func setIdentityBackupAt(ctx context.Context, db *sql.DB, at time.Time) error {
	at = at.UTC().Truncate(time.Second)
	res, err := db.ExecContext(ctx, `UPDATE servers SET identity_backup_at = $1 WHERE self = TRUE`, at)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no self server row to update")
	}
	return nil
}

// staleIdentityBackupMessage returns a non-empty warning when the self
// identity has never been backed up, or the backup is older than the newest
// private key. Empty string means no warning.
func staleIdentityBackupMessage(ctx context.Context, db *sql.DB) (string, error) {
	var serverID string
	var backupAt sql.NullTime
	var newestKey sql.NullTime

	err := db.QueryRowContext(ctx, `
		SELECT s.id, s.identity_backup_at,
			(SELECT MAX(created_at) FROM private_keys)
		FROM servers s
		WHERE s.self = TRUE
	`).Scan(&serverID, &backupAt, &newestKey)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !newestKey.Valid {
		return "", nil
	}
	if !backupAt.Valid {
		return fmt.Sprintf(
			"server identity %s has never been exported — run: ops export-identity",
			serverID,
		), nil
	}
	if backupAt.Time.Before(newestKey.Time) {
		return fmt.Sprintf(
			"server identity backup is stale (backup %s < newest key %s) — run: ops export-identity",
			backupAt.Time.UTC().Format(time.RFC3339),
			newestKey.Time.UTC().Format(time.RFC3339),
		), nil
	}
	return "", nil
}

// recoveryImportResult describes the outcome of importIntoDB.
type recoveryImportResult int

const (
	// recoveryImportApplied means the self identity and keys were written.
	recoveryImportApplied recoveryImportResult = iota
	// recoveryImportAlreadyPresent means the DB already held a matching identity.
	recoveryImportAlreadyPresent
)

// recoveryExistingKey is one private_keys row used for match comparison.
type recoveryExistingKey struct {
	ID    string
	Armor string
}

// recoveryExistingSelf is the self servers row used for match comparison.
type recoveryExistingSelf struct {
	ID         string
	Name       string
	SigningKey string
}

// identityMatchesBundle reports whether existing self + keys equal the bundle.
// Both sides are already canonical ids — no stripping/re-wrapping needed.
func identityMatchesBundle(b *recoveryBundle, self recoveryExistingSelf, keys []recoveryExistingKey) bool {
	if b == nil {
		return false
	}
	if self.ID != b.ServerID || self.Name != b.ServerName ||
		self.SigningKey != b.SigningKeyID {
		return false
	}
	if len(keys) != len(b.Keys) {
		return false
	}
	byID := make(map[string]string, len(keys))
	for _, k := range keys {
		byID[k.ID] = k.Armor
	}
	for _, k := range b.Keys {
		armor, ok := byID[k.ID]
		if !ok || armor != k.PrivateKeyArmor {
			return false
		}
	}
	return true
}

// importIntoDB restores identity from bundle into db. Caller must have run
// InitDB and already validated every key decrypts under passphrase
// (validateBundleDecrypt) — that same passphrase is used here to produce
// a fresh self-countersignature for each restored public key, since the
// original self-signature isn't part of the bundle. On mismatch with an
// existing self identity, returns an error and writes nothing.
func importIntoDB(ctx context.Context, db *sql.DB, cryptoSvc *cryptoService, passphrase string, b *recoveryBundle) (recoveryImportResult, error) {
	if err := validateBundleShape(b); err != nil {
		return 0, err
	}

	var self recoveryExistingSelf
	err := db.QueryRowContext(ctx, `
		SELECT id, name, COALESCE(signing_key, '') FROM servers WHERE self = TRUE
	`).Scan(&self.ID, &self.Name, &self.SigningKey)

	if err == nil {
		keys, kerr := loadExistingPrivateKeys(ctx, db)
		if kerr != nil {
			return 0, kerr
		}
		if identityMatchesBundle(b, self, keys) {
			return recoveryImportAlreadyPresent, nil
		}
		return 0, fmt.Errorf(
			"self identity already exists and does not match the bundle (db id=%s name=%s signing=%s; bundle id=%s name=%s signing=%s) — resolve manually before re-importing",
			self.ID, self.Name, self.SigningKey, b.ServerID, b.ServerName, b.SigningKeyID,
		)
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("load self server: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for _, k := range b.Keys {
		var revokedAt interface{}
		var reason interface{}
		if k.RevokedAt != nil {
			revokedAt = k.RevokedAt.UTC()
		}
		if k.RevokeReason != nil {
			reason = *k.RevokeReason
		}
		keyID := k.ID
		bareFP, _, ok := parseIdentityID(identityID(keyID))
		if !ok {
			return 0, fmt.Errorf("malformed bundle key id: %s", keyID)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO private_keys (id, armor, created_at, revoked_at, revoke_reason)
			VALUES ($1, $2, $3, $4, $5)
		`, keyID, k.PrivateKeyArmor, k.CreatedAt.UTC(), revokedAt, reason); err != nil {
			return 0, fmt.Errorf("insert private_keys %s: %w", keyID, err)
		}

		plainPrivate, err := cryptoSvc.decryptPrivateKey(k.PrivateKeyArmor, passphrase)
		if err != nil {
			return 0, fmt.Errorf("decrypt private key %s: %w", keyID, err)
		}
		selfPayload := buildPublicKeyPayload(
			b.ServerID, keyID, keyID, bareFP, k.PublicKeyArmor, k.CreatedAt.UTC(),
		)
		selfSigArmor, err := cryptoSvc.sign(string(selfPayload), plainPrivate)
		if err != nil {
			return 0, fmt.Errorf("self-countersign restored key %s: %w", keyID, err)
		}
		serverSignatureID, err := insertRecoveryServerSignature(ctx, tx, keyID, selfSigArmor, k.CreatedAt.UTC())
		if err != nil {
			return 0, fmt.Errorf("insert server signature for %s: %w", keyID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO public_keys (id, armor, created_at, server_signature_id)
			VALUES ($1, $2, $3, $4)
		`, keyID, k.PublicKeyArmor, k.CreatedAt.UTC(), serverSignatureID); err != nil {
			return 0, fmt.Errorf("insert public_keys %s: %w", keyID, err)
		}
	}

	backupAt := b.ExportedAt.UTC().Truncate(time.Second)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO servers (id, name, self, signing_key, identity_backup_at)
		VALUES ($1, $2, TRUE, $3, $4)
	`, b.ServerID, b.ServerName, b.SigningKeyID, backupAt); err != nil {
		return 0, fmt.Errorf("insert self server: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return recoveryImportApplied, nil
}

// insertRecoveryServerSignature inserts a server countersignature row and
// returns its id. Duplicates services.go's insertServerSignature (same
// signing tag, !ops && !ripplescleanup) rather than pulling that file's
// large, deeply !ops-coupled signing section into every binary variant —
// this is the only ops-reachable caller that needs it.
func insertRecoveryServerSignature(ctx context.Context, tx *sql.Tx, privateKeyID, signature string, signedAt time.Time) (int64, error) {
	signedAt = signedAt.UTC().Truncate(time.Second)
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO server_signatures (private_key_id, signature, signed_at)
		VALUES ($1, $2, $3)
		RETURNING id
	`, privateKeyID, signature, signedAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert server_signatures: %w", err)
	}
	return id, nil
}

func loadExistingPrivateKeys(ctx context.Context, db *sql.DB) ([]recoveryExistingKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, armor FROM private_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []recoveryExistingKey
	for rows.Next() {
		var k recoveryExistingKey
		if err := rows.Scan(&k.ID, &k.Armor); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// rotateServerKeyPassphrase re-wraps every private_keys.armor under newPassphrase.
// Private key material is decrypted with oldPassphrase then re-encrypted; fingerprints
// and public keys are unchanged.
func rotateServerKeyPassphrase(ctx context.Context, db *sql.DB, cryptoSvc *cryptoService, oldPassphrase, newPassphrase string) error {
	if len(newPassphrase) < 16 {
		return fmt.Errorf("new passphrase must be at least 16 characters")
	}

	rows, err := db.QueryContext(ctx, `SELECT id, armor FROM private_keys`)
	if err != nil {
		return fmt.Errorf("list private keys: %w", err)
	}
	defer rows.Close()

	type item struct {
		fp    string
		armor string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.fp, &it.armor); err != nil {
			return err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("no private keys to re-wrap")
	}

	rewrapped := make([]item, 0, len(items))
	for _, it := range items {
		plain, err := cryptoSvc.decryptPrivateKey(it.armor, oldPassphrase)
		if err != nil {
			return fmt.Errorf("decrypt %s (wrong current passphrase?): %w", it.fp, err)
		}
		enc, err := cryptoSvc.encryptPrivateKey(plain, newPassphrase)
		if err != nil {
			return fmt.Errorf("encrypt %s: %w", it.fp, err)
		}
		rewrapped = append(rewrapped, item{fp: it.fp, armor: enc})
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, it := range rewrapped {
		if _, err := tx.ExecContext(ctx, `UPDATE private_keys SET armor = $1 WHERE id = $2`, it.armor, it.fp); err != nil {
			return fmt.Errorf("update %s: %w", it.fp, err)
		}
	}
	return tx.Commit()
}

const recoveryRecommendedPasswordLen = 16

// passwordStrengthWarning returns a non-empty message when pw is weaker than
// recommended. Callers should print it and still accept the password (except
// empty, which remains invalid for the bundle).
func passwordStrengthWarning(pw string) string {
	if pw == "" {
		return ""
	}
	if len(pw) < recoveryRecommendedPasswordLen {
		return fmt.Sprintf(
			"password is only %d characters (recommend ≥%d with upper, lower, digit, and symbol)",
			len(pw), recoveryRecommendedPasswordLen,
		)
	}

	var upper, lower, digit, symbol bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			symbol = true
		}
	}
	var missing []string
	if !upper {
		missing = append(missing, "uppercase")
	}
	if !lower {
		missing = append(missing, "lowercase")
	}
	if !digit {
		missing = append(missing, "digit")
	}
	if !symbol {
		missing = append(missing, "symbol")
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"password is missing %s (recommend mixed case, digits, and symbols)",
		strings.Join(missing, ", "),
	)
}

// === //
// Wire types + nest verification
// === //

// DELIBERATE EXCEPTION — recoveryKeyWire.Fingerprint / recoveryRevocation.Fingerprint
// stay bare (recoveryKeyNest.ts signs those raw); nested recoveryUserSignature /
// recoveryServerSignature are NOT part of that and decode canonical `id` below.

// recoveryUserSignature is the nested user attestation wire block for
// recovery's own claim/peer/reed flows. KeyID is the full canonical key id
// ("userID@serverID/fingerprint"), not a bare fingerprint — signed profile
// payloads bind the whole thing. Distinct from root's UserSignature (plain
// string ID) and signing's DB-row shape — this is the only variant that
// splits the wire `id` into components via custom JSON marshal/unmarshal.
type recoveryUserSignature struct {
	KeyID string `json:"-"`
	Armor string `json:"armor"`
}

func (s *recoveryUserSignature) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID    string `json:"id"`
		Armor string `json:"armor"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	s.Armor = wire.Armor
	s.KeyID = wire.ID
	return nil
}

func (s recoveryUserSignature) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID    string `json:"id"`
		Armor string `json:"armor"`
	}{
		ID:    s.KeyID,
		Armor: s.Armor,
	})
}

// recoveryServerSignature is the server's countersignature metadata on a
// signed resource (identity, public key, revocation, reed) within recovery's
// own flows. Decodes the wire's `id` (canonical "fingerprint@serverID") into
// Fingerprint and ServerID — distinct from both root's ServerSignature and
// signing's DB-row shape.
type recoveryServerSignature struct {
	ServerID    string    `json:"-"`
	Fingerprint string    `json:"-"`
	Armor       string    `json:"armor"`
	Timestamp   time.Time `json:"timestamp"`
}

func (s *recoveryServerSignature) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID        string    `json:"id"`
		Armor     string    `json:"armor"`
		Timestamp time.Time `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	s.Armor = wire.Armor
	s.Timestamp = wire.Timestamp
	if wire.ID == "" {
		return nil
	}
	fingerprint, serverID, ok := parseIdentityID(identityID(wire.ID))
	if !ok {
		return fmt.Errorf("serverSignature.id is not a canonical key id: %q", wire.ID)
	}
	s.Fingerprint = fingerprint
	s.ServerID = serverID
	return nil
}

func (s recoveryServerSignature) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID        string    `json:"id"`
		Armor     string    `json:"armor"`
		Timestamp time.Time `json:"timestamp"`
	}{
		ID:        string(canonicalID(s.ServerID, s.Fingerprint)),
		Armor:     s.Armor,
		Timestamp: s.Timestamp,
	})
}

// recoveryProfile is the wire shape of a countersigned identity record used
// by recovery's claim/peer/status flows. Close to but not identical to
// root's User (adds ActiveKeyFingerprint/HasReeds, uses the split-ID
// signature variants above) — kept as its own type rather than forced into
// User's shape.
type recoveryProfile struct {
	ID                   string                  `json:"id"`
	Username             string                  `json:"username"`
	Role                 string                  `json:"role"`
	MemberSince          time.Time               `json:"memberSince"`
	Bio                  string                  `json:"bio"`
	ActiveKeyFingerprint string                  `json:"activeKeyFingerprint"`
	UserSignature        recoveryUserSignature   `json:"userSignature"`
	ServerSignature      recoveryServerSignature `json:"serverSignature"`
	HasReeds             bool                    `json:"hasReeds"`
	Invite               *Invite                 `json:"invite"`
}

// recoveryKeyWire is the public-key fields shared by live Key responses and
// each level of a recovery nest (fingerprint, armor, server countersig, …).
type recoveryKeyWire struct {
	Fingerprint     string                  `json:"fingerprint"`
	UserID          string                  `json:"userID"`
	Armor           string                  `json:"armor"`
	CreatedAt       time.Time               `json:"createdAt"`
	ExpiresAt       *time.Time              `json:"expiresAt,omitempty"`
	Revoked         bool                    `json:"revoked"`
	ServerSignature recoveryServerSignature `json:"serverSignature"`
}

// recoveryRevocation is a signed revocation attestation for a user key.
type recoveryRevocation struct {
	Fingerprint     string                  `json:"fingerprint"`
	UserID          string                  `json:"userID"`
	Reason          string                  `json:"reason"`
	Successor       *string                 `json:"successor"`
	UserSignature   recoveryUserSignature   `json:"userSignature"`
	ServerSignature recoveryServerSignature `json:"serverSignature"`
}

// recoveryKeyNode is one level of the nested key chain. recoveryKeyWire is
// embedded so the wire is `key.armor` / `predecessor.armor` (not
// `key.key.armor`). Outermost is the active key; Predecessor walks back to
// the signup key (nil). Signature is set only on predecessor links: the
// older key's detached sig over the newer (parent) key's armor.
type recoveryKeyNode struct {
	recoveryKeyWire
	Signature   string              `json:"signature,omitempty"`
	Revocation  *recoveryRevocation `json:"revocation"`
	Predecessor *recoveryKeyNode    `json:"predecessor"`
}

// recoveryClaimRequest is the POST /api/recovery/identity/claim body.
type recoveryClaimRequest struct {
	Challenge int64           `json:"challenge"`
	Signature string          `json:"signature"`
	Profile   recoveryProfile `json:"profile"`
	Key       recoveryKeyNode `json:"key"`
}

// recoveryPeerIdentityRequest is the POST /api/recovery/identity body.
type recoveryPeerIdentityRequest struct {
	Profile recoveryProfile `json:"profile"`
	Key     recoveryKeyNode `json:"key"`
}

// recoveryChallengeResponse is the GET /api/recovery/identity/claim body.
type recoveryChallengeResponse struct {
	Challenge int64 `json:"challenge"`
}

// recoveryReedRequest is the POST /api/recovery/reeds body.
type recoveryReedRequest struct {
	ReedID          string                  `json:"reedID"`
	AuthorID        string                  `json:"authorID"`
	UserSignature   recoveryUserSignature   `json:"userSignature"`
	ServerSignature recoveryServerSignature `json:"serverSignature"`
}

// recoveryFollowingRequest is the POST /api/recovery/following body.
type recoveryFollowingRequest struct {
	UserIDs []string `json:"userIDs"`
}

// recoveryFlatKey is one key in oldest→newest order after nest verification.
// Oldest-first matches public_keys.predecessor_id FK insert order.
type recoveryFlatKey struct {
	Key                    recoveryKeyWire
	Revocation             *recoveryRevocation
	PredecessorFingerprint string // empty for the oldest key
	PredecessorSignature   string // older key's sig over this key's armor
}

// recoveryVerifier checks detached PGP signatures (armored). Implemented by
// *cryptoService.
type recoveryVerifier interface {
	verifySignature(message, signature, publicKey string) error
	verifySignedChallenge(signature, publicKey, challenge string) error
}

// recoveryServerKeyLookup returns the armored public half of a historical
// server signing key, or "" if unknown.
type recoveryServerKeyLookup func(ctx context.Context, fingerprint string) (armor string, err error)

// flattenKeysNest walks the nest outermost→oldest, verifying each key
// (and its predecessor link / optional revocation) as soon as it is
// visited, then returns the active (outermost) key and the full chain
// oldest→newest for FK-safe insert. serverID must match
// profile.serverSignature.serverID. Any failure aborts with no partial write.
func flattenKeysNest(
	ctx context.Context,
	profile recoveryProfile,
	root recoveryKeyNode,
	serverID string,
	lookup recoveryServerKeyLookup,
	v recoveryVerifier,
) (active recoveryFlatKey, flat []recoveryFlatKey, err error) {
	if profile.ID == "" || profile.Username == "" {
		return recoveryFlatKey{}, nil, fmt.Errorf("profile id and username are required")
	}
	if err := validateProfileRole(profile.ID, profile.Role, serverID); err != nil {
		return recoveryFlatKey{}, nil, err
	}
	if profile.ServerSignature.ServerID != serverID {
		return recoveryFlatKey{}, nil, fmt.Errorf("profile server id mismatch")
	}
	if profile.UserSignature.KeyID == "" || profile.UserSignature.Armor == "" {
		return recoveryFlatKey{}, nil, fmt.Errorf("profile signature is required")
	}

	byFP := make(map[string]recoveryKeyWire)
	var newestFirst []recoveryFlatKey
	var parent *recoveryKeyNode

	for n := &root; n != nil; n = n.Predecessor {
		if n.Fingerprint == "" || n.Armor == "" {
			return recoveryFlatKey{}, nil, fmt.Errorf("incomplete nest: key missing fingerprint or armor")
		}
		if n.UserID != "" && n.UserID != profile.ID {
			return recoveryFlatKey{}, nil, fmt.Errorf("key %s userID does not match profile", n.Fingerprint)
		}
		if _, dup := byFP[n.Fingerprint]; dup {
			return recoveryFlatKey{}, nil, fmt.Errorf("duplicate fingerprint in nest: %s", n.Fingerprint)
		}

		if parent != nil {
			// Arrived at older key n, predecessor of parent. n.Signature is
			// n's detached sig over parent.Armor — verify before going deeper.
			if n.Signature == "" {
				return recoveryFlatKey{}, nil, fmt.Errorf("broken predecessor link: missing signature for key %s", parent.Fingerprint)
			}
			if err := v.verifySignedChallenge(n.Signature, n.Armor, parent.Armor); err != nil {
				return recoveryFlatKey{}, nil, fmt.Errorf("broken predecessor signature for %s: %w", parent.Fingerprint, err)
			}
			newestFirst[len(newestFirst)-1].PredecessorFingerprint = n.Fingerprint
			newestFirst[len(newestFirst)-1].PredecessorSignature = n.Signature
		}

		if err := verifyRecoveryKeyCountersig(ctx, n.recoveryKeyWire, profile.ID, serverID, lookup, v); err != nil {
			return recoveryFlatKey{}, nil, fmt.Errorf("key %s: %w", n.Fingerprint, err)
		}
		if n.Revocation != nil {
			if err := verifyRecoveryRevocation(ctx, n.Revocation, n.recoveryKeyWire, profile.ID, serverID, lookup, v); err != nil {
				return recoveryFlatKey{}, nil, fmt.Errorf("revocation for %s: %w", n.Fingerprint, err)
			}
		}

		byFP[n.Fingerprint] = n.recoveryKeyWire
		newestFirst = append(newestFirst, recoveryFlatKey{Key: n.recoveryKeyWire, Revocation: n.Revocation})
		parent = n
	}

	if len(newestFirst) == 0 {
		return recoveryFlatKey{}, nil, fmt.Errorf("empty key nest")
	}
	active = newestFirst[0]

	// byFP is keyed by bare fingerprint (recoveryKeyWire.Fingerprint);
	// profile's is the full canonical key id — extract the bare part to
	// look it up.
	_, _, signerFP, ok := parseKeyFingerprint(identityID(profile.UserSignature.KeyID))
	if !ok {
		return recoveryFlatKey{}, nil, fmt.Errorf("profile userSignature.id is not a canonical key id")
	}
	signer, ok := byFP[signerFP]
	if !ok {
		return recoveryFlatKey{}, nil, fmt.Errorf("profile signatureFingerprint not in nest")
	}

	userPayload := buildUserIdentityPayload(
		profile.Username,
		profile.UserSignature.KeyID,
		profile.Bio,
	)
	userSigArmor, err := decodeRecoveryB64Armor(profile.UserSignature.Armor)
	if err != nil {
		return recoveryFlatKey{}, nil, fmt.Errorf("profile user signature: %w", err)
	}
	if err := v.verifySignature(string(userPayload), userSigArmor, signer.Armor); err != nil {
		return recoveryFlatKey{}, nil, fmt.Errorf("profile user signature: %w", err)
	}

	if err := verifyProfileServerCountersig(ctx, profile, serverID, lookup, v); err != nil {
		return recoveryFlatKey{}, nil, err
	}

	// Oldest → newest for public_keys.predecessor_id inserts.
	flat = make([]recoveryFlatKey, 0, len(newestFirst))
	for i := len(newestFirst) - 1; i >= 0; i-- {
		flat = append(flat, newestFirst[i])
	}
	return active, flat, nil
}

func recoveryProfileInviteID(profile recoveryProfile) string {
	if profile.Invite == nil {
		return ""
	}
	return profile.Invite.ID
}

// verifyProfileServerCountersig checks profile.serverSignature.serverID
// against serverID and verifies the server countersignature. It does not
// verify the user signature or key nest (those are claim/peer concerns).
func verifyProfileServerCountersig(ctx context.Context, profile recoveryProfile, serverID string, lookup recoveryServerKeyLookup, v recoveryVerifier) error {
	if profile.ServerSignature.ServerID != serverID {
		return fmt.Errorf("profile server id mismatch")
	}
	if profile.ServerSignature.Fingerprint == "" || profile.ServerSignature.Armor == "" || profile.ServerSignature.Timestamp.IsZero() {
		return fmt.Errorf("missing server countersignature")
	}
	serverPub, err := lookup(ctx, profile.ServerSignature.Fingerprint)
	if err != nil {
		return err
	}
	if serverPub == "" {
		return fmt.Errorf("unknown server key %s for profile", profile.ServerSignature.Fingerprint)
	}
	profilePayload := buildProfilePayload(
		profile.ID,
		profile.Username,
		profile.UserSignature.KeyID,
		serverID,
		profile.ServerSignature.Fingerprint,
		profile.UserSignature.Armor,
		recoveryProfileInviteID(profile),
		profile.Role,
		profile.Bio,
		profile.MemberSince.UTC().Truncate(time.Second),
		profile.ServerSignature.Timestamp.UTC().Truncate(time.Second),
	)
	serverSigArmor, err := decodeRecoveryB64Armor(profile.ServerSignature.Armor)
	if err != nil {
		return fmt.Errorf("profile server signature: %w", err)
	}
	if err := v.verifySignature(string(profilePayload), serverSigArmor, serverPub); err != nil {
		return fmt.Errorf("profile server signature: %w", err)
	}
	return nil
}

func verifyRecoveryKeyCountersig(ctx context.Context, key recoveryKeyWire, userID, serverID string, lookup recoveryServerKeyLookup, v recoveryVerifier) error {
	if key.ServerSignature.ServerID != "" && key.ServerSignature.ServerID != serverID {
		return fmt.Errorf("server id mismatch")
	}
	if key.ServerSignature.Fingerprint == "" || key.ServerSignature.Armor == "" {
		return fmt.Errorf("missing server countersignature")
	}
	serverPub, err := lookup(ctx, key.ServerSignature.Fingerprint)
	if err != nil {
		return err
	}
	if serverPub == "" {
		return fmt.Errorf("unknown server key %s", key.ServerSignature.Fingerprint)
	}
	payload := buildPublicKeyPayload(
		serverID,
		userID,
		key.Fingerprint,
		key.ServerSignature.Fingerprint,
		key.Armor,
		key.ServerSignature.Timestamp.UTC().Truncate(time.Second),
	)
	sigArmor, err := decodeRecoveryB64Armor(key.ServerSignature.Armor)
	if err != nil {
		return err
	}
	return v.verifySignature(string(payload), sigArmor, serverPub)
}

func verifyRecoveryRevocation(ctx context.Context, rev *recoveryRevocation, key recoveryKeyWire, userID, serverID string, lookup recoveryServerKeyLookup, v recoveryVerifier) error {
	if rev.Fingerprint != key.Fingerprint {
		return fmt.Errorf("fingerprint mismatch")
	}
	if rev.UserID != "" && rev.UserID != userID {
		return fmt.Errorf("userID mismatch")
	}
	if rev.ServerSignature.ServerID != "" && rev.ServerSignature.ServerID != serverID {
		return fmt.Errorf("server id mismatch")
	}
	userPayload := buildUserRevocationPayload(userID, rev.Fingerprint, rev.Reason)
	userSigArmor, err := decodeRecoveryB64Armor(rev.UserSignature.Armor)
	if err != nil {
		return err
	}
	if err := v.verifySignature(string(userPayload), userSigArmor, key.Armor); err != nil {
		return fmt.Errorf("user signature: %w", err)
	}
	serverPub, err := lookup(ctx, rev.ServerSignature.Fingerprint)
	if err != nil {
		return err
	}
	if serverPub == "" {
		return fmt.Errorf("unknown server key %s", rev.ServerSignature.Fingerprint)
	}
	serverPayload := buildServerRevocationPayload(
		userID,
		rev.Fingerprint,
		rev.Reason,
		serverID,
		rev.ServerSignature.Fingerprint,
		rev.UserSignature.Armor,
		rev.ServerSignature.Timestamp.UTC().Truncate(time.Second),
	)
	serverSigArmor, err := decodeRecoveryB64Armor(rev.ServerSignature.Armor)
	if err != nil {
		return err
	}
	return v.verifySignature(string(serverPayload), serverSigArmor, serverPub)
}

func decodeRecoveryB64Armor(s string) (string, error) {
	raw, err := base64Decode(s)
	if err != nil {
		return "", fmt.Errorf("invalid base64 encoding")
	}
	return raw, nil
}

// decodeRecoveryKeyNestArmor walks a key nest outermost→oldest, decoding
// each recoveryKeyWire.Armor from base64 to plain armor in place. Must run
// once, right after JSON-decoding the request, before flattenKeysNest or any
// crypto use — every downstream consumer (signature verification, payload
// signing) expects plain armor, matching how flat signature fields already
// work.
func decodeRecoveryKeyNestArmor(root *recoveryKeyNode) error {
	for n := root; n != nil; n = n.Predecessor {
		armor, err := decodeRecoveryB64Armor(n.Armor)
		if err != nil {
			return fmt.Errorf("key %s: invalid armor encoding", n.Fingerprint)
		}
		n.Armor = armor
	}
	return nil
}

// validateChallengeAge rejects challenges in the future or older than maxAge.
func validateChallengeAge(challenge int64, now time.Time, maxAge time.Duration) error {
	nowUnix := now.UTC().Unix()
	if challenge > nowUnix {
		return fmt.Errorf("challenge is in the future")
	}
	if nowUnix-challenge > int64(maxAge.Seconds()) {
		return fmt.Errorf("challenge is stale")
	}
	return nil
}

// verifyChallengeSignature checks a base64(armored) detached sig over the
// decimal challenge string using the outermost public key.
func verifyChallengeSignature(challenge int64, signatureB64, publicKeyArmor string, v recoveryVerifier) error {
	sigArmor, err := decodeRecoveryB64Armor(signatureB64)
	if err != nil {
		return fmt.Errorf("challenge signature: %w", err)
	}
	msg := strconv.FormatInt(challenge, 10)
	if err := v.verifySignature(msg, sigArmor, publicKeyArmor); err != nil {
		return fmt.Errorf("challenge signature: %w", err)
	}
	return nil
}
