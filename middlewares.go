package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// contextKey is a type for context keys to avoid collisions
type contextKey string

const requestIDKey contextKey = "request_id"

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
		userId := r.Context().Value(requestIDKey).(string)
		if userId != "" {
			entry = entry.Str("userID", userId)
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

// Authentication middleware
func (h *Handlers) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		excludePaths := []string{
			"/",
			"/api/users/login",
			"/api/users/reset-password",
			"/api/users/reset-password/nonce",
			"/api/users/signup",
		}

		for _, path := range excludePaths {
			if r.URL.Path == path {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Get session and check if user is logged in
		session := h.getSession(r)
		if session.Values["userID"] == nil {
			log.Error().Msg("User ID not found in session")
			writeResponse(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		next.ServeHTTP(w, r)
	})
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

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
