//go:build !ops && !ripplescleanup && !challengescleanup

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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"syrinx/observability/metrics"
	pb "syrinx/proto"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
)

// countersign signs payload with the active server key and returns a
// ServerSignature. Used for keys, reeds, identity, etc.
func (h *Handlers) countersign(payload []byte, ts time.Time) (ServerSignature, error) {
	sigArmor, err := h.services.crypto.sign(string(payload), h.signingKey.Armor)
	if err != nil {
		return ServerSignature{}, err
	}
	return ServerSignature{
		ID:       string(canonicalID(h.services.db.GetServerID(), h.signingKey.Fingerprint)),
		Armor:    sigArmor,
		SignedAt: ts,
	}, nil
}

// /////////// //
//   Structs   //
// /////////// //

type Handlers struct {
	services      *Services
	cfg           AppConfig
	broadcastChan chan<- realtimeBroadcastMessage
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
	// approvalNotifierOverride lets tests skip calling a peer on approval.
	approvalNotifierOverride func(ctx context.Context, serverID, baseURL string) (bool, error)
	// realtimeRelay backs the cross-server REQUEST_REED relay's HTTP
	// endpoints (federation_relay.go) — they need to touch pending_events/
	// reed_allocations/the WS connection registry, which only exists on
	// the realtime service.
	realtimeRelay *realtimeService
	// peerKeyUpdateAttempts remembers when each unknown peer key was last chased
	// (see updatePeerKey).
	peerKeyUpdateAttempts sync.Map
}

// FederatedServerInfo is one established peer as /server/info lists it.
// FrontendURL is where its users open links, as agreed in the handshake.
type FederatedServerInfo struct {
	ID          string
	Name        string
	KeyID       string
	CreatedAt   time.Time
	FrontendURL string
}

// ///////////// //
//   Utilities  //
// ///////////// //

func NewHandlers(services *Services, cfg AppConfig, broadcastChan chan<- realtimeBroadcastMessage, signingKey ServerSigningKey) *Handlers {
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

// SetRealtimeRelay installs the realtimeService the cross-server
// REQUEST_REED relay endpoints (federation_relay.go) call into.
func (h *Handlers) SetRealtimeRelay(rs *realtimeService) {
	h.realtimeRelay = rs
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
	// The SPA can't boot without this endpoint, so a failed peer lookup
	// degrades to an empty list rather than an error.
	federation, err := h.services.db.ListFederatedServers(r.Context())
	if err != nil {
		h.services.log.GetLogger(r.Context()).Error().Err(err).Msg("Failed to list federated servers for server info")
		federation = []FederatedServerInfo{}
	}
	info := &pb.ServerInfo{
		Id:                h.services.db.GetServerID(),
		Name:              h.cfg.ServerName,
		RecoveryMode:      h.cfg.RecoveryMode,
		SignupMode:        h.cfg.SignupMode,
		MaxInvitesPerUser: int32(h.cfg.MaxInvitesPerUser),
		ServerKeyId:       string(canonicalID(h.services.db.GetServerID(), h.signingKey.Fingerprint)),
	}
	for _, srv := range federation {
		info.Federation = append(info.Federation, &pb.FederatedServerInfo{
			Id:          srv.ID,
			Name:        srv.Name,
			KeyId:       srv.KeyID,
			CreatedAt:   unixOrZero(srv.CreatedAt),
			FrontendUrl: srv.FrontendURL,
		})
	}
	writeResponse(w, http.StatusOK, info)
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
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	if h.proxyKeyToForeign(w, r, id) {
		return
	}

	key, err := h.services.db.GetPublicKey(r.Context(), id)
	if err != nil {
		log.Error().Str("id", id).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if key == nil {
		writeError(w, http.StatusNotFound, "Key not found")
		return
	}

	h.allocateServedKey(r, key)
	writeResponse(w, http.StatusOK, pbKey(key))
}

// allocateServedKey records who now has key cached, so they are owed its
// revocation: a local user, or a peer fetching it for its users. A key
// already revoked isn't allocated: its response says so.
func (h *Handlers) allocateServedKey(r *http.Request, key *Key) {
	if key.UserID == "" || key.Revoked {
		return
	}
	log := h.services.log.GetLogger(r.Context())
	if requester, _ := r.Context().Value(userIDKey).(string); requester != "" && requester != key.UserID {
		if err := h.services.db.AllocatePublicKey(r.Context(), requester, key.ID); err != nil {
			log.Error().Str("keyID", key.ID).Err(err).Msg("Error allocating public key")
		}
	}
	if peerID, _ := r.Context().Value(peerServerIDKey).(string); peerID != "" {
		if err := h.services.db.AllocatePublicKeyToServer(r.Context(), peerID, key.ID); err != nil {
			log.Error().Str("keyID", key.ID).Err(err).Msg("Error allocating public key to peer")
		}
	}
}

// proxyKeyToForeign forwards GET /keys/{id} for a foreign key to its home
// server, and allocates the key to the local requester when it comes back
// unrevoked. It returns false, doing nothing, for a local key.
func (h *Handlers) proxyKeyToForeign(w http.ResponseWriter, r *http.Request, id string) bool {
	homeServerID, foreign := h.foreignServerOf(id)
	if !foreign {
		return false
	}
	peer, err := h.services.db.GetServerByID(r.Context(), homeServerID)
	if err != nil {
		internalServerError(w)
		return true
	}
	if peer == nil {
		writeError(w, http.StatusNotFound, "Not found")
		return true
	}
	log := h.services.log.GetLogger(r.Context())
	respBody, status, err := h.forwardToPeer(r, peer.BaseURL, "")
	if err != nil {
		log.Error().Err(err).Str("target", peer.BaseURL).Msg("proxy key to peer server failed")
		writeError(w, http.StatusBadGateway, "Failed to reach peer server")
		return true
	}
	// A user key is cached only once verified, so it can be allocated.
	if status == http.StatusOK {
		key, err := decodePeerKey(respBody)
		if err != nil || key.ID != id {
			writeError(w, http.StatusBadGateway, "Peer server returned an invalid key")
			return true
		}
		if key.UserID != "" {
			if _, err := h.verifyAndCachePeerUserKey(r.Context(), peer.BaseURL, homeServerID, key); err != nil {
				log.Error().Err(err).Str("keyID", id).Msg("Peer user key failed verification")
				writeError(w, http.StatusBadGateway, "Peer server returned an invalid key")
				return true
			}
			h.allocateServedKey(r, &key)
		}
	}
	w.Header().Set("Content-Type", protobufContentType)
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
	return true
}

func (h *Handlers) Signup(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("Signup request received")

	if h.cfg.RecoveryMode {
		writeError(w, http.StatusForbidden, "Signups are closed while this server is in recovery mode")
		return
	}

	if inviteSignupMode(h.cfg.SignupMode) == signupModeClosed {
		writeError(w, http.StatusForbidden, "Signups are closed on this server")
		return
	}

	req := &pb.SignupRequest{}
	if err := readRequest(r, req); err != nil {
		log.Error().Err(err).Msg("Error parsing form data")
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	username := trimInvisibleChars(req.GetUsername())
	if username == "" {
		writeError(w, http.StatusBadRequest, "Argument `username` is required")
		return
	}
	if len(username) > 32 {
		writeError(w, http.StatusBadRequest, "Username cannot exceed 32 characters")
		return
	}

	deviceID, err := parseDeviceID(r.Header.Get("X-Syrinx-Device-Id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Missing or invalid X-Syrinx-Device-Id header")
		return
	}

	publicKey := req.GetPublicKey()
	if publicKey == "" {
		writeError(w, http.StatusBadRequest, "Argument `publicKey` is required")
		return
	}

	signature := req.GetSignature()
	if signature == "" {
		writeError(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}
	signatureArmor := signature

	userSignature := req.GetUserSignature()
	if userSignature == "" {
		writeError(w, http.StatusBadRequest, "Argument `userSignature` is required")
		return
	}

	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		writeError(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}
	if userID == rootUserID {
		writeError(w, http.StatusBadRequest, "userID is reserved")
		return
	}
	if !isValidCryptoID(userID) {
		writeError(w, http.StatusBadRequest, "Invalid userID")
		return
	}

	userIDSig := req.GetUserIdSignature()
	if userIDSig == "" {
		writeError(w, http.StatusBadRequest, "Argument `userIDSignature` is required")
		return
	}
	userIDSigArmor := userIDSig

	userIDFingerprint := strings.TrimSpace(req.GetUserIdFingerprint())
	if userIDFingerprint == "" {
		writeError(w, http.StatusBadRequest, "Argument `userIDFingerprint` is required")
		return
	}

	inviteID := strings.TrimSpace(req.GetInviteId())
	inviteSecret := strings.TrimSpace(req.GetInviteSecret())
	invite, err := h.services.db.GetPendingInvite(r.Context(), inviteID, inviteSecret)
	if err != nil {
		log.Error().Err(err).Msg("Failed to look up invite")
		internalServerError(w)
		return
	}
	resolved, err := resolveSignup(
		inviteSignupMode(h.cfg.SignupMode),
		inviteID,
		inviteSecret,
		invite,
	)
	if err != nil {
		if errors.Is(err, errInviteRequired) {
			writeError(w, http.StatusForbidden, "Invite required")
			return
		}
		if errors.Is(err, errInvalidInvite) {
			writeError(w, http.StatusForbidden, "Invalid or claimed invite")
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
	if err := h.services.crypto.verifySignature(userID, userIDSigArmor, serverPubKey); err != nil {
		log.Error().Err(err).Msg("userID signature verification failed")
		writeError(w, http.StatusBadRequest, "userID signature verification failed")
		return
	}

	exists, err := h.services.db.UsernameExists(r.Context(), username, "")
	if err != nil {
		log.Error().
			Str("username", username).
			Err(err).Msg("Failed to check username")
		internalServerError(w)
		return
	}
	if exists {
		writeError(w, http.StatusBadRequest, "Username already exists")
		return
	}

	// Validate the self-signature over the public key AND extract the
	// bare fingerprint / creation time / expiry from the armored key.
	// This must happen before we can build the user identity payload,
	// since the payload binds the fingerprint.
	key, err := h.services.crypto.validateAndExtractPublicKey(publicKey, signatureArmor)
	if err != nil {
		log.Error().Err(err).Msg("Error validating public key")
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// GetUserProfile/GetPublicKey return this user in userID@serverID form,
	// so the signed payloads must sign that same form or client-side
	// verification will rebuild different bytes than what was signed.
	// Computed up front (before verifying userSignature) because the
	// canonical key fingerprint the client signed over is built from it.
	selfIdentity := canonicalID(h.services.db.GetServerID(), userID)
	canonicalFingerprint := appendEntity(selfIdentity, key.Fingerprint)

	// Reconstruct the exact bytes the client claims to have signed. At
	// signup bio is empty — a user cannot set it before their account
	// exists.
	userPayload := buildUserIdentityPayload(username, string(canonicalFingerprint), "")

	if err := h.services.crypto.verifySignature(string(userPayload), userSignature, publicKey); err != nil {
		log.Error().Err(err).Msg("userSignature verification failed")
		writeError(w, http.StatusBadRequest, "userSignature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)

	inviteGrantedRole := ""
	hasInvite := invite != nil && resolved.InviteID != ""
	if hasInvite {
		inviteGrantedRole = invite.GrantedRole
	}
	signupRole := signupRole(userID, inviteGrantedRole, hasInvite, h.services.db.GetServerID())

	profilePayload := buildNewProfilePayload(
		string(selfIdentity),
		username,
		string(canonicalFingerprint),
		h.services.db.GetServerID(),
		h.signingKey.Fingerprint,
		userSignature,
		resolved.InviteID,
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
	keyPayload := buildPublicKeyPayload(
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
		UserSignature:   userSignature,
		MemberSince:        now,
		ProfileSignature:   profileSignature,
		PublicKeySignature: keySignature,
		Invite:             invite,
		DeviceID:           deviceID,
	})
	if err != nil {
		if errors.Is(err, errInvalidInvite) {
			writeError(w, http.StatusForbidden, "Invalid or claimed invite")
			return
		}
		// Username race → 400. A userID collision (or anything else) is a
		// 500; the client retries signup with a fresh random ID.
		if errors.Is(err, ErrUsernameTaken) {
			log.Info().Str("username", username).Msg("Username already exists")
			writeError(w, http.StatusBadRequest, "Username already exists")
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

	writeResponse(w, http.StatusCreated, pbUser(user))
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

	sig, err := h.services.crypto.sign(userID, h.signingKey.Armor)
	if err != nil {
		log.Error().Err(err).Msg("Error signing userID")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, &pb.UserIDResponse{
		UserId:      userID,
		Signature:   sig,
		Fingerprint: h.signingKey.Fingerprint,
	})
}

func (h *Handlers) CheckUsername(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("CheckUsername request received")

	if h.cfg.RecoveryMode {
		writeError(w, http.StatusForbidden, "Signups are closed while this server is in recovery mode")
		return
	}

	if inviteSignupMode(h.cfg.SignupMode) == signupModeClosed {
		writeError(w, http.StatusForbidden, "Signups are closed on this server")
		return
	}

	values, username, ok := h.parseCheckUsernameForm(w, r, log)
	if !ok {
		return
	}

	inviteID := strings.TrimSpace(values.GetInviteId())
	inviteSecret := strings.TrimSpace(values.GetInviteSecret())
	invite, err := h.services.db.GetPendingInvite(r.Context(), inviteID, inviteSecret)
	if err != nil {
		log.Error().Err(err).Msg("Failed to look up invite")
		internalServerError(w)
		return
	}
	if _, err := resolveSignup(
		inviteSignupMode(h.cfg.SignupMode),
		inviteID,
		inviteSecret,
		invite,
	); err != nil {
		if errors.Is(err, errInviteRequired) {
			writeError(w, http.StatusForbidden, "Invite required")
			return
		}
		if errors.Is(err, errInvalidInvite) {
			writeError(w, http.StatusForbidden, "Invalid or claimed invite")
			return
		}
		log.Error().Err(err).Msg("Invite policy error")
		internalServerError(w)
		return
	}

	h.respondUsernameAvailability(w, r, log, username, "")
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

	userID := h.getUserID(r) // panics via middleware contract if unauthenticated

	_, username, ok := h.parseCheckUsernameForm(w, r, log)
	if !ok {
		return
	}

	// The caller's own name, in any capitalization, is available to them.
	h.respondUsernameAvailability(w, r, log, username, userID)
}

// parseCheckUsernameForm parses and validates the `username` form field
// shared by CheckUsername and CheckUsernameForRename, writing the
// appropriate error response and returning ok=false on failure.
func (h *Handlers) parseCheckUsernameForm(w http.ResponseWriter, r *http.Request, log *zerolog.Logger) (*pb.CheckUsernameRequest, string, bool) {
	req := &pb.CheckUsernameRequest{}
	if err := readRequest(r, req); err != nil {
		log.Error().Err(err).Msg("Error parsing request")
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return nil, "", false
	}

	username := trimInvisibleChars(req.GetUsername())
	if username == "" {
		writeError(w, http.StatusBadRequest, "Argument `username` is required")
		return nil, "", false
	}

	if len(username) > 32 {
		writeError(w, http.StatusBadRequest, "Username cannot exceed 32 characters")
		return nil, "", false
	}

	return req, username, true
}

// respondUsernameAvailability checks username availability and writes the
// shared 200/409/500 response, used by both CheckUsername and
// CheckUsernameForRename after their distinct gating logic.
func (h *Handlers) respondUsernameAvailability(w http.ResponseWriter, r *http.Request, log *zerolog.Logger, username, exceptUserID string) {
	exists, err := h.services.db.UsernameExists(r.Context(), username, exceptUserID)
	if err != nil {
		log.Error().
			Str("username", username).
			Err(err).Msg("Failed to get user by username")
		internalServerError(w)
		return
	}

	if exists {
		writeError(w, http.StatusConflict, "Username is taken")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UserStatus handles POST /api/users/status. Unauthenticated probe: client
// sends a countersigned profile; server verifies its own countersignature and
// reports claimed / unclaimed / mid-recovery state.
func (h *Handlers) UserStatus(w http.ResponseWriter, r *http.Request) {
	profile, err := readRecoveryProfile(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if profile.ID == "" || profile.Username == "" || profile.UserSignature.KeyID == "" {
		writeError(w, http.StatusBadRequest, "profile id, username, and userSignature.id are required")
		return
	}

	// Profile must carry this server's countersignature (wrong serverID or
	// bad/missing sig → 400).
	if err := verifyProfileServerCountersig(
		r.Context(),
		profile,
		h.services.db.GetServerID(),
		h.services.db.GetServerKeyArmorAt,
		h.services.crypto,
	); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeResponse(w, http.StatusNotFound, &pb.UserStatusResponse{Status: recoveryUserStatusUnknown})
		return
	}

	// No users row → unknown.
	signedAt, err := h.services.db.UserServerSignedAt(r.Context(), profile.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeResponse(w, http.StatusNotFound, &pb.UserStatusResponse{Status: recoveryUserStatusUnknown})
			return
		}
		internalServerError(w)
		return
	}

	// Submitted profile older than the claimed DB record → reject (would
	// fork the identity / key chain).
	submittedAt := profile.ServerSignature.Timestamp.UTC().Truncate(time.Second)
	if submittedAt.Before(signedAt) {
		writeError(w, http.StatusBadRequest, "stale profile: backup is older than the server record")
		return
	}

	// Claimed and mid-import → ongoing.
	ongoing, err := h.services.db.IsOngoing(r.Context(), profile.ID)
	if err != nil {
		internalServerError(w)
		return
	}
	if ongoing {
		writeResponse(w, http.StatusConflict, &pb.UserStatusResponse{Status: recoveryUserStatusOngoing})
		return
	}

	// Claimed, not mid-import, profile not older than DB → complete.
	writeResponse(w, http.StatusOK, &pb.UserStatusResponse{Status: recoveryUserStatusComplete})
}

func (h *Handlers) GetUserProfile(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUserProfile request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeError(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	if h.refuseIfBlocked(w, r, userID) {
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
		writeAccountGone(w, h.accountRemovalWire(removal))
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
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	writeResponse(w, http.StatusOK, pbUser(user))
}

func (h *Handlers) GetUserInfo(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUserInfo request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeError(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	if h.refuseIfBlocked(w, r, userID) {
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
		writeAccountGone(w, h.accountRemovalWire(removal))
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
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	writeResponse(w, http.StatusOK, pbUserInfo(info))
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
		writeResponse(w, http.StatusOK, &pb.UserSearchResponse{})
		return
	}

	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}

	localResults, err := h.services.db.SearchUsers(r.Context(), query, h.getUserID(r), limit)
	if err != nil {
		log.Error().Str("query", query).Err(err).Msg("Error searching users")
		internalServerError(w)
		return
	}

	foreignResults := h.fanoutUserSearchToPeers(r.Context(), query, limit, searchUsersFanoutTimeout)
	results := mergeUserSearchResults(query, localResults, foreignResults)

	blocking, err := h.services.db.UsersBlocking(r.Context(), h.getUserID(r))
	if err != nil {
		log.Error().Err(err).Msg("Error loading users blocking the searcher")
		internalServerError(w)
		return
	}
	results = slices.DeleteFunc(results, func(u UserSearchResult) bool { return blocking[u.ID] })

	writeResponse(w, http.StatusOK, pbUserSearch(results))
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
	_, embeddedServerID, ok := parseIdentityID(identityID(userID))
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
		writeError(w, http.StatusNotFound, "Not found")
		return true, 0
	}

	if err := h.forwardFollowToPeer(r.Context(), method, peer.BaseURL, userID, followerID); err != nil {
		log.Error().Str("userID", userID).Str("followerID", followerID).Err(err).Msg("Failed to forward follow to peer")
		h.logFederationServerAsync(peer.ID, "error", fmt.Sprintf("Follow forward to %s failed: %s", peer.BaseURL, err.Error()))
		writeError(w, http.StatusBadGateway, "Failed to reach peer server")
		return true, 0
	}

	w.WriteHeader(http.StatusNoContent)
	return true, http.StatusNoContent
}

// resolveFollower returns who's following: an end-user's own session, or
// a peer vouching for one of its users via the body's follower_id.
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
	var req pb.FollowRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return "", false
	}
	followerID = strings.TrimSpace(req.GetFollowerId())
	_, embeddedServerID, parseOK := parseIdentityID(identityID(followerID))
	if !parseOK || embeddedServerID != peerServerID {
		return "", false
	}
	return followerID, true
}

// resolveActingUser returns who's acting: an end-user's own session, or a
// peer vouching for one of its users via candidateID (already extracted
// from the request body by the caller, since each route's request message
// names it differently).
func (h *Handlers) resolveActingUser(r *http.Request, candidateID string) (userID string, ok bool) {
	if userID, isUser := r.Context().Value(userIDKey).(string); isUser {
		return userID, true
	}
	peerServerID, isPeer := r.Context().Value(peerServerIDKey).(string)
	if !isPeer {
		return "", false
	}
	candidateID = strings.TrimSpace(candidateID)
	_, embeddedServerID, parseOK := parseIdentityID(identityID(candidateID))
	if !parseOK || embeddedServerID != peerServerID {
		return "", false
	}
	return candidateID, true
}

func (h *Handlers) FollowUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	userID := mux.Vars(r)["userID"]
	if followerID, ok := h.resolveFollower(r); ok && h.refuseBlockedRequester(w, r, userID, followerID) {
		return
	}

	if handled, status := h.proxyFollowIfForeign(w, r, http.MethodPost, userID); handled {
		if status == http.StatusNoContent {
			followerID, _ := h.resolveFollower(r)
			h.rememberRemoteIdentityAndFollowLocally(r.Context(), log, followerID, userID)
		}
		return
	}

	followerID, ok := h.resolveFollower(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Argument `followerID` is required")
		return
	}
	if followerID == userID {
		writeError(w, http.StatusBadRequest, "Cannot follow yourself")
		return
	}

	if _, isPeer := r.Context().Value(peerServerIDKey).(string); isPeer {
		h.upsertRemoteIdentity(r.Context(), log, followerID)
		if err := h.services.db.RecordRemoteFollower(r.Context(), userID, followerID); err != nil {
			if errors.Is(err, ErrFollowTargetNotFound) {
				writeError(w, http.StatusNotFound, "User not found")
				return
			}
			log.Error().Str("followerID", followerID).Str("userID", userID).Err(err).Msg("Error recording remote follower")
			internalServerError(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.services.db.FollowUser(r.Context(), followerID, userID); err != nil {
		if errors.Is(err, ErrFollowTargetNotFound) {
			writeError(w, http.StatusNotFound, "User not found")
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
		writeError(w, http.StatusBadRequest, "Argument `followerID` is required")
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
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	if isRoot, err := h.isRoot(r.Context(), userID); err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error checking root status for account removal")
		internalServerError(w)
		return
	} else if isRoot {
		writeError(w, http.StatusForbidden, "The root account cannot be deleted")
		return
	}

	req := &pb.DeleteAccountRequest{}
	if err := readRequest(r, req); err != nil {
		log.Error().Err(err).Msg("Error parsing form")
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	userSignature := strings.TrimSpace(req.GetSignature())
	if userSignature == "" {
		writeError(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}
	note := req.GetNote()
	if err := validateAccountNote(note); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		if existing.UserSignature != userSignature || existing.Note != note {
			writeError(w, http.StatusConflict, "Account removal already exists with a different attestation")
			return
		}
		writeResponse(w, http.StatusOK, pbAccountRemoval(h.accountRemovalWire(existing)))
		return
	}

	user, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	fingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), userID)
	if err != nil || fingerprint == "" {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading active key fingerprint")
		internalServerError(w)
		return
	}
	userPayload := buildAccountRemovalUserPayload(serverID, userID, note)
	userSigArmor := userSignature
	pubKey, err := h.services.db.GetPublicKey(r.Context(), fingerprint)
	if err != nil {
		log.Error().Str("userID", userID).Str("fingerprint", fingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeError(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("account removal signature verification failed")
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	serverPayload := buildAccountRemovalServerPayload(
		serverID, userID, note,
		h.signingKey.Fingerprint, userSignature, now,
	)
	serverSignature, err := h.countersign(serverPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing account-removal countersignature")
		internalServerError(w)
		return
	}

	cert := accountRemovalCert{
		UserID:            userID,
		Note:              note,
		UserSignature:     userSignature,
		UserKeyID:         fingerprint,
		ServerSignature:   serverSignature.Armor,
		ServerFingerprint: serverSignature.ID,
		ServerSignedAt:    serverSignature.SignedAt,
	}
	if err := h.services.db.InsertAccountRemoval(r.Context(), cert); err != nil {
		if errors.Is(err, errRemovalConflict) {
			existing, getErr := h.services.db.GetAccountRemoval(r.Context(), userID)
			if getErr == nil && existing != nil && existing.UserSignature == userSignature && existing.Note == note {
				writeResponse(w, http.StatusOK, pbAccountRemoval(h.accountRemovalWire(existing)))
				return
			}
			writeError(w, http.StatusConflict, "Account removal already exists with a different attestation")
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
			h.broadcastChan <- realtimeBroadcastMessage{
				Type:   realtimeEchoCountChanged,
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
			h.broadcastChan <- realtimeBroadcastMessage{
				Type:   realtimeReplyCountChanged,
				UserID: t.CanonicalAuthorID(),
				ReedID: t.ReedID,
			}
		}
	}

	wire := newAccountRemovalWire(serverID, cert)
	h.broadcastChan <- realtimeBroadcastMessage{
		Type:           realtimeAccountRemoved,
		ServerID:       serverID,
		UserID:         userID,
		AccountRemoval: &wire,
	}

	go h.notifyForeignAccountRemovalToPeers(context.Background(), userID, cert)

	log.Info().Str("userID", userID).Msg("Account removal accepted")
	writeResponse(w, http.StatusOK, pbAccountRemoval(h.accountRemovalWire(&cert)))
}

func (h *Handlers) accountRemovalWire(cert *accountRemovalCert) AccountRemoval {
	return AccountRemoval{
		Type:     identityTypeAccount,
		ServerID: h.services.db.GetServerID(),
		UserID:   cert.UserID,
		Note:     cert.Note,
		UserSignature: UserSignature{
			ID:    cert.UserKeyID,
			Armor: cert.UserSignature,
		},
		ServerSignature: ServerSignature{
			ID:       cert.ServerFingerprint,
			Armor:    cert.ServerSignature,
			SignedAt: cert.ServerSignedAt,
		},
	}
}

// UpdateUser mints a fresh signed identity record for an authenticated
// user editing their own profile. Full-replacement semantics: the
// request MUST carry the complete post-edit tuple (username, bio) plus
// `userSignature`, an armored PGP detached signature over
// `buildUserIdentityPayload(username, fingerprint, bio)` where
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
		writeError(w, http.StatusBadRequest, "User not found")
		return
	}

	req := &pb.UpdateUserRequest{}
	if err := readRequest(r, req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	userSignature := req.GetUserSignature()
	if userSignature == "" {
		writeError(w, http.StatusBadRequest, "Argument `userSignature` is required")
		return
	}

	// No-op fast path. See doc comment on this function for why byte
	// equality on the signature is a sufficient change detector.
	if userSignature == currentUser.UserSignature.Armor {
		log.Info().
			Str("userID", userID).
			Msg("UpdateUser no-op (signature unchanged)")
		writeResponse(w, http.StatusOK, pbUser(currentUser))
		return
	}

	username := trimInvisibleChars(req.GetUsername())
	if username == "" {
		writeError(w, http.StatusBadRequest, "Argument `username` is required")
		return
	}
	if len(username) > 32 {
		writeError(w, http.StatusBadRequest, "Username cannot exceed 32 characters")
		return
	}

	if currentUser.Username != username {
		exists, err := h.services.db.UsernameExists(r.Context(), username, userID)
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
			writeError(w, http.StatusBadRequest, "Username already taken")
			return
		}
	}

	bio := req.GetBio()
	if CountMarkdownCharacters(bio) > MaxReedVisibleChars {
		log.Error().
			Str("userID", userID).
			Int("length", CountMarkdownCharacters(bio)).
			Msg("Bio cannot exceed 140 visible characters")
		writeError(w, http.StatusBadRequest, "Bio cannot exceed 140 characters")
		return
	}

	// Reconstruct the exact bytes the client claims to have signed,
	// using the fingerprint we trust (from the row) rather than one
	// supplied by the caller. Then verify.
	//
	// The client signs with the currently-active key (users.active_key_id).
	fingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), userID)
	if err != nil || fingerprint == "" {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading active key fingerprint")
		internalServerError(w)
		return
	}
	userPayload := buildUserIdentityPayload(username, fingerprint, bio)

	userSigArmor := userSignature
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
		writeError(w, http.StatusUnauthorized, "Key is revoked")
		return
	}
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("userSignature verification failed")
		writeError(w, http.StatusBadRequest, "userSignature verification failed")
		return
	}

	// Mint the server-authored fields and countersign. createdAt
	// stays pinned to the value set at signup; only signedAt advances.
	// inviteID is immutable — always re-bind the value stored on the row.
	inviteID := ""
	if currentUser.Invite != nil {
		inviteID = currentUser.Invite.ID
	}
	signedAt := time.Now().UTC().Truncate(time.Second)
	profilePayload := buildProfilePayload(
		userID,
		username,
		fingerprint,
		h.services.db.GetServerID(),
		h.signingKey.Fingerprint,
		userSignature,
		inviteID,
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
		UserSignature: userSignature,
		ProfileSignature: profileSignature,
	}); err != nil {
		// Race with a concurrent rename that took our target username
		// between the UsernameExists check above and this UPDATE.
		if errors.Is(err, ErrUsernameTaken) {
			log.Info().Str("username", username).Msg("Username already taken (race)")
			writeError(w, http.StatusBadRequest, "Username already taken")
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

	h.broadcastChan <- realtimeBroadcastMessage{
		Type:   realtimeUserUpdate,
		UserID: userID,
		UserUpdate: &userUpdateBroadcast{
			Username: updated.Username,
			Bio:      updated.Bio,
		},
	}

	log.Info().
		Str("userID", userID).
		Str("username", username).
		Msg("Signed identity record updated")

	writeResponse(w, http.StatusOK, pbUser(updated))
}

func (h *Handlers) AddPublicKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("AddPublicKey request received")

	req := &pb.AddPublicKeyRequest{}
	if err := readRequest(r, req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		log.Error().Msg("Argument `userID` is required")
		writeError(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	revokedKeySignature := strings.TrimSpace(req.GetRevokedKeySignature())
	if revokedKeySignature == "" {
		log.Error().
			Str("userID", userID).
			Msg("Argument `revokedKeySignature` not found in request")
		writeError(w, http.StatusBadRequest, "Argument `revokedKeySignature` is required")
		return
	}
	revokedKeySigArmor := revokedKeySignature

	newKeySignature := strings.TrimSpace(req.GetNewKeySignature())
	if newKeySignature == "" {
		log.Error().
			Str("userID", userID).
			Msg("Argument `newKeySignature` not found in request")
		writeError(w, http.StatusBadRequest, "Argument `newKeySignature` is required")
		return
	}
	newKeySigArmor := newKeySignature

	// Revoking the predecessor happens in the same request/transaction as
	// adding the new key — a separate revoke-then-add round trip leaves a
	// window where the caller has no valid key at all, and any request
	// signed in that window (even this server's own best-effort follow-ups)
	// is rejected.
	revocationReason := strings.TrimSpace(req.GetRevocationReason())
	revocationUserSignature := strings.TrimSpace(req.GetRevocationUserSignature())
	if revocationUserSignature == "" {
		log.Error().
			Str("userID", userID).
			Msg("Argument `revocationUserSignature` not found in request")
		writeError(w, http.StatusBadRequest, "Argument `revocationUserSignature` is required")
		return
	}
	revocationUserSigArmor := revocationUserSignature

	armoredPublicKey := strings.TrimSpace(req.GetPublicKey())
	if armoredPublicKey == "" {
		log.Error().Str("userID", userID).Msg("No public key found in request")
		writeError(w, http.StatusBadRequest, "Argument `publicKey` is required")
		return
	}

	// revokedKeyFingerprint travels bare over the wire (form field); join
	// it with userID (already canonical) to get the DB/lookup key.
	revokedKeyFingerprintBare := strings.TrimSpace(req.GetRevokedKeyFingerprint())
	if revokedKeyFingerprintBare == "" {
		log.Error().Str("userID", userID).Msg("Argument `revokedKeyFingerprint` not found in request")
		writeError(w, http.StatusBadRequest, "Argument `revokedKeyFingerprint` is required")
		return
	}
	revokedKeyFingerprint := string(appendEntity(identityID(userID), revokedKeyFingerprintBare))

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
		writeError(w, http.StatusNotFound, "Old public key not found")
		return
	}

	// Verify revoked key signature against old key
	err = h.services.crypto.verifySignedChallenge(revokedKeySigArmor, revokedKey.Armor, armoredPublicKey)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("revokedKeyFingerprint", revokedKeyFingerprint).
			Err(err).Msg("Revoked key signature verification failed")
		writeError(w, http.StatusUnauthorized, "Revoked key signature verification failed")
		return
	}

	log.Info().
		Str("userID", userID).
		Str("revokedKeyFingerprint", revokedKeyFingerprint).
		Msg("Revoked key signature verified successfully")

	// Verify the revocation attestation itself, same check RevokeKey used
	// to do standalone — the old key signing off on its own revocation.
	revocationPayload := buildUserRevocationPayload(userID, revokedKeyFingerprint, revocationReason)
	if err := h.services.crypto.verifySignature(string(revocationPayload), revocationUserSigArmor, revokedKey.Armor); err != nil {
		log.Error().
			Str("userID", userID).
			Str("revokedKeyFingerprint", revokedKeyFingerprint).
			Err(err).Msg("revocationUserSignature verification failed")
		writeError(w, http.StatusUnauthorized, "revocationUserSignature verification failed")
		return
	}

	// Validate and verify the public newKey using crypto service
	newKey, err := h.services.crypto.validateAndExtractPublicKey(armoredPublicKey, newKeySigArmor)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error validating public key")
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Info().
		Str("userID", userID).
		Str("fingerprint", newKey.Fingerprint).
		Msg("Public key signature verified successfully")

	newKeyFingerprint := string(appendEntity(identityID(userID), newKey.Fingerprint))

	now := time.Now().UTC().Truncate(time.Second)
	keyPayload := buildPublicKeyPayload(
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

	revocationServerPayload := buildServerRevocationPayload(
		userID,
		revokedKeyFingerprint,
		revocationReason,
		h.services.db.GetServerID(),
		h.signingKey.Fingerprint,
		revocationUserSignature,
		now,
	)
	revocationServerSignature, err := h.countersign(revocationServerPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error signing key revocation")
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

		RevocationReason:        revocationReason,
		RevocationUserSignature: revocationUserSignature,
		RevocationServer:        revocationServerSignature,
	})
	if err != nil {
		var tooSoon *ErrKeyRotationTooSoon
		switch {
		case errors.As(err, &tooSoon):
			log.Info().
				Str("userID", userID).
				Dur("retryAfter", tooSoon.RetryAfter).
				Msg("AddPublicKey rejected: cooldown")
			w.Header().Set("Retry-After", strconv.Itoa(int(tooSoon.RetryAfter.Seconds())+1))
			writeError(w, http.StatusTooManyRequests,
				"You can only revoke your key once every 24 hours")
		case errors.Is(err, ErrUserNotFound):
			writeError(w, http.StatusNotFound, "User not found")
		case errors.Is(err, ErrKeyAlreadyExists):
			writeError(w, http.StatusConflict, "Public key fingerprint already registered")
		case errors.Is(err, ErrPredecessorRequired),
			errors.Is(err, ErrPredecessorNotFound),
			errors.Is(err, ErrPredecessorNotRevoked),
			errors.Is(err, ErrPredecessorAlreadyReplaced),
			errors.Is(err, ErrActiveKeyExists):
			log.Error().
				Str("userID", userID).
				Str("revokedKeyFingerprint", revokedKeyFingerprint).
				Err(err).Msg("AddPublicKey rejected")
			writeError(w, http.StatusBadRequest, err.Error())
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

	h.metrics.KeyRevoked(r.Context(), userID)
	h.broadcastChan <- realtimeBroadcastMessage{Type: realtimeKeyRevoked, KeyID: revokedKeyFingerprint}
	go h.notifyPeersOfKeyRevocation(revokedKeyFingerprint)

	writeResponse(w, http.StatusOK, pbKey(publicKey))
}

func (h *Handlers) GetKeyRevocation(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetKeyRevocation request received")

	fingerprint := mux.Vars(r)["id"]
	if fingerprint == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
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
	if revocation != nil {
		writeResponse(w, http.StatusOK, &pb.KeyRevocationResponse{Revocation: &pb.KeyRevocationResponse_User{User: pbKeyRevocation(revocation)}})
		return
	}

	// Not a user key: it may be one of this server's own signing keys.
	serverRevocation, err := h.services.db.GetServerKeyRevocation(r.Context(), fingerprint)
	if err != nil {
		log.Error().Str("keyID", fingerprint).Err(err).Msg("Error fetching server key revocation")
		internalServerError(w)
		return
	}
	if serverRevocation == nil {
		writeError(w, http.StatusNotFound, "Revocation not found")
		return
	}
	writeResponse(w, http.StatusOK, &pb.KeyRevocationResponse{Revocation: &pb.KeyRevocationResponse_Server{Server: pbServerKeyRevocation(serverRevocation)}})
}

// normalizeClaimedTags lowercases, trims, and dedupes a client-claimed tag
// list (first-appearance order), dropping any tag containing whitespace —
// tag names never contain spaces. The server never sees content to check
// these claims against — receiving pipe watchers do that.
func normalizeClaimedTags(claims []string) []string {
	if len(claims) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(claims))
	out := make([]string, 0, len(claims))
	for _, c := range claims {
		tag := strings.ToLower(strings.TrimSpace(c))
		if tag == "" || strings.ContainsAny(tag, " \t\n\r") {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

// errMentionTargetNotFound rejects a claimed mention of a local user who
// doesn't exist.
var errMentionTargetNotFound = errors.New("mentioned user not found")

// resolveMentionClaims returns the mentions to store for a reed. Local ones
// must exist; foreign ones are kept when their server is a peer, for
// new-reed to carry. Either way the mentioned client re-verifies.
func (h *Handlers) resolveMentionClaims(ctx context.Context, claimed []string, userID string) ([]string, error) {
	localServerID := h.services.db.GetServerID()
	all := ValidateMentionClaims(claimed, userID)
	stored := make([]string, 0, len(all))
	var foreign []string
	for _, m := range all {
		mentionedUserID := m.CanonicalAuthorID()
		if m.ServerID != localServerID {
			foreign = append(foreign, mentionedUserID)
			continue
		}
		valid, err := h.services.db.MentionTargetValid(ctx, m.AuthorID, m.ServerID)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, errMentionTargetNotFound
		}
		stored = append(stored, mentionedUserID)
	}
	for _, mentionedUserID := range foreign {
		_, mentionedServerID, _ := parseIdentityID(identityID(mentionedUserID))
		peer, err := h.services.db.GetServerByID(ctx, mentionedServerID)
		if err != nil || peer == nil {
			continue
		}
		if err := h.services.db.UpsertRemoteIdentity(ctx, mentionedUserID, mentionedServerID); err != nil {
			h.services.log.GetLogger(ctx).Error().Str("mentionedUserID", mentionedUserID).Err(err).Msg("Error recording foreign mention target")
			continue
		}
		stored = append(stored, mentionedUserID)
	}
	return stored, nil
}

// activeUserKey loads userID's active key, writing the error response and
// returning false when it is missing or revoked.
func (h *Handlers) activeUserKey(w http.ResponseWriter, r *http.Request, userID string) (*Key, bool) {
	log := h.services.log.GetLogger(r.Context())
	keyID, err := h.services.db.GetActiveKeyFingerprint(r.Context(), userID)
	if err != nil || keyID == "" {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading active key fingerprint")
		internalServerError(w)
		return nil, false
	}
	key, err := h.services.db.GetPublicKey(r.Context(), keyID)
	if err != nil {
		log.Error().Str("userID", userID).Str("keyID", keyID).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return nil, false
	}
	if key == nil || key.Revoked {
		writeError(w, http.StatusUnauthorized, "Active public key not available")
		return nil, false
	}
	return key, true
}

func (h *Handlers) SignReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("SignReed request received")

	userID := h.getUserID(r)

	req := &pb.SignReedRequest{}
	err := readRequest(r, req)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error parsing request")
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	userSignature := req.GetSignature()
	if userSignature == "" {
		writeError(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}

	reedID := req.GetReedId()
	if reedID == "" {
		writeError(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	// Echoing/replying are optional reed refs. Reed content never reaches
	// the server — only structural metadata and the author's claims about
	// it (tags, mentions), which receiving clients verify.
	echoing := strings.TrimSpace(req.GetEchoing())
	replyingTo := strings.TrimSpace(req.GetReplyingTo())
	previousID := strings.TrimSpace(req.GetPreviousId())
	claimedTags := req.GetTags()
	claimedMentions := req.GetMentions()

	localServerID := h.services.db.GetServerID()
	var echoRef *ReedRef
	var replyRef *ReedRef
	// Blankness/existence of the target was already weak trust theater;
	// the server no longer even tries. A viewer resolving the target
	// through the verify path enforces real authenticity.
	if echoing != "" {
		ref, ok := h.parseReedRef(echoing, localServerID)
		if !ok {
			writeError(w, http.StatusBadRequest, "Invalid echoing reference")
			return
		}
		echoRef = &ref
	}
	if replyingTo != "" {
		ref, ok := h.parseReedRef(replyingTo, localServerID)
		if !ok {
			writeError(w, http.StatusBadRequest, "Invalid replying reference")
			return
		}
		replyRef = &ref
	}
	if echoRef != nil && replyRef != nil {
		writeError(w, http.StatusBadRequest, "A reed cannot both echo and reply")
		return
	}
	for _, ref := range []*ReedRef{echoRef, replyRef} {
		if ref != nil && h.refuseBlockedRequester(w, r, ref.CanonicalAuthorID(), userID) {
			return
		}
	}

	rootID := ""
	if replyRef != nil {
		var err error
		rootID, err = h.services.db.ResolveConversationRoot(r.Context(), *replyRef)
		if err != nil {
			log.Error().Err(err).Msg("Error resolving conversation root")
			internalServerError(w)
			return
		}
	}

	storedMentions, err := h.resolveMentionClaims(r.Context(), claimedMentions, userID)
	if errors.Is(err, errMentionTargetNotFound) {
		writeError(w, http.StatusBadRequest, "Mentioned user not found")
		return
	}
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error validating mentions")
		internalServerError(w)
		return
	}
	storedMentions, err = h.dropMentionsBlockingAuthor(r.Context(), storedMentions, userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading users blocking the author")
		internalServerError(w)
		return
	}

	user, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeError(w, http.StatusBadRequest, "User not found")
		return
	}

	// Unverifiable here (no content), but still required/stored/countersigned:
	// it closes the "re-sign different content under the same id" swap
	// attack. See docs/content_privacy.md.
	pubKey, ok := h.activeUserKey(w, r, userID)
	if !ok {
		return
	}
	userFingerprint := pubKey.ID

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
	reedPayload := buildReedPayload(
		h.services.db.GetServerID(),
		reedID,
		h.signingKey.Fingerprint,
		userFingerprint,
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

	tags := normalizeClaimedTags(claimedTags)
	if h.filterPipeTags != nil {
		tags = h.filterPipeTags(tags)
	}
	reedServerFingerprint := serverSignature.ID
	createParams := createReedParams{
		ReedID:             reedID,
		UserID:             userID,
		UserKeyID:          userFingerprint,
		UserSignature:   userSignature,
		ServerFingerprint:  reedServerFingerprint,
		ServerSignature: serverSignature.Armor,
		Timestamp:          serverSignature.SignedAt,
		Tags:               tags,
		Mentions:           storedMentions,
		PreviousID:         previousID,
	}

	var reed *Reed
	var echoIndexed bool
	switch {
	case echoRef != nil:
		// is_blank is read-side display only; the server can't tell blank
		// from commented without content, so a conservative default costs
		// nothing — receiving clients derive the real answer themselves.
		reed, echoIndexed, err = h.services.db.CreateReedWithEcho(r.Context(), createParams, *echoRef, false)
	case replyRef != nil:
		reed, err = h.services.db.CreateReedWithReply(r.Context(), createParams, rootID, *replyRef)
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
			writeError(w, http.StatusConflict, "previousID does not match the author's current tip")
			return
		}
		log.Error().
			Str("reedID", reedID).
			Str("userID", userID).
			Str("serverFingerprint", reedServerFingerprint).
			Err(err).Msg("Error creating reed")
		internalServerError(w)
		return
	}

	if echoIndexed && echoRef != nil {
		h.metrics.EchoTargeted(r.Context(), echoRef.AuthorID, echoRef.ReedID)
		// A foreign echo target's server learns of it from new-reed.
		if echoRef.ServerID == localServerID {
			h.broadcastChan <- realtimeBroadcastMessage{
				Type:   realtimeEchoCountChanged,
				UserID: echoRef.CanonicalAuthorID(),
				ReedID: echoRef.ReedID,
			}
		}
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
				h.broadcastChan <- realtimeBroadcastMessage{
					Type:   realtimeReplyCountChanged,
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
		Kind:     reedKind,
		AuthorID: userID,
		ReedID:   reed.ID,
		TagCount: len(tags),
	})
	if activeUsers, err := getActiveUsers(r.Context(), h.services.db.db); err == nil {
		h.metrics.ReedCoverage(r.Context(), userID, reed.ID, 1, coveragePercent(1, activeUsers))
	}

	log.Debug().
		Str("userID", userID).
		Str("reedID", reed.ID).
		Msg("Reed created successfully")

	writeResponse(w, http.StatusCreated, pbServerSignature(serverSignature))
}

// respondSignReedReplay returns the stored countersignature (HTTP 200) when
// the user signature matches; otherwise 409.
func (h *Handlers) respondSignReedReplay(
	w http.ResponseWriter,
	r *http.Request,
	existing *ReedAttestation,
	userSignature, userID, reedID string,
) {
	log := h.services.log.GetLogger(r.Context())
	if existing.UserSignature != userSignature {
		writeError(w, http.StatusConflict, "Reed already exists with a different signature")
		return
	}
	log.Info().
		Str("reedID", reedID).
		Str("userID", userID).
		Msg("SignReed replay: returning stored countersignature")
	writeResponse(w, http.StatusOK, pbServerSignature(ServerSignature{
		ID:       existing.ServerFingerprint,
		Armor:    existing.ServerSignature,
		SignedAt: existing.ServerSignedAt,
	}))
}

// threadPartRequest is one part of a POST /threads body; its position in
// the list is its index.
type threadPartRequest struct {
	ReedID    string   `json:"reedID"`
	Signature string   `json:"signature"`
	Tags      []string `json:"tags"`
	Mentions  []string `json:"mentions"`
}

type createThreadRequest struct {
	PreviousID      string              `json:"previousID"`
	ThreadSignature string              `json:"threadSignature"`
	Reeds           []threadPartRequest `json:"reeds"`
}

// threadSignatures is the server's countersignature on a thread record and
// on each of its parts, in index order.
type threadSignatures struct {
	ServerSignature ServerSignature   `json:"serverSignature"`
	Reeds           []ServerSignature `json:"reeds"`
}

// CreateThread countersigns and stores a whole thread: every part and the
// author-signed thread record, in one transaction.
func (h *Handlers) CreateThread(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	userID := h.getUserID(r)

	var msg pb.CreateThreadRequest
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req := createThreadRequest{PreviousID: msg.GetPreviousId(), ThreadSignature: msg.GetThreadSignature()}
	for _, part := range msg.GetReeds() {
		req.Reeds = append(req.Reeds, threadPartRequest{
			ReedID:    part.GetReedId(),
			Signature: part.GetSignature(),
			Tags:      part.GetTags(),
			Mentions:  part.GetMentions(),
		})
	}
	if req.ThreadSignature == "" {
		writeError(w, http.StatusBadRequest, "Argument `threadSignature` is required")
		return
	}

	parts := make([]createReedParams, len(req.Reeds))
	reedIDs := make([]string, len(req.Reeds))
	seen := make(map[string]struct{}, len(req.Reeds))
	for i, part := range req.Reeds {
		if part.ReedID == "" || part.Signature == "" {
			writeError(w, http.StatusBadRequest, "Every reed needs `reedID` and `signature`")
			return
		}
		if _, dup := seen[part.ReedID]; dup {
			writeError(w, http.StatusBadRequest, "A reed appears twice in the thread")
			return
		}
		seen[part.ReedID] = struct{}{}
		reedIDs[i] = part.ReedID
		parts[i] = createReedParams{ReedID: part.ReedID, UserID: userID, UserSignature: part.Signature}
	}
	if err := validateThreadParts(userID, parts); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	threadID := reedIDs[0]

	for i, part := range req.Reeds {
		mentions, err := h.resolveMentionClaims(r.Context(), part.Mentions, userID)
		if errors.Is(err, errMentionTargetNotFound) {
			writeError(w, http.StatusBadRequest, "Mentioned user not found")
			return
		}
		if err != nil {
			log.Error().Str("userID", userID).Err(err).Msg("Error validating mentions")
			internalServerError(w)
			return
		}
		tags := normalizeClaimedTags(part.Tags)
		if h.filterPipeTags != nil {
			tags = h.filterPipeTags(tags)
		}
		parts[i].Mentions = mentions
		parts[i].Tags = tags
	}

	pubKey, ok := h.activeUserKey(w, r, userID)
	if !ok {
		return
	}
	serverID := h.services.db.GetServerID()
	userPayload := buildThreadUserPayload(serverID, threadID, reedIDs)
	if err := h.services.crypto.verifySignature(string(userPayload), req.ThreadSignature, pubKey.Armor); err != nil {
		writeError(w, http.StatusBadRequest, "Thread signature verification failed")
		return
	}

	existing, err := h.services.db.GetThreadRecord(r.Context(), threadID)
	if err != nil {
		log.Error().Str("threadID", threadID).Err(err).Msg("Error loading thread")
		internalServerError(w)
		return
	}
	if existing != nil {
		h.respondThreadReplay(w, r, existing, req)
		return
	}

	timestamp := time.Now().UTC().Truncate(time.Second)
	sigs := threadSignatures{Reeds: make([]ServerSignature, len(parts))}
	for i := range parts {
		sig, err := h.countersign(buildReedPayload(
			serverID, parts[i].ReedID, h.signingKey.Fingerprint, pubKey.ID, parts[i].UserSignature, timestamp,
		), timestamp)
		if err != nil {
			log.Error().Str("userID", userID).Err(err).Msg("Error signing thread part")
			internalServerError(w)
			return
		}
		parts[i].UserKeyID = pubKey.ID
		parts[i].ServerFingerprint = sig.ID
		parts[i].ServerSignature = sig.Armor
		sigs.Reeds[i] = sig
	}
	threadSig, err := h.countersign(buildThreadServerPayload(
		serverID, threadID, pubKey.ID, h.signingKey.Fingerprint, req.ThreadSignature, timestamp,
	), timestamp)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error signing thread record")
		internalServerError(w)
		return
	}
	sigs.ServerSignature = threadSig

	_, err = h.services.db.CreateThread(r.Context(), createThreadParams{
		UserID:            userID,
		UserKeyID:         pubKey.ID,
		UserSignature:     req.ThreadSignature,
		ServerFingerprint: threadSig.ID,
		ServerSignature:   threadSig.Armor,
		Timestamp:         timestamp,
		PreviousID:        strings.TrimSpace(req.PreviousID),
		Parts:             parts,
	})
	if err != nil {
		// A concurrent duplicate lost the insert race: replay the winner.
		if isThreadUniqueViolation(err) {
			if existing, getErr := h.services.db.GetThreadRecord(r.Context(), threadID); getErr == nil && existing != nil {
				h.respondThreadReplay(w, r, existing, req)
				return
			}
			writeError(w, http.StatusConflict, "A reed in the thread already exists")
			return
		}
		switch {
		case errors.Is(err, ErrReedFork):
			writeError(w, http.StatusConflict, "previousID does not match the author's current tip")
		case errors.Is(err, ErrInvalidThread):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			log.Error().Str("threadID", threadID).Str("userID", userID).Err(err).Msg("Error creating thread")
			internalServerError(w)
		}
		return
	}

	for i, part := range parts {
		h.metrics.ReedPublished(r.Context(), metrics.ReedPublishedAttrs{
			Kind:     metrics.ReedKindPlain,
			AuthorID: userID,
			ReedID:   part.ReedID,
			TagCount: len(parts[i].Tags),
		})
	}
	log.Debug().Str("userID", userID).Str("threadID", threadID).Int("reeds", len(parts)).Msg("Thread created")
	writeResponse(w, http.StatusCreated, pbServerSignatures(sigs))
}

// respondThreadReplay returns the stored signatures (HTTP 200) when the
// request repeats the stored thread exactly; otherwise 409.
func (h *Handlers) respondThreadReplay(w http.ResponseWriter, r *http.Request, existing *threadRecord, req createThreadRequest) {
	log := h.services.log.GetLogger(r.Context())
	conflict := func() {
		writeError(w, http.StatusConflict, "Thread already exists with different signatures")
	}
	if existing.UserSignature != req.ThreadSignature || len(existing.ReedIDs) != len(req.Reeds) {
		conflict()
		return
	}
	sigs := threadSignatures{
		ServerSignature: ServerSignature{
			ID:       existing.ServerFingerprint,
			Armor:    existing.ServerSignature,
			SignedAt: existing.ServerSignedAt,
		},
		Reeds: make([]ServerSignature, len(req.Reeds)),
	}
	for i, part := range req.Reeds {
		if existing.ReedIDs[i] != part.ReedID {
			conflict()
			return
		}
		att, err := h.services.db.GetReedAttestation(r.Context(), part.ReedID)
		if err != nil {
			log.Error().Str("reedID", part.ReedID).Err(err).Msg("Error loading thread part")
			internalServerError(w)
			return
		}
		if att == nil || att.UserSignature != part.Signature {
			conflict()
			return
		}
		sigs.Reeds[i] = ServerSignature{ID: att.ServerFingerprint, Armor: att.ServerSignature, SignedAt: att.ServerSignedAt}
	}
	log.Info().Str("threadID", existing.ThreadID).Msg("CreateThread replay: returning stored countersignatures")
	writeResponse(w, http.StatusOK, pbServerSignatures(sigs))
}

// DeleteThread removes a whole thread: the author signs a removal bound to
// the thread record's signature, and every part is removed with it.
func (h *Handlers) DeleteThread(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	userID := h.getUserID(r)
	threadID := mux.Vars(r)["threadID"]

	author, ok := authorOf(identityID(threadID))
	if !ok || string(author) != userID {
		writeError(w, http.StatusForbidden, "You can only delete your own threads")
		return
	}
	req := &pb.RemovalRequest{}
	if err := readRequest(r, req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	userSignature := strings.TrimSpace(req.GetSignature())
	if userSignature == "" {
		writeError(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}

	existing, err := h.services.db.GetThreadRemoval(r.Context(), threadID)
	if err != nil {
		log.Error().Str("threadID", threadID).Err(err).Msg("Error loading thread removal")
		internalServerError(w)
		return
	}
	if existing != nil {
		if existing.Cert.UserSignature.Armor != userSignature {
			writeError(w, http.StatusConflict, "Thread removal already exists with a different signature")
			return
		}
		writeResponse(w, http.StatusOK, pbThreadRemoval(existing))
		return
	}

	rec, err := h.services.db.GetThreadRecord(r.Context(), threadID)
	if err != nil {
		log.Error().Str("threadID", threadID).Err(err).Msg("Error loading thread record")
		internalServerError(w)
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "Thread not found")
		return
	}

	pubKey, ok := h.activeUserKey(w, r, userID)
	if !ok {
		return
	}
	serverID := h.services.db.GetServerID()
	userPayload := buildThreadRemovalUserPayload(serverID, threadID, rec.UserSignature)
	if err := h.services.crypto.verifySignature(string(userPayload), userSignature, pubKey.Armor); err != nil {
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	serverSignature, err := h.countersign(buildThreadRemovalServerPayload(
		serverID, threadID, pubKey.ID, h.signingKey.Fingerprint, userSignature, now,
	), now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing thread-removal countersignature")
		internalServerError(w)
		return
	}
	rm := threadRemoval{
		Cert: threadRemovalWire{
			Type:            identityTypeThreadRemoval,
			ServerID:        serverID,
			UserID:          userID,
			ThreadID:        threadID,
			UserSignature:   UserSignature{ID: pubKey.ID, Armor: userSignature},
			ServerSignature: serverSignature,
		},
		Record: rec.wire(serverID),
	}
	if err := h.services.db.InsertThreadRemoval(r.Context(), rm); err != nil {
		if errors.Is(err, errRemovalConflict) {
			writeError(w, http.StatusConflict, "Thread removal already exists with a different signature")
			return
		}
		log.Error().Str("threadID", threadID).Err(err).Msg("Error storing thread removal")
		internalServerError(w)
		return
	}

	for _, reedID := range rec.ReedIDs {
		h.metrics.ReedDeleted(r.Context(), userID, reedID)
		if err := h.services.db.DeleteMentionsForReed(r.Context(), reedID); err != nil {
			log.Error().Str("reedID", reedID).Err(err).Msg("Error clearing mention index for removed thread part")
		}
	}
	h.broadcastChan <- realtimeBroadcastMessage{Type: realtimeThreadRemoved, ThreadRemoval: &rm}
	go h.deliverAuthorToPeers(userID)

	log.Info().Str("userID", userID).Str("threadID", threadID).Msg("Thread removal accepted")
	writeResponse(w, http.StatusOK, pbThreadRemoval(&rm))
}

// parseReedRef parses userID@serverID/reedID and checks reed id + local server.
func (h *Handlers) parseReedRef(raw, localServerID string) (ReedRef, bool) {
	ref, ok := ParseReedRef(raw)
	if !ok {
		return ReedRef{}, false
	}
	if !isValidUUIDv7(ref.ReedID) {
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
		writeError(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}

	userID := h.getUserID(r)
	if userID != pathUserID {
		writeError(w, http.StatusForbidden, "You can only delete your own reeds")
		return
	}
	reedID := string(appendEntity(identityID(userID), bareReedID))

	req := &pb.RemovalRequest{}
	if err := readRequest(r, req); err != nil {
		log.Error().Err(err).Msg("Error parsing form")
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	userSignature := strings.TrimSpace(req.GetSignature())
	if userSignature == "" {
		writeError(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}

	serverID := h.services.db.GetServerID()

	// A thread is removed whole, never one part at a time.
	if head, err := h.services.db.ThreadHeadOf(r.Context(), reedID); err != nil {
		log.Error().Str("reedID", reedID).Err(err).Msg("Error checking thread membership")
		internalServerError(w)
		return
	} else if head != "" {
		writeError(w, http.StatusBadRequest, "Delete the whole thread")
		return
	}

	existing, err := h.services.db.GetReedRemoval(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error loading reed removal")
		internalServerError(w)
		return
	}
	if existing != nil {
		if existing.UserSignature != userSignature {
			writeError(w, http.StatusConflict, "Reed removal already exists with a different signature")
			return
		}
		writeResponse(w, http.StatusOK, pbReedRemoval(h.reedRemovalWire(existing)))
		return
	}

	reed, err := h.services.db.GetReed(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error getting reed")
		internalServerError(w)
		return
	}
	if reed == nil {
		writeError(w, http.StatusNotFound, "Reed not found")
		return
	}
	if reed.UserID != userID {
		writeError(w, http.StatusForbidden, "You can only delete your own reeds")
		return
	}

	user, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeError(w, http.StatusBadRequest, "User not found")
		return
	}

	fingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), userID)
	if err != nil || fingerprint == "" {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading active key fingerprint")
		internalServerError(w)
		return
	}
	userPayload := buildReedRemovalUserPayload(serverID, reedID)
	userSigArmor := userSignature
	pubKey, err := h.services.db.GetPublicKey(r.Context(), fingerprint)
	if err != nil {
		log.Error().Str("userID", userID).Str("fingerprint", fingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeError(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Err(err).
			Msg("signature verification failed")
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	serverPayload := buildReedRemovalServerPayload(
		serverID, reedID, fingerprint,
		h.signingKey.Fingerprint, userSignature, now,
	)
	serverSignature, err := h.countersign(serverPayload, now)
	if err != nil {
		log.Error().Err(err).Msg("Error producing reed-removal countersignature")
		internalServerError(w)
		return
	}

	cert := reedRemovalCert{
		ReedID:            reedID,
		UserID:            userID,
		UserSignature:     userSignature,
		UserKeyID:         fingerprint,
		ServerSignature:   serverSignature.Armor,
		ServerFingerprint: serverSignature.ID,
		ServerSignedAt:    serverSignature.SignedAt,
	}
	if err := h.services.db.InsertReedRemoval(r.Context(), cert); err != nil {
		if errors.Is(err, errRemovalConflict) {
			// Concurrent first accept: return the stored cert if the user
			// signature matches; otherwise a true conflicting attestation.
			existing, getErr := h.services.db.GetReedRemoval(r.Context(), reedID)
			if getErr == nil && existing != nil && existing.UserSignature == userSignature {
				writeResponse(w, http.StatusOK, pbReedRemoval(h.reedRemovalWire(existing)))
				return
			}
			writeError(w, http.StatusConflict, "Reed removal already exists with a different signature")
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
			// A foreign target's server learns of it from reed-removal.
			if t.ServerID != serverID {
				continue
			}
			h.broadcastChan <- realtimeBroadcastMessage{
				Type:   realtimeEchoCountChanged,
				UserID: t.CanonicalAuthorID(),
				ReedID: t.ReedID,
			}
		}
	}

	// Keep the reeds row for allocation catch-up: reed_allocations FK
	// cascades on reed delete. Tip/list already exclude reed_removals.
	wire := newReedRemovalWire(serverID, cert)

	replyTargets, err := h.services.db.ReplyCountNotifyTargetsForRemovedReply(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error resolving reply count targets for removed reed")
	} else {
		for i, t := range replyTargets {
			// A foreign parent's server learns of it from reed-removal.
			if i == 0 && t.ServerID != serverID {
				continue
			}
			h.broadcastChan <- realtimeBroadcastMessage{
				Type:   realtimeReplyCountChanged,
				UserID: t.CanonicalAuthorID(),
				ReedID: t.ReedID,
			}
		}
	}

	h.broadcastChan <- realtimeBroadcastMessage{
		Type:        realtimeReedRemoved,
		ServerID:    serverID,
		UserID:      userID,
		ReedID:      bareReedID,
		ReedRemoval: &wire,
	}

	go h.deliverAuthorToPeers(userID)

	log.Info().Str("userID", userID).Str("reedID", reedID).Msg("Reed removal accepted")
	writeResponse(w, http.StatusOK, pbReedRemoval(h.reedRemovalWire(&cert)))
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
		writeError(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(appendEntity(identityID(authorID), bareReedID))
	if viewerID := h.requestViewer(r); viewerID != "" && h.refuseBlockedRequester(w, r, authorID, viewerID) {
		return
	}

	if h.proxyLikeToForeignReed(w, r, reedID) {
		return
	}

	req := &pb.LikeRequest{}
	if err := readRequest(r, req); err != nil {
		log.Error().Err(err).Msg("Error parsing form")
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	likerID, ok := h.resolveActingUser(r, req.GetLikerId())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}
	if h.refuseBlockedRequester(w, r, authorID, likerID) {
		return
	}
	userSignature := strings.TrimSpace(req.GetSignature())
	if userSignature == "" {
		writeError(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}
	bareFingerprint := strings.TrimSpace(req.GetFingerprint())
	if bareFingerprint == "" {
		writeError(w, http.StatusBadRequest, "Argument `fingerprint` is required")
		return
	}
	fingerprint := string(appendEntity(identityID(likerID), bareFingerprint))

	serverID := h.services.db.GetServerID()

	existing, err := h.services.db.GetReedLike(r.Context(), likerID, reedID)
	if err != nil {
		log.Error().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Err(err).Msg("Error loading reed like")
		internalServerError(w)
		return
	}
	if existing != nil {
		if existing.UserSignature.Armor != userSignature {
			writeError(w, http.StatusConflict, "Reed like already exists with a different signature")
			return
		}
		writeResponse(w, http.StatusOK, pbLikeCert(existing))
		return
	}

	reed, err := h.services.db.GetReed(r.Context(), reedID)
	if err != nil {
		log.Error().Str("authorID", authorID).Str("reedID", reedID).Err(err).Msg("Error getting reed")
		internalServerError(w)
		return
	}
	if reed == nil {
		writeError(w, http.StatusNotFound, "Reed not found")
		return
	}

	userPayload := buildReedLikeUserPayload(reedID, fingerprint)
	userSigArmor := userSignature
	pubKey, err := h.resolvePublicKey(r.Context(), fingerprint)
	if err != nil {
		log.Error().Str("likerID", likerID).Str("fingerprint", fingerprint).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeError(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().
			Str("likerID", likerID).
			Str("authorID", authorID).
			Str("reedID", reedID).
			Err(err).
			Msg("signature verification failed")
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	serverPayload := buildReedLikeServerPayload(
		reedID,
		h.signingKey.Fingerprint, userSignature, now,
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
			ID:    fingerprint,
			Armor: userSignature,
		},
		ServerSignature: serverSignature,
	}
	if err := h.services.db.InsertReedLike(r.Context(), likerID, fingerprint, cert); err != nil {
		if errors.Is(err, ErrLikeConflict) {
			existing, getErr := h.services.db.GetReedLike(r.Context(), likerID, reedID)
			if getErr == nil && existing != nil && existing.UserSignature.Armor == userSignature {
				writeResponse(w, http.StatusOK, pbLikeCert(existing))
				return
			}
			writeError(w, http.StatusConflict, "Reed like already exists with a different signature")
			return
		}
		log.Error().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Err(err).Msg("Error storing reed like")
		internalServerError(w)
		return
	}

	h.broadcastChan <- realtimeBroadcastMessage{
		Type:   realtimeLikeCountChanged,
		UserID: authorID,
		ReedID: bareReedID,
	}

	log.Info().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Msg("Reed like accepted")
	writeResponse(w, http.StatusOK, pbLikeCert(&cert))
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
		writeError(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(appendEntity(identityID(authorID), bareReedID))

	if h.proxyUnlikeToForeignReed(w, r, reedID) {
		return
	}

	req := &pb.UnlikeRequest{}
	if err := readRequest(r, req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	likerID, ok := h.resolveActingUser(r, req.GetLikerId())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}

	deleted, err := h.services.db.DeleteReedLike(r.Context(), likerID, reedID)
	if err != nil {
		log.Error().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Err(err).Msg("Error deleting reed like")
		internalServerError(w)
		return
	}

	if deleted {
		h.broadcastChan <- realtimeBroadcastMessage{
			Type:   realtimeLikeCountChanged,
			UserID: authorID,
			ReedID: bareReedID,
		}
	}

	log.Info().Str("likerID", likerID).Str("authorID", authorID).Str("reedID", reedID).Msg("Reed unlike accepted")
	w.WriteHeader(http.StatusNoContent)
}

// PinReed handles POST /reeds/{reedID}/pin.
func (h *Handlers) PinReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	reedID := mux.Vars(r)["reedID"]
	if reedID == "" {
		writeError(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	req := &pb.PinRequest{}
	if err := readRequest(r, req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	pinnerID, ok := h.resolveActingUser(r, req.GetPinnerId())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}

	if err := h.services.db.PinReed(r.Context(), pinnerID, reedID); err != nil {
		if errors.Is(err, ErrPinTargetNotFound) {
			writeError(w, http.StatusNotFound, "Reed not found or not owned by this user")
			return
		}
		if errors.Is(err, ErrPinLimitReached) {
			writeError(w, http.StatusConflict, "Already pinned 3 reeds")
			return
		}
		log.Error().Str("pinnerID", pinnerID).Str("reedID", reedID).Err(err).Msg("Error pinning reed")
		internalServerError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnpinReed handles DELETE /reeds/{reedID}/pin. Always 204, even if
// reedID wasn't pinned — mirrors UnlikeReed's no-op-on-absent behavior.
func (h *Handlers) UnpinReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	reedID := mux.Vars(r)["reedID"]
	if reedID == "" {
		writeError(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	req := &pb.PinRequest{}
	if err := readRequest(r, req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	pinnerID, ok := h.resolveActingUser(r, req.GetPinnerId())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}

	if err := h.services.db.UnpinReed(r.Context(), pinnerID, reedID); err != nil {
		log.Error().Str("pinnerID", pinnerID).Str("reedID", reedID).Err(err).Msg("Error unpinning reed")
		internalServerError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) reedRemovalWire(cert *reedRemovalCert) ReedRemoval {
	return ReedRemoval{
		Type:     identityTypeReed,
		ServerID: h.services.db.GetServerID(),
		UserID:   cert.UserID,
		ReedID:   cert.ReedID,
		UserSignature: UserSignature{
			ID:    cert.UserKeyID,
			Armor: cert.UserSignature,
		},
		ServerSignature: ServerSignature{
			ID:       cert.ServerFingerprint,
			Armor:    cert.ServerSignature,
			SignedAt: cert.ServerSignedAt,
		},
	}
}

func (h *Handlers) GetReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReed request received")

	bareReedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || bareReedID == "" {
		writeError(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(appendEntity(identityID(userID), bareReedID))

	if h.refuseIfBlocked(w, r, userID) {
		return
	}

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
		writeAccountGone(w, h.accountRemovalWire(result.AccountRemoval))
		return
	}
	if result.ReedRemoval != nil {
		writeReedGone(w, h.reedRemovalWire(result.ReedRemoval))
		return
	}
	if result.ThreadRemoval != nil {
		writeThreadGone(w, result.ThreadRemoval)
		return
	}
	if result.Reed == nil {
		writeError(w, http.StatusNotFound, "Post not found")
		return
	}

	log.Debug().
		Str("userID", userID).
		Str("reedID", reedID).
		Msg("Post found")

	// The reed exists and is not removed, which is all this route can say:
	// the server holds no content, so a body would carry nothing the
	// caller did not already have. Content comes from a peer relay.
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) GetReedEchoCount(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReedEchoCount request received")

	bareReedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || bareReedID == "" {
		writeError(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(appendEntity(identityID(userID), bareReedID))

	if h.refuseIfBlocked(w, r, userID) {
		return
	}

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
		writeAccountGone(w, h.accountRemovalWire(result.AccountRemoval))
		return
	}
	if result.ReedRemoval != nil {
		writeReedGone(w, h.reedRemovalWire(result.ReedRemoval))
		return
	}
	if result.ThreadRemoval != nil {
		writeThreadGone(w, result.ThreadRemoval)
		return
	}
	if result.Reed == nil {
		writeError(w, http.StatusNotFound, "Post not found")
		return
	}

	count, err := h.services.db.CountEchoes(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error counting echoes")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, &pb.EchoCountResponse{Count: int32(count)})
}

// GetReedChorus handles GET /reeds/{userID}/{reedID}/chorus.
func (h *Handlers) GetReedChorus(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReedChorus request received")

	bareReedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || bareReedID == "" {
		writeError(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(appendEntity(identityID(userID), bareReedID))

	if h.refuseIfBlocked(w, r, userID) {
		return
	}

	if handled, _ := h.proxyIfForeign(w, r, reedID); handled {
		return
	}

	result, err := h.services.db.GetReedOrRemovalCert(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error loading reed")
		internalServerError(w)
		return
	}
	if result.Reed == nil && result.ReedRemoval == nil && result.AccountRemoval == nil && result.ThreadRemoval == nil {
		writeError(w, http.StatusNotFound, "Post not found")
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	var before *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid before cursor")
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

	writeResponse(w, http.StatusOK, pbEchoerList(list))
}

func (h *Handlers) GetReedReplies(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReedReplies request received")

	bareReedID := mux.Vars(r)["reedID"]
	userID := mux.Vars(r)["userID"]
	if userID == "" || bareReedID == "" {
		writeError(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	reedID := string(appendEntity(identityID(userID), bareReedID))

	if h.refuseIfBlocked(w, r, userID) {
		return
	}

	if handled, _ := h.proxyIfForeign(w, r, reedID); handled {
		return
	}

	result, err := h.services.db.GetReedOrRemovalCert(r.Context(), reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error loading reed")
		internalServerError(w)
		return
	}
	if result.Reed == nil && result.ReedRemoval == nil && result.AccountRemoval == nil && result.ThreadRemoval == nil {
		writeError(w, http.StatusNotFound, "Post not found")
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	var before *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid before cursor")
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

	writeResponse(w, http.StatusOK, pbReplyList(list))
}


// DeleteMention handles DELETE /mentions/{reedID}: the caller reports a
// claimed mention of them isn't really present, removing it from their
// inbox only (row key is reedID+callerUserID). Doesn't touch the reed.
func (h *Handlers) DeleteMention(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("DeleteMention request received")

	userID := h.getUserID(r)

	reedID := mux.Vars(r)["reedID"]
	if reedID == "" {
		writeError(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	reason := strings.TrimSpace(r.URL.Query().Get("reason"))

	removed, err := h.services.db.DeleteMentionEntry(r.Context(), reedID, userID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error deleting mention entry")
		internalServerError(w)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "Mention not found")
		return
	}

	authorID := reedID
	if a, ok := authorOf(identityID(reedID)); ok {
		authorID = string(a)
	}
	h.metrics.MentionClaimRejected(r.Context(), authorID, userID, reason)

	writeResponse(w, http.StatusOK, &pb.DeleteMentionResponse{Removed: true})
}

// GetUserFollowing handles GET /users/{userID}/following.
func (h *Handlers) GetUserFollowing(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUserFollowing request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeError(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	if h.refuseIfBlocked(w, r, userID) {
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	var before *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid before cursor")
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

	writeResponse(w, http.StatusOK, pbFollowList(list))
}

// GetUserFollowers handles GET /users/{userID}/followers.
func (h *Handlers) GetUserFollowers(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUserFollowers request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeError(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	if h.refuseIfBlocked(w, r, userID) {
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	var before *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid before cursor")
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

	writeResponse(w, http.StatusOK, pbFollowList(list))
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
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req pb.RecordBackupRequest
	if err := readRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	kind := metrics.BackupKind(strings.TrimSpace(strings.ToLower(req.GetKind())))
	switch kind {
	case metrics.BackupKindIdentity, metrics.BackupKindFull:
	default:
		writeError(w, http.StatusBadRequest, "kind must be identity or full")
		return
	}

	h.metrics.UserBackup(r.Context(), userID, kind)
	w.WriteHeader(http.StatusNoContent)
}

// BindDevice handles POST /api/users/device — revoke-all + bind this origin's device.
func (h *Handlers) BindDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok || userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID, err := parseDeviceID(r.Header.Get("X-Syrinx-Device-Id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid device id.")
		return
	}

	now := time.Now().UTC()
	if err := h.services.db.BindDevice(r.Context(), userID, deviceID, now); err != nil {
		internalServerError(w)
		return
	}

	h.kickUserDevices(userID)

	writeResponse(w, http.StatusOK, &pb.BindDeviceResponse{DeviceId: deviceID})
}

func (h *Handlers) kickUserDevices(userID string) {
	if h.kickUserWS != nil {
		h.kickUserWS(userID)
	}
}

// ==================== //
//   Account recovery   //
// ==================== //

type bootstrapAccountRecoveryRequest struct {
	Challenge string `json:"challenge"`
	UserID    string `json:"userID"`
	KeyID     string `json:"keyID"`
	Signature string `json:"signature"`
}

func (h *Handlers) AccountRecoveryChallenge(w http.ResponseWriter, r *http.Request) {
	nonce, err := h.services.db.IssueAccountRecoveryChallenge(r.Context())
	if err != nil {
		internalServerError(w)
		return
	}
	writeResponse(w, http.StatusOK, &pb.ChallengeResponse{Challenge: nonce})
}

func (h *Handlers) BootstrapAccountRecovery(w http.ResponseWriter, r *http.Request) {
	var msg pb.AccountRecoveryBootstrapRequest
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req := bootstrapAccountRecoveryRequest{
		Challenge: msg.GetChallenge(),
		UserID:    msg.GetUserId(),
		KeyID:     msg.GetKeyId(),
		Signature: msg.GetSignature(),
	}
	if req.Challenge == "" || req.UserID == "" || req.KeyID == "" || req.Signature == "" {
		writeError(w, http.StatusBadRequest, "Missing required fields")
		return
	}

	deviceID, err := parseDeviceID(r.Header.Get("X-Syrinx-Device-Id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Missing or invalid X-Syrinx-Device-Id header")
		return
	}

	// Consumed before any lookup, so a failed bootstrap can't be retried with it.
	live, err := h.services.db.ConsumeAccountRecoveryChallenge(r.Context(), req.Challenge)
	if err != nil {
		internalServerError(w)
		return
	}
	if !live {
		writeError(w, http.StatusBadRequest, "Unknown or expired challenge")
		return
	}
	now := time.Now()

	removed, err := h.services.db.HasAccountRemoval(r.Context(), req.UserID)
	if err != nil {
		internalServerError(w)
		return
	}
	if removed {
		writeError(w, http.StatusGone, "Account removed")
		return
	}

	profile, err := h.services.db.GetUserProfile(r.Context(), req.UserID)
	if err != nil {
		internalServerError(w)
		return
	}
	if profile == nil {
		writeError(w, http.StatusNotFound, "Account not found")
		return
	}

	activeFingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), req.UserID)
	if err != nil {
		internalServerError(w)
		return
	}
	if activeFingerprint == "" || activeFingerprint != req.KeyID {
		writeError(w, http.StatusUnauthorized, "Key is not the active key for this account")
		return
	}

	key, err := h.services.db.GetPublicKey(r.Context(), req.KeyID)
	if err != nil {
		internalServerError(w)
		return
	}
	if key == nil || key.Revoked {
		writeError(w, http.StatusUnauthorized, "Unknown or revoked key")
		return
	}

	if err := verifyChallengeSignature(req.Challenge, req.Signature, key.Armor, h.services.crypto); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
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

	writeResponse(w, http.StatusOK, &pb.AccountRecoveryBootstrapResponse{
		Profile:   pbUser(profile),
		Following: following,
		TipReedId: tipReedID,
		ReedIds:   reedIDs,
	})
}

// ============ //
//   recovery   //
// ============ //

// IssueChallenge handles GET /api/recovery/identity/claim.
func (h *Handlers) IssueChallenge(w http.ResponseWriter, r *http.Request) {
	nonce, err := h.services.db.IssueRecoveryChallenge(r.Context())
	if err != nil {
		internalServerError(w)
		return
	}
	writeResponse(w, http.StatusOK, &pb.ChallengeResponse{Challenge: nonce})
}

// ClaimIdentity handles POST /api/recovery/identity/claim.
func (h *Handlers) ClaimIdentity(w http.ResponseWriter, r *http.Request) {
	req, err := readRecoveryClaim(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Consumed before anything else, so a failed claim can't be retried with it.
	live, err := h.services.db.ConsumeRecoveryChallenge(r.Context(), req.Challenge)
	if err != nil {
		internalServerError(w)
		return
	}
	if !live {
		writeError(w, http.StatusBadRequest, "Unknown or expired challenge")
		return
	}

	serverID := h.services.db.GetServerID()
	active, keys, err := flattenKeysNest(r.Context(), req.Profile, req.Key, serverID, h.services.db.GetServerKeyArmorAt, h.services.crypto)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := requireUnrevokedTip(active); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	if err := verifyChallengeSignature(req.Challenge, req.Signature, active.Key.Armor, h.services.crypto); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	deviceID, err := parseDeviceID(r.Header.Get("X-Syrinx-Device-Id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Missing or invalid X-Syrinx-Device-Id header")
		return
	}

	res, err := saveOwnIdentity(r.Context(), h.services.db.db, serverID, req.Profile, keys, deviceID)
	if err != nil {
		internalServerError(w)
		return
	}
	if res.Rejected {
		writeError(w, http.StatusConflict, "Username is already held by a more recently signed identity on this server")
		return
	}
	if res.Created {
		h.metrics.UserCreated(r.Context(), metrics.SignupModeImport, req.Profile.ID)
	}

	req.Profile.ActiveKeyFingerprint = active.Key.Fingerprint
	writeResponse(w, http.StatusOK, pbRecoveryProfile(req.Profile))
}

// ReportPeerIdentity handles POST /api/recovery/identity.
func (h *Handlers) ReportPeerIdentity(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(userIDKey).(string)
	if !ok || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	req, err := readRecoveryPeerIdentity(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Profile.ID == caller {
		writeError(w, http.StatusBadRequest, "own identity must use claim")
		return
	}

	serverID := h.services.db.GetServerID()
	active, keys, err := flattenKeysNest(r.Context(), req.Profile, req.Key, serverID, h.services.db.GetServerKeyArmorAt, h.services.crypto)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	res, err := savePeerIdentity(r.Context(), h.services.db.db, serverID, req.Profile, keys)
	if err != nil {
		internalServerError(w)
		return
	}
	if res.Rejected {
		writeError(w, http.StatusConflict, "Username is already held by a more recently signed identity on this server")
		return
	}
	if res.Created {
		h.metrics.UserCreated(r.Context(), metrics.SignupModeImport, req.Profile.ID)
	}

	req.Profile.ActiveKeyFingerprint = active.Key.Fingerprint
	writeResponse(w, http.StatusOK, pbRecoveryProfile(req.Profile))
}

// ReportReed handles POST /api/recovery/reeds.
func (h *Handlers) ReportReed(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(userIDKey).(string)
	if !ok || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	req, err := readRecoveryReed(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.ReedID == "" || req.AuthorID == "" || req.UserSignature.Armor == "" {
		writeError(w, http.StatusBadRequest, "reedID, authorID, and userSignature are required")
		return
	}
	if req.ServerSignature.Fingerprint == "" || req.ServerSignature.Armor == "" || req.ServerSignature.Timestamp.IsZero() {
		writeError(w, http.StatusBadRequest, "server countersignature is required")
		return
	}

	serverID := h.services.db.GetServerID()
	if err := verifyRecoveryReedCountersig(r.Context(), req, serverID, h.services.db.GetServerKeyArmorAt, h.services.crypto); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// req.AuthorID (reed.userID client-side) and req.UserSignature.KeyID
	// both arrive already canonical — only req.ReedID is bare on this wire.
	authorKeyID := req.UserSignature.KeyID
	canonicalReedID := string(appendEntity(identityID(req.AuthorID), req.ReedID))
	err = saveRecoveryReed(r.Context(), h.services.db.db,
		serverID,
		canonicalReedID,
		req.ServerSignature.Fingerprint,
		req.ServerSignature.Timestamp,
		caller,
		authorKeyID,
		req.UserSignature.Armor,
		req.ServerSignature.Armor,
	)
	switch {
	case errors.Is(err, errRecoveryAuthorNotFound):
		writeError(w, http.StatusBadRequest, "author not found")
	case errors.Is(err, errRecoveryReedConflict):
		writeError(w, http.StatusConflict, "reed metadata conflict")
	case err != nil:
		internalServerError(w)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ReportFollowing handles POST /api/recovery/following.
func (h *Handlers) ReportFollowing(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(userIDKey).(string)
	if !ok || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var msg pb.RecoveryFollowingRequest
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req := recoveryFollowingRequest{UserIDs: msg.GetUserIds()}
	if len(req.UserIDs) > maxRecoveryFollowingBatch {
		writeError(w, http.StatusBadRequest, "userIDs exceeds maximum of 100")
		return
	}
	for _, id := range req.UserIDs {
		if id == caller {
			writeError(w, http.StatusBadRequest, "Cannot follow yourself")
			return
		}
	}

	if err := saveRecoveryFollowing(r.Context(), h.services.db.db, h.services.db.GetServerID(), caller, req.UserIDs); err != nil {
		internalServerError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CompleteImport handles POST /api/recovery/complete.
func (h *Handlers) CompleteImport(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(userIDKey).(string)
	if !ok || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	if err := h.services.db.DeleteOngoing(r.Context(), h.services.db.GetServerID(), caller); err != nil {
		internalServerError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func verifyRecoveryReedCountersig(ctx context.Context, req recoveryReedRequest, serverID string, lookup recoveryServerKeyLookup, v recoveryVerifier) error {
	if req.ServerSignature.ServerID != "" && req.ServerSignature.ServerID != serverID {
		return fmt.Errorf("server id mismatch")
	}
	serverPub, err := lookup(ctx, req.ServerSignature.Fingerprint, req.ServerSignature.Timestamp)
	if err != nil {
		return err
	}
	if serverPub == "" {
		return fmt.Errorf("unknown server key %s", req.ServerSignature.Fingerprint)
	}

	// req.AuthorID arrives already canonical (reed.userID client-side);
	// only req.ReedID is bare on this wire — append it to rebuild the id
	// the original countersignature was computed over.
	canonicalReedID := string(appendEntity(identityID(req.AuthorID), req.ReedID))
	ts := req.ServerSignature.Timestamp.UTC().Truncate(time.Second)
	payload := buildReedPayload(
		serverID,
		canonicalReedID,
		req.ServerSignature.Fingerprint,
		req.UserSignature.KeyID,
		req.UserSignature.Armor,
		ts,
	)
	sigArmor := req.ServerSignature.Armor
	if err := v.verifySignature(string(payload), sigArmor, serverPub); err != nil {
		return fmt.Errorf("bad countersignature")
	}
	return nil
}

// ============== //
//   Federation   //
// ============== //

func (h *Handlers) isAdmin(ctx context.Context, userID string) (bool, error) {
	role, err := h.services.db.GetUserRole(ctx, userID)
	if err != nil {
		return false, err
	}
	return requireAdmin(role) == nil, nil
}

func (h *Handlers) isRoot(ctx context.Context, userID string) (bool, error) {
	role, err := h.services.db.GetUserRole(ctx, userID)
	if err != nil {
		return false, err
	}
	return isRoot(userID, role, h.services.db.GetServerID()), nil
}

func (h *Handlers) federationSignServer(message []byte) (string, error) {
	sigArmor, err := h.services.crypto.sign(string(message), h.signingKey.Armor)
	if err != nil {
		return "", err
	}
	return sigArmor, nil
}

// federationRequestTimeout caps every server-to-server call, so requests
// to a slow or unreachable peer can't pile up here.
const federationRequestTimeout = 3 * time.Second

// federationHTTPClient returns the client used for all server-to-server
// federation calls.
func (h *Handlers) federationHTTPClient() *http.Client {
	if h.federationHTTPClientOverride != nil {
		return h.federationHTTPClientOverride
	}
	return &http.Client{Timeout: federationRequestTimeout}
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
	_, serverID, ok := parseIdentityID(identityID(canonicalID))
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

// foreignServerOf returns the peer embedded in id, or ok=false when id is
// local or malformed.
func (h *Handlers) foreignServerOf(id string) (serverID string, ok bool) {
	_, embeddedServerID, _, ok := parseKeyFingerprint(identityID(id))
	if !ok {
		_, embeddedServerID, ok = parseIdentityID(identityID(id))
	}
	if !ok || embeddedServerID == h.services.db.GetServerID() {
		return "", false
	}
	return embeddedServerID, true
}

// proxyIfForeign proxies to the peer owning id's embedded serverID if
// foreign (handled=true, plus the peer's status code), or does nothing
// (handled=false) when id is local or malformed.
func (h *Handlers) proxyIfForeign(w http.ResponseWriter, r *http.Request, id string) (handled bool, status int) {
	embeddedServerID, ok := h.foreignServerOf(id)
	if !ok {
		return false, 0
	}

	peer, err := h.services.db.GetServerByID(r.Context(), embeddedServerID)
	if err != nil {
		internalServerError(w)
		return true, 0
	}
	if peer == nil {
		writeError(w, http.StatusNotFound, "Not found")
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
	_, embeddedServerID, _, ok := parseKeyFingerprint(identityID(reedID))
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
		writeError(w, http.StatusNotFound, "Not found")
		return true
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		internalServerError(w)
		return true
	}
	var req pb.LikeRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return true
	}
	likerID, ok := h.resolveActingUser(r, req.GetLikerId())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Could not resolve acting user")
		return true
	}

	respBody, status, err := h.forwardToPeer(r, peer.BaseURL, string(body))
	if err != nil {
		log.Error().Err(err).Str("target", peer.BaseURL).Msg("proxy like to peer server failed")
		h.logFederationServerAsync(peer.ID, "error", fmt.Sprintf("Proxy like to %s failed: %s", peer.BaseURL, err.Error()))
		writeError(w, http.StatusBadGateway, "Failed to reach peer server")
		return true
	}

	if status == http.StatusOK {
		var cert pb.LikeCert
		if err := proto.Unmarshal(respBody, &cert); err != nil {
			log.Error().Err(err).Str("likerID", likerID).Str("reedID", reedID).Msg("Failed to parse peer like cert")
		} else if err := h.mirrorForeignLike(r.Context(), likerID, likeCertFromPB(&cert)); err != nil {
			log.Error().Err(err).Str("likerID", likerID).Str("reedID", reedID).Msg("Failed to mirror foreign like locally")
		}
	}

	w.Header().Set("Content-Type", protobufContentType)
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
	if err := h.services.db.InsertReedLike(ctx, likerID, cert.UserSignature.ID, cert); err != nil {
		return fmt.Errorf("insert reed like: %w", err)
	}
	return nil
}

// proxyUnlikeToForeignReed mirrors proxyLikeToForeignReed for the
// DELETE/unlike direction: forwards to the home server, and on success
// removes the locally mirrored like row (if any) so both servers agree.
func (h *Handlers) proxyUnlikeToForeignReed(w http.ResponseWriter, r *http.Request, reedID string) (handled bool) {
	_, embeddedServerID, _, ok := parseKeyFingerprint(identityID(reedID))
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
		writeError(w, http.StatusNotFound, "Not found")
		return true
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return true
	}
	var req pb.UnlikeRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return true
	}
	likerID, ok := h.resolveActingUser(r, req.GetLikerId())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Could not resolve acting user")
		return true
	}

	respBody, status, err := h.forwardToPeer(r, peer.BaseURL, string(body))
	if err != nil {
		log.Error().Err(err).Str("target", peer.BaseURL).Msg("proxy unlike to peer server failed")
		h.logFederationServerAsync(peer.ID, "error", fmt.Sprintf("Proxy unlike to %s failed: %s", peer.BaseURL, err.Error()))
		writeError(w, http.StatusBadGateway, "Failed to reach peer server")
		return true
	}

	if status == http.StatusNoContent {
		if _, err := h.services.db.DeleteReedLike(r.Context(), likerID, reedID); err != nil {
			log.Error().Err(err).Str("likerID", likerID).Str("reedID", reedID).Msg("Failed to mirror foreign unlike locally")
		}
	}

	if len(respBody) > 0 {
		w.Header().Set("Content-Type", protobufContentType)
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
	query := r.URL.Query()
	if viewerID, ok := r.Context().Value(userIDKey).(string); ok {
		query.Set("requester", viewerID)
	}
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
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
	query := r.URL.Query()
	if viewerID, ok := r.Context().Value(userIDKey).(string); ok {
		query.Set("requester", viewerID)
	}
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
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
		writeError(w, http.StatusBadGateway, "Failed to reach peer server")
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
	var respBody io.Reader = resp.Body
	if resp.StatusCode == http.StatusForbidden {
		if body, err := io.ReadAll(resp.Body); err == nil {
			h.acceptRefusalBlock(r.Context(), peerServerID, body)
			respBody = bytes.NewReader(body)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, respBody); err != nil {
		log.Error().Err(err).Str("target", target).Msg("failed to relay proxied response body")
	}
	return resp.StatusCode
}

// forwardFollowToPeer tells a peer that followerID follows/unfollows
// userID (local to the peer). Blocks until the peer confirms.
func (h *Handlers) forwardFollowToPeer(ctx context.Context, method, peerBaseURL, userID, followerID string) error {
	target := strings.TrimRight(peerBaseURL, "/") + "/api/users/" + userID + "/follow"
	encoded, err := proto.Marshal(&pb.FollowRequest{FollowerId: followerID})
	if err != nil {
		return err
	}
	body := string(encoded)
	httpReq, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", protobufContentType)
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

	sigArmor, err := h.services.crypto.sign(canonical, h.signingKey.Armor)
	if err != nil {
		return err
	}
	publicKeyID := string(canonicalID(h.services.db.GetServerID(), h.signingKey.Fingerprint))
	req.Header.Set("X-Syrinx-Public-Key-Id", publicKeyID)
	req.Header.Set("X-Syrinx-Signature", base64Encode(sigArmor))
	req.Header.Set("X-Syrinx-Signature-Scope", "body")
	req.Header.Set("X-Syrinx-Timestamp", timestamp)
	return nil
}

// fetchPeerServerKeyArmor live-fetches a peer's own signing key armor over
// GET /api/keys/{id} — a server-owned key id is "fingerprint@serverID",
// same route as any other federated key. Peer keys are never persisted
// locally; the returned armor's fingerprint is checked against the
// caller's pinned expectation rather than trusted outright.
func (h *Handlers) fetchPeerServerKeyArmor(ctx context.Context, baseURL, serverID, fingerprint string) (string, error) {
	keyID := string(canonicalID(serverID, fingerprint))
	target := strings.TrimRight(baseURL, "/") + "/api/keys/" + keyID
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	if err := h.setPeerProxyAuthHeaders(httpReq, ""); err != nil {
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
		return "", fmt.Errorf("read peer server key: %w", err)
	}
	key, err := decodePeerKey(body)
	if err != nil {
		return "", fmt.Errorf("decode peer server key: %w", err)
	}
	armorBytes := key.Armor
	armor := string(armorBytes)
	actualFingerprint, err := h.services.crypto.extractFingerprintFromArmor(armor)
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

	_, embeddedServerID, _, ok := parseKeyFingerprint(identityID(fingerprint))
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read peer user key: %w", err)
	}
	key, err := decodePeerKey(body)
	if err != nil {
		return nil, fmt.Errorf("decode peer user key: %w", err)
	}
	return h.verifyAndCachePeerUserKey(ctx, baseURL, peerServerID, key)
}

// verifyAndCachePeerUserKey checks a user key a peer served against the
// peer's countersignature, then caches it locally.
func (h *Handlers) verifyAndCachePeerUserKey(ctx context.Context, baseURL, peerServerID string, key Key) (*Key, error) {
	armor := key.Armor

	peerKeyServerFingerprint, _, parseOK := parseIdentityID(identityID(key.ServerSignature.ID))
	if !parseOK {
		return nil, fmt.Errorf("malformed peer key server signature id: %s", key.ServerSignature.ID)
	}
	// The peer's own signing key, live-fetched and pinned against what
	// the response itself claims signed it — never trusted from a local
	// cache (peer keys are never persisted, same rule fetchPeerServerKeyArmor
	// already follows for the transport-auth case).
	serverKeyArmor, err := h.fetchPeerServerKeyArmor(ctx, baseURL, peerServerID, peerKeyServerFingerprint)
	if err != nil {
		return nil, fmt.Errorf("fetch peer server key for verification: %w", err)
	}

	// Reject anything whose embedded identity doesn't match what we
	// asked for, and verify the peer's countersignature actually covers
	// this exact key material — a peer must not be able to hand back
	// content for a different user, or unsigned/tampered key bytes.
	_, ownerServerID, ok := parseIdentityID(identityID(key.UserID))
	if !ok || ownerServerID != peerServerID {
		return nil, fmt.Errorf("peer %s returned a key for a different server", peerServerID)
	}
	serverSigArmor := key.ServerSignature.Armor
	keyPayload := buildPublicKeyPayload(
		peerServerID, key.UserID, key.ID,
		peerKeyServerFingerprint, armor,
		key.ServerSignature.SignedAt,
	)
	if err := h.services.crypto.verifySignature(string(keyPayload), serverSigArmor, serverKeyArmor); err != nil {
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
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	var req pb.FederationCreateRequest
	if err := readRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	remoteArmor := strings.TrimSpace(req.GetRemotePublicKeyArmor())
	if remoteArmor == "" {
		writeError(w, http.StatusBadRequest, "remotePublicKeyArmor is required")
		return
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > 255 {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}

	remoteFingerprint, err := h.services.crypto.extractFingerprintFromArmor(remoteArmor)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid remote public key")
		return
	}
	if remoteFingerprint == h.signingKey.Fingerprint {
		writeError(w, http.StatusBadRequest, "Cannot create a federation invitation using this server's own public key")
		return
	}

	inviteID, err := newCryptoID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	secret, err := newInviteSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	baseURL := h.federationBaseURL()
	frontendURL := h.federationFrontendURL()
	signBytes := buildFederationInvitationPayload(
		inviteID,
		h.services.db.GetServerID(),
		baseURL,
		frontendURL,
		h.signingKey.Fingerprint,
		secret,
	)
	sig, err := h.federationSignServer(signBytes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	serverPubArmor, err := h.services.db.GetServerPublicKeyByFingerprint(r.Context(), h.signingKey.Fingerprint)
	if err != nil || serverPubArmor == "" {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	payload := federationConnectionPayload{
		InviteID:       inviteID,
		ServerID:       h.services.db.GetServerID(),
		ServerName:     h.cfg.ServerName,
		BaseURL:        baseURL,
		FrontendURL:    frontendURL,
		Fingerprint:    h.signingKey.Fingerprint,
		PublicKeyArmor: serverPubArmor,
		Signature:      sig,
		Secret:         secret,
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	connectionString, err := h.services.crypto.encrypt(plaintext, remoteArmor)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to encrypt connection payload")
		return
	}

	secretHash := cryptoHash(secret)
	now := time.Now().UTC().Truncate(time.Second)
	if err := h.services.db.InsertFederationInvitation(r.Context(), inviteID, name, caller, remoteFingerprint, remoteArmor, secretHash, connectionString, now); err != nil {
		switch {
		case errors.Is(err, errFederationInvitationExists):
			writeError(w, http.StatusConflict, "Invitation already exists")
		case errors.Is(err, errFederationInvitationDuplicateKey):
			writeError(w, http.StatusConflict, "A pending invitation for this public key already exists")
		case errors.Is(err, errFederationInvitationDuplicateName):
			writeError(w, http.StatusConflict, "A pending invitation with this name already exists")
		default:
			writeError(w, http.StatusInternalServerError, "Internal Server Error")
		}
		return
	}

	writeResponse(w, http.StatusCreated, &pb.FederationCreateResponse{
		InviteId:         inviteID,
		ConnectionString: connectionString,
		Status:           federationStatusNew,
	})
}

func (h *Handlers) ListFederationInvitations(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	rows, err := h.services.db.ListFederationInvitations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	out := &pb.FederationInvitationList{}
	for _, row := range rows {
		out.Invitations = append(out.Invitations, federationInvitationRowToWire(row))
	}
	writeResponse(w, http.StatusOK, out)
}

func federationInvitationRowToWire(row federationInvitationListRow) *pb.FederationInvitation {
	item := &pb.FederationInvitation{
		InviteId:          row.ID,
		Name:              row.Name,
		Status:            row.Status,
		CreatedBy:         row.CreatedBy,
		RemoteFingerprint: row.Fingerprint,
		CreatedAt:         unixOrZero(row.CreatedAt),
		AcceptedAt:        unixPtr(row.AcceptedAt),
		ReviewedAt:        unixPtr(row.ReviewedAt),
	}
	if row.ServerID != "" {
		item.ServerId = &row.ServerID
	}
	if row.ReviewedBy != "" {
		item.ReviewedBy = &row.ReviewedBy
	}
	if row.Status == federationStatusNew && row.ConnectionCiphertext != "" {
		item.ConnectionString = &row.ConnectionCiphertext
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
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	rows, err := h.services.db.ListFederationServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	out := &pb.FederationServerList{}
	for _, row := range rows {
		out.Servers = append(out.Servers, federationServerRowToWire(row))
	}
	writeResponse(w, http.StatusOK, out)
}

// federationServerRowToWire converts one servers row to its wire shape,
// shared by ListFederationServers and GetFederationList.
func federationServerRowToWire(row federationServerListRow) *pb.FederationServer {
	wire := &pb.FederationServer{
		ServerId:          row.ID,
		Name:              row.Name,
		BaseUrl:           row.BaseURL,
		FrontendUrl:       row.FrontendURL,
		Connected:         row.Connected,
		Established:       row.Established,
		CreatedAt:         unixOrZero(row.CreatedAt),
		Revoked:           row.Revoked,
		DisconnectPending: row.DisconnectPending,
	}
	if row.Revoked {
		wire.RevokedAt = unixPtr(row.RevokedAt)
		if row.RevokedBy != "" {
			wire.RevokedBy = &row.RevokedBy
		}
		if row.RevokedReason != "" {
			wire.RevokedReason = &row.RevokedReason
		}
	}
	if row.DisconnectPending {
		wire.DisconnectRequestedAt = unixPtr(row.DisconnectRequestedAt)
		if row.DisconnectRequestedBy != "" {
			wire.DisconnectRequestedBy = &row.DisconnectRequestedBy
		}
		if row.DisconnectReason != "" {
			wire.DisconnectReason = &row.DisconnectReason
		}
	}
	return wire
}

// GetFederationServerLogs returns federation_log lines for one peer server,
// one line per entry — the mesh tab's per-server drill-down (tap
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
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	serverID := mux.Vars(r)["id"]
	if serverID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	var sb strings.Builder

	// Every servers row has a backing attempt (ApproveFederationAttempt
	// backfills federation_attempt.server_id), but treat a miss as "no
	// attempt info" rather than an error.
	attempt, err := h.services.db.GetFederationAttemptForServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if attempt != nil {
		attemptRows, err := h.services.db.ListFederationAttemptLogs(r.Context(), attempt.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		writeFederationLogLines(&sb, attemptRows)
	}

	serverRows, err := h.services.db.ListFederationServerLogs(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	writeFederationLogLines(&sb, serverRows)

	writeResponse(w, http.StatusOK, &pb.FederationLogs{Text: sb.String()})
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
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	serverID := mux.Vars(r)["id"]
	if serverID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	inv, err := h.services.db.GetFederationInvitationForServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if inv == nil {
		writeResponse(w, http.StatusOK, &pb.FederationInvitationResponse{})
		return
	}
	writeResponse(w, http.StatusOK, &pb.FederationInvitationResponse{Invitation: federationInvitationRowToWire(*inv)})
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
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	serverID := mux.Vars(r)["id"]
	if serverID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	attempt, err := h.services.db.GetFederationAttemptForServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if attempt == nil {
		writeResponse(w, http.StatusOK, &pb.FederationAttemptResponse{})
		return
	}
	writeResponse(w, http.StatusOK, &pb.FederationAttemptResponse{Attempt: federationAttemptRowToWire(*attempt)})
}

func federationAttemptRowToWire(row federationAttemptRow) *pb.FederationAttempt {
	item := &pb.FederationAttempt{
		AttemptId:        row.ID,
		RemoteServerId:   row.RemoteServerID,
		RemoteServerName: row.RemoteServerName,
		BaseUrl:          row.BaseURL,
		FrontendUrl:      row.FrontendURL,
		Fingerprint:      row.Fingerprint,
		CreatedAt:        unixOrZero(row.CreatedAt),
		Status:           row.Status,
		ApprovedAt:       unixPtr(row.ApprovedAt),
		RejectedAt:       unixPtr(row.RejectedAt),
	}
	if row.InvitationID != "" {
		item.InvitationId = &row.InvitationID
	}
	if row.ServerID != "" {
		item.ServerId = &row.ServerID
	}
	if row.ApprovedBy != "" {
		item.ApprovedBy = &row.ApprovedBy
	}
	if row.RejectedBy != "" {
		item.RejectedBy = &row.RejectedBy
	}
	if row.RejectedReason != "" {
		item.RejectedReason = &row.RejectedReason
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
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	invRows, err := h.services.db.ListFederationInvitations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	attemptRows, err := h.services.db.ListFederationAttempts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	serverRows, err := h.services.db.ListFederationServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	out := &pb.FederationList{}
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
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	attemptID := strings.TrimSpace(mux.Vars(r)["id"])
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	attempt, err := h.services.db.GetFederationAttempt(r.Context(), attemptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if attempt == nil {
		writeError(w, http.StatusNotFound, "Attempt not found")
		return
	}
	writeResponse(w, http.StatusOK, federationAttemptRowToWire(*attempt))
}

// GetFederationAttemptLogs returns federation_log lines for one attempt as
// one line per entry — see GetFederationServerLogs's doc comment.
func (h *Handlers) GetFederationAttemptLogs(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	attemptID := strings.TrimSpace(mux.Vars(r)["id"])
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	rows, err := h.services.db.ListFederationAttemptLogs(r.Context(), attemptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	var sb strings.Builder
	writeFederationLogLines(&sb, rows)

	writeResponse(w, http.StatusOK, &pb.FederationLogs{Text: sb.String()})
}

// ApproveFederationAttempt creates the servers row — see
// ApproveFederationAttempt's (DataService) doc comment.
func (h *Handlers) ApproveFederationAttempt(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	callerIsRoot, err := h.isRoot(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	attemptID := strings.TrimSpace(mux.Vars(r)["id"])
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	serverID, established, err := h.services.db.ApproveFederationAttempt(r.Context(), attemptID, caller, time.Now().UTC().Truncate(time.Second), callerIsRoot, h.countersign, h.notifyPeerOfApproval)
	switch {
	case errors.Is(err, errFederationAttemptNotFound):
		writeError(w, http.StatusNotFound, "Attempt not found")
	case errors.Is(err, errFederationAttemptNotPending):
		writeError(w, http.StatusConflict, "Attempt already decided")
	case errors.Is(err, errFederationSameApprover):
		writeError(w, http.StatusForbidden, "A different admin must approve this connection")
	case errors.Is(err, errFederationPeerUnreachable):
		h.services.log.GetLogger(r.Context()).Warn().Err(err).Str("attemptId", attemptID).Msg("federation attempt approve could not reach peer")
		h.logFederationAttemptAsync(attemptID, federationLogError, fmt.Sprintf("Approval by %s failed: %v", caller, err))
		writeError(w, http.StatusBadGateway,
			"Couldn't reach the other server, so the connection wasn't approved. Try again, or contact its admin.")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("attemptId", attemptID).Msg("federation attempt approve failed")
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		h.logFederationAttemptAsync(attemptID, federationLogInfo,
			fmt.Sprintf("Approved by %s", caller))
		writeResponse(w, http.StatusOK, &pb.FederationActionResponse{AttemptId: attemptID, ServerId: serverID, Status: "approved", Established: established})
	}
}

// RejectFederationAttempt requires a non-empty reason. Unlike the old
// servers-row-based reject, the attempt row (and its logs) are never
// deleted — status just flips to rejected.
func (h *Handlers) RejectFederationAttempt(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	attemptID := strings.TrimSpace(mux.Vars(r)["id"])
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	var body pb.FederationReasonRequest
	if err := readRequest(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	reason := strings.TrimSpace(body.GetReason())
	if reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}

	err = h.services.db.RejectFederationAttempt(r.Context(), attemptID, caller, reason, time.Now().UTC().Truncate(time.Second))
	switch {
	case errors.Is(err, errFederationAttemptNotFound):
		writeError(w, http.StatusNotFound, "Attempt not found")
	case errors.Is(err, errFederationAttemptNotPending):
		writeError(w, http.StatusConflict, "Attempt already decided")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("attemptId", attemptID).Msg("federation attempt reject failed")
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		h.logFederationAttemptAsync(attemptID, federationLogError,
			fmt.Sprintf("Rejected by %s: %s", caller, reason))
		writeResponse(w, http.StatusOK, &pb.FederationActionResponse{AttemptId: attemptID, Status: "rejected"})
	}
}

// RequestFederationServerDisconnect stages a disconnect; any admin may
// request one, with a required reason. The peer stays trusted until a
// second admin confirms via ConfirmFederationServerDisconnect.
func (h *Handlers) RequestFederationServerDisconnect(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	serverID := strings.TrimSpace(mux.Vars(r)["id"])
	if serverID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	var body pb.FederationReasonRequest
	if err := readRequest(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	reason := strings.TrimSpace(body.GetReason())
	if reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}

	err = h.services.db.RequestFederationServerDisconnect(r.Context(), serverID, caller, reason, time.Now().UTC().Truncate(time.Second))
	switch {
	case errors.Is(err, errFederationServerNotFound):
		writeError(w, http.StatusNotFound, "Server not found")
	case errors.Is(err, errFederationServerAlreadyRevoked):
		writeError(w, http.StatusConflict, "Server already revoked")
	case errors.Is(err, errFederationDisconnectAlreadyRequested):
		writeError(w, http.StatusConflict, "Disconnect already requested for this server")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("serverId", serverID).Msg("federation server disconnect request failed")
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		h.logFederationServerAsync(serverID, federationLogInfo,
			fmt.Sprintf("Disconnect requested by %s: %s", caller, reason))
		writeResponse(w, http.StatusOK, &pb.FederationActionResponse{ServerId: serverID, Status: "disconnect_pending"})
	}
}

// ConfirmFederationServerDisconnect finalizes a staged disconnect,
// requiring the confirming admin to differ from the requester (root
// exempt). This is the point the peer is actually revoked and notified.
func (h *Handlers) ConfirmFederationServerDisconnect(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	callerIsRoot, err := h.isRoot(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	serverID := strings.TrimSpace(mux.Vars(r)["id"])
	if serverID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	reason, err := h.services.db.ConfirmFederationServerDisconnect(r.Context(), serverID, caller, time.Now().UTC().Truncate(time.Second), callerIsRoot)
	switch {
	case errors.Is(err, errFederationServerNotFound):
		writeError(w, http.StatusNotFound, "Server not found")
	case errors.Is(err, errFederationDisconnectNotRequested):
		writeError(w, http.StatusConflict, "No disconnect request is pending for this server")
	case errors.Is(err, errFederationSameApprover):
		writeError(w, http.StatusForbidden, "A different admin must confirm this disconnect")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("serverId", serverID).Msg("federation server disconnect confirm failed")
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		h.logFederationServerAsync(serverID, federationLogError,
			fmt.Sprintf("Disconnected by %s (confirmed): %s", caller, reason))
		if h.realtimeRelay != nil {
			h.realtimeRelay.forgetPeer(r.Context(), serverID)
		}
		if err := h.services.db.DeletePeerStreams(r.Context(), serverID); err != nil {
			h.services.log.GetLogger(r.Context()).Error().Err(err).Str("serverId", serverID).Msg("failed to drop delivery streams of revoked peer")
		}
		if err := h.services.db.DeleteVouchReferencesForServer(r.Context(), serverID); err != nil {
			h.services.log.GetLogger(r.Context()).Error().Err(err).Str("serverId", serverID).Msg("failed to drop vouch references of revoked peer")
		}
		go func() {
			if err := h.notifyPeerOfDisconnect(context.Background(), serverID, reason); err != nil {
				h.services.log.GetLogger(context.Background()).Warn().Err(err).Str("serverId", serverID).Msg("failed to notify peer of disconnect")
			}
		}()
		writeResponse(w, http.StatusOK, &pb.FederationActionResponse{ServerId: serverID, Status: "revoked"})
	}
}

// CancelFederationServerDisconnect withdraws a staged disconnect request
// before a second admin confirms it. Any admin may cancel — same
// visibility as requesting.
func (h *Handlers) CancelFederationServerDisconnect(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	serverID := strings.TrimSpace(mux.Vars(r)["id"])
	if serverID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	err = h.services.db.CancelFederationServerDisconnect(r.Context(), serverID)
	switch {
	case errors.Is(err, errFederationDisconnectNotRequested):
		writeError(w, http.StatusConflict, "No disconnect request is pending for this server")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("serverId", serverID).Msg("federation server disconnect cancel failed")
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		h.logFederationServerAsync(serverID, federationLogInfo,
			fmt.Sprintf("Disconnect request cancelled by %s", caller))
		writeResponse(w, http.StatusOK, &pb.FederationActionResponse{ServerId: serverID, Status: "disconnect_cancelled"})
	}
}

// PurgeFederationServer permanently deletes a disconnected peer's row
// and every local reed/identity it owns. Root only — this is
// irreversible and goes further than a normal admin disconnect.
func (h *Handlers) PurgeFederationServer(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	root, err := h.isRoot(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !root {
		writeError(w, http.StatusForbidden, "Root required")
		return
	}

	serverID := strings.TrimSpace(mux.Vars(r)["id"])
	if serverID == "" {
		writeError(w, http.StatusBadRequest, "Argument `id` is required")
		return
	}

	err = h.services.db.PurgeFederationServer(r.Context(), serverID)
	switch {
	case errors.Is(err, errFederationServerNotFound):
		writeError(w, http.StatusNotFound, "Server not found")
	case errors.Is(err, errFederationServerNotRevoked):
		writeError(w, http.StatusConflict, "Server must be disconnected before it can be deleted")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("serverId", serverID).Msg("federation server purge failed")
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		writeResponse(w, http.StatusOK, &pb.FederationActionResponse{ServerId: serverID, Status: "purged"})
	}
}

// GetFederationUserIdentity is the IdP endpoint an established peer calls
// to resolve one of THIS server's users. Peer-authenticated; userID must
// be local — a peer can't vouch for a third server's user.
func (h *Handlers) GetFederationUserIdentity(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	peerServerID, ok := r.Context().Value(peerServerIDKey).(string)
	if !ok || peerServerID == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	userID := strings.TrimSpace(mux.Vars(r)["userID"])
	if userID == "" {
		writeError(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}
	if !strings.HasSuffix(userID, "@"+h.services.db.GetServerID()) {
		writeError(w, http.StatusBadRequest, "userID must be local to this server")
		return
	}

	removal, err := h.services.db.GetAccountRemoval(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading account removal")
		internalServerError(w)
		return
	}
	if removal != nil {
		writeAccountGone(w, h.accountRemovalWire(removal))
		return
	}

	user, err := h.services.db.GetUserProfile(r.Context(), userID)
	if err != nil {
		log.Error().Str("userID", userID).Str("peerServerId", peerServerID).Err(err).Msg("Error getting user profile for peer")
		internalServerError(w)
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	fingerprint, err := h.services.db.GetActiveKeyFingerprint(r.Context(), userID)
	if err != nil {
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, &pb.FederationUserIdentity{
		User:        pbUser(user),
		ActiveKeyId: fingerprint,
	})
}

func (h *Handlers) RevokeFederationInvitation(w http.ResponseWriter, r *http.Request) {
	caller, authed := r.Context().Value(userIDKey).(string)
	if !authed || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	id := strings.TrimSpace(mux.Vars(r)["id"])
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	err = h.services.db.RevokeFederationInvitation(r.Context(), id, caller, time.Now().UTC().Truncate(time.Second))
	switch {
	case errors.Is(err, errFederationInvitationNotFound):
		writeError(w, http.StatusNotFound, "Invitation not found")
	case errors.Is(err, errFederationInvitationNotRevocable):
		writeError(w, http.StatusBadRequest, "Invitation cannot be revoked")
	case err != nil:
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("id", id).Msg("federation invitation revoke failed")
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		writeResponse(w, http.StatusOK, &pb.FederationActionResponse{InviteId: id, Status: federationStatusCanceled})
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
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	var req pb.FederationConnectRequest
	if err := readRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.ServerId = strings.TrimSpace(req.ServerId)
	req.BaseUrl = strings.TrimSpace(req.BaseUrl)
	req.FrontendUrl = strings.TrimSpace(req.FrontendUrl)
	req.Fingerprint = strings.TrimSpace(req.Fingerprint)
	req.Secret = strings.TrimSpace(req.Secret)
	if req.ServerId == "" || req.BaseUrl == "" || req.FrontendUrl == "" || req.Fingerprint == "" ||
		req.Signature == "" || req.Secret == "" {
		writeError(w, http.StatusBadRequest, "Missing required fields")
		return
	}
	if !h.federationURLAllowed(req.BaseUrl) || !h.federationURLAllowed(req.FrontendUrl) {
		writeError(w, http.StatusBadRequest, "baseUrl and frontendUrl must be https")
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
		writeError(w, http.StatusNotFound, "Invitation not found")
		return
	}

	h.logFederationInvitationAsync(inviteID, federationLogInfo,
		fmt.Sprintf("Incoming connect attempt from server %s (%s)", req.ServerId, req.BaseUrl))

	if inv.Status != federationStatusNew {
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: invitation is not new")
		writeError(w, http.StatusConflict, "Invitation is not new")
		return
	}

	if subtle.ConstantTimeCompare(cryptoHash(req.Secret), inv.SecretHash) != 1 {
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: invalid secret")
		writeError(w, http.StatusForbidden, "Invalid secret")
		return
	}
	// req.Fingerprint must be the exact key A's admin pasted when creating
	// this invitation — the connection string was encrypted to it, so only
	// the holder of the matching private key could have decrypted the
	// secret in the first place. This also pins which key we verify the
	// signature against, instead of trusting a self-reported fingerprint.
	if req.Fingerprint != inv.Fingerprint {
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: fingerprint does not match invitation")
		writeError(w, http.StatusForbidden, "Fingerprint does not match invitation")
		return
	}

	signBytes := buildFederationConnectPayload(inviteID, req.ServerId, req.BaseUrl, req.FrontendUrl, req.Fingerprint)
	sigArmor := req.Signature
	if err := h.services.crypto.verifyDetachedSignature(string(signBytes), sigArmor, inv.PublicKey); err != nil {
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: invalid signature")
		writeError(w, http.StatusBadRequest, "Invalid signature")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	attemptID, err := h.services.db.MarkFederationInvitationAccepted(r.Context(), inviteID, federationPeer{
		ServerID:    req.ServerId,
		ServerName:  req.ServerName,
		BaseURL:     req.BaseUrl,
		FrontendURL: req.FrontendUrl,
		Fingerprint: req.Fingerprint,
	}, now)
	switch {
	case errors.Is(err, errFederationInvitationNotFound):
		writeError(w, http.StatusNotFound, "Invitation not found")
	case errors.Is(err, errFederationInvitationNotNew):
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: invitation is not new")
		writeError(w, http.StatusConflict, "Invitation is not new")
	case errors.Is(err, errFederationServerAlreadyKnown):
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: server already known")
		writeError(w, http.StatusConflict, "A federation attempt or connection with this server already exists")
	case errors.Is(err, errFederationKeyAlreadyKnown):
		h.logFederationInvitationAsync(inviteID, federationLogError, "Rejected connect attempt: public key already known")
		writeError(w, http.StatusConflict, "This server's public key is already on record")
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
			fmt.Sprintf("Handshake verified with server %s (%s); awaiting approval", req.ServerId, req.BaseUrl))
		writeResponse(w, http.StatusOK, &pb.FederationConnectResponse{Status: federationStatusAccepted, ServerId: req.ServerId})
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
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	admin, err := h.isAdmin(r.Context(), caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !admin {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}

	var req pb.FederationAttemptRequest
	if err := readRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	connectionString := strings.TrimSpace(req.GetConnectionString())
	if connectionString == "" {
		writeError(w, http.StatusBadRequest, "connectionString is required")
		return
	}

	plaintext, err := h.services.crypto.decrypt(connectionString, h.signingKey.Armor)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to decrypt connection string")
		return
	}
	var payload federationConnectionPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid connection payload")
		return
	}
	if payload.InviteID == "" || payload.ServerID == "" || payload.BaseURL == "" || payload.FrontendURL == "" ||
		payload.Fingerprint == "" || payload.PublicKeyArmor == "" || payload.Signature == "" || payload.Secret == "" {
		writeError(w, http.StatusBadRequest, "Incomplete connection payload")
		return
	}
	if !h.federationURLAllowed(payload.BaseURL) || !h.federationURLAllowed(payload.FrontendURL) {
		writeError(w, http.StatusBadRequest, "baseUrl and frontendUrl must be https")
		return
	}

	remoteFingerprint, err := h.services.crypto.extractFingerprintFromArmor(payload.PublicKeyArmor)
	if err != nil || remoteFingerprint != payload.Fingerprint {
		writeError(w, http.StatusBadRequest, "Public key does not match claimed fingerprint")
		return
	}
	initiatorSignBytes := buildFederationInvitationPayload(
		payload.InviteID, payload.ServerID, payload.BaseURL, payload.FrontendURL, payload.Fingerprint, payload.Secret,
	)
	initiatorSigArmor := payload.Signature
	if err := h.services.crypto.verifyDetachedSignature(string(initiatorSignBytes), initiatorSigArmor, payload.PublicKeyArmor); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid initiator signature")
		return
	}

	// Create a pending federation_attempt before attempting the handshake —
	// so there's somewhere to log against even if the next steps fail,
	// instead of writing nothing until success.
	now := time.Now().UTC().Truncate(time.Second)
	peer := federationPeer{
		ServerID:       payload.ServerID,
		ServerName:     payload.ServerName,
		BaseURL:        payload.BaseURL,
		FrontendURL:    payload.FrontendURL,
		Fingerprint:    payload.Fingerprint,
		PublicKeyArmor: payload.PublicKeyArmor,
	}
	attemptID, err := h.services.db.CreateFederationAttempt(r.Context(), peer, now)
	switch {
	case errors.Is(err, errFederationServerAlreadyKnown):
		writeError(w, http.StatusConflict, "A federation attempt or connection with this server already exists")
		return
	case errors.Is(err, errFederationKeyAlreadyKnown):
		writeError(w, http.StatusConflict, "This server's public key is already on record")
		return
	case err != nil:
		log.Error().Err(err).Msg("failed to record federation attempt")
		internalServerError(w)
		return
	}
	h.logFederationAttemptAsync(attemptID, federationLogInfo,
		fmt.Sprintf("Attempting to redeem invitation from server %s (%s)", payload.ServerID, payload.BaseURL))

	localBaseURL := h.federationBaseURL()
	localFrontendURL := h.federationFrontendURL()
	localServerID := h.services.db.GetServerID()
	connectSignBytes := buildFederationConnectPayload(payload.InviteID, localServerID, localBaseURL, localFrontendURL, h.signingKey.Fingerprint)
	connectSig, err := h.federationSignServer(connectSignBytes)
	if err != nil {
		internalServerError(w)
		return
	}

	connectBody, err := proto.Marshal(&pb.FederationConnectRequest{
		ServerId:    localServerID,
		ServerName:  h.cfg.ServerName,
		BaseUrl:     localBaseURL,
		FrontendUrl: localFrontendURL,
		Fingerprint: h.signingKey.Fingerprint,
		Signature:   connectSig,
		Secret:      payload.Secret,
	})
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
	httpReq.Header.Set("Content-Type", protobufContentType)
	resp, err := h.federationHTTPClient().Do(httpReq)
	if err != nil {
		log.Error().Err(err).Str("connectURL", connectURL).Msg("federation connect callback failed")
		h.logFederationAttemptAsync(attemptID, federationLogError,
			fmt.Sprintf("Failed to reach %s (%s): %s", payload.ServerID, payload.BaseURL, err.Error()))
		writeError(w, http.StatusBadGateway, "Failed to reach initiator server")
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Info().Int("status", resp.StatusCode).Str("connectURL", connectURL).Msg("federation connect callback rejected")
		h.logFederationAttemptAsync(attemptID, federationLogError,
			fmt.Sprintf("Rejected by %s (%s): %s", payload.ServerID, payload.BaseURL, string(respBody)))
		writeError(w, resp.StatusCode, peerErrorMessage(respBody))
		return
	}
	var connectResp pb.FederationConnectResponse
	if err := proto.Unmarshal(respBody, &connectResp); err != nil {
		internalServerError(w)
		return
	}

	// No servers row yet: the handshake having verified is not the same as
	// a second admin having approved the connection — see
	// ApproveFederationAttempt.
	h.logFederationAttemptAsync(attemptID, federationLogInfo,
		fmt.Sprintf("Handshake verified with server %s (%s); awaiting approval", payload.ServerID, payload.BaseURL))

	writeResponse(w, http.StatusOK, &pb.FederationAttemptStatus{Status: federationStatusAccepted, ServerId: payload.ServerID})
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
		writeAccountGone(w, h.accountRemovalWire(result.AccountRemoval))
		return false
	}
	if result.ReedRemoval != nil {
		writeReedGone(w, h.reedRemovalWire(result.ReedRemoval))
		return false
	}
	if result.ThreadRemoval != nil {
		writeThreadGone(w, result.ThreadRemoval)
		return false
	}
	if result.Reed == nil {
		writeError(w, http.StatusNotFound, "Post not found")
		return false
	}
	blank, err := h.services.db.IsBlankEcho(r.Context(), reedID)
	if err != nil && !errors.Is(err, ErrReedNotFound) {
		internalServerError(w)
		return false
	}
	if blank {
		writeError(w, http.StatusBadRequest, "Empty echoes have no ripples — use the original reed instead")
		return false
	}
	return true
}

type postRippleRequest struct {
	Content       string  `json:"content"`
	ThreadID      string  `json:"threadID"`
	ReplyingTo    *string `json:"replyingTo"`
	KeyID         string  `json:"keyID"`
	UserSignature string  `json:"userSignature"`
	// UserID is the acting user's canonical id — only read when the
	// request arrives via peer relay (see resolveActingUser); a local
	// caller's own session already provides this.
	UserID string `json:"userID"`
}

// PostRipple handles POST /api/reeds/{userID}/{reedID}/ripples. Only a
// holder of the parent reed may post (checkReedHolder). Verifies the user
// signature, then countersigns via DataService.PostRipple.
func (h *Handlers) PostRipple(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	reedUserID := mux.Vars(r)["userID"]
	reedID := mux.Vars(r)["reedID"]
	if reedUserID == "" || reedID == "" {
		writeError(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	canonicalReedID := string(appendEntity(identityID(reedUserID), reedID))
	if viewerID := h.requestViewer(r); viewerID != "" && h.refuseBlockedRequester(w, r, reedUserID, viewerID) {
		return
	}

	// A foreign reed's home server checks this server as the holder, so
	// this server checks its own user before proxying.
	if _, foreign := h.foreignServerOf(canonicalReedID); foreign && !h.checkReedHolder(w, r, canonicalReedID) {
		return
	}
	if handled, _ := h.proxyIfForeign(w, r, canonicalReedID); handled {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var msg pb.PostRippleRequest
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req := postRippleRequest{
		Content:       msg.GetContent(),
		ThreadID:      msg.GetThreadId(),
		ReplyingTo:    msg.ReplyingTo,
		KeyID:         msg.GetKeyId(),
		UserSignature: msg.GetUserSignature(),
		UserID:        msg.GetUserId(),
	}
	callerID, ok := h.resolveActingUser(r, req.UserID)
	if !ok {
		writeError(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}
	if h.refuseBlockedRequester(w, r, reedUserID, callerID) {
		return
	}

	// Parent check first: it's what tells a removed reed (410 + cert) from
	// one that never existed (404).
	if !h.checkRippleParentReed(w, r, canonicalReedID) {
		return
	}
	if !h.checkReedHolder(w, r, canonicalReedID) {
		return
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "Comment cannot be empty")
		return
	}
	if utf8.RuneCountInString(content) > MaxRippleContentChars {
		writeError(w, http.StatusBadRequest, "Comment is too long (max 140 characters)")
		return
	}

	if _, err := uuid.Parse(req.ThreadID); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid threadID")
		return
	}

	if req.ReplyingTo != nil {
		target, err := h.services.db.GetRipple(r.Context(), *req.ReplyingTo)
		if errors.Is(err, ErrRippleNotFound) {
			writeError(w, http.StatusBadRequest, "Cannot reply to a comment that doesn't exist")
			return
		}
		if err != nil {
			internalServerError(w)
			return
		}
		if target.ReedID != canonicalReedID {
			writeError(w, http.StatusBadRequest, "Cannot reply to a comment on a different post")
			return
		}
		if target.ThreadID != req.ThreadID {
			writeError(w, http.StatusBadRequest, "Reply must use the same thread as the comment it replies to.")
			return
		}
	}

	if req.KeyID == "" || req.UserSignature == "" {
		writeError(w, http.StatusBadRequest, "Arguments `keyID` and `userSignature` are required")
		return
	}
	pubKey, err := h.resolvePublicKey(r.Context(), req.KeyID)
	if err != nil {
		log.Error().Str("userID", callerID).Str("keyID", req.KeyID).Err(err).Msg("Error loading public key")
		internalServerError(w)
		return
	}
	if pubKey == nil || pubKey.Revoked {
		writeError(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	userSigArmor := req.UserSignature
	replyingToVal := ""
	if req.ReplyingTo != nil {
		replyingToVal = *req.ReplyingTo
	}
	userPayload := buildRippleUserPayload(
		canonicalReedID, callerID, req.KeyID, req.ThreadID, replyingToVal, content,
	)
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, pubKey.Armor); err != nil {
		log.Error().Str("userID", callerID).Str("reedID", canonicalReedID).Err(err).Msg("ripple signature verification failed")
		writeError(w, http.StatusBadRequest, "Invalid signature.")
		return
	}

	resp, err := h.services.db.PostRipple(
		r.Context(), canonicalReedID, callerID, content, req.ThreadID, req.ReplyingTo,
		req.KeyID, req.UserSignature, h.countersign, time.Now(),
	)
	if errors.Is(err, ErrRippleThreadMismatch) {
		writeError(w, http.StatusBadRequest, "Reply must use the same thread as the comment it replies to.")
		return
	}
	if err != nil {
		log.Error().Str("userID", reedUserID).Str("reedID", reedID).Err(err).Msg("Error posting ripple")
		internalServerError(w)
		return
	}

	wire := rippleWire(resp)
	writeResponse(w, http.StatusCreated, pbRipple(wire))

	h.broadcastChan <- realtimeBroadcastMessage{
		Type:                 realtimeRipplePosted,
		UserID:               reedUserID,
		ReedID:               reedID,
		Ripple:               &wire,
		RippleParentAuthorID: h.localRippleAuthor(r.Context(), req.ReplyingTo),
	}
}

// localRippleAuthor returns the author of the ripple being replied to when
// they're a user of this server, so they can be told about the reply.
func (h *Handlers) localRippleAuthor(ctx context.Context, rippleID *string) string {
	if rippleID == nil {
		return ""
	}
	parent, err := h.services.db.GetRipple(ctx, *rippleID)
	if err != nil {
		return ""
	}
	if _, serverID, ok := parseIdentityID(identityID(parent.UserID)); !ok || serverID != h.services.db.GetServerID() {
		return ""
	}
	return parent.UserID
}

// checkReedHolder lets only holders of the reed see or join its ripples:
// a user must hold it on their own server; a peer proxying for one of its
// users must hold it as a server, vouching for that user.
func (h *Handlers) checkReedHolder(w http.ResponseWriter, r *http.Request, reedID string) (ok bool) {
	ctx := r.Context()
	var holds bool
	var err error
	if userID, isUser := ctx.Value(userIDKey).(string); isUser {
		holds, err = h.services.db.IsReedHolder(ctx, reedID, userID)
	} else if peerServerID, isPeer := ctx.Value(peerServerIDKey).(string); isPeer {
		holds, err = h.services.db.IsServerHolder(ctx, reedID, peerServerID)
	} else {
		writeError(w, http.StatusUnauthorized, "Could not resolve caller")
		return false
	}
	if err != nil {
		internalServerError(w)
		return false
	}
	if !holds {
		writeError(w, http.StatusForbidden, "You don't hold this post")
		return false
	}
	return true
}

// GetRipples handles GET /api/reeds/{userID}/{reedID}/ripples. Only a
// holder of the parent reed may list them (checkReedHolder).
func (h *Handlers) GetRipples(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	reedUserID := mux.Vars(r)["userID"]
	reedID := mux.Vars(r)["reedID"]
	if reedUserID == "" || reedID == "" {
		writeError(w, http.StatusBadRequest, "Arguments `userID` and `reedID` are required")
		return
	}
	canonicalReedID := string(appendEntity(identityID(reedUserID), reedID))

	if h.refuseIfBlocked(w, r, reedUserID) {
		return
	}

	// A foreign reed's home server checks this server as the holder, so
	// this server checks its own user before proxying.
	if _, foreign := h.foreignServerOf(canonicalReedID); foreign && !h.checkReedHolder(w, r, canonicalReedID) {
		return
	}
	if handled, _ := h.proxyIfForeign(w, r, canonicalReedID); handled {
		return
	}

	// Parent check first: it's what tells a removed reed (410 + cert) from
	// one that never existed (404).
	if !h.checkRippleParentReed(w, r, canonicalReedID) {
		return
	}
	if !h.checkReedHolder(w, r, canonicalReedID) {
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	before := strings.TrimSpace(r.URL.Query().Get("before"))
	if before != "" {
		if _, err := decodeRippleCursor(before); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid before cursor")
			return
		}
	}

	list, err := h.services.db.ListRipples(r.Context(), canonicalReedID, limit, before)
	if err != nil {
		log.Error().Str("userID", reedUserID).Str("reedID", reedID).Err(err).Msg("Error listing ripples")
		internalServerError(w)
		return
	}

	lastActivityAt, err := h.services.db.GetRipplesLastActivityAt(r.Context(), canonicalReedID)
	if err != nil {
		internalServerError(w)
		return
	}

	resp := &pb.RippleListResponse{
		HasMore:        list.HasMore,
		NextCursor:     list.NextCursor,
		LastActivityAt: unixOrZero(lastActivityAt),
	}
	for i := range list.Ripples {
		resp.Responses = append(resp.Responses, pbRipple(rippleWire(&list.Ripples[i])))
	}
	writeResponse(w, http.StatusOK, resp)
}

// DeleteRipple handles DELETE /api/reeds/{reedID}/ripples/{rippleID}. Routed
// by reed rather than by the bare ripple hash so it can proxy to the reed's
// true home server (see proxyIfForeign) the same way PostRipple already
// does — ripples are never mirrored locally, so the home server is the only
// place a remote reed's ripple row exists.
func (h *Handlers) DeleteRipple(w http.ResponseWriter, r *http.Request) {
	reedID := mux.Vars(r)["reedID"]
	rippleID := mux.Vars(r)["rippleID"]
	if reedID == "" || rippleID == "" {
		writeError(w, http.StatusBadRequest, "Arguments `reedID` and `rippleID` are required")
		return
	}

	if handled, _ := h.proxyIfForeign(w, r, reedID); handled {
		return
	}

	var req pb.DeleteRippleRequest
	if err := readRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	callerID, ok := h.resolveActingUser(r, req.GetUserId())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Could not resolve acting user")
		return
	}

	found, owned, err := h.services.db.SoftDeleteRipple(r.Context(), rippleID, callerID)
	if err != nil {
		internalServerError(w)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "Comment not found")
		return
	}
	if !owned {
		writeError(w, http.StatusForbidden, "You can only delete your own comments.")
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
	_, _, bareReedID, ok := parseKeyFingerprint(identityID(tombstoned.ReedID))
	if !ok {
		h.services.log.GetLogger(r.Context()).Error().Str("reedID", tombstoned.ReedID).Msg("Malformed reed id on tombstoned ripple")
		return
	}
	h.broadcastChan <- realtimeBroadcastMessage{
		Type:   realtimeRippleUpdated,
		UserID: tombstoned.ReedAuthorID,
		ReedID: bareReedID,
		Ripple: &wire,
	}
}

// GetReceivedRipples handles GET /ripples: the caller's ripples inbox —
// every response on a reed they own, on any reed hosted on this server.
func (h *Handlers) GetReceivedRipples(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())

	userID := h.getUserID(r)

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "Invalid limit")
			return
		}
		limit = n
	}

	before := strings.TrimSpace(r.URL.Query().Get("before"))
	if before != "" {
		if _, err := decodeReceivedRippleCursor(before); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid before cursor")
			return
		}
	}

	list, err := h.services.db.ListReceivedRipples(r.Context(), userID, limit, before)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error listing received ripples")
		internalServerError(w)
		return
	}

	out := &pb.ReceivedRippleListResponse{HasMore: list.HasMore, NextCursor: list.NextCursor}
	for i := range list.Ripples {
		rr := &list.Ripples[i]
		out.Ripples = append(out.Ripples, &pb.ReceivedRipple{
			Ripple:       pbRipple(rippleWire(&rr.Ripple)),
			ReedId:       rr.ReedID,
			ReedAuthorId: rr.ReedAuthorID,
		})
	}
	writeResponse(w, http.StatusOK, out)
}

// federationFrontendURL is where this server's users open links
// (ALLOWED_ORIGIN), told to peers in the handshake.
func (h *Handlers) federationFrontendURL() string {
	return frontendURLFromOrigin(h.cfg.AllowedOrigin)
}

// federationURLAllowed applies the handshake's https rule to an address a
// peer sent, relaxed only for local development.
func (h *Handlers) federationURLAllowed(u string) bool {
	return strings.HasPrefix(u, "https://") || h.cfg.FederationAllowInsecureHTTP
}

func (h *Handlers) federationBaseURL() string {
	return strings.TrimRight(string(h.cfg.APIBaseURL), "/")
}

func (h *Handlers) SendMailboxMessage(ctx context.Context, userID string, category MailboxCategory, kind, message, link, senderUserID string, meta any) error {
	id, ciphertext, err := h.services.db.SendMailboxMessage(ctx, h.services.crypto, userID, category, kind, message, link, senderUserID, meta)
	if err != nil {
		return err
	}
	h.realtimeRelay.NotifyMailboxMessage(userID, id, ciphertext)
	return nil
}

// =========== //
//   invites   //
// =========== //

type inviteCreateRequest struct {
	ID            string
	TokenHash     string
	CreatedAt     time.Time
	GrantedRole   string
	UserSignature UserSignature
}

// CreateInvite handles POST /api/invites.
// Client mints id + secret; only SHA-256(secret) is sent (tokenHash).
func (h *Handlers) CreateInvite(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(userIDKey).(string)
	ok = ok && caller != ""
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if inviteSignupMode(h.cfg.SignupMode) == signupModeClosed {
		writeError(w, http.StatusForbidden, "Signups are closed on this server")
		return
	}

	var msg pb.CreateInviteRequest
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req := inviteCreateRequest{
		ID:            msg.GetId(),
		TokenHash:     msg.GetTokenHash(),
		CreatedAt:     timeFromUnix(msg.GetCreatedAt()),
		GrantedRole:   msg.GetGrantedRole(),
		UserSignature: userSignatureFromPB(msg.GetUserSignature()),
	}
	idOwner, idServerID, idEntity, ok := parseKeyFingerprint(identityID(req.ID))
	if !ok || !isValidUUIDv7(idEntity) {
		writeError(w, http.StatusBadRequest, "Invalid invite id")
		return
	}
	if string(canonicalID(idServerID, idOwner)) != caller {
		writeError(w, http.StatusForbidden, "Invite id does not belong to the caller")
		return
	}
	tokenHash, err := decodeHashHex(req.TokenHash)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid tokenHash")
		return
	}
	tokenHashHex := encodeHashHex(tokenHash)
	if req.UserSignature.ID == "" || req.UserSignature.Armor == "" {
		writeError(w, http.StatusBadRequest, "userSignature is required")
		return
	}

	createdAt := req.CreatedAt.UTC().Truncate(time.Second)
	if createdAt.IsZero() {
		writeError(w, http.StatusBadRequest, "createdAt is required")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	skew := now.Sub(createdAt)
	if skew < 0 {
		skew = -skew
	}
	if skew > inviteCreateSkew {
		writeError(w, http.StatusBadRequest, "createdAt out of range")
		return
	}

	grantedRole := strings.TrimSpace(req.GrantedRole)
	if grantedRole == "" {
		grantedRole = roleUser
	}
	if grantedRole != roleUser && grantedRole != roleAdmin {
		writeError(w, http.StatusBadRequest, "Invalid grantedRole")
		return
	}
	if grantedRole == roleAdmin {
		callerRole, err := h.services.db.GetUserRole(r.Context(), caller)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		if !canGrantAdmin(callerRole) {
			writeError(w, http.StatusForbidden, "Cannot grant admin role")
			return
		}
	}

	userPayload := buildInviteUserPayload(
		h.services.db.GetServerID(), caller, req.ID, tokenHashHex, grantedRole, createdAt,
	)
	userSigArmor := req.UserSignature.Armor
	key, err := h.services.db.GetPublicKey(r.Context(), req.UserSignature.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	var pubArmor string
	if key != nil && !key.Revoked {
		pubArmor = key.Armor
	}
	if pubArmor == "" {
		writeError(w, http.StatusUnauthorized, "Active public key not available")
		return
	}
	if err := h.services.crypto.verifySignature(string(userPayload), userSigArmor, pubArmor); err != nil {
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	if err := h.services.db.insertInvite(
		r.Context(), req.ID, caller, tokenHash, createdAt, grantedRole,
		req.UserSignature.ID, req.UserSignature.Armor, h.cfg.MaxInvitesPerUser,
	); err != nil {
		if errors.Is(err, errInviteExists) {
			writeError(w, http.StatusConflict, "Invite already exists")
			return
		}
		if errors.Is(err, errInviteLimitReached) {
			writeError(w, http.StatusForbidden, "Invite limit reached")
			return
		}
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	signedAt := now
	serverPayload := buildInviteServerPayload(
		h.services.db.GetServerID(),
		caller,
		req.ID,
		tokenHashHex,
		h.signingKey.Fingerprint,
		req.UserSignature.Armor,
		createdAt,
		signedAt,
	)
	serverSig, err := h.countersign(serverPayload, signedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	writeResponse(w, http.StatusCreated, &pb.Invite{
		Id:              req.ID,
		TokenHash:       tokenHashHex,
		CreatedAt:       unixOrZero(createdAt),
		GrantedRole:     grantedRole,
		UserSignature:   pbUserSignature(req.UserSignature),
		ServerSignature: pbServerSignature(serverSig),
	})
}

// InviteStatus handles GET /api/invites/{id} for the caller's invite.
func (h *Handlers) InviteStatus(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(userIDKey).(string)
	ok = ok && caller != ""
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id := mux.Vars(r)["id"]
	if id == "" {
		writeError(w, http.StatusBadRequest, "Invite id is required")
		return
	}

	inv, err := h.services.db.getInviteByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if inv == nil || inv.CreatedBy != caller {
		writeError(w, http.StatusNotFound, "Invite not found")
		return
	}

	writeResponse(w, http.StatusOK, &pb.InviteStatus{
		Id:        inv.ID,
		CreatedAt: unixOrZero(inv.CreatedAt),
		Status:    inv.Status(),
		ClaimedAt: unixPtr(inv.ClaimedAt),
		ClaimedBy: inv.ClaimedBy,
		RevokedAt: unixPtr(inv.RevokedAt),
	})
}

// DeleteInvite handles DELETE /api/invites/{id}.
func (h *Handlers) DeleteInvite(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(userIDKey).(string)
	ok = ok && caller != ""
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id := mux.Vars(r)["id"]
	if id == "" {
		writeError(w, http.StatusBadRequest, "Invite id is required")
		return
	}

	err := h.services.db.revokeInvite(r.Context(), id, caller, time.Now().UTC())
	switch {
	case errors.Is(err, errInviteNotFound), errors.Is(err, errInviteNotOwner):
		writeError(w, http.StatusNotFound, "Invite not found")
	case errors.Is(err, errInviteAlreadyClaimed):
		writeError(w, http.StatusConflict, "Invite already claimed")
	case errors.Is(err, errInviteAlreadyRevoked):
		w.WriteHeader(http.StatusNoContent)
	case err != nil:
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// CheckInvite handles GET /api/invites/check?id=&secret=.
// Client sends the fragment secret; server looks up by id + hash.
func (h *Handlers) CheckInvite(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	secret := strings.TrimSpace(r.URL.Query().Get("secret"))
	if id == "" || secret == "" {
		writeError(w, http.StatusBadRequest, "Arguments `id` and `secret` are required")
		return
	}
	inv, err := h.services.db.GetPendingInvite(r.Context(), id, secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	valid := inv != nil
	writeResponse(w, http.StatusOK, &pb.InviteCheckResponse{Valid: valid})
}
