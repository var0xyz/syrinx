//go:build !ops && !ripplescleanup

package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"syrinx/coverage"
	"syrinx/crypto"
	"syrinx/deletion"
	"syrinx/encoding"
	"syrinx/identity"
	"syrinx/invites"
	"syrinx/observability/metrics"
	"syrinx/realtime"
	"syrinx/recovery"
	"syrinx/roles"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

// countersign signs payload with the active server key and returns a
// ServerSignature. Used for keys, reeds, identity, etc.
func (h *Handlers) countersign(payload []byte, ts time.Time) (ServerSignature, error) {
	sigArmor, err := h.services.crypto.Sign(string(payload), h.signingKey.Armor)
	if err != nil {
		return ServerSignature{}, err
	}
	return ServerSignature{
		ServerID:    h.services.db.GetServerID(),
		Fingerprint: h.signingKey.Fingerprint,
		Armor:       encoding.Base64Encode(sigArmor),
		SignedAt:    ts,
	}, nil
}

// /////////// //
//   Structs   //
// /////////// //

type Handlers struct {
	services      *Services
	cfg           AppConfig
	broadcastChan chan<- realtime.BroadcastMessage
	signingKey    ServerSigningKey
	metrics       metrics.Recorder
	// filterPipeTags keeps only tags with current pipe listeners (SignReed stash).
	// Nil means stash all extracted tags (tests / no realtime).
	filterPipeTags func([]string) []string
	kickUserWS     func(userID string)
	// federationHTTPClientOverride lets tests substitute a client that
	// trusts an httptest.NewTLSServer's certificate. Nil means production
	// default (see federationHTTPClient).
	federationHTTPClientOverride *http.Client
	// realtimeRelay backs the cross-server REQUEST_REED relay's HTTP
	// endpoints (federation_relay.go) — they need to touch pending_events/
	// reed_allocations/the WS connection registry, which only exists in
	// the realtime package.
	realtimeRelay *realtime.RealtimeService
}

type ServerInfo struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	RecoveryMode      bool   `json:"recoveryMode"`
	SignupMode        string `json:"signupMode"`
	MaxInvitesPerUser int    `json:"maxInvitesPerUser"` // -1 = infinite
	// ServerKeyID is this server's own current signing key's canonical id
	// (fingerprint@serverID) — clients check their local publicKeys cache
	// for it and, on a miss, fetch it via GET /server/key.
	ServerKeyID string `json:"serverKeyId"`
}

// ///////////// //
//   Utilities  //
// ///////////// //

func NewHandlers(services *Services, cfg AppConfig, broadcastChan chan<- realtime.BroadcastMessage, signingKey ServerSigningKey) *Handlers {
	return &Handlers{
		services:      services,
		cfg:           cfg,
		broadcastChan: broadcastChan,
		signingKey:    signingKey,
		metrics:       metrics.Noop{},
	}
}

// SetMetrics installs the business-metrics recorder (no-op when observability is off).
func (h *Handlers) SetMetrics(rec metrics.Recorder) {
	if rec == nil {
		h.metrics = metrics.Noop{}
		return
	}
	h.metrics = rec
}

// SetPipeTagFilter installs the SignReed hook that intersects extracted tags
// with live pipe subscriptions.
func (h *Handlers) SetPipeTagFilter(filter func([]string) []string) {
	h.filterPipeTags = filter
}

func (h *Handlers) SetKickUserWS(kick func(userID string)) {
	h.kickUserWS = kick
}

// SetRealtimeRelay installs the RealtimeService the cross-server
// REQUEST_REED relay endpoints (federation_relay.go) call into.
func (h *Handlers) SetRealtimeRelay(rs *realtime.RealtimeService) {
	h.realtimeRelay = rs
}

func writeResponse(w http.ResponseWriter, statusCode int, message any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	json.NewEncoder(w).Encode(message)
}

func internalServerError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte("Internal Server Error"))
}

func parseFormData(r *http.Request) (url.Values, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}

	return values, nil
}

func (h *Handlers) getUserID(r *http.Request) string {
	// First try to get user ID from context (set by signature auth middleware)
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		panic("userID not found in context")
	}

	return userID
}

// //////////// //
//   Handlers   //
// //////////// //

// noop handler is used for CORS preflight requests. The CORSMiddleware will
// set the appropriate headers.
func (h *Handlers) noop(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) GetServerInfo(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, http.StatusOK, ServerInfo{
		ID:                h.services.db.GetServerID(),
		Name:              h.cfg.ServerName,
		RecoveryMode:      h.cfg.RecoveryMode,
		SignupMode:        h.cfg.SignupMode,
		MaxInvitesPerUser: h.cfg.MaxInvitesPerUser,
		ServerKeyID:       string(identity.CanonicalID(h.services.db.GetServerID(), h.signingKey.Fingerprint)),
	})
}

// GetServerPublicKey returns the armored public half of a server signing
// key by fingerprint. Clients use this to select the historical key that
// produced a countersignature (keys, reeds, identity records).
// GetKey handles GET /keys/{id}: the single fetch-any-key route. id is the
// full canonical key id — "userID@serverID/fingerprint" for a user key,
// "fingerprint@serverID" for a server's own key — for any server, local or
// federated. A foreign id (embedded serverID != this server's own) is
// proxied live to that peer (see proxyToPeer) rather than looked up
// locally, since public_keys only ever holds local keys.
func (h *Handlers) GetKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	id := mux.Vars(r)["id"]
	if id == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	if handled, _ := h.proxyIfForeign(w, r, id); handled {
		return
	}

	key, err := h.services.db.GetPublicKey(r.Context(), id)
	if err != nil {
		log.Error().Str("id", id).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if key == nil {
		writeResponse(w, http.StatusNotFound, "Key not found")
		return
	}

	key.Armor = encoding.Base64Encode(key.Armor)
	writeResponse(w, http.StatusOK, key)
}

// GetServerKey returns THIS server's own current signing key armor as
// plain text — the only key that's public verification material (anyone
// validating a countersignature must be able to fetch it without being
// signed in, even mid-rotation with a revoked user key of their own; see
// signatureAuthMiddleware's excludePaths). Deliberately takes no {id} path
// param and never resolves anything else, so there's no id/URL shape for
// an unauthenticated caller to manipulate — unlike GetKey, which serves
// both user keys and (via proxying) peer server keys and stays
// authenticated. Served straight from the in-memory signing key (no DB
// round-trip, no base64/JSON envelope) since this route only ever returns
// the one thing.
func (h *Handlers) GetServerKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetServerKey request received")
	if h.signingKey.Armor == "" {
		log.Error().Msg("GetServerKey: no signing key loaded")
		internalServerError(w)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(h.signingKey.Armor))
}

func (h *Handlers) Signup(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("Signup request received")

	if h.cfg.RecoveryMode {
		writeResponse(w, http.StatusForbidden, "Signups are closed while this server is in recovery mode")
		return
	}

	if invites.SignupMode(h.cfg.SignupMode) == invites.ModeClosed {
		writeResponse(w, http.StatusForbidden, "Signups are closed on this server")
		return
	}

	values, err := parseFormData(r)
	if err != nil {
		log.Error().Err(err).Msg("Error parsing form data")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	username := trimInvisibleChars(values.Get("username"))
	if username == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `username` is required")
		return
	}
	if len(username) > 32 {
		writeResponse(w, http.StatusBadRequest, "Username cannot exceed 32 characters")
		return
	}

	deviceID, err := identity.ParseDeviceID(r.Header.Get("X-Syrinx-Device-Id"))
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Missing or invalid X-Syrinx-Device-Id header")
		return
	}

	publicKeyB64 := values.Get("publicKey")
	if publicKeyB64 == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `publicKey` is required")
		return
	}
	publicKey, err := encoding.Base64Decode(publicKeyB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid publicKey encoding")
		return
	}

	signatureB64 := values.Get("signature")
	if signatureB64 == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}
	signatureArmor, err := encoding.Base64Decode(signatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}

	userSignatureB64 := values.Get("userSignature")
	if userSignatureB64 == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userSignature` is required")
		return
	}

	userID := strings.TrimSpace(values.Get("userID"))
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}
	if userID == roles.RootUserID {
		writeResponse(w, http.StatusBadRequest, "userID is reserved")
		return
	}
	if !crypto.IsValidID(userID) {
		writeResponse(w, http.StatusBadRequest, "Invalid userID")
		return
	}

	userIDSigB64 := values.Get("userIDSignature")
	if userIDSigB64 == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userIDSignature` is required")
		return
	}
	userIDSigArmor, err := encoding.Base64Decode(userIDSigB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid userIDSignature encoding")
		return
	}

	userIDFingerprint := strings.TrimSpace(values.Get("userIDFingerprint"))
	if userIDFingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userIDFingerprint` is required")
		return
	}

	inviteID := strings.TrimSpace(values.Get("inviteID"))
	// inviteCreatorID arrives already in "userID@serverID" form.
	// GetPendingInvite composes identity.CanonicalID internally and expects
	// bare, so decode only for that call.
	inviteCreatorID := strings.TrimSpace(values.Get("inviteCreatorID"))
	inviteCreatorBare := ""
	if inviteCreatorID != "" {
		if bare, _, ok := identity.ParseIdentityID(identity.IdentityID(inviteCreatorID)); ok {
			inviteCreatorBare = bare
		}
	}
	inviteSecret := strings.TrimSpace(values.Get("inviteSecret"))
	invite, err := h.services.db.GetPendingInvite(r.Context(), inviteCreatorBare, inviteID, inviteSecret)
	if err != nil {
		log.Error().Err(err).Msg("Failed to look up invite")
		internalServerError(w)
		return
	}
	resolved, err := invites.ResolveSignup(
		invites.SignupMode(h.cfg.SignupMode),
		inviteID,
		inviteCreatorID,
		inviteSecret,
		invite,
	)
	if err != nil {
		if errors.Is(err, invites.ErrInviteRequired) {
			writeResponse(w, http.StatusForbidden, "Invite required")
			return
		}
		if errors.Is(err, invites.ErrInvalidInvite) {
			writeResponse(w, http.StatusForbidden, "Invalid or claimed invite")
			return
		}
		log.Error().Err(err).Msg("Invite policy error")
		internalServerError(w)
		return
	}

	serverPubKey, err := h.services.db.GetServerPublicKeyByFingerprint(r.Context(), userIDFingerprint)
	if err != nil || serverPubKey == "" {
		log.Error().
			Str("userIDFingerprint", userIDFingerprint).
			Err(err).
			Msg("Failed to load server public key for userID verification")
		internalServerError(w)
		return
	}
	if err := h.services.crypto.VerifySignature(userID, userIDSigArmor, serverPubKey); err != nil {
		log.Error().Err(err).Msg("userID signature verification failed")
		writeResponse(w, http.StatusBadRequest, "userID signature verification failed")
		return
	}

	exists, err := h.services.db.UsernameExists(r.Context(), username)
	if err != nil {
		log.Error().
			Str("username", username).
			Err(err).Msg("Failed to check username")
		internalServerError(w)
		return
	}
	if exists {
		writeResponse(w, http.StatusBadRequest, "Username already exists")
		return
	}

	// Validate the self-signature over the public key AND extract the
	// bare fingerprint / creation time / expiry from the armored key.
	// This must happen before we can build the user identity payload,
	// since the payload binds the fingerprint.
	key, err := h.services.crypto.ValidateAndExtractPublicKey(publicKey, signatureArmor)
	if err != nil {
		log.Error().Err(err).Msg("Error validating public key")
		writeResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	// GetUserProfile/GetPublicKey return this user in userID@serverID form,
	// so the signed payloads must sign that same form or client-side
	// verification will rebuild different bytes than what was signed.
	// Computed up front (before verifying userSignature) because the
	// canonical key fingerprint the client signed over is built from it.
	selfIdentity := identity.CanonicalID(h.services.db.GetServerID(), userID)
	canonicalFingerprint := identity.AppendEntity(selfIdentity, key.Fingerprint)

	// Reconstruct the exact bytes the client claims to have signed. At
	// signup bio is empty — a user cannot set it before their account
	// exists.
	userPayload := identity.BuildUserIdentityPayload(username, string(canonicalFingerprint), "")

	// userSignature travels as base64(armored PGP). Decode once and hand
	// the armor to VerifySignature.
	userSigArmor, err := encoding.Base64Decode(userSignatureB64)
	if err != nil {
		log.Error().Err(err).Msg("Invalid userSignature encoding")
		writeResponse(w, http.StatusBadRequest, "Invalid userSignature encoding")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), userSigArmor, publicKey); err != nil {
		log.Error().Err(err).Msg("userSignature verification failed")
		writeResponse(w, http.StatusBadRequest, "userSignature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)

	inviteGrantedRole := ""
	hasInvite := invite != nil && resolved.InviteID != ""
	if hasInvite {
		inviteGrantedRole = invite.GrantedRole
	}
	signupRole := roles.SignupRole(userID, inviteGrantedRole, hasInvite, h.services.db.GetServerID())

	profilePayload := identity.BuildNewProfilePayload(
		string(selfIdentity),
		username,
		string(canonicalFingerprint),
		h.services.db.GetServerID(),
		h.signingKey.Fingerprint,
		userSignatureB64,
		resolved.InviterID,
		signupRole,
		now,
	)
	// Server signature over the user's brand new profile
	profileSignature, err := h.countersign(profilePayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing server signature")
		internalServerError(w)
		return
	}

	// Server signature over the user's public key
	keyPayload := identity.BuildPublicKeyPayload(
		h.services.db.GetServerID(),
		string(selfIdentity),
		string(canonicalFingerprint),
		h.signingKey.Fingerprint,
		publicKey,
		now,
	)
	keySignature, err := h.countersign(keyPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error signing public key")
		internalServerError(w)
		return
	}

	user, err := h.services.db.Signup(r.Context(), SignupInput{
		UserID:             userID,
		Username:           username,
		PublicKeyArmor:     publicKey,
		Fingerprint:        string(canonicalFingerprint),
		KeyCreatedAt:       key.CreatedAt,
		UserSignatureB64:   userSignatureB64,
		MemberSince:        now,
		ProfileSignature:   profileSignature,
		PublicKeySignature: keySignature,
		Invite:             invite,
		DeviceID:           deviceID,
	})
	if err != nil {
		if errors.Is(err, invites.ErrInvalidInvite) {
			writeResponse(w, http.StatusForbidden, "Invalid or claimed invite")
			return
		}
		// Username race → 400. A userID collision (or anything else) is a
		// 500; the client retries signup with a fresh random ID.
		if errors.Is(err, ErrUsernameTaken) {
			log.Info().Str("username", username).Msg("Username already exists")
			writeResponse(w, http.StatusBadRequest, "Username already exists")
			return
		}
		log.Error().Err(err).Msg("Failed to create user '" + username + "'")
		internalServerError(w)
		return
	}

	log.Info().
		Str("userID", user.ID).
		Str("username", username).
		Str("fingerprint", string(canonicalFingerprint)).
		Msg("Identity record created")

	h.metrics.UserCreated(r.Context(), h.cfg.SignupMode, user.ID)

	writeResponse(w, http.StatusCreated, user)
}

// GenerateUserID returns a fresh random user ID signed by the server.
// The client uses this before key generation so the OpenPGP identity can
// embed userID@serverID. The ID and signature are ephemeral — nothing is
// stored in DB.
func (h *Handlers) GenerateUserID(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	userID, err := generateUserID()
	if err != nil {
		log.Error().Err(err).Msg("Error generating userID")
		internalServerError(w)
		return
	}

	sig, err := h.services.crypto.Sign(userID, h.signingKey.Armor)
	if err != nil {
		log.Error().Err(err).Msg("Error signing userID")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, map[string]string{
		"userID":      userID,
		"signature":   encoding.Base64Encode(sig),
		"fingerprint": h.signingKey.Fingerprint,
	})
}

func (h *Handlers) CheckUsername(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("CheckUsername request received")

	if h.cfg.RecoveryMode {
		writeResponse(w, http.StatusForbidden, "Signups are closed while this server is in recovery mode")
		return
	}

	if invites.SignupMode(h.cfg.SignupMode) == invites.ModeClosed {
		writeResponse(w, http.StatusForbidden, "Signups are closed on this server")
		return
	}

	values, username, ok := h.parseCheckUsernameForm(w, r, log)
	if !ok {
		return
	}

	inviteID := strings.TrimSpace(values.Get("inviteID"))
	// Same inviteCreatorID handling as CreateAccount's identical block
	// above — see that call site's comment.
	inviteCreatorID := strings.TrimSpace(values.Get("inviteCreatorID"))
	inviteCreatorBare := ""
	if inviteCreatorID != "" {
		if bare, _, ok := identity.ParseIdentityID(identity.IdentityID(inviteCreatorID)); ok {
			inviteCreatorBare = bare
		}
	}
	inviteSecret := strings.TrimSpace(values.Get("inviteSecret"))
	invite, err := h.services.db.GetPendingInvite(r.Context(), inviteCreatorBare, inviteID, inviteSecret)
	if err != nil {
		log.Error().Err(err).Msg("Failed to look up invite")
		internalServerError(w)
		return
	}
	if _, err := invites.ResolveSignup(
		invites.SignupMode(h.cfg.SignupMode),
		inviteID,
		inviteCreatorID,
		inviteSecret,
		invite,
	); err != nil {
		if errors.Is(err, invites.ErrInviteRequired) {
			writeResponse(w, http.StatusForbidden, "Invite required")
			return
		}
		if errors.Is(err, invites.ErrInvalidInvite) {
			writeResponse(w, http.StatusForbidden, "Invalid or claimed invite")
			return
		}
		log.Error().Err(err).Msg("Invite policy error")
		internalServerError(w)
		return
	}

	h.respondUsernameAvailability(w, r, log, username)
}

// CheckUsernameForRename handles POST /api/users/me/check-username — the
// authenticated counterpart to CheckUsername. An already-logged-in user
// checking a prospective rename on the profile page is not signing up, so
// none of the signup gates (recovery mode, signup mode, invite requirement)
// apply here; this only ever checks availability for a caller who is
// already an existing account.
func (h *Handlers) CheckUsernameForRename(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("CheckUsernameForRename request received")

	h.getUserID(r) // require an authenticated caller; panics via middleware contract otherwise

	_, username, ok := h.parseCheckUsernameForm(w, r, log)
	if !ok {
		return
	}

	h.respondUsernameAvailability(w, r, log, username)
}

// parseCheckUsernameForm parses and validates the `username` form field
// shared by CheckUsername and CheckUsernameForRename, writing the
// appropriate error response and returning ok=false on failure.
func (h *Handlers) parseCheckUsernameForm(w http.ResponseWriter, r *http.Request, log *zerolog.Logger) (url.Values, string, bool) {
	values, err := parseFormData(r)
	if err != nil {
		log.Error().Err(err).Msg("Error parsing form data")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return nil, "", false
	}

	username := trimInvisibleChars(values.Get("username"))
	if username == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `username` is required")
		return nil, "", false
	}

	if len(username) > 32 {
		writeResponse(w, http.StatusBadRequest, "Username cannot exceed 32 characters")
		return nil, "", false
	}

	return values, username, true
}

// respondUsernameAvailability checks username availability and writes the
// shared 200/409/500 response, used by both CheckUsername and
// CheckUsernameForRename after their distinct gating logic.
func (h *Handlers) respondUsernameAvailability(w http.ResponseWriter, r *http.Request, log *zerolog.Logger, username string) {
	exists, err := h.services.db.UsernameExists(r.Context(), username)
	if err != nil {
		log.Error().
			Str("username", username).
			Err(err).Msg("Failed to get user by username")
		internalServerError(w)
		return
	}

	if exists {
		writeResponse(w, http.StatusConflict, "Username is taken")
		return
	}

	writeResponse(w, http.StatusOK, "Username is available")
}

// UserStatus handles POST /api/users/status. Unauthenticated probe: client
// sends a countersigned profile; server verifies its own countersignature and
// reports claimed / unclaimed / mid-recovery state.
func (h *Handlers) UserStatus(w http.ResponseWriter, r *http.Request) {
	var profile recovery.Profile
	if err := json.NewDecoder(r.Body).Decode(&profile); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if profile.ID == "" || profile.Username == "" || profile.UserSignature.Fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "profile id, username, and userSignature.fingerprint are required")
		return
	}

	// Profile must carry this server's countersignature (wrong serverID or
	// bad/missing sig → 400).
	if err := recovery.VerifyProfileServerCountersig(
		r.Context(),
		profile,
		h.services.db.GetServerID(),
		func(ctx context.Context, fp string) (string, error) {
			return h.services.db.GetServerPublicKeyByFingerprint(ctx, fp)
		},
		h.services.crypto,
	); err != nil {
		writeResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	// Peer-seeded only (in unclaimed_accounts) → unknown. Implies a users
	// row exists; no need to read users yet.
	unclaimed, err := h.services.db.IsUnclaimed(r.Context(), profile.ID)
	if err != nil {
		internalServerError(w)
		return
	}
	if unclaimed {
		writeResponse(w, http.StatusNotFound, recovery.UserStatusUnknownResponse)
		return
	}

	// No users row → unknown.
	signedAt, err := h.services.db.UserServerSignedAt(r.Context(), profile.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeResponse(w, http.StatusNotFound, recovery.UserStatusUnknownResponse)
			return
		}
		internalServerError(w)
		return
	}

	// Submitted profile older than the claimed DB record → reject (would
	// fork the identity / key chain).
	submittedAt := profile.ServerSignature.Timestamp.UTC().Truncate(time.Second)
	if submittedAt.Before(signedAt) {
		writeResponse(w, http.StatusBadRequest, "stale profile: backup is older than the server record")
		return
	}

	// Claimed and mid-import → ongoing.
	ongoing, err := h.services.db.IsOngoing(r.Context(), profile.ID)
	if err != nil {
		internalServerError(w)
		return
	}
	if ongoing {
		writeResponse(w, http.StatusConflict, recovery.UserStatusOngoingResponse)
		return
	}

	// Claimed, not mid-import, profile not older than DB → complete.
	writeResponse(w, http.StatusOK, recovery.UserStatusCompleteResponse)
}

func (h *Handlers) GetUserProfile(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUserProfile request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	if handled, status := h.proxyIfForeign(w, r, userID); handled {
		h.rememberRemoteIdentityOnSuccess(r.Context(), log, userID, status)
		return
	}

	removal, err := h.services.db.GetAccountRemoval(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading account removal")
		internalServerError(w)
		return
	}
	if removal != nil {
		writeResponse(w, http.StatusGone, h.accountRemovalWire(removal))
		return
	}

	user, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error getting user profile")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusNotFound, "User not found")
		return
	}

	writeResponse(w, http.StatusOK, user)
}

func (h *Handlers) GetUserInfo(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUserInfo request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	if handled, status := h.proxyIfForeign(w, r, userID); handled {
		h.rememberRemoteIdentityOnSuccess(r.Context(), log, userID, status)
		return
	}

	removal, err := h.services.db.GetAccountRemoval(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading account removal")
		internalServerError(w)
		return
	}
	if removal != nil {
		writeResponse(w, http.StatusGone, h.accountRemovalWire(removal))
		return
	}

	info, err := h.services.db.GetUserInfo(r.Context(), userID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error getting user info")
		internalServerError(w)
		return
	}
	if info == nil {
		writeResponse(w, http.StatusNotFound, "User not found")
		return
	}

	writeResponse(w, http.StatusOK, info)
}

// searchUsersFanoutTimeout bounds each peer's search-users call — this
// runs on a live user's typing path (composer @ picker), so a slow or
// unreachable peer must never make the whole search feel broken. A peer
// that doesn't answer in time is just dropped from that request's results.
const searchUsersFanoutTimeout = 2 * time.Second

// SearchUsers handles GET /users/search?q=&limit= — the composer @-mention
// picker's backing search. Auth required (not in signatureAuthMiddleware's
// excludePaths); minimal fields only, no keys. Fans out to every connected
// peer (leg 19, search-users) in parallel and merges their results with
// this server's own local matches, so the picker can find users on any
// server in the mesh, not just this one.
func (h *Handlers) SearchUsers(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeResponse(w, http.StatusOK, map[string]any{"users": []UserSearchResult{}})
		return
	}

	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}

	localResults, err := h.services.db.SearchUsers(r.Context(), query, limit)
	if err != nil {
		log.Error().Str("query", query).Err(err).Msg("Error searching users")
		internalServerError(w)
		return
	}

	foreignResults := h.fanoutUserSearchToPeers(r.Context(), query, limit, searchUsersFanoutTimeout)
	results := mergeUserSearchResults(query, localResults, foreignResults)

	writeResponse(w, http.StatusOK, map[string]any{"users": results})
}

// mergeUserSearchResults combines local and fanned-out foreign results.
// If no LOCAL result is an exact (case-insensitive) username match, any
// foreign exact matches are moved to the front — the assumption being
// that a searcher typing a full, exact username most likely means the
// specific person they're already looking for, and that intent shouldn't
// get buried under partial local matches when the actual target lives on
// another server. When a local exact match already exists, no reordering
// happens at all — local-then-foreign order is left as-is.
func mergeUserSearchResults(query string, local, foreign []UserSearchResult) []UserSearchResult {
	for _, r := range local {
		if strings.EqualFold(r.Username, query) {
			merged := make([]UserSearchResult, 0, len(local)+len(foreign))
			merged = append(merged, local...)
			merged = append(merged, foreign...)
			return merged
		}
	}

	var exact, rest []UserSearchResult
	for _, r := range foreign {
		if strings.EqualFold(r.Username, query) {
			exact = append(exact, r)
		} else {
			rest = append(rest, r)
		}
	}
	merged := make([]UserSearchResult, 0, len(local)+len(foreign))
	merged = append(merged, exact...)
	merged = append(merged, local...)
	merged = append(merged, rest...)
	return merged
}

// proxyFollowIfForeign forwards a local end-user's follow/unfollow to the
// peer owning userID, if foreign, waiting for confirmation.
func (h *Handlers) proxyFollowIfForeign(w http.ResponseWriter, r *http.Request, method, userID string) (handled bool, status int) {
	followerID, isUser := r.Context().Value(userIDKey).(string)
	if !isUser {
		return false, 0
	}
	_, embeddedServerID, ok := identity.ParseIdentityID(identity.IdentityID(userID))
	if !ok || embeddedServerID == h.services.db.GetServerID() {
		return false, 0
	}

	log := h.services.log.GetLogger(r.Context())
	peer, err := h.services.db.GetServerByID(r.Context(), embeddedServerID)
	if err != nil {
		internalServerError(w)
		return true, 0
	}
	if peer == nil {
		writeResponse(w, http.StatusNotFound, "Not found")
		return true, 0
	}

	if err := h.forwardFollowToPeer(r.Context(), method, peer.BaseURL, userID, followerID); err != nil {
		log.Error().Str("userID", userID).Str("followerID", followerID).Err(err).Msg("Failed to forward follow to peer")
		h.logFederationServerAsync(peer.ID, "error", fmt.Sprintf("Follow forward to %s failed: %s", peer.BaseURL, err.Error()))
		writeResponse(w, http.StatusBadGateway, "Failed to reach peer server")
		return true, 0
	}

	w.WriteHeader(http.StatusNoContent)
	return true, http.StatusNoContent
}

// resolveFollower returns who's following: an end-user's own session, or
// a peer vouching for one of its users via a followerID form field. Reads
// the body directly since r.FormValue skips DELETE.
func (h *Handlers) resolveFollower(r *http.Request) (followerID string, ok bool) {
	if userID, isUser := r.Context().Value(userIDKey).(string); isUser {
		return userID, true
	}
	peerServerID, isPeer := r.Context().Value(peerServerIDKey).(string)
	if !isPeer {
		return "", false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", false
	}
	followerID = strings.TrimSpace(values.Get("followerID"))
	_, embeddedServerID, parseOK := identity.ParseIdentityID(identity.IdentityID(followerID))
	if !parseOK || embeddedServerID != peerServerID {
		return "", false
	}
	return followerID, true
}

// resolveActingUser returns who's acting: an end-user's own session, or a
// peer vouching for one of its users via candidateID (already extracted
// from the request body by the caller — LikeReed's form field / PostRipple's
// JSON field, whose parsing differs per handler, unlike resolveFollower's
// single form-encoded shape).
func (h *Handlers) resolveActingUser(r *http.Request, candidateID string) (userID string, ok bool) {
	if userID, isUser := r.Context().Value(userIDKey).(string); isUser {
		return userID, true
	}
	peerServerID, isPeer := r.Context().Value(peerServerIDKey).(string)
	if !isPeer {
		return "", false
	}
	candidateID = strings.TrimSpace(candidateID)
	_, embeddedServerID, parseOK := identity.ParseIdentityID(identity.IdentityID(candidateID))
	if !parseOK || embeddedServerID != peerServerID {
		return "", false
	}
	return candidateID, true
}

func (h *Handlers) FollowUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	userID := mux.Vars(r)["userID"]

	if handled, status := h.proxyFollowIfForeign(w, r, http.MethodPost, userID); handled {
		if status == http.StatusNoContent {
			followerID, _ := h.resolveFollower(r)
			h.rememberRemoteIdentityAndFollowLocally(r.Context(), log, followerID, userID)
		}
		return
	}

	followerID, ok := h.resolveFollower(r)
	if !ok {
		writeResponse(w, http.StatusBadRequest, "Argument `followerID` is required")
		return
	}
	if followerID == userID {
		writeResponse(w, http.StatusBadRequest, "Cannot follow yourself")
		return
	}

	if _, isPeer := r.Context().Value(peerServerIDKey).(string); isPeer {
		h.upsertRemoteIdentity(r.Context(), log, followerID)
		if err := h.services.db.RecordRemoteFollower(r.Context(), userID, followerID); err != nil {
			log.Error().Str("followerID", followerID).Str("userID", userID).Err(err).Msg("Error recording remote follower")
			internalServerError(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.services.db.FollowUser(r.Context(), followerID, userID); err != nil {
		if errors.Is(err, ErrFollowTargetNotFound) {
			writeResponse(w, http.StatusNotFound, "User not found")
			return
		}
		log.Error().Str("followerID", followerID).Str("userID", userID).Err(err).Msg("Error following user")
		internalServerError(w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) UnfollowUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	userID := mux.Vars(r)["userID"]

	if handled, _ := h.proxyFollowIfForeign(w, r, http.MethodDelete, userID); handled {
		return
	}

	followerID, ok := h.resolveFollower(r)
	if !ok {
		writeResponse(w, http.StatusBadRequest, "Argument `followerID` is required")
		return
	}

	if _, isPeer := r.Context().Value(peerServerIDKey).(string); isPeer {
		if err := h.services.db.RemoveRemoteFollower(r.Context(), userID, followerID); err != nil {
			log.Error().Str("followerID", followerID).Str("userID", userID).Err(err).Msg("Error removing remote follower")
			internalServerError(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.services.db.UnfollowUser(r.Context(), followerID, userID); err != nil {
		log.Error().Str("followerID", followerID).Str("userID", userID).Err(err).Msg("Error unfollowing user")
		internalServerError(w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) DeleteMe(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("DeleteMe (account removal) request received")

	userID := h.getUserID(r)
	if userID == "" {
		writeResponse(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	values, err := parseFormData(r)
	if err != nil {
		log.Error().Err(err).Msg("Error parsing form")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	userSignatureB64 := strings.TrimSpace(values.Get("signature"))
	if userSignatureB64 == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}
	note := values.Get("note")
	if err := deletion.ValidateAccountNote(note); err != nil {
		writeResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	serverID := h.services.db.GetServerID()

	existing, err := h.services.db.GetAccountRemoval(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading account removal")
		internalServerError(w)
		return
	}
	if existing != nil {
		if existing.UserSignature != userSignatureB64 || existing.Note != note {
			writeResponse(w, http.StatusConflict, "Account removal already exists with a different attestation")
			return
		}
		writeResponse(w, http.StatusOK, h.accountRemovalWire(existing))
		return
	}

	user, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusNotFound, "User not found")
		return
	}

	fingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), userID)
	if err != nil || fingerprint == "" {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading active key fingerprint")
		internalServerError(w)
		return
	}
	userPayload := identity.BuildAccountRemovalUserPayload(serverID, userID, note)
	userSigArmor, err := encoding.Base64Decode(userSignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(r.Context(), fingerprint)
	if err != nil {
		log.Error().Str("userID", userID).Str("fingerprint", fingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("account removal signature verification failed")
		writeResponse(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	serverPayload := identity.BuildAccountRemovalServerPayload(
		serverID, userID, note,
		h.signingKey.Fingerprint, userSignatureB64, now,
	)
	serverSignature, err := h.countersign(serverPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing account-removal countersignature")
		internalServerError(w)
		return
	}

	cert := deletion.AccountCert{
		UserID:            userID,
		Note:              note,
		UserSignature:     userSignatureB64,
		UserFingerprint:   fingerprint,
		ServerSignature:   serverSignature.Armor,
		ServerFingerprint: serverSignature.Fingerprint,
		ServerSignedAt:    serverSignature.SignedAt,
	}
	if err := h.services.db.InsertAccountRemoval(r.Context(), cert); err != nil {
		if errors.Is(err, deletion.ErrConflict) {
			existing, getErr := h.services.db.GetAccountRemoval(r.Context(), userID)
			if getErr == nil && existing != nil && existing.UserSignature == userSignatureB64 && existing.Note == note {
				writeResponse(w, http.StatusOK, h.accountRemovalWire(existing))
				return
			}
			writeResponse(w, http.StatusConflict, "Account removal already exists with a different attestation")
			return
		}
		log.Error().Str("userID", userID).Err(err).Msg("Error storing account removal")
		internalServerError(w)
		return
	}

	noteHas := strings.TrimSpace(note) != ""
	h.metrics.UserDeleted(r.Context(), userID, noteHas)

	if err := h.services.db.DeleteMentionsByAuthor(r.Context(), userID); err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error clearing mention index for removed account")
	}

	affectedTargets, err := h.services.db.DeleteEchoesByAuthor(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error clearing echo index for removed account")
	} else {
		for _, t := range affectedTargets {
			h.broadcastChan <- realtime.BroadcastMessage{
				Type:   realtime.EchoCountChanged,
				UserID: t.CanonicalAuthorID(),
				ReedID: t.ReedID,
			}
		}
	}

	threadTargets, err := h.services.db.ReplyCountNotifyTargetsForAuthor(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error resolving reply count targets for removed account")
	} else {
		for _, t := range threadTargets {
			h.broadcastChan <- realtime.BroadcastMessage{
				Type:   realtime.ReplyCountChanged,
				UserID: t.CanonicalAuthorID(),
				ReedID: t.ReedID,
			}
		}
	}

	wire := realtime.NewAccountRemovalWire(serverID, &cert)
	h.broadcastChan <- realtime.BroadcastMessage{
		Type:           realtime.AccountRemoved,
		ServerID:       serverID,
		UserID:         userID,
		AccountRemoval: &wire,
	}

	log.Info().Str("userID", userID).Msg("Account removal accepted")
	writeResponse(w, http.StatusOK, h.accountRemovalWire(&cert))
}

func (h *Handlers) accountRemovalWire(cert *deletion.AccountCert) AccountRemoval {
	return AccountRemoval{
		Type:     identity.TypeAccount,
		ServerID: h.services.db.GetServerID(),
		UserID:   cert.UserID,
		Note:     cert.Note,
		UserSignature: UserSignature{
			Fingerprint: cert.UserFingerprint,
			Armor:       cert.UserSignature,
		},
		ServerSignature: ServerSignature{
			ServerID:    h.services.db.GetServerID(),
			Fingerprint: cert.ServerFingerprint,
			Armor:       cert.ServerSignature,
			SignedAt:    cert.ServerSignedAt,
		},
	}
}

// UpdateUser mints a fresh signed identity record for an authenticated
// user editing their own profile. Full-replacement semantics: the
// request MUST carry the complete post-edit tuple (username, bio) plus
// `userSignature`, a base64(armored PGP) detached signature over
// `identity.BuildUserIdentityPayload(username, fingerprint, bio)` where
// `fingerprint` is the caller's active user key.
//
// The client is expected to skip the network call entirely when nothing
// changed. As a defence against clients that don't (or against probes),
// the server treats byte-equality between the submitted `userSignature`
// and the row's stored user attestation as the authoritative "did
// anything change?" test. A valid detached signature deterministically
// binds a specific (username, fingerprint, bio) tuple under a specific
// key, so equal signature bytes ⇒ equal signed bytes ⇒ equal fields. In
// that case the server short-circuits: no re-verify, no new signedAt, no
// new server signature, no realtime broadcast, just return the current
// record.
//
// On a real change: validate the submitted fields, verify
// `userSignature` against the caller's active public key, mint a fresh
// signedAt, countersign the server payload, persist profile fields plus
// new signature rows/FKs, broadcast, and return the fresh identity record.
func (h *Handlers) UpdateUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("UpdateUser request received")

	userID := h.getUserID(r)

	// Load the caller's current row up front. Needed for the no-op
	// fast path (compare stored userSignature), for the signed
	// fingerprint (which the client also knows but the server must
	// re-derive to avoid trusting caller-supplied fingerprints), and
	// for createdAt (pinned across every record produced by this
	// user — signup sets it, updates carry it forward).
	currentUser, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if currentUser == nil {
		writeResponse(w, http.StatusBadRequest, "User not found")
		return
	}

	userSignatureB64 := r.FormValue("userSignature")
	if userSignatureB64 == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userSignature` is required")
		return
	}

	// No-op fast path. See doc comment on this function for why byte
	// equality on the signature is a sufficient change detector.
	if userSignatureB64 == currentUser.UserSignature.Armor {
		log.Info().
			Str("userID", userID).
			Msg("UpdateUser no-op (signature unchanged)")
		writeResponse(w, http.StatusOK, currentUser)
		return
	}

	username := trimInvisibleChars(r.FormValue("username"))
	if username == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `username` is required")
		return
	}
	if len(username) > 32 {
		writeResponse(w, http.StatusBadRequest, "Username cannot exceed 32 characters")
		return
	}

	if currentUser.Username != username {
		exists, err := h.services.db.UsernameExists(r.Context(), username)
		if err != nil {
			log.Error().
				Str("userID", userID).
				Str("username", username).
				Err(err).Msg("Error checking if username exists")
			internalServerError(w)
			return
		}
		if exists {
			log.Info().
				Str("userID", userID).
				Str("username", username).
				Msg("Username already taken")
			writeResponse(w, http.StatusBadRequest, "Username already taken")
			return
		}
	}

	bio := r.FormValue("bio")
	if CountMarkdownCharacters(bio) > MaxReedVisibleChars {
		log.Error().
			Str("userID", userID).
			Int("length", CountMarkdownCharacters(bio)).
			Msg("Bio cannot exceed 140 visible characters")
		writeResponse(w, http.StatusBadRequest, "Bio cannot exceed 140 characters")
		return
	}

	// Reconstruct the exact bytes the client claims to have signed,
	// using the fingerprint we trust (from the row) rather than one
	// supplied by the caller. Then verify.
	//
	// The client signs with the currently-active key (users.user_fingerprint).
	fingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), userID)
	if err != nil || fingerprint == "" {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading active key fingerprint")
		internalServerError(w)
		return
	}
	userPayload := identity.BuildUserIdentityPayload(username, fingerprint, bio)

	userSigArmor, err := encoding.Base64Decode(userSignatureB64)
	if err != nil {
		log.Error().Err(err).Msg("Invalid userSignature encoding")
		writeResponse(w, http.StatusBadRequest, "Invalid userSignature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(r.Context(), fingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Err(err).Msg("Error loading user public key")
		internalServerError(w)
		return
	}
	if pubKey == nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Msg("Active public key not found for user")
		internalServerError(w)
		return
	}
	// Refuse to accept a new identity record signed by a revoked key.
	//
	// The middleware already rejects revoked-key request signatures, but
	// UpdateUser additionally verifies the *payload signature* embedded in
	// the identity record — the artifact that propagates to followers and
	// survives on client devices. An attacker who somehow slipped past the
	// transport-level check (misconfiguration, future refactor, request
	// forwarded through a trusted internal path) must still not be able to
	// mint a new signed identity record with a revoked key. Existing
	// records signed while the key was active remain valid as history;
	// what is forbidden is producing a *new* one.
	if pubKey.Revoked {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Msg("UpdateUser rejected: identity record signed by revoked key")
		writeResponse(w, http.StatusUnauthorized, "Key is revoked")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("userSignature verification failed")
		writeResponse(w, http.StatusBadRequest, "userSignature verification failed")
		return
	}

	// Mint the server-authored fields and countersign. createdAt
	// stays pinned to the value set at signup; only signedAt advances.
	// invitedBy is immutable — always re-bind the value stored on the row.
	invitedByID := ""
	if currentUser.InvitedBy != nil {
		invitedByID = currentUser.InvitedBy.ID
	}
	signedAt := time.Now().UTC().Truncate(time.Second)
	profilePayload := identity.BuildProfilePayload(
		userID,
		username,
		fingerprint,
		h.services.db.GetServerID(),
		h.signingKey.Fingerprint,
		userSignatureB64,
		invitedByID,
		currentUser.Role,
		bio,
		currentUser.CreatedAt,
		signedAt,
	)
	profileSignature, err := h.countersign(profilePayload, signedAt)
	if err != nil {
		log.Error().Err(err).Msg("Error producing server signature")
		internalServerError(w)
		return
	}

	if err := h.services.db.UpdateUser(r.Context(), UpdateUserInput{
		UserID:           userID,
		Username:         username,
		Bio:              bio,
		Fingerprint:      fingerprint,
		UserSignatureB64: userSignatureB64,
		ProfileSignature: profileSignature,
	}); err != nil {
		// Race with a concurrent rename that took our target username
		// between the UsernameExists check above and this UPDATE.
		if errors.Is(err, ErrUsernameTaken) {
			log.Info().Str("username", username).Msg("Username already taken (race)")
			writeResponse(w, http.StatusBadRequest, "Username already taken")
			return
		}
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error updating user")
		internalServerError(w)
		return
	}

	updated, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil || updated == nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error reloading updated user")
		internalServerError(w)
		return
	}

	h.broadcastChan <- realtime.BroadcastMessage{
		Type:   realtime.UserUpdate,
		UserID: userID,
		UserUpdate: &realtime.UserUpdateBroadcast{
			Username: updated.Username,
			Bio:      updated.Bio,
		},
	}

	log.Info().
		Str("userID", userID).
		Str("username", username).
		Msg("Signed identity record updated")

	writeResponse(w, http.StatusOK, updated)
}

func (h *Handlers) AddPublicKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("AddPublicKey request received")

	userID := strings.TrimSpace(r.FormValue("userID"))
	if userID == "" {
		log.Error().Msg("Argument `userID` is required")
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	revokedKeySignatureB64 := strings.TrimSpace(r.FormValue("revokedKeySignature"))
	if revokedKeySignatureB64 == "" {
		log.Error().
			Str("userID", userID).
			Msg("Argument `revokedKeySignature` not found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `revokedKeySignature` is required")
		return
	}
	revokedKeySigArmor, err := encoding.Base64Decode(revokedKeySignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid revokedKeySignature encoding")
		return
	}

	newKeySignatureB64 := strings.TrimSpace(r.FormValue("newKeySignature"))
	if newKeySignatureB64 == "" {
		log.Error().
			Str("userID", userID).
			Msg("Argument `newKeySignature` not found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `newKeySignature` is required")
		return
	}
	newKeySigArmor, err := encoding.Base64Decode(newKeySignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid newKeySignature encoding")
		return
	}

	publicKeyB64 := strings.TrimSpace(r.FormValue("publicKey"))
	if publicKeyB64 == "" {
		log.Error().Str("userID", userID).Msg("No public key found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `publicKey` is required")
		return
	}
	armoredPublicKey, err := encoding.Base64Decode(publicKeyB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid publicKey encoding")
		return
	}

	// revokedKeyFingerprint travels bare over the wire (form field); join
	// it with userID (already canonical) to get the DB/lookup key.
	revokedKeyFingerprintBare := strings.TrimSpace(r.FormValue("revokedKeyFingerprint"))
	if revokedKeyFingerprintBare == "" {
		log.Error().Str("userID", userID).Msg("Argument `revokedKeyFingerprint` not found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `revokedKeyFingerprint` is required")
		return
	}
	revokedKeyFingerprint := string(identity.AppendEntity(identity.IdentityID(userID), revokedKeyFingerprintBare))

	// Retrieve old key — needed for cryptographic verification of the
	// rotation proof below. DB integrity of the rotation itself (revoked,
	// no successor yet, no other active key, …) is enforced inside
	// DataService.AddPublicKey.
	revokedKey, err := h.services.db.GetPublicKey(r.Context(), revokedKeyFingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("revokedKeyFingerprint", revokedKeyFingerprint).
			Err(err).Msg("Error retrieving old public key")
		internalServerError(w)
		return
	}
	if revokedKey == nil {
		log.Error().
			Str("userID", userID).
			Str("revokedKeyFingerprint", revokedKeyFingerprint).
			Msg("Old public key not found")
		writeResponse(w, http.StatusNotFound, "Old public key not found")
		return
	}

	// Verify revoked key signature against old key
	err = h.services.crypto.VerifySignedChallenge(revokedKeySigArmor, revokedKey.Armor, armoredPublicKey)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("revokedKeyFingerprint", revokedKeyFingerprint).
			Err(err).Msg("Revoked key signature verification failed")
		writeResponse(w, http.StatusUnauthorized, "Revoked key signature verification failed")
		return
	}

	log.Info().
		Str("userID", userID).
		Str("revokedKeyFingerprint", revokedKeyFingerprint).
		Msg("Revoked key signature verified successfully")

	// Validate and verify the public newKey using crypto service
	newKey, err := h.services.crypto.ValidateAndExtractPublicKey(armoredPublicKey, newKeySigArmor)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error validating public key")
		writeResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Info().
		Str("userID", userID).
		Str("fingerprint", newKey.Fingerprint).
		Msg("Public key signature verified successfully")

	newKeyFingerprint := string(identity.AppendEntity(identity.IdentityID(userID), newKey.Fingerprint))

	now := time.Now().UTC().Truncate(time.Second)
	keyPayload := identity.BuildPublicKeyPayload(
		h.services.db.GetServerID(),
		userID,
		newKeyFingerprint,
		h.signingKey.Fingerprint,
		armoredPublicKey,
		now,
	)
	keySignature, err := h.countersign(keyPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error signing public key")
		internalServerError(w)
		return
	}

	publicKey, err := h.services.db.AddPublicKey(r.Context(), AddPublicKeyInput{
		ID:        newKeyFingerprint,
		UserID:    userID,
		CreatedAt: newKey.CreatedAt,
		Armor:     armoredPublicKey,
		Server:    keySignature,

		PredecessorID:        revokedKeyFingerprint,
		PredecessorSignature: revokedKeySigArmor,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			writeResponse(w, http.StatusNotFound, "User not found")
		case errors.Is(err, ErrKeyAlreadyExists):
			writeResponse(w, http.StatusConflict, "Public key fingerprint already registered")
		case errors.Is(err, ErrPredecessorRequired),
			errors.Is(err, ErrPredecessorNotFound),
			errors.Is(err, ErrPredecessorNotRevoked),
			errors.Is(err, ErrPredecessorAlreadyReplaced),
			errors.Is(err, ErrActiveKeyExists):
			log.Error().
				Str("userID", userID).
				Str("revokedKeyFingerprint", revokedKeyFingerprint).
				Err(err).Msg("AddPublicKey rejected")
			writeResponse(w, http.StatusBadRequest, err.Error())
		default:
			log.Error().
				Str("userID", userID).
				Err(err).Msg("Error adding public key")
			internalServerError(w)
		}
		return
	}
	log.Info().
		Str("userID", userID).
		Str("fingerprint", newKeyFingerprint).
		Msg("Public key created")

	publicKey.Armor = encoding.Base64Encode(publicKey.Armor)
	writeResponse(w, http.StatusOK, publicKey)
}

func (h *Handlers) RevokeKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("RevokeKey request received")

	userID := h.getUserID(r)
	fingerprint := mux.Vars(r)["id"]
	if fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}
	// A key can only be revoked by its own owner — a foreign id (or one
	// belonging to a different local user) is rejected outright, never
	// proxied: revoking a key you don't own is nonsensical, and only the
	// owning server can produce a valid server countersignature for it.
	embeddedUserID, embeddedServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(fingerprint))
	if !ok || string(identity.CanonicalID(embeddedServerID, embeddedUserID)) != userID {
		writeResponse(w, http.StatusNotFound, "Key not found")
		return
	}

	err := r.ParseForm()
	if err != nil {
		log.Error().Err(err).Msg("Error parsing form")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	reason := strings.TrimSpace(r.FormValue("reason"))
	userSignatureB64 := strings.TrimSpace(r.FormValue("userSignature"))
	if userSignatureB64 == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userSignature` is required")
		return
	}

	pubKey, err := h.services.db.GetPublicKey(r.Context(), fingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil {
		writeResponse(w, http.StatusNotFound, "Key not found")
		return
	}
	if pubKey.Revoked {
		writeResponse(w, http.StatusConflict, "Key is already revoked")
		return
	}

	userPayload := identity.BuildUserRevocationPayload(userID, fingerprint, reason)
	userSigArmor, err := encoding.Base64Decode(userSignatureB64)
	if err != nil {
		log.Error().Err(err).Msg("Invalid userSignature encoding")
		writeResponse(w, http.StatusBadRequest, "Invalid userSignature encoding")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Err(err).Msg("userSignature verification failed")
		writeResponse(w, http.StatusUnauthorized, "userSignature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	serverPayload := identity.BuildServerRevocationPayload(
		userID,
		fingerprint,
		reason,
		h.services.db.GetServerID(),
		h.signingKey.Fingerprint,
		userSignatureB64,
		now,
	)
	serverSignature, err := h.countersign(serverPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing server revocation signature")
		internalServerError(w)
		return
	}

	err = h.services.db.RevokeKey(r.Context(), RevokeKeyInput{
		ID:               fingerprint,
		UserID:           userID,
		Reason:           reason,
		UserSignatureB64: userSignatureB64,
		Server:           serverSignature,
	})
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Err(err).Msg("Error revoking key")
		internalServerError(w)
		return
	}

	key, err := h.services.db.GetPublicKey(r.Context(), fingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Err(err).Msg("Error fetching revoked key")
		internalServerError(w)
		return
	}

	log.Info().
		Str("userID", userID).
		Str("fingerprint", fingerprint).
		Msg("Key revoked successfully")

	h.metrics.KeyRevoked(r.Context(), userID)

	key.Armor = encoding.Base64Encode(key.Armor)
	writeResponse(w, http.StatusOK, key)
}

func (h *Handlers) GetKeyRevocation(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetKeyRevocation request received")

	fingerprint := mux.Vars(r)["id"]
	if fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	if handled, _ := h.proxyIfForeign(w, r, fingerprint); handled {
		return
	}

	revocation, err := h.services.db.GetKeyRevocation(r.Context(), fingerprint)
	if err != nil {
		log.Error().
			Str("fingerprint", fingerprint).
			Err(err).Msg("Error fetching key revocation")
		internalServerError(w)
		return
	}
	if revocation == nil {
		writeResponse(w, http.StatusNotFound, "Revocation not found")
		return
	}

	writeResponse(w, http.StatusOK, revocation)
}

func (h *Handlers) SignReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("SignReed request received")

	userID := h.getUserID(r)

	err := r.ParseForm()
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error parsing form")
		writeResponse(w, http.StatusBadRequest, "Error parsing form")
		return
	}

	userSignature := r.FormValue("signature")
	if userSignature == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}

	reedID := r.FormValue("reedID")
	if reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	// Content may be empty (e.g. bare echo). Echoing/replying are optional reed refs.
	contentBody := r.FormValue("content")
	echoing := strings.TrimSpace(r.FormValue("echoing"))
	replying := strings.TrimSpace(r.FormValue("replying"))
	previousID := strings.TrimSpace(r.FormValue("previousID"))

	if !ReedContentWithinLimits(contentBody) {
		h.metrics.ReedRejectedLength(r.Context(), len(contentBody), CountMarkdownCharacters(contentBody))
		writeResponse(w, http.StatusBadRequest, "Reed content exceeds character limits")
		return
	}

	localServerID := h.services.db.GetServerID()
	var echoRef *ReedRef
	var replyRef *ReedRef
	if echoing != "" {
		ref, ok := h.parseReedRef(echoing, localServerID)
		if !ok {
			writeResponse(w, http.StatusBadRequest, "Invalid echoing reference")
			return
		}
		// Blankness/existence can only be checked against this server's own
		// DB, so a foreign target skips it entirely — the client already has
		// the target rendered locally (that's how it's being echoed at all),
		// and this check was weak trust theater even for local targets (any
		// client could already misreport blank status). Real authenticity is
		// enforced downstream, when a viewer actually resolves the target
		// through the existing read-proxy/signature-verification path — not
		// here. TODO: most of these server-side "sanity" checks predate
		// federation and made limited sense even then; reconsider dropping
		// them outright rather than patching each one for the foreign case.
		if ref.ServerID == localServerID {
			blank, err := h.services.db.IsBlankEcho(r.Context(), FormatReedRef(ref))
			if errors.Is(err, ErrReedNotFound) {
				writeResponse(w, http.StatusBadRequest, "Target reed not found")
				return
			}
			if err != nil {
				log.Error().Err(err).Msg("Error checking echo target blankness")
				internalServerError(w)
				return
			}
			if blank {
				writeResponse(w, http.StatusBadRequest, "Cannot echo an empty echo — echo the original reed instead")
				return
			}
		}
		echoRef = &ref
	}
	if replying != "" {
		ref, ok := h.parseReedRef(replying, localServerID)
		if !ok {
			writeResponse(w, http.StatusBadRequest, "Invalid replying reference")
			return
		}
		if ref.ServerID == localServerID {
			blank, err := h.services.db.IsBlankEcho(r.Context(), FormatReedRef(ref))
			if errors.Is(err, ErrReedNotFound) {
				writeResponse(w, http.StatusBadRequest, "Target reed not found")
				return
			}
			if err != nil {
				log.Error().Err(err).Msg("Error checking reply target blankness")
				internalServerError(w)
				return
			}
			if blank {
				writeResponse(w, http.StatusBadRequest, "Cannot reply to an empty echo — reply to the original reed instead")
				return
			}
		}
		replyRef = &ref
	}
	if echoRef != nil && replyRef != nil {
		writeResponse(w, http.StatusBadRequest, "A reed cannot both echo and reply")
		return
	}

	threadID := ""
	if replyRef != nil {
		var err error
		threadID, err = h.services.db.ResolveThreadIDForParent(r.Context(), *replyRef)
		if err != nil {
			log.Error().Err(err).Msg("Error resolving thread")
			internalServerError(w)
			return
		}
	}

	// Local mentions are validated + indexed immediately here; foreign
	// mentions (mentioned user lives on a peer) get no local validation —
	// only that peer's own server can confirm the user exists — and are
	// instead queued for the mention-notify federation leg below, once
	// create succeeds.
	allMentions := ExtractMentions(contentBody, userID)
	localMentions := make([]string, 0, len(allMentions))
	foreignMentions := make([]string, 0, len(allMentions))
	for _, m := range allMentions {
		mentionedUserID := m.CanonicalAuthorID()
		if m.ServerID != localServerID {
			foreignMentions = append(foreignMentions, mentionedUserID)
			continue
		}
		valid, err := h.services.db.MentionTargetValid(r.Context(), m.AuthorID, m.ServerID)
		if err != nil {
			log.Error().Str("mentionedUserID", m.AuthorID).Err(err).Msg("Error validating mention target")
			internalServerError(w)
			return
		}
		if !valid {
			writeResponse(w, http.StatusBadRequest, "Mentioned user not found")
			return
		}
		localMentions = append(localMentions, mentionedUserID)
	}

	markdown := ReedAsMarkdown(reedID, userID, contentBody, echoing, replying, threadID)

	user, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusBadRequest, "User not found")
		return
	}

	userFingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), userID)
	if err != nil || userFingerprint == "" {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading active key fingerprint")
		internalServerError(w)
		return
	}
	userSigArmor, err := encoding.Base64Decode(userSignature)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(r.Context(), userFingerprint)
	if err != nil {
		log.Error().Str("userID", userID).Str("userFingerprint", userFingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.VerifySignature(markdown, userSigArmor, pubKey.Armor); err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Err(err).
			Msg("reed signature verification failed")
		writeResponse(w, http.StatusBadRequest, "signature verification failed")
		return
	}

	existing, err := h.services.db.GetReedAttestation(r.Context(), reedID)
	if err != nil {
		log.Error().Str("reedID", reedID).Str("userID", userID).Err(err).Msg("Error loading reed")
		internalServerError(w)
		return
	}
	if existing != nil {
		h.respondSignReedReplay(w, r, existing, userSignature, userID, reedID)
		return
	}

	timestamp := time.Now().UTC().Truncate(time.Second)
	reedPayload := identity.BuildReedPayload(
		h.services.db.GetServerID(),
		reedID,
		h.signingKey.Fingerprint,
		userSignature,
		timestamp,
	)
	serverSignature, err := h.countersign(reedPayload, timestamp)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", h.signingKey.Fingerprint).
			Err(err).Msg("Error signing")
		internalServerError(w)
		return
	}

	tags := ExtractTags(contentBody)
	if h.filterPipeTags != nil {
		tags = h.filterPipeTags(tags)
	}
	createParams := createReedParams{
		ReedID:             reedID,
		UserID:             userID,
		UserFingerprint:    userFingerprint,
		UserSignatureB64:   userSignature,
		ServerFingerprint:  serverSignature.Fingerprint,
		ServerSignatureB64: serverSignature.Armor,
		Timestamp:          serverSignature.SignedAt,
		Tags:               tags,
		Mentions:           localMentions,
		PreviousID:         previousID,
	}

	var reed *Reed
	var echoIndexed bool
	switch {
	case echoRef != nil:
		isBlankEcho := strings.TrimSpace(contentBody) == ""
		reed, echoIndexed, err = h.services.db.CreateReedWithEcho(r.Context(), createParams, *echoRef, isBlankEcho)
	case replyRef != nil:
		reed, err = h.services.db.CreateReedWithReply(r.Context(), createParams, threadID, *replyRef)
	default:
		reed, err = h.services.db.CreateReed(r.Context(), createParams)
	}
	if err != nil {
		// Concurrent SignReed for the same id: both passed the pre-insert
		// GetReedAttestation (nil), both tried Create; the loser hits unique
		// violation and must return the winner's stored countersignature.
		// (Lost-response retries are already handled by the check above.)
		if isReedUniqueViolation(err) {
			existing, getErr := h.services.db.GetReedAttestation(r.Context(), reedID)
			if getErr == nil && existing != nil {
				h.respondSignReedReplay(w, r, existing, userSignature, userID, reedID)
				return
			}
		}
		if errors.Is(err, ErrReedFork) {
			writeResponse(w, http.StatusConflict, "previousID does not match the author's current tip")
			return
		}
		log.Error().
			Str("reedID", reedID).
			Str("userID", userID).
			Str("serverFingerprint", serverSignature.Fingerprint).
			Err(err).Msg("Error creating reed")
		internalServerError(w)
		return
	}

	if echoIndexed && echoRef != nil {
		h.metrics.EchoTargeted(r.Context(), echoRef.AuthorID, echoRef.ReedID)
		if echoRef.ServerID == localServerID {
			h.broadcastChan <- realtime.BroadcastMessage{
				Type:   realtime.EchoCountChanged,
				UserID: echoRef.CanonicalAuthorID(),
				ReedID: echoRef.ReedID,
			}
		} else {
			go func() {
				if err := h.notifyForeignEchoToPeer(context.Background(), FormatReedRef(*echoRef), reedID, userID, strings.TrimSpace(contentBody) == "", serverSignature.SignedAt); err != nil {
					log.Error().Err(err).Str("echoedReedID", FormatReedRef(*echoRef)).Str("echoingReedID", reedID).Msg("Failed to notify foreign echo target's home server")
				}
			}()
		}
	}

	if len(foreignMentions) > 0 {
		go func() {
			for _, mentionedUserID := range foreignMentions {
				if err := h.notifyForeignMentionToPeer(context.Background(), reedID, mentionedUserID, serverSignature.SignedAt); err != nil {
					log.Error().Err(err).Str("mentioningReedID", reedID).Str("mentionedUserID", mentionedUserID).Msg("Failed to notify foreign mention target's home server")
				}
			}
		}()
	}

	if replyRef != nil {
		// ReplyPosted (content relay) is NOT fired here — the reply's own
		// author isn't a valid relay holder for it until their client sends
		// PUBLISH_READY (see handlePublishReady). Firing it this early races
		// PUBLISH_READY: the resulting relay miss deletes the author's
		// allocation for their own reed, orphaning it from relay entirely.
		targets, err := h.services.db.ReplyCountNotifyTargets(r.Context(), FormatReedRef(*replyRef))
		if err != nil {
			log.Error().Err(err).Msg("Error resolving reply count notify targets")
		} else {
			for _, t := range targets {
				h.broadcastChan <- realtime.BroadcastMessage{
					Type:   realtime.ReplyCountChanged,
					UserID: t.CanonicalAuthorID(),
					ReedID: t.ReedID,
				}
			}
		}
	}

	reedKind := metrics.ReedKindPlain
	switch {
	case echoRef != nil:
		reedKind = metrics.ReedKindEcho
	case replyRef != nil:
		reedKind = metrics.ReedKindReply
	}
	h.metrics.ReedPublished(r.Context(), metrics.ReedPublishedAttrs{
		Kind:         reedKind,
		AuthorID:     userID,
		ReedID:       reed.ID,
		TagCount:     len(tags),
		RawChars:     len(contentBody),
		VisibleChars: CountMarkdownCharacters(contentBody),
	})
	if activeUsers, err := coverage.ActiveUsers(r.Context(), h.services.db.db); err == nil {
		h.metrics.ReedCoverage(r.Context(), userID, reed.ID, 1, coverage.Percent(1, activeUsers))
	}

	log.Debug().
		Str("userID", userID).
		Str("reedID", reed.ID).
		Msg("Reed created successfully")

	writeResponse(w, http.StatusCreated, serverSignature)
}

// respondSignReedReplay returns the stored countersignature (HTTP 200) when
// the user signature matches; otherwise 409.
func (h *Handlers) respondSignReedReplay(
	w http.ResponseWriter,
	r *http.Request,
	existing *ReedAttestation,
	userSignatureB64, userID, reedID string,
) {
	log := h.services.log.GetLogger(r.Context())
	if existing.UserSignature != userSignatureB64 {
		writeResponse(w, http.StatusConflict, "Reed already exists with a different signature")
		return
	}
	log.Info().
		Str("reedID", reedID).
		Str("userID", userID).
		Msg("SignReed replay: returning stored countersignature")
	writeResponse(w, http.StatusOK, ServerSignature{
		ServerID:    h.services.db.GetServerID(),
		Fingerprint: existing.ServerFingerprint,
		Armor:       existing.ServerSignature,
		SignedAt:    existing.ServerSignedAt,
	})
}

// parseReedRef parses userID@serverID/reedID and checks reed id + local server.
func (h *Handlers) parseReedRef(raw, localServerID string) (ReedRef, bool) {
	ref, ok := ParseReedRef(raw)
	if !ok {
		return ReedRef{}, false
	}
	if !crypto.IsValidUUIDv7(ref.ReedID) {
		return ReedRef{}, false
	}
	return ref, true
}

func (h *Handlers) DeleteReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("DeleteReed request received")

	pathUserID := mux.Vars(r)["userID"]
	bareReedID := mux.Vars(r)["reedID"]
	if pathUserID == "" || bareReedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}

	userID := h.getUserID(r)
	if userID != pathUserID {
		writeResponse(w, http.StatusForbidden, "You can only delete your own reeds")
		return
	}
	reedID := string(identity.AppendEntity(identity.IdentityID(userID), bareReedID))

	values, err := parseFormData(r)
	if err != nil {
		log.Error().Err(err).Msg("Error parsing form")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	userSignatureB64 := strings.TrimSpace(values.Get("signature"))
	if userSignatureB64 == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}

	serverID := h.services.db.GetServerID()

	existing, err := h.services.db.GetReedRemoval(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error loading reed removal")
		internalServerError(w)
		return
	}
	if existing != nil {
		if existing.UserSignature != userSignatureB64 {
			writeResponse(w, http.StatusConflict, "Reed removal already exists with a different signature")
			return
		}
		writeResponse(w, http.StatusOK, h.reedRemovalWire(existing))
		return
	}

	reed, err := h.services.db.GetReed(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error getting reed")
		internalServerError(w)
		return
	}
	if reed == nil {
		writeResponse(w, http.StatusNotFound, "Reed not found")
		return
	}
	if reed.UserID != userID {
		writeResponse(w, http.StatusForbidden, "You can only delete your own reeds")
		return
	}

	user, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusBadRequest, "User not found")
		return
	}

	fingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), userID)
	if err != nil || fingerprint == "" {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading active key fingerprint")
		internalServerError(w)
		return
	}
	userPayload := identity.BuildReedRemovalUserPayload(serverID, reedID)
	userSigArmor, err := encoding.Base64Decode(userSignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(r.Context(), fingerprint)
	if err != nil {
		log.Error().Str("userID", userID).Str("fingerprint", fingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Err(err).
			Msg("signature verification failed")
		writeResponse(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	serverPayload := identity.BuildReedRemovalServerPayload(
		serverID, reedID,
		h.signingKey.Fingerprint, userSignatureB64, now,
	)
	serverSignature, err := h.countersign(serverPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing reed-removal countersignature")
		internalServerError(w)
		return
	}

	cert := deletion.Cert{
		ReedID:            reedID,
		UserID:            userID,
		UserSignature:     userSignatureB64,
		UserFingerprint:   fingerprint,
		ServerSignature:   serverSignature.Armor,
		ServerFingerprint: serverSignature.Fingerprint,
		ServerSignedAt:    serverSignature.SignedAt,
	}
	if err := h.services.db.InsertReedRemoval(r.Context(), cert); err != nil {
		if errors.Is(err, deletion.ErrConflict) {
			// Concurrent first accept: return the stored cert if the user
			// signature matches; otherwise a true conflicting attestation.
			existing, getErr := h.services.db.GetReedRemoval(r.Context(), reedID)
			if getErr == nil && existing != nil && existing.UserSignature == userSignatureB64 {
				writeResponse(w, http.StatusOK, h.reedRemovalWire(existing))
				return
			}
			writeResponse(w, http.StatusConflict, "Reed removal already exists with a different signature")
			return
		}
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error storing reed removal")
		internalServerError(w)
		return
	}

	h.metrics.ReedDeleted(r.Context(), userID, reedID)

	if err := h.services.db.DeleteMentionsForReed(r.Context(), reedID); err != nil {
		log.Error().Str("reedID", reedID).Err(err).Msg("Error clearing mention index for removed reed")
	}

	affectedTargets, err := h.services.db.DeleteEchoIndexForReed(r.Context(), reedID)
	if err != nil {
		log.Error().Str("reedID", reedID).Err(err).Msg("Error clearing echo index for removed reed")
	} else {
		for _, t := range affectedTargets {
			if t.ServerID != serverID {
				// Target is foreign — this server's own EchoCountChanged
				// broadcast below only reaches its own local subscribers;
				// without this notify the target's home server never
				// learns the echo is gone and keeps counting/showing it,
				// same gap as the reply-removal case (leg 13).
				go func(echoedReedID, echoingReedID string) {
					if err := h.notifyForeignEchoRemovalToPeer(context.Background(), echoedReedID, echoingReedID); err != nil {
						log.Error().Err(err).Str("echoedReedID", echoedReedID).Str("echoingReedID", echoingReedID).Msg("Failed to notify foreign echo target's home server of removal")
					}
				}(FormatReedRef(t), reedID)
				continue
			}
			h.broadcastChan <- realtime.BroadcastMessage{
				Type:   realtime.EchoCountChanged,
				UserID: t.CanonicalAuthorID(),
				ReedID: t.ReedID,
			}
		}
	}

	// Keep the reeds row for allocation catch-up (04): reed_allocations FK
	// cascades on reed delete. Tip/list already exclude reed_removals.
	wire := realtime.NewReedRemovalWire(serverID, &cert)

	replyTargets, err := h.services.db.ReplyCountNotifyTargetsForRemovedReply(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error resolving reply count targets for removed reed")
	} else {
		for i, t := range replyTargets {
			if i == 0 && t.ServerID != serverID {
				// Immediate parent is foreign — this server's own
				// reply-count fanout above only covers ancestors it can
				// see via its own reed_replies table, which stops at this
				// foreign parent. Without this notify, the parent's home
				// server never learns the reply was removed and keeps
				// counting/showing it forever, and never delivers the
				// removal notice to that thread's own viewers either.
				go func(parentReedID, replyReedID string) {
					if err := h.notifyForeignReplyRemovalToPeer(context.Background(), parentReedID, replyReedID, &wire); err != nil {
						log.Error().Err(err).Str("parentReedID", parentReedID).Str("replyReedID", replyReedID).Msg("Failed to notify foreign parent's home server of reply removal")
					}
				}(FormatReedRef(t), reedID)
				continue
			}
			h.broadcastChan <- realtime.BroadcastMessage{
				Type:   realtime.ReplyCountChanged,
				UserID: t.CanonicalAuthorID(),
				ReedID: t.ReedID,
			}
		}
	}

	h.broadcastChan <- realtime.BroadcastMessage{
		Type:        realtime.ReedRemoved,
		ServerID:    serverID,
		UserID:      userID,
		ReedID:      bareReedID,
		ReedRemoval: &wire,
	}

	log.Info().Str("userID", userID).Str("reedID", reedID).Msg("Reed removal accepted")
	writeResponse(w, http.StatusOK, h.reedRemovalWire(&cert))
}

// LikeReed handles POST /reeds/{userID}/{reedID}/like — a signed like.
// {userID} is the reed's author; the liker is the authenticated caller.
// The request carries the liker's own key fingerprint alongside the
// signature, so verification targets that exact key.
func (h *Handlers) LikeReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("LikeReed request received")

	authorID := mux.Vars(r)["userID"]
	bareReedID := mux.Vars(r)["reedID"]
	if authorID == "" || bareReedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(identity.AppendEntity(identity.IdentityID(authorID), bareReedID))

	if h.proxyLikeToForeignReed(w, r, reedID) {
		return
	}

	values, err := parseFormData(r)
	if err != nil {
		log.Error().Err(err).Msg("Error parsing form")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	likerID, ok := h.resolveActingUser(r, values.Get("likerID"))
	if !ok {
		writeResponse(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}
	userSignatureB64 := strings.TrimSpace(values.Get("signature"))
	if userSignatureB64 == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}
	bareFingerprint := strings.TrimSpace(values.Get("fingerprint"))
	if bareFingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `fingerprint` is required")
		return
	}
	fingerprint := string(identity.AppendEntity(identity.IdentityID(likerID), bareFingerprint))

	serverID := h.services.db.GetServerID()

	existing, err := h.services.db.GetReedLike(r.Context(), likerID, reedID)
	if err != nil {
		log.Error().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Err(err).Msg("Error loading reed like")
		internalServerError(w)
		return
	}
	if existing != nil {
		if existing.UserSignature.Armor != userSignatureB64 {
			writeResponse(w, http.StatusConflict, "Reed like already exists with a different signature")
			return
		}
		writeResponse(w, http.StatusOK, existing)
		return
	}

	reed, err := h.services.db.GetReed(r.Context(), reedID)
	if err != nil {
		log.Error().Str("authorID", authorID).Str("reedID", reedID).Err(err).Msg("Error getting reed")
		internalServerError(w)
		return
	}
	if reed == nil {
		writeResponse(w, http.StatusNotFound, "Reed not found")
		return
	}

	userPayload := identity.BuildReedLikeUserPayload(serverID, reedID, fingerprint)
	userSigArmor, err := encoding.Base64Decode(userSignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.resolvePublicKey(r.Context(), fingerprint)
	if err != nil {
		log.Error().Str("likerID", likerID).Str("fingerprint", fingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().
			Str("likerID", likerID).
			Str("authorID", authorID).
			Str("reedID", reedID).
			Err(err).
			Msg("signature verification failed")
		writeResponse(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	serverPayload := identity.BuildReedLikeServerPayload(
		serverID, reedID,
		h.signingKey.Fingerprint, userSignatureB64, now,
	)
	serverSignature, err := h.countersign(serverPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing reed-like countersignature")
		internalServerError(w)
		return
	}

	cert := LikeCert{
		ServerID: serverID,
		AuthorID: authorID,
		ReedID:   reedID,
		UserSignature: UserSignature{
			Fingerprint: fingerprint,
			Armor:       userSignatureB64,
		},
		ServerSignature: serverSignature,
	}
	if err := h.services.db.InsertReedLike(r.Context(), likerID, fingerprint, cert); err != nil {
		if errors.Is(err, ErrLikeConflict) {
			existing, getErr := h.services.db.GetReedLike(r.Context(), likerID, reedID)
			if getErr == nil && existing != nil && existing.UserSignature.Armor == userSignatureB64 {
				writeResponse(w, http.StatusOK, existing)
				return
			}
			writeResponse(w, http.StatusConflict, "Reed like already exists with a different signature")
			return
		}
		log.Error().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Err(err).Msg("Error storing reed like")
		internalServerError(w)
		return
	}

	h.broadcastChan <- realtime.BroadcastMessage{
		Type:   realtime.LikeCountChanged,
		UserID: authorID,
		ReedID: bareReedID,
	}

	log.Info().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Msg("Reed like accepted")
	writeResponse(w, http.StatusOK, cert)
}

// UnlikeReed handles DELETE /reeds/{userID}/{reedID}/like: a plain hard
// delete of the liker's row, authenticated as the liker. Empty request
// and response bodies; the status code is the whole signal. Works
// against a since-deleted reed, which is how a client clears it from its
// own liked-reeds view.
func (h *Handlers) UnlikeReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("UnlikeReed request received")

	authorID := mux.Vars(r)["userID"]
	bareReedID := mux.Vars(r)["reedID"]
	if authorID == "" || bareReedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(identity.AppendEntity(identity.IdentityID(authorID), bareReedID))

	if h.proxyUnlikeToForeignReed(w, r, reedID) {
		return
	}

	// r.FormValue skips DELETE bodies, same as resolveFollower's UNFOLLOW
	// case — read directly.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	values, _ := url.ParseQuery(string(body))
	likerID, ok := h.resolveActingUser(r, values.Get("likerID"))
	if !ok {
		writeResponse(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}

	deleted, err := h.services.db.DeleteReedLike(r.Context(), likerID, reedID)
	if err != nil {
		log.Error().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Err(err).Msg("Error deleting reed like")
		internalServerError(w)
		return
	}

	if deleted {
		h.broadcastChan <- realtime.BroadcastMessage{
			Type:   realtime.LikeCountChanged,
			UserID: authorID,
			ReedID: bareReedID,
		}
	}

	log.Info().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Msg("Reed unlike accepted")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) reedRemovalWire(cert *deletion.Cert) ReedRemoval {
	return ReedRemoval{
		Type:     identity.TypeReed,
		ServerID: h.services.db.GetServerID(),
		UserID:   cert.UserID,
		ReedID:   cert.ReedID,
		UserSignature: UserSignature{
			Fingerprint: cert.UserFingerprint,
			Armor:       cert.UserSignature,
		},
		ServerSignature: ServerSignature{
			ServerID:    h.services.db.GetServerID(),
			Fingerprint: cert.ServerFingerprint,
			Armor:       cert.ServerSignature,
			SignedAt:    cert.ServerSignedAt,
		},
	}
}

func (h *Handlers) GetReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReed request received")

	bareReedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || bareReedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(identity.AppendEntity(identity.IdentityID(userID), bareReedID))

	if handled, _ := h.proxyIfForeign(w, r, reedID); handled {
		return
	}

	result, err := h.services.db.GetReedOrRemovalCert(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error loading reed")
		internalServerError(w)
		return
	}
	if result.AccountRemoval != nil {
		writeResponse(w, http.StatusGone, h.accountRemovalWire(result.AccountRemoval))
		return
	}
	if result.ReedRemoval != nil {
		writeResponse(w, http.StatusGone, h.reedRemovalWire(result.ReedRemoval))
		return
	}
	if result.Reed == nil {
		writeResponse(w, http.StatusNotFound, "Post not found")
		return
	}

	log.Debug().
		Str("userID", userID).
		Str("reedID", reedID).
		Msg("Post found")

	writeResponse(w, http.StatusOK, result.Reed)
}

func (h *Handlers) GetReedEchoCount(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReedEchoCount request received")

	bareReedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || bareReedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(identity.AppendEntity(identity.IdentityID(userID), bareReedID))

	if handled, _ := h.proxyIfForeign(w, r, reedID); handled {
		return
	}

	result, err := h.services.db.GetReedOrRemovalCert(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error loading reed")
		internalServerError(w)
		return
	}
	if result.AccountRemoval != nil {
		writeResponse(w, http.StatusGone, h.accountRemovalWire(result.AccountRemoval))
		return
	}
	if result.ReedRemoval != nil {
		writeResponse(w, http.StatusGone, h.reedRemovalWire(result.ReedRemoval))
		return
	}
	if result.Reed == nil {
		writeResponse(w, http.StatusNotFound, "Post not found")
		return
	}

	count, err := h.services.db.CountEchoes(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error counting echoes")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, count)
}

// GetReedChorus handles GET /reeds/{userID}/{reedID}/chorus.
func (h *Handlers) GetReedChorus(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReedChorus request received")

	bareReedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || bareReedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(identity.AppendEntity(identity.IdentityID(userID), bareReedID))

	if handled, _ := h.proxyIfForeign(w, r, reedID); handled {
		return
	}

	result, err := h.services.db.GetReedOrRemovalCert(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error loading reed")
		internalServerError(w)
		return
	}
	if result.Reed == nil && result.ReedRemoval == nil && result.AccountRemoval == nil {
		writeResponse(w, http.StatusNotFound, "Post not found")
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeResponse(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	var before *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeResponse(w, http.StatusBadRequest, "Invalid before cursor")
			return
		}
		t = t.UTC().Truncate(time.Second)
		before = &t
	}

	list, err := h.services.db.GetReedChorus(r.Context(), reedID, limit, before)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error listing echoers")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, list)
}

func (h *Handlers) GetReedReplies(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReedReplies request received")

	bareReedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || bareReedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(identity.AppendEntity(identity.IdentityID(userID), bareReedID))

	if handled, _ := h.proxyIfForeign(w, r, reedID); handled {
		return
	}

	result, err := h.services.db.GetReedOrRemovalCert(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error loading reed")
		internalServerError(w)
		return
	}
	if result.Reed == nil && result.ReedRemoval == nil && result.AccountRemoval == nil {
		writeResponse(w, http.StatusNotFound, "Post not found")
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeResponse(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	var before *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeResponse(w, http.StatusBadRequest, "Invalid before cursor")
			return
		}
		t = t.UTC().Truncate(time.Second)
		before = &t
	}

	list, err := h.services.db.ListReplies(r.Context(), reedID, limit, before)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error listing replies")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, list)
}

// GetUserFollowing handles GET /users/{userID}/following.
func (h *Handlers) GetUserFollowing(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUserFollowing request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeResponse(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	var before *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeResponse(w, http.StatusBadRequest, "Invalid before cursor")
			return
		}
		t = t.UTC().Truncate(time.Second)
		before = &t
	}

	list, err := h.services.db.ListFollowing(r.Context(), userID, limit, before)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error listing following")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, list)
}

// GetUserFollowers handles GET /users/{userID}/followers.
func (h *Handlers) GetUserFollowers(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUserFollowers request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeResponse(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	var before *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeResponse(w, http.StatusBadRequest, "Invalid before cursor")
			return
		}
		t = t.UTC().Truncate(time.Second)
		before = &t
	}

	list, err := h.services.db.ListFollowers(r.Context(), userID, limit, before)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error listing followers")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, list)
}

// =================== //
//   Device handlers   //
// =================== //

// RecordBackup handles POST /api/users/me/backup — SPA reports a successful
// local keys-only (.sxi.gpg) or full (.sxb.gpg) export. No DB state; emits
// an anonymized metric only.
func (h *Handlers) RecordBackup(w http.ResponseWriter, r *http.Request) {
	userID := h.getUserID(r)
	if userID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	kind := metrics.BackupKind(strings.TrimSpace(strings.ToLower(req.Kind)))
	switch kind {
	case metrics.BackupKindIdentity, metrics.BackupKindFull:
	default:
		writeResponse(w, http.StatusBadRequest, "kind must be identity or full")
		return
	}

	h.metrics.UserBackup(r.Context(), userID, kind)
	writeResponse(w, http.StatusOK, "")
}

// BindDevice handles POST /api/users/device — revoke-all + bind this origin's device.
func (h *Handlers) BindDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok || userID == "" {
		writeDeviceError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID, err := identity.ParseDeviceID(r.Header.Get("X-Syrinx-Device-Id"))
	if err != nil {
		writeDeviceError(w, http.StatusBadRequest, "Invalid device id.")
		return
	}

	now := time.Now().UTC()
	if err := h.services.db.BindDevice(r.Context(), userID, deviceID, now); err != nil {
		internalServerError(w)
		return
	}

	h.kickUserDevices(userID)

	writeResponse(w, http.StatusOK, deviceID)
}

func (h *Handlers) kickUserDevices(userID string) {
	if h.kickUserWS != nil {
		h.kickUserWS(userID)
	}
}

// ==================== //
//   Account recovery   //
// ==================== //

type accountRecoveryChallengeResponse struct {
	Challenge int64 `json:"challenge"`
}

type bootstrapAccountRecoveryRequest struct {
	Challenge   int64  `json:"challenge"`
	UserID      string `json:"userID"`
	Fingerprint string `json:"fingerprint"`
	Signature   string `json:"signature"`
}

type bootstrapAccountRecoveryResponse struct {
	Profile   User     `json:"profile"`
	Following []string `json:"following"`
	TipReedID *string  `json:"tipReedID"`
	ReedIDs   []string `json:"reedIDs"`
}

func (h *Handlers) AccountRecoveryChallenge(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, http.StatusOK, accountRecoveryChallengeResponse{
		Challenge: time.Now().UTC().Unix(),
	})
}

func (h *Handlers) BootstrapAccountRecovery(w http.ResponseWriter, r *http.Request) {
	var req bootstrapAccountRecoveryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.UserID == "" || req.Fingerprint == "" || req.Signature == "" {
		writeResponse(w, http.StatusBadRequest, "Missing required fields")
		return
	}

	now := time.Now()
	if err := recovery.ValidateChallengeAge(req.Challenge, now, 60*time.Second); err != nil {
		writeResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	deviceID, err := identity.ParseDeviceID(r.Header.Get("X-Syrinx-Device-Id"))
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Missing or invalid X-Syrinx-Device-Id header")
		return
	}

	removed, err := h.services.db.HasAccountRemoval(r.Context(), req.UserID)
	if err != nil {
		internalServerError(w)
		return
	}
	if removed {
		writeResponse(w, http.StatusGone, "Account removed")
		return
	}

	profile, err := h.services.db.GetUserProfile(r.Context(), req.UserID)
	if err != nil {
		internalServerError(w)
		return
	}
	if profile == nil {
		writeResponse(w, http.StatusNotFound, "Account not found")
		return
	}

	activeFingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), req.UserID)
	if err != nil {
		internalServerError(w)
		return
	}
	if activeFingerprint == "" || activeFingerprint != req.Fingerprint {
		writeResponse(w, http.StatusUnauthorized, "Key is not the active key for this account")
		return
	}

	key, err := h.services.db.GetPublicKey(r.Context(), req.Fingerprint)
	if err != nil {
		internalServerError(w)
		return
	}
	if key == nil || key.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Unknown or revoked key")
		return
	}

	if err := recovery.VerifyChallengeSignature(req.Challenge, req.Signature, key.Armor, h.services.crypto); err != nil {
		writeResponse(w, http.StatusUnauthorized, err.Error())
		return
	}

	if err := h.services.db.BindDevice(r.Context(), req.UserID, deviceID, now.UTC()); err != nil {
		internalServerError(w)
		return
	}
	h.kickUserDevices(req.UserID)

	following, err := h.services.db.ListUserFollowing(r.Context(), req.UserID)
	if err != nil {
		internalServerError(w)
		return
	}
	if following == nil {
		following = []string{}
	}

	tipReedID, reedIDs, err := h.services.db.ListUserReeds(r.Context(), req.UserID)
	if err != nil {
		internalServerError(w)
		return
	}
	if reedIDs == nil {
		reedIDs = []string{}
	}

	writeResponse(w, http.StatusOK, bootstrapAccountRecoveryResponse{
		Profile:   *profile,
		Following: following,
		TipReedID: tipReedID,
		ReedIDs:   reedIDs,
	})
}

// ============== //
//   Federation   //
// ============== //

func (h *Handlers) isAdmin(ctx context.Context, userID string) (bool, error) {
	role, err := h.services.db.GetUserRole(ctx, userID)
	if err != nil {
		return false, err
	}
	return roles.RequireAdmin(role) == nil, nil
}

func (h *Handlers) isRoot(ctx context.Context, userID string) (bool, error) {
	role, err := h.services.db.GetUserRole(ctx, userID)
	if err != nil {
		return false, err
	}
	return roles.IsRoot(userID, role, h.services.db.GetServerID()), nil
}

func (h *Handlers) federationSignServer(message []byte) (string, error) {
	sigArmor, err := h.services.crypto.Sign(string(message), h.signingKey.Armor)
	if err != nil {
		return "", err
	}
	return encoding.Base64Encode(sigArmor), nil
}

// federationHTTPClient returns the client used for server-to-server
// federation callbacks (e.g. POST .../federation/connect/{inviteId}).
// Bounded timeout: this call happens synchronously inside an admin's
// accept request and must not hang indefinitely on an unreachable peer.
func (h *Handlers) federationHTTPClient() *http.Client {
	if h.federationHTTPClientOverride != nil {
		return h.federationHTTPClientOverride
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// rememberRemoteIdentityOnSuccess upserts a local identities row for a
// foreign user after a successful profile/info proxy fetch. Best-effort.
func (h *Handlers) rememberRemoteIdentityOnSuccess(ctx context.Context, log *zerolog.Logger, canonicalID string, status int) {
	if status != http.StatusOK {
		return
	}
	h.upsertRemoteIdentity(ctx, log, canonicalID)
}

func (h *Handlers) upsertRemoteIdentity(ctx context.Context, log *zerolog.Logger, canonicalID string) {
	_, serverID, ok := identity.ParseIdentityID(identity.IdentityID(canonicalID))
	if !ok {
		return
	}
	if err := h.services.db.UpsertRemoteIdentity(ctx, canonicalID, serverID); err != nil {
		log.Error().Str("userID", canonicalID).Err(err).Msg("Failed to remember remote identity")
	}
}

// rememberRemoteIdentityAndFollowLocally writes this server's own
// user_following row after a remote follow was confirmed by the peer —
// the target's identities row must exist first for the FK.
func (h *Handlers) rememberRemoteIdentityAndFollowLocally(ctx context.Context, log *zerolog.Logger, followerID, userID string) {
	h.upsertRemoteIdentity(ctx, log, userID)
	if err := h.services.db.FollowUser(ctx, followerID, userID); err != nil {
		log.Error().Str("followerID", followerID).Str("userID", userID).Err(err).Msg("Failed to record confirmed remote follow locally")
	}
}

// proxyIfForeign proxies to the peer owning id's embedded serverID if
// foreign (handled=true, plus the peer's status code), or does nothing
// (handled=false) when id is local or malformed.
func (h *Handlers) proxyIfForeign(w http.ResponseWriter, r *http.Request, id string) (handled bool, status int) {
	_, embeddedServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(id))
	if !ok {
		_, embeddedServerID, ok = identity.ParseIdentityID(identity.IdentityID(id))
	}
	if !ok || embeddedServerID == h.services.db.GetServerID() {
		return false, 0
	}

	peer, err := h.services.db.GetServerByID(r.Context(), embeddedServerID)
	if err != nil {
		internalServerError(w)
		return true, 0
	}
	if peer == nil {
		writeResponse(w, http.StatusNotFound, "Not found")
		return true, 0
	}

	return true, h.proxyToPeer(w, r, peer.ID, peer.BaseURL)
}

// proxyLikeToForeignReed forwards a LikeReed request to reedID's true
// home server (returns handled=false, doing nothing, if reedID is local)
// and — unlike the generic proxyIfForeign, which streams the peer's
// response straight through unread — parses a successful LikeCert back
// out so this server can mirror the like into its own reeds_liked table.
// Without this, only the reed's home server would ever know the local
// user liked it: every read of "did I like this" against this server's
// own DB would incorrectly say no.
func (h *Handlers) proxyLikeToForeignReed(w http.ResponseWriter, r *http.Request, reedID string) (handled bool) {
	_, embeddedServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(reedID))
	if !ok || embeddedServerID == h.services.db.GetServerID() {
		return false
	}
	log := h.services.log.GetLogger(r.Context())

	peer, err := h.services.db.GetServerByID(r.Context(), embeddedServerID)
	if err != nil {
		internalServerError(w)
		return true
	}
	if peer == nil {
		writeResponse(w, http.StatusNotFound, "Not found")
		return true
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		internalServerError(w)
		return true
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return true
	}
	likerID, ok := h.resolveActingUser(r, values.Get("likerID"))
	if !ok {
		writeResponse(w, http.StatusUnauthorized, "Could not resolve acting user")
		return true
	}

	respBody, status, err := h.forwardToPeer(r, peer.BaseURL, string(body))
	if err != nil {
		log.Error().Err(err).Str("target", peer.BaseURL).Msg("proxy like to peer server failed")
		h.logFederationServerAsync(peer.ID, "error", fmt.Sprintf("Proxy like to %s failed: %s", peer.BaseURL, err.Error()))
		writeResponse(w, http.StatusBadGateway, "Failed to reach peer server")
		return true
	}

	if status == http.StatusOK {
		var cert LikeCert
		if err := json.Unmarshal(respBody, &cert); err != nil {
			log.Error().Err(err).Str("likerID", likerID).Str("reedID", reedID).Msg("Failed to parse peer like cert")
		} else if err := h.mirrorForeignLike(r.Context(), likerID, cert); err != nil {
			log.Error().Err(err).Str("likerID", likerID).Str("reedID", reedID).Msg("Failed to mirror foreign like locally")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
	return true
}

// mirrorForeignLike stores a like cert this server's own user received
// back from the reed's true home server, so a local read of "did I like
// this" is answered from this server's own DB without a round trip.
// likerID's own public key must already be cached locally — true for a
// local liker liking foreign content, which is the only case this is
// ever called for (a foreign liker's like never reaches this server at
// all; it's persisted directly by the reed's home server).
func (h *Handlers) mirrorForeignLike(ctx context.Context, likerID string, cert LikeCert) error {
	if err := h.services.db.UpsertReedIdentity(ctx, cert.ReedID); err != nil {
		return fmt.Errorf("upsert reed identity: %w", err)
	}
	if err := h.services.db.InsertReedLike(ctx, likerID, cert.UserSignature.Fingerprint, cert); err != nil {
		return fmt.Errorf("insert reed like: %w", err)
	}
	return nil
}

// proxyUnlikeToForeignReed mirrors proxyLikeToForeignReed for the
// DELETE/unlike direction: forwards to the home server, and on success
// removes the locally mirrored like row (if any) so both servers agree.
func (h *Handlers) proxyUnlikeToForeignReed(w http.ResponseWriter, r *http.Request, reedID string) (handled bool) {
	_, embeddedServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(reedID))
	if !ok || embeddedServerID == h.services.db.GetServerID() {
		return false
	}
	log := h.services.log.GetLogger(r.Context())

	peer, err := h.services.db.GetServerByID(r.Context(), embeddedServerID)
	if err != nil {
		internalServerError(w)
		return true
	}
	if peer == nil {
		writeResponse(w, http.StatusNotFound, "Not found")
		return true
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return true
	}
	values, _ := url.ParseQuery(string(body))
	likerID, ok := h.resolveActingUser(r, values.Get("likerID"))
	if !ok {
		writeResponse(w, http.StatusUnauthorized, "Could not resolve acting user")
		return true
	}

	respBody, status, err := h.forwardToPeer(r, peer.BaseURL, string(body))
	if err != nil {
		log.Error().Err(err).Str("target", peer.BaseURL).Msg("proxy unlike to peer server failed")
		h.logFederationServerAsync(peer.ID, "error", fmt.Sprintf("Proxy unlike to %s failed: %s", peer.BaseURL, err.Error()))
		writeResponse(w, http.StatusBadGateway, "Failed to reach peer server")
		return true
	}

	if status == http.StatusNoContent {
		if _, err := h.services.db.DeleteReedLike(r.Context(), likerID, reedID); err != nil {
			log.Error().Err(err).Str("likerID", likerID).Str("reedID", reedID).Msg("Failed to mirror foreign unlike locally")
		}
	}

	w.WriteHeader(status)
	if len(respBody) > 0 {
		_, _ = w.Write(respBody)
	}
	return true
}

// forwardToPeer signs and sends r (with body substituted for the
// already-read bytes) to baseURL, returning the peer's response body and
// status. Unlike proxyToPeer, it does not write to w itself — callers
// need to inspect the response before deciding what (if anything) to
// mirror locally.
func (h *Handlers) forwardToPeer(r *http.Request, baseURL, body string) (respBody []byte, status int, err error) {
	target := strings.TrimRight(baseURL, "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	httpReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	if err := h.setPeerProxyAuthHeaders(httpReq, body); err != nil {
		return nil, 0, fmt.Errorf("sign proxied peer request: %w", err)
	}
	resp, err := h.federationHTTPClient().Do(httpReq)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return respBody, resp.StatusCode, nil
}

// proxyToPeer forwards the request (including its body, if any) to
// baseURL, re-signed as this server's own key, and relays the response.
// Returns the peer's status (0 if none).
func (h *Handlers) proxyToPeer(w http.ResponseWriter, r *http.Request, peerServerID, baseURL string) int {
	log := h.services.log.GetLogger(r.Context())

	body, err := io.ReadAll(r.Body)
	if err != nil {
		internalServerError(w)
		return 0
	}

	// r.URL.Path already carries the "/api" prefix — gorilla/mux's
	// PathPrefix("/api").Subrouter() matches on it but does not strip it
	// from the request, unlike some other routers' subrouter semantics.
	target := strings.TrimRight(baseURL, "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	httpReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		internalServerError(w)
		return 0
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	if err := h.setPeerProxyAuthHeaders(httpReq, string(body)); err != nil {
		log.Error().Err(err).Str("target", target).Msg("failed to sign proxied peer request")
		internalServerError(w)
		return 0
	}
	resp, err := h.federationHTTPClient().Do(httpReq)
	if err != nil {
		log.Error().Err(err).Str("target", target).Msg("proxy to peer server failed")
		h.logFederationServerAsync(peerServerID, "error", fmt.Sprintf("Proxy request to %s failed: %s", target, err.Error()))
		writeResponse(w, http.StatusBadGateway, "Failed to reach peer server")
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest {
		log.Error().Int("status", resp.StatusCode).Str("target", target).Msg("peer server rejected proxied request")
		h.logFederationServerAsync(peerServerID, "error", fmt.Sprintf("Proxy request to %s rejected: status %d", target, resp.StatusCode))
	}

	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Error().Err(err).Str("target", target).Msg("failed to relay proxied response body")
	}
	return resp.StatusCode
}

// forwardFollowToPeer tells a peer that followerID follows/unfollows
// userID (local to the peer). Blocks until the peer confirms.
func (h *Handlers) forwardFollowToPeer(ctx context.Context, method, peerBaseURL, userID, followerID string) error {
	target := strings.TrimRight(peerBaseURL, "/") + "/api/users/" + userID + "/follow"
	body := "followerID=" + url.QueryEscape(followerID)
	httpReq, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := h.setPeerProxyAuthHeaders(httpReq, body); err != nil {
		return err
	}
	resp, err := h.federationHTTPClient().Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("peer responded %d", resp.StatusCode)
	}
	return nil
}

// setPeerProxyAuthHeaders signs an outgoing peer request (with the given
// body, "" for none) as this server's own key, matching
// buildCanonicalRequestString's shape.
func (h *Handlers) setPeerProxyAuthHeaders(req *http.Request, body string) error {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	canonical := req.Method + " " + req.URL.Path
	if req.URL.RawQuery != "" {
		canonical += "?" + req.URL.RawQuery
	}
	canonical += "\n\n" + body + "\n\n" + timestamp

	sigArmor, err := h.services.crypto.Sign(canonical, h.signingKey.Armor)
	if err != nil {
		return err
	}
	publicKeyID := string(identity.CanonicalID(h.services.db.GetServerID(), h.signingKey.Fingerprint))
	req.Header.Set("X-Syrinx-Public-Key-Id", publicKeyID)
	req.Header.Set("X-Syrinx-Signature", encoding.Base64Encode(sigArmor))
	req.Header.Set("X-Syrinx-Signature-Scope", "body")
	req.Header.Set("X-Syrinx-Timestamp", timestamp)
	return nil
}

// fetchPeerServerKeyArmor live-fetches a peer's own signing key armor over
// GET /api/server/key — peer keys are never persisted locally. The
// returned armor's fingerprint is checked against the caller's pinned
// expectation rather than trusted outright.
func (h *Handlers) fetchPeerServerKeyArmor(ctx context.Context, baseURL, serverID, fingerprint string) (string, error) {
	target := strings.TrimRight(baseURL, "/") + "/api/server/key"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := h.federationHTTPClient().Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch peer server key: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	armor := string(body)
	actualFingerprint, err := h.services.crypto.ExtractFingerprintFromArmor(armor)
	if err != nil {
		return "", fmt.Errorf("parse peer server key: %w", err)
	}
	if actualFingerprint != fingerprint {
		return "", fmt.Errorf("peer %s returned a key not matching pinned fingerprint", serverID)
	}
	return armor, nil
}

// resolvePublicKey returns fingerprint's key, fetching and caching it
// live from the owning peer first if this server has no local copy —
// the home-server-side counterpart of LikeReed/PostRipple's write-proxy:
// a peer-relayed like/ripple carries the ACTING USER's own signature,
// verified against THEIR key, which this server (the reed's home) may
// never have seen before if it's the first time that user has done
// anything this server needed their key for. Falls straight through to
// the ordinary local lookup for a local fingerprint (embeddedServerID ==
// this server's own) — no network call in that case.
func (h *Handlers) resolvePublicKey(ctx context.Context, fingerprint string) (*Key, error) {
	key, err := h.services.db.GetPublicKey(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	if key != nil {
		return key, nil
	}

	_, embeddedServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(fingerprint))
	if !ok || embeddedServerID == h.services.db.GetServerID() {
		// Malformed, or genuinely local and simply doesn't exist —
		// no peer to ask, and asking ourselves again would be pointless.
		return nil, nil
	}

	peer, err := h.services.db.GetServerByID(ctx, embeddedServerID)
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return nil, nil
	}

	return h.fetchAndCachePeerUserKey(ctx, peer.BaseURL, embeddedServerID, fingerprint)
}

// fetchAndCachePeerUserKey live-fetches a user key from its owning peer
// over the existing GET /api/keys/{id} route (already peer-callable —
// no new endpoint needed), verifies the peer's own countersignature over
// it before trusting anything in the response, and caches a minimal
// local copy so future likes/ripples from the same user don't need
// another round trip. Returns (nil, nil) — not an error — for any
// response the peer itself reports as absent/invalid, mirroring
// GetPublicKey's own nil-means-not-found convention.
func (h *Handlers) fetchAndCachePeerUserKey(ctx context.Context, baseURL, peerServerID, fingerprint string) (*Key, error) {
	target := strings.TrimRight(baseURL, "/") + "/api/keys/" + fingerprint
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if err := h.setPeerProxyAuthHeaders(httpReq, ""); err != nil {
		return nil, err
	}
	resp, err := h.federationHTTPClient().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch peer user key: status %d", resp.StatusCode)
	}

	var key Key
	if err := json.NewDecoder(resp.Body).Decode(&key); err != nil {
		return nil, fmt.Errorf("decode peer user key: %w", err)
	}
	armor, err := encoding.Base64Decode(key.Armor)
	if err != nil {
		return nil, fmt.Errorf("decode peer user key armor: %w", err)
	}

	// The peer's own signing key, live-fetched and pinned against what
	// the response itself claims signed it — never trusted from a local
	// cache (peer keys are never persisted, same rule fetchPeerServerKeyArmor
	// already follows for the transport-auth case).
	serverKeyArmor, err := h.fetchPeerServerKeyArmor(ctx, baseURL, peerServerID, key.ServerSignature.Fingerprint)
	if err != nil {
		return nil, fmt.Errorf("fetch peer server key for verification: %w", err)
	}

	// Reject anything whose embedded identity doesn't match what we
	// asked for, and verify the peer's countersignature actually covers
	// this exact key material — a peer must not be able to hand back
	// content for a different user, or unsigned/tampered key bytes.
	_, ownerServerID, ok := identity.ParseIdentityID(identity.IdentityID(key.UserID))
	if !ok || ownerServerID != peerServerID {
		return nil, fmt.Errorf("peer %s returned a key for a different server", peerServerID)
	}
	// serverSignature.armor travels base64-encoded on the wire, same as
	// key.Armor above (see GetKey/verifyPublicKey's own base64 handling of
	// this same field) — only successorSignature on a revocation cert is
	// raw armor.
	serverSigArmor, err := encoding.Base64Decode(key.ServerSignature.Armor)
	if err != nil {
		return nil, fmt.Errorf("decode peer key server signature: %w", err)
	}
	keyPayload := identity.BuildPublicKeyPayload(
		peerServerID, key.UserID, key.ID,
		key.ServerSignature.Fingerprint, armor,
		key.ServerSignature.SignedAt,
	)
	if err := h.services.crypto.VerifySignature(string(keyPayload), serverSigArmor, serverKeyArmor); err != nil {
		return nil, fmt.Errorf("peer %s key countersignature verification failed: %w", peerServerID, err)
	}

	if err := h.services.db.CachePeerUserKey(ctx, key.ID, key.UserID, ownerServerID, armor, key.ServerSignature); err != nil {
		return nil, fmt.Errorf("cache peer user key: %w", err)
	}

	key.Armor = armor
	return &key, nil
}

// logFederationInvitationAsync records a federation_log line for
// invitationID without blocking the caller or affecting its response — a
// failure to write a log line must never turn a successful (or already
// being reported) handshake step into a 500. Errors are logged locally
// instead.
func (h *Handlers) logFederationInvitationAsync(invitationID, level, message string) {
	go func() {
		ctx := context.Background()
		if err := h.services.db.logFederationInvitation(ctx, invitationID, level, message); err != nil {
			h.services.log.GetLogger(ctx).Error().Err(err).Str("invitationId", invitationID).Msg("Failed to write federation invitation log")
		}
	}()
}

// logFederationServerAsync records a federation_log line for serverID —
// see logFederationInvitationAsync.
func (h *Handlers) logFederationServerAsync(serverID, level, message string) {
	go func() {
		ctx := context.Background()
		if err := h.services.db.logFederationServer(ctx, serverID, level, message); err != nil {
			h.services.log.GetLogger(ctx).Error().Err(err).Str("serverId", serverID).Msg("Failed to write federation server log")
		}
	}()
}

// logFederationAttemptAsync records a federation_log line for attemptID —
// see logFederationInvitationAsync.
func (h *Handlers) logFederationAttemptAsync(attemptID, level, message string) {
	go func() {
		ctx := context.Background()
		if err := h.services.db.logFederationAttempt(ctx, attemptID, level, message); err != nil {
			h.services.log.GetLogger(ctx).Error().Err(err).Str("attemptId", attemptID).Msg("Failed to write federation attempt log")
		}
	}()
}

func (h *Handlers) CreateFederationInvitation(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	var req federationCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	remoteArmorB64 := strings.TrimSpace(req.RemotePublicKeyArmor)
	if remoteArmorB64 == "" {
		writeResponse(w, http.StatusBadRequest, "remotePublicKeyArmor is required")
		return
	}
	remoteArmor, err := encoding.Base64Decode(remoteArmorB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid remotePublicKeyArmor encoding")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeResponse(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > 255 {
		writeResponse(w, http.StatusBadRequest, "name is too long")
		return
	}

	remoteFingerprint, err := h.services.crypto.ExtractFingerprintFromArmor(remoteArmor)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid remote public key")
		return
	}

	inviteID, err := crypto.NewID()
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	secret, err := invites.NewSecret()
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	baseURL := h.federationBaseURL()
	signBytes := identity.BuildFederationInvitationPayload(
		inviteID,
		h.services.db.GetServerID(),
		baseURL,
		h.signingKey.Fingerprint,
		secret,
	)
	sigB64, err := h.federationSignServer(signBytes)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	serverPubArmor, err := h.services.db.GetServerPublicKeyByFingerprint(r.Context(), h.signingKey.Fingerprint)
	if err != nil || serverPubArmor == "" {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	payload := federationConnectionPayload{
		InviteID:       inviteID,
		ServerID:       h.services.db.GetServerID(),
		ServerName:     h.cfg.ServerName,
		BaseURL:        baseURL,
		Fingerprint:    h.signingKey.Fingerprint,
		PublicKeyArmor: serverPubArmor,
		Signature:      sigB64,
		Secret:         secret,
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	connectionString, err := h.services.crypto.Encrypt(plaintext, remoteArmor)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Failed to encrypt connection payload")
		return
	}

	secretHash := crypto.Hash(secret)
	now := time.Now().UTC().Truncate(time.Second)
	if err := h.services.db.InsertFederationInvitation(r.Context(), inviteID, name, caller, remoteFingerprint, remoteArmor, secretHash, connectionString, now); err != nil {
		if errors.Is(err, errFederationInvitationExists) {
			writeResponse(w, http.StatusConflict, "Invitation already exists")
			return
		}
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	writeResponse(w, http.StatusCreated, federationCreateResponse{
		InviteID:         inviteID,
		ConnectionString: connectionString,
		Status:           federationStatusNew,
	})
}

func (h *Handlers) ListFederationInvitations(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	rows, err := h.services.db.ListFederationInvitations(r.Context())
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	out := make([]federationListItemWire, 0, len(rows))
	for _, row := range rows {
		out = append(out, federationInvitationRowToWire(row))
	}
	writeResponse(w, http.StatusOK, out)
}

func federationInvitationRowToWire(row federationInvitationListRow) federationListItemWire {
	item := federationListItemWire{
		InviteID:          row.ID,
		Name:              row.Name,
		Status:            row.Status,
		CreatedBy:         row.CreatedBy,
		RemoteFingerprint: row.Fingerprint,
		CreatedAt:         row.CreatedAt.UTC().Format(time.RFC3339),
	}
	if row.AcceptedAt != nil {
		s := row.AcceptedAt.UTC().Format(time.RFC3339)
		item.AcceptedAt = &s
	}
	if row.ServerID != "" {
		sid := row.ServerID
		item.ServerID = &sid
	}
	if row.ReviewedBy != "" {
		rb := row.ReviewedBy
		item.ReviewedBy = &rb
	}
	if row.ReviewedAt != nil {
		s := row.ReviewedAt.UTC().Format(time.RFC3339)
		item.ReviewedAt = &s
	}
	if row.Status == federationStatusNew && row.ConnectionCiphertext != "" {
		cs := row.ConnectionCiphertext
		item.ConnectionString = &cs
	}
	return item
}

// ListFederationServers returns peer servers known to this instance —
// the responder side has no federation_invitation row to list (see
// OutgoingFederationAttempt's doc comment), so this is what surfaces a
// pasted connection's status until 03's approval workflow lands.
func (h *Handlers) ListFederationServers(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	rows, err := h.services.db.ListFederationServers(r.Context())
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	out := make([]federationServerWire, 0, len(rows))
	for _, row := range rows {
		out = append(out, federationServerRowToWire(row))
	}
	writeResponse(w, http.StatusOK, out)
}

// federationServerRowToWire converts one servers row to its wire shape,
// shared by ListFederationServers and GetFederationList.
func federationServerRowToWire(row federationServerListRow) federationServerWire {
	wire := federationServerWire{
		ServerID:          row.ID,
		Name:              row.Name,
		BaseURL:           row.BaseURL,
		Connected:         row.Connected,
		CreatedAt:         row.CreatedAt.UTC().Format(time.RFC3339),
		Revoked:           row.Revoked,
		DisconnectPending: row.DisconnectPending,
	}
	if row.Revoked {
		if row.RevokedAt != nil {
			t := row.RevokedAt.UTC().Format(time.RFC3339)
			wire.RevokedAt = &t
		}
		if row.RevokedBy != "" {
			wire.RevokedBy = &row.RevokedBy
		}
		if row.RevokedReason != "" {
			wire.RevokedReason = &row.RevokedReason
		}
	}
	if row.DisconnectPending {
		if row.DisconnectRequestedAt != nil {
			t := row.DisconnectRequestedAt.UTC().Format(time.RFC3339)
			wire.DisconnectRequestedAt = &t
		}
		if row.DisconnectRequestedBy != "" {
			wire.DisconnectRequestedBy = &row.DisconnectRequestedBy
		}
		if row.DisconnectReason != "" {
			wire.DisconnectReason = &row.DisconnectReason
		}
	}
	return wire
}

// GetFederationServerLogs returns federation_log lines for one peer server as
// plain text, one line per entry — the mesh tab's per-server drill-down (tap
// a server to see what actually happened during its handshake, since that
// spans two servers and can fail or stall asynchronously). Attempt log lines
// (pre-approval, written before a servers row existed — see
// logFederationAttempt's doc comment) come first, then server log lines,
// each section chronological — the attempt and the server together are one
// continuous story for this connection. Plain text instead of a JSON array:
// the client only ever displays these lines verbatim, so there's nothing to
// parse on either side.
func (h *Handlers) GetFederationServerLogs(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	serverID := mux.Vars(r)["id"]
	if serverID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	var sb strings.Builder

	// Every servers row has a backing attempt (ApproveFederationAttempt
	// backfills federation_attempt.server_id), but treat a miss as "no
	// attempt info" rather than an error.
	attempt, err := h.services.db.GetFederationAttemptForServer(r.Context(), serverID)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if attempt != nil {
		attemptRows, err := h.services.db.ListFederationAttemptLogs(r.Context(), attempt.ID)
		if err != nil {
			writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		writeFederationLogLines(&sb, attemptRows)
	}

	serverRows, err := h.services.db.ListFederationServerLogs(r.Context(), serverID)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	writeFederationLogLines(&sb, serverRows)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(sb.String()))
}

func writeFederationLogLines(sb *strings.Builder, rows []federationServerLogRow) {
	for _, row := range rows {
		fmt.Fprintf(sb, "%s [%s] %s\n",
			row.CreatedAt.UTC().Format(time.RFC3339), strings.ToUpper(row.Level), row.Message)
	}
}

// GetFederationServerInvitation returns the invitation that produced this
// server connection (invited-by/approved-by/dates), for the mesh tab's
// peer detail page. 200 with a JSON null body when this server was the
// responder — it has no local invitation row (see
// GetFederationInvitationForServer's doc comment), which is expected, not
// an error.
func (h *Handlers) GetFederationServerInvitation(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	serverID := mux.Vars(r)["id"]
	if serverID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	inv, err := h.services.db.GetFederationInvitationForServer(r.Context(), serverID)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if inv == nil {
		writeResponse(w, http.StatusOK, nil)
		return
	}
	writeResponse(w, http.StatusOK, federationInvitationRowToWire(*inv))
}

// GetFederationServerAttempt returns the (approved) attempt that produced
// this server connection — approved-by/dates, for the mesh tab's peer
// detail page. 200 with a JSON null body if no attempt row is found, which
// shouldn't happen for a real servers row but is treated as "no attempt
// info" rather than an error (see GetFederationAttemptForServer's doc
// comment).
func (h *Handlers) GetFederationServerAttempt(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	serverID := mux.Vars(r)["id"]
	if serverID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	attempt, err := h.services.db.GetFederationAttemptForServer(r.Context(), serverID)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if attempt == nil {
		writeResponse(w, http.StatusOK, nil)
		return
	}
	writeResponse(w, http.StatusOK, federationAttemptRowToWire(*attempt))
}

func federationAttemptRowToWire(row federationAttemptRow) federationAttemptWire {
	item := federationAttemptWire{
		AttemptID:        row.ID,
		RemoteServerID:   row.RemoteServerID,
		RemoteServerName: row.RemoteServerName,
		BaseURL:          row.BaseURL,
		Fingerprint:      row.Fingerprint,
		CreatedAt:        row.CreatedAt.UTC().Format(time.RFC3339),
		Status:           row.Status,
	}
	if row.InvitationID != "" {
		id := row.InvitationID
		item.InvitationID = &id
	}
	if row.ServerID != "" {
		id := row.ServerID
		item.ServerID = &id
	}
	if row.ApprovedBy != "" {
		ab := row.ApprovedBy
		item.ApprovedBy = &ab
	}
	if row.ApprovedAt != nil {
		s := row.ApprovedAt.UTC().Format(time.RFC3339)
		item.ApprovedAt = &s
	}
	if row.RejectedBy != "" {
		rb := row.RejectedBy
		item.RejectedBy = &rb
	}
	if row.RejectedAt != nil {
		s := row.RejectedAt.UTC().Format(time.RFC3339)
		item.RejectedAt = &s
	}
	if row.RejectedReason != "" {
		reason := row.RejectedReason
		item.RejectedReason = &reason
	}
	return item
}

// GetFederationList returns invitations, attempts, and servers together —
// the mesh tab's single combined-view fetch. Each item still links to its
// own detail/logs endpoint by id; this just avoids three separate
// round trips to render the list.
func (h *Handlers) GetFederationList(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	invRows, err := h.services.db.ListFederationInvitations(r.Context())
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	attemptRows, err := h.services.db.ListFederationAttempts(r.Context())
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	serverRows, err := h.services.db.ListFederationServers(r.Context())
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	out := federationListWire{
		Invitations: make([]federationListItemWire, 0, len(invRows)),
		Attempts:    make([]federationAttemptWire, 0, len(attemptRows)),
		Servers:     make([]federationServerWire, 0, len(serverRows)),
	}
	for _, row := range invRows {
		out.Invitations = append(out.Invitations, federationInvitationRowToWire(row))
	}
	for _, row := range attemptRows {
		out.Attempts = append(out.Attempts, federationAttemptRowToWire(row))
	}
	for _, row := range serverRows {
		out.Servers = append(out.Servers, federationServerRowToWire(row))
	}
	writeResponse(w, http.StatusOK, out)
}

// GetFederationAttempt returns one attempt by id — the mesh tab's
// /mesh/attempt/{id} detail page.
func (h *Handlers) GetFederationAttempt(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	attemptID := strings.TrimSpace(mux.Vars(r)["id"])
	if attemptID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	attempt, err := h.services.db.GetFederationAttempt(r.Context(), attemptID)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if attempt == nil {
		writeResponse(w, http.StatusNotFound, "Attempt not found")
		return
	}
	writeResponse(w, http.StatusOK, federationAttemptRowToWire(*attempt))
}

// GetFederationAttemptLogs returns federation_log lines for one attempt as
// plain text, one line per entry — see GetFederationServerLogs's doc
// comment for why plain text.
func (h *Handlers) GetFederationAttemptLogs(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	attemptID := strings.TrimSpace(mux.Vars(r)["id"])
	if attemptID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	rows, err := h.services.db.ListFederationAttemptLogs(r.Context(), attemptID)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	var sb strings.Builder
	writeFederationLogLines(&sb, rows)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(sb.String()))
}

// ApproveFederationAttempt creates the servers row — see
// ApproveFederationAttempt's (DataService) doc comment.
func (h *Handlers) ApproveFederationAttempt(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}
	callerIsRoot, err := h.isRoot(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	attemptID := strings.TrimSpace(mux.Vars(r)["id"])
	if attemptID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	err = h.services.db.ApproveFederationAttempt(r.Context(), attemptID, caller, time.Now().UTC().Truncate(time.Second), callerIsRoot)
	switch {
	case errors.Is(err, errFederationAttemptNotFound):
		writeResponse(w, http.StatusNotFound, "Attempt not found")
	case errors.Is(err, errFederationAttemptNotPending):
		writeResponse(w, http.StatusConflict, "Attempt already decided")
	case errors.Is(err, errFederationSameApprover):
		writeResponse(w, http.StatusForbidden, "A different admin must approve this connection")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("attemptId", attemptID).Msg("federation attempt approve failed")
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		h.logFederationAttemptAsync(attemptID, federationLogInfo,
			fmt.Sprintf("Approved by %s", caller))
		writeResponse(w, http.StatusOK, map[string]string{"attemptId": attemptID, "status": "approved"})
	}
}

// RejectFederationAttempt requires a non-empty reason. Unlike the old
// servers-row-based reject, the attempt row (and its logs) are never
// deleted — status just flips to rejected.
func (h *Handlers) RejectFederationAttempt(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	attemptID := strings.TrimSpace(mux.Vars(r)["id"])
	if attemptID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		writeResponse(w, http.StatusBadRequest, "reason is required")
		return
	}

	err = h.services.db.RejectFederationAttempt(r.Context(), attemptID, caller, reason, time.Now().UTC().Truncate(time.Second))
	switch {
	case errors.Is(err, errFederationAttemptNotFound):
		writeResponse(w, http.StatusNotFound, "Attempt not found")
	case errors.Is(err, errFederationAttemptNotPending):
		writeResponse(w, http.StatusConflict, "Attempt already decided")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("attemptId", attemptID).Msg("federation attempt reject failed")
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		h.logFederationAttemptAsync(attemptID, federationLogError,
			fmt.Sprintf("Rejected by %s: %s", caller, reason))
		writeResponse(w, http.StatusOK, map[string]string{"attemptId": attemptID, "status": "rejected"})
	}
}

// RequestFederationServerDisconnect stages a disconnect; any admin may
// request one, with a required reason. The peer stays trusted until a
// second admin confirms via ConfirmFederationServerDisconnect.
func (h *Handlers) RequestFederationServerDisconnect(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	serverID := strings.TrimSpace(mux.Vars(r)["id"])
	if serverID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		writeResponse(w, http.StatusBadRequest, "reason is required")
		return
	}

	err = h.services.db.RequestFederationServerDisconnect(r.Context(), serverID, caller, reason, time.Now().UTC().Truncate(time.Second))
	switch {
	case errors.Is(err, errFederationServerNotFound):
		writeResponse(w, http.StatusNotFound, "Server not found")
	case errors.Is(err, errFederationServerAlreadyRevoked):
		writeResponse(w, http.StatusConflict, "Server already revoked")
	case errors.Is(err, errFederationDisconnectAlreadyRequested):
		writeResponse(w, http.StatusConflict, "Disconnect already requested for this server")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("serverId", serverID).Msg("federation server disconnect request failed")
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		h.logFederationServerAsync(serverID, federationLogInfo,
			fmt.Sprintf("Disconnect requested by %s: %s", caller, reason))
		writeResponse(w, http.StatusOK, map[string]string{"serverId": serverID, "status": "disconnect_pending"})
	}
}

// ConfirmFederationServerDisconnect finalizes a staged disconnect,
// requiring the confirming admin to differ from the requester (root
// exempt). This is the point the peer is actually revoked and notified.
func (h *Handlers) ConfirmFederationServerDisconnect(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}
	callerIsRoot, err := h.isRoot(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	serverID := strings.TrimSpace(mux.Vars(r)["id"])
	if serverID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	reason, err := h.services.db.ConfirmFederationServerDisconnect(r.Context(), serverID, caller, time.Now().UTC().Truncate(time.Second), callerIsRoot)
	switch {
	case errors.Is(err, errFederationServerNotFound):
		writeResponse(w, http.StatusNotFound, "Server not found")
	case errors.Is(err, errFederationDisconnectNotRequested):
		writeResponse(w, http.StatusConflict, "No disconnect request is pending for this server")
	case errors.Is(err, errFederationSameApprover):
		writeResponse(w, http.StatusForbidden, "A different admin must confirm this disconnect")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("serverId", serverID).Msg("federation server disconnect confirm failed")
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		h.logFederationServerAsync(serverID, federationLogError,
			fmt.Sprintf("Disconnected by %s (confirmed): %s", caller, reason))
		go func() {
			if err := h.notifyPeerOfDisconnect(context.Background(), serverID, reason); err != nil {
				h.services.log.GetLogger(context.Background()).Warn().Err(err).Str("serverId", serverID).Msg("failed to notify peer of disconnect")
			}
		}()
		writeResponse(w, http.StatusOK, map[string]string{"serverId": serverID, "status": "revoked"})
	}
}

// CancelFederationServerDisconnect withdraws a staged disconnect request
// before a second admin confirms it. Any admin may cancel — same
// visibility as requesting.
func (h *Handlers) CancelFederationServerDisconnect(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	serverID := strings.TrimSpace(mux.Vars(r)["id"])
	if serverID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	err = h.services.db.CancelFederationServerDisconnect(r.Context(), serverID)
	switch {
	case errors.Is(err, errFederationDisconnectNotRequested):
		writeResponse(w, http.StatusConflict, "No disconnect request is pending for this server")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("serverId", serverID).Msg("federation server disconnect cancel failed")
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		h.logFederationServerAsync(serverID, federationLogInfo,
			fmt.Sprintf("Disconnect request cancelled by %s", caller))
		writeResponse(w, http.StatusOK, map[string]string{"serverId": serverID, "status": "disconnect_cancelled"})
	}
}

// PurgeFederationServer permanently deletes a disconnected peer's row
// and every local reed/identity it owns. Root only — this is
// irreversible and goes further than a normal admin disconnect.
func (h *Handlers) PurgeFederationServer(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	root, err := h.isRoot(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !root {
		writeResponse(w, http.StatusForbidden, "Root required")
		return
	}

	serverID := strings.TrimSpace(mux.Vars(r)["id"])
	if serverID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	err = h.services.db.PurgeFederationServer(r.Context(), serverID)
	switch {
	case errors.Is(err, errFederationServerNotFound):
		writeResponse(w, http.StatusNotFound, "Server not found")
	case errors.Is(err, errFederationServerNotRevoked):
		writeResponse(w, http.StatusConflict, "Server must be disconnected before it can be deleted")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("serverId", serverID).Msg("federation server purge failed")
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		writeResponse(w, http.StatusOK, map[string]string{"serverId": serverID, "status": "purged"})
	}
}

// GetFederationUserIdentity is the IdP endpoint an established peer calls
// to resolve one of THIS server's users. Peer-authenticated; userID must
// be local — a peer can't vouch for a third server's user.
func (h *Handlers) GetFederationUserIdentity(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	userID := strings.TrimSpace(mux.Vars(r)["userID"])
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}
	if !strings.HasSuffix(userID, "@"+h.services.db.GetServerID()) {
		writeResponse(w, http.StatusBadRequest, "userID must be local to this server")
		return
	}

	removal, err := h.services.db.GetAccountRemoval(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading account removal")
		internalServerError(w)
		return
	}
	if removal != nil {
		writeResponse(w, http.StatusGone, h.accountRemovalWire(removal))
		return
	}

	user, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Str("peerServerId", peerServerID).Err(err).Msg("Error getting user profile for peer")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusNotFound, "User not found")
		return
	}

	fingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), userID)
	if err != nil {
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, federationUserIdentityWire{
		User:                 user,
		ActiveKeyFingerprint: fingerprint,
	})
}

func (h *Handlers) RevokeFederationInvitation(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	id := strings.TrimSpace(mux.Vars(r)["id"])
	if id == "" {
		writeResponse(w, http.StatusBadRequest, "id is required")
		return
	}

	err = h.services.db.RevokeFederationInvitation(r.Context(), id, caller, time.Now().UTC().Truncate(time.Second))
	switch {
	case errors.Is(err, errFederationInvitationNotFound):
		writeResponse(w, http.StatusNotFound, "Invitation not found")
	case errors.Is(err, errFederationInvitationNotRevocable):
		writeResponse(w, http.StatusBadRequest, "Invitation cannot be revoked")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("id", id).Msg("federation invitation revoke failed")
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		writeResponse(w, http.StatusOK, map[string]string{"inviteId": id, "status": federationStatusCanceled})
	}
}

// IncomingFederationAttempt is the initiator-side callback a remote
// (responder) server calls after decrypting a connection string it was
// given out-of-band. No session auth — legitimacy is proven by the
// invitation secret and the responder's own signature. Allowlisted in
// signatureAuthMiddleware.
func (h *Handlers) IncomingFederationAttempt(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	inviteID := strings.TrimSpace(mux.Vars(r)["id"])
	if inviteID == "" {
		writeResponse(w, http.StatusBadRequest, "id is required")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	var req federationConnectRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ServerID = strings.TrimSpace(req.ServerID)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.Fingerprint = strings.TrimSpace(req.Fingerprint)
	req.Secret = strings.TrimSpace(req.Secret)
	if req.ServerID == "" || req.BaseURL == "" || req.Fingerprint == "" ||
		req.Signature == "" || req.Secret == "" {
		writeResponse(w, http.StatusBadRequest, "Missing required fields")
		return
	}
	if !strings.HasPrefix(req.BaseURL, "https://") && !h.cfg.FederationAllowInsecureHTTP {
		writeResponse(w, http.StatusBadRequest, "baseUrl must be https")
		return
	}

	inv, err := h.services.db.GetFederationInvitation(r.Context(), inviteID)
	if err != nil {
		log.Error().Err(err).Str("inviteId", inviteID).Msg("Error loading federation invitation")
		internalServerError(w)
		return
	}
	if inv == nil {
		// Nothing to attach a log line to — an unknown invite id isn't a
		// real invitation's problem to surface.
		writeResponse(w, http.StatusNotFound, "Invitation not found")
		return
	}

	h.logFederationInvitationAsync(inviteID, federationLogInfo,
		fmt.Sprintf("Incoming connect attempt from server %s (%s)", req.ServerID, req.BaseURL))

	if inv.Status != federationStatusNew {
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: invitation is not new")
		writeResponse(w, http.StatusConflict, "Invitation is not new")
		return
	}

	if subtle.ConstantTimeCompare(crypto.Hash(req.Secret), inv.SecretHash) != 1 {
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: invalid secret")
		writeResponse(w, http.StatusForbidden, "Invalid secret")
		return
	}
	// req.Fingerprint must be the exact key A's admin pasted when creating
	// this invitation — the connection string was encrypted to it, so only
	// the holder of the matching private key could have decrypted the
	// secret in the first place. This also pins which key we verify the
	// signature against, instead of trusting a self-reported fingerprint.
	if req.Fingerprint != inv.Fingerprint {
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: fingerprint does not match invitation")
		writeResponse(w, http.StatusForbidden, "Fingerprint does not match invitation")
		return
	}

	signBytes := identity.BuildFederationConnectPayload(inviteID, req.ServerID, req.BaseURL, req.Fingerprint)
	sigArmor, err := encoding.Base64Decode(req.Signature)
	if err != nil {
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: invalid signature encoding")
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	if err := h.services.crypto.VerifyDetachedSignature(string(signBytes), sigArmor, inv.PublicKey); err != nil {
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: invalid signature")
		writeResponse(w, http.StatusBadRequest, "Invalid signature")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	attemptID, err := h.services.db.MarkFederationInvitationAccepted(r.Context(), inviteID, federationPeer{
		ServerID:    req.ServerID,
		ServerName:  req.ServerName,
		BaseURL:     req.BaseURL,
		Fingerprint: req.Fingerprint,
	}, now)
	switch {
	case errors.Is(err, errFederationInvitationNotFound):
		writeResponse(w, http.StatusNotFound, "Invitation not found")
	case errors.Is(err, errFederationInvitationNotNew):
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: invitation is not new")
		writeResponse(w, http.StatusConflict, "Invitation is not new")
	case err != nil:
		log.Error().Err(err).Str("inviteId", inviteID).Msg("federation connect accept failed")
		h.logFederationInvitationAsync(inviteID, federationLogError, "Failed to record connect attempt: internal error")
		internalServerError(w)
	default:
		h.logFederationInvitationAsync(inviteID, federationLogInfo, "Connect attempt accepted; pending approval")
		// MarkFederationInvitationAccepted just created a federation_attempt
		// row (pending — no servers row yet, see ApproveFederationAttempt),
		// so federation_attempt_log's FK is satisfiable now — mirror the
		// responder's logFederationAttemptAsync calls in
		// OutgoingFederationAttempt so the initiator's mesh/attempt view
		// isn't empty for a connection it originated.
		h.logFederationAttemptAsync(attemptID, federationLogInfo,
			fmt.Sprintf("Handshake verified with server %s (%s); awaiting approval", req.ServerID, req.BaseURL))
		writeResponse(w, http.StatusOK, federationConnectResponse{Status: federationStatusAccepted, ServerID: req.ServerID})
	}
}

// OutgoingFederationAttempt is the responder-side action: an admin here
// pastes a connection string they received out-of-band from the
// initiator's admin. This decrypts it, verifies the initiator's signature,
// creates a pending federation_attempt row (so there's somewhere to log
// against even if the next step fails), and signs and posts our own
// callback to the initiator's connect endpoint. No servers row is created
// here even once the initiator confirms — that only means the handshake
// verified, not that a second admin has approved the connection; see
// ApproveFederationAttempt for where servers actually gets a row.
func (h *Handlers) OutgoingFederationAttempt(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeResponse(w, http.StatusForbidden, "Admin required")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	var req federationAttemptRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	connectionString := strings.TrimSpace(req.ConnectionString)
	if connectionString == "" {
		writeResponse(w, http.StatusBadRequest, "connectionString is required")
		return
	}

	plaintext, err := h.services.crypto.Decrypt(connectionString, h.signingKey.Armor)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Failed to decrypt connection string")
		return
	}
	var payload federationConnectionPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid connection payload")
		return
	}
	if payload.InviteID == "" || payload.ServerID == "" || payload.BaseURL == "" ||
		payload.Fingerprint == "" || payload.PublicKeyArmor == "" || payload.Signature == "" || payload.Secret == "" {
		writeResponse(w, http.StatusBadRequest, "Incomplete connection payload")
		return
	}
	if !strings.HasPrefix(payload.BaseURL, "https://") && !h.cfg.FederationAllowInsecureHTTP {
		writeResponse(w, http.StatusBadRequest, "baseUrl must be https")
		return
	}

	remoteFingerprint, err := h.services.crypto.ExtractFingerprintFromArmor(payload.PublicKeyArmor)
	if err != nil || remoteFingerprint != payload.Fingerprint {
		writeResponse(w, http.StatusBadRequest, "Public key does not match claimed fingerprint")
		return
	}
	initiatorSignBytes := identity.BuildFederationInvitationPayload(
		payload.InviteID, payload.ServerID, payload.BaseURL, payload.Fingerprint, payload.Secret,
	)
	initiatorSigArmor, err := encoding.Base64Decode(payload.Signature)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	if err := h.services.crypto.VerifyDetachedSignature(string(initiatorSignBytes), initiatorSigArmor, payload.PublicKeyArmor); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid initiator signature")
		return
	}

	// Create a pending federation_attempt before attempting the handshake —
	// so there's somewhere to log against even if the next steps fail,
	// instead of writing nothing until success.
	now := time.Now().UTC().Truncate(time.Second)
	peer := federationPeer{
		ServerID:    payload.ServerID,
		ServerName:  payload.ServerName,
		BaseURL:     payload.BaseURL,
		Fingerprint: payload.Fingerprint,
	}
	attemptID, err := h.services.db.CreateFederationAttempt(r.Context(), peer, now)
	if err != nil {
		log.Error().Err(err).Msg("failed to record federation attempt")
		internalServerError(w)
		return
	}
	h.logFederationAttemptAsync(attemptID, federationLogInfo,
		fmt.Sprintf("Attempting to redeem invitation from server %s (%s)", payload.ServerID, payload.BaseURL))

	localBaseURL := h.federationBaseURL()
	localServerID := h.services.db.GetServerID()
	connectSignBytes := identity.BuildFederationConnectPayload(payload.InviteID, localServerID, localBaseURL, h.signingKey.Fingerprint)
	connectSigB64, err := h.federationSignServer(connectSignBytes)
	if err != nil {
		internalServerError(w)
		return
	}

	connectReq := federationConnectRequest{
		ServerID:    localServerID,
		ServerName:  h.cfg.ServerName,
		BaseURL:     localBaseURL,
		Fingerprint: h.signingKey.Fingerprint,
		Signature:   connectSigB64,
		Secret:      payload.Secret,
	}
	connectBody, err := json.Marshal(connectReq)
	if err != nil {
		internalServerError(w)
		return
	}

	connectURL := strings.TrimRight(payload.BaseURL, "/") + "/api/federation/connect/" + payload.InviteID
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, connectURL, bytes.NewReader(connectBody))
	if err != nil {
		internalServerError(w)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := h.federationHTTPClient().Do(httpReq)
	if err != nil {
		log.Error().Err(err).Str("connectURL", connectURL).Msg("federation connect callback failed")
		h.logFederationAttemptAsync(attemptID, federationLogError,
			fmt.Sprintf("Failed to reach %s (%s): %s", payload.ServerID, payload.BaseURL, err.Error()))
		writeResponse(w, http.StatusBadGateway, "Failed to reach initiator server")
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Info().Int("status", resp.StatusCode).Str("connectURL", connectURL).Msg("federation connect callback rejected")
		h.logFederationAttemptAsync(attemptID, federationLogError,
			fmt.Sprintf("Rejected by %s (%s): %s", payload.ServerID, payload.BaseURL, string(respBody)))
		writeResponse(w, resp.StatusCode, string(respBody))
		return
	}
	var connectResp federationConnectResponse
	if err := json.Unmarshal(respBody, &connectResp); err != nil {
		internalServerError(w)
		return
	}

	// No servers row yet: the handshake having verified is not the same as
	// a second admin having approved the connection — see
	// ApproveFederationAttempt.
	h.logFederationAttemptAsync(attemptID, federationLogInfo,
		fmt.Sprintf("Handshake verified with server %s (%s); awaiting approval", payload.ServerID, payload.BaseURL))

	writeResponse(w, http.StatusOK, federationAttemptResponse{Status: federationStatusAccepted, ServerID: payload.ServerID})
}

// //////////// //
//   Ripples    //
// //////////// //

// MaxRippleContentChars mirrors MaxReedVisibleChars — ripples reuse the
// reed visible-length cap, validated as a plain code-point count since
// ripple content is never markdown.
const MaxRippleContentChars = 140

// RippleWire is the JSON shape of one ripple response, shared by POST and
// GET. The response's id is `hash` — the hex-SHA256 digest of its signed
// server payload — not a randomly minted id.
type RippleWire struct {
	Hash            string          `json:"hash"`
	ThreadID        string          `json:"threadID"`
	UserID          string          `json:"userID"`
	Content         string          `json:"content"`
	ReplyingTo      *string         `json:"replyingTo"`
	Deleted         bool            `json:"deleted"`
	PostedAt        time.Time       `json:"postedAt"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

func rippleWire(r *Ripple) RippleWire {
	return RippleWire{
		Hash:            r.ID,
		ThreadID:        r.ThreadID,
		UserID:          r.UserID,
		Content:         r.Content,
		ReplyingTo:      r.ReplyingTo,
		Deleted:         r.Deleted,
		PostedAt:        r.PostedAt,
		UserSignature:   r.UserSignature,
		ServerSignature: r.ServerSignature,
	}
}

// checkRippleParentReed validates the parent reed for a ripples request,
// writing the appropriate 404/410 response and returning ok=false if the
// caller should stop. Mirrors GetReed/GetReedEchoCount's convention (not
// GetReedReplies, which omits the removal checks). reedID is canonical.
func (h *Handlers) checkRippleParentReed(w http.ResponseWriter, r *http.Request, reedID string) (ok bool) {
	result, err := h.services.db.GetReedOrRemovalCert(r.Context(), reedID)
	if err != nil {
		internalServerError(w)
		return false
	}
	if result.AccountRemoval != nil {
		writeResponse(w, http.StatusGone, h.accountRemovalWire(result.AccountRemoval))
		return false
	}
	if result.ReedRemoval != nil {
		writeResponse(w, http.StatusGone, h.reedRemovalWire(result.ReedRemoval))
		return false
	}
	if result.Reed == nil {
		writeResponse(w, http.StatusNotFound, "Post not found")
		return false
	}
	blank, err := h.services.db.IsBlankEcho(r.Context(), reedID)
	if err != nil && !errors.Is(err, ErrReedNotFound) {
		internalServerError(w)
		return false
	}
	if blank {
		writeResponse(w, http.StatusBadRequest, "Empty echoes have no ripples — use the original reed instead")
		return false
	}
	return true
}

type postRippleRequest struct {
	Content       string  `json:"content"`
	ThreadID      string  `json:"threadID"`
	ReplyingTo    *string `json:"replyingTo"`
	Fingerprint   string  `json:"fingerprint"`
	UserSignature string  `json:"userSignature"`
	// UserID is the acting user's canonical id — only read when the
	// request arrives via peer relay (see resolveActingUser); a local
	// caller's own session already provides this.
	UserID string `json:"userID"`

	// Proof is the parent reed's base64 server-signature armor — proof of
	// possession, see checkReedPossession.
	Proof string `json:"proof"`
}

// PostRipple handles POST /api/reeds/{userID}/{reedID}/ripples. The
// caller submits a user-signed payload plus proof of possession of the
// parent reed (see checkReedPossession — posting requires the same proof
// as listing); this handler verifies that signature, then countersigns
// and hashes the server payload via DataService.PostRipple.
func (h *Handlers) PostRipple(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	reedUserID := mux.Vars(r)["userID"]
	reedID := mux.Vars(r)["reedID"]
	if reedUserID == "" || reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	canonicalReedID := string(identity.AppendEntity(identity.IdentityID(reedUserID), reedID))

	if handled, _ := h.proxyIfForeign(w, r, canonicalReedID); handled {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	var req postRippleRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	callerID, ok := h.resolveActingUser(r, req.UserID)
	if !ok {
		writeResponse(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}

	// `checkReedPossession` only looks at the live reeds row, so a removed
	// reed looks identical to a nonexistent one from its point of view —
	// att == nil either way. Running it first would collapse that 410 into
	// a 400 (no live reed to prove possession against), losing both the
	// correct status code and the removal-cert payload callers may depend on.
	if !h.checkRippleParentReed(w, r, canonicalReedID) {
		return
	}

	if !h.checkReedPossession(w, r.Context(), canonicalReedID, req.Proof) {
		return
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeResponse(w, http.StatusBadRequest, "Comment cannot be empty")
		return
	}
	if utf8.RuneCountInString(content) > MaxRippleContentChars {
		writeResponse(w, http.StatusBadRequest, "Comment is too long (max 140 characters)")
		return
	}

	if _, err := uuid.Parse(req.ThreadID); err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid threadID")
		return
	}

	if req.ReplyingTo != nil {
		target, err := h.services.db.GetRipple(r.Context(), *req.ReplyingTo)
		if errors.Is(err, ErrRippleNotFound) {
			writeResponse(w, http.StatusBadRequest, "Cannot reply to a comment that doesn't exist")
			return
		}
		if err != nil {
			internalServerError(w)
			return
		}
		if target.ReedID != canonicalReedID {
			writeResponse(w, http.StatusBadRequest, "Cannot reply to a comment on a different post")
			return
		}
		if target.ThreadID != req.ThreadID {
			writeResponse(w, http.StatusBadRequest, "Reply must use the same thread as the comment it replies to.")
			return
		}
	}

	if req.Fingerprint == "" || req.UserSignature == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `fingerprint` and `userSignature` are required")
		return
	}
	// req.Fingerprint travels bare over the wire; join with callerID
	// (already canonical) before any lookup or signed-payload use.
	req.Fingerprint = string(identity.AppendEntity(identity.IdentityID(callerID), req.Fingerprint))
	pubKey, err := h.resolvePublicKey(r.Context(), req.Fingerprint)
	if err != nil {
		log.Error().Str("userID", callerID).Str("fingerprint", req.Fingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	userSigArmor, err := encoding.Base64Decode(req.UserSignature)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	replyingToVal := ""
	if req.ReplyingTo != nil {
		replyingToVal = *req.ReplyingTo
	}
	userPayload := identity.BuildRippleUserPayload(
		canonicalReedID, callerID, req.Fingerprint, req.ThreadID, replyingToVal, content,
	)
	if err := h.services.crypto.VerifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().Str("userID", callerID).Str("reedID", canonicalReedID).Err(err).Msg("ripple signature verification failed")
		writeResponse(w, http.StatusBadRequest, "Invalid signature.")
		return
	}

	resp, err := h.services.db.PostRipple(
		r.Context(), canonicalReedID, callerID, content, req.ThreadID, req.ReplyingTo,
		req.Fingerprint, req.UserSignature, h.countersign, time.Now(),
	)
	if errors.Is(err, ErrRippleThreadMismatch) {
		writeResponse(w, http.StatusBadRequest, "Reply must use the same thread as the comment it replies to.")
		return
	}
	if err != nil {
		log.Error().Str("userID", reedUserID).Str("reedID", reedID).Err(err).Msg("Error posting ripple")
		internalServerError(w)
		return
	}

	wire := rippleWire(resp)
	writeResponse(w, http.StatusCreated, wire)

	h.broadcastChan <- realtime.BroadcastMessage{
		Type:   realtime.RipplePosted,
		UserID: reedUserID,
		ReedID: reedID,
		Ripple: realtimeRippleWire(&wire),
	}
}

// realtimeRippleWire converts the HTTP-layer RippleWire into realtime's
// own duplicated wire shape (realtime cannot import the main package —
// same reasoning as ReedRemovalWire/AccountRemovalWire).
func realtimeRippleWire(w *RippleWire) *realtime.RippleWire {
	return &realtime.RippleWire{
		Hash:       w.Hash,
		ThreadID:   w.ThreadID,
		UserID:     w.UserID,
		Content:    w.Content,
		ReplyingTo: w.ReplyingTo,
		Deleted:    w.Deleted,
		PostedAt:   w.PostedAt,
		UserSignature: realtime.UserSignatureWire{
			Fingerprint: w.UserSignature.Fingerprint,
			Armor:       w.UserSignature.Armor,
		},
		ServerSignature: realtime.ServerSignatureWire{
			ServerID:    w.ServerSignature.ServerID,
			Fingerprint: w.ServerSignature.Fingerprint,
			Armor:       w.ServerSignature.Armor,
			Timestamp:   w.ServerSignature.SignedAt,
		},
	}
}

type rippleListResponse struct {
	Responses  []RippleWire `json:"responses"`
	HasMore    bool         `json:"hasMore"`
	NextCursor string       `json:"nextCursor,omitempty"`
	// ExpiresAt is the absolute instant the whole ripples section on this
	// reed disappears — the client converts this to a local monotonic
	// countdown once at fetch time (Date.parse(expiresAt) - Date.now(),
	// then ticked via performance.now()), so the animation itself stays
	// skew-resistant, but the reference point can be independently
	// re-validated against any fresh read (poll, reload, WS event)
	// without the server needing to compute a fresh relative delta each
	// time. The client must treat an already-past expiresAt as "do not
	// render this section's ripples," even if the server still sent them
	// (the sweep may not have run yet).
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// checkReedPossession requires the caller to prove they've actually seen
// the parent reed before it will act on its ripples — list them, or post
// a new one: a ripple thread is only discoverable/joinable by someone the
// reed was relayed to (e.g. an echo in their feed), never by guessing a
// (userID, reedID) pair. Proof is the reed's own base64-encoded
// server-signature armor — something only visible on a copy of the reed
// itself — echoed back by the caller (proof extracts it: the raw request
// body for GetRipples, a JSON field for PostRipple).
func (h *Handlers) checkReedPossession(w http.ResponseWriter, ctx context.Context, reedID, proof string) (ok bool) {
	proof = strings.TrimSpace(proof)
	if proof == "" {
		writeResponse(w, http.StatusBadRequest, "Proof of posession required")
		return false
	}

	att, err := h.services.db.GetReedAttestation(ctx, reedID)
	if err != nil {
		internalServerError(w)
		return false
	}
	if att == nil {
		writeResponse(w, http.StatusNotFound, "Post not found")
		return false
	}
	if proof != att.ServerSignature {
		writeResponse(w, http.StatusForbidden, "Invalid proof of posession")
		return false
	}
	return true
}

// GetRipples handles QUERY /api/reeds/{userID}/{reedID}/ripples — listing
// ripples requires proving possession of the parent reed (see
// checkReedPossession), so this is a QUERY (body-bearing, safe/read-only)
// rather than a plain GET. If QUERY turns out not to be viable end-to-end,
// swap to the commented-out POST /ripples/proof route in main.go instead.
func (h *Handlers) GetRipples(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	reedUserID := mux.Vars(r)["userID"]
	reedID := mux.Vars(r)["reedID"]
	if reedUserID == "" || reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	canonicalReedID := string(identity.AppendEntity(identity.IdentityID(reedUserID), reedID))

	if handled, _ := h.proxyIfForeign(w, r, canonicalReedID); handled {
		return
	}

	// Why `checkRippleParentReed` should run before `checkReedPossession`:
	// it's the only way to tell a removed reed (410 + cert body) apart from
	// one that never existed (404), which checkReedPossession's
	// live-row-only lookup can't distinguish. See the ordering note above
	// PostRipple's equivalent pair of checks.
	if !h.checkRippleParentReed(w, r, canonicalReedID) {
		return
	}

	proofBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if !h.checkReedPossession(w, r.Context(), canonicalReedID, string(proofBody)) {
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeResponse(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	before := strings.TrimSpace(r.URL.Query().Get("before"))
	if before != "" {
		if _, err := decodeRippleCursor(before); err != nil {
			writeResponse(w, http.StatusBadRequest, "Invalid before cursor")
			return
		}
	}

	list, err := h.services.db.ListRipples(r.Context(), canonicalReedID, limit, before)
	if err != nil {
		log.Error().Str("userID", reedUserID).Str("reedID", reedID).Err(err).Msg("Error listing ripples")
		internalServerError(w)
		return
	}

	expiresAt, err := h.services.db.GetRipplesExpiresAt(r.Context(), canonicalReedID)
	if err != nil {
		internalServerError(w)
		return
	}

	wires := make([]RippleWire, len(list.Ripples))
	for i := range list.Ripples {
		wires[i] = rippleWire(&list.Ripples[i])
	}

	resp := rippleListResponse{
		Responses:  wires,
		HasMore:    list.HasMore,
		NextCursor: list.NextCursor,
	}
	if !expiresAt.IsZero() {
		resp.ExpiresAt = &expiresAt
	}
	writeResponse(w, http.StatusOK, resp)
}

// DeleteRipple handles DELETE /api/ripples/{rippleID}.
func (h *Handlers) DeleteRipple(w http.ResponseWriter, r *http.Request) {
	callerID := h.getUserID(r)

	rippleID := mux.Vars(r)["rippleID"]
	if rippleID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `rippleID` is required")
		return
	}

	found, owned, err := h.services.db.SoftDeleteRipple(r.Context(), rippleID, callerID)
	if err != nil {
		internalServerError(w)
		return
	}
	if !found {
		writeResponse(w, http.StatusNotFound, "Comment not found")
		return
	}
	if !owned {
		writeResponse(w, http.StatusForbidden, "You can only delete your own comments.")
		return
	}

	w.WriteHeader(http.StatusNoContent)

	tombstoned, err := h.services.db.GetRipple(r.Context(), rippleID)
	if err != nil {
		h.services.log.GetLogger(r.Context()).Error().Str("rippleID", rippleID).Err(err).
			Msg("Failed to load tombstoned ripple for RIPPLE_UPDATED broadcast")
		return
	}
	wire := rippleWire(tombstoned)
	_, _, bareReedID, ok := identity.ParseKeyFingerprint(identity.IdentityID(tombstoned.ReedID))
	if !ok {
		h.services.log.GetLogger(r.Context()).Error().Str("reedID", tombstoned.ReedID).Msg("Malformed reed id on tombstoned ripple")
		return
	}
	h.broadcastChan <- realtime.BroadcastMessage{
		Type:   realtime.RippleUpdated,
		UserID: tombstoned.ReedAuthorID,
		ReedID: bareReedID,
		Ripple: realtimeRippleWire(&wire),
	}
}
