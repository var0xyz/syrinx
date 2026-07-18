// Command ops is the Syrinx operator CLI for server identity backup and
// passphrase rotation. See docs/proposals/recovery/01_key_bundle_export_ops_cli.md.
package main

import (
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

type config struct {
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
      Default outfile: syrinx-<serverID>-<YYYYMMDDTHHMMSSZ>.json.gpg

  rotate-passphrase
      Re-wrap private_keys under a new server key passphrase, update the
      OS keychain when not using SERVER_KEY_PASSPHRASE, and remind you to
      re-export the identity bundle.

  help
      Show this message.

Environment: same DB_* and SERVER_NAME as the server process. Optional
SERVER_KEY_PASSPHRASE is the HA escape hatch for the server key passphrase;
otherwise the OS keychain / interactive prompt from syrinx/secret is used.
`)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func loadConfig() config {
	var c config
	return env.MustAssert(c)
}

func openDB(cfg config) (*sql.DB, error) {
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
	cfg := loadConfig()
	db, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	exportedAt := time.Now().UTC().Truncate(time.Second)
	bundle, err := recovery.ExportFromDB(db, exportedAt)
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

	if err := recovery.SetIdentityBackupAt(db, bundle.ExportedAt); err != nil {
		return fmt.Errorf("wrote %s but failed to update identity_backup_at: %w", outfile, err)
	}

	fmt.Printf("Wrote encrypted identity bundle to %s\n", outfile)
	fmt.Printf("Recorded identity_backup_at=%s\n", bundle.ExportedAt.Format(time.RFC3339))
	return nil
}

func runRotatePassphrase() error {
	cfg := loadConfig()
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
	if err := recovery.RotateServerKeyPassphrase(db, cryptoSvc, current.Value, newPass); err != nil {
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
	fmt.Fprint(os.Stderr, "Confirm bundle password: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(a) != string(b) {
		return "", fmt.Errorf("passwords do not match")
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
