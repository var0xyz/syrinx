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

	"syrinx/observability"

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

	if cfg.MaxInvitesPerUser < 1 && cfg.MaxInvitesPerUser != maxInvitesUnlimited {
		l.Panicf("[ERR] invalid MAX_INVITES_PER_USER %d: must be >= 1, or -1 for unlimited", cfg.MaxInvitesPerUser)
	}

	// RECOVERY_MODE always wins: Signup and CheckUsername refuse all
	// requests while it's on, regardless of SIGNUP_MODE, to stop username
	// sniping against not-yet-reclaimed identities. A non-closed SIGNUP_MODE
	// is therefore inert here — warn so the operator doesn't mistake it for
	// "signups are open" and gets surprised once recovery ends.
	if cfg.RecoveryMode && inviteSignupMode(cfg.SignupMode) != signupModeClosed {
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
	cryptoService := newCryptoService()
	services := &Services{
		db:     dataService,
		crypto: cryptoService,
	}
	log.Info().Msg("[OK] Services initialized successfully")

	log.Debug().Msg("Initializing server identity...")
	if err := dataService.InitServer(context.Background(), cfg.RecoveryMode, string(cfg.APIBaseURL)); err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to initialize server identity")
	}

	log.Debug().Msg("Resolving server key passphrase...")
	passphrase, err := resolvePassphrase(cfg.ServerKeyPassphrase, cfg.ServerName)
	if err != nil {
		log.Fatal().Err(err).Msg("[ERR] Failed to resolve server key passphrase")
	}
	switch passphrase.Source {
	case SourceEnv:
		log.Info().Msg("[OK] Server key passphrase found in SERVER_KEY_PASSPHRASE")
	case SourceKeychain:
		log.Info().Msg("[OK] Server key passphrase fetched from OS keychain")
	case SourcePrompt:
		log.Info().Msg("[OK] Server key passphrase stored in OS keychain")
	case SourceGenerated:
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
	log.Info().Str("userID", rootUserID).Msg("[OK] Root user present")

	if msg, err := staleIdentityBackupMessage(context.Background(), db); err != nil {
		log.Warn().Err(err).Msg("[WARN] Could not check identity backup freshness")
	} else if msg != "" {
		log.Warn().Msg("[WARN] " + msg)
	}
	log.Info().Msg("[OK] Server identity initialized successfully")

	log.Debug().Msg("Initializing realtime service...")
	rtService := newRealtimeService(dataService, cryptoService, cfg.AllowedOrigin)
	rtService.SetMetrics(obs.Metrics())

	// Create broadcast channel
	broadcastChan := make(chan realtimeBroadcastMessage, 100)

	// Start realtime service in goroutine
	go rtService.Start(broadcastChan)

	log.Debug().Msg("Initializing handlers...")
	h := NewHandlers(services, cfg, broadcastChan, *signingKey)
	h.SetMetrics(obs.Metrics())
	h.SetPipeTagFilter(rtService.FilterSubscribedPipeTags)
	h.SetKickUserWS(rtService.DisconnectUser)
	h.SetRealtimeRelay(rtService)
	rtService.SetForeignRequestReedHook(h.relayRequestToPeer)
	rtService.SetForeignSubscribeProfileHook(h.subscribeProfileToPeer)
	rtService.SetForeignDeliverHook(h.deliverRelayResponseToPeer)
	rtService.SetForeignNotHeldHook(h.notifyRelayNotHeldToPeer)
	rtService.SetForeignCancelHook(h.cancelRelayRequestWithPeer)
	rtService.SetForeignAckHook(h.ackRelayDeliveryWithPeer)
	rtService.SetForeignUnsubscribeProfileHook(h.unsubscribeProfileWithPeer)
	rtService.SetForeignSubscribeReedHook(h.subscribeReedToPeer)
	rtService.SetForeignUnsubscribeReedHook(h.unsubscribeReedWithPeer)
	rtService.SetForeignReedStatsHook(h.pushReedStatsToPeer)
	rtService.SetForeignReplyNotifyHook(h.notifyForeignReplyToPeer)
	rtService.SetForeignHolderNotifyHook(h.notifyHolderToPeer)
	rtService.SetForeignFallbackRequestHook(h.relayFallbackRequestToPeer)
	rtService.SetForeignNewReedNotifyHook(h.notifyNewReedToPeer)
	rtService.SetForeignReplyRemovalToViewerHook(h.notifyForeignReplyRemovalToViewer)
	rtService.SetDeviceCheck(func(userID, deviceID string) error {
		// userID arrives already in "userID@serverID" form (see
		// authenticateWebSocket), and CheckActiveDevice expects that same
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
	api.Use(h.serverKeyProofMiddleware("/api"))
	api.Use(h.signatureAuthMiddleware("/api"))
	if cfg.RecoveryMode {
		rtService.SetOngoingCheck(func(userID string) (bool, error) {
			// userID arrives already in "userID@serverID" form (see
			// authenticateWebSocket), and IsOngoing expects that same
			// composed form — pass through unmodified, same as the
			// import-gate middleware registration below.
			return dataService.IsOngoing(context.Background(), userID)
		})
		api.Use(recoveryImportGateMiddleware(userIDKey, func(ctx context.Context, userID string) (bool, error) { return dataService.IsOngoing(ctx, userID) }))
	}
	api.Use(h.deviceMiddleware())
	api.Use(h.responseSignerMiddleware(signingKey.Armor))

	api.HandleFunc("/server/info", h.GetServerInfo).Methods("GET")
	api.HandleFunc("/server/info", h.noop).Methods("OPTIONS")

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

	// {id} is the full canonical key id — "userID@serverID/fingerprint" for
	// a user key, "fingerprint@serverID" for a server's own key — and
	// carries a "/", so it needs a greedy path variable ({id:.+}), not a
	// plain {id} which would stop at the first "/". One route serves every
	// key: local or (via handlers.go's proxyToPeer) foreign, user-owned or
	// server-owned, since the id shape alone determines both ownership and
	// which server to ask.
	//
	// /revocation is registered BEFORE the bare /keys/{id:.+}: gorilla/mux
	// matches in registration order, and a greedy {id:.+} can otherwise
	// swallow ".../revocation" as part of id before the more specific
	// route ever gets a chance.
	api.HandleFunc("/keys/{id:.+}/revocation", h.GetKeyRevocation).Methods("GET")
	api.HandleFunc("/keys/{id:.+}/revocation", h.noop).Methods("OPTIONS")

	api.HandleFunc("/keys/{id:.+}", h.GetKey).Methods("GET")
	api.HandleFunc("/keys/{id:.+}", h.noop).Methods("OPTIONS")

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

	api.HandleFunc("/mentions", h.GetMentions).Methods("GET")
	api.HandleFunc("/mentions", h.noop).Methods("OPTIONS")

	api.HandleFunc("/mentions/{reedID:.+}", h.DeleteMention).Methods("DELETE")
	api.HandleFunc("/mentions/{reedID:.+}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{userID}/{reedID}/like", h.LikeReed).Methods("POST")
	api.HandleFunc("/reeds/{userID}/{reedID}/like", h.UnlikeReed).Methods("DELETE")
	api.HandleFunc("/reeds/{userID}/{reedID}/like", h.noop).Methods("OPTIONS")

	// {reedID:.+} (not split {userID}/{reedID}) so the full reed id the
	// client already has is used as-is, no server-side reconstruction.
	api.HandleFunc("/reeds/{reedID:.+}/pin", h.PinReed).Methods("POST")
	api.HandleFunc("/reeds/{reedID:.+}/pin", h.UnpinReed).Methods("DELETE")
	api.HandleFunc("/reeds/{reedID:.+}/pin", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{userID}/{reedID}/ripples", h.PostRipple).Methods("POST")
	api.HandleFunc("/reeds/{userID}/{reedID}/ripples", h.GetRipples).Methods("QUERY")
	api.HandleFunc("/reeds/{userID}/{reedID}/ripples", h.noop).Methods("OPTIONS")

	// api.HandleFunc("/reeds/{userID}/{reedID}/ripples/proof", h.GetRipples).Methods("POST")
	// api.HandleFunc("/reeds/{userID}/{reedID}/ripples/proof", h.noop).Methods("OPTIONS")

	api.HandleFunc("/reeds/{reedID:.+}/ripples/{rippleID}", h.DeleteRipple).Methods("DELETE")
	api.HandleFunc("/reeds/{reedID:.+}/ripples/{rippleID}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/ripples", h.GetReceivedRipples).Methods("GET")
	api.HandleFunc("/ripples", h.noop).Methods("OPTIONS")

	api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods("GET")

	api.HandleFunc("/invites", h.CreateInvite).Methods("POST")
	api.HandleFunc("/invites", h.noop).Methods("OPTIONS")
	api.HandleFunc("/invites/check", h.CheckInvite).Methods("GET")
	api.HandleFunc("/invites/check", h.noop).Methods("OPTIONS")
	// {id} is "userID@serverID/reedID"-shaped and carries a "/", so it needs
	// a greedy path variable ({id:.+}) — a plain {id} stops at the first "/"
	// and never matches (see /keys/{id:.+} above for the same gotcha).
	api.HandleFunc("/invites/{id:.+}", h.InviteStatus).Methods("GET")
	api.HandleFunc("/invites/{id:.+}", h.DeleteInvite).Methods("DELETE")
	api.HandleFunc("/invites/{id:.+}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/list", h.GetFederationList).Methods("GET")
	api.HandleFunc("/federation/list", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/invitations", h.CreateFederationInvitation).Methods("POST")
	api.HandleFunc("/federation/invitations", h.ListFederationInvitations).Methods("GET")
	api.HandleFunc("/federation/invitations", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/invitations/{id}/revoke", h.RevokeFederationInvitation).Methods("POST")
	api.HandleFunc("/federation/invitations/{id}/revoke", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/servers", h.ListFederationServers).Methods("GET")
	api.HandleFunc("/federation/servers", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/servers/{id}/logs", h.GetFederationServerLogs).Methods("GET")
	api.HandleFunc("/federation/servers/{id}/logs", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/servers/{id}/invitation", h.GetFederationServerInvitation).Methods("GET")
	api.HandleFunc("/federation/servers/{id}/invitation", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/servers/{id}/attempt", h.GetFederationServerAttempt).Methods("GET")
	api.HandleFunc("/federation/servers/{id}/attempt", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/servers/{id}/revoke", h.RequestFederationServerDisconnect).Methods("POST")
	api.HandleFunc("/federation/servers/{id}/revoke", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/servers/{id}/revoke/confirm", h.ConfirmFederationServerDisconnect).Methods("POST")
	api.HandleFunc("/federation/servers/{id}/revoke/confirm", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/servers/{id}/revoke/cancel", h.CancelFederationServerDisconnect).Methods("POST")
	api.HandleFunc("/federation/servers/{id}/revoke/cancel", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/servers/{id}/purge", h.PurgeFederationServer).Methods("POST")
	api.HandleFunc("/federation/servers/{id}/purge", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/attempts/{id}", h.GetFederationAttempt).Methods("GET")
	api.HandleFunc("/federation/attempts/{id}", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/attempts/{id}/logs", h.GetFederationAttemptLogs).Methods("GET")
	api.HandleFunc("/federation/attempts/{id}/logs", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/attempts/{id}/approve", h.ApproveFederationAttempt).Methods("POST")
	api.HandleFunc("/federation/attempts/{id}/approve", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/attempts/{id}/reject", h.RejectFederationAttempt).Methods("POST")
	api.HandleFunc("/federation/attempts/{id}/reject", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/attempt", h.OutgoingFederationAttempt).Methods("POST")
	api.HandleFunc("/federation/attempt", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/connect/{id}", h.IncomingFederationAttempt).Methods("POST")
	api.HandleFunc("/federation/connect/{id}", h.noop).Methods("OPTIONS")

	// Peer-authenticated (specs/federation/04): signatureAuthMiddleware
	// recognizes a foreign-server X-Syrinx-Public-Key-Id and routes to
	// authenticateAsPeer automatically — no separate wrapper needed here.
	api.HandleFunc("/federation/users/{userID}/identity", h.GetFederationUserIdentity).Methods("GET")
	api.HandleFunc("/federation/users/{userID}/identity", h.noop).Methods("OPTIONS")

	// Cross-server REQUEST_REED relay (federation_relay.go): also
	// peer-authenticated only, never end-user-callable. Each handler
	// records its own syrinx.federation.relay metric inline.
	api.HandleFunc("/federation/relay/request", h.RelayRequestFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/request", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/subscribe", h.RelaySubscribeProfileFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/subscribe", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/deliver", h.DeliverRelayResponseFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/deliver", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/not-held", h.RelayNotHeldFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/not-held", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/cancel", h.CancelRelayRequestFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/cancel", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/ack", h.AckRelayDeliveryFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/ack", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/unsubscribe", h.RelayUnsubscribeProfileFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/unsubscribe", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/subscribe-reed", h.RelaySubscribeReedFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/subscribe-reed", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/unsubscribe-reed", h.RelayUnsubscribeReedFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/unsubscribe-reed", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/reed-stats", h.PushReedStatsFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/reed-stats", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/reply-notify", h.ReplyNotifyFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/reply-notify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/echo-notify", h.EchoNotifyFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/echo-notify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/mention-notify", h.MentionNotifyFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/mention-notify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/reply-removal-notify", h.ReplyRemovalNotifyFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/reply-removal-notify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/echo-removal-notify", h.EchoRemovalNotifyFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/echo-removal-notify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/holder-notify", h.HolderNotifyFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/holder-notify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/fallback-request", h.RelayFallbackRequestFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/fallback-request", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/new-reed-notify", h.RelayNewReedNotifyFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/new-reed-notify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/search-users", h.SearchUsersFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/search-users", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/reply-removal-to-viewer", h.ReplyRemovalToViewerFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/reply-removal-to-viewer", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/disconnect-notify", h.DisconnectNotifyFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/disconnect-notify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/account-removal-notify", h.AccountRemovalNotifyFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/account-removal-notify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/federation/relay/reed-removal-notify", h.ReedRemovalNotifyFromPeer).Methods("POST")
	api.HandleFunc("/federation/relay/reed-removal-notify", h.noop).Methods("OPTIONS")

	api.HandleFunc("/account-recovery/challenge", h.AccountRecoveryChallenge).Methods("GET")
	api.HandleFunc("/account-recovery/challenge", h.noop).Methods("OPTIONS")

	api.HandleFunc("/account-recovery/bootstrap", h.BootstrapAccountRecovery).Methods("POST")
	api.HandleFunc("/account-recovery/bootstrap", h.noop).Methods("OPTIONS")

	// WebSocket Router (must be before catch-all SPA handler)
	ws := router.PathPrefix("/ws").Subrouter()
	ws.HandleFunc("/", rtService.HandleWebSocket)

	// SvelteKit static build (../frontend/build, relative to src/backend/
	// where this binary is built/run from) with SPA fallback for client
	// routes. Local dev only — production serves the SPA via nginx
	// directly (see deploy/scripts/syrinx/setup.sh), so this path is
	// never resolved there.
	router.PathPrefix("/").Handler(spaHandler("../frontend/build"))

	if cfg.RecoveryMode {
		log.Debug().Msg("Initializing recovery mode...")
		unclaimedCount, err := dataService.CountUnclaimed(context.Background())
		if err != nil {
			log.Warn().Err(err).Msg("[WARN] Could not count unclaimed accounts")
		} else {
			if unclaimedCount > 0 {
				log.Warn().Msg(fmt.Sprintf("[OK] %d unclaimed accounts", unclaimedCount))
			}
		}
		api.HandleFunc("/recovery/identity/claim", h.IssueChallenge).Methods(http.MethodGet)
		api.HandleFunc("/recovery/identity/claim", h.ClaimIdentity).Methods(http.MethodPost)
		api.HandleFunc("/recovery/identity/claim", h.noop).Methods(http.MethodOptions)
		api.HandleFunc("/recovery/identity", h.ReportPeerIdentity).Methods(http.MethodPost)
		api.HandleFunc("/recovery/identity", h.noop).Methods(http.MethodOptions)
		api.HandleFunc("/recovery/reeds", h.ReportReed).Methods(http.MethodPost)
		api.HandleFunc("/recovery/reeds", h.noop).Methods(http.MethodOptions)
		api.HandleFunc("/recovery/following", h.ReportFollowing).Methods(http.MethodPost)
		api.HandleFunc("/recovery/following", h.noop).Methods(http.MethodOptions)
		api.HandleFunc("/recovery/complete", h.CompleteImport).Methods(http.MethodPost)
		api.HandleFunc("/recovery/complete", h.noop).Methods(http.MethodOptions)
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
	rtService.Shutdown()

	// Shutdown server
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("[ERR] Server forced to shutdown")
	}

	log.Info().Msg("[OK] Server stopped gracefully")
}
