package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"syrinx/roles"

	"github.com/gorilla/mux"
)

// TestDeleteMe_RootRejected verifies the root account cannot be deleted:
// the guard runs before any signature/body parsing, so an otherwise-empty
// request is enough to prove it fires.
func TestDeleteMe_RootRejected(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	root := seedFederationUser(t, ds, roles.RootUserID, "root", roles.RoleRoot)

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/users/me", h.DeleteMe).Methods(http.MethodDelete)

	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodDelete, "/api/users/me", nil), root)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("DeleteMe(root) status=%d body=%s, want 403", rr.Code, rr.Body.String())
	}

	removed, err := ds.HasAccountRemoval(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("root account was marked removed despite the guard")
	}
}

// TestDeleteMe_NonRootAllowedPastGuard verifies the guard doesn't block a
// regular user — it should fail later, on the missing signature, not here.
func TestDeleteMe_NonRootAllowedPastGuard(t *testing.T) {
	h, ds, _, _ := testFederationHandlers(t)
	user := seedFederationUser(t, ds, "regular1", "regular1", roles.RoleUser)

	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/users/me", h.DeleteMe).Methods(http.MethodDelete)

	rr := httptest.NewRecorder()
	req := federationWithUID(httptest.NewRequest(http.MethodDelete, "/api/users/me", nil), user)
	router.ServeHTTP(rr, req)
	if rr.Code == http.StatusForbidden {
		t.Fatalf("DeleteMe(non-root) was rejected by the root guard, status=%d body=%s", rr.Code, rr.Body.String())
	}
}
