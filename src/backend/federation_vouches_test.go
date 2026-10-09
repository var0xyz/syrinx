//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// vouchRefFixture is a subject's server (home) with a local user, and a
// peer whose user vouches for them, with real keys on both sides.
type vouchRefFixture struct {
	db           *sql.DB
	h            *Handlers
	crypto       *cryptoService
	peerKP       *cryptoKeyPair
	peerKeyID    string
	voucherKP    *cryptoKeyPair
	subject      string
	subjectKeyID string
	voucher      string
	voucherKeyID string
}

func newVouchRefFixture(t *testing.T) *vouchRefFixture {
	t.Helper()
	db, rs, _ := newTeardownTestService(t)
	c := newCryptoService()
	keyPair := func(name string) *cryptoKeyPair {
		kp, err := c.createKeyPair(name, "", "")
		if err != nil {
			t.Fatalf("createKeyPair %s: %v", name, err)
		}
		return kp
	}
	addKey := func(id, owner, armor string) {
		var sigID int64
		if err := db.QueryRow(`INSERT INTO server_signatures (private_key_id, signature, signed_at) VALUES ('seed', 'sig', NOW()) RETURNING id`).Scan(&sigID); err != nil {
			t.Fatalf("insert server signature: %v", err)
		}
		var ownerArg any
		if owner != "" {
			ownerArg = owner
		}
		if _, err := db.Exec(`INSERT INTO public_keys (id, owner, armor, server_signature_id) VALUES ($1, $2, $3, $4)`, id, ownerArg, armor, sigID); err != nil {
			t.Fatalf("insert key %s: %v", id, err)
		}
	}

	f := &vouchRefFixture{db: db, crypto: c}
	f.peerKP = keyPair("peer")
	f.peerKeyID = f.peerKP.Fingerprint + "@" + teardownPeerID
	addKey(f.peerKeyID, "", f.peerKP.PublicKey)
	if _, err := db.Exec(`UPDATE servers SET key_id = $1, connected = TRUE WHERE id = $2`, f.peerKeyID, teardownPeerID); err != nil {
		t.Fatalf("pin peer key: %v", err)
	}

	subjectKP := keyPair("subject")
	f.subject = string(canonicalID(teardownHomeID, "sam"))
	insertTeardownIdentity(t, db, f.subject, teardownHomeID)
	if _, err := db.Exec(`INSERT INTO users (id) VALUES ($1)`, f.subject); err != nil {
		t.Fatalf("insert subject user: %v", err)
	}
	f.subjectKeyID = f.subject + "/" + subjectKP.Fingerprint
	addKey(f.subjectKeyID, f.subject, subjectKP.PublicKey)

	f.voucherKP = keyPair("voucher")
	f.voucher = string(canonicalID(teardownPeerID, "vic"))
	insertTeardownIdentity(t, db, f.voucher, teardownPeerID)
	f.voucherKeyID = f.voucher + "/" + f.voucherKP.Fingerprint
	addKey(f.voucherKeyID, f.voucher, f.voucherKP.PublicKey)

	f.h = &Handlers{
		services:      &Services{db: rs.db, log: NewLoggingService(), crypto: c},
		metrics:       metrics.Noop{},
		realtimeRelay: rs,
		broadcastChan: make(chan realtimeBroadcastMessage, 16),
	}
	return f
}

func (f *vouchRefFixture) sign(t *testing.T, payload []byte, privateKey string) string {
	t.Helper()
	armor, err := f.crypto.sign(string(payload), privateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return armor
}

// cert builds a vouch exactly as the peer would: signed by the voucher and
// countersigned by the peer's pinned key.
func (f *vouchRefFixture) cert(t *testing.T, note string) VouchCert {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	userSig := f.sign(t, buildVouchUserPayload(f.voucherKeyID, f.subjectKeyID, note), f.voucherKP.PrivateKey)
	now := time.Now().UTC().Truncate(time.Second)
	serverSig := f.sign(t, buildVouchServerPayload(f.subjectKeyID, f.peerKP.Fingerprint, userSig, now), f.peerKP.PrivateKey)
	return VouchCert{
		ID:              string(appendEntity(identityID(f.voucher), id.String())),
		VoucherUserID:   f.voucher,
		VoucherKeyID:    f.voucherKeyID,
		SubjectUserID:   f.subject,
		SubjectKeyID:    f.subjectKeyID,
		Note:            note,
		UserSignature:   UserSignature{ID: f.voucherKeyID, Armor: userSig},
		ServerSignature: ServerSignature{ID: f.peerKeyID, Armor: serverSig, SignedAt: now},
	}
}

func (f *vouchRefFixture) post(t *testing.T, handler http.HandlerFunc, path, peerServerID string, payload proto.Message) int {
	t.Helper()
	req := protoRequest(http.MethodPost, path, payload)
	rr := httptest.NewRecorder()
	handler(rr, withPeer(req, peerServerID))
	return rr.Code
}

func (f *vouchRefFixture) referenced(t *testing.T) []string {
	t.Helper()
	ids, err := f.h.services.db.ListVouchIDsForSubject(context.Background(), f.subject)
	if err != nil {
		t.Fatalf("ListVouchIDsForSubject: %v", err)
	}
	return ids
}

// A valid vouch from the voucher's own server is kept as a reference and
// listed with the subject's vouches.
func TestVouchReferenceAccepted(t *testing.T) {
	f := newVouchRefFixture(t)
	cert := f.cert(t, "met at the fair")

	code := f.post(t, f.h.VouchReferenceFromPeer, "/api/federation/relay/vouch-reference", teardownPeerID, &pb.RelayVouchReferencePayload{Cert: pbVouch(&cert)})
	if code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", code)
	}
	if ids := f.referenced(t); len(ids) != 1 || ids[0] != cert.ID {
		t.Fatalf("subject's vouch ids = %v, want [%s]", ids, cert.ID)
	}
}

// Anything that doesn't check out is refused for good, and nothing is kept.
func TestVouchReferenceRefused(t *testing.T) {
	cases := map[string]func(f *vouchRefFixture, cert *VouchCert) string{
		"tampered note": func(f *vouchRefFixture, cert *VouchCert) string {
			cert.Note = "something else"
			return teardownPeerID
		},
		"caller is not the voucher's server": func(f *vouchRefFixture, cert *VouchCert) string {
			return teardownOtherPeerID
		},
		"subject is not ours": func(f *vouchRefFixture, cert *VouchCert) string {
			cert.SubjectUserID = f.voucher
			return teardownPeerID
		},
		"countersigned by another key": func(f *vouchRefFixture, cert *VouchCert) string {
			cert.ServerSignature.Armor = f.sign(t, buildVouchServerPayload(cert.SubjectKeyID, f.peerKP.Fingerprint, cert.UserSignature.Armor, cert.ServerSignature.SignedAt), f.voucherKP.PrivateKey)
			return teardownPeerID
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			f := newVouchRefFixture(t)
			cert := f.cert(t, "met at the fair")
			caller := tamper(f, &cert)

			code := f.post(t, f.h.VouchReferenceFromPeer, "/api/federation/relay/vouch-reference", caller, &pb.RelayVouchReferencePayload{Cert: pbVouch(&cert)})
			if code < 400 || code >= 500 {
				t.Fatalf("status = %d, want a 4xx", code)
			}
			if ids := f.referenced(t); len(ids) != 0 {
				t.Fatalf("subject's vouch ids = %v, want none", ids)
			}
		})
	}
}

// A signed withdrawal drops the reference; repeating it is harmless.
func TestVouchWithdrawalDropsReference(t *testing.T) {
	f := newVouchRefFixture(t)
	cert := f.cert(t, "")
	if code := f.post(t, f.h.VouchReferenceFromPeer, "/api/federation/relay/vouch-reference", teardownPeerID, &pb.RelayVouchReferencePayload{Cert: pbVouch(&cert)}); code != http.StatusNoContent {
		t.Fatalf("reference status = %d, want 204", code)
	}

	userSig := f.sign(t, buildVouchWithdrawalUserPayload(cert.ID), f.voucherKP.PrivateKey)
	now := time.Now().UTC().Truncate(time.Second)
	serverSig := f.sign(t, buildVouchWithdrawalServerPayload(cert.ID, f.peerKP.Fingerprint, userSig, now), f.peerKP.PrivateKey)
	payload := &pb.RelayVouchWithdrawalPayload{
		VouchId:       cert.ID,
		VoucherUserId: f.voucher,
		Withdrawal: &pb.VouchWithdrawal{
			UserSignature:   &pb.UserSignature{Id: f.voucherKeyID, Armor: userSig},
			ServerSignature: pbServerSignature(ServerSignature{ID: f.peerKeyID, Armor: serverSig, SignedAt: now}),
		},
	}
	for i := 0; i < 2; i++ {
		if code := f.post(t, f.h.VouchWithdrawalFromPeer, "/api/federation/relay/vouch-withdrawal", teardownPeerID, payload); code != http.StatusNoContent {
			t.Fatalf("withdrawal #%d status = %d, want 204", i+1, code)
		}
	}
	if ids := f.referenced(t); len(ids) != 0 {
		t.Fatalf("subject's vouch ids = %v, want none after withdrawal", ids)
	}
}

// An unreachable subject server is reported as such, so the client can say
// so and the voucher's server stores nothing.
func TestVouchDeliveryToUnreachableServer(t *testing.T) {
	f := newVouchRefFixture(t)
	if _, err := f.db.Exec(`UPDATE servers SET base_url = 'http://127.0.0.1:1' WHERE id = $1`, teardownPeerID); err != nil {
		t.Fatalf("point peer at nothing: %v", err)
	}
	cert := f.cert(t, "")
	cert.SubjectUserID = f.voucher

	err := f.h.deliverVouchToSubject(context.Background(), cert)
	if err == nil {
		t.Fatal("delivery to an unreachable server succeeded")
	}
	rr := httptest.NewRecorder()
	writeVouchDeliveryError(rr, err)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for an unreachable server", rr.Code)
	}
}
