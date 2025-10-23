package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
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
	statusCode    int
	wroteHeaders  bool
	bodyBuffer    *bytes.Buffer
	responseSent  bool
	cryptoService *CryptoService
	dataService   *DataService
	userID        string
	passphrase    string
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
	rs.ResponseWriter.Header().Set("X-Syrinx-Algorithm", "PGP")
	rs.ResponseWriter.Header().Set("X-Syrinx-Signature-Scope", "body")

	return nil
}

// getServerPrivateKey retrieves the server's private key from the file system
func (rs *responseSigner) getServerPrivateKey() (string, error) {
	// Read the private key from the file system
	privateKeyPath := "./keys/syrinx.private.pgp"

	// Check if file exists
	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		log.Error().
			Str("path", privateKeyPath).
			Err(err).
			Msg("Server private key file not found")
		return "", fmt.Errorf("server private key not found")
	}

	// Read the private key file
	privateKeyBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return "", fmt.Errorf("failed to read private key file: %w", err)
	}

	// If passphrase is provided, decrypt the key
	if rs.passphrase != "" {
		decryptedKey, err := rs.decryptPrivateKey(string(privateKeyBytes), rs.passphrase)
		if err != nil {
			log.Error().
				Err(err).
				Msg("Failed to decrypt private key with passphrase")
			return "", fmt.Errorf("failed to decrypt private key: %w", err)
		}
		return decryptedKey, nil
	}

	// Return raw key content if no passphrase
	return string(privateKeyBytes), nil
}

// decryptPrivateKey decrypts a passphrase-protected private key
func (rs *responseSigner) decryptPrivateKey(encryptedKey, passphrase string) (string, error) {
	// Parse the armored private key
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(encryptedKey))
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	if len(entities) == 0 {
		return "", fmt.Errorf("no entities found in private key")
	}

	entity := entities[0]

	// Decrypt the private key with the passphrase
	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		err = entity.PrivateKey.Decrypt([]byte(passphrase))
		if err != nil {
			return "", fmt.Errorf("failed to decrypt private key: %w", err)
		}
	}

	// Serialize the decrypted private key
	var buf bytes.Buffer
	err = entity.SerializePrivate(&buf, nil)
	if err != nil {
		return "", fmt.Errorf("failed to serialize decrypted private key: %w", err)
	}

	// Armor encode the decrypted private key
	var armoredBuf bytes.Buffer
	armorWriter, err := armor.Encode(&armoredBuf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create armor encoder: %w", err)
	}

	_, err = armorWriter.Write(buf.Bytes())
	if err != nil {
		armorWriter.Close()
		return "", fmt.Errorf("failed to write armored private key: %w", err)
	}

	err = armorWriter.Close()
	if err != nil {
		return "", fmt.Errorf("failed to close armor encoder: %w", err)
	}

	return armoredBuf.String(), nil
}

// signDetached creates a detached signature of the message
func (rs *responseSigner) signDetached(message, privateKey string) (string, error) {
	// Parse the armored private key
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(privateKey))
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	if len(entities) == 0 {
		return "", fmt.Errorf("no entities found in private key")
	}

	entity := entities[0]

	// Create detached signature
	var signatureBuf bytes.Buffer
	messageReader := strings.NewReader(message)

	err = openpgp.DetachSign(&signatureBuf, entity, messageReader, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create detached signature: %w", err)
	}

	// Armor encode the signature
	var armoredSignature bytes.Buffer
	armorWriter, err := armor.Encode(&armoredSignature, openpgp.SignatureType, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create signature armor encoder: %w", err)
	}

	_, err = armorWriter.Write(signatureBuf.Bytes())
	if err != nil {
		armorWriter.Close()
		return "", fmt.Errorf("failed to write armored signature: %w", err)
	}

	err = armorWriter.Close()
	if err != nil {
		return "", fmt.Errorf("failed to close signature armor encoder: %w", err)
	}

	// Strip armor delimiters to reduce payload size
	signature := armoredSignature.String()
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

func acceptMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acceptHeader := r.Header.Get("Accept")
		acceptAll := strings.Contains(acceptHeader, "*/*")
		acceptHtml := strings.Contains(acceptHeader, "text/html")
		if acceptHeader != "" && !acceptAll && !acceptHtml {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotAcceptable)
			fmt.Fprintf(w, "Content type %s not supported", acceptHeader)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Signature-based authentication middleware
func (h *Handlers) signatureAuthMiddleware(prefix string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Info().
				Msg("signatureAuthMiddleware()")
			excludePaths := []string{
				prefix + "/users/login",
				prefix + "/users/reset-password",
				prefix + "/users/reset-password/nonce",
				prefix + "/users/signup",
				prefix + "/check-username",
			}

			for _, path := range excludePaths {
				if r.URL.Path == path {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Extract requred authentication headers
			userID := r.Header.Get("X-Syrinx-User-Id")
			fingerprintHeader := r.Header.Get("X-Syrinx-Fingerprint")
			signatureHeader := r.Header.Get("X-Syrinx-Signature")
			algorithmHeader := r.Header.Get("X-Syrinx-Algorithm")
			signatureScopeHeader := r.Header.Get("X-Syrinx-Signature-Scope")
			timestampHeader := r.Header.Get("X-Syrinx-Timestamp")

			if userID == "" || fingerprintHeader == "" || signatureHeader == "" || algorithmHeader == "" || signatureScopeHeader == "" || timestampHeader == "" {
				log.Error().
					Str("userID", userID).
					Str("fingerprint", fingerprintHeader).
					Bool("hasSignature", signatureHeader != "").
					Str("algorithm", algorithmHeader).
					Str("signatureScope", signatureScopeHeader).
					Str("timestamp", timestampHeader).
					Msg("Missing authentication headers")
				writeResponse(w, http.StatusBadRequest, "Missing authentication headers")
				return
			}

			// Validate algorithm
			if algorithmHeader != "PGP" {
				log.Error().
					Str("algorithm", algorithmHeader).
					Msg("Unsupported signature algorithm")
				writeResponse(w, http.StatusBadRequest, "Unsupported signature algorithm")
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

			// Get public key for the user and fingerprint
			publicKey, err := h.services.db.GetPublicKey(userID, fingerprintHeader)
			if err != nil {
				log.Error().
					Str("userID", userID).
					Str("fingerprint", fingerprintHeader).
					Err(err).
					Msg("Error retrieving public key")
				internalServerError(w)
				return
			}

			if publicKey == nil {
				log.Error().
					Str("userID", userID).
					Str("fingerprint", fingerprintHeader).
					Msg("Public key not found")
				writeResponse(w, http.StatusBadRequest, "Invalid fingerprint")
				return
			}

			// Verify signature
			if err := h.verifyRequestSignature(r, signatureHeader, publicKey.Armor); err != nil {
				log.Error().
					Str("userID", userID).
					Str("fingerprint", fingerprintHeader).
					Err(err).
					Msg("Signature verification failed")
				writeResponse(w, http.StatusUnauthorized, "Signature verification failed")
				return
			}

			// Add user ID to request context for downstream handlers
			log.Info().
				Str("userID", userID).
				Interface("contextKey", userIDKey).
				Msg("Setting user ID in context")
			ctx := context.WithValue(r.Context(), userIDKey, userID)
			r = r.WithContext(ctx)

			next.ServeHTTP(w, r)
		})
	}
}

// verifyRequestSignature verifies the signature of the request
func (h *Handlers) verifyRequestSignature(r *http.Request, signature, publicKey string) error {
	// Build the canonical request string (method + path + headers + body)
	requestString := h.buildCanonicalRequestString(r)

	// Unescape newlines from the signature
	unescapedSignature := strings.ReplaceAll(signature, "\\n", "\n")

	// Add armor delimiters back to the signature
	armoredSignature := "-----BEGIN PGP SIGNATURE-----\n\n" + unescapedSignature + "\n-----END PGP SIGNATURE-----"

	log.Info().
		Str("requestString", requestString).
		Msg("Verifying signature")

	// Use the existing CryptoService method to verify the signature
	return h.services.crypto.VerifySignature(requestString, armoredSignature, publicKey)
}

// buildCanonicalRequestString creates a canonical representation of the request for signing
// Only signs method + path + query + body + timestamp (no headers)
func (h *Handlers) buildCanonicalRequestString(r *http.Request) string {
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

	builder.WriteString("\n")
	builder.WriteString(bodyString)

	// Add timestamp for replay protection
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
	// Parse timestamp
	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp format: %w", err)
	}

	// Get current time
	now := time.Now().Unix()

	// Check if timestamp is within acceptable window (±5 minutes = 300 seconds)
	const timeWindow = 300
	timeDiff := now - timestamp

	if timeDiff > timeWindow || timeDiff < -timeWindow {
		return fmt.Errorf("timestamp too old or too far in future: diff=%d seconds, window=%d", timeDiff, timeWindow)
	}

	return nil
}

func (h *Handlers) CORSMiddleware(next http.Handler) http.Handler {
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
		"hx-trigger-name",
		"hx-trigger-value",
		"hx-trigger",
		"X-Requested-With",
		"X-Syrinx-User-Id",
		"X-Syrinx-Fingerprint",
		"X-Syrinx-Signature",
		"X-Syrinx-Algorithm",
		"X-Syrinx-Signature-Scope",
		"X-Syrinx-Timestamp",
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// For credentials to work, we can't use "*" as origin
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "http://localhost:8001" // Default to frontend port
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Expose-Headers", "Signature, X-Syrinx-Algorithm")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// responseSignerMiddleware wraps responses to sign the complete response before it's sent
func (h *Handlers) responseSignerMiddleware(passphrase string) func(http.Handler) http.Handler {
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
				ResponseWriter: w,
				statusCode:     http.StatusOK,
				cryptoService:  h.services.crypto,
				dataService:    h.services.db,
				userID:         userID.(string),
				passphrase:     passphrase,
			}

			// Continue with wrapped writer
			next.ServeHTTP(signer, r)

			// Flush the response (sign and send)
			signer.Flush()
		})
	}
}
