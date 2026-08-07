//go:build ops

// Operator CLI for server identity backup/restore and passphrase rotation.
//
// Before running, copy .env.example to .env, fill in DB_* and SERVER_NAME, then
// `source .env` in your shell (this binary reads the process environment; it
// does not load .env itself).
//
// Build: go build -tags ops -o bin/ops .
package main

import (
	"context"
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"syrinx/crypto"
	"syrinx/recovery"
	"syrinx/secret"

	_ "github.com/lib/pq"
	"github.com/tooxie/env"
	"golang.org/x/term"
)

type opsConfig struct {
	DBHost     string `env:"name='DB_HOST'"`
	DBPort     string `env:"name='DB_PORT'"`
	DBUser     string `env:"name='DB_USER'"`
	DBPassword string `env:"name='DB_PASSWORD'"`
	DBName     string `env:"name='DB_NAME'"`
	DBSSLMode  string `env:"name='DB_SSLMODE'"`

	ServerName          string `env:"name='SERVER_NAME'"`
	ServerKeyPassphrase string `env:"name='SERVER_KEY_PASSPHRASE'"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "export-identity":
		outfile := ""
		if len(os.Args) > 2 {
			outfile = os.Args[2]
		}
		if err := runExportIdentity(outfile); err != nil {
			fail(err)
		}
	case "import-identity":
		if len(os.Args) < 3 {
			fail(fmt.Errorf("usage: ops import-identity <infile>"))
		}
		if err := runImportIdentity(os.Args[2]); err != nil {
			fail(err)
		}
	case "rotate-passphrase":
		if err := runRotatePassphrase(); err != nil {
			fail(err)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: ops <command> [args]

Commands:
  export-identity [outfile]
      Export the server identity (ID + full signing-key history) as an
      OpenPGP-encrypted file. Prompts for a bundle password (never stored).
      Default outfile: syrinx-<serverID>-<YYYYMMDDTHHMMSSZ>.sxi.gpg

  import-identity <infile>
      Restore identity from an encrypted bundle before first server boot.
      Calls InitDB (full schema), prompts for bundle password, resolves the
      server key passphrase (not in the bundle), then writes identity.
      Reminds you to start the server with RECOVERY_MODE.

  rotate-passphrase
      Re-wrap private_keys under a new server key passphrase, update the
      OS keychain when not using SERVER_KEY_PASSPHRASE, and remind you to
      re-export the identity bundle.

  help
      Show this message.

Before running any command, copy .env.example to .env, set the variables
accordingly, then source it:
	$ source .env
`)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func loadOpsConfig() opsConfig {
	var c opsConfig
	return env.MustAssert(c)
}

func openDB(cfg opsConfig) (*sql.DB, error) {
	if cfg.ServerName == "" {
		return nil, fmt.Errorf("SERVER_NAME is required")
	}
	url := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBSSLMode)
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func runExportIdentity(outfile string) error {
	cfg := loadOpsConfig()
	db, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	exportedAt := time.Now().UTC().Truncate(time.Second)
	bundle, err := recovery.ExportFromDB(context.Background(), db, exportedAt)
	if err != nil {
		return err
	}

	password, err := promptBundlePassword()
	if err != nil {
		return err
	}

	raw, err := recovery.MarshalBundleJSON(bundle)
	if err != nil {
		return err
	}
	armored, err := recovery.EncryptSymmetric(raw, password)
	if err != nil {
		return err
	}

	if outfile == "" {
		outfile = recovery.DefaultExportFilename(bundle.ServerID, bundle.ExportedAt)
	}
	if err := os.WriteFile(outfile, []byte(armored), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", outfile, err)
	}

	if err := recovery.SetIdentityBackupAt(context.Background(), db, bundle.ExportedAt); err != nil {
		return fmt.Errorf("wrote %s but failed to update identity_backup_at: %w", outfile, err)
	}

	fmt.Printf("Wrote encrypted identity bundle to %s\n", outfile)
	fmt.Printf("Recorded identity_backup_at=%s\n", bundle.ExportedAt.Format(time.RFC3339))
	return nil
}

func runImportIdentity(infile string) error {
	cfg := loadOpsConfig()
	db, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := InitDB(db); err != nil {
		return fmt.Errorf("InitDB: %w", err)
	}

	armored, err := os.ReadFile(infile)
	if err != nil {
		return fmt.Errorf("read %s: %w", infile, err)
	}

	password, err := promptBundlePassword()
	if err != nil {
		return err
	}
	plain, err := recovery.DecryptSymmetric(string(armored), password)
	if err != nil {
		return fmt.Errorf("decrypt bundle: %w", err)
	}
	bundle, err := recovery.ParseBundleJSON(plain)
	if err != nil {
		return err
	}

	if cfg.ServerName != bundle.ServerName {
		fmt.Fprintf(os.Stderr, "warning: SERVER_NAME=%q differs from bundle serverName=%q (import will use the bundle name; boot may rename later)\n",
			cfg.ServerName, bundle.ServerName)
	}

	resolver := secret.NewResolver(cfg.ServerKeyPassphrase, cfg.ServerName)
	passphrase, err := resolver.Resolve()
	if err != nil {
		return fmt.Errorf("resolve server key passphrase: %w", err)
	}

	cryptoSvc := crypto.NewService()
	if err := recovery.ValidateDecrypt(bundle, cryptoSvc, passphrase.Value); err != nil {
		return err
	}

	result, err := recovery.ImportIntoDB(context.Background(), db, bundle)
	if err != nil {
		return err
	}
	switch result {
	case recovery.ImportAlreadyPresent:
		fmt.Println("Identity already present and matches the bundle (no changes).")
	case recovery.ImportApplied:
		fmt.Printf("Imported identity serverID=%s serverName=%s keys=%d\n",
			bundle.ServerID, bundle.ServerName, len(bundle.Keys))
		fmt.Printf("Recorded identity_backup_at=%s\n", bundle.ExportedAt.UTC().Format(time.RFC3339))
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Next: start the server with RECOVERY_MODE enabled.")
	fmt.Fprintln(os.Stderr, "Do not boot in normal mode until recovery is complete — that would mint a new identity.")

	if err := maybeDeleteBundle(infile); err != nil {
		return err
	}
	return nil
}

func runRotatePassphrase() error {
	cfg := loadOpsConfig()
	db, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	resolver := secret.NewResolver(cfg.ServerKeyPassphrase, cfg.ServerName)
	current, err := resolver.Resolve()
	if err != nil {
		return fmt.Errorf("resolve current passphrase: %w", err)
	}

	newPass, err := promptNewServerKeyPassphrase()
	if err != nil {
		return err
	}

	cryptoSvc := crypto.NewService()
	if err := recovery.RotateServerKeyPassphrase(context.Background(), db, cryptoSvc, current.Value, newPass); err != nil {
		return err
	}

	if err := resolver.Store(newPass); err != nil {
		if errors.Is(err, secret.ErrEnvManaged) {
			fmt.Fprintln(os.Stderr, "Passphrase re-wrapped in DB.")
			fmt.Fprintln(os.Stderr, "SERVER_KEY_PASSPHRASE is set — update the injected secret yourself; keychain was not changed.")
		} else {
			return fmt.Errorf("re-wrapped DB keys but failed to update keychain: %w", err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "Passphrase re-wrapped in DB and OS keychain updated.")
	}

	fmt.Fprintln(os.Stderr, "Re-export the identity bundle now (bundle password will be prompted again):")
	fmt.Fprintln(os.Stderr, "  ops export-identity")
	return nil
}

func promptBundlePassword() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("stdin is not a TTY; cannot prompt for bundle password")
	}
	fmt.Fprint(os.Stderr, "Enter bundle password: ")
	a, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(a) == "" {
		return "", fmt.Errorf("bundle password must not be empty")
	}
	pw := string(a)
	if msg := recovery.PasswordStrengthWarning(pw); msg != "" {
		fmt.Fprintf(os.Stderr, "warning: %s — accepting anyway\n", msg)
	}
	return pw, nil
}

func promptNewServerKeyPassphrase() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("stdin is not a TTY; cannot prompt for new passphrase")
	}
	fmt.Fprint(os.Stderr, "Enter new server key passphrase (≥16 characters, empty to auto-generate): ")
	a, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	pw := strings.TrimSpace(string(a))
	if pw == "" {
		pw, err = secret.GeneratePassphrase()
		if err != nil {
			return "", err
		}
		fmt.Fprintf(os.Stdout, "Generated server key passphrase: %s\n", pw)
		return pw, nil
	}
	if len(pw) < secret.MinPassphraseLen {
		return "", secret.ErrTooShort
	}
	fmt.Fprint(os.Stderr, "Confirm new server key passphrase: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if pw != string(b) {
		return "", fmt.Errorf("passphrases do not match")
	}
	return pw, nil
}

func maybeDeleteBundle(path string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil
	}
	fmt.Fprintf(os.Stderr, "Delete encrypted bundle file %s? [y/N] ", path)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return err
	}
	ans := strings.TrimSpace(strings.ToLower(line))
	if ans != "y" && ans != "yes" {
		fmt.Fprintln(os.Stderr, "Keeping bundle file.")
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "Deleted %s\n", path)
	return nil
}
