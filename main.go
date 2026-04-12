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

	"syrinx/crypto"
	"syrinx/realtime"

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

	ServerName    string `env:"name='SERVER_NAME'"`
	Port          int
	AllowedOrigin string `env:"name='ALLOWED_ORIGIN'"`

	ServerKeyPassphrase string `env:"name='SERVER_PRIVATE_KEY_PASSPHRASE'"`
	PrivateKeyFile      string `env:"name='SERVER_PRIVATE_KEY_FILE'"`
}

func main() {
	var appConfig AppConfig
	cfg := env.MustAssert(appConfig)

	// ServerName cannot be empty. There's no max though, but please be
	// reasonable. Consider something short and unique.
	if len(cfg.ServerName) == 0 {
		l.Panicf("[ERR] ServerName cannot be empty")
	}

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
	dataService := NewDataService(db, cfg.ServerName)
	cryptoService := crypto.NewService()
	markdownService := NewMarkdownService()
	services := &Services{
		db:     dataService,
		crypto: cryptoService,
		md:     markdownService,
	}
	log.Info().Msg("[OK] Services initialized successfully")

	log.Debug().Msg("Initializing server identity...")
	if err := dataService.InitServer(); err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to initialize server identity")
	}
	log.Info().Msg("[OK] Server identity initialized successfully")

	log.Debug().Msg("Initializing realtime service...")
	realtimeService := realtime.NewService(db, cryptoService, cfg.AllowedOrigin)

	// Create broadcast channel
	broadcastChan := make(chan realtime.BroadcastMessage, 100)

	// Start realtime service in goroutine
	go realtimeService.Start(broadcastChan)

	log.Debug().Msg("Initializing handlers...")
	h := NewHandlers(services, cfg, broadcastChan)
	log.Info().Msg("[OK] Handlers initialized successfully")

	log.Debug().Msg("Setting up router...")
	router := mux.NewRouter()

	// API Router
	api := router.PathPrefix("/api").Subrouter()

	// Middlewares
	api.Use(loggingMiddleware)
	api.Use(h.CORSMiddleware(cfg.AllowedOrigin))
	api.Use(h.signatureAuthMiddleware("/api"))
	api.Use(h.responseSignerMiddleware(cfg.ServerKeyPassphrase, cfg.PrivateKeyFile))

	api.HandleFunc("/server/info", h.GetServerInfo).Methods("GET")
	api.HandleFunc("/server/info", h.noop).Methods("OPTIONS")

	api.HandleFunc("/check-username", h.CheckUsername).Methods("POST")
	api.HandleFunc("/check-username", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/signup", h.Signup).Methods("POST")
	api.HandleFunc("/users/signup", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/me", h.UpdateUser).Methods("PUT")
	// TODO: If the user lost their password but may not want to delete their
	// account, we should have a way to "freeze" it. That is, their keys are
	// not revoked but no new content can be published.
	api.HandleFunc("/users/me", h.DeleteMe).Methods("DELETE")
	api.HandleFunc("/users/me", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}", h.GetUser).Methods("GET")
	api.HandleFunc("/users/{userID}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/follow", h.FollowUser).Methods("POST")
	api.HandleFunc("/users/{userID}/follow", h.UnfollowUser).Methods("DELETE")
	api.HandleFunc("/users/{userID}/follow", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/reeds", h.GetReedsByUserID).Methods("GET")
	api.HandleFunc("/users/{userID}/reeds", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/keys", h.GetUserKeys).Methods("GET")
	// Note: The system will refuse to generate a new key pair if a non-revoked
	// key pair exists. The user must first revoke their existing keys.
	api.HandleFunc("/users/{userID}/keys", h.GenerateUserKeys).Methods("POST")
	api.HandleFunc("/users/{userID}/keys", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/keys/{fingerprint}", h.GetPublicKey).Methods("GET")
	api.HandleFunc("/users/{userID}/keys/{fingerprint}/revoke", h.RevokeKey).Methods("POST")
	api.HandleFunc("/users/{userID}/keys/{fingerprint}", h.noop).Methods("OPTIONS")
	api.HandleFunc("/users/{userID}/keys/{fingerprint}/revoke", h.noop).Methods("OPTIONS")

	api.HandleFunc("/keys", h.AddPublicKey).Methods("POST")
	api.HandleFunc("/keys", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds", h.SignReed).Methods("POST")
	api.HandleFunc("/reeds", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{userID}/{reedID}", h.GetReed).Methods("GET")
	api.HandleFunc("/reeds/{userID}/{reedID}", h.DeleteReed).Methods("DELETE")
	api.HandleFunc("/reeds/{userID}/{reedID}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{userID}/{reedID}/verify", h.VerifySignature).Methods("POST")
	api.HandleFunc("/reeds/{userID}/{reedID}/verify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods("GET")

	// Serve static files
	router.PathPrefix("/static/").
		Handler(
			http.StripPrefix("/static/", http.FileServer(http.Dir("spa/build/"))),
		)

	// WebSocket Router (must be before catch-all static file handler)
	ws := router.PathPrefix("/ws").Subrouter()
	ws.HandleFunc("/", realtimeService.HandleWebSocket)

	// router.PathPrefix("/pwa").Handler(http.FileServer(http.Dir("pwa/")))
	// Catch-all static file handler (must be last)
	router.PathPrefix("/").Handler(http.FileServer(http.Dir("pwa/")))

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
                    %s (%s)
	`, cfg.Port, cfg.ServerName, h.services.db.GetServerID())

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
