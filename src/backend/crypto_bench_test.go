package main

import (
	"context"
	"bytes"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// Signup's crypto against a real server key (createKeyPair: Ed25519) and an
// Ed25519 user key like the ones the SPA generates; RSA-2048 for comparison.
type benchKeys struct {
	svc           *cryptoService
	server        *cryptoKeyPair
	userPublic    string
	userSelfSig   string // user's signature over its own public key armor
	userPayload   string
	userSignature string
	serverSig     string // server signature over benchMessage
	rsaPrivate    string // RSA-2048 key, the server key type before Ed25519
}

const benchMessage = `{"fingerprint":"u1@srv/0123456789abcdef","role":"user","serverID":"srv","signedAt":"2026-10-21T00:00:00Z","username":"alice"}`

func newBenchKeys(b *testing.B) benchKeys {
	b.Helper()
	svc := newCryptoService()
	server, err := svc.createKeyPair("srv", "", "")
	if err != nil {
		b.Fatal(err)
	}
	user, err := openpgp.NewEntity("u1@srv", "", "", &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA})
	if err != nil {
		b.Fatal(err)
	}
	var pub bytes.Buffer
	w, err := armor.Encode(&pub, openpgp.PublicKeyType, nil)
	if err != nil {
		b.Fatal(err)
	}
	if err := user.Serialize(w); err != nil {
		b.Fatal(err)
	}
	w.Close()
	serverSig, err := svc.sign(context.Background(), benchMessage, server.PrivateKey)
	if err != nil {
		b.Fatal(err)
	}
	rsa, err := openpgp.NewEntity("srv", "", "", nil)
	if err != nil {
		b.Fatal(err)
	}
	var rsaPriv bytes.Buffer
	rw, err := armor.Encode(&rsaPriv, openpgp.PrivateKeyType, nil)
	if err != nil {
		b.Fatal(err)
	}
	if err := rsa.SerializePrivate(rw, nil); err != nil {
		b.Fatal(err)
	}
	rw.Close()
	return benchKeys{
		rsaPrivate:    rsaPriv.String(),
		svc:           svc,
		server:        server,
		userPublic:    pub.String(),
		userSelfSig:   benchDetachSign(b, user, pub.String()),
		userPayload:   benchMessage,
		userSignature: benchDetachSign(b, user, benchMessage),
		serverSig:     serverSig,
	}
}

func benchDetachSign(b *testing.B, signer *openpgp.Entity, message string) string {
	b.Helper()
	var out bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&out, signer, strings.NewReader(message), nil); err != nil {
		b.Fatal(err)
	}
	return out.String()
}

// One server countersignature (signup makes three, a reed two).
func BenchmarkServerSign(b *testing.B) {
	k := newBenchKeys(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := k.svc.sign(context.Background(), benchMessage, k.server.PrivateKey); err != nil {
			b.Fatal(err)
		}
	}
}

// The same with the RSA-2048 server keys used before.
func BenchmarkServerSignRSA(b *testing.B) {
	k := newBenchKeys(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := k.svc.sign(context.Background(), benchMessage, k.rsaPrivate); err != nil {
			b.Fatal(err)
		}
	}
}

// Parsing the armored server private key alone, which sign used to repeat.
func BenchmarkParseServerPrivateKey(b *testing.B) {
	k := newBenchKeys(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := openpgp.ReadArmoredKeyRing(strings.NewReader(k.rsaPrivate)); err != nil {
			b.Fatal(err)
		}
	}
}

// Checking the server's signature, as signup does for the userID.
func BenchmarkVerifyServerSignature(b *testing.B) {
	k := newBenchKeys(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := k.svc.verifySignature(context.Background(), benchMessage, k.serverSig, k.server.PublicKey); err != nil {
			b.Fatal(err)
		}
	}
}

// Signup's two checks on the new user's key (its self-signature and the
// signature over the identity payload), parsing the key once as Signup does.
func BenchmarkSignupUserKeyChecks(b *testing.B) {
	k := newBenchKeys(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entity, err := k.svc.extractEntity(k.userPublic)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := k.svc.validateAndExtractKey(context.Background(), entity, k.userPublic, k.userSelfSig); err != nil {
			b.Fatal(err)
		}
		if err := k.svc.verifySignatureWithEntity(context.Background(), k.userPayload, k.userSignature, entity); err != nil {
			b.Fatal(err)
		}
	}
}
