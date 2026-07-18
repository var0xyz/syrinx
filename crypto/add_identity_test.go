package crypto

import (
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
)

func TestAddIdentityPersistsAcrossEncryptDecrypt(t *testing.T) {
	svc := NewService()
	kp, err := svc.CreateKeyPair("ServerID01", "", "")
	if err != nil {
		t.Fatal(err)
	}
	pass := "sixteen-chars!!!"
	enc, err := svc.EncryptPrivateKey(kp.PrivateKey, pass)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		dec, err := svc.DecryptPrivateKey(enc, pass)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := svc.AddIdentity(dec, "syrinx.var0.xyz")
		if err != nil {
			t.Fatal(err)
		}
		changed := updated != dec
		ents, err := svc.ReadArmoredKeyRing(updated)
		if err != nil {
			t.Fatal(err)
		}
		if !hasIdentity(ents[0], "syrinx.var0.xyz") {
			t.Fatalf("round %d: missing identity after AddIdentity; have %v", i, idsOf(ents[0]))
		}
		if i > 0 && changed {
			t.Fatalf("round %d: AddIdentity re-wrote armor; identity was not persisted", i)
		}
		enc, err = svc.EncryptPrivateKey(updated, pass)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func hasIdentity(e *openpgp.Entity, want string) bool {
	_, ok := e.Identities[want]
	return ok
}

func idsOf(e *openpgp.Entity) []string {
	out := make([]string, 0, len(e.Identities))
	for id := range e.Identities {
		out = append(out, id)
	}
	return out
}
