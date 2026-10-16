//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// A verification window is open while a user shows their verification code.
// The client pings it every few seconds; one pinged over verificationWindowTTL
// ago counts as closed, and going offline deletes it.
const verificationWindowTTL = time.Minute

// vouchWindowClosedMessage answers a vouch, with 412, when the subject has
// no open window: locally, or as reported by the subject's server.
const vouchWindowClosedMessage = "They need to have their verification code open while you scan it"

// OpenVerificationWindow replaces whatever window userID has open with id,
// reporting false when userID isn't online.
func (s *DataService) OpenVerificationWindow(ctx context.Context, userID, id string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO verification_windows (id, user_id, last_ping_at)
		SELECT $1::uuid, $2::varchar, $3::timestamp
		WHERE EXISTS (SELECT 1 FROM online_users WHERE user_id = $2::varchar)
		ON CONFLICT (user_id) DO UPDATE SET id = EXCLUDED.id, last_ping_at = EXCLUDED.last_ping_at
	`, id, userID, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// PingVerificationWindow keeps id open, reporting false when it isn't
// userID's open window: closed, replaced by a newer one, or expired.
func (s *DataService) PingVerificationWindow(ctx context.Context, userID, id string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE verification_windows SET last_ping_at = $3
		WHERE id = $1 AND user_id = $2 AND last_ping_at > $4
	`, id, userID, now, now.Add(-verificationWindowTTL))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CloseVerificationWindow closes id if it's userID's.
func (s *DataService) CloseVerificationWindow(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM verification_windows WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

// VerificationWindowOpen reports whether userID has a window pinged within
// verificationWindowTTL of now.
func (s *DataService) VerificationWindowOpen(ctx context.Context, userID string, now time.Time) (bool, error) {
	var open bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM verification_windows WHERE user_id = $1 AND last_ping_at > $2)
	`, userID, now.Add(-verificationWindowTTL)).Scan(&open)
	return open, err
}

// verificationWindowRequest reads the caller and the window id from r,
// answering the request itself when either is missing or malformed.
func verificationWindowRequest(w http.ResponseWriter, r *http.Request) (userID, id string, ok bool) {
	userID, _ = r.Context().Value(userIDKey).(string)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return "", "", false
	}
	parsed, err := uuid.Parse(mux.Vars(r)["windowID"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "The window id must be a UUID")
		return "", "", false
	}
	return userID, parsed.String(), true
}

// OpenVerificationWindow handles POST /verification-windows/{windowID}.
func (h *Handlers) OpenVerificationWindow(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := verificationWindowRequest(w, r)
	if !ok {
		return
	}
	opened, err := h.services.db.OpenVerificationWindow(r.Context(), userID, id, time.Now().UTC())
	if err != nil {
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("userID", userID).Msg("Error opening verification window")
		internalServerError(w)
		return
	}
	if !opened {
		writeError(w, http.StatusConflict, "Connect to the server to show your verification code")
		return
	}
	h.metrics.VerificationWindow(r.Context(), "open", userID)
	w.WriteHeader(http.StatusNoContent)
}

// PingVerificationWindow handles POST /verification-windows/{windowID}/ping.
// A 404 tells the client the window is gone and it should open a new one.
func (h *Handlers) PingVerificationWindow(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := verificationWindowRequest(w, r)
	if !ok {
		return
	}
	open, err := h.services.db.PingVerificationWindow(r.Context(), userID, id, time.Now().UTC())
	if err != nil {
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("userID", userID).Msg("Error pinging verification window")
		internalServerError(w)
		return
	}
	if !open {
		writeError(w, http.StatusNotFound, "Verification window not open")
		return
	}
	h.metrics.VerificationWindow(r.Context(), "ping", userID)
	w.WriteHeader(http.StatusNoContent)
}

// CloseVerificationWindow handles DELETE /verification-windows/{windowID}.
func (h *Handlers) CloseVerificationWindow(w http.ResponseWriter, r *http.Request) {
	userID, id, ok := verificationWindowRequest(w, r)
	if !ok {
		return
	}
	if err := h.services.db.CloseVerificationWindow(r.Context(), userID, id); err != nil {
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("userID", userID).Msg("Error closing verification window")
		internalServerError(w)
		return
	}
	h.metrics.VerificationWindow(r.Context(), "close", userID)
	w.WriteHeader(http.StatusNoContent)
}
