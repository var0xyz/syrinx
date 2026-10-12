//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	pb "syrinx/proto"
)

// ListReservedUsernames returns every reserved username, sorted.
func (s *DataService) ListReservedUsernames(ctx context.Context) ([]string, error) {
	names, err := s.queryStrings(ctx, `SELECT username FROM reserved_usernames ORDER BY username`)
	if names == nil {
		names = []string{}
	}
	return names, err
}

// rootCaller answers 401 or 403 unless root is calling.
func (h *Handlers) rootCaller(w http.ResponseWriter, r *http.Request) (string, bool) {
	callerID, role, ok := h.adminCaller(w, r)
	if !ok {
		return "", false
	}
	if !isRoot(callerID, role, h.services.db.GetServerID()) {
		writeError(w, http.StatusForbidden, "Root required")
		return "", false
	}
	return callerID, true
}

// reservedUsernameVar is the {username} path variable, lowercased, or ""
// when it isn't a username.
func reservedUsernameVar(r *http.Request) string {
	name := strings.ToLower(strings.TrimSpace(mux.Vars(r)["username"]))
	if name == "" || len(name) > 32 {
		return ""
	}
	return name
}

// ListReservedUsernames handles GET /admin/reserved-usernames.
func (h *Handlers) ListReservedUsernames(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.rootCaller(w, r); !ok {
		return
	}
	names, err := h.services.db.ListReservedUsernames(r.Context())
	if err != nil {
		h.writeAdminError(w, r, err, "Error listing reserved usernames")
		return
	}
	writeResponse(w, http.StatusOK, &pb.ReservedUsernameList{Usernames: names})
}

// ReserveUsername handles PUT /admin/reserved-usernames/{username}. It's
// idempotent: reserving a reserved name writes no ledger entry.
func (h *Handlers) ReserveUsername(w http.ResponseWriter, r *http.Request) {
	h.changeReservedUsername(w, r, true)
}

// ReleaseUsername handles DELETE /admin/reserved-usernames/{username}.
func (h *Handlers) ReleaseUsername(w http.ResponseWriter, r *http.Request) {
	h.changeReservedUsername(w, r, false)
}

func (h *Handlers) changeReservedUsername(w http.ResponseWriter, r *http.Request, reserve bool) {
	callerID, ok := h.rootCaller(w, r)
	if !ok {
		return
	}
	name := reservedUsernameVar(r)
	if name == "" {
		writeError(w, http.StatusBadRequest, "Invalid username")
		return
	}
	var entry *ledgerEntry
	err := withAdminTx(h.services.db.db, r.Context(), func(tx *sql.Tx) error {
		query := `DELETE FROM reserved_usernames WHERE username = $1`
		message := fmt.Sprintf("Username %s released", name)
		if reserve {
			query = `INSERT INTO reserved_usernames (username) VALUES ($1) ON CONFLICT DO NOTHING`
			message = fmt.Sprintf("Username %s reserved", name)
		}
		res, err := tx.ExecContext(r.Context(), query, name)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		entry = &ledgerEntry{UserID: callerID, Message: message}
		return h.appendLedgerTx(r.Context(), tx, entry)
	})
	if err != nil {
		h.writeAdminError(w, r, err, "Error changing a reserved username")
		return
	}
	h.pushLedgerEntries(entry)
	w.WriteHeader(http.StatusNoContent)
}
