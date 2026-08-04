//go:build !ops

package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"syrinx/deletion"
	"syrinx/identity"
	"syrinx/ids"
	"syrinx/invites"
	"syrinx/realtime"
	"syrinx/recovery"
	"syrinx/signing"

	"github.com/gorilla/mux"
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
	// filterPipeTags keeps only tags with current pipe listeners (SignReed stash).
	// Nil means stash all extracted tags (tests / no realtime).
	filterPipeTags func([]string) []string
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
	}
}

// SetPipeTagFilter installs the SignReed hook that intersects extracted tags
// with live pipe subscriptions.
func (h *Handlers) SetPipeTagFilter(filter func([]string) []string) {
	h.filterPipeTags = filter
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

	armor, err := h.services.db.GetServerPublicKeyByFingerprint(fingerprint)
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

// Signup produces a fresh signed identity record for a brand-new user.
// One-round flow:
//
//  1. Client sends {username, publicKey, signature, userSignature}.
//     - `signature` is the user's self-signature over `publicKey`.
//     - `userSignature` is a fresh base64-armored PGP detached signature
//     over `identity.BuildUserIdentityPayload(username, fingerprint, "", "")`.
//  2. Server verifies both signatures, mints createdAt / signedAt,
//     assembles and countersigns the server identity payload, and
//     persists both signatures on the users row alongside the
//     user_keys insert.
//
// The client can compute `userSignature` locally without any pre-flight
// roundtrip — the user payload contains no server-authored fields, so
// there is nothing to fetch first.
func (h *Handlers) Signup(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("Signup request received")

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
	if !ids.Valid(userID) {
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

	serverPubKey, err := h.services.db.GetServerPublicKeyByFingerprint(userIDFingerprint)
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

	inviteID := strings.TrimSpace(values.Get("inviteID"))
	inviteSecret := strings.TrimSpace(values.Get("inviteSecret"))
	inv, err := h.services.db.LookupPendingInvite(r.Context(), inviteID, inviteSecret)
	if err != nil {
		log.Error().Err(err).Msg("Failed to look up invite")
		internalServerError(w)
		return
	}
	resolved, err := invites.ResolveSignup(invites.SignupMode(h.cfg.SignupMode), inviteID, inviteSecret, inv)
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

	exists, err := h.services.db.UsernameExists(username)
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
	// signup avatarURL and bio are both empty — a user cannot set them
	// before their account exists. If we ever let signup carry an
	// initial bio/avatar, they'd flow in here.
	userPayload := identity.BuildUserIdentityPayload(username, key.Fingerprint, "", "")

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

	profilePayload := identity.BuildNewProfilePayload(
		userID,
		username,
		key.Fingerprint,
		h.services.db.GetServerID(),
		h.signingKey.Fingerprint,
		userSignatureB64,
		resolved.InviterID,
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

	user, err := h.services.db.Signup(SignupInput{
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
		SignupMode:         invites.SignupMode(h.cfg.SignupMode),
		InviteID:           inviteID,
		InviteSecret:       inviteSecret,
		InvitedBy:          resolved.InviterID,
	})
	if err != nil {
		if errors.Is(err, invites.ErrInviteRequired) {
			writeResponse(w, http.StatusForbidden, "Invite required")
			return
		}
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

	exists, err := h.services.db.UsernameExists(username)
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
		profile,
		h.services.db.GetServerID(),
		h.services.db.GetServerPublicKeyByFingerprint,
		h.services.crypto,
	); err != nil {
		writeResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	// Peer-seeded only (in unclaimed_accounts) → unknown. Implies a users
	// row exists; no need to read users yet.
	unclaimed, err := h.services.db.IsUnclaimed(profile.ID)
	if err != nil {
		internalServerError(w)
		return
	}
	if unclaimed {
		writeResponse(w, http.StatusNotFound, recovery.UserStatusUnknownResponse)
		return
	}

	// No users row → unknown.
	signedAt, err := h.services.db.UserServerSignedAt(profile.ID)
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
	ongoing, err := h.services.db.IsOngoing(profile.ID)
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

func (h *Handlers) GetUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUser request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	removal, err := h.services.db.GetAccountRemoval(userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading account removal")
		internalServerError(w)
		return
	}
	if removal != nil {
		writeResponse(w, http.StatusGone, h.accountRemovalWire(removal))
		return
	}

	user, err := h.services.db.GetUser(userID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusNotFound, "User not found")
		return
	}

	writeResponse(w, http.StatusOK, user)
}

func (h *Handlers) FollowUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	followerID := h.getUserID(r)
	userID := mux.Vars(r)["userID"]

	if followerID == userID {
		writeResponse(w, http.StatusBadRequest, "Cannot follow yourself")
		return
	}

	if err := h.services.db.FollowUser(followerID, userID); err != nil {
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

	if err := h.services.db.UnfollowUser(followerID, userID); err != nil {
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

	existing, err := h.services.db.GetAccountRemoval(userID)
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

	user, err := h.services.db.GetUser(userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusNotFound, "User not found")
		return
	}

	fingerprint := user.ActiveKeyFingerprint
	userPayload := identity.BuildAccountRemovalUserPayload(serverID, userID, note)
	userSigArmor, err := base64.StdEncoding.DecodeString(userSignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(userID, fingerprint)
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
	if err := h.services.db.InsertAccountRemoval(cert); err != nil {
		if errors.Is(err, deletion.ErrConflict) {
			existing, getErr := h.services.db.GetAccountRemoval(userID)
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

	affectedTargets, err := h.services.db.DeleteEchoesByAuthor(userID)
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

	h.broadcastChan <- realtime.BroadcastMessage{
		Type:     realtime.AccountRemoved,
		ServerID: serverID,
		UserID:   userID,
		Data: map[string]interface{}{
			"cert": h.accountRemovalWire(&cert),
		},
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
// request MUST carry the complete post-edit tuple (username, avatarURL,
// bio) plus `userSignature`, a base64(armored PGP) detached signature
// over `identity.BuildUserIdentityPayload(username, fingerprint, avatarURL,
// bio)` where `fingerprint` is the caller's active user key.
//
// The client is expected to skip the network call entirely when nothing
// changed. As a defence against clients that don't (or against probes),
// the server treats byte-equality between the submitted `userSignature`
// and the row's stored user attestation as the authoritative "did
// anything change?" test. A valid detached signature deterministically
// binds a specific (username, fingerprint, avatarURL, bio) tuple under a
// specific key, so equal signature bytes ⇒ equal signed bytes ⇒ equal
// fields. In that case the server short-circuits: no re-verify, no new
// signedAt, no new server signature, no realtime broadcast, just return
// the current record.
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
	currentUser, err := h.services.db.GetUser(userID)
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
		exists, err := h.services.db.UsernameExists(username)
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

	avatarURL := r.FormValue("avatarURL")
	if avatarURL != "" {
		if _, err := url.ParseRequestURI(avatarURL); err != nil {
			log.Error().
				Str("avatarURL", avatarURL).
				Err(err).Msg("Error parsing avatar URL")
			writeResponse(w, http.StatusBadRequest, "Invalid avatar URL")
			return
		}

		if !strings.HasPrefix(avatarURL, "http://") && !strings.HasPrefix(avatarURL, "https://") {
			log.Error().
				Str("avatarURL", avatarURL).
				Msg("Unsupported protocol for avatar URL")
			writeResponse(w, http.StatusBadRequest, "Invalid protocol for avatar URL")
			return
		}
	}

	bio := r.FormValue("bio")
	if len(bio) > 500 {
		log.Error().
			Str("userID", userID).
			Int("length", len(bio)).
			Msg("Bio cannot exceed 500 characters")
		writeResponse(w, http.StatusBadRequest, "Bio cannot exceed 500 characters")
		return
	}

	// Reconstruct the exact bytes the client claims to have signed,
	// using the fingerprint we trust (from the row) rather than one
	// supplied by the caller. Then verify.
	//
	// We use ActiveKeyFingerprint here — the client signs with whatever
	// key is currently active. Today ActiveKeyFingerprint and
	// SignatureFingerprint collapse to the same DB column (see
	// GetUser); if/when they diverge (rotation minting its own identity
	// record), the active key is still the right one to rebuild the
	// user-signed payload against.
	fingerprint := currentUser.ActiveKeyFingerprint
	userPayload := identity.BuildUserIdentityPayload(username, fingerprint, avatarURL, bio)

	userSigArmor, err := base64.StdEncoding.DecodeString(userSignatureB64)
	if err != nil {
		log.Error().Err(err).Msg("Invalid userSignature encoding")
		writeResponse(w, http.StatusBadRequest, "Invalid userSignature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(userID, fingerprint)
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
		avatarURL,
		h.services.db.GetServerID(),
		h.signingKey.Fingerprint,
		userSignatureB64,
		invitedByID,
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

	if err := h.services.db.UpdateUser(UpdateUserInput{
		UserID:           userID,
		Username:         username,
		AvatarURL:        avatarURL,
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

	updated, err := h.services.db.GetUser(userID)
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
		Data: map[string]interface{}{
			"username":  updated.Username,
			"avatarURL": updated.AvatarURL,
			"bio":       updated.Bio,
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
	revokedKey, err := h.services.db.GetPublicKey(userID, revokedKeyFingerprint)
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

	publicKey, err := h.services.db.AddPublicKey(AddPublicKeyInput{
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

	key, err := h.services.db.GetPublicKey(userID, fingerprint)
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

	pubKey, err := h.services.db.GetPublicKey(userID, fingerprint)
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

	err = h.services.db.RevokeKey(RevokeKeyInput{
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

	key, err := h.services.db.GetPublicKey(userID, fingerprint)
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

	revocation, err := h.services.db.GetKeyRevocation(userID, fingerprint)
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

	if !ReedContentWithinLimits(contentBody) {
		writeResponse(w, http.StatusBadRequest, "Reed content exceeds character limits")
		return
	}

	localServerID := h.services.db.GetServerID()
	var echoRef *ReedRef
	if echoing != "" {
		ref, ok := h.parseReedRef(echoing, localServerID)
		if !ok {
			writeResponse(w, http.StatusBadRequest, "Invalid echoing reference")
			return
		}
		exists, err := h.services.db.ReedExists(ref.AuthorID, ref.ReedID)
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
		exists, err := h.services.db.ReedExists(ref.AuthorID, ref.ReedID)
		if err != nil {
			log.Error().Err(err).Msg("Error checking reply target")
			internalServerError(w)
			return
		}
		if !exists {
			writeResponse(w, http.StatusBadRequest, "Target reed not found")
			return
		}
	}

	markdown := ReedAsMarkdown(reedID, userID, contentBody, echoing, replying)

	user, err := h.services.db.GetUser(userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusBadRequest, "User not found")
		return
	}

	userFingerprint := user.ActiveKeyFingerprint
	userSigArmor, err := base64.StdEncoding.DecodeString(userSignature)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(userID, userFingerprint)
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

	existing, err := h.services.db.GetReedAttestation(userID, reedID)
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
	reed, echoIndexed, err := h.services.db.CreateReedWithEcho(
		r.Context(),
		reedID,
		userID,
		userFingerprint,
		userSignature,
		serverSignature.Fingerprint,
		serverSignature.Armor,
		serverSignature.SignedAt,
		echoRef,
		tags,
	)
	if err != nil {
		// Concurrent SignReed for the same id: both passed the pre-insert
		// GetReedAttestation (nil), both tried Create; the loser hits unique
		// violation and must return the winner's stored countersignature.
		// (Lost-response retries are already handled by the check above.)
		if isReedUniqueViolation(err) {
			existing, getErr := h.services.db.GetReedAttestation(userID, reedID)
			if getErr == nil && existing != nil {
				h.respondSignReedReplay(w, r, existing, userSignature, userID, reedID)
				return
			}
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
		h.broadcastChan <- realtime.BroadcastMessage{
			Type:   realtime.EchoCountChanged,
			UserID: echoRef.AuthorID,
			ReedID: echoRef.ReedID,
		}
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
	if !ids.ValidReed(ref.ReedID) {
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

	existing, err := h.services.db.GetReedRemoval(userID, reedID)
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

	user, err := h.services.db.GetUser(userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusBadRequest, "User not found")
		return
	}

	fingerprint := user.ActiveKeyFingerprint
	userPayload := identity.BuildReedRemovalUserPayload(serverID, userID, reedID)
	userSigArmor, err := base64.StdEncoding.DecodeString(userSignatureB64)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid signature encoding")
		return
	}
	pubKey, err := h.services.db.GetPublicKey(userID, fingerprint)
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
	if err := h.services.db.InsertReedRemoval(cert); err != nil {
		if errors.Is(err, deletion.ErrConflict) {
			// Concurrent first accept: return the stored cert if the user
			// signature matches; otherwise a true conflicting attestation.
			existing, getErr := h.services.db.GetReedRemoval(userID, reedID)
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

	affectedTargets, err := h.services.db.DeleteEchoIndexForReed(userID, reedID)
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

	// Keep the reeds row for allocation catch-up (04): reed_allocations FK
	// cascades on reed delete. Tip/list already exclude reed_removals.
	h.broadcastChan <- realtime.BroadcastMessage{
		Type:     realtime.ReedRemoved,
		ServerID: serverID,
		UserID:   userID,
		ReedID:   reedID,
		Data: map[string]interface{}{
			"cert": h.reedRemovalWire(&cert),
		},
	}

	log.Info().Str("userID", userID).Str("reedID", reedID).Msg("Reed removal accepted")
	writeResponse(w, http.StatusOK, h.reedRemovalWire(&cert))
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

	// Account-removal check first (deletion 08): tombstone wins over reed cert.
	accountRemoval, err := h.services.db.GetAccountRemoval(userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading account removal")
		internalServerError(w)
		return
	}
	if accountRemoval != nil {
		writeResponse(w, http.StatusGone, h.accountRemovalWire(accountRemoval))
		return
	}

	removal, err := h.services.db.GetReedRemoval(userID, reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error loading reed removal")
		internalServerError(w)
		return
	}
	if removal != nil {
		writeResponse(w, http.StatusGone, h.reedRemovalWire(removal))
		return
	}

	reed, err := h.services.db.GetReed(r.Context(), userID, reedID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Err(err).Msg("Error getting post")
		internalServerError(w)
		return
	}
	if reed == nil {
		writeResponse(w, http.StatusNotFound, "Post not found")
		return
	}

	log.Debug().
		Str("userID", userID).
		Str("reedID", reedID).
		Msg("Post found")

	writeResponse(w, http.StatusOK, reed)
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

	accountRemoval, err := h.services.db.GetAccountRemoval(userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error loading account removal")
		internalServerError(w)
		return
	}
	if accountRemoval != nil {
		writeResponse(w, http.StatusGone, h.accountRemovalWire(accountRemoval))
		return
	}

	removal, err := h.services.db.GetReedRemoval(userID, reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error loading reed removal")
		internalServerError(w)
		return
	}
	if removal != nil {
		writeResponse(w, http.StatusGone, h.reedRemovalWire(removal))
		return
	}

	reed, err := h.services.db.GetReed(r.Context(), userID, reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error getting reed")
		internalServerError(w)
		return
	}
	if reed == nil {
		writeResponse(w, http.StatusNotFound, "Post not found")
		return
	}

	count, err := h.services.db.CountEchoes(userID, reedID)
	if err != nil {
		log.Error().Str("userID", userID).Str("reedID", reedID).Err(err).Msg("Error counting echoes")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, count)
}

func (h *Handlers) VerifySignature(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("VerifySignature request received")

	// Get userID from URL path
	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	// Get reedID from URL path
	reedID := mux.Vars(r)["reedID"]
	if reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	// Parse multipart form data to get signature
	err := r.ParseMultipartForm(32 << 20) // 32 MB max memory
	if err != nil {
		log.Error().Err(err).Msg("Error parsing multipart form data")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	userSignature := r.FormValue("userSignature")
	if userSignature == "" {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Msg("No userSignature found in form data")
		writeResponse(w, http.StatusBadRequest, "Argument `userSignature` is required")
		return
	}

	serverSignature := r.FormValue("serverSignature")
	if serverSignature == "" {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Msg("No serverSignature found in form data")
		writeResponse(w, http.StatusBadRequest, "Argument `serverSignature` is required")
		return
	}

	// Get the reed from database to get the user fingerprint
	reed, err := h.services.db.GetReed(r.Context(), userID, reedID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Err(err).Msg("Error getting reed from database")
		internalServerError(w)
		return
	}
	if reed == nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Msg("Reed not found")
		writeResponse(w, http.StatusNotFound, "Reed not found")
		return
	}

	// The reed row lookup already scoped by userID, but re-assert it in case the
	// row-fetch semantics ever drift.
	if reed.UserID != userID {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Str("reedUserID", reed.UserID).
			Msg("Reed userID mismatch")
		writeResponse(w, http.StatusBadRequest, "Reed does not belong to the given user")
		return
	}

	// Select the server public key that produced the countersignature by the
	// reed's stored fingerprint. The current signing key is not necessarily
	// the one that signed this reed (key rotation, historical reeds), so we
	// look it up explicitly.
	serverPubKey, err := h.services.db.GetServerPublicKeyByFingerprint(reed.Fingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Str("fingerprint", reed.Fingerprint).
			Err(err).Msg("Error loading server public key by fingerprint")
		internalServerError(w)
		return
	}
	if serverPubKey == "" {
		log.Warn().
			Str("userID", userID).
			Str("reedID", reedID).
			Str("fingerprint", reed.Fingerprint).
			Msg("Server public key not found for reed fingerprint")
		writeResponse(
			w,
			http.StatusBadRequest,
			"Server signing key with fingerprint "+reed.Fingerprint+" is not known to this server; cannot verify this reed's countersignature.",
		)
		return
	}

	// Rebuild the exact bytes that SignReed signed (headers + userSignature
	// content, via signing.BytesToSign) and verify serverSignature against
	// those bytes. The wire form of serverSignature is base64(armored PGP
	// signature); decode it once before handing to the PGP verifier.
	payload := signing.BytesToSign(
		identity.ReedCountersignHeaders(h.services.db.GetServerID(), reed.ID, reed.UserID, reed.Fingerprint, reed.Timestamp),
		userSignature,
	)
	decodedServerSig, err := base64.StdEncoding.DecodeString(serverSignature)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Err(err).Msg("Failed to base64-decode serverSignature")
		writeResponse(w, http.StatusBadRequest, "Invalid serverSignature encoding")
		return
	}
	err = h.services.crypto.VerifySignature(string(payload), string(decodedServerSig), serverPubKey)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Err(err).Msg("Signature verification failed")
		writeResponse(w, http.StatusBadRequest, "Signature verification failed")
		return
	}

	writeResponse(w, http.StatusOK, "Signature verification successful")
}
