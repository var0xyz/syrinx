package invites

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"syrinx/crypto"
	"syrinx/encoding"
	"syrinx/identity"
	"syrinx/roles"

	"github.com/gorilla/mux"
)

// UserSignatureWire is the nested user attestation on create/response.
type UserSignatureWire struct {
	ID    string `json:"id"`
	Armor string `json:"armor"`
}

// ServerSignatureWire is the nested server countersignature on create
// response, same shape as every other ServerSignature.
type ServerSignatureWire struct {
	ID        string `json:"id"`
	Armor     string `json:"armor"`
	Timestamp string `json:"timestamp"`
}

// Deps are dependencies RegisterRoutes needs from main.
type Deps struct {
	Store                *Store
	Mode                 SignupMode
	Max                  MaxInvitesPerUser
	UserIDKey            any
	Now                  func() time.Time
	ServerID             string
	ServerKeyFingerprint string
	GetPublicKeyArmor    func(ctx context.Context, userID, fingerprint string) (string, error)
	GetUserRole          func(ctx context.Context, userID string) (string, error)
	VerifySignature      func(payload, sigArmor, pubKeyArmor string) error
	Countersign          func(payload []byte, ts time.Time) (ServerSignatureWire, error)
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
	api.HandleFunc("/invites", noop).Methods(http.MethodOptions)
	api.HandleFunc("/invites/check", deps.Check).Methods(http.MethodGet)
	api.HandleFunc("/invites/check", noop).Methods(http.MethodOptions)
	// {id} is "userID@serverID/reedID"-shaped and carries a "/", so it needs
	// a greedy path variable ({id:.+}) — a plain {id} stops at the first "/"
	// and never matches (see main.go's /keys/{id:.+} for the same gotcha).
	api.HandleFunc("/invites/{id:.+}", deps.Status).Methods(http.MethodGet)
	api.HandleFunc("/invites/{id:.+}", deps.RevokeInvite).Methods(http.MethodDelete)
	api.HandleFunc("/invites/{id:.+}", noop).Methods(http.MethodOptions)
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

type createRequest struct {
	ID            string            `json:"id"`
	TokenHash     string            `json:"tokenHash"`
	CreatedAt     time.Time         `json:"createdAt"`
	GrantedRole   string            `json:"grantedRole"`
	UserSignature UserSignatureWire `json:"userSignature"`
}

type createResponse struct {
	ID              string              `json:"id"`
	TokenHash       string              `json:"tokenHash"`
	CreatedAt       time.Time           `json:"createdAt"`
	GrantedRole     string              `json:"grantedRole"`
	UserSignature   UserSignatureWire   `json:"userSignature"`
	ServerSignature ServerSignatureWire `json:"serverSignature"`
}

// Create handles POST /api/invites.
// Client mints id + secret; only SHA-256(secret) is sent (tokenHash).
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
	if d.GetPublicKeyArmor == nil || d.VerifySignature == nil || d.Countersign == nil || d.GetUserRole == nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	var req createRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	idOwner, idServerID, idEntity, ok := identity.ParseKeyFingerprint(identity.IdentityID(req.ID))
	if !ok || !crypto.IsValidUUIDv7(idEntity) {
		writeJSON(w, http.StatusBadRequest, "Invalid invite id")
		return
	}
	if string(identity.CanonicalID(idServerID, idOwner)) != caller {
		writeJSON(w, http.StatusForbidden, "Invite id does not belong to the caller")
		return
	}
	tokenHash, err := DecodeHashHex(req.TokenHash)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, "Invalid tokenHash")
		return
	}
	tokenHashHex := EncodeHashHex(tokenHash)
	if req.UserSignature.ID == "" || req.UserSignature.Armor == "" {
		writeJSON(w, http.StatusBadRequest, "userSignature is required")
		return
	}

	createdAt := req.CreatedAt.UTC().Truncate(time.Second)
	if createdAt.IsZero() {
		writeJSON(w, http.StatusBadRequest, "createdAt is required")
		return
	}
	now := d.now().UTC().Truncate(time.Second)
	skew := now.Sub(createdAt)
	if skew < 0 {
		skew = -skew
	}
	if skew > InviteCreateSkew {
		writeJSON(w, http.StatusBadRequest, "createdAt out of range")
		return
	}

	grantedRole := strings.TrimSpace(req.GrantedRole)
	if grantedRole == "" {
		grantedRole = roles.RoleUser
	}
	if grantedRole != roles.RoleUser && grantedRole != roles.RoleAdmin {
		writeJSON(w, http.StatusBadRequest, "Invalid grantedRole")
		return
	}
	if grantedRole == roles.RoleAdmin {
		callerRole, err := d.GetUserRole(r.Context(), caller)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		if !roles.CanGrantAdmin(callerRole) {
			writeJSON(w, http.StatusForbidden, "Cannot grant admin role")
			return
		}
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

	userPayload := identity.BuildInviteUserPayload(
		d.ServerID, caller, req.ID, tokenHashHex, grantedRole, createdAt,
	)
	userSigArmor, err := encoding.Base64Decode(req.UserSignature.Armor)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, "Invalid userSignature encoding")
		return
	}
	pubArmor, err := d.GetPublicKeyArmor(r.Context(), caller, req.UserSignature.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if pubArmor == "" {
		writeJSON(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := d.VerifySignature(string(userPayload), userSigArmor, pubArmor); err != nil {
		writeJSON(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	if err := d.Store.Insert(r.Context(), req.ID, caller, tokenHash, createdAt, grantedRole); err != nil {
		if errors.Is(err, ErrInviteExists) {
			writeJSON(w, http.StatusConflict, "Invite already exists")
			return
		}
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	signedAt := now
	serverPayload := identity.BuildInviteServerPayload(
		d.ServerID,
		caller,
		req.ID,
		tokenHashHex,
		d.ServerKeyFingerprint,
		req.UserSignature.Armor,
		createdAt,
		signedAt,
	)
	serverSig, err := d.Countersign(serverPayload, signedAt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	writeJSON(w, http.StatusCreated, createResponse{
		ID:              req.ID,
		TokenHash:       tokenHashHex,
		CreatedAt:       createdAt,
		GrantedRole:     grantedRole,
		UserSignature:   req.UserSignature,
		ServerSignature: serverSig,
	})
}

type statusResponse struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"createdAt"`
	Status    string     `json:"status"`
	ClaimedAt *time.Time `json:"claimedAt"`
	ClaimedBy *string    `json:"claimedBy"`
	RevokedAt *time.Time `json:"revokedAt"`
}

// Status handles GET /api/invites/{id} for the caller's invite.
func (d Deps) Status(w http.ResponseWriter, r *http.Request) {
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

	inv, err := d.Store.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if inv == nil || inv.CreatedBy != caller {
		writeJSON(w, http.StatusNotFound, "Invite not found")
		return
	}

	out := statusResponse{
		ID:        inv.ID,
		CreatedAt: inv.CreatedAt.UTC(),
		Status:    inv.Status(),
		ClaimedAt: inv.ClaimedAt,
		ClaimedBy: inv.ClaimedBy,
		RevokedAt: inv.RevokedAt,
	}
	writeJSON(w, http.StatusOK, out)
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

// Check handles GET /api/invites/check?id=&secret=.
// Client sends the fragment secret; server looks up by id + hash.
func (d Deps) Check(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	secret := strings.TrimSpace(r.URL.Query().Get("secret"))
	if id == "" || secret == "" {
		writeJSON(w, http.StatusBadRequest, "Arguments `id` and `secret` are required")
		return
	}
	inv, err := d.Store.GetPendingInvite(r.Context(), id, HashSecret(secret))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	valid := inv != nil
	writeJSON(w, http.StatusOK, checkResponse{Valid: valid})
}
