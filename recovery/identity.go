package recovery

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"syrinx/identity"
)

const challengeMaxAge = 60 * time.Second

// Deps are dependencies RegisterRoutes needs from main.
type Deps struct {
	DB        *sql.DB
	Crypto    Verifier
	ServerID  string
	Lookup    ServerKeyLookup
	UserIDKey any              // context key for authenticated caller (peer endpoints)
	Now       func() time.Time // optional; defaults to time.Now
}

// now returns the current time. Deps.Now exists so tests can freeze the
// clock when checking challenge freshness; production leaves it nil.
func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// IssueChallenge handles GET /api/recovery/identity/claim.
func (d Deps) IssueChallenge(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ChallengeResponse{
		Challenge: d.now().UTC().Unix(),
	})
}

// ClaimIdentity handles POST /api/recovery/identity/claim.
func (d Deps) ClaimIdentity(w http.ResponseWriter, r *http.Request) {
	var req ClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	now := d.now()
	if err := ValidateChallengeAge(req.Challenge, now, challengeMaxAge); err != nil {
		writeJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	active, keys, err := FlattenKeysNest(req.Profile, req.Key, d.ServerID, d.Lookup, d.Crypto)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := VerifyChallengeSignature(req.Challenge, req.Signature, active.Key.Armor, d.Crypto); err != nil {
		writeJSON(w, http.StatusUnauthorized, err.Error())
		return
	}

	deviceID, err := identity.ParseDeviceID(r.Header.Get("X-Syrinx-Device-Id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, "Missing or invalid X-Syrinx-Device-Id header")
		return
	}

	if _, err := SaveOwnIdentity(d.DB, req.Profile, keys, deviceID); err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	req.Profile.ActiveKeyFingerprint = active.Key.Fingerprint
	writeJSON(w, http.StatusOK, req.Profile)
}

// ReportPeerIdentity handles POST /api/recovery/identity.
func (d Deps) ReportPeerIdentity(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(d.UserIDKey).(string)
	if !ok || caller == "" {
		writeJSON(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req PeerIdentityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Profile.ID == caller {
		writeJSON(w, http.StatusBadRequest, "own identity must use claim")
		return
	}

	active, keys, err := FlattenKeysNest(req.Profile, req.Key, d.ServerID, d.Lookup, d.Crypto)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	if _, err := SavePeerIdentity(d.DB, req.Profile, keys); err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	req.Profile.ActiveKeyFingerprint = active.Key.Fingerprint
	writeJSON(w, http.StatusOK, req.Profile)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func noop(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
