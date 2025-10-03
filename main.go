package main

import (
	"context"
	"database/sql"
	"fmt"
	l "log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"
	"github.com/tooxie/env"
)

// Config holds all configuration for the application
type AppConfig struct {
	// Database configuration
	DBHost     string `env:"name='DB_HOST'"`
	DBPort     string `env:"name='DB_PORT'"`
	DBUser     string `env:"name='DB_USER'"`
	DBPassword string `env:"name='DB_PASSWORD'"`
	DBName     string `env:"name='DB_NAME'"`
	DBSSLMode  string `env:"name='DB_SSLMODE'"`

	// Server configuration
	Port          int
	SecureCookies bool   `env:"name='SECURE_COOKIES'"`
	SecretKey     string `env:"name='SESSION_SECRET_KEY'"`
	CORSOrigin    string `env:"name='CORS_ORIGIN'"`
	IdentityName  string `env:"name='SERVER_IDENTITY_NAME'"`
	IdentityEmail string `env:"name='SERVER_IDENTITY_EMAIL'"`
}

func main() {
	var appConfig AppConfig
	cfg := env.MustAssert(appConfig)

	log.Info().Msg("Starting Syrinx API...")
	SetupLogger()
	log.Info().Msg("[OK] Logger setup successful")

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBSSLMode)

	log.Debug().Msg("Checking for connectivity to database...")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to connect to database")
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to ping database")
	}
	log.Info().Msg("[OK] Database connection successful")

	log.Debug().Msg("Initializing database tables...")
	if err := InitDB(db); err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to initialize database")
	}
	log.Info().Msg("[OK] Database tables initialized successfully")

	log.Debug().Msg("Initializing services...")

	// Wrap database with instrumentation
	userService := NewDataService(db)
	cryptoService := NewCryptoService()
	markdownService := NewMarkdownService()
	services := &Services{
		db:     userService,
		crypto: cryptoService,
		md:     markdownService,
	}
	log.Info().Msg("[OK] Services initialized successfully")

	log.Debug().Msg("Initializing handlers...")
	h := NewHandlers(services, cfg)
	log.Info().Msg("[OK] Handlers initialized successfully")

	log.Debug().Msg("Setting up router...")
	router := mux.NewRouter()

	// Create API subrouter
	api := router.PathPrefix("/api").Subrouter()

	// Middlewares
	api.Use(loggingMiddleware)
	// api.Use(acceptMiddleware)
	api.Use(h.CORSMiddleware)
	api.Use(h.authMiddleware)

	// User routes
	api.HandleFunc("/users/signup", h.Signup).Methods("POST")
	api.HandleFunc("/users/signup", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/login", h.Login).Methods("POST")
	api.HandleFunc("/users/login", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/logout", h.Logout).Methods("POST")
	api.HandleFunc("/users/logout", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/me", h.WhoAmI).Methods("GET")
	api.HandleFunc("/users/me", h.DeleteMe).Methods("DELETE")
	api.HandleFunc("/users/me", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/reset-password/nonce", h.GetNonce).Methods("POST")
	api.HandleFunc("/users/reset-password", h.ResetPassword).Methods("POST")
	api.HandleFunc("/users/reset-password", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/profile", h.GetProfileByUserID).Methods("GET")
	api.HandleFunc("/users/{userID}/profile", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/reeds", h.GetReedsByUserID).Methods("GET")
	api.HandleFunc("/users/{userID}/reeds", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/reeds/{reedID}", h.GetReed).Methods("GET")
	api.HandleFunc("/users/{userID}/reeds/{reedID}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/reeds/{reedID}/verify", h.VerifySignature).Methods("POST")
	api.HandleFunc("/users/{userID}/reeds/{reedID}/verify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/keys/{fingerprint}", h.GetPublicKey).Methods("GET")
	api.HandleFunc("/users/{userID}/keys/{fingerprint}", h.DeletePublicKey).Methods("DELETE")
	api.HandleFunc("/users/{userID}/keys/{fingerprint}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/identities", h.GetIdentitiesByUserID).Methods("GET")
	api.HandleFunc("/users/{userID}/identities", h.noop).Methods("OPTIONS")

	api.HandleFunc("/profile", h.UpdateProfile).Methods("POST")
	api.HandleFunc("/profile", h.noop).Methods("OPTIONS")

	api.HandleFunc("/profile/default-identity", h.SetDefaultIdentity).Methods("POST")
	api.HandleFunc("/profile/default-identity", h.noop).Methods("OPTIONS")

	api.HandleFunc("/keys", h.AddPublicKey).Methods("POST")
	api.HandleFunc("/keys", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds", h.PublishReed).Methods("POST")
	api.HandleFunc("/reeds", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{reedID}", h.DeleteReed).Methods("DELETE")
	api.HandleFunc("/reeds/{reedID}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods("GET")

	// Replace the API subrouter with the instrumented version
	router.PathPrefix("/api").Handler(api)

	// Serve static files
	router.PathPrefix("/static/").
		Handler(
			http.StripPrefix("/static/", http.FileServer(http.Dir("html/static/"))),
		)
	router.PathPrefix("/").Handler(http.FileServer(http.Dir("html/")))

	log.Info().Msg("[OK] Router configured successfully")

	server := &http.Server{
		Addr:         ":" + strconv.Itoa(cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	testListener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to create listener")
	}
	testListener.Close()

	log.Info().Msg("[OK] Server listening on http://127.0.0.1:" + strconv.Itoa(cfg.Port))
	l.Printf(`
    _____            _
   / ____|          (_)
  | (___  _   _ _ __ _ _ __ __  __
   \___ \| | | | '__| | '_ \\ \/ /
   ____) | |_| | |  | | | | |> _<
  |_____/ \__, |_|  |_|_| |_/_/\_\
           __/ |
          |___/     127.0.0.1:%d
	`, cfg.Port)

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("[ERR] Server failed to start")
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Info().Msg("Shutdown signal received, shutting down gracefully...")

	// Shutdown server
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("[ERR] Server forced to shutdown")
	}

	log.Info().Msg("[OK] Server stopped gracefully")
}
