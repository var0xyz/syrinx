package invites

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

// Deps are dependencies RegisterRoutes needs from main.
type Deps struct {
	Store     *Store
	Mode      SignupMode
	Max       MaxInvitesPerUser
	UserIDKey any
	Now       func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// RegisterRoutes mounts invite HTTP endpoints on api (typically /api).
func RegisterRoutes(api *mux.Router, deps Deps) {
	api.HandleFunc("/invites", deps.Create).Methods(http.MethodPost)
	api.HandleFunc("/invites", deps.List).Methods(http.MethodGet)
	api.HandleFunc("/invites", noop).Methods(http.MethodOptions)
	api.HandleFunc("/invites/check", deps.Check).Methods(http.MethodGet)
	api.HandleFunc("/invites/check", noop).Methods(http.MethodOptions)
	api.HandleFunc("/invites/{id}", deps.RevokeInvite).Methods(http.MethodDelete)
	api.HandleFunc("/invites/{id}", noop).Methods(http.MethodOptions)
}

func noop(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (d Deps) callerID(r *http.Request) (string, bool) {
	id, ok := r.Context().Value(d.UserIDKey).(string)
	return id, ok && id != ""
}

type createResponse struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"createdAt"`
}

// Create handles POST /api/invites.
func (d Deps) Create(w http.ResponseWriter, r *http.Request) {
	caller, ok := d.callerID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if d.Mode == ModeClosed {
		writeJSON(w, http.StatusForbidden, "Signups are closed on this server")
		return
	}
	if d.Max != MaxInvitesUnlimited {
		n, err := d.Store.CountByCreator(r.Context(), caller)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		if n >= int(d.Max) {
			writeJSON(w, http.StatusForbidden, "Invite limit reached")
			return
		}
	}

	raw, hash, err := NewToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	createdAt := d.now().UTC().Truncate(time.Second)

	var id string
	for attempt := 0; attempt < 5; attempt++ {
		id, err = NewInviteID()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		err = d.Store.Insert(r.Context(), id, caller, hash, createdAt)
		if err == nil {
			writeJSON(w, http.StatusCreated, createResponse{
				ID:        id,
				Token:     raw,
				CreatedAt: createdAt,
			})
			return
		}
		// retry only on primary-key collision
	}
	writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
}

type claimedByWire struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type inviteWire struct {
	ID        string         `json:"id"`
	CreatedAt time.Time      `json:"createdAt"`
	Status    string         `json:"status"`
	ClaimedAt *time.Time     `json:"claimedAt"`
	ClaimedBy *claimedByWire `json:"claimedBy"`
	RevokedAt *time.Time     `json:"revokedAt"`
}

type listResponse struct {
	Invites []inviteWire `json:"invites"`
}

// List handles GET /api/invites.
func (d Deps) List(w http.ResponseWriter, r *http.Request) {
	caller, ok := d.callerID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	rows, err := d.Store.ListByCreatorWithUsernames(r.Context(), caller)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	out := make([]inviteWire, 0, len(rows))
	for _, row := range rows {
		item := inviteWire{
			ID:        row.Invite.ID,
			CreatedAt: row.Invite.CreatedAt.UTC(),
			Status:    row.Invite.Status(),
			ClaimedAt: row.Invite.ClaimedAt,
			RevokedAt: row.Invite.RevokedAt,
		}
		if row.Invite.ClaimedBy != nil {
			item.ClaimedBy = &claimedByWire{
				ID:       *row.Invite.ClaimedBy,
				Username: row.ClaimedUsername,
			}
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, listResponse{Invites: out})
}

// RevokeInvite handles DELETE /api/invites/{id}.
func (d Deps) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	caller, ok := d.callerID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id := mux.Vars(r)["id"]
	if id == "" {
		writeJSON(w, http.StatusBadRequest, "Invite id is required")
		return
	}

	err := d.Store.Revoke(r.Context(), id, caller, d.now().UTC())
	switch {
	case errors.Is(err, ErrInviteNotFound), errors.Is(err, ErrInviteNotOwner):
		writeJSON(w, http.StatusNotFound, "Invite not found")
	case errors.Is(err, ErrInviteAlreadyClaimed):
		writeJSON(w, http.StatusConflict, "Invite already claimed")
	case errors.Is(err, ErrInviteAlreadyRevoked):
		w.WriteHeader(http.StatusNoContent)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

type checkResponse struct {
	Valid bool `json:"valid"`
}

// Check handles GET /api/invites/check?token=.
func (d Deps) Check(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		writeJSON(w, http.StatusBadRequest, "Argument `token` is required")
		return
	}
	inv, err := d.Store.GetByTokenHash(r.Context(), HashToken(token))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	valid := inv != nil && inv.Status() == "pending"
	writeJSON(w, http.StatusOK, checkResponse{Valid: valid})
}

// ListedInvite is an invite plus optional claimed username for list API.
type ListedInvite struct {
	Invite          Invite
	ClaimedUsername string
}

// ListByCreatorWithUsernames joins claimed_by usernames for the list API.
func (s *Store) ListByCreatorWithUsernames(ctx context.Context, creatorID string) ([]ListedInvite, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT i.id, i.created_by, i.created_at, i.claimed_at, i.claimed_by, i.revoked_at,
		       COALESCE(u.username, '')
		FROM invites i
		LEFT JOIN users u ON u.id = i.claimed_by
		WHERE i.created_by = $1
		ORDER BY i.created_at DESC
	`, creatorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ListedInvite
	for rows.Next() {
		var inv Invite
		var claimedAt, revokedAt sql.NullTime
		var claimedBy sql.NullString
		var username string
		if err := rows.Scan(
			&inv.ID, &inv.CreatedBy, &inv.CreatedAt,
			&claimedAt, &claimedBy, &revokedAt, &username,
		); err != nil {
			return nil, err
		}
		if claimedAt.Valid {
			t := claimedAt.Time.UTC()
			inv.ClaimedAt = &t
		}
		if claimedBy.Valid {
			s := claimedBy.String
			inv.ClaimedBy = &s
		}
		if revokedAt.Valid {
			t := revokedAt.Time.UTC()
			inv.RevokedAt = &t
		}
		out = append(out, ListedInvite{Invite: inv, ClaimedUsername: username})
	}
	return out, rows.Err()
}
