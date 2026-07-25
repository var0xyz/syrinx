package invites

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
		Store:     &Store{DB: db},
		Mode:      mode,
		Max:       max,
		UserIDKey: testUserIDKey,
		Now:       func() time.Time { return fixed },
	}
}

func TestCreate_Open(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "u1", "alice")

	rr := httptest.NewRecorder()
	deps.Create(rr, withUID(httptest.NewRequest(http.MethodPost, "/api/invites", nil), "u1"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body createResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID == "" || body.Token == "" {
		t.Fatalf("empty id/token: %+v", body)
	}

	rr2 := httptest.NewRecorder()
	deps.Create(rr2, withUID(httptest.NewRequest(http.MethodPost, "/api/invites", nil), "u1"))
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

	rr := httptest.NewRecorder()
	deps.Create(rr, withUID(httptest.NewRequest(http.MethodPost, "/api/invites", nil), "u1"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create = %d", rr.Code)
	}

	rr2 := httptest.NewRecorder()
	deps.Create(rr2, withUID(httptest.NewRequest(http.MethodPost, "/api/invites", nil), "u1"))
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("quota create = %d want 403", rr2.Code)
	}

	depsUnlimited := Deps{
		Store:     deps.Store,
		Mode:      ModeInvite,
		Max:       MaxInvitesUnlimited,
		UserIDKey: testUserIDKey,
		Now:       deps.Now,
	}
	rr3 := httptest.NewRecorder()
	depsUnlimited.Create(rr3, withUID(httptest.NewRequest(http.MethodPost, "/api/invites", nil), "u1"))
	if rr3.Code != http.StatusCreated {
		t.Fatalf("unlimited create = %d", rr3.Code)
	}
}

func TestCreate_Closed(t *testing.T) {
	deps := testDeps(t, ModeClosed, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "u1", "alice")

	rr := httptest.NewRecorder()
	deps.Create(rr, withUID(httptest.NewRequest(http.MethodPost, "/api/invites", nil), "u1"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("create closed = %d want 403", rr.Code)
	}

	rrList := httptest.NewRecorder()
	deps.List(rrList, withUID(httptest.NewRequest(http.MethodGet, "/api/invites", nil), "u1"))
	if rrList.Code != http.StatusOK {
		t.Fatalf("list closed = %d", rrList.Code)
	}
}

func TestCreate_Unauthenticated(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	rr := httptest.NewRecorder()
	deps.Create(rr, httptest.NewRequest(http.MethodPost, "/api/invites", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rr.Code)
	}
}

func TestList_ClaimedBy(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "creator", "alice")
	seedUser(t, deps.Store.DB, "invitee", "bob")

	raw, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewInviteID()
	if err != nil {
		t.Fatal(err)
	}
	now := deps.now()
	if err := deps.Store.Insert(context.Background(), id, "creator", hash, now); err != nil {
		t.Fatal(err)
	}
	tx, err := deps.Store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := deps.Store.MarkClaimed(context.Background(), tx, id, "invitee", now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("MarkClaimed ok=%v err=%v", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = raw

	rr := httptest.NewRecorder()
	deps.List(rr, withUID(httptest.NewRequest(http.MethodGet, "/api/invites", nil), "creator"))
	if rr.Code != http.StatusOK {
		t.Fatalf("list = %d body=%s", rr.Code, rr.Body.String())
	}
	var body listResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Invites) != 1 {
		t.Fatalf("len = %d", len(body.Invites))
	}
	item := body.Invites[0]
	if item.Status != "claimed" {
		t.Fatalf("status = %q", item.Status)
	}
	if item.ClaimedBy == nil || item.ClaimedBy.ID != "invitee" || item.ClaimedBy.Username != "bob" {
		t.Fatalf("claimedBy = %+v", item.ClaimedBy)
	}
}

func TestRevoke_PendingAndIdempotent(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "u1", "alice")

	rrCreate := httptest.NewRecorder()
	deps.Create(rrCreate, withUID(httptest.NewRequest(http.MethodPost, "/api/invites", nil), "u1"))
	var created createResponse
	_ = json.Unmarshal(rrCreate.Body.Bytes(), &created)

	rrCheck := httptest.NewRecorder()
	deps.Check(rrCheck, httptest.NewRequest(http.MethodGet, "/api/invites/check?token="+created.Token, nil))
	var checkBody checkResponse
	_ = json.Unmarshal(rrCheck.Body.Bytes(), &checkBody)
	if !checkBody.Valid {
		t.Fatal("expected valid before revoke")
	}

	r := mux.SetURLVars(
		withUID(httptest.NewRequest(http.MethodDelete, "/api/invites/"+created.ID, nil), "u1"),
		map[string]string{"id": created.ID},
	)
	rr := httptest.NewRecorder()
	deps.RevokeInvite(rr, r)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", rr.Code)
	}

	rrCheck2 := httptest.NewRecorder()
	deps.Check(rrCheck2, httptest.NewRequest(http.MethodGet, "/api/invites/check?token="+created.Token, nil))
	_ = json.Unmarshal(rrCheck2.Body.Bytes(), &checkBody)
	if checkBody.Valid {
		t.Fatal("expected invalid after revoke")
	}

	r2 := mux.SetURLVars(
		withUID(httptest.NewRequest(http.MethodDelete, "/api/invites/"+created.ID, nil), "u1"),
		map[string]string{"id": created.ID},
	)
	rr2 := httptest.NewRecorder()
	deps.RevokeInvite(rr2, r2)
	if rr2.Code != http.StatusNoContent {
		t.Fatalf("second revoke = %d", rr2.Code)
	}
}

func TestRevoke_Claimed(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "creator", "alice")
	seedUser(t, deps.Store.DB, "invitee", "bob")

	_, hash, _ := NewToken()
	id, _ := NewInviteID()
	now := deps.now()
	_ = deps.Store.Insert(context.Background(), id, "creator", hash, now)
	tx, _ := deps.Store.DB.Begin()
	_, _ = deps.Store.MarkClaimed(context.Background(), tx, id, "invitee", now)
	_ = tx.Commit()

	r := mux.SetURLVars(
		withUID(httptest.NewRequest(http.MethodDelete, "/api/invites/"+id, nil), "creator"),
		map[string]string{"id": id},
	)
	rr := httptest.NewRecorder()
	deps.RevokeInvite(rr, r)
	if rr.Code != http.StatusConflict {
		t.Fatalf("revoke claimed = %d want 409", rr.Code)
	}
}

func TestRevoke_OtherUser(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "owner", "alice")
	seedUser(t, deps.Store.DB, "other", "eve")

	rrCreate := httptest.NewRecorder()
	deps.Create(rrCreate, withUID(httptest.NewRequest(http.MethodPost, "/api/invites", nil), "owner"))
	var created createResponse
	_ = json.Unmarshal(rrCreate.Body.Bytes(), &created)

	r := mux.SetURLVars(
		withUID(httptest.NewRequest(http.MethodDelete, "/api/invites/"+created.ID, nil), "other"),
		map[string]string{"id": created.ID},
	)
	rr := httptest.NewRecorder()
	deps.RevokeInvite(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("revoke other = %d want 404", rr.Code)
	}
}

func TestCheck(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	seedUser(t, deps.Store.DB, "u1", "alice")

	rrMissing := httptest.NewRecorder()
	deps.Check(rrMissing, httptest.NewRequest(http.MethodGet, "/api/invites/check", nil))
	if rrMissing.Code != http.StatusBadRequest {
		t.Fatalf("missing token = %d", rrMissing.Code)
	}

	rrUnknown := httptest.NewRecorder()
	deps.Check(rrUnknown, httptest.NewRequest(http.MethodGet, "/api/invites/check?token=not-a-real-token", nil))
	if rrUnknown.Code != http.StatusOK {
		t.Fatalf("unknown = %d", rrUnknown.Code)
	}
	var body checkResponse
	_ = json.Unmarshal(rrUnknown.Body.Bytes(), &body)
	if body.Valid {
		t.Fatal("unknown should be invalid")
	}

	rrCreate := httptest.NewRecorder()
	deps.Create(rrCreate, withUID(httptest.NewRequest(http.MethodPost, "/api/invites", nil), "u1"))
	var created createResponse
	_ = json.Unmarshal(rrCreate.Body.Bytes(), &created)

	rrOk := httptest.NewRecorder()
	deps.Check(rrOk, httptest.NewRequest(http.MethodGet, "/api/invites/check?token="+created.Token, nil))
	_ = json.Unmarshal(rrOk.Body.Bytes(), &body)
	if rrOk.Code != http.StatusOK || !body.Valid {
		t.Fatalf("pending check = %d valid=%v", rrOk.Code, body.Valid)
	}
}

func TestRegisterRoutes_CheckUnauthenticated(t *testing.T) {
	deps := testDeps(t, ModeOpen, MaxInvitesUnlimited)
	r := mux.NewRouter()
	api := r.PathPrefix("/api").Subrouter()
	RegisterRoutes(api, deps)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/invites/check?token=x", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("check via router = %d", rr.Code)
	}

	rrOpt := httptest.NewRecorder()
	r.ServeHTTP(rrOpt, httptest.NewRequest(http.MethodOptions, "/api/invites", nil))
	if rrOpt.Code != http.StatusOK {
		t.Fatalf("options = %d", rrOpt.Code)
	}
}
