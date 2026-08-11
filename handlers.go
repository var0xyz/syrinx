//go:build !ops

package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"syrinx/coverage"
	"syrinx/crypto"
	"syrinx/deletion"
	"syrinx/identity"
	"syrinx/invites"
	"syrinx/observability/metrics"
	"syrinx/realtime"
	"syrinx/recovery"
	"syrinx/roles"

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
		Armor:       base64.StdEncoding.EncodeToString([]byte(sigArmor)),
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
	signingKey    Key
	metrics       metrics.Recorder
	// filterPipeTags keeps only tags with current pipe listeners (SignReed stash).
	// Nil means stash all extracted tags (tests / no realtime).
	filterPipeTags func([]string) []string
	kickUserWS     func(userID string)
}

type ServerInfo struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	RecoveryMode      bool   `json:"recoveryMode"`
	SignupMode        string `json:"signupMode"`
	MaxInvitesPerUser int    `json:"maxInvitesPerUser"` // -1 = infinite
}

// ///////////// //
//   Utilities  //
// ///////////// //

func NewHandlers(services *Services, cfg AppConfig, broadcastChan chan<- realtime.BroadcastMessage, signingKey Key) *Handlers {
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
	})
}

// GetServerPublicKey returns the armored public half of a server signing
// key by fingerprint. Clients use this to select the historical key that
// produced a countersignature (keys, reeds, identity records).
func (h *Handlers) GetServerPublicKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	fingerprint := mux.Vars(r)["fingerprint"]
	if fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `fingerprint` is required")
		return
	}

	armor, err := h.services.db.GetServerPublicKeyByFingerprint(r.Context(), fingerprint)
	if err != nil {
		log.Error().Str("fingerprint", fingerprint).Err(err).Msg("Error loading server public key")
		internalServerError(w)
		return
	}
	if armor == "" {
		writeResponse(w, http.StatusNotFound, "Server public key not found")
		return
	}

	writeResponse(w, http.StatusOK, map[string]string{
		"fingerprint": fingerprint,
		"armor":       armor,
	})
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

	publicKey := values.Get("publicKey")
	if publicKey == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `publicKey` is required")
		return
	}

	signature := values.Get("signature")
	if signature == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `signature` is required")
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
	userIDSigArmor, err := base64.StdEncoding.DecodeString(userIDSigB64)
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
	inviteCreatorID := strings.TrimSpace(values.Get("inviteCreatorID"))
	inviteSecret := strings.TrimSpace(values.Get("inviteSecret"))
	invite, err := h.services.db.GetPendingInvite(r.Context(), inviteCreatorID, inviteID, inviteSecret)
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
	if err := h.services.crypto.VerifySignature(userID, string(userIDSigArmor), serverPubKey); err != nil {
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
	// canonical fingerprint / creation time / expiry from the armored
	// key. This must happen before we can build the user identity
	// payload, since the payload binds the fingerprint.
	key, err := h.services.crypto.ValidateAndExtractPublicKey(publicKey, signature)
	if err != nil {
		log.Error().Err(err).Msg("Error validating public key")
		writeResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	// Reconstruct the exact bytes the client claims to have signed. At
	// signup bio is empty — a user cannot set it before their account
	// exists.
	userPayload := identity.BuildUserIdentityPayload(username, key.Fingerprint, "")

	// userSignature travels as base64(armored PGP). Decode once and hand
	// the armor to VerifySignature.
	userSigArmor, err := base64.StdEncoding.DecodeString(userSignatureB64)
	if err != nil {
		log.Error().Err(err).Msg("Invalid userSignature encoding")
		writeResponse(w, http.StatusBadRequest, "Invalid userSignature encoding")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), string(userSigArmor), publicKey); err != nil {
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
	signupRole := roles.SignupRole(userID, inviteGrantedRole, hasInvite)

	profilePayload := identity.BuildNewProfilePayload(
		userID,
		username,
		key.Fingerprint,
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
		userID,
		key.Fingerprint,
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
		Fingerprint:        key.Fingerprint,
		KeyCreatedAt:       key.CreatedAt,
		KeyExpiresAt:       key.ExpiresAt,
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
		Str("fingerprint", key.Fingerprint).
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
		"signature":   base64.StdEncoding.EncodeToString([]byte(sig)),
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
	inviteCreatorID := strings.TrimSpace(values.Get("inviteCreatorID"))
	inviteSecret := strings.TrimSpace(values.Get("inviteSecret"))
	invite, err := h.services.db.GetPendingInvite(r.Context(), inviteCreatorID, inviteID, inviteSecret)
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

// SearchUsers handles GET /users/search?q=&limit= — the composer @-mention
// picker's backing search. Auth required (not in signatureAuthMiddleware's
// excludePaths); minimal fields only, no keys.
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

	results, err := h.services.db.SearchUsers(r.Context(), query, limit)
	if err != nil {
		log.Error().Str("query", query).Err(err).Msg("Error searching users")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, map[string]any{"users": results})
}

func (h *Handlers) FollowUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	followerID := h.getUserID(r)
	userID := mux.Vars(r)["userID"]

	if followerID == userID {
		writeResponse(w, http.StatusBadRequest, "Cannot follow yourself")
		return
	}

	if err := h.services.db.FollowUser(r.Context(), followerID, userID); err != nil {
		log.Error().Str("followerID", followerID).Str("userID", userID).Err(err).Msg("Error following user")
		internalServerError(w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) UnfollowUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	followerID := h.getUserID(r)
	userID := mux.Vars(r)["userID"]

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
	userSigArmor, err := base64.StdEncoding.DecodeString(userSignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(r.Context(), userID, fingerprint)
	if err != nil {
		log.Error().Str("userID", userID).Str("fingerprint", fingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), string(userSigArmor), pubKey.Armor); err != nil {
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
				UserID: t.AuthorID,
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
				UserID: t.AuthorID,
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

	userSigArmor, err := base64.StdEncoding.DecodeString(userSignatureB64)
	if err != nil {
		log.Error().Err(err).Msg("Invalid userSignature encoding")
		writeResponse(w, http.StatusBadRequest, "Invalid userSignature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(r.Context(), userID, fingerprint)
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
	if err := h.services.crypto.VerifySignature(string(userPayload), string(userSigArmor), pubKey.Armor); err != nil {
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

	revokedKeySignature := strings.TrimSpace(r.FormValue("revokedKeySignature"))
	if revokedKeySignature == "" {
		log.Error().
			Str("userID", userID).
			Msg("Argument `revokedKeySignature` not found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `revokedKeySignature` is required")
		return
	}

	newKeySignature := strings.TrimSpace(r.FormValue("newKeySignature"))
	if newKeySignature == "" {
		log.Error().
			Str("userID", userID).
			Msg("Argument `newKeySignature` not found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `newKeySignature` is required")
		return
	}

	armoredPublicKey := strings.TrimSpace(r.FormValue("publicKey"))
	if armoredPublicKey == "" {
		log.Error().Str("userID", userID).Msg("No public key found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `publicKey` is required")
		return
	}

	revokedKeyFingerprint := strings.TrimSpace(r.FormValue("revokedKeyFingerprint"))
	if revokedKeyFingerprint == "" {
		log.Error().Str("userID", userID).Msg("Argument `revokedKeyFingerprint` not found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `revokedKeyFingerprint` is required")
		return
	}

	// Retrieve old key — needed for cryptographic verification of the
	// rotation proof below. DB integrity of the rotation itself (revoked,
	// no successor yet, no other active key, …) is enforced inside
	// DataService.AddPublicKey.
	revokedKey, err := h.services.db.GetPublicKey(r.Context(), userID, revokedKeyFingerprint)
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
	err = h.services.crypto.VerifySignedChallenge(revokedKeySignature, revokedKey.Armor, armoredPublicKey)
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
	newKey, err := h.services.crypto.ValidateAndExtractPublicKey(armoredPublicKey, newKeySignature)
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

	now := time.Now().UTC().Truncate(time.Second)
	keyPayload := identity.BuildPublicKeyPayload(
		h.services.db.GetServerID(),
		userID,
		newKey.Fingerprint,
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
		Fingerprint: newKey.Fingerprint,
		UserID:      userID,
		CreatedAt:   newKey.CreatedAt,
		ExpiresAt:   newKey.ExpiresAt,
		Armor:       armoredPublicKey,
		Server:      keySignature,

		PredecessorFingerprint: revokedKeyFingerprint,
		PredecessorSignature:   revokedKeySignature,
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
		Str("fingerprint", newKey.Fingerprint).
		Msg("Public key created")

	writeResponse(w, http.StatusOK, publicKey)
}
func (h *Handlers) GetPublicKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetPublicKey request received")

	fingerprint := mux.Vars(r)["fingerprint"]
	if fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `fingerprint` is required")
		return
	}

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	key, err := h.services.db.GetPublicKey(r.Context(), userID, fingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if key == nil {
		writeResponse(w, http.StatusNotFound, "Key not found")
		return
	}

	writeResponse(w, http.StatusOK, key)
}

func (h *Handlers) RevokeKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("RevokeKey request received")

	userID := h.getUserID(r)
	fingerprint := mux.Vars(r)["fingerprint"]
	if fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `fingerprint` is required")
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

	pubKey, err := h.services.db.GetPublicKey(r.Context(), userID, fingerprint)
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
	userSigArmor, err := base64.StdEncoding.DecodeString(userSignatureB64)
	if err != nil {
		log.Error().Err(err).Msg("Invalid userSignature encoding")
		writeResponse(w, http.StatusBadRequest, "Invalid userSignature encoding")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), string(userSigArmor), pubKey.Armor); err != nil {
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
		Fingerprint:      fingerprint,
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

	key, err := h.services.db.GetPublicKey(r.Context(), userID, fingerprint)
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

	writeResponse(w, http.StatusOK, key)
}

func (h *Handlers) GetKeyRevocation(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetKeyRevocation request received")

	userID := mux.Vars(r)["userID"]
	fingerprint := mux.Vars(r)["fingerprint"]
	if userID == "" || fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `fingerprint` are required")
		return
	}

	revocation, err := h.services.db.GetKeyRevocation(r.Context(), userID, fingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
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
		exists, err := h.services.db.ReedExists(r.Context(), ref.AuthorID, ref.ReedID)
		if err != nil {
			log.Error().Err(err).Msg("Error checking echo target")
			internalServerError(w)
			return
		}
		if !exists {
			writeResponse(w, http.StatusBadRequest, "Target reed not found")
			return
		}
		echoRef = &ref
	}
	if replying != "" {
		ref, ok := h.parseReedRef(replying, localServerID)
		if !ok {
			writeResponse(w, http.StatusBadRequest, "Invalid replying reference")
			return
		}
		exists, err := h.services.db.ReedExists(r.Context(), ref.AuthorID, ref.ReedID)
		if err != nil {
			log.Error().Err(err).Msg("Error checking reply target")
			internalServerError(w)
			return
		}
		if !exists {
			writeResponse(w, http.StatusBadRequest, "Target reed not found")
			return
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

	// Only local mentions are indexed — reed_mentions.mentioned_user_id is a
	// hard FK to users(id), so a mention of a user on a foreign server has
	// nothing to reference and is never stored. The content itself still
	// carries the ~userID@serverID token either way; this only affects what
	// gets recorded server-side (and, later, who gets notified).
	allMentions := ExtractMentions(contentBody, userID)
	localMentions := make([]ReedRef, 0, len(allMentions))
	for _, m := range allMentions {
		if m.ServerID != localServerID {
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
		localMentions = append(localMentions, m)
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
	userSigArmor, err := base64.StdEncoding.DecodeString(userSignature)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(r.Context(), userID, userFingerprint)
	if err != nil {
		log.Error().Str("userID", userID).Str("userFingerprint", userFingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.VerifySignature(markdown, string(userSigArmor), pubKey.Armor); err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Err(err).
			Msg("reed signature verification failed")
		writeResponse(w, http.StatusBadRequest, "signature verification failed")
		return
	}

	existing, err := h.services.db.GetReedAttestation(r.Context(), userID, reedID)
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
		userID,
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
		reed, echoIndexed, err = h.services.db.CreateReedWithEcho(r.Context(), createParams, *echoRef)
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
			existing, getErr := h.services.db.GetReedAttestation(r.Context(), userID, reedID)
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
		h.broadcastChan <- realtime.BroadcastMessage{
			Type:   realtime.EchoCountChanged,
			UserID: echoRef.AuthorID,
			ReedID: echoRef.ReedID,
		}
	}

	if replyRef != nil {
		targets, err := h.services.db.ReplyCountNotifyTargets(r.Context(), replyRef.AuthorID, replyRef.ReedID)
		if err != nil {
			log.Error().Err(err).Msg("Error resolving reply count notify targets")
		} else {
			for _, t := range targets {
				h.broadcastChan <- realtime.BroadcastMessage{
					Type:   realtime.ReplyCountChanged,
					UserID: t.AuthorID,
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
	// Targets on other instances are not supported yet.
	if ref.ServerID != localServerID {
		return ReedRef{}, false
	}
	return ref, true
}

func (h *Handlers) DeleteReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("DeleteReed request received")

	pathUserID := mux.Vars(r)["userID"]
	reedID := mux.Vars(r)["reedID"]
	if pathUserID == "" || reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}

	userID := h.getUserID(r)
	if userID != pathUserID {
		writeResponse(w, http.StatusForbidden, "You can only delete your own reeds")
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

	serverID := h.services.db.GetServerID()

	existing, err := h.services.db.GetReedRemoval(r.Context(), userID, reedID)
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

	reed, err := h.services.db.GetReed(r.Context(), userID, reedID)
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
	userPayload := identity.BuildReedRemovalUserPayload(serverID, userID, reedID)
	userSigArmor, err := base64.StdEncoding.DecodeString(userSignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(r.Context(), userID, fingerprint)
	if err != nil {
		log.Error().Str("userID", userID).Str("fingerprint", fingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), string(userSigArmor), pubKey.Armor); err != nil {
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
		serverID, userID, reedID,
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
			existing, getErr := h.services.db.GetReedRemoval(r.Context(), userID, reedID)
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

	if err := h.services.db.DeleteMentionsForReed(r.Context(), userID, reedID); err != nil {
		log.Error().Str("reedID", reedID).Err(err).Msg("Error clearing mention index for removed reed")
	}

	affectedTargets, err := h.services.db.DeleteEchoIndexForReed(r.Context(), userID, reedID)
	if err != nil {
		log.Error().Str("reedID", reedID).Err(err).Msg("Error clearing echo index for removed reed")
	} else {
		for _, t := range affectedTargets {
			h.broadcastChan <- realtime.BroadcastMessage{
				Type:   realtime.EchoCountChanged,
				UserID: t.AuthorID,
				ReedID: t.ReedID,
			}
		}
	}

	replyTargets, err := h.services.db.ReplyCountNotifyTargetsForRemovedReply(r.Context(), userID, reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error resolving reply count targets for removed reed")
	} else {
		for _, t := range replyTargets {
			h.broadcastChan <- realtime.BroadcastMessage{
				Type:   realtime.ReplyCountChanged,
				UserID: t.AuthorID,
				ReedID: t.ReedID,
			}
		}
	}

	// Keep the reeds row for allocation catch-up (04): reed_allocations FK
	// cascades on reed delete. Tip/list already exclude reed_removals.
	wire := realtime.NewReedRemovalWire(serverID, &cert)
	h.broadcastChan <- realtime.BroadcastMessage{
		Type:        realtime.ReedRemoved,
		ServerID:    serverID,
		UserID:      userID,
		ReedID:      reedID,
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
	reedID := mux.Vars(r)["reedID"]
	if authorID == "" || reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}

	likerID := h.getUserID(r)

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
	fingerprint := strings.TrimSpace(values.Get("fingerprint"))
	if fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `fingerprint` is required")
		return
	}

	serverID := h.services.db.GetServerID()

	existing, err := h.services.db.GetReedLike(r.Context(), likerID, authorID, reedID)
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

	reed, err := h.services.db.GetReed(r.Context(), authorID, reedID)
	if err != nil {
		log.Error().Str("authorID", authorID).Str("reedID", reedID).Err(err).Msg("Error getting reed")
		internalServerError(w)
		return
	}
	if reed == nil {
		writeResponse(w, http.StatusNotFound, "Reed not found")
		return
	}

	userPayload := identity.BuildReedLikeUserPayload(serverID, authorID, reedID, fingerprint)
	userSigArmor, err := base64.StdEncoding.DecodeString(userSignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(r.Context(), likerID, fingerprint)
	if err != nil {
		log.Error().Str("likerID", likerID).Str("fingerprint", fingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeResponse(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.VerifySignature(string(userPayload), string(userSigArmor), pubKey.Armor); err != nil {
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
		serverID, authorID, reedID,
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
			existing, getErr := h.services.db.GetReedLike(r.Context(), likerID, authorID, reedID)
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
		ReedID: reedID,
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
	reedID := mux.Vars(r)["reedID"]
	if authorID == "" || reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}

	likerID := h.getUserID(r)

	deleted, err := h.services.db.DeleteReedLike(r.Context(), likerID, authorID, reedID)
	if err != nil {
		log.Error().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Err(err).Msg("Error deleting reed like")
		internalServerError(w)
		return
	}

	if deleted {
		h.broadcastChan <- realtime.BroadcastMessage{
			Type:   realtime.LikeCountChanged,
			UserID: authorID,
			ReedID: reedID,
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

	reedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}

	result, err := h.services.db.GetReedOrRemovalCert(r.Context(), userID, reedID)
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

func (h *Handlers) GetReedEchoes(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReedEchoes request received")

	reedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}

	result, err := h.services.db.GetReedOrRemovalCert(r.Context(), userID, reedID)
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

	count, err := h.services.db.CountEchoes(r.Context(), userID, reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error counting echoes")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, count)
}

func (h *Handlers) GetReedReplies(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReedReplies request received")

	reedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}

	result, err := h.services.db.GetReedOrRemovalCert(r.Context(), userID, reedID)
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

	list, err := h.services.db.ListReplies(r.Context(), userID, reedID, limit, before)
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

	key, err := h.services.db.GetPublicKey(r.Context(), req.UserID, req.Fingerprint)
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

func (h *Handlers) federationSignServer(message []byte) (string, error) {
	sigArmor, err := h.services.crypto.Sign(string(message), h.signingKey.Armor)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(sigArmor)), nil
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
	remoteArmor := strings.TrimSpace(req.RemotePublicKeyArmor)
	if remoteArmor == "" {
		writeResponse(w, http.StatusBadRequest, "remotePublicKeyArmor is required")
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

	baseURL := federationBaseURL(r)
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
	if err := h.services.db.InsertFederationInvitation(r.Context(), inviteID, name, caller, remoteFingerprint, secretHash, connectionString, now); err != nil {
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
		item := federationListItemWire{
			InviteID:          row.ID,
			Name:              row.Name,
			Status:            row.Status,
			CreatedBy:         row.CreatedBy,
			CreatedByUsername: row.CreatedByUsername,
			RemoteFingerprint: row.RemoteFingerprint,
			CreatedAt:         row.CreatedAt.UTC().Format(time.RFC3339),
		}
		if row.AcceptedAt != nil {
			s := row.AcceptedAt.UTC().Format(time.RFC3339)
			item.AcceptedAt = &s
		}
		if row.ApprovedAt != nil {
			s := row.ApprovedAt.UTC().Format(time.RFC3339)
			item.ApprovedAt = &s
		}
		if row.ReviewedBy != "" {
			rb := row.ReviewedBy
			item.ReviewedBy = &rb
			ru := row.ReviewedByUsername
			item.ReviewedByUsername = &ru
		}
		if row.ReviewedAt != nil {
			s := row.ReviewedAt.UTC().Format(time.RFC3339)
			item.ReviewedAt = &s
		}
		if row.Status == federationStatusNew && row.ConnectionCiphertext != "" {
			cs := row.ConnectionCiphertext
			item.ConnectionString = &cs
		}
		out = append(out, item)
	}
	writeResponse(w, http.StatusOK, out)
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
		writeResponse(w, http.StatusOK, map[string]string{"inviteId": id, "status": federationStatusRevoked})
	}
}
