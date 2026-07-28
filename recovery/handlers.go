package recovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"syrinx/identity"
)

const maxFollowingBatch = 100

// ReportReed handles POST /api/recovery/reeds.
func (d Deps) ReportReed(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(d.UserIDKey).(string)
	if !ok || caller == "" {
		writeJSON(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req ReedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.ReedID == "" || req.AuthorID == "" || req.UserSignature.Armor == "" {
		writeJSON(w, http.StatusBadRequest, "reedID, authorID, and userSignature are required")
		return
	}
	if req.ServerSignature.Fingerprint == "" || req.ServerSignature.Armor == "" || req.ServerSignature.Timestamp.IsZero() {
		writeJSON(w, http.StatusBadRequest, "server countersignature is required")
		return
	}

	if err := verifyReedCountersig(req, d.ServerID, d.Lookup, d.Crypto); err != nil {
		writeJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	err := SaveReed(
		d.DB,
		req.ReedID,
		req.AuthorID,
		req.ServerSignature.Fingerprint,
		req.ServerSignature.Timestamp,
		caller,
		req.UserSignature.Fingerprint,
		req.UserSignature.Armor,
		req.ServerSignature.Armor,
	)
	switch {
	case errors.Is(err, ErrAuthorNotFound):
		writeJSON(w, http.StatusBadRequest, "author not found")
	case errors.Is(err, ErrReedConflict):
		writeJSON(w, http.StatusConflict, "reed metadata conflict")
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ReportFollowing handles POST /api/recovery/following.
func (d Deps) ReportFollowing(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(d.UserIDKey).(string)
	if !ok || caller == "" {
		writeJSON(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req FollowingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(req.UserIDs) > maxFollowingBatch {
		writeJSON(w, http.StatusBadRequest, "userIDs exceeds maximum of 100")
		return
	}
	for _, id := range req.UserIDs {
		if id == caller {
			writeJSON(w, http.StatusBadRequest, "Cannot follow yourself")
			return
		}
	}

	if err := SaveFollowing(d.DB, caller, req.UserIDs); err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CompleteImport handles POST /api/recovery/complete.
func (d Deps) CompleteImport(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(d.UserIDKey).(string)
	if !ok || caller == "" {
		writeJSON(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	if err := DeleteOngoing(d.DB, caller); err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func verifyReedCountersig(req ReedRequest, serverID string, lookup ServerKeyLookup, v Verifier) error {
	if req.ServerSignature.ServerID != "" && req.ServerSignature.ServerID != serverID {
		return fmt.Errorf("server id mismatch")
	}
	serverPub, err := lookup(req.ServerSignature.Fingerprint)
	if err != nil {
		return err
	}
	if serverPub == "" {
		return fmt.Errorf("unknown server key %s", req.ServerSignature.Fingerprint)
	}

	ts := req.ServerSignature.Timestamp.UTC().Truncate(time.Second)
	payload := identity.BuildReedPayload(
		serverID,
		req.AuthorID,
		req.ReedID,
		req.ServerSignature.Fingerprint,
		req.UserSignature.Armor,
		ts,
	)
	sigArmor, err := decodeB64Armor(req.ServerSignature.Armor)
	if err != nil {
		return fmt.Errorf("server signature: %w", err)
	}
	if err := v.VerifySignature(string(payload), sigArmor, serverPub); err != nil {
		return fmt.Errorf("bad countersignature")
	}
	return nil
}
