//go:build !ops && !ripplescleanup

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"syrinx/crypto"
	"syrinx/encoding"
	"syrinx/identity"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	return rw.ResponseWriter.Write(b)
}

// responseSigner wraps http.ResponseWriter to capture and sign the entire response
type responseSigner struct {
	http.ResponseWriter

	statusCode      int
	wroteHeaders    bool
	bodyBuffer      *bytes.Buffer
	responseSent    bool
	cryptoService   crypto.Crypto
	dataService     *DataService
	userID          string
	signingKeyArmor string
}

// Header returns the header map (delegates to underlying ResponseWriter)
func (rs *responseSigner) Header() http.Header {
	return rs.ResponseWriter.Header()
}

// WriteHeader captures the status code but doesn't write headers yet
func (rs *responseSigner) WriteHeader(statusCode int) {
	if rs.wroteHeaders {
		return // Headers already written
	}

	rs.statusCode = statusCode
	// Don't write headers yet - we'll do it after we have the complete response
}

// Write buffers the response body
func (rs *responseSigner) Write(b []byte) (int, error) {
	// Only set default status code if WriteHeader was never called
	if rs.statusCode == 0 {
		rs.statusCode = http.StatusOK
	}

	// Initialize body buffer if this is the first write
	if rs.bodyBuffer == nil {
		rs.bodyBuffer = &bytes.Buffer{}
	}

	// Buffer the body
	rs.bodyBuffer.Write(b)

	return len(b), nil
}

// Flush sends the complete response with signature
func (rs *responseSigner) Flush() {
	if rs.responseSent {
		return
	}

	// Sign the complete response (headers + body)
	if err := rs.signCompleteResponse(); err != nil {
		log.Error().
			Err(err).
			Msg("Failed to sign complete response")
		rs.statusCode = http.StatusInternalServerError
	}

	// Write headers
	rs.ResponseWriter.WriteHeader(rs.statusCode)
	rs.wroteHeaders = true

	// Write body
	if rs.bodyBuffer != nil {
		rs.ResponseWriter.Write(rs.bodyBuffer.Bytes())
	}

	rs.responseSent = true
}

// signCompleteResponse creates a signature of the entire response (headers + body)
func (rs *responseSigner) signCompleteResponse() error {
	// Get all headers
	headers := rs.ResponseWriter.Header()

	// Set Content-Length header if we have a body
	if rs.bodyBuffer != nil && rs.bodyBuffer.Len() > 0 {
		headers.Set("Content-Length", fmt.Sprintf("%d", rs.bodyBuffer.Len()))
	}

	// Build a canonical representation of headers
	headerString := buildCanonicalHeaderString(headers)

	// Get the response body
	var bodyString string
	if rs.bodyBuffer != nil {
		bodyString = rs.bodyBuffer.String()
	}

	// Create the complete response string (headers + body)
	completeResponse := headerString + "\n\n" + bodyString

	// Get server's private key for this user
	privateKey, err := rs.getServerPrivateKey()
	if err != nil {
		return fmt.Errorf("failed to get private key: %w", err)
	}

	if privateKey == "" {
		// No private key available, skip signing
		log.Warn().
			Str("userID", rs.userID).
			Msg("No private key available for signing complete response")
		return nil
	}

	// Sign the complete response with detached signature
	signature, err := rs.signDetached(completeResponse, privateKey)
	if err != nil {
		log.Error().
			Err(err).Msg("Failed to sign complete response")
		return fmt.Errorf("failed to sign complete response: %w", err)
	}

	// Escape newlines for safe HTTP header transmission
	escapedSignature := strings.ReplaceAll(signature, "\n", "\\n")

	// Add signature to headers (stripped of armor delimiters)
	rs.ResponseWriter.Header().Set("Signature", escapedSignature)
	rs.ResponseWriter.Header().Set("X-Syrinx-Signature-Scope", "body")

	return nil
}

// getServerPrivateKey returns the server's signing key (already decrypted, loaded at startup)
func (rs *responseSigner) getServerPrivateKey() (string, error) {
	return rs.signingKeyArmor, nil
}

// signDetached creates a detached signature of the message
func (rs *responseSigner) signDetached(message, privateKey string) (string, error) {
	// Use crypto service to sign the message
	signature, err := rs.cryptoService.Sign(message, privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign message: %w", err)
	}

	// Strip armor delimiters to reduce payload size (HTTP-specific formatting)
	signature = stripArmorDelimiters(signature)

	return signature, nil
}

// stripArmorDelimiters removes PGP armor delimiters from a signature
func stripArmorDelimiters(signature string) string {
	lines := strings.Split(signature, "\n")
	var result []string

	for _, line := range lines {
		// Skip armor delimiters and empty lines
		if strings.HasPrefix(line, "-----BEGIN PGP SIGNATURE-----") ||
			strings.HasPrefix(line, "-----END PGP SIGNATURE-----") ||
			line == "" {
			continue
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// buildCanonicalHeaderString creates a consistent string representation of headers
// Headers are sorted alphabetically for consistency
func buildCanonicalHeaderString(headers http.Header) string {
	// Get all header names and sort them
	headerNames := make([]string, 0, len(headers))
	for headerName := range headers {
		headerNames = append(headerNames, headerName)
	}
	sort.Strings(headerNames)

	// Build canonical string
	var builder strings.Builder
	for i, name := range headerNames {
		if i > 0 {
			builder.WriteString("\n")
		}

		// Get all values for this header
		values := headers.Values(name)
		sort.Strings(values) // Sort values for consistency

		builder.WriteString(name)
		builder.WriteString(": ")
		builder.WriteString(strings.Join(values, ", "))
	}

	return builder.String()
}

// loggingMiddleware logs all HTTP requests with status codes and URLs
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Generate request ID and add to context
		requestID := uuid.New().String()
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		r = r.WithContext(ctx)

		// Wrap the response writer to capture status code
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // default status
		}

		// Process the request
		next.ServeHTTP(wrapped, r)

		// Log the request details
		duration := time.Since(start)
		entry := log.Info().
			Str("request_id", requestID).
			Str("method", r.Method).
			Str("url", r.URL.String()).
			Str("path", r.URL.Path).
			Str("remote_addr", r.RemoteAddr).
			Str("user_agent", r.UserAgent()).
			Int("status_code", wrapped.statusCode).
			Dur("duration", duration)

		// Add user ID to log if available in context
		if userID, ok := r.Context().Value(userIDKey).(string); ok {
			entry = entry.Str("userID", userID)
		}

		entry.Msg("Request completed")
	})
}

// authenticateAsPeer verifies a request signed by a peer's own key,
// fetched live since peer keys aren't stored locally.
func (h *Handlers) authenticateAsPeer(w http.ResponseWriter, r *http.Request, next http.Handler, fingerprint, callerServerID, signatureHeader string) {
	ok, err := h.services.db.VerifyFederationPeer(r.Context(), callerServerID, fingerprint)
	if err != nil {
		internalServerError(w)
		return
	}
	if !ok {
		writeResponse(w, http.StatusForbidden, "Not an established peer")
		return
	}

	peer, err := h.services.db.GetServerByID(r.Context(), callerServerID)
	if err != nil {
		internalServerError(w)
		return
	}
	if peer == nil {
		writeResponse(w, http.StatusForbidden, "Not an established peer")
		return
	}
	publicKeyArmor, err := h.fetchPeerServerKeyArmor(r.Context(), peer.BaseURL, callerServerID, fingerprint)
	if err != nil || publicKeyArmor == "" {
		writeResponse(w, http.StatusForbidden, "Not an established peer")
		return
	}

	if err := h.verifyRequestSignature(r, signatureHeader, publicKeyArmor); err != nil {
		writeResponse(w, http.StatusUnauthorized, "Request signature verification failed")
		return
	}

	ctx := context.WithValue(r.Context(), peerServerIDKey, callerServerID)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// Signature-based authentication middleware
func (h *Handlers) signatureAuthMiddleware(prefix string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Info().
				Msg("signatureAuthMiddleware()")
			excludePaths := []string{
				prefix + "/users/login",
				prefix + "/users/id",
				prefix + "/users/signup",
				prefix + "/users/status",
				prefix + "/check-username",
				prefix + "/keys",
				prefix + "/server/info",
				// GetServerKey only ever returns this server's own key, so
				// it's safe to leave unauthenticated.
				prefix + "/server/key",
				prefix + "/recovery/identity/claim",
				prefix + "/account-recovery/challenge",
				prefix + "/account-recovery/bootstrap",
				prefix + "/invites/check",
			}
			// /federation/connect/ is the initiator's callback route — no
			// local session, the invitation secret proves legitimacy.
			excludePrefixes := []string{
				prefix + "/federation/connect/",
			}

			for _, path := range excludePaths {
				if r.URL.Path == path {
					next.ServeHTTP(w, r)
					return
				}
			}
			for _, p := range excludePrefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Canonical id of the key that signed the request — local
			// user's own key or a federated peer's own key (see proxyToPeer).
			publicKeyIDHeader := r.Header.Get("X-Syrinx-Public-Key-Id")
			signatureHeader := r.Header.Get("X-Syrinx-Signature")
			signatureScopeHeader := r.Header.Get("X-Syrinx-Signature-Scope")
			timestampHeader := r.Header.Get("X-Syrinx-Timestamp")

			if publicKeyIDHeader == "" || signatureHeader == "" || signatureScopeHeader == "" || timestampHeader == "" {
				log.Error().
					Str("publicKeyId", publicKeyIDHeader).
					Bool("hasSignature", signatureHeader != "").
					Str("signatureScope", signatureScopeHeader).
					Str("timestamp", timestampHeader).
					Str("path", r.URL.Path).
					Msg("Missing authentication headers")
				writeResponse(w, http.StatusBadRequest, "Missing authentication headers")
				return
			}

			// Validate signature scope
			if signatureScopeHeader != "body" {
				log.Error().
					Str("signatureScope", signatureScopeHeader).
					Msg("Invalid signature scope")
				writeResponse(w, http.StatusBadRequest, "Invalid signature scope")
				return
			}

			// Validate timestamp for replay protection
			if err := h.validateTimestamp(timestampHeader); err != nil {
				log.Error().
					Str("timestamp", timestampHeader).
					Err(err).
					Msg("Invalid timestamp")
				writeResponse(w, http.StatusBadRequest, "Invalid timestamp")
				return
			}

			publicKey, err := h.services.db.GetPublicKey(r.Context(), publicKeyIDHeader)
			if err != nil {
				log.Error().
					Str("publicKeyId", publicKeyIDHeader).
					Err(err).
					Msg("Error retrieving public key")
				internalServerError(w)
				return
			}
			// No local row — could be a foreign peer's own key (never
			// stored locally), so try authenticateAsPeer before giving up.
			if publicKey == nil {
				fingerprint, callerServerID, ok := identity.ParseIdentityID(identity.IdentityID(publicKeyIDHeader))
				if ok && !strings.Contains(publicKeyIDHeader, "/") {
					h.authenticateAsPeer(w, r, next, fingerprint, callerServerID, signatureHeader)
					return
				}
				log.Error().
					Str("publicKeyId", publicKeyIDHeader).
					Msg("Public key not found")
				writeResponse(w, http.StatusForbidden, "Key not found")
				return
			}

			// Reject requests signed by a revoked key.
			//
			// Threat model: an attacker who compromises an old key of a user
			// whose current key is a newer, uncompromised one must not be able
			// to act as that user. The user's revocation record is what stops
			// them — but only if the server refuses to honor signatures made
			// with any revoked key on every new authenticated operation.
			//
			// Historical artifacts (a profile record or reed that was signed
			// while the key was still active) remain valid: they are frozen
			// bytes. What we forbid is producing *new* signed operations with
			// a revoked key — updating the profile, publishing reeds,
			// following/unfollowing, opening subscriptions, etc.
			//
			// The rotation flow is unaffected: AddPublicKey is on the
			// unauthenticated `/keys` path (see excludePaths above), and
			// RevokeKey is signed by the key being revoked *before* the
			// revocations row is inserted, so this check sees it as still
			// active.
			if publicKey.Revoked {
				log.Error().
					Str("publicKeyId", publicKeyIDHeader).
					Msg("Request signed by revoked key rejected")
				writeResponse(w, http.StatusUnauthorized, "Key is revoked")
				return
			}

			// publicKey.UserID is empty when the caller signed with this
			// server's own key rather than a user key — skip the
			// account-removal check in that case.
			userID := publicKey.UserID
			if userID != "" {
				// Account-removed users may only replay DELETE /users/me
				// (idempotent cert fetch). All other authenticated actions
				// are forbidden.
				removed, remErr := h.services.db.HasAccountRemoval(r.Context(), userID)
				if remErr != nil {
					log.Error().Str("userID", userID).Err(remErr).Msg("Error checking account removal")
					internalServerError(w)
					return
				}
				if removed {
					path := r.URL.Path
					if !(r.Method == http.MethodDelete && (path == prefix+"/users/me" || strings.HasSuffix(path, "/users/me"))) {
						log.Info().Str("userID", userID).Str("path", path).Msg("Rejected auth for removed account")
						writeResponse(w, http.StatusGone, "Account removed")
						return
					}
				}
			}

			// Verify signature
			if err := h.verifyRequestSignature(r, signatureHeader, publicKey.Armor); err != nil {
				log.Error().
					Str("publicKeyId", publicKeyIDHeader).
					Err(err).
					Msg("Request signature verification failed")
				writeResponse(w, http.StatusUnauthorized, "Request signature verification failed")
				return
			}

			ctx := r.Context()
			if userID != "" {
				ctx = context.WithValue(ctx, userIDKey, userID)
			} else {
				ctx = context.WithValue(ctx, peerServerIDKey, h.services.db.GetServerID())
			}
			r = r.WithContext(ctx)

			next.ServeHTTP(w, r)
		})
	}
}

// verifyRequestSignature verifies the signature of the request
func (h *Handlers) verifyRequestSignature(r *http.Request, signature, publicKey string) error {
	// Build the canonical request string (method + path + headers + body)
	requestString := h.buildCanonicalRequestString(r)

	// Decode base64 signature to get the armored signature
	decodedSignature, err := encoding.Base64Decode(signature)
	if err != nil {
		return fmt.Errorf("failed to decode base64 signature: %w", err)
	}

	// Use the existing CryptoService method to verify the signature
	return h.services.crypto.VerifySignature(requestString, decodedSignature, publicKey)
}

// buildCanonicalRequestString creates a canonical representation of the request for signing
// Only signs method + path + query + body + timestamp (no headers)
func (h *Handlers) buildCanonicalRequestString(r *http.Request) string {
	// Always add body (even if empty) - this ensures there's always something to sign
	var bodyString string
	if r.Body != nil {
		contentType := r.Header.Get("Content-Type")

		// Handle different content types
		if strings.Contains(contentType, "application/x-www-form-urlencoded") {
			// For urlencoded forms, use the raw body for signature verification
			bodyBytes, err := io.ReadAll(r.Body)
			if err == nil {
				bodyString = string(bodyBytes)
				// Reset body for downstream handlers
				r.Body = io.NopCloser(strings.NewReader(bodyString))
			}
		} else {
			// For other content types, read the raw body
			bodyBytes, err := io.ReadAll(r.Body)
			if err == nil {
				bodyString = string(bodyBytes)
				// Reset body for downstream handlers
				r.Body = io.NopCloser(strings.NewReader(bodyString))
			}
		}
	}

	var builder strings.Builder
	// Add method and path
	builder.WriteString(r.Method)
	builder.WriteString(" ")
	builder.WriteString(r.URL.Path)
	if r.URL.RawQuery != "" {
		builder.WriteString("?")
		builder.WriteString(r.URL.RawQuery)
	}
	builder.WriteString("\n")

	// Add body
	builder.WriteString("\n")
	builder.WriteString(bodyString)

	// Get timestamp for replay protection
	timestamp := r.Header.Get("X-Syrinx-Timestamp")
	if timestamp != "" {
		builder.WriteString("\n\n")
		builder.WriteString(timestamp)
	}

	return builder.String()
}

// validateTimestamp validates the timestamp for replay protection
// Accepts timestamps within ±5 minutes of current time
func (h *Handlers) validateTimestamp(timestampStr string) error {
	return h.services.crypto.ValidateTimestamp(timestampStr)
}

func (h *Handlers) CORSMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		allowedHeaders := []string{
			"Authorization",
			"Content-Type",
			"hx-boost",
			"hx-current-url",
			"hx-push-url",
			"hx-replace-url",
			"hx-request",
			"hx-select",
			"hx-swap",
			"hx-target",
			"hx-trigger",
			"hx-trigger-name",
			"hx-trigger-value",
			"X-Requested-With",
			"X-Syrinx-Device-Id",
			"X-Syrinx-Public-Key-Id",
			"X-Syrinx-Signature",
			"X-Syrinx-Signature-Scope",
			"X-Syrinx-Timestamp",
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, QUERY, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Expose-Headers", "Signature")

			// Handle preflight requests
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ===================== //
//   Device middleware   //
// ===================== //

type deviceErrorBody struct {
	Error string `json:"error"`
}

func writeDeviceError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(deviceErrorBody{Error: message})
}

func (h *Handlers) deviceMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			if r.URL.Path == "/api/users/device" {
				next.ServeHTTP(w, r)
				return
			}

			userID, ok := r.Context().Value(userIDKey).(string)
			if !ok || userID == "" {
				next.ServeHTTP(w, r)
				return
			}

			if err := h.services.db.CheckActiveDevice(r.Context(), userID, r.Header.Get("X-Syrinx-Device-Id")); err != nil {
				if err == errDeviceMismatch || err == identity.ErrMissingDevice {
					writeDeviceError(w, http.StatusForbidden, "Device mismatch: this session is not bound to the active device.")
					return
				}
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// responseSignerMiddleware wraps responses to sign the complete response before it's sent.
// signingKeyArmor is the decrypted server private key, loaded once at startup.
func (h *Handlers) responseSignerMiddleware(signingKeyArmor string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get user ID from context (set by signature auth middleware)
			userID := r.Context().Value(userIDKey)
			if userID == nil {
				// No user ID in context, skip signing and use regular response writer
				next.ServeHTTP(w, r)
				return
			}

			// Wrap the response writer for authenticated requests
			signer := &responseSigner{
				ResponseWriter:  w,
				statusCode:      http.StatusOK,
				cryptoService:   h.services.crypto,
				dataService:     h.services.db,
				userID:          userID.(string),
				signingKeyArmor: signingKeyArmor,
			}

			// Continue with wrapped writer
			next.ServeHTTP(signer, r)

			// Flush the response (sign and send)
			signer.Flush()
		})
	}
}
