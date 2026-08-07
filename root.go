//go:build !ops

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"syrinx/crypto"
	"syrinx/identity"
	"syrinx/invites"
	"syrinx/roles"

	"github.com/rs/zerolog/log"
)

const (
	rootUsername = "root"
)

type identityBackupPayload struct {
	Timestamp    int64             `json:"timestamp"`
	Origin       string            `json:"origin"`
	LocalStorage map[string]string `json:"localStorage"`
	IndexedDB    struct {
		Name   string              `json:"name"`
		Tables []identityBackupTbl `json:"tables"`
	} `json:"indexedDB"`
}

type identityBackupTbl struct {
	Name  string        `json:"name"`
	Items []interface{} `json:"items"`
}

type identityPrivateKeyItem struct {
	Fingerprint string    `json:"fingerprint"`
	Armor       string    `json:"armor"`
	CreatedAt   time.Time `json:"createdAt"`
	Revoked     bool      `json:"revoked"`
}

// maybeExportRootKey mints root when ROOT_KEY_EXPORT_PASSPHRASE is set.
// Returns exit=true after writing .sxi.gpg; otherwise normal startup continues.
func maybeExportRootKey(cfg AppConfig, db *DataService, cryptoSvc *crypto.Service, signingKey *Key) (exit bool, err error) {
	if cfg.RecoveryMode {
		return false, nil
	}

	passphrase := strings.TrimSpace(cfg.RootKeyExportPassphrase)
	if passphrase == "" {
		return false, nil
	}

	root, err := db.GetUserProfile(context.Background(), roles.RootUserID)
	if err != nil {
		return false, err
	}
	if root != nil {
		return false, fmt.Errorf("ROOT_KEY_EXPORT_PASSPHRASE is set but root user %q already exists — remove the env var and restart", roles.RootUserID)
	}

	outPath, err := exportRootIdentity(db, cryptoSvc, signingKey, passphrase, cfg.RootKeyExportPath, cfg.ServerName)
	if err != nil {
		return false, err
	}

	log.Info().Str("path", outPath).Msg("Root identity export written")
	fmt.Fprintf(os.Stderr, "\nWrote root identity export: %s\n", outPath)
	fmt.Fprintln(os.Stderr, "Remove ROOT_KEY_EXPORT_PASSPHRASE from the environment and restart the server.")
	return true, nil
}

// requireRootUser refuses normal startup when the reserved root row is missing.
// RECOVERY_MODE may run with an empty users table until clients report evidence;
// the one-shot mint path (maybeExportRootKey) runs before this check.
func requireRootUser(cfg AppConfig, db *DataService) error {
	if cfg.RecoveryMode {
		return nil
	}
	root, err := db.GetUserProfile(context.Background(), roles.RootUserID)
	if err != nil {
		return err
	}
	if root != nil {
		return nil
	}
	return fmt.Errorf(
		"no root user (id %q): set ROOT_KEY_EXPORT_PASSPHRASE, start once to mint root and write syrinx-1-….sxi.gpg, import that file via /import, then unset the env var and restart",
		roles.RootUserID,
	)
}

func exportRootIdentity(
	db *DataService,
	cryptoSvc *crypto.Service,
	signingKey *Key,
	exportPassphrase string,
	outDir string,
	serverName string,
) (string, error) {
	serverID := db.GetServerID()
	if serverID == "" {
		return "", fmt.Errorf("server id not initialized")
	}

	keyPassphrase := exportPassphrase
	openPGPName := roles.RootUserID + "@" + serverID

	kp, err := cryptoSvc.CreateKeyPair(openPGPName, "", serverName)
	if err != nil {
		return "", fmt.Errorf("generate root key: %w", err)
	}

	encryptedPrivate, err := cryptoSvc.EncryptPrivateKey(kp.PrivateKey, keyPassphrase)
	if err != nil {
		return "", fmt.Errorf("encrypt root private key: %w", err)
	}

	pubKeySig, err := cryptoSvc.Sign(kp.PublicKey, kp.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("sign root public key: %w", err)
	}

	keyMeta, err := cryptoSvc.ValidateAndExtractPublicKey(kp.PublicKey, pubKeySig)
	if err != nil {
		return "", fmt.Errorf("validate root public key: %w", err)
	}

	userPayload := identity.BuildUserIdentityPayload(rootUsername, keyMeta.Fingerprint, "")
	userSigArmor, err := cryptoSvc.Sign(string(userPayload), kp.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("sign root identity: %w", err)
	}
	userSigB64 := base64.StdEncoding.EncodeToString([]byte(userSigArmor))

	now := time.Now().UTC().Truncate(time.Second)

	profilePayload := identity.BuildNewProfilePayload(
		roles.RootUserID,
		rootUsername,
		keyMeta.Fingerprint,
		serverID,
		signingKey.Fingerprint,
		userSigB64,
		"",
		roles.RoleRoot,
		now,
	)
	profileSig, err := rootCountersign(cryptoSvc, db, signingKey, profilePayload, now)
	if err != nil {
		return "", err
	}

	keyPayload := identity.BuildPublicKeyPayload(
		serverID,
		roles.RootUserID,
		keyMeta.Fingerprint,
		signingKey.Fingerprint,
		kp.PublicKey,
		now,
	)
	keySig, err := rootCountersign(cryptoSvc, db, signingKey, keyPayload, now)
	if err != nil {
		return "", err
	}

	if _, err := db.Signup(context.Background(), SignupInput{
		UserID:             roles.RootUserID,
		Username:           rootUsername,
		PublicKeyArmor:     kp.PublicKey,
		Fingerprint:        keyMeta.Fingerprint,
		KeyCreatedAt:       keyMeta.CreatedAt,
		KeyExpiresAt:       keyMeta.ExpiresAt,
		UserSignatureB64:   userSigB64,
		MemberSince:        now,
		ProfileSignature:   profileSig,
		PublicKeySignature: keySig,
		SignupMode:         invites.ModeOpen,
	}); err != nil {
		return "", fmt.Errorf("persist root identity: %w", err)
	}

	wireKey, err := db.GetPublicKey(context.Background(), roles.RootUserID, keyMeta.Fingerprint)
	if err != nil || wireKey == nil {
		return "", fmt.Errorf("load root public key after signup: %w", err)
	}

	ts := time.Now().UnixMilli()
	payload := identityBackupPayload{
		Timestamp: ts,
		Origin:    "",
		LocalStorage: map[string]string{
			"userId":         roles.RootUserID,
			"keyFingerprint": keyMeta.Fingerprint,
			"keyPassphrase":  keyPassphrase,
			"serverId":       serverID,
			"serverName":     serverName,
		},
	}
	payload.IndexedDB.Name = "Syrinx"
	payload.IndexedDB.Tables = []identityBackupTbl{
		{
			Name: "privateKeys",
			Items: []interface{}{
				identityPrivateKeyItem{
					Fingerprint: keyMeta.Fingerprint,
					Armor:       encryptedPrivate,
					CreatedAt:   now,
					Revoked:     false,
				},
			},
		},
		{
			Name:  "publicKeys",
			Items: []interface{}{wireKey},
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw); err != nil {
		_ = zw.Close()
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}

	encrypted, err := cryptoSvc.EncryptSymmetric(gz.Bytes(), exportPassphrase)
	if err != nil {
		return "", err
	}

	filename := fmt.Sprintf("syrinx-%s-%d.sxi.gpg", roles.RootUserID, ts)
	outPath := filename
	if dir := strings.TrimSpace(outDir); dir != "" {
		outPath = filepath.Join(dir, filename)
	}
	if err := os.WriteFile(outPath, []byte(encrypted), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", outPath, err)
	}

	return outPath, nil
}

func rootCountersign(cryptoSvc *crypto.Service, db *DataService, signingKey *Key, payload []byte, ts time.Time) (ServerSignature, error) {
	sigArmor, err := cryptoSvc.Sign(string(payload), signingKey.Armor)
	if err != nil {
		return ServerSignature{}, err
	}
	return ServerSignature{
		ServerID:    db.GetServerID(),
		Fingerprint: signingKey.Fingerprint,
		Armor:       base64.StdEncoding.EncodeToString([]byte(sigArmor)),
		SignedAt:    ts,
	}, nil
}
