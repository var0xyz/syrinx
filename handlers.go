package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"syrinx/realtime"

	"github.com/gorilla/mux"
)

// /////////// //
//   Structs   //
// /////////// //

type Handlers struct {
	services      *Services
	cfg           AppConfig
	broadcastChan chan<- realtime.BroadcastMessage
}

type ServerInfo struct {
	Name string `json:"name"`
}

// ///////////// //
//   Utilities  //
// ///////////// //

func NewHandlers(services *Services, cfg AppConfig, broadcastChan chan<- realtime.BroadcastMessage) *Handlers {
	return &Handlers{
		services:      services,
		cfg:           cfg,
		broadcastChan: broadcastChan,
	}
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
	writeResponse(w, http.StatusOK, ServerInfo{Name: h.cfg.ServerName})
}

func (h *Handlers) Signup(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("Signup request received")

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

	exists, err := h.services.db.UsernameExists(username)
	if err != nil {
		log.Error().
			Str("username", username).
			Err(err).Msg("Failed to get user by username")
		internalServerError(w)
		return
	}
	if exists {
		writeResponse(w, http.StatusBadRequest, "Username already exists")
		return
	}

	user, err := h.services.db.CreateUser(username)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
			log.Info().
				Str("username", username).
				Msg("Username already exists")
			writeResponse(w, http.StatusBadRequest, "Username already exists")
			return
		} else {
			log.Error().Err(err).Msg("Failed to create user '" + username + "': " + err.Error())
			internalServerError(w)
			return
		}
	}
	log.Debug().
		Str("userID", user.ID).
		Str("username", username).
		Msg("User created successfully")

	// Validate and verify the public key using crypto service
	key, err := h.services.crypto.ValidateAndExtractPublicKey(publicKey, signature)
	if err != nil {
		log.Error().
			Str("userID", user.ID).
			Err(err).Msg("Error validating public key")
		writeResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Info().
		Str("userID", user.ID).
		Str("fingerprint", key.Fingerprint).
		Msg("Public key signature verified successfully")

	// Check if key already exists
	keyExists, err := h.services.db.PublicKeyExists(key.Fingerprint)
	if err != nil {
		log.Error().Str("fingerprint", key.Fingerprint).Err(err).Msg("Error checking if public key exists")
		internalServerError(w)
		return
	}
	if keyExists {
		log.Info().
			Str("fingerprint", key.Fingerprint).
			Str("userID", user.ID).
			Msg("Key with this fingerprint already exists")
		writeResponse(w, http.StatusBadRequest, "Key with this fingerprint already exists")
		return
	}

	_, err = h.services.db.AddPublicKey(key.Fingerprint, user.ID, key.CreatedAt, key.ExpiresAt, publicKey)
	if err != nil {
		log.Error().
			Str("userID", user.ID).
			Err(err).Msg("Error adding public key")
		internalServerError(w)
		return
	}
	log.Info().
		Str("userID", user.ID).
		Str("fingerprint", key.Fingerprint).
		Msg("Public key added successfully")

	// Create server keys for the user
	email := strings.TrimSpace(values.Get("email"))
	keyPair, err := h.services.crypto.CreateKeyPair(user.ID, email, h.cfg.ServerName)
	if err != nil {
		log.Error().
			Str("userID", user.ID).
			Err(err).
			Msg("Failed to create server key")
		// Don't fail signup if server key creation fails
		// Just log the error and continue
	} else {
		// Updates the user's server key fingerprint in-place
		err = h.services.db.UpdateDefaultServerKeyForUser(user, keyPair.Fingerprint)
		if err != nil {
			log.Error().
				Str("userID", user.ID).
				Str("fingerprint", keyPair.Fingerprint).
				Err(err).
				Msg("Failed to update server key fingerprint")
		}

		// Save the key pair to the database
		_, err = h.services.db.SaveKeyPair(keyPair)
		if err != nil {
			log.Error().
				Str("userID", user.ID).
				Str("fingerprint", keyPair.Fingerprint).
				Err(err).
				Msg("Failed to save server key pair")
		}
	}

	writeResponse(w, http.StatusCreated, user)
}

func (h *Handlers) GenerateUserKeys(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GenerateUserKeys request received")

	userID := h.getUserID(r)
	user, err := h.services.db.GetUser(userID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Failed to get user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusBadRequest, "User not found")
		return
	}

	email := r.FormValue("email")
	keyPair, err := h.services.crypto.CreateKeyPair(user.ID, email, h.cfg.ServerName)
	if err != nil {
		log.Error().
			Str("userID", user.ID).
			Err(err).Msg("Failed to create server key")
		internalServerError(w)
		return
	}
	log.Debug().
		Str("userID", user.ID).
		Str("fingerprint", keyPair.Fingerprint).
		Msg("Server key created successfully")

	err = h.services.db.UpdateDefaultServerKeyForUser(user, keyPair.Fingerprint)
	if err != nil {
		log.Error().
			Str("userID", user.ID).
			Str("fingerprint", keyPair.Fingerprint).
			Err(err).Msg("Failed to update server key fingerprint")
		internalServerError(w)
		return
	}

	publicKey, err := h.services.db.SaveKeyPair(keyPair)
	if err != nil {
		log.Error().
			Str("userID", user.ID).
			Err(err).Msg("Failed to save key pair")
		internalServerError(w)
		return
	}
	log.Debug().
		Str("userID", user.ID).
		Str("fingerprint", keyPair.Fingerprint).
		Msg("Server key saved successfully")

	writeResponse(w, http.StatusCreated, publicKey)
}

func (h *Handlers) CheckUsername(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("CheckUsername request received")

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

func (h *Handlers) GetUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUser request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
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

func (h *Handlers) DeleteMe(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("DeleteCurrentlyLoggedInUser request received")

	userID := h.getUserID(r)
	err := h.services.db.DeleteUser(userID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error deleting user")
		internalServerError(w)
		return
	}
	log.Info().
		Str("userID", userID).
		Msg("User deleted")

	writeResponse(w, http.StatusOK, "user deleted successfully")
}

func (h *Handlers) UpdateUser(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("UpdateUser request received")

	userID := h.getUserID(r)
	username := strings.TrimSpace(r.FormValue("username"))
	if username == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `username` is required")
		return
	}

	if len(username) > 32 {
		writeResponse(w, http.StatusBadRequest, "Username cannot exceed 32 characters")
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
		writeResponse(w, http.StatusBadRequest, "User not found")
		return
	}

	if user.Username != username {
		log.Info().
			Str("userID", userID).
			Str("username", username).
			Msg("Checking if username exists")
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

		user.Username = username
	}

	avatarURL := r.FormValue("avatarURL")
	log.Println("avatarURL", avatarURL)
	if avatarURL != "" {
		_, err := url.ParseRequestURI(avatarURL)
		if err != nil {
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

		resp, err := http.Head(avatarURL)
		if err != nil {
			log.Error().
				Str("avatarURL", avatarURL).
				Err(err).Msg("Error checking avatar URL")
			writeResponse(w, http.StatusBadRequest, "Error checking avatar URL")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMovedPermanently {
			log.Error().
				Str("avatarURL", avatarURL).
				Int("statusCode", resp.StatusCode).
				Msg("Avatar URL returned non-200 status")
			writeResponse(w, http.StatusBadRequest, "Avatar URL returned non-200 status")
			return
		}
	}
	user.AvatarURL = avatarURL

	bio := r.FormValue("bio")
	if len(bio) > 500 {
		log.Error().
			Str("userID", userID).
			Int("length", len(bio)).
			Msg("Bio cannot exceed 500 characters")
		writeResponse(w, http.StatusBadRequest, "Bio cannot exceed 500 characters")
		return
	}
	user.Bio = bio

	log.Info().
		Str("userID", userID).
		Str("username", username).
		Str("avatarURL", avatarURL).
		Str("bio", bio).
		Msg("Updating user")

	err = h.services.db.UpdateUser(user)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error updating user")
		internalServerError(w)
		return
	}

	// Broadcast the user update to realtime subscribers
	h.broadcastChan <- realtime.BroadcastMessage{
		Type:   realtime.UserUpdate,
		UserID: userID,
		Data: map[string]interface{}{
			"username":  user.Username,
			"avatarURL": user.AvatarURL,
			"bio":       user.Bio,
		},
	}

	writeResponse(w, http.StatusOK, user)
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
		log.Error().Str("userID", userID).Msg("Argument `revokedKeySignature` not found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `revokedKeySignature` is required")
		return
	}

	newKeySignature := strings.TrimSpace(r.FormValue("newKeySignature"))
	if newKeySignature == "" {
		log.Error().Str("userID", userID).Msg("Argument `newKeySignature` not found in request")
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

	// Retrieve old key from database
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

	isRevoked, err := h.services.db.IsPublicKeyRevoked(revokedKey)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("revokedKeyFingerprint", revokedKeyFingerprint).
			Err(err).Msg("Error checking if public key is revoked")
		internalServerError(w)
		return
	}
	if !isRevoked {
		log.Error().
			Str("userID", userID).
			Str("revokedKeyFingerprint", revokedKeyFingerprint).
			Msg("Key is not revoked")
		writeResponse(w, http.StatusBadRequest, "Key is not revoked")
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

	// Check if key already exists
	keyExists, err := h.services.db.PublicKeyExists(newKey.Fingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", newKey.Fingerprint).
			Err(err).Msg("Error checking if public key exists")
		internalServerError(w)
		return
	}
	if keyExists {
		log.Info().
			Str("fingerprint", newKey.Fingerprint).
			Str("userID", userID).
			Msg("Key with this fingerprint already exists")
		writeResponse(w, http.StatusBadRequest, "Key with this fingerprint already exists")
		return
	}

	publicKey, err := h.services.db.AddPublicKey(newKey.Fingerprint, userID, newKey.CreatedAt, newKey.ExpiresAt, armoredPublicKey)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error adding public key")
		internalServerError(w)
		return
	}
	log.Info().
		Str("userID", userID).
		Str("fingerprint", newKey.Fingerprint).
		Msg("Public key created")

	writeResponse(w, http.StatusOK, publicKey)
}

func (h *Handlers) GetUserKeys(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetUserKeys request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	publicKeys, err := h.services.db.GetPublicKeysByUserID(userID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error getting user keys")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, publicKeys)
}

func (h *Handlers) GetPublicKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetPublicKey request received")

	fingerprint := mux.Vars(r)["fingerprint"]
	if fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `fingerprint` is required")
		return
	}

	userID := h.getUserID(r)
	key, err := h.services.db.GetPublicKey(userID, fingerprint)
	if err != nil {
		writeResponse(w, http.StatusNotFound, "Key '"+fingerprint+"' not found")
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
	if reason == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `reason` is required")
		return
	}

	log.Debug().
		Str("userID", userID).
		Str("fingerprint", fingerprint).
		Str("reason", reason).
		Msg("Attempting to revoke key")

	err = h.services.db.RevokeKey(fingerprint, userID, reason)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", fingerprint).
			Err(err).Msg("Error revoking key")
		internalServerError(w)
		return
	}

	log.Info().
		Str("userID", userID).
		Str("fingerprint", fingerprint).
		Msg("Key revoked successfully")

	writeResponse(w, http.StatusOK, "Key revoked successfully")
}

func (h *Handlers) GetReedsByUserID(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReedsByUserID request received")

	userID := mux.Vars(r)["userID"]
	if userID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	reeds, err := h.services.db.GetReedsByUserID(userID)
	if err != nil {
		log.Error().Str("userID", userID).Err(err).Msg("Error getting reeds for user")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, reeds)
}

func (h *Handlers) SignReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("SignReed request received")

	userID := h.getUserID(r)

	// Parse form data
	err := r.ParseForm()
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error parsing form")
		writeResponse(w, http.StatusBadRequest, "Error parsing form")
		return
	}

	signature := r.FormValue("signature")
	if signature == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `signature` is required")
		return
	}

	reedID := r.FormValue("reedID")
	if reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	// Get the user to find the fingerprint
	user, err := h.services.db.GetUser(userID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusBadRequest, "User not found")
		return
	}

	publicKey, err := h.services.db.GetPublicKey(userID, user.ServerKeyFingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", user.ServerKeyFingerprint).
			Err(err).Msg("Error getting public key")
		internalServerError(w)
		return
	}

	if publicKey == nil {
		writeResponse(w, http.StatusBadRequest, "Identity not found")
		return
	}

	// Get the server's private key for signing the signature
	privateKey, err := h.services.db.GetPrivateKey(userID, user.ServerKeyFingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error getting private key")
		internalServerError(w)
		return
	}
	if privateKey == nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", user.ServerKeyFingerprint).
			Msg("Private key not found for fingerprint")
		writeResponse(w, http.StatusBadRequest, "Private key "+user.ServerKeyFingerprint+" not found")
		return
	}

	// We sign the user's signature with the server's private key, which in
	// turn signed the reed that we just validated.
	serverSignature, err := h.services.crypto.Sign(signature, privateKey.Armor)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("signature", signature).
			Str("fingerprint", user.ServerKeyFingerprint).
			Err(err).Msg("Error signing")
		internalServerError(w)
		return
	}

	reed, err := h.services.db.CreateReed(reedID, userID, user.ServerKeyFingerprint)
	if err != nil {
		log.Error().
			Str("reedID", reedID).
			Str("userID", userID).
			Str("fingerprint", user.ServerKeyFingerprint).
			Err(err).Msg("Error creating reed")
		internalServerError(w)
		return
	}

	log.Debug().
		Str("userID", userID).
		Str("reedID", reed.ID).
		Msg("Reed created successfully")

	// Broadcast the new reed to realtime subscribers
	h.broadcastChan <- realtime.BroadcastMessage{
		Type:     realtime.NewReed,
		ServerID: h.services.db.GetServerID(),
		UserID:   userID,
		ReedID:   reed.ID,
	}

	writeResponse(w, http.StatusCreated, serverSignature)
}

func (h *Handlers) DeleteReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("DeleteReed request received")

	reedID := mux.Vars(r)["reedID"]
	if reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	userID := h.getUserID(r)

	// Get user information for broadcast
	user, err := h.services.db.GetUser(userID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Error getting user")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusBadRequest, "User not found")
		return
	}

	// Check if reed exists and belongs to user
	reed, err := h.services.db.GetReed(userID, reedID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Err(err).Msg("Error getting reed")
		internalServerError(w)
		return
	}
	if reed == nil {
		writeResponse(w, http.StatusNotFound, "Reed not found")
		return
	}
	if reed.UserID != userID {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Str("reedUserID", reed.UserID).
			Msg("User does not own this reed")
		writeResponse(w, http.StatusForbidden, "You can only delete your own reeds")
		return
	}

	// Delete the reed
	err = h.services.db.DeleteReed(reedID)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("reedID", reedID).
			Err(err).Msg("Error deleting reed")
		internalServerError(w)
		return
	}

	log.Info().
		Str("userID", userID).
		Str("reedID", reedID).
		Msg("Reed deleted successfully")

	// Broadcast the reed deletion to realtime subscribers
	h.broadcastChan <- realtime.BroadcastMessage{
		Type:   realtime.ReedDeleted,
		UserID: userID,
		ReedID: reedID,
		Data: map[string]interface{}{
			"username": user.Username,
		},
	}

	writeResponse(w, http.StatusOK, "Reed deleted successfully")
}

func (h *Handlers) GetReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReed request received")

	reedID := mux.Vars(r)["reedID"]
	if reedID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	userID := h.getUserID(r)
	reed, err := h.services.db.GetReed(userID, reedID)
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
	reed, err := h.services.db.GetReed(userID, reedID)
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

	privateKey, err := h.services.db.GetPrivateKey(reed.UserID, reed.Fingerprint)
	if err != nil {
		log.Error().
			Str("userID", userID).
			Str("reed.userID", reed.UserID).
			Str("fingerprint", reed.Fingerprint).
			Err(err).Msg("Error getting private key for verification")
		internalServerError(w)
		return
	}
	if privateKey == nil {
		log.Error().
			Str("userID", userID).
			Str("fingerprint", reed.Fingerprint).
			Msg("Private key not found for verification")
		writeResponse(w, http.StatusNotFound, "Private key "+reed.Fingerprint+" not found")
		return
	}

	// Verify the signature using the crypto service
	err = h.services.crypto.VerifySignature(userSignature, serverSignature, privateKey.Armor)
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
