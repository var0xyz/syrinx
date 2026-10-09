//go:build !ops

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "syrinx/proto"
)

// signedReedRemoval is a removal of one of the fixture user's reeds, signed
// by them and countersigned over serverPayload by their home server.
func signedReedRemoval(t *testing.T, s signedKeyRevocation, serverPayload func(reedID, userSig string, at time.Time) []byte) *pb.RelayReedRemovalPayload {
	t.Helper()
	cryptoSvc := newCryptoService()
	sign := func(payload []byte, armor string) string {
		sig, err := cryptoSvc.sign(string(payload), armor)
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
	reedID := s.userID + "/" + newTestReedID(t)
	at := time.Now().UTC().Truncate(time.Second)
	userSig := sign(buildReedRemovalUserPayload(revHomeServerID, reedID), s.userKP.PrivateKey)
	return &pb.RelayReedRemovalPayload{Cert: &pb.RelayReedRemovalCert{
		ReedId:            reedID,
		UserId:            s.userID,
		UserSignature:     userSig,
		UserKeyId:         s.keyID,
		ServerSignature:   sign(serverPayload(reedID, userSig, at), s.serverKP.PrivateKey),
		ServerFingerprint: s.serverKeyID,
		ServerSignedAt:    unixOrZero(at),
	}}
}

func postReedRemoval(t *testing.T, h *Handlers, payload *pb.RelayReedRemovalPayload) int {
	t.Helper()
	r := protoRequest(http.MethodPost, "/api/federation/relay/reed-removal", payload)
	r = r.WithContext(context.WithValue(r.Context(), peerServerIDKey, revHomeServerID))
	rr := httptest.NewRecorder()
	h.ReedRemovalFromPeer(rr, r)
	return rr.Code
}

func TestReedRemovalFromPeer_VerifiesCountersignature(t *testing.T) {
	s := newSignedKeyRevocation(t)
	fake, _ := s.fakeHome(t)
	countersigned := func(reedID, userSig string, at time.Time) []byte {
		return buildReedRemovalServerPayload(revHomeServerID, reedID, s.keyID, s.serverKP.Fingerprint, userSig, at)
	}

	t.Run("accepted", func(t *testing.T) {
		h, _, _ := s.peerHandlers(t, fake)
		rm := signedReedRemoval(t, s, countersigned)
		if code := postReedRemoval(t, h, rm); code != http.StatusNoContent {
			t.Fatalf("status %d, want 204", code)
		}
		if cert, err := h.services.db.GetReedRemoval(context.Background(), rm.Cert.ReedId); err != nil || cert == nil {
			t.Fatalf("removal not stored: %v", err)
		}
	})

	refused := map[string]func() *pb.RelayReedRemovalPayload{
		"countersignature without the author key": func() *pb.RelayReedRemovalPayload {
			return signedReedRemoval(t, s, func(reedID, userSig string, at time.Time) []byte {
				return canonicalJSON(signedFields{
					"type": identityTypeReed, "serverID": revHomeServerID, "reedID": reedID,
					"signedAt":             at.Format(identityRecordTimeFormat),
					"serverKeyFingerprint": s.serverKP.Fingerprint, "userSignature": userSig,
				})
			})
		},
		"signed with another user's key": func() *pb.RelayReedRemovalPayload {
			rm := signedReedRemoval(t, s, countersigned)
			rm.Cert.UserKeyId = string(canonicalID(revHomeServerID, "eve")) + "/" + s.userKP.Fingerprint
			return rm
		},
	}
	for name, build := range refused {
		t.Run(name, func(t *testing.T) {
			h, _, _ := s.peerHandlers(t, fake)
			rm := build()
			if code := postReedRemoval(t, h, rm); code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", code)
			}
			if cert, _ := h.services.db.GetReedRemoval(context.Background(), rm.Cert.ReedId); cert != nil {
				t.Fatal("refused removal was stored")
			}
		})
	}
}
