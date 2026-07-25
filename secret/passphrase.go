// Package secret resolves and stores the server key passphrase.
//
// Resolution order:
//  1. non-empty SERVER_KEY_PASSPHRASE (HA escape hatch; never written to keychain)
//  2. OS keychain / secret store
//  3. interactive TTY prompt → store in keychain
//  4. otherwise fail closed
package secret

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

const (
	// MinPassphraseLen is the minimum accepted server key passphrase length.
	MinPassphraseLen = 16

	// GeneratedPassphraseLen is the length of an auto-generated passphrase
	// when the operator presses Enter at the prompt with an empty input.
	GeneratedPassphraseLen = 24

	keyringService     = "syrinx"
	keyringAccountBase = "server-key-passphrase"

	// passphraseAlphabet is URL-safe and shell-friendly (no quotes/spaces).
	passphraseAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)

var (
	// ErrNotProvided is returned when neither env nor keychain has a passphrase
	// and stdin is not a TTY (so prompting is impossible).
	ErrNotProvided = errors.New("server key passphrase not provided: set SERVER_KEY_PASSPHRASE or run interactively to store it in the OS keychain")

	// ErrTooShort is returned when a candidate passphrase is shorter than MinPassphraseLen.
	ErrTooShort = fmt.Errorf("server key passphrase must be at least %d characters", MinPassphraseLen)

	// ErrEnvManaged is returned by Store when the process is using the env var
	// (HA path). Operators must update the injected secret themselves.
	ErrEnvManaged = errors.New("passphrase is managed via SERVER_KEY_PASSPHRASE; update the injected secret instead of the keychain")
)

// Source identifies where Resolve obtained the passphrase.
type Source int

const (
	SourceNone Source = iota
	SourceEnv
	SourceKeychain
	SourcePrompt
	SourceGenerated
)

// Passphrase is the resolved server key passphrase and its origin.
type Passphrase struct {
	Value  string
	Source Source
}

// Keyring abstracts the OS secret store for tests.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type osKeyring struct{}

func (osKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (osKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// Resolver resolves the server key passphrase for boot and ops commands.
type Resolver struct {
	// EnvPassphrase is the value of SERVER_KEY_PASSPHRASE (empty means unset).
	EnvPassphrase string
	// ServerName scopes the keychain account when non-empty.
	ServerName string
	// Keyring defaults to the OS keychain when nil.
	Keyring Keyring
	// StdinFD is the file descriptor used for TTY checks and password reads.
	StdinFD int
	// IsTerminal reports whether fd is a terminal. Defaults to term.IsTerminal.
	IsTerminal func(fd int) bool
	// ReadPassword reads a hidden line from fd. Defaults to term.ReadPassword.
	ReadPassword func(fd int) ([]byte, error)
	// PromptWriter receives the interactive prompt. Defaults to os.Stderr.
	PromptWriter io.Writer
	// OutputWriter receives the auto-generated passphrase. Defaults to os.Stdout.
	OutputWriter io.Writer
	// GeneratePassphrase produces a random passphrase. Defaults to GeneratePassphrase.
	GeneratePassphrase func() (string, error)
}

// NewResolver returns a Resolver wired to the OS keychain and stdin.
func NewResolver(envPassphrase, serverName string) *Resolver {
	return &Resolver{
		EnvPassphrase:      envPassphrase,
		ServerName:         serverName,
		Keyring:            osKeyring{},
		StdinFD:            int(os.Stdin.Fd()),
		IsTerminal:         term.IsTerminal,
		ReadPassword:       term.ReadPassword,
		PromptWriter:       os.Stderr,
		OutputWriter:       os.Stdout,
		GeneratePassphrase: GeneratePassphrase,
	}
}

func (r *Resolver) account() string {
	if r.ServerName == "" {
		return keyringAccountBase
	}
	return keyringAccountBase + ":" + r.ServerName
}

func (r *Resolver) keyring() Keyring {
	if r.Keyring != nil {
		return r.Keyring
	}
	return osKeyring{}
}

// Resolve returns the server key passphrase using env → keychain → prompt.
func (r *Resolver) Resolve() (Passphrase, error) {
	if r.EnvPassphrase != "" {
		if len(r.EnvPassphrase) < MinPassphraseLen {
			return Passphrase{}, ErrTooShort
		}
		return Passphrase{Value: r.EnvPassphrase, Source: SourceEnv}, nil
	}

	kr := r.keyring()
	pw, err := kr.Get(keyringService, r.account())
	if err == nil && pw != "" {
		if len(pw) < MinPassphraseLen {
			return Passphrase{}, fmt.Errorf("keychain: %w", ErrTooShort)
		}
		return Passphrase{Value: pw, Source: SourceKeychain}, nil
	}
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return Passphrase{}, fmt.Errorf("keychain lookup: %w", err)
	}

	isTTY := r.IsTerminal != nil && r.IsTerminal(r.StdinFD)
	if !isTTY {
		return Passphrase{}, ErrNotProvided
	}

	if r.PromptWriter == nil {
		r.PromptWriter = os.Stderr
	}
	fmt.Fprint(r.PromptWriter, "Enter server key passphrase (≥16 characters, empty to auto-generate): ")
	b, err := r.ReadPassword(r.StdinFD)
	fmt.Fprintln(r.PromptWriter)
	if err != nil {
		return Passphrase{}, fmt.Errorf("reading passphrase: %w", err)
	}
	pw = strings.TrimSpace(string(b))
	source := SourcePrompt
	if pw == "" {
		pw, err = r.generate()
		if err != nil {
			return Passphrase{}, fmt.Errorf("generating passphrase: %w", err)
		}
		source = SourceGenerated
		if r.OutputWriter == nil {
			r.OutputWriter = os.Stdout
		}
		fmt.Fprintf(r.OutputWriter, "Generated server key passphrase: %s\n", pw)
	} else if len(pw) < MinPassphraseLen {
		return Passphrase{}, ErrTooShort
	}

	if err := kr.Set(keyringService, r.account(), pw); err != nil {
		return Passphrase{}, fmt.Errorf("storing passphrase in keychain: %w", err)
	}
	return Passphrase{Value: pw, Source: source}, nil
}

func (r *Resolver) generate() (string, error) {
	if r.GeneratePassphrase != nil {
		return r.GeneratePassphrase()
	}
	return GeneratePassphrase()
}

// GeneratePassphrase returns a cryptographically random passphrase of
// GeneratedPassphraseLen characters from passphraseAlphabet.
func GeneratePassphrase() (string, error) {
	buf := make([]byte, GeneratedPassphraseLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	n := byte(len(passphraseAlphabet))
	for i := range buf {
		buf[i] = passphraseAlphabet[buf[i]%n]
	}
	return string(buf), nil
}

// Store writes passphrase to the OS keychain. Used by rotate-passphrase when
// the process is not using the env HA path. Returns ErrEnvManaged when
// EnvPassphrase is set so callers can remind the operator to update the
// injected secret.
func (r *Resolver) Store(passphrase string) error {
	if r.EnvPassphrase != "" {
		return ErrEnvManaged
	}
	if len(passphrase) < MinPassphraseLen {
		return ErrTooShort
	}
	if err := r.keyring().Set(keyringService, r.account(), passphrase); err != nil {
		return fmt.Errorf("storing passphrase in keychain: %w", err)
	}
	return nil
}
