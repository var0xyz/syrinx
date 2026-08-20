//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"fmt"
	l "log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"syrinx/crypto"
	"syrinx/invites"
	"syrinx/observability"
	"syrinx/realtime"
	"syrinx/recovery"
	"syrinx/roles"
	"syrinx/secret"

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

	// This server's own public URL, used for federation.
	APIBaseURL env.HTTPURL `env:"name='API_BASE_URL'"`

	// Dev-only escape hatch: lets federation baseUrls be plain http:// so two
	// local instances can complete a handshake without TLS. Never set this in
	// production — federation's threat model assumes TLS on baseUrl.
	FederationAllowInsecureHTTP bool `env:"optional,default='false',name='FEDERATION_ALLOW_INSECURE_HTTP'"`

	ServerKeyPassphrase string `env:"name='SERVER_KEY_PASSPHRASE'"`

	RecoveryMode      bool   `env:"optional,default='false',name='RECOVERY_MODE'"`
	SignupMode        string `env:"optional,default='invite',values='open,invite,closed',name='SIGNUP_MODE'"`
	MaxInvitesPerUser int    `env:"optional,default='-1',name='MAX_INVITES_PER_USER'"`

	// Empty (default) means no local OTLP collector — observability stays
	// disabled with zero setup cost. See specs/observability/ for the
	// collector-side wiring.
	OTELCollectorHost string `env:"optional,default='',name='OTEL_COLLECTOR_HOST'"`
	OTELCollectorPort string `env:"optional,default='4317',name='OTEL_COLLECTOR_PORT'"`

	// One-shot root operator export on empty DB (see specs/account_recovery/07).
	RootKeyExportPassphrase string `env:"optional,default='',name='ROOT_KEY_EXPORT_PASSPHRASE'"`
	RootKeyExportPath       string `env:"optional,default='',name='ROOT_KEY_EXPORT_PATH'"` // output directory only
}

func main() {
	var appConfig AppConfig
	cfg := env.MustAssert(appConfig)

	if cfg.MaxInvitesPerUser < 1 && cfg.MaxInvitesPerUser != int(invites.MaxInvitesUnlimited) {
		l.Panicf("[ERR] invalid MAX_INVITES_PER_USER %d: must be >= 1, or -1 for unlimited", cfg.MaxInvitesPerUser)
	}

	// RECOVERY_MODE always wins: Signup and CheckUsername refuse all
	// requests while it's on, regardless of SIGNUP_MODE, to stop username
	// sniping against not-yet-reclaimed identities. A non-closed SIGNUP_MODE
	// is therefore inert here — warn so the operator doesn't mistake it for
	// "signups are open" and gets surprised once recovery ends.
	if cfg.RecoveryMode && invites.SignupMode(cfg.SignupMode) != invites.ModeClosed {
		l.Printf("[WARN] RECOVERY_MODE is on with SIGNUP_MODE=%q: signups are blocked entirely until recovery mode is turned off, regardless of SIGNUP_MODE", cfg.SignupMode)
	}

	// ServerName cannot be empty. There's no max though, but please be
	// reasonable. Consider something short and unique.
	if len(cfg.ServerName) == 0 {
		l.Panicf("[ERR] ServerName cannot be empty")
	}

	if !strings.HasPrefix(string(cfg.APIBaseURL), "https://") {
		l.Printf("[WARN] API_BASE_URL %q is not https:// — fine for local dev, not for production", cfg.APIBaseURL)
	}

	log.Info().Msg("Starting Syrinx API...")
	SetupLogger()
	log.Info().Msg("[OK] Logger setup successful")

	obs, err := observability.Setup(cfg.OTELCollectorHost, cfg.OTELCollectorPort)
	if err != nil {
		if cfg.OTELCollectorHost != "" {
			log.Fatal().Err(err).Msg("[ERR] Telemetry host configured but unreachable")
		}
		log.Warn().Err(err).Msg("[WARN] Observability disabled")
	} else if cfg.OTELCollectorHost != "" {
		log.Info().Msg("[OK] Observability enabled")
	}
	defer obs.Shutdown()

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBSSLMode)

	log.Debug().Msg("Checking for connectivity to database...")
	db, err := obs.OpenDB("postgres", dbURL)
	if err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to connect to database")
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to ping database")
	}
	log.Info().Msg("[OK] Database connection successful")

	unregisterDBStats, err := obs.RegisterDBStats(db)
	if err != nil {
		log.Warn().Err(err).Msg("[WARN] Failed to register DB pool metrics")
	}
	defer unregisterDBStats()

	log.Debug().Msg("Initializing database tables...")
	if err := InitDB(db); err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to initialize database")
	}
	log.Info().Msg("[OK] Database tables initialized successfully")

	// log.Debug().Msg("Applying database migrations...")
	// if err := MigrateDB(db); err != nil {
	// 	log.Fatal().Err(err).Msg("[ERR] Failed to apply database migrations")
	// }
	// log.Info().Msg("[OK] Database migrations applied successfully")

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
	if err := dataService.InitServer(context.Background(), cfg.RecoveryMode, string(cfg.APIBaseURL)); err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to initialize server identity")
	}

	log.Debug().Msg("Resolving server key passphrase...")
	passphrase, err := secret.NewResolver(cfg.ServerKeyPassphrase, cfg.ServerName).Resolve()
	if err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to resolve server key passphrase")
	}
	switch passphrase.Source {
	case secret.SourceEnv:
		log.Info().Msg("[OK] Server key passphrase found in SERVER_KEY_PASSPHRASE")
	case secret.SourceKeychain:
		log.Info().Msg("[OK] Server key passphrase fetched from OS keychain")
	case secret.SourcePrompt:
		log.Info().Msg("[OK] Server key passphrase stored in OS keychain")
	case secret.SourceGenerated:
		log.Info().Msg("[OK] Server key passphrase auto-generated and stored in OS keychain")
	}

	log.Debug().Msg("Processing key revocations...")
	if err := dataService.ProcessRevocations(context.Background()); err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to process key revocations")
	}
	log.Info().Msg("[OK] Key revocations processed")

	log.Debug().Msg("Initializing server signing key...")
	signingKey, err := dataService.InitServerKey(context.Background(), cryptoService, passphrase.Value)
	if err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to initialize server signing key")
	}
	log.Info().Str("fingerprint", signingKey.Fingerprint).Msg("[OK] Server signing key ready")

	if exit, err := maybeExportRootKey(cfg, dataService, cryptoService, signingKey); err != nil {
		log.Fatal().Err(err).Msg("[ERR] Root key export failed")
	} else if exit {
		os.Exit(0)
	}

	if err := requireRootUser(cfg, dataService); err != nil {
		log.Fatal().Err(err).Msg("[ERR] Root user required")
	}
	log.Info().Str("userID", roles.RootUserID).Msg("[OK] Root user present")

	if msg, err := recovery.StaleIdentityBackupMessage(context.Background(), db); err != nil {
		log.Warn().Err(err).Msg("[WARN] Could not check identity backup freshness")
	} else if msg != "" {
		log.Warn().Msg("[WARN] " + msg)
	}
	log.Info().Msg("[OK] Server identity initialized successfully")

	log.Debug().Msg("Initializing realtime service...")
	realtimeService := realtime.NewService(db, cryptoService, cfg.AllowedOrigin, dataService.GetServerID())
	realtimeService.SetMetrics(obs.Metrics())

	// Create broadcast channel
	broadcastChan := make(chan realtime.BroadcastMessage, 100)

	// Start realtime service in goroutine
	go realtimeService.Start(broadcastChan)

	log.Debug().Msg("Initializing handlers...")
	h := NewHandlers(services, cfg, broadcastChan, *signingKey)
	h.SetMetrics(obs.Metrics())
	h.SetPipeTagFilter(realtimeService.FilterSubscribedPipeTags)
	h.SetKickUserWS(realtimeService.DisconnectUser)
	realtimeService.SetDeviceCheck(func(userID, deviceID string) error {
		// userID arrives already in "userID@serverID" form (see
		// realtime/auth.go), and CheckActiveDevice expects that same
		// composed form (see its doc comment) — pass through unmodified.
		return dataService.CheckActiveDevice(context.Background(), userID, deviceID)
	})
	log.Info().Msg("[OK] Handlers initialized successfully")

	log.Debug().Msg("Setting up router...")
	router := mux.NewRouter()
	router.Use(obs.Middleware(cfg.ServerName))

	// API Router
	api := router.PathPrefix("/api").Subrouter()

	// Middlewares
	api.Use(loggingMiddleware)
	api.Use(h.CORSMiddleware(cfg.AllowedOrigin))
	api.Use(h.signatureAuthMiddleware("/api"))
	if cfg.RecoveryMode {
		realtimeService.SetOngoingCheck(func(userID string) (bool, error) {
			// userID arrives already in "userID@serverID" form (see
			// realtime/auth.go), and IsOngoing expects that same composed
			// form — pass through unmodified, same as the recovery.Middleware
			// registration below.
			return dataService.IsOngoing(context.Background(), userID)
		})
		api.Use(recovery.Middleware(userIDKey, func(ctx context.Context, userID string) (bool, error) { return dataService.IsOngoing(ctx, userID) }))
	}
	api.Use(h.deviceMiddleware())
	api.Use(h.responseSignerMiddleware(signingKey.Armor))

	api.HandleFunc("/server/info", h.GetServerInfo).Methods("GET")
	api.HandleFunc("/server/info", h.noop).Methods("OPTIONS")

	api.HandleFunc("/server/keys/{fingerprint}", h.GetServerPublicKey).Methods("GET")
	api.HandleFunc("/server/keys/{fingerprint}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/check-username", h.CheckUsername).Methods("POST")
	api.HandleFunc("/check-username", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/id", h.GenerateUserID).Methods("GET")
	api.HandleFunc("/users/id", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/signup", h.Signup).Methods("POST")
	api.HandleFunc("/users/signup", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/status", h.UserStatus).Methods("POST")
	api.HandleFunc("/users/status", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/me", h.UpdateUser).Methods("PUT")
	api.HandleFunc("/users/me", h.DeleteMe).Methods("DELETE")
	api.HandleFunc("/users/me", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/me/check-username", h.CheckUsernameForRename).Methods("POST")
	api.HandleFunc("/users/me/check-username", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/me/backup", h.RecordBackup).Methods("POST")
	api.HandleFunc("/users/me/backup", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/device", h.BindDevice).Methods("POST")
	api.HandleFunc("/users/device", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/search", h.SearchUsers).Methods("GET")
	api.HandleFunc("/users/search", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/profile", h.GetUserProfile).Methods("GET")
	api.HandleFunc("/users/{userID}/profile", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/info", h.GetUserInfo).Methods("GET")
	api.HandleFunc("/users/{userID}/info", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/follow", h.FollowUser).Methods("POST")
	api.HandleFunc("/users/{userID}/follow", h.UnfollowUser).Methods("DELETE")
	api.HandleFunc("/users/{userID}/follow", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/following", h.GetUserFollowing).Methods("GET")
	api.HandleFunc("/users/{userID}/following", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/followers", h.GetUserFollowers).Methods("GET")
	api.HandleFunc("/users/{userID}/followers", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/keys/{fingerprint}", h.GetPublicKey).Methods("GET")
	api.HandleFunc("/users/{userID}/keys/{fingerprint}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/keys/{fingerprint}/revoke", h.RevokeKey).Methods("POST")
	api.HandleFunc("/users/{userID}/keys/{fingerprint}/revoke", h.noop).Methods("OPTIONS")

	api.HandleFunc("/users/{userID}/keys/{fingerprint}/revocation", h.GetKeyRevocation).Methods("GET")
	api.HandleFunc("/users/{userID}/keys/{fingerprint}/revocation", h.noop).Methods("OPTIONS")

	api.HandleFunc("/keys", h.AddPublicKey).Methods("POST")
	api.HandleFunc("/keys", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds", h.SignReed).Methods("POST")
	api.HandleFunc("/reeds", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{userID}/{reedID}", h.GetReed).Methods("GET")
	api.HandleFunc("/reeds/{userID}/{reedID}", h.DeleteReed).Methods("DELETE")
	api.HandleFunc("/reeds/{userID}/{reedID}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{userID}/{reedID}/echoes", h.GetReedEchoCount).Methods("GET")
	api.HandleFunc("/reeds/{userID}/{reedID}/echoes", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{userID}/{reedID}/chorus", h.GetReedChorus).Methods("GET")
	api.HandleFunc("/reeds/{userID}/{reedID}/chorus", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{userID}/{reedID}/replies", h.GetReedReplies).Methods("GET")
	api.HandleFunc("/reeds/{userID}/{reedID}/replies", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{userID}/{reedID}/like", h.LikeReed).Methods("POST")
	api.HandleFunc("/reeds/{userID}/{reedID}/like", h.UnlikeReed).Methods("DELETE")
	api.HandleFunc("/reeds/{userID}/{reedID}/like", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{userID}/{reedID}/ripples", h.PostRipple).Methods("POST")
	api.HandleFunc("/reeds/{userID}/{reedID}/ripples", h.GetRipples).Methods("QUERY")
	api.HandleFunc("/reeds/{userID}/{reedID}/ripples", h.noop).Methods("OPTIONS")

	// api.HandleFunc("/reeds/{userID}/{reedID}/ripples/proof", h.GetRipples).Methods("POST")
	// api.HandleFunc("/reeds/{userID}/{reedID}/ripples/proof", h.noop).Methods("OPTIONS")

	api.HandleFunc("/ripples/{rippleID}", h.DeleteRipple).Methods("DELETE")
	api.HandleFunc("/ripples/{rippleID}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods("GET")

	invites.RegisterRoutes(api, invites.Deps{
		Store:                &invites.Store{DB: db, ServerID: dataService.GetServerID()},
		Mode:                 invites.SignupMode(cfg.SignupMode),
		Max:                  invites.MaxInvitesPerUser(cfg.MaxInvitesPerUser),
		UserIDKey:            userIDKey,
		ServerID:             dataService.GetServerID(),
		ServerKeyFingerprint: signingKey.Fingerprint,
		GetPublicKeyArmor: func(ctx context.Context, userID, fingerprint string) (string, error) {
			key, err := dataService.GetPublicKey(ctx, userID, fingerprint)
			if err != nil {
				return "", err
			}
			if key == nil || key.Revoked {
				return "", nil
			}
			return key.Armor, nil
		},
		GetUserRole: dataService.GetUserRole,
		VerifySignature: func(payload, sigArmor, pubKeyArmor string) error {
			return cryptoService.VerifySignature(payload, sigArmor, pubKeyArmor)
		},
		Countersign: func(payload []byte, ts time.Time) (invites.ServerSignatureWire, error) {
			sig, err := h.countersign(payload, ts)
			if err != nil {
				return invites.ServerSignatureWire{}, err
			}
			return invites.ServerSignatureWire{
				ServerID:    sig.ServerID,
				Fingerprint: sig.Fingerprint,
				Armor:       sig.Armor,
				Timestamp:   sig.SignedAt.UTC().Format(time.RFC3339),
			}, nil
		},
	})

	api.HandleFunc("/federation/invitations", h.CreateFederationInvitation).Methods("POST")
	api.HandleFunc("/federation/invitations", h.ListFederationInvitations).Methods("GET")
	api.HandleFunc("/federation/invitations", h.noop).Methods("OPTIONS")
	api.HandleFunc("/federation/invitations/{id}/revoke", h.RevokeFederationInvitation).Methods("POST")
	api.HandleFunc("/federation/invitations/{id}/revoke", h.noop).Methods("OPTIONS")
	api.HandleFunc("/federation/servers", h.ListFederationServers).Methods("GET")
	api.HandleFunc("/federation/servers", h.noop).Methods("OPTIONS")
	api.HandleFunc("/federation/servers/{id}/logs", h.GetFederationServerLogs).Methods("GET")
	api.HandleFunc("/federation/servers/{id}/logs", h.noop).Methods("OPTIONS")
	api.HandleFunc("/federation/attempt", h.OutgoingFederationAttempt).Methods("POST")
	api.HandleFunc("/federation/attempt", h.noop).Methods("OPTIONS")
	api.HandleFunc("/federation/connect/{id}", h.IncomingFederationAttempt).Methods("POST")
	api.HandleFunc("/federation/connect/{id}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/account-recovery/challenge", h.AccountRecoveryChallenge).Methods("GET")
	api.HandleFunc("/account-recovery/challenge", h.noop).Methods("OPTIONS")
	api.HandleFunc("/account-recovery/bootstrap", h.BootstrapAccountRecovery).Methods("POST")
	api.HandleFunc("/account-recovery/bootstrap", h.noop).Methods("OPTIONS")

	// WebSocket Router (must be before catch-all SPA handler)
	ws := router.PathPrefix("/ws").Subrouter()
	ws.HandleFunc("/", realtimeService.HandleWebSocket)

	// SvelteKit static build (spa/build) with SPA fallback for client routes
	router.PathPrefix("/").Handler(spaHandler("spa/build"))

	if cfg.RecoveryMode {
		log.Debug().Msg("Initializing recovery mode...")
		unclaimedCount, err := recovery.CountUnclaimed(context.Background(), db)
		if err != nil {
			log.Warn().Err(err).Msg("[WARN] Could not count unclaimed accounts")
		} else {
			if unclaimedCount > 0 {
				log.Warn().Msg(fmt.Sprintf("[OK] %d unclaimed accounts", unclaimedCount))
			}
		}
		recovery.RegisterRoutes(api, recovery.Deps{
			DB:        db,
			Crypto:    cryptoService,
			ServerID:  dataService.GetServerID(),
			Lookup:    dataService.GetServerPublicKeyByFingerprint,
			UserIDKey: userIDKey,
			Metrics:   obs.Metrics(),
		})
		log.Info().Msg("[OK] Recovery mode initialized successfully")
	}

	// Unmatched /api/* must not fall through to the SPA catch-all (which
	// would return index.html and look like a successful page load).
	api.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponse(w, http.StatusNotFound, "Not found")
	})

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

	// WebSocket connections are hijacked from net/http once upgraded, so
	// server.Shutdown below can't see or close them — it would just block
	// on their still-open sockets until its timeout. Notify and close them
	// ourselves first so clients reconnect immediately instead of being
	// left on a connection that silently goes dead.
	realtimeService.Shutdown()

	// Shutdown server
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("[ERR] Server forced to shutdown")
	}

	log.Info().Msg("[OK] Server stopped gracefully")
}
