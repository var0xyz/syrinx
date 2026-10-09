//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// protoRequest builds a request carrying msg as its protobuf body.
func protoRequest(method, target string, msg proto.Message) *http.Request {
	body, err := proto.Marshal(msg)
	if err != nil {
		panic(err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Content-Type", protobufContentType)
	return req
}

// decodeProto unmarshals a protobuf response body into msg.
func decodeProto(t testing.TB, body []byte, msg proto.Message) {
	t.Helper()
	if err := proto.Unmarshal(body, msg); err != nil {
		t.Fatalf("unmarshal response: %v (body %q)", err, body)
	}
}

// errorMessage reads the plain-English message out of an Error body.
func errorMessage(t testing.TB, body []byte) string {
	t.Helper()
	var e pb.Error
	decodeProto(t, body, &e)
	return e.GetMessage()
}

func TestReadRequestRejectsNonProtobufBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/x", bytes.NewReader([]byte(`{"a":1}`)))
	req.Header.Set("Content-Type", "application/json")
	var msg pb.RemovalRequest
	if err := readRequest(req, &msg); err != errNotProtobuf {
		t.Fatalf("readRequest = %v, want errNotProtobuf", err)
	}
}

func TestReadRequestAcceptsEmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/api/x", nil)
	var msg pb.UnlikeRequest
	if err := readRequest(req, &msg); err != nil {
		t.Fatalf("readRequest(empty) = %v", err)
	}
}

func TestReadRequestRejectsMalformedBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/x", bytes.NewReader([]byte{0xff, 0xff, 0xff}))
	req.Header.Set("Content-Type", protobufContentType)
	var msg pb.RemovalRequest
	if err := readRequest(req, &msg); err == nil {
		t.Fatal("readRequest accepted a malformed body")
	}
}

func TestWriteErrorIsProtobuf(t *testing.T) {
	rr := httptest.NewRecorder()
	writeError(rr, http.StatusBadRequest, "Nope")
	if ct := rr.Header().Get("Content-Type"); ct != protobufContentType {
		t.Fatalf("Content-Type = %q", ct)
	}
	if got := errorMessage(t, rr.Body.Bytes()); got != "Nope" {
		t.Fatalf("message = %q", got)
	}
}

func TestWriteResponseNilSendsNoBody(t *testing.T) {
	rr := httptest.NewRecorder()
	writeResponse(rr, http.StatusNoContent, nil)
	if rr.Body.Len() != 0 || rr.Code != http.StatusNoContent {
		t.Fatalf("got %d with %d bytes", rr.Code, rr.Body.Len())
	}
}

// Golden bytes pin the wire for the two most-shared resources, so a field
// renumbering shows up as a failing test rather than a silent break.
func TestUserAndReedRemovalGoldenBytes(t *testing.T) {
	user := &pb.User{
		Id: "u@s", Username: "ann", Role: "user", MemberSince: 1,
		UserSignature:   &pb.UserSignature{Id: "u@s/k", Armor: "a"},
		ServerSignature: &pb.ServerSignature{Id: "k@s", Armor: "b", SignedAt: 2},
	}
	got, err := proto.MarshalOptions{Deterministic: true}.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x0a, 0x03, 'u', '@', 's',
		0x12, 0x03, 'a', 'n', 'n',
		0x1a, 0x04, 'u', 's', 'e', 'r',
		0x28, 0x01,
		0x32, 0x0a, 0x0a, 0x05, 'u', '@', 's', '/', 'k', 0x12, 0x01, 'a',
		0x3a, 0x0a, 0x0a, 0x03, 'k', '@', 's', 0x12, 0x01, 'b', 0x18, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("User bytes = % x\nwant        % x", got, want)
	}
	var back pb.User
	decodeProto(t, got, &back)
	if !proto.Equal(&back, user) {
		t.Fatal("User round trip changed the message")
	}

	cert := &pb.ReedRemovalCert{ServerId: "s", UserId: "u@s", ReedId: "u@s/r"}
	got, err = proto.MarshalOptions{Deterministic: true}.Marshal(cert)
	if err != nil {
		t.Fatal(err)
	}
	want = []byte{0x0a, 0x01, 's', 0x12, 0x03, 'u', '@', 's', 0x1a, 0x05, 'u', '@', 's', '/', 'r'}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReedRemovalCert bytes = % x\nwant % x", got, want)
	}
}

// pbRecoveryKeyNode is the inverse of recoveryKeyNodeFromPB, for building
// request bodies in tests.
func pbRecoveryKeyNode(n recoveryKeyNode) *pb.RecoveryKeyNode {
	out := &pb.RecoveryKeyNode{
		Fingerprint:     n.Fingerprint,
		UserId:          n.UserID,
		Armor:           n.Armor,
		CreatedAt:       unixOrZero(n.CreatedAt),
		ExpiresAt:       unixPtr(n.ExpiresAt),
		Revoked:         n.Revoked,
		ServerSignature: pbRecoveryServerSignature(n.ServerSignature),
		Signature:       n.Signature,
	}
	if rev := n.Revocation; rev != nil {
		out.Revocation = &pb.RecoveryRevocation{
			Fingerprint:     rev.Fingerprint,
			UserId:          rev.UserID,
			Reason:          rev.Reason,
			Successor:       rev.Successor,
			UserSignature:   &pb.UserSignature{Id: rev.UserSignature.KeyID, Armor: rev.UserSignature.Armor},
			ServerSignature: pbRecoveryServerSignature(rev.ServerSignature),
		}
	}
	if n.Predecessor != nil {
		out.Predecessor = pbRecoveryKeyNode(*n.Predecessor)
	}
	return out
}

// refusalBlock is the block a 403 Error body carries, or nil.
func refusalBlock(body []byte) *BlockCert {
	var e pb.Error
	if err := proto.Unmarshal(body, &e); err != nil || e.GetBlock() == nil {
		return nil
	}
	cert := blockCertFromPB(e.GetBlock())
	return &cert
}

// userFromPB is the inverse of pbUser.
func userFromPB(u *pb.User) User {
	out := User{
		ID:              u.GetId(),
		Username:        u.GetUsername(),
		Role:            u.GetRole(),
		Bio:             u.GetBio(),
		CreatedAt:       timeFromUnix(u.GetMemberSince()),
		UserSignature:   userSignatureFromPB(u.GetUserSignature()),
		ServerSignature: serverSignatureFromPB(u.GetServerSignature()),
	}
	if inv := u.GetInvite(); inv != nil {
		out.Invite = &Invite{ID: inv.GetId(), UserID: inv.GetUserId(), Username: inv.GetUsername()}
	}
	return out
}

func TestSPARecordMatchesProtobufES(t *testing.T) {
	predecessor := "u@s/old"
	key := &pb.PublicKey{
		Id:              "u@s/new",
		UserId:          "u@s",
		Armor:           "armor",
		CreatedAt:       10,
		Predecessor:     &predecessor,
		ServerSignature: &pb.ServerSignature{Id: "s/fp", Armor: "sig", SignedAt: 20},
	}
	raw, err := json.Marshal(spaRecord(key.ProtoReflect()))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"$typeName":"syrinx.PublicKey","armor":"armor","compromised":false,"createdAt":10,"id":"u@s/new","predecessor":"u@s/old","revoked":false,"revokedAt":0,"serverSignature":{"$typeName":"syrinx.ServerSignature","armor":"sig","id":"s/fp","signedAt":20},"userId":"u@s"}`
	if string(raw) != want {
		t.Fatalf("got %s\nwant %s", raw, want)
	}
	key.Predecessor = nil
	if _, ok := spaRecord(key.ProtoReflect())["predecessor"]; ok {
		t.Fatal("unset optional field should be absent")
	}
}
