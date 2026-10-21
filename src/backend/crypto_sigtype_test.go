//go:build !ops

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
)

func TestVerifySignature_BinaryOnly(t *testing.T) {
	c := newCryptoService()
	kp, err := c.createKeyPair("sigtype", "", "")
	if err != nil {
		t.Fatal(err)
	}
	msg := "a\nb"

	binSig, err := c.sign(context.Background(), msg, kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.verifySignature(context.Background(), msg, binSig, kp.PublicKey); err != nil {
		t.Fatalf("binary signature rejected: %v", err)
	}
	if err := c.verifySignature(context.Background(), "a\r\nb", binSig, kp.PublicKey); err == nil {
		t.Fatal("binary signature verified a CRLF variant")
	}

	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(kp.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	var textSig bytes.Buffer
	if err := openpgp.ArmoredDetachSignText(&textSig, entities[0], strings.NewReader(msg), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.verifySignature(context.Background(), msg, textSig.String(), kp.PublicKey); err == nil {
		t.Fatal("text-mode signature accepted by verifySignature")
	}
	if err := c.verifySignedChallenge(context.Background(), textSig.String(), kp.PublicKey, msg); err == nil {
		t.Fatal("text-mode signature accepted by verifySignedChallenge")
	}
}
