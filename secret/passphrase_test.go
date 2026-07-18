package secret

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

type memKeyring struct {
	store map[string]string
	err   error // if set, Get/Set/Delete return this
}

func newMemKeyring() *memKeyring {
	return &memKeyring{store: make(map[string]string)}
}

func (m *memKeyring) key(service, user string) string {
	return service + "\x00" + user
}

func (m *memKeyring) Get(service, user string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	pw, ok := m.store[m.key(service, user)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return pw, nil
}

func (m *memKeyring) Set(service, user, password string) error {
	if m.err != nil {
		return m.err
	}
	m.store[m.key(service, user)] = password
	return nil
}

func (m *memKeyring) Delete(service, user string) error {
	if m.err != nil {
		return m.err
	}
	delete(m.store, m.key(service, user))
	return nil
}

func testResolver(envPassphrase, serverName string, kr Keyring) *Resolver {
	return &Resolver{
		EnvPassphrase: envPassphrase,
		ServerName:    serverName,
		Keyring:       kr,
		StdinFD:       0,
		IsTerminal:    func(int) bool { return false },
		ReadPassword:  func(int) ([]byte, error) { return nil, errors.New("unexpected prompt") },
		PromptWriter:  &bytes.Buffer{},
	}
}

func TestResolve_Env(t *testing.T) {
	kr := newMemKeyring()
	r := testResolver("sixteen-chars!!!", "myserver", kr)

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Source != SourceEnv {
		t.Fatalf("Source = %v, want SourceEnv", got.Source)
	}
	if got.Value != "sixteen-chars!!!" {
		t.Fatalf("Value = %q", got.Value)
	}
	if len(kr.store) != 0 {
		t.Fatalf("keychain written on env path: %v", kr.store)
	}
}

func TestResolve_EnvTooShort(t *testing.T) {
	r := testResolver("short", "myserver", newMemKeyring())
	_, err := r.Resolve()
	if !errors.Is(err, ErrTooShort) {
		t.Fatalf("err = %v, want ErrTooShort", err)
	}
}

func TestResolve_Keychain(t *testing.T) {
	kr := newMemKeyring()
	_ = kr.Set(keyringService, "server-key-passphrase:myserver", "from-keychain-xx")
	r := testResolver("", "myserver", kr)

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Source != SourceKeychain {
		t.Fatalf("Source = %v, want SourceKeychain", got.Source)
	}
	if got.Value != "from-keychain-xx" {
		t.Fatalf("Value = %q", got.Value)
	}
}

func TestResolve_PromptStoresInKeychain(t *testing.T) {
	kr := newMemKeyring()
	var prompt bytes.Buffer
	r := &Resolver{
		EnvPassphrase: "",
		ServerName:    "myserver",
		Keyring:       kr,
		StdinFD:       0,
		IsTerminal:    func(int) bool { return true },
		ReadPassword:  func(int) ([]byte, error) { return []byte("prompted-pass-16"), nil },
		PromptWriter:  &prompt,
	}

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Source != SourcePrompt {
		t.Fatalf("Source = %v, want SourcePrompt", got.Source)
	}
	if got.Value != "prompted-pass-16" {
		t.Fatalf("Value = %q", got.Value)
	}
	stored, err := kr.Get(keyringService, "server-key-passphrase:myserver")
	if err != nil || stored != "prompted-pass-16" {
		t.Fatalf("keychain = %q, err = %v", stored, err)
	}
	if !strings.Contains(prompt.String(), "Enter server key passphrase") {
		t.Fatalf("prompt missing: %q", prompt.String())
	}
}

func TestResolve_EmptyPromptGenerates(t *testing.T) {
	kr := newMemKeyring()
	var out bytes.Buffer
	r := &Resolver{
		EnvPassphrase: "",
		ServerName:    "myserver",
		Keyring:       kr,
		IsTerminal:    func(int) bool { return true },
		ReadPassword:  func(int) ([]byte, error) { return []byte(""), nil },
		PromptWriter:  &bytes.Buffer{},
		OutputWriter:  &out,
		GeneratePassphrase: func() (string, error) {
			return "auto-generated-pass-24", nil
		},
	}

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Source != SourceGenerated {
		t.Fatalf("Source = %v, want SourceGenerated", got.Source)
	}
	if got.Value != "auto-generated-pass-24" {
		t.Fatalf("Value = %q", got.Value)
	}
	if !strings.Contains(out.String(), "auto-generated-pass-24") {
		t.Fatalf("stdout missing generated passphrase: %q", out.String())
	}
	stored, err := kr.Get(keyringService, "server-key-passphrase:myserver")
	if err != nil || stored != "auto-generated-pass-24" {
		t.Fatalf("keychain = %q, err = %v", stored, err)
	}
}

func TestGeneratePassphrase_Length(t *testing.T) {
	pw, err := GeneratePassphrase()
	if err != nil {
		t.Fatalf("GeneratePassphrase: %v", err)
	}
	if len(pw) != GeneratedPassphraseLen {
		t.Fatalf("len = %d, want %d", len(pw), GeneratedPassphraseLen)
	}
	for _, c := range pw {
		if !strings.ContainsRune(passphraseAlphabet, c) {
			t.Fatalf("unexpected char %q in %q", c, pw)
		}
	}
}

func TestResolve_NonTTYFailClosed(t *testing.T) {
	r := testResolver("", "myserver", newMemKeyring())
	_, err := r.Resolve()
	if !errors.Is(err, ErrNotProvided) {
		t.Fatalf("err = %v, want ErrNotProvided", err)
	}
}

func TestResolve_PromptTooShort(t *testing.T) {
	r := &Resolver{
		EnvPassphrase: "",
		ServerName:    "myserver",
		Keyring:       newMemKeyring(),
		IsTerminal:    func(int) bool { return true },
		ReadPassword:  func(int) ([]byte, error) { return []byte("short"), nil },
		PromptWriter:  &bytes.Buffer{},
	}
	_, err := r.Resolve()
	if !errors.Is(err, ErrTooShort) {
		t.Fatalf("err = %v, want ErrTooShort", err)
	}
}

func TestStore_UpdatesKeychain(t *testing.T) {
	kr := newMemKeyring()
	r := testResolver("", "myserver", kr)
	if err := r.Store("rotated-pass-16x"); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := kr.Get(keyringService, "server-key-passphrase:myserver")
	if err != nil || got != "rotated-pass-16x" {
		t.Fatalf("keychain = %q, err = %v", got, err)
	}
}

func TestStore_EnvManaged(t *testing.T) {
	kr := newMemKeyring()
	r := testResolver("sixteen-chars!!!", "myserver", kr)
	err := r.Store("rotated-pass-16x")
	if !errors.Is(err, ErrEnvManaged) {
		t.Fatalf("err = %v, want ErrEnvManaged", err)
	}
	if len(kr.store) != 0 {
		t.Fatalf("keychain written: %v", kr.store)
	}
}

func TestAccount_Unscoped(t *testing.T) {
	r := testResolver("", "", newMemKeyring())
	if got := r.account(); got != "server-key-passphrase" {
		t.Fatalf("account = %q", got)
	}
}
