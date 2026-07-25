// Package recovery implements server identity backup/restore and (later)
// takeover-recovery endpoints.
package recovery

import (
	"bytes"
	"fmt"
	"io"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// EncryptSymmetric returns an ASCII-armored OpenPGP message encrypting
// plaintext with password (gpg -c style). The password is never stored.
func EncryptSymmetric(plaintext []byte, password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("bundle password must not be empty")
	}

	var out bytes.Buffer
	armorWriter, err := armor.Encode(&out, "PGP MESSAGE", nil)
	if err != nil {
		return "", fmt.Errorf("armor encode: %w", err)
	}

	// Non-nil config required: SymmetricallyEncrypt dereferences AEAD() when
	// deciding cipher suite; a nil *Config panics.
	config := &packet.Config{}
	plaintextWriter, err := openpgp.SymmetricallyEncrypt(armorWriter, []byte(password), nil, config)
	if err != nil {
		return "", fmt.Errorf("symmetric encrypt: %w", err)
	}
	if _, err := plaintextWriter.Write(plaintext); err != nil {
		_ = plaintextWriter.Close()
		return "", fmt.Errorf("write plaintext: %w", err)
	}
	if err := plaintextWriter.Close(); err != nil {
		return "", fmt.Errorf("close plaintext: %w", err)
	}
	if err := armorWriter.Close(); err != nil {
		return "", fmt.Errorf("close armor: %w", err)
	}
	return out.String(), nil
}

// DecryptSymmetric decrypts an ASCII-armored OpenPGP message produced by
// EncryptSymmetric. Wrong passwords fail closed without returning plaintext.
func DecryptSymmetric(armoredCiphertext, password string) ([]byte, error) {
	if password == "" {
		return nil, fmt.Errorf("bundle password must not be empty")
	}

	block, err := armor.Decode(bytes.NewReader([]byte(armoredCiphertext)))
	if err != nil {
		return nil, fmt.Errorf("armor decode: %w", err)
	}

	tried := false
	md, err := openpgp.ReadMessage(block.Body, nil, func(_ []openpgp.Key, _ bool) ([]byte, error) {
		if tried {
			return nil, fmt.Errorf("wrong password")
		}
		tried = true
		return []byte(password), nil
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed")
	}

	plain, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed")
	}
	return plain, nil
}
