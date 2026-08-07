package invites

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"syrinx/roles"

	"github.com/gorilla/mux"
)

type testUIDKeyType struct{}

var testUserIDKey = testUIDKeyType{}

func withUID(r *http.Request, uid string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), testUserIDKey, uid))
}

func testDeps(t *testing.T, mode SignupMode, max MaxInvitesPerUser) Deps {
	t.Helper()
	db := openTestDB(t)
	fixed := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	return Deps{
		Store:                &Store{DB: db},
		Mode:                 mode,
		Max:                  max,
		UserIDKey:            testUserIDKey,
		Now:                  func() time.Time { return fixed },
		ServerID:             "test-server",
		ServerKeyFingerprint: "server-fp",
		GetPublicKeyArmor: func(ctx context.Context, userID, fingerprint string) (string, error) {
			return "pub-armor", nil
		},
		GetUserRole: func(ctx context.Context, userID string) (string, error) {
			var role string
			err := db.QueryRowContext(ctx, `SELECT role FROM users WHERE id = $1`, userID).Scan(&role)
			if err == sql.ErrNoRows {
				return "", nil
			}
			return role, err
		},
		VerifySignature: func(payload, sigArmor, pubKeyArmor string) error {
			return nil
		},
		Countersign: func(payload []byte, ts time.Time) (ServerSignatureWire, error) {
			return ServerSignatureWire{
				ServerID:    "test-server",
				Fingerprint: "server-fp",
				Armor:       base64.StdEncoding.EncodeToString([]byte("server-sig")),
				Timestamp:   ts.UTC().Format(time.RFC3339),
			}, nil
		},
	}
}

func createBody(t *testing.T, id, tokenHashHex string, createdAt time.Time, grantedRole string) *bytes.Buffer {
	t.Helper()
	var err error
	if tokenHashHex == "" {
		secret, err := NewSecret()
		if err != nil {
			t.Fatal(err)
		}
		tokenHashHex = EncodeHashHex(HashSecret(secret))
	}
	if id == "" {
		id, err = NewInviteID()
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.Marshal(createRequest{
		ID:          id,
		TokenHash:   tokenHashHex,
		CreatedAt:   createdAt,
		GrantedRole: grantedRole,
		UserSignature: UserSignatureWire{
			Fingerprint: "user-fp",
			Armor:       base64.StdEncoding.EncodeToString([]byte("user-sig")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewBuffer(b)
}

func postCreate(deps Deps, uid string, body *bytes.Buffer) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/invites", body)
	if uid != "" {
		req = withUID(req, uid)
	}
	deps.Create(rr, req)
	return rr
}

func TestCreate_Open(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "u1", "alice")
	fixed := deps.Now()

	rr := postCreate(deps, "u1", createBody(t, "", "", fixed, ""))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body createResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID == "" || body.TokenHash == "" || body.ServerSignature.Armor == "" {
		t.Fatalf("empty fields: %+v", body)
	}
	if len(body.TokenHash) != sha256.Size*2 {
		t.Fatalf("tokenHash len = %d", len(body.TokenHash))
	}

	rr2 := postCreate(deps, "u1", createBody(t, "", "", fixed, ""))
	if rr2.Code != http.StatusCreated {
		t.Fatalf("second create status = %d", rr2.Code)
	}
	n, err := deps.Store.CountByCreator(context.Background(), "u1")
	if err != nil || n != 2 {
		t.Fatalf("count = %d err=%v", n, err)
	}
}

func TestCreate_Quota(t *testing.T) {
	deps := testDeps(t, ModeInvite, MaxInvitesPerUser(1))
	seedUser(t, deps.Store.DB, "u1", "alice")
	fixed := deps.Now()

	rr := postCreate(deps, "u1", createBody(t, "", "", fixed, ""))
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create = %d", rr.Code)
	}

	rr2 := postCreate(deps, "u1", createBody(t, "", "", fixed, ""))
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("quota create = %d want 403", rr2.Code)
	}

	depsUnlimited := deps
	depsUnlimited.Max = MaxInvitesUnlimited
	rr3 := postCreate(depsUnlimited, "u1", createBody(t, "", "", fixed, ""))
	if rr3.Code != http.StatusCreated {
		t.Fatalf("unlimited create = %d", rr3.Code)
	}
}

func TestCreate_Closed(t *testing.T) {
	deps := testDeps(t, ModeClosed, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "u1", "alice")
	fixed := deps.Now()

	rr := postCreate(deps, "u1", createBody(t, "", "", fixed, ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("create closed = %d want 403", rr.Code)
	}

	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.Store.Insert(context.Background(), id, "u1", HashSecret(secret), fixed, roles.RoleUser); err != nil {
		t.Fatal(err)
	}
	rrStatus := httptest.NewRecorder()
	deps.Status(rrStatus, withUID(httptest.NewRequest(http.MethodGet, "/api/invites/"+id, nil), "u1"))
	if rrStatus.Code != http.StatusOK {
		t.Fatalf("status closed = %d", rrStatus.Code)
	}
}

func TestCreate_Unauthenticated(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	rr := postCreate(deps, "", createBody(t, "", "", deps.Now(), ""))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rr.Code)
	}
}

func TestCreate_DuplicateID(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "u1", "alice")
	fixed := deps.Now()
	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}

	rr := postCreate(deps, "u1", createBody(t, id, "", fixed, ""))
	if rr.Code != http.StatusCreated {
		t.Fatalf("first = %d", rr.Code)
	}
	rr2 := postCreate(deps, "u1", createBody(t, id, "", fixed, ""))
	if rr2.Code != http.StatusConflict {
		t.Fatalf("dup = %d want 409 body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestStatus_ClaimedBy(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "creator", "alice")
	seedUser(t, deps.Store.DB, "invitee", "bob")
	now := deps.Now()

	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.Store.Insert(context.Background(), id, "creator", HashSecret(secret), now, roles.RoleUser); err != nil {
		t.Fatal(err)
	}
	tx, err := deps.Store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := deps.Store.MarkClaimed(context.Background(), tx, "creator", id, "invitee", now.Add(time.Minute))
	if err != nil || !ok {
		tx.Rollback()
		t.Fatalf("MarkClaimed ok=%v err=%v", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	deps.Status(rr, withUID(httptest.NewRequest(http.MethodGet, "/api/invites/"+id, nil), "creator"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body statusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "claimed" || body.ClaimedBy == nil || body.ClaimedBy.Username != "bob" {
		t.Fatalf("unexpected status body: %+v", body)
	}
}

func TestRevokeAndCheck(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "u1", "alice")
	fixed := deps.Now()

	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	hashHex := EncodeHashHex(HashSecret(secret))
	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}

	rrCreate := postCreate(deps, "u1", createBody(t, id, hashHex, fixed, ""))
	if rrCreate.Code != http.StatusCreated {
		t.Fatalf("create = %d", rrCreate.Code)
	}

	rrCheck := httptest.NewRecorder()
	deps.Check(rrCheck, httptest.NewRequest(http.MethodGet, "/api/invites/check?id="+id+"&secret="+secret, nil))
	if rrCheck.Code != http.StatusOK || !bytes.Contains(rrCheck.Body.Bytes(), []byte(`"valid":true`)) {
		t.Fatalf("check pending: %d %s", rrCheck.Code, rrCheck.Body.String())
	}

	rrRevoke := httptest.NewRecorder()
	deps.RevokeInvite(
		rrRevoke,
		withUID(httptest.NewRequest(http.MethodDelete, "/api/invites/"+id, nil), "u1"),
	)
	if rrRevoke.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", rrRevoke.Code)
	}

	rrCheck2 := httptest.NewRecorder()
	deps.Check(rrCheck2, httptest.NewRequest(http.MethodGet, "/api/invites/check?id="+id+"&secret="+secret, nil))
	if rrCheck2.Code != http.StatusOK || !bytes.Contains(rrCheck2.Body.Bytes(), []byte(`"valid":false`)) {
		t.Fatalf("check revoked: %d %s", rrCheck2.Code, rrCheck2.Body.String())
	}
}

func TestCheck_Variants(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "u1", "alice")
	fixed := deps.Now()

	rrMissing := httptest.NewRecorder()
	deps.Check(rrMissing, httptest.NewRequest(http.MethodGet, "/api/invites/check", nil))
	if rrMissing.Code != http.StatusBadRequest {
		t.Fatalf("missing = %d", rrMissing.Code)
	}

	rrUnknown := httptest.NewRecorder()
	deps.Check(rrUnknown, httptest.NewRequest(http.MethodGet, "/api/invites/check?id=abcdefghijkl&secret=nope", nil))
	if rrUnknown.Code != http.StatusOK || !bytes.Contains(rrUnknown.Body.Bytes(), []byte(`"valid":false`)) {
		t.Fatalf("unknown: %s", rrUnknown.Body.String())
	}

	secret, _ := NewSecret()
	id, _ := NewInviteID()
	hashHex := EncodeHashHex(HashSecret(secret))
	rrCreate := postCreate(deps, "u1", createBody(t, id, hashHex, fixed, ""))
	if rrCreate.Code != http.StatusCreated {
		t.Fatalf("create = %d", rrCreate.Code)
	}
	rrOk := httptest.NewRecorder()
	deps.Check(rrOk, httptest.NewRequest(http.MethodGet, "/api/invites/check?id="+id+"&secret="+secret, nil))
	if rrOk.Code != http.StatusOK || !bytes.Contains(rrOk.Body.Bytes(), []byte(`"valid":true`)) {
		t.Fatalf("pending check: %s", rrOk.Body.String())
	}

	// Wrong id for valid secret → invalid
	rrWrongID := httptest.NewRecorder()
	deps.Check(rrWrongID, httptest.NewRequest(http.MethodGet, "/api/invites/check?id=zzzzzzzzzzzz&secret="+secret, nil))
	if !bytes.Contains(rrWrongID.Body.Bytes(), []byte(`"valid":false`)) {
		t.Fatalf("wrong id: %s", rrWrongID.Body.String())
	}
}

func TestCreate_UserCannotGrantAdmin(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "u1", "alice")

	rr := postCreate(deps, "u1", createBody(t, "", "", deps.Now(), roles.RoleAdmin))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("Cannot grant admin role")) {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestCreate_AdminCanGrantAdmin(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUserWithRole(t, deps.Store.DB, "admin1", "admin", roles.RoleAdmin)

	rr := postCreate(deps, "admin1", createBody(t, "", "", deps.Now(), roles.RoleAdmin))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body createResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.GrantedRole != roles.RoleAdmin {
		t.Fatalf("grantedRole = %q want admin", body.GrantedRole)
	}
}

func TestRegisterRoutes_CheckAllowlistedPath(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	r := mux.NewRouter()
	api := r.PathPrefix("/api").Subrouter()
	RegisterRoutes(api, deps)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/invites/check?id=x&secret=y", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("check route = %d", rr.Code)
	}
}
