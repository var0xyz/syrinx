package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
)

// /////////// //
//   Structs   //
// /////////// //

type Handlers struct {
	services *Services
	store    *sessions.CookieStore
	cfg      AppConfig
}

type SignedReed struct {
	ID       uuid.UUID `json:"id"`
	UserID   uuid.UUID `json:"userID"`
	SignedAt time.Time `json:"signedAt"`
	Identity string    `json:"identity"`

	UserFingerprint   string `json:"userFingerprint"`
	ServerFingerprint string `json:"serverFingerprint"`

	Signature string `json:"signature"`
}

type WelcomeInfo struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
}

// ///////////// //
//   Utilities  //
// ///////////// //

func NewHandlers(services *Services, cfg AppConfig) *Handlers {
	secretKey := cfg.SecretKey

	store := sessions.NewCookieStore([]byte(secretKey))
	day := 86400
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   day * 7,
		HttpOnly: true,
		Secure:   cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	}

	return &Handlers{
		services: services,
		store:    store,
		cfg:      cfg,
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

func (h *Handlers) getSession(r *http.Request) *sessions.Session {
	session, _ := h.store.Get(r, "session")
	return session
}

func (h *Handlers) getUserID(r *http.Request) uuid.UUID {
	session := h.getSession(r)
	if session.Values["userID"] == nil {
		return uuid.Nil
	}
	rawUserID := session.Values["userID"].(string)
	if rawUserID == "" {
		return uuid.Nil
	}
	userID, err := uuid.Parse(rawUserID)
	if err != nil {
		return uuid.Nil
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

	password := values.Get("password")
	if password == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `password` is required")
		return
	}

	confirmPassword := values.Get("confirmPassword")
	if confirmPassword == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `confirmPassword` is required")
		return
	}

	if password != confirmPassword {
		writeResponse(w, http.StatusBadRequest, "Passwords do not match")
		return
	}

	user, err := h.services.db.GetUserByUsername(username)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get user by username '" + username + "': " + err.Error())
		internalServerError(w)
		return
	}
	if user != nil {
		writeResponse(w, http.StatusBadRequest, "Username already exists")
		return
	}

	user, err = h.services.db.CreateUser(username, password)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create user '" + username + "': " + err.Error())
		internalServerError(w)
		return
	}
	log.Debug().
		Str("userID", user.ID.String()).
		Str("username", username).
		Msg("User created successfully")

	_, err = h.services.db.CreateProfile(user.ID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create profile for '" + username + "': " + err.Error())
		internalServerError(w)
		return
	}
	log.Debug().
		Str("userID", user.ID.String()).
		Str("username", username).
		Msg("Profile created successfully")

	serverKey, err := h.services.crypto.CreateServerKey(user.ID, h.cfg.IdentityEmail)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create server key for '" + username + "': " + err.Error())
		internalServerError(w)
		return
	}
	log.Debug().
		Interface("serverKey", serverKey).
		Str("userID", user.ID.String()).
		Str("serverKeyFingerprint", serverKey.Fingerprint).
		Msg("Server key created successfully")

	err = h.services.db.SaveServerKey(serverKey)
	if err != nil {
		log.Error().Err(err).Msg("Failed to save server key for '" + username + "': " + err.Error())
		internalServerError(w)
		return
	}
	log.Debug().
		Str("userID", user.ID.String()).
		Str("username", username).
		Str("serverKeyFingerprint", serverKey.Fingerprint).
		Msg("Server key saved successfully")

	// Record user creation metric
	if globalMetrics != nil {
		log.Info().Msg("Incrementing user creation metric")
		globalMetrics.UsersCreatedTotal.Add(r.Context(), 1)
	}

	session := h.getSession(r)
	session.Values["userID"] = user.ID.String()
	session.Values["username"] = user.Username

	err = session.Save(r, w)
	if err != nil {
		// We don't want to fail the signup process if the session saving fails, it's not critical.
		log.Error().
			Str("userID", user.ID.String()).
			Str("username", user.Username).
			Err(err).Msg("Error saving session")
	}

	response := WelcomeInfo{
		UserID:   user.ID.String(),
		Username: user.Username,
	}

	writeResponse(w, http.StatusCreated, response)
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("Login request received")

	values, err := parseFormData(r)
	if err != nil {
		log.Error().Err(err).Msg("Error parsing form data")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	username := values.Get("username")
	password := values.Get("password")

	if username == "" || password == "" {
		writeResponse(w, http.StatusBadRequest, "Username and password are required")
		return
	}

	user, err := h.services.db.GetUserByUsername(username)
	if err != nil {
		log.Error().Err(err).Msg("Error getting user by username")
		writeResponse(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	if !h.services.crypto.ValidatePassword(user, password) {
		log.Error().Msg("Invalid password for user: " + username)
		writeResponse(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	response := WelcomeInfo{
		UserID:   user.ID.String(),
		Username: user.Username,
	}

	// Create session
	session := h.getSession(r)
	session.Values["userID"] = user.ID.String()
	session.Values["username"] = user.Username

	err = session.Save(r, w)
	if err != nil {
		log.Error().Str("username", username).Err(err).Msg("Error saving session")
		internalServerError(w)
		return
	}
	log.Info().Str("username", username).Msg("Login successful")
	writeResponse(w, http.StatusOK, response)
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("Logout request received")

	session := h.getSession(r)
	user := session.Values["username"]
	// We exit early because if an attacker could cause the session map to grow indefinitely
	// by making multiple requests to this endpoint with random session IDs.
	if user == nil {
		writeResponse(w, http.StatusOK, "User logged out successfully")
		return
	}
	session.Values["userID"] = nil
	session.Values["username"] = nil
	session.Save(r, w)

	writeResponse(w, http.StatusOK, "User logged out successfully")
}

func (h *Handlers) WhoAmI(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetCurrentlyLoggedInUser request received")

	userID := h.getUserID(r)
	writeResponse(w, http.StatusOK, userID.String())
}

func (h *Handlers) DeleteMe(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("DeleteCurrentlyLoggedInUser request received")

	userID := h.getUserID(r)
	if userID == uuid.Nil {
		writeResponse(w, http.StatusBadRequest, "User not logged in")
		return
	}

	err := h.services.db.DeleteUser(userID)
	if err != nil {
		log.Error().Str("userID", userID.String()).Err(err).Msg("Error deleting user")
		internalServerError(w)
		return
	}

	globalMetrics.UsersDeletedTotal.Add(r.Context(), 1)
	log.Info().Str("userID", userID.String()).Msg("User deleted")

	writeResponse(w, http.StatusOK, "user deleted successfully")
}

func (h *Handlers) GetNonce(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetNonce request received")

	values, err := parseFormData(r)
	if err != nil {
		log.Error().Err(err).Msg("Error parsing form data")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	username := values.Get("username")
	if username == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `username` is required")
		return
	}

	latestNonce, err := h.services.db.GetLatestPasswordResetNonce(username)
	if err != nil {
		log.Error().Str("username", username).Err(err).Msg("Error getting latest password reset nonce")
		internalServerError(w)
		return
	}
	if latestNonce != nil {
		if time.Since(latestNonce.CreatedAt) < 30*time.Second {
			log.Info().Str("username", username).Msg("Password reset nonce requested too soon")
			writeResponse(w, http.StatusTooManyRequests, "Please wait 30 seconds before requesting another nonce")
			return
		}
	}

	// We sleep to mitigate a potential attack. If a malicious user were to make a lot of
	// requests to this endpoint, they could potentially fill the database with nonce records.
	// The reason we do it here is to prevent timing attacks. If we only sleep when the user is found,
	// the attacker could potentially determine if the user exists or not by the time it takes to respond.
	time.Sleep(time.Duration(500+rand.Intn(1000)) * time.Millisecond)

	user, _ := h.services.db.GetUserByUsername(username)
	if user == nil {
		log.Warn().Str("username", username).Msg("User not found, returning dummy nonce")
		writeResponse(w, http.StatusOK, h.services.crypto.GetDummyNonce())
		return
	}

	token, err := h.services.crypto.GenerateNonce()
	if err != nil {
		log.Error().Err(err).Msg("Error generating nonce")
		internalServerError(w)
		return
	}

	nonce, err := h.services.db.SavePasswordResetNonce(user.ID, token)
	if err != nil {
		log.Error().Err(err).Msg("Error saving nonce")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, nonce)
}

func (h *Handlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("ResetPassword request received")

	values, err := parseFormData(r)
	if err != nil {
		log.Error().Err(err).Msg("Error parsing form data")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	username := values.Get("username")
	signature := values.Get("signature")
	fingerprint := values.Get("fingerprint")
	password := values.Get("password")
	confirmPassword := values.Get("confirmPassword")

	if username == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `username` is required")
		return
	}

	if signature == "" || fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Signature and key ID are required")
		return
	}

	if password == "" || confirmPassword == "" {
		writeResponse(w, http.StatusBadRequest, "Password and password confirmation are required")
		return
	}

	if password != confirmPassword {
		writeResponse(w, http.StatusBadRequest, "Passwords do not match")
		return
	}

	publicKey, err := h.services.db.GetPublicKeyByFingerprint(fingerprint)
	if err != nil {
		log.Error().Str("fingerprint", fingerprint).Err(err).Msg("Error getting public key")
		internalServerError(w)
		return
	}
	if publicKey == nil {
		writeResponse(w, http.StatusBadRequest, "Invalid public key fingerprint")
		return
	}

	user, err := h.services.db.GetUserByUsername(username)
	if err != nil {
		log.Error().Str("username", username).Err(err).Msg("Error getting user by username")
		internalServerError(w)
		return
	}
	if user == nil {
		writeResponse(w, http.StatusBadRequest, "Invalid username")
		return
	}

	if user.ID != publicKey.UserID {
		writeResponse(w, http.StatusBadRequest, "Invalid public key fingerprint")
		return
	}

	nonce := h.services.crypto.ExtractMessageFromSignature(signature)
	if nonce == "" {
		writeResponse(w, http.StatusBadRequest, "Invalid signature")
		return
	}

	passwordResetNonce, err := h.services.db.GetPasswordResetNonce(nonce, user.ID)
	if err != nil {
		log.Error().Str("nonce", nonce).Err(err).Msg("Error getting password reset nonce")
		internalServerError(w)
		return
	}
	if passwordResetNonce == nil {
		writeResponse(w, http.StatusBadRequest, "Invalid nonce")
		return
	}

	if time.Now().After(passwordResetNonce.ExpiresAt) {
		writeResponse(w, http.StatusBadRequest, "Expired nonce")
		return
	}

	// Hash new password
	hashedPassword, err := h.services.crypto.HashPassword(password)
	if err != nil {
		log.Error().Str("username", username).Err(err).Msg("Error hashing password")
		internalServerError(w)
		return
	}

	err = h.services.db.UpdatePassword(user.ID, hashedPassword)
	if err != nil {
		log.Error().Str("username", username).Err(err).Msg("Error updating password")
		internalServerError(w)
		return
	}

	err = h.services.db.DeleteAllNoncesForUser(user.ID)
	if err != nil {
		log.Error().Str("username", username).Err(err).Msg("Error deleting expired nonces")
		internalServerError(w)
		return
	}

	// Logout
	session := h.getSession(r)
	session.Values["userID"] = nil
	session.Values["username"] = nil
	session.Save(r, w)

	writeResponse(w, http.StatusOK, "Password updated successfully")
}

func (h *Handlers) GetProfileByUserID(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetProfile request received")

	rawUserID := mux.Vars(r)["userID"]
	if rawUserID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	userID, err := uuid.Parse(rawUserID)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	profile, err := h.services.db.GetProfile(userID)
	if err != nil {
		log.Error().Str("userID", userID.String()).Err(err).Msg("Error getting profile")
		internalServerError(w)
		return
	}

	if profile == nil {
		writeResponse(w, http.StatusNotFound, "Profile not found")
		return
	}

	writeResponse(w, http.StatusOK, profile)
}

func (h *Handlers) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("UpdateProfile request received")

	userID := h.getUserID(r)

	username := strings.TrimSpace(r.FormValue("username"))
	session, _ := h.store.Get(r, "session")
	if username != "" && username != session.Values["username"] {
		user, err := h.services.db.GetUserByUsername(username)
		if err != nil {
			log.Error().
				Str("userID", userID.String()).
				Str("username", username).
				Err(err).Msg("Error getting user by username")
			internalServerError(w)
			return
		}
		if user != nil {
			writeResponse(w, http.StatusBadRequest, "Username already taken")
			return
		}

		err = h.services.db.UpdateUsername(userID, username)
		if err != nil {
			log.Error().Str("userID", userID.String()).Err(err).Msg("Error updating username")
			internalServerError(w)
			return
		}
	}

	avatarURL := r.FormValue("avatar_url")
	log.Println("avatarURL", avatarURL)
	if avatarURL != "" {
		_, err := url.ParseRequestURI(avatarURL)
		if err != nil {
			log.Error().Str("avatarURL", avatarURL).Err(err).Msg("Error parsing avatar URL")
			writeResponse(w, http.StatusBadRequest, "Invalid avatar URL")
			return
		}

		if !strings.HasPrefix(avatarURL, "http://") && !strings.HasPrefix(avatarURL, "https://") {
			log.Error().Str("avatarURL", avatarURL).Msg("Unsupported protocol for avatar URL")
			writeResponse(w, http.StatusBadRequest, "Invalid avatar URL")
			return
		}

		resp, err := http.Head(avatarURL)
		if err != nil {
			log.Error().Str("avatarURL", avatarURL).Err(err).Msg("Error checking avatar URL")
			writeResponse(w, http.StatusBadRequest, "Invalid avatar URL")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMovedPermanently {
			log.Error().Str("avatarURL", avatarURL).Int("statusCode", resp.StatusCode).Msg("Avatar URL returned non-200 status")
			writeResponse(w, http.StatusBadRequest, "Invalid avatar URL")
			return
		}
	}

	bio := r.FormValue("bio")
	if len(bio) > 500 {
		writeResponse(w, http.StatusBadRequest, "Bio cannot exceed 500 characters")
		return
	}

	err := h.services.db.UpdateProfile(userID, avatarURL, bio)
	if err != nil {
		log.Error().Str("userID", userID.String()).Err(err).Msg("Error updating profile")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, "Profile updated successfully")
}

func (h *Handlers) AddPublicKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("AddPublicKey request received")

	userID := h.getUserID(r)
	signedChallenge := r.FormValue("signedChallenge")
	if signedChallenge == "" {
		log.Error().Str("userID", userID.String()).Msg("No signed challenge found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `signedChallenge` is required")
		return

	}

	challenge := extractChallenge(signedChallenge)
	err := validateChallenge(challenge)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("challenge", challenge).
			Err(err).Msg("Error validating challenge")
		writeResponse(w, http.StatusBadRequest, "Invalid challenge, please generate a new one")
		return
	}

	publicKeyArmor := r.FormValue("publicKey")
	if publicKeyArmor == "" {
		log.Error().Str("userID", userID.String()).Msg("No public key found in request")
		writeResponse(w, http.StatusBadRequest, "Argument `publicKey` is required")
		return
	}

	entities, err := h.services.crypto.ExtractEntitiesFromMessage(publicKeyArmor)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("publicKey", publicKeyArmor).
			Err(err).Msg("Error extracting keys from public key")
		writeResponse(w, http.StatusBadRequest, "Invalid public key")
		return
	}

	if len(entities) == 0 {
		log.Error().
			Str("userID", userID.String()).
			Str("publicKey", publicKeyArmor).
			Msg("No entities found in public key")
		writeResponse(w, http.StatusBadRequest, "No entities found in public key")
		return
	}

	// Verify that the clearsigned challenge was signed by the provided public key
	// Create a temporary PublicKey struct for verification
	tempPublicKey := &PublicKey{
		Armor: publicKeyArmor,
	}
	err = h.services.crypto.VerifySignature(signedChallenge, tempPublicKey)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("signedChallenge", signedChallenge).
			Err(err).Msg("Error verifying signature")
		writeResponse(w, http.StatusBadRequest, "Invalid signature: the clearsigned challenge was not signed by the provided public key")
		return
	}

	log.Info().
		Str("userID", userID.String()).
		Msg("Challenge signature verified successfully")

	// Security: Only process the key that was used to sign the challenge
	// If multiple keys are in the keyring, return an error
	if len(entities) > 1 {
		log.Error().
			Str("userID", userID.String()).
			Int("keyCount", len(entities)).
			Msg("Multiple keys found in keyring")
		writeResponse(w, http.StatusBadRequest, "Multiple keys found in keyring. Please upload only the single key that signed the challenge.")
		return
	}

	// Process the single entity
	entity := entities[0]
	if len(entity.Identities) == 0 {
		publicKeyId := fmt.Sprintf("%d", entity.PrimaryKey.KeyId)
		writeResponse(w, http.StatusBadRequest, "Key must have at least one identity: "+publicKeyId)
		return
	}

	fingerprint := hex.EncodeToString(entity.PrimaryKey.Fingerprint)
	keyExists, err := h.services.db.PublicKeyExists(fingerprint)
	if err != nil {
		log.Error().Str("fingerprint", fingerprint).Err(err).Msg("Error checking if public key exists")
		internalServerError(w)
		return
	}
	if keyExists {
		log.Info().
			Str("fingerprint", fingerprint).
			Str("userID", userID.String()).
			Msg("Key with this fingerprint already exists")
		writeResponse(w, http.StatusBadRequest, "Key with this fingerprint already exists")
		return
	}

	creationTime := h.services.crypto.ExtractCreationTime(entity.PrimaryKey)
	keyExpirationTime := h.services.crypto.ExtractKeyExpirationTime(entity, creationTime)

	extractedPublicKeyArmor, err := h.services.crypto.ExtractPublicKeyArmorFromEntity(entity)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("publicKey", publicKeyArmor).
			Err(err).Msg("Error extracting public key armor")
		internalServerError(w)
		return
	}

	publicKey, err := h.services.db.AddPublicKey(fingerprint, userID, creationTime, keyExpirationTime, extractedPublicKeyArmor)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("publicKey", publicKeyArmor).
			Err(err).Msg("Error adding public key")
		internalServerError(w)
		return
	}
	log.Info().
		Str("userID", userID.String()).
		Str("fingerprint", publicKey.Fingerprint).
		Msg("Public key created")

	// If the key has only one identity, we will automatically assign it as
	// the default identity
	for uid := range entity.Identities {
		publicKeyIdentity, err := h.services.db.AddPublicKeyIdentity(publicKey.Fingerprint, uid)
		if err != nil {
			log.Error().
				Str("userID", userID.String()).
				Str("publicKey", publicKeyArmor).
				Err(err).Msg("Error adding public key identity")
			internalServerError(w)
			return
		}
		log.Debug().
			Str("userID", userID.String()).
			Str("publicKeyIdentity", publicKeyIdentity.ID.String()).
			Str("fingerprint", publicKey.Fingerprint).
			Msg("Identity created")
	}

	publicKeys, err := h.services.db.GetPublicKeysByUserID(userID)
	if err != nil {
		log.Error().Str("userID", userID.String()).Err(err).Msg("Error getting public keys for user")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, publicKeys)
}

func (h *Handlers) GetIdentitiesByUserID(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetIdentitiesByUserID request received")

	rawUserID := mux.Vars(r)["userID"]
	if rawUserID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	userID, err := uuid.Parse(rawUserID)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	identities, err := h.services.db.getUserIdentities(userID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Err(err).Msg("Error getting identities")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, identities)
}

func (h *Handlers) SetDefaultIdentity(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("SetDefaultIdentity request received")

	userID := h.getUserID(r)

	rawIdentityID := r.FormValue("identity")
	if rawIdentityID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `identity` is required")
		return
	}

	identityID, err := uuid.Parse(rawIdentityID)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid identity ID")
		return
	}

	publicKey, err := h.services.db.GetPublicKeyForIdentity(userID, identityID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("identityID", identityID.String()).
			Err(err).Msg("Error getting public key for identity")
		internalServerError(w)
		return
	}
	if publicKey == nil {
		writeResponse(w, http.StatusNotFound, "Identity not found")
		return
	}

	err = h.services.db.SetDefaultIdentity(userID, identityID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("identityID", identityID.String()).
			Err(err).Msg("Error setting default identity")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, "Identity set as default successfully")
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
	key, err := h.services.db.GetPublicKey(fingerprint, userID)
	if err != nil {
		writeResponse(w, http.StatusNotFound, "Key '"+fingerprint+"' not found")
		return
	}

	if key == nil {
		key, err = h.services.db.GetServerPublicKey(userID)
		if err != nil {
			log.Error().
				Str("userID", userID.String()).
				Err(err).Msg("Error getting server public key")
			internalServerError(w)
			return
		}

		if key == nil {
			writeResponse(w, http.StatusNotFound, "Key not found")
			return
		}
	}

	writeResponse(w, http.StatusOK, key)
}

func (h *Handlers) DeletePublicKey(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("DeletePublicKey request received")

	userID := h.getUserID(r)
	fingerprint := mux.Vars(r)["fingerprint"]
	if fingerprint == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `fingerprint` is required")
		return
	}

	log.Debug().
		Str("userID", userID.String()).
		Str("fingerprint", fingerprint).
		Msg("Attempting to delete public key")

	err := h.services.db.DeletePublicKey(fingerprint, userID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("fingerprint", fingerprint).
			Err(err).Msg("Error deleting public key")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, "Public key deleted successfully")
}

func (h *Handlers) GetReedsByUserID(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReedsByUserID request received")

	userID, err := uuid.Parse(mux.Vars(r)["userID"])
	if err != nil {
		writeResponse(w, http.StatusNotFound, "User not found")
		return
	}

	reeds, err := h.services.db.GetReedsByUserID(userID)
	if err != nil {
		log.Error().Str("userID", userID.String()).Err(err).Msg("Error getting reeds for user")
		internalServerError(w)
		return
	}

	writeResponse(w, http.StatusOK, reeds)
}

func (h *Handlers) PublishReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("PublishReed request received")

	userID := h.getUserID(r)

	err := r.ParseForm()
	if err != nil {
		log.Error().Str("userID", userID.String()).Err(err).Msg("Error parsing form")
		writeResponse(w, http.StatusBadRequest, "Error parsing form")
		return
	}

	rawIdentityID := r.FormValue("identity")
	if rawIdentityID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `identity` is required")
		return
	}
	identityID := uuid.MustParse(rawIdentityID)
	if identityID == uuid.Nil {
		writeResponse(w, http.StatusNotFound, "Identity not found")
		return
	}

	clearsignedReed := r.FormValue("clearsignedReed")
	if clearsignedReed == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `clearsignedReed` is required")
		return
	}

	// Validate reed headers
	msg := h.services.crypto.ExtractMessageFromSignature(clearsignedReed)
	err = h.services.md.ValidateReedHeader(msg)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Err(err).Msg("Invalid reed header")
		writeResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	// Parse the reed header to verify author matches signed in user
	header := h.services.md.ExtractReedHeader(msg)
	if header.Author == "" {
		log.Error().
			Str("userID", userID.String()).
			Msg("Reed author not found in reed header")
		writeResponse(w, http.StatusBadRequest, "Key `author` not found in header")
		return
	}
	if header.Author != userID.String() {
		log.Error().
			Str("userID", userID.String()).
			Str("headerAuthor", header.Author).
			Msg("Reed author does not match signed in user")
		writeResponse(w, http.StatusBadRequest, "Key `author` does not match signed in user")
		return
	}

	content := h.services.md.ExtractReedContent(msg)
	if len(content) > 2048 {
		log.Error().
			Str("userID", userID.String()).
			Msg("Reed content exceeds 1024 characters")
		writeResponse(w, http.StatusBadRequest, "Reed content exceeds 1024 characters")
		return
	}

	if len(h.services.md.ParseMarkdown(content)) > 140 {
		log.Error().
			Str("userID", userID.String()).
			Msg("Reed content exceeds 140 characters")
		writeResponse(w, http.StatusBadRequest, "Reed content exceeds 140 characters")
		return
	}

	publicKey, err := h.services.db.GetPublicKeyForIdentity(userID, identityID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("identityID", identityID.String()).
			Err(err).Msg("Error getting public key for identity")
		internalServerError(w)
		return
	}

	if publicKey == nil {
		writeResponse(w, http.StatusBadRequest, "Identity not found")
		return
	}

	err = h.services.crypto.VerifySignature(clearsignedReed, publicKey)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Signature verification failed")
		return
	}

	_, serverKey, err := h.services.db.GetServerPrivateKey(userID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Err(err).Msg("Error getting server key")
		internalServerError(w)
		return
	}

	serverSignature, err := h.services.crypto.Sign(clearsignedReed, serverKey)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Err(err).Msg("Error signing reed")
		internalServerError(w)
		return
	}
	log.Debug().
		Str("serverSignature", serverSignature).
		Msg("Reed signed successfully")

	reed, err := h.services.db.CreateReed(userID, identityID)
	if err != nil {
		log.Error().Str("userID", userID.String()).Err(err).Msg("Error publishing reed")
		internalServerError(w)
		return
	}

	log.Debug().
		Str("userID", userID.String()).
		Str("reedID", reed.ID.String()).
		Msg("Reed created successfully")

	signedReed := SignedReed{
		ID:       reed.ID,
		UserID:   reed.UserID,
		SignedAt: reed.CreatedAt,
		Identity: reed.Identity,

		UserFingerprint:   reed.UserFingerprint,
		ServerFingerprint: reed.ServerFingerprint,

		Signature: serverSignature,
	}

	writeResponse(w, http.StatusCreated, signedReed)
}

func (h *Handlers) DeleteReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("DeleteReed request received")

	userID := h.getUserID(r)
	rawReedID := mux.Vars(r)["reedID"]
	if rawReedID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	reedID, err := uuid.Parse(rawReedID)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid reed ID format")
		return
	}

	// Check if reed exists and belongs to user
	reed, err := h.services.db.GetReed(userID, reedID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("reedID", reedID.String()).
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
			Str("userID", userID.String()).
			Str("reedID", reedID.String()).
			Str("reedUserID", reed.UserID.String()).
			Msg("User does not own this reed")
		writeResponse(w, http.StatusForbidden, "You can only delete your own reeds")
		return
	}

	// Delete the reed
	err = h.services.db.DeleteReed(reedID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("reedID", reedID.String()).
			Err(err).Msg("Error deleting reed")
		internalServerError(w)
		return
	}

	log.Info().
		Str("userID", userID.String()).
		Str("reedID", reedID.String()).
		Msg("Reed deleted successfully")

	writeResponse(w, http.StatusOK, "Reed deleted successfully")
}

func (h *Handlers) GetReed(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("GetReed request received")

	userID := h.getUserID(r)
	rawReedID := mux.Vars(r)["reedID"]
	if rawReedID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	reedID, err := uuid.Parse(rawReedID)
	if err != nil {
		writeResponse(w, http.StatusNotFound, "Post not found")
		return
	}

	reed, err := h.services.db.GetReed(userID, reedID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("reedID", reedID.String()).
			Err(err).Msg("Error getting post")
		internalServerError(w)
		return
	}
	if reed == nil {
		writeResponse(w, http.StatusNotFound, "Post not found")
		return
	}

	log.Debug().
		Str("userID", userID.String()).
		Str("reedID", reedID.String()).
		Msg("Post found")

	writeResponse(w, http.StatusOK, reed)
}

func (h *Handlers) VerifySignature(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	log.Info().Msg("VerifySignature request received")

	// Get userID from URL path
	rawUserID := mux.Vars(r)["userID"]
	if rawUserID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `userID` is required")
		return
	}

	userID, err := uuid.Parse(rawUserID)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	// Get reedID from URL path
	rawReedID := mux.Vars(r)["reedID"]
	if rawReedID == "" {
		writeResponse(w, http.StatusBadRequest, "Argument `reedID` is required")
		return
	}

	reedID, err := uuid.Parse(rawReedID)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, "Invalid reed ID")
		return
	}

	// Parse multipart form data to get signature
	err = r.ParseMultipartForm(32 << 20) // 32 MB max memory
	if err != nil {
		log.Error().Err(err).Msg("Error parsing multipart form data")
		writeResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	signature := r.FormValue("signature")
	if signature == "" {
		log.Error().
			Str("userID", userID.String()).
			Str("reedID", reedID.String()).
			Msg("No signature provided in form data")
		writeResponse(w, http.StatusBadRequest, "Signature is required")
		return
	}

	// Get the reed from database to get the user fingerprint
	reed, err := h.services.db.GetReed(userID, reedID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("reedID", reedID.String()).
			Err(err).Msg("Error getting reed from database")
		internalServerError(w)
		return
	}
	if reed == nil {
		log.Error().
			Str("userID", userID.String()).
			Str("reedID", reedID.String()).
			Msg("Reed not found")
		writeResponse(w, http.StatusNotFound, "Reed not found")
		return
	}

	// Get the public key for verification
	publicKey, err := h.services.db.GetServerPublicKey(reed.UserID)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("reed.userID", reed.UserID.String()).
			Str("fingerprint", reed.UserFingerprint).
			Err(err).Msg("Error getting public key for verification")
		internalServerError(w)
		return
	}
	if publicKey == nil {
		log.Error().
			Str("userID", userID.String()).
			Str("fingerprint", reed.UserFingerprint).
			Msg("Public key not found for verification")
		writeResponse(w, http.StatusNotFound, "Public key not found")
		return
	}

	// Verify the signature using the crypto service
	err = h.services.crypto.VerifySignature(signature, publicKey)
	if err != nil {
		log.Error().
			Str("userID", userID.String()).
			Str("reedID", reedID.String()).
			Err(err).Msg("Signature verification failed")
		writeResponse(w, http.StatusBadRequest, "Signature verification failed")
		return
	}

	writeResponse(w, http.StatusOK, "Signature verification successful")
}
