package realtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"syrinx/crypto"
	"syrinx/identity"
	"syrinx/observability/metrics"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

type errorMessage struct {
	Error string `json:"error"`
}

func rejectConnection(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(errorMessage{Error: reason})
}

// RealtimeService represents the main realtime service
type RealtimeService struct {
	connManager   *ConnectionManager
	dbService     *DBService
	authService   *AuthService
	crypto        *crypto.Service
	allowedOrigin string
	metrics       metrics.Recorder
	ongoingCheck  func(userID string) (bool, error)
	deviceCheck   func(userID, deviceID string) error

	// Cross-server REQUEST_REED relay hooks: RealtimeService has no signing
	// key, HTTP client, or federation-table access of its own, so the
	// actual peer HTTP calls are injected from the main package (mirrors
	// SetDeviceCheck/SetOngoingCheck's existing injection direction).
	foreignRequestReedHook        ForeignRequestReedHook
	foreignDeliverHook            ForeignDeliverHook
	foreignCancelHook             ForeignCancelHook
	foreignSubscribeProfileHook   ForeignSubscribeProfileHook
	foreignAckHook                ForeignAckHook
	foreignUnsubscribeProfileHook ForeignUnsubscribeProfileHook
	foreignSubscribeReedHook      ForeignSubscribeReedHook
	foreignUnsubscribeReedHook    ForeignUnsubscribeReedHook
	foreignReedStatsHook          ForeignReedStatsHook
	foreignReplyNotifyHook        ForeignReplyNotifyHook
	foreignHolderNotifyHook       ForeignHolderNotifyHook
	foreignFallbackHook           ForeignFallbackRequestHook
	foreignNewReedNotifyHook      ForeignNewReedNotifyHook
}

// NewService creates a new realtime service. serverID is this server's own
// id, needed to build the identities.id form for every FK'd column touched
// by DBService/AuthService (see db.go's/auth.go's doc comments).
func NewService(db *sql.DB, crypto *crypto.Service, allowedOrigin, serverID string) *RealtimeService {
	dbService := NewDBService(db, serverID)
	authService := NewAuthService(db, crypto, serverID)
	connManager := NewConnectionManager()
	log.Info().Msg("[OK] Realtime services initialized successfully")

	return &RealtimeService{
		connManager:   connManager,
		dbService:     dbService,
		authService:   authService,
		crypto:        crypto,
		allowedOrigin: allowedOrigin,
		metrics:       metrics.Noop{},
	}
}

// SetMetrics installs the business-metrics recorder.
func (rs *RealtimeService) SetMetrics(rec metrics.Recorder) {
	if rec == nil {
		rs.metrics = metrics.Noop{}
		return
	}
	rs.metrics = rec
}

// SetOngoingCheck installs an optional import-gate check used after WebSocket
// auth succeeds. When the check returns true, the connection is rejected with 403.
func (rs *RealtimeService) SetOngoingCheck(check func(userID string) (bool, error)) {
	rs.ongoingCheck = check
}

// SetDeviceCheck installs the active-device check used after WebSocket auth succeeds.
func (rs *RealtimeService) SetDeviceCheck(check func(userID, deviceID string) error) {
	rs.deviceCheck = check
}

// ForeignRequestResult reports the outcome of registering a REQUEST_REED
// with a reed's home server.
type ForeignRequestResult int

const (
	ForeignRequestOK ForeignRequestResult = iota
	ForeignRequestReedNotFound
	ForeignRequestReedNotHeld
)

// ForeignRequestReedHook registers requesterUserID's interest in reedID
// with reedID's home server over peer HTTP (leg 1), returning the home
// server's own event id on success.
type ForeignRequestReedHook func(ctx context.Context, reedID, requesterUserID, localRequestID string) (result ForeignRequestResult, peerEventID string, err error)

// SetForeignRequestReedHook installs the leg-1 (register-request) hook.
func (rs *RealtimeService) SetForeignRequestReedHook(hook ForeignRequestReedHook) {
	rs.foreignRequestReedHook = hook
}

// ForeignDeliverHook delivers relayed data for peerEventID back to
// requestingServerID over peer HTTP (leg 2), called on the home server
// once a local holder relays content for a peer-registered request.
type ForeignDeliverHook func(ctx context.Context, requestingServerID, peerEventID string, data json.RawMessage) error

// SetForeignDeliverHook installs the leg-2 (deliver-response) hook.
func (rs *RealtimeService) SetForeignDeliverHook(hook ForeignDeliverHook) {
	rs.foreignDeliverHook = hook
}

// ForeignCancelHook notifies homeServerID over peer HTTP (leg 4) that
// peerEventID's originating requester disconnected and its pending event
// should be dropped.
type ForeignCancelHook func(ctx context.Context, homeServerID, peerEventID string) error

// SetForeignCancelHook installs the leg-4 (cancel-request) hook.
func (rs *RealtimeService) SetForeignCancelHook(hook ForeignCancelHook) {
	rs.foreignCancelHook = hook
}

// ForeignUnsubscribeProfileHook notifies homeServerID over peer HTTP that
// requestingUserID no longer wants live fanout for authorID — the
// teardown counterpart of ForeignSubscribeProfileHook's durable
// registration (leg 1b). Without this, B's profile_subscriptions row for
// a departed A viewer would never be cleaned up and B would keep trying
// to fan out to them forever.
type ForeignUnsubscribeProfileHook func(ctx context.Context, authorID, requestingUserID string) error

// SetForeignUnsubscribeProfileHook installs the leg-7 (unsubscribe-profile) hook.
func (rs *RealtimeService) SetForeignUnsubscribeProfileHook(hook ForeignUnsubscribeProfileHook) {
	rs.foreignUnsubscribeProfileHook = hook
}

// ForeignUnsubscribeReedHook notifies reedID's home server over peer HTTP
// that requestingUserID no longer wants live stats for it — the teardown
// counterpart of ForeignSubscribeReedHook's registration (leg 8).
type ForeignUnsubscribeReedHook func(ctx context.Context, reedID, requestingUserID string) error

// SetForeignUnsubscribeReedHook installs the leg-9 (unsubscribe-reed) hook.
func (rs *RealtimeService) SetForeignUnsubscribeReedHook(hook ForeignUnsubscribeReedHook) {
	rs.foreignUnsubscribeReedHook = hook
}

// ForeignReplyNotifyHook tells parentReedID's home server over peer HTTP
// that replyReedID (authored on this server) replies to it — the write
// side of cross-server replying. Without this, a reply to a foreign reed
// is created successfully but never surfaces on the parent's home server
// at all: no reed_replies row there means its reply-count/thread queries
// never find it.
type ForeignReplyNotifyHook func(ctx context.Context, parentReedID, replyReedID, threadID string, ts time.Time) error

// SetForeignReplyNotifyHook installs the leg-11 (reply-notify) hook.
func (rs *RealtimeService) SetForeignReplyNotifyHook(hook ForeignReplyNotifyHook) {
	rs.foreignReplyNotifyHook = hook
}

// ForeignReedStatsHook pushes a pre-built WS message (REED_COVERAGE,
// REED_ECHOES, REED_REPLIES, REED_LIKES, RIPPLE_POSTED, or
// RIPPLE_UPDATED — already JSON-marshaled) to requestingServerID over
// peer HTTP (leg 10), for one reed-stats subscriber on that peer. Opaque
// payload, not a typed snapshot: the receiving server relays it to its
// own local client unmodified, exactly like every other relayed-content
// path in this file — the client independently verifies whatever needs
// verifying (a ripple's signature; a bare count needs none).
type ForeignReedStatsHook func(ctx context.Context, requestingServerID, requestingUserID string, payload json.RawMessage) error

// SetForeignReedStatsHook installs the leg-10 (reed-stats push) hook.
func (rs *RealtimeService) SetForeignReedStatsHook(hook ForeignReedStatsHook) {
	rs.foreignReedStatsHook = hook
}

// ForeignAckHook notifies homeServerID over peer HTTP (leg 5) that
// peerEventID's delivered content was verified and locally allocated on
// the originating server — the home server should mirror that
// allocation on its own side (against its per-peer sentinel) so a
// future GetUnallocatedReeds-style query for this peer excludes it,
// instead of re-offering content the peer already has on every
// subscribe. Fire-and-forget: the originating server has already
// persisted its own allocation before calling this (the client's
// verified content is never lost even if this notification fails —
// only the home server's bookkeeping goes stale, the same pre-existing
// class of gap as an unacked local pending event).
type ForeignAckHook func(ctx context.Context, homeServerID, peerEventID string) error

// SetForeignAckHook installs the leg-5 (ack-delivered) hook.
func (rs *RealtimeService) SetForeignAckHook(hook ForeignAckHook) {
	rs.foreignAckHook = hook
}

// ForeignHolderNotifyHook tells homeServerID over peer HTTP that this
// server now holds a verified copy of reedID (a reed it doesn't own) —
// fire-and-forget, called whenever a local client acks a foreign reed,
// regardless of which delivery path brought it. Distinct from
// ForeignAckHook: that closes out a specific leg-1-originated pending
// event; this simply informs the home server it has a new fallback target
// for reedID, and can fire even when there is no matching
// foreign_pending_events row (e.g. content that arrived via ordinary
// FOLLOW_REED/REED_REPLY fanout, not a REQUEST_REED this server itself
// initiated).
type ForeignHolderNotifyHook func(ctx context.Context, homeServerID, reedID string) error

// SetForeignHolderNotifyHook installs the holder-notify hook.
func (rs *RealtimeService) SetForeignHolderNotifyHook(hook ForeignHolderNotifyHook) {
	rs.foreignHolderNotifyHook = hook
}

// ForeignFallbackRequestHook asks peerServerID — a server previously
// notified (via ForeignHolderNotifyHook) that it holds a copy of reedID —
// to relay that copy back to one of THIS server's own local users. Unlike
// ForeignRequestReedHook (leg 1), which always targets reedID's own home
// server, this targets an explicit candidate peer chosen from
// GetForeignHolderServers, since reedID's embedded home server is this
// server itself in the fallback scenario.
type ForeignFallbackRequestHook func(ctx context.Context, peerServerID, reedID, requesterUserID, localRequestID string) (result ForeignRequestResult, peerEventID string, err error)

// SetForeignFallbackRequestHook installs the fallback-request hook.
func (rs *RealtimeService) SetForeignFallbackRequestHook(hook ForeignFallbackRequestHook) {
	rs.foreignFallbackHook = hook
}

// ForeignNewReedNotifyHook pushes one newly-published reed (authorID is
// local to this server, the home server H) out to requestingServerID (O) so
// O can register it against a durable profile subscription it already
// holds — the live counterpart of ForeignSubscribeProfileHook's one-time
// backfill (leg 1b). Without this push, O never learns peerEventID exists,
// so H's eventual RELAY_RESPONSE for it has nothing on O's side to resolve
// against and is silently dropped (leg 2's DeliverRelayResponseFromPeer
// 404s). Fire-and-forget from the caller's side: a delivery failure here
// just means that one viewer misses this reed's live push and falls back
// to picking it up on their next resubscribe/reload, exactly as if this
// hook didn't exist.
type ForeignNewReedNotifyHook func(ctx context.Context, requestingServerID, authorID, requesterUserID, reedID, peerEventID string) error

// SetForeignNewReedNotifyHook installs the leg-17 (new-reed-notify) hook.
func (rs *RealtimeService) SetForeignNewReedNotifyHook(hook ForeignNewReedNotifyHook) {
	rs.foreignNewReedNotifyHook = hook
}

// DisconnectUser closes all WebSocket connections for a user (device rebind kick).
// Its caller passes a bare userID, but connManager's registry is keyed by
// the "userID@serverID" form (see auth.go) — convert here at the boundary.
func (rs *RealtimeService) DisconnectUser(userID string) {
	selfIdentity := identity.CanonicalID(rs.dbService.serverID, userID)
	rs.connManager.DisconnectUser(string(selfIdentity))
}

// Shutdown notifies every connected client the server is going away and
// closes their sockets. Call this before the process exits so clients can
// reconnect immediately instead of waiting on a connection that silently
// went dead.
func (rs *RealtimeService) Shutdown() {
	rs.connManager.BroadcastShutdown()
}

func (rs *RealtimeService) deviceMismatch(userID, deviceID string) bool {
	if rs.deviceCheck == nil {
		return false
	}
	return rs.deviceCheck(userID, deviceID) != nil
}

func (rs *RealtimeService) ongoingImport(userID string) (bool, error) {
	if rs.ongoingCheck == nil {
		return false, nil
	}
	return rs.ongoingCheck(userID)
}

// Start starts the realtime service
func (rs *RealtimeService) Start(broadcastChan <-chan BroadcastMessage) {
	// Start the connection manager
	go rs.connManager.Start()

	// Start the broadcast handler
	go rs.handleBroadcasts(broadcastChan)

	// Start periodic cleanup
	go rs.startPeriodicCleanup()

	log.Info().Msg("[OK] Realtime service started")
}

// handleBroadcasts handles incoming broadcast messages from the main app
func (rs *RealtimeService) handleBroadcasts(broadcastChan <-chan BroadcastMessage) {
	for message := range broadcastChan {
		log.Debug().
			Str("type", message.Type.String()).
			Str("userID", message.UserID).
			Str("reedID", message.ReedID).
			Msg("Received broadcast message")

		if message.Type == EchoCountChanged {
			reedID := string(identity.AppendEntity(identity.IdentityID(message.UserID), message.ReedID))
			rs.notifyReedEchoes(reedID)
		}

		if message.Type == ReplyCountChanged {
			reedID := string(identity.AppendEntity(identity.IdentityID(message.UserID), message.ReedID))
			rs.notifyReedReplies(reedID)
		}

		if message.Type == LikeCountChanged {
			reedID := string(identity.AppendEntity(identity.IdentityID(message.UserID), message.ReedID))
			rs.notifyReedLikes(reedID)
		}

		if message.Type == ReplyPosted {
			reedID := string(identity.AppendEntity(identity.IdentityID(message.UserID), message.ReedID))
			replyReedID := string(identity.AppendEntity(identity.IdentityID(message.ReplyUserID), message.ReplyReedID))
			rs.notifyReedSubscribersOfReply(reedID, replyReedID)
		}

		if message.Type == RipplePosted && message.Ripple != nil {
			reedID := string(identity.AppendEntity(identity.IdentityID(message.UserID), message.ReedID))
			rs.notifyRipplePosted(reedID, message.Ripple.UserID, *message.Ripple)
		}

		if message.Type == RippleUpdated && message.Ripple != nil {
			reedID := string(identity.AppendEntity(identity.IdentityID(message.UserID), message.ReedID))
			rs.notifyRippleUpdated(reedID, message.Ripple.UserID, *message.Ripple)
		}

		if message.Type == ReedRemoved {
			reedID := string(identity.AppendEntity(identity.IdentityID(message.UserID), message.ReedID))
			log.Info().
				Str("userID", message.UserID).
				Str("reedID", reedID).
				Msg("Reed removed; fanout cert")

			cert := message.ReedRemoval
			followers, err := rs.dbService.GetOnlineFollowers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get online followers for reed removal")
			}
			broadcastRecipients, err := rs.dbService.GetBroadcastSubscribers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get broadcast subscribers for reed removal")
			}
			rs.dispatchRemovalMany(followers, reedID, cert)
			rs.dispatchRemovalMany(broadcastRecipients, reedID, cert)

			profileSubscribers, err := rs.dbService.GetProfileSubscribers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get profile subscribers for reed removal")
			}
			for _, sub := range profileSubscribers {
				rs.dispatchRemovalTo(sub.ViewerUserID, reedID, cert)
			}

			// Anyone viewing this reed's thread (SUBSCRIBE_REED) needs to know
			// it's gone too — same gap as ReplyPosted: a reed-stat subscriber
			// isn't necessarily a follower/broadcast/profile subscriber.
			reedSubscribers := rs.connManager.ReedSubscriberUserIDs(reedID, "")
			rs.dispatchRemovalMany(reedSubscribers, reedID, cert)

			// If the removed reed was itself a reply, everyone subscribed to
			// an ancestor further up the thread also needs the removal notice
			// — they were shown the reply and need to know it's gone, same as
			// notifyReplyAncestorsOfReply for a newly posted reply.
			rs.notifyReplyAncestorsOfRemoval(reedID, cert)
		}

		if message.Type == AccountRemoved {
			log.Info().
				Str("userID", message.UserID).
				Msg("Account removed; fanout cert")

			cert := message.AccountRemoval
			followers, err := rs.dbService.GetOnlineFollowers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get online followers for account removal")
			}
			broadcastRecipients, err := rs.dbService.GetBroadcastSubscribers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get broadcast subscribers for account removal")
			}
			rs.dispatchAccountRemovalMany(followers, message.UserID, cert)
			rs.dispatchAccountRemovalMany(broadcastRecipients, message.UserID, cert)

			profileSubscribers, err := rs.dbService.GetProfileSubscribers(context.Background(), message.UserID)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get profile subscribers for account removal")
			}
			for _, sub := range profileSubscribers {
				rs.dispatchAccountRemovalTo(sub.ViewerUserID, message.UserID, cert)
			}
		}
	}
}

// fanoutNewReed dispatches a newly published reed to followers, broadcast subs,
// profile subs, and pipe listeners for the claimed tags. reedID is canonical.
func (rs *RealtimeService) fanoutNewReed(reedID string, tags []string) {
	authorUserID := reedAuthorIdentity(reedID)
	log.Info().
		Str("userID", authorUserID).
		Str("reedID", reedID).
		Int("tags", len(tags)).
		Msg("Fanning out new reed")

	broadcastRecipients, err := rs.dbService.GetBroadcastSubscribers(context.Background(), authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get broadcast subscribers from database")
	}

	rs.fanoutNewReedCore(reedID, broadcastRecipients, tags)
}

// fanoutNewReedNoBroadcast dispatches to followers, profile subs, and pipe
// listeners only (no broadcast stream). reedID is canonical.
func (rs *RealtimeService) fanoutNewReedNoBroadcast(reedID string, tags []string) {
	log.Info().
		Str("userID", reedAuthorIdentity(reedID)).
		Str("reedID", reedID).
		Int("tags", len(tags)).
		Msg("Fanning out new reed (no broadcast)")

	rs.fanoutNewReedCore(reedID, nil, tags)
}

func (rs *RealtimeService) fanoutNewReedCore(reedID string, broadcastRecipients, tags []string) {
	authorUserID := reedAuthorIdentity(reedID)
	followers, err := rs.dbService.GetOnlineFollowers(context.Background(), authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get online followers from database")
	}

	pipeListeners := rs.connManager.PipeListenerUserIDs(tags, authorUserID)
	// Pipe listeners always get PIPE_REED (push). Followers who are not on the
	// pipe get FOLLOW_REED. Overlap prefers PIPE_REED (one event).
	followersOnly := subtractUserIDs(followers, pipeListeners)

	durable := unionUserIDs(followersOnly, pipeListeners)
	broadcastOnly := subtractUserIDs(broadcastRecipients, durable)

	log.Info().
		Str("userID", authorUserID).
		Int("followersOnly", len(followersOnly)).
		Int("pipeListeners", len(pipeListeners)).
		Int("broadcastSubscribers", len(broadcastOnly)).
		Msg("Dispatching new reed to recipients")
	rs.dispatchMany(followersOnly, FollowReedEvent, reedID)
	rs.dispatchMany(pipeListeners, PipeReedEvent, reedID)
	rs.dispatchMany(broadcastOnly, BroadcastReedEvent, reedID)

	profileSubscribers, err := rs.dbService.GetProfileSubscribers(context.Background(), authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get profile subscribers from database")
	}

	for _, sub := range profileSubscribers {
		requesterUserID := sub.ViewerUserID
		foreign, viewerServerID := rs.isForeignReed(sub.ViewerUserID)
		if foreign {
			// pending_events.requester_user_id FKs to online_users, which a
			// foreign viewer never has a row in — substitute the per-peer
			// sentinel, same as HandleForeignSubscribeProfile's backfill
			// path. Delivery still ends up at the real viewer: handleRelayResponse's
			// default branch checks foreign_relay_requests (recorded below)
			// and routes to foreignDeliverHook instead of a local WS send.
			sentinelUserID, err := rs.dbService.EnsurePeerSentinelUser(context.Background(), viewerServerID)
			if err != nil {
				log.Error().Err(err).Str("viewerUserID", sub.ViewerUserID).Msg("Failed to ensure peer sentinel for foreign profile subscriber")
				continue
			}
			requesterUserID = sentinelUserID
		}

		eventID := generateEventID(requesterUserID)
		requestID := generateEventID(requesterUserID)
		if err := rs.dbService.CreateProfileSubscriptionEvent(context.Background(), eventID, requestID, requesterUserID, ProfileSubscriptionEvent, reedID, sub.SubscriptionID); err != nil {
			log.Error().
				Err(err).
				Str("viewerUserID", sub.ViewerUserID).
				Msg("Failed to create pending event for profile subscriber")
			continue
		}

		if foreign {
			if err := rs.recordForeignRelayRequest(context.Background(), eventID, viewerServerID, sub.ViewerUserID); err != nil {
				log.Error().Err(err).Str("viewerUserID", sub.ViewerUserID).Msg("Failed to record foreign relay request for live profile fanout")
				continue
			}
			// Tell the viewer's own server about this specific new event so
			// it can register foreign_pending_events against its existing
			// subscription — without this push, only reeds that existed at
			// SUBSCRIBE_PROFILE time ever get such a mapping there, and this
			// eventID's eventual RELAY_RESPONSE has nothing to resolve
			// against on the viewer's side (see ForeignNewReedNotifyHook).
			if rs.foreignNewReedNotifyHook != nil {
				if err := rs.foreignNewReedNotifyHook(context.Background(), viewerServerID, authorUserID, sub.ViewerUserID, reedID, eventID); err != nil {
					log.Error().Err(err).Str("viewerUserID", sub.ViewerUserID).Msg("Failed to notify viewer's server of new reed for live profile fanout")
				}
			}
		}
	}

	rs.dispatchNextIfConnected(authorUserID)
}

func unionUserIDs(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, id := range a {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range b {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func subtractUserIDs(from, remove []string) []string {
	if len(from) == 0 {
		return nil
	}
	drop := make(map[string]struct{}, len(remove))
	for _, id := range remove {
		drop[id] = struct{}{}
	}
	out := make([]string, 0, len(from))
	for _, id := range from {
		if _, ok := drop[id]; ok {
			continue
		}
		out = append(out, id)
	}
	return out
}

// dispatchRemovalMany enqueues reed_removed pending events and delivers certs
// server-side (no holder relay). reedID is canonical.
func (rs *RealtimeService) dispatchRemovalMany(recipients []string, reedID string, cert *ReedRemovalWire) {
	for _, recipientID := range recipients {
		rs.dispatchRemovalTo(recipientID, reedID, cert)
	}
}

func (rs *RealtimeService) dispatchRemovalTo(recipientID, reedID string, cert *ReedRemovalWire) {
	requestID, err := rs.dbService.GetSyncRequestID(context.Background(), recipientID)
	if err != nil || requestID == "" {
		return
	}
	eventID := generateEventID(recipientID)
	if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, recipientID, ReedRemovedEvent, reedID); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Msg("Failed to create reed_removed pending event")
		return
	}
	rs.deliverReedRemoved(eventID, requestID, recipientID, reedID, cert)
}

func (rs *RealtimeService) dispatchAccountRemovalMany(recipients []string, removedUserID string, cert *AccountRemovalWire) {
	for _, recipientID := range recipients {
		rs.dispatchAccountRemovalTo(recipientID, removedUserID, cert)
	}
}

func (rs *RealtimeService) dispatchAccountRemovalTo(recipientID, removedUserID string, cert *AccountRemovalWire) {
	requestID, err := rs.dbService.GetSyncRequestID(context.Background(), recipientID)
	if err != nil || requestID == "" {
		return
	}
	eventID := generateEventID(recipientID)
	if err := rs.dbService.CreatePendingAccountEvent(context.Background(), eventID, requestID, recipientID, removedUserID); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Msg("Failed to create account_removed pending event")
		return
	}
	rs.deliverAccountRemoved(eventID, requestID, recipientID, removedUserID, cert)
}

func (rs *RealtimeService) deliverAccountRemoved(eventID, requestID, recipientID, removedUserID string, cert *AccountRemovalWire) {
	wire := AccountRemovalWire{}
	if cert != nil {
		wire = *cert
	} else {
		var err error
		wire, err = rs.dbService.GetAccountRemovalWire(context.Background(), removedUserID)
		if err != nil || wire.UserID == "" {
			log.Error().Err(err).Str("userID", removedUserID).Msg("Failed to load account removal cert for delivery")
			return
		}
	}
	ok, err := rs.dbService.MarkEventDispatched(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to mark account_removed dispatched")
		return
	}
	if !ok {
		return
	}
	if err := rs.connManager.SendToUser(recipientID, NewAccountRemovedMsg(eventID, requestID, removedUserID, wire)); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Str("userID", removedUserID).Msg("Failed to send ACCOUNT_REMOVED")
	}
}

func (rs *RealtimeService) deliverReedRemoved(eventID, requestID, recipientID, reedID string, cert *ReedRemovalWire) {
	wire := ReedRemovalWire{}
	if cert != nil {
		wire = *cert
	} else {
		var err error
		wire, err = rs.dbService.GetReedRemovalWire(context.Background(), reedID)
		if err != nil || wire.UserID == "" {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to load reed removal cert for delivery")
			return
		}
	}
	ok, err := rs.dbService.MarkEventDispatched(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to mark reed_removed dispatched")
		return
	}
	if !ok {
		return
	}
	if err := rs.connManager.SendToUser(recipientID, NewReedRemovedMsg(eventID, requestID, reedID, wire)); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Str("reedID", reedID).Msg("Failed to send REED_REMOVED")
	}
}

// dispatchMany creates a pending reed event for each recipient and triggers
// relay dispatch to the reed's author (also the holder for published-reed
// fanout). reedID is canonical.
func (rs *RealtimeService) dispatchMany(recipients []string, eventName EventName, reedID string) {
	if foreign, homeServerID := rs.isForeignReed(reedID); foreign {
		rs.dispatchManyForeign(recipients, eventName, reedID, homeServerID)
		return
	}

	authorUserID := reedAuthorIdentity(reedID)
	for _, recipientID := range recipients {
		requestID, err := rs.dbService.GetSyncRequestID(context.Background(), recipientID)
		if err != nil || requestID == "" {
			// User hasn't sent SYNC_REQUEST yet; they'll receive this via catchUp on connect.
			log.Debug().
				Str("recipientID", recipientID).
				Str("eventName", string(eventName)).
				Msg("Skipping recipient: no sync_request_id set")
			continue
		}
		eventID := generateEventID(recipientID)
		if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, recipientID, eventName, reedID); err != nil {
			log.Error().
				Err(err).
				Str("recipientID", recipientID).
				Msg("Failed to create pending event")
			continue
		}
		log.Debug().
			Str("recipientID", recipientID).
			Str("eventID", eventID).
			Str("holderUserID", authorUserID).
			Msg("Pending event created, dispatching to holder")
		rs.dispatchNextIfConnected(authorUserID)
	}
}

// dispatchManyForeign is dispatchMany's branch for a foreign-authored
// reedID: the recipient's own holder-dispatch never fires, since reedID's
// real holder (its author) is connected to reedID's home server, not this
// one — the exact gap that left foreign-authored FOLLOW_REED/PIPE_REED/
// REED_REPLY fanout silently undelivered. Every recipient — whether local
// to this server or (via notifyForeignReedSubscribersOfReply-style
// callers) itself foreign — needs the same cross-server relay-holder
// bridge already used for a client's own foreign REQUEST_REED, registered
// once per recipient against reedID's true home server.
func (rs *RealtimeService) dispatchManyForeign(recipients []string, eventName EventName, reedID, homeServerID string) {
	if rs.foreignRequestReedHook == nil {
		return
	}
	if err := rs.dbService.UpsertReedIdentity(context.Background(), reedID); err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to upsert reed identity for foreign dispatch")
		return
	}
	for _, recipientID := range recipients {
		requestID := generateEventID(recipientID)
		eventID := generateEventID(recipientID)
		if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, recipientID, eventName, reedID); err != nil {
			log.Error().Err(err).Str("recipientID", recipientID).Msg("Failed to create local pending event for foreign dispatch")
			continue
		}
		result, peerEventID, err := rs.foreignRequestReedHook(context.Background(), reedID, recipientID, requestID)
		if err != nil || result != ForeignRequestOK {
			if err != nil {
				log.Error().Err(err).Str("reedID", reedID).Str("homeServerID", homeServerID).Msg("Failed to register foreign dispatch with home server")
			}
			if delErr := rs.dbService.DeletePendingEvent(context.Background(), eventID); delErr != nil {
				log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete speculative pending event after foreign dispatch failure")
			}
			continue
		}
		if err := rs.dbService.CreateForeignPendingEvent(context.Background(), eventID, homeServerID, peerEventID); err != nil {
			log.Error().Err(err).Msg("Failed to record foreign pending event mapping for foreign dispatch")
			if delErr := rs.dbService.DeletePendingEvent(context.Background(), eventID); delErr != nil {
				log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete pending event after foreign_pending_events insert failure")
			}
		}
	}
}

// startPeriodicCleanup runs periodic cleanup tasks
func (rs *RealtimeService) startPeriodicCleanup() {
	// TODO: Implement periodic cleanup of stale connections
	// This could run every 5 minutes to clean up old online_users entries
}

// HandleWebSocket handles WebSocket connections
func (rs *RealtimeService) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("WebSocket connection attempt")

	// Authenticate the connection
	userID, err := rs.authService.AuthenticateWebSocket(r)
	if err != nil {
		log.Error().Err(err).Msg("WebSocket authentication failed")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	log.Info().
		Str("userID", userID).
		Msg("WebSocket authentication successful")

	deviceID := r.URL.Query().Get("deviceId")
	if rs.deviceMismatch(userID, deviceID) {
		rejectConnection(w, "Device mismatch: this session is not bound to the active device.")
		return
	}

	ongoing, err := rs.ongoingImport(userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Import gate check failed")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if ongoing {
		log.Info().Str("userID", userID).Msg("WebSocket rejected: ongoing recovery import")
		rejectConnection(w, "Finish recovery import first.")
		return
	}

	// Check if the response writer implements Hijacker
	if _, ok := w.(http.Hijacker); !ok {
		log.Error().Msg("Response writer does not implement http.Hijacker")
		http.Error(w, "WebSocket not supported", http.StatusInternalServerError)
		return
	}

	// Upgrade HTTP connection to WebSocket
	upgrader := websocket.Upgrader{
		CheckOrigin:     rs.checkOrigin,
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upgrade connection to WebSocket")
		return
	}
	defer conn.Close()

	// Create client and register
	client := NewClient(conn, userID)
	client.wsRecordOutbound = func(messageType int, data []byte) {
		rs.metrics.WSMessage(context.Background(), metrics.DirectionOut, metrics.WSMessageType(messageType, data))
	}
	rs.connManager.RegisterClient(client)

	// Mark user as online
	if err := rs.dbService.MarkUserOnline(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Failed to mark user as online")
		return
	}

	// Dispatch any pending relay requests for reeds this user holds
	rs.handleUserCameOnline(client)

	log.Info().
		Str("userID", userID).
		Msg("WebSocket client connected")

	// Handle incoming messages
	rs.handleClientMessages(client)

	// Cleanup when connection closes. Only tear down DB presence when this
	// was the user's last socket — a superseded connection must not wipe
	// online_users, broadcast subscriptions, or in-flight pending events.
	stillOnline := rs.connManager.UnregisterClient(client)
	if stillOnline {
		log.Info().
			Str("userID", userID).
			Msg("WebSocket client replaced; keeping online state")
		return
	}

	if err := rs.dbService.MarkUserOffline(context.Background(), userID); err != nil {
		log.Error().Err(err).Msg("Failed to mark user as offline")
	}

	// Remove broadcast subscription on disconnect
	if err := rs.dbService.UnsubscribeFromBroadcast(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to remove broadcast subscription on disconnect")
	}

	// Notify any home servers this user had outstanding cross-server relay
	// requests with, so they stop tracking them — must run BEFORE the
	// cascade delete below removes the correlating foreign_pending_events
	// rows. Best-effort: an HTTP failure here must not block local cleanup.
	if rs.foreignCancelHook != nil {
		foreignPending, err := rs.dbService.GetForeignPendingEventsByRequester(context.Background(), userID)
		if err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Failed to load foreign pending events on disconnect")
		} else {
			for _, fpe := range foreignPending {
				if err := rs.foreignCancelHook(context.Background(), fpe.HomeServerID, fpe.PeerEventID); err != nil {
					log.Error().Err(err).Str("eventID", fpe.EventID).Str("homeServerID", fpe.HomeServerID).Msg("Failed to notify home server of cancelled relay request")
				}
			}
		}
	}

	// Discard all pending relay events for this requester (cascades from profile_subscriptions too)
	if err := rs.dbService.DeletePendingEventsByUser(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to delete pending events on disconnect")
	}

	// Notify any foreign authors' home servers that this viewer's live
	// fanout subscriptions are going away — must run BEFORE the delete
	// below removes the local rows. Same best-effort/non-blocking
	// treatment as the foreignCancelHook block above.
	if rs.foreignUnsubscribeProfileHook != nil {
		viewerSubs, err := rs.dbService.GetProfileSubscriptionsByViewer(context.Background(), userID)
		if err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Failed to load profile subscriptions on disconnect")
		} else {
			for _, sub := range viewerSubs {
				if foreign, _ := rs.isForeignReed(sub.AuthorUserID); foreign {
					if err := rs.foreignUnsubscribeProfileHook(context.Background(), sub.AuthorUserID, userID); err != nil {
						log.Error().Err(err).Str("authorID", sub.AuthorUserID).Msg("Failed to notify home server of profile unsubscribe on disconnect")
					}
				}
			}
		}
	}

	// Clean up any active profile subscriptions for this viewer
	if err := rs.dbService.DeleteProfileSubscriptionsByViewer(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to delete profile subscriptions on disconnect")
	}

	// Same treatment for reed-stats subscriptions: notify foreign reeds'
	// home servers before the local rows are deleted below.
	if rs.foreignUnsubscribeReedHook != nil {
		reedSubs, err := rs.dbService.GetReedSubscriptionsByViewer(context.Background(), userID)
		if err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Failed to load reed subscriptions on disconnect")
		} else {
			for _, sub := range reedSubs {
				if foreign, _ := rs.isForeignReed(sub.ReedID); foreign {
					if err := rs.foreignUnsubscribeReedHook(context.Background(), sub.ReedID, userID); err != nil {
						log.Error().Err(err).Str("reedID", sub.ReedID).Msg("Failed to notify home server of reed stats unsubscribe on disconnect")
					}
				}
			}
		}
	}

	// Clean up any active reed-stats subscriptions for this viewer
	if err := rs.dbService.DeleteReedSubscriptionsByViewer(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to delete reed subscriptions on disconnect")
	}

	log.Info().
		Str("userID", userID).
		Msg("WebSocket client disconnected")
}

// checkOrigin validates the Origin header against the allowed origin
func (rs *RealtimeService) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")

	// If no origin is provided, reject the connection for security
	if origin == "" {
		log.Warn().Msg("WebSocket connection rejected: missing Origin header")
		return false
	}

	// If no allowed origin is configured, reject all connections
	if rs.allowedOrigin == "" {
		log.Warn().
			Str("origin", origin).
			Msg("WebSocket connection rejected: no allowed origin configured")
		return false
	}

	// Check if the origin matches the allowed origin
	if origin == rs.allowedOrigin {
		log.Debug().
			Str("origin", origin).
			Msg("WebSocket origin validated")
		return true
	}

	log.Warn().
		Str("origin", origin).
		Str("allowedOrigin", rs.allowedOrigin).
		Msg("WebSocket connection rejected: origin not allowed")
	return false
}

// handleClientMessages handles incoming messages from a client
func (rs *RealtimeService) handleClientMessages(client *Client) {
	for {
		messageType, data, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Error().Err(err).Msg("WebSocket error")
			} else {
				log.Info().Msg("Client disconnected")
			}
			break
		}

		// Handle both binary (protobuf) and text (JSON) messages
		rs.metrics.WSMessage(context.Background(), metrics.DirectionIn, metrics.WSMessageType(messageType, data))

		switch messageType {
		case websocket.BinaryMessage:
			rs.handleProtobufMessage(client, data)
		case websocket.TextMessage:
			rs.handleJSONMessage(client, data)
		default:
			log.Warn().Int("messageType", messageType).Msg("Received unsupported message type, ignoring")
			continue
		}
	}
}

// handleProtobufMessage handles protobuf messages
func (rs *RealtimeService) handleProtobufMessage(client *Client, data []byte) {
	var msg pb.WSMessage
	if err := proto.Unmarshal(data, &msg); err != nil {
		log.Error().Err(err).Msg("Failed to unmarshal protobuf message")
		return
	}

	log.Debug().Str("type", msg.Type.String()).Msg("Received protobuf WebSocket message")

	// Handle different message types
	switch msg.Type {
	case pb.MessageType_PING:
		rs.handlePing(client, msg.GetPing())

	case pb.MessageType_SUBSCRIBE_USER:
		rs.handleSubscribeUser(client, msg.GetSubscribe())

	case pb.MessageType_SUBSCRIBE_BROADCAST:
		rs.handleSubscribeBroadcast(client, msg.GetSubscribe())

	case pb.MessageType_UNSUBSCRIBE_USER:
		rs.handleUnsubscribeUser(client, msg.GetSubscribe())

	case pb.MessageType_UNSUBSCRIBE_BROADCAST:
		rs.handleUnsubscribeBroadcast(client, msg.GetSubscribe())

	default:
		log.Warn().Str("type", msg.Type.String()).Msg("Unknown protobuf WebSocket message type")
	}
}

// handleJSONMessage handles JSON messages for testing
func (rs *RealtimeService) handleJSONMessage(client *Client, data []byte) {
	log.Debug().Str("data", string(data)).Msg("Received JSON WebSocket message")

	var jsonMsg InboundJSONMsg
	if err := json.Unmarshal(data, &jsonMsg); err != nil {
		log.Error().Err(err).Str("data", string(data)).Msg("Failed to unmarshal JSON message")
		return
	}

	if jsonMsg.Type == "" {
		log.Warn().Str("data", string(data)).Msg("JSON message missing type field")
		return
	}

	// Handle different message types
	switch jsonMsg.Type {
	case "ping":
		response := PongMsg{Type: "pong", Data: jsonMsg.Data}
		if jsonBytes, err := json.Marshal(response); err == nil {
			client.writeMessage(websocket.TextMessage, jsonBytes)
		}

	case "SUBSCRIBE_USER":
		rs.handleSubscribeUserJSON(client)

	case "SUBSCRIBE_BROADCAST":
		rs.handleSubscribeBroadcastJSON(client)

	case "UNSUBSCRIBE_USER":
		rs.handleUnsubscribeUserJSON(client)

	case "UNSUBSCRIBE_BROADCAST":
		rs.handleUnsubscribeBroadcastJSON(client)

	case "SYNC_REQUEST":
		var syncData SyncRequestData
		if err := json.Unmarshal(jsonMsg.Data, &syncData); err == nil {
			rs.handleSyncRequest(client, syncData)
		}

	case "REQUEST_REED":
		rs.handleRequestReed(client, jsonMsg.Data)

	case "RELAY_RESPONSE":
		rs.handleRelayResponse(client, jsonMsg.Data)

	case "RELAY_MISS":
		var d RelayMissData
		if err := json.Unmarshal(jsonMsg.Data, &d); err == nil {
			rs.handleRelayMiss(client, d)
		}

	case "DATA_ACK":
		var d DataAckData
		if err := json.Unmarshal(jsonMsg.Data, &d); err == nil {
			rs.handleDataAck(client, d)
		}

	case "DATA_INVALID":
		var d DataInvalidData
		if err := json.Unmarshal(jsonMsg.Data, &d); err == nil {
			rs.handleDataInvalid(client, d)
		}

	case "KEY_FETCH_ERROR":
		var d KeyFetchErrorData
		if err := json.Unmarshal(jsonMsg.Data, &d); err == nil {
			rs.handleKeyFetchError(client, d)
		}

	case "REVOKED_KEY_USED":
		var d RevokedKeyUsedData
		if err := json.Unmarshal(jsonMsg.Data, &d); err == nil {
			rs.handleRevokedKeyUsed(client, d)
		}

	case "SUBSCRIBE_PROFILE":
		rs.handleSubscribeProfile(client, jsonMsg.Data)

	case "UNSUBSCRIBE_PROFILE":
		rs.handleUnsubscribeProfile(client, jsonMsg.Data)

	case "PUBLISH_READY":
		rs.handlePublishReady(client, jsonMsg.Data)

	case "SUBSCRIBE_REED":
		rs.handleSubscribeReed(client, jsonMsg)

	case "UNSUBSCRIBE_REED":
		rs.handleUnsubscribeReed(client, jsonMsg)

	case "SUBSCRIBE_PIPE":
		rs.handleSubscribePipe(client, jsonMsg.Data)

	case "UNSUBSCRIBE_PIPE":
		rs.handleUnsubscribePipe(client, jsonMsg.Data)

	default:
		log.Warn().Str("type", jsonMsg.Type).Msg("Unknown JSON WebSocket message type")
	}
}

// handleSubscribeUserJSON handles JSON user subscription requests
func (rs *RealtimeService) handleSubscribeUserJSON(client *Client) {
	rs.handleSubscribeUser(client, nil)

	response := SubscribedMsg{Type: "subscribed", Data: "Subscribed to user notifications"}
	if jsonBytes, err := json.Marshal(response); err == nil {
		client.writeMessage(websocket.TextMessage, jsonBytes)
	}
}

// handleSubscribeBroadcastJSON handles JSON broadcast subscription requests
func (rs *RealtimeService) handleSubscribeBroadcastJSON(client *Client) {
	rs.handleSubscribeBroadcast(client, nil)

	response := SubscribedMsg{Type: "subscribed", Data: "Subscribed to broadcast notifications"}
	if jsonBytes, err := json.Marshal(response); err == nil {
		client.writeMessage(websocket.TextMessage, jsonBytes)
	}
}

// handleUnsubscribeUserJSON handles JSON user unsubscription requests
func (rs *RealtimeService) handleUnsubscribeUserJSON(client *Client) {
	rs.handleUnsubscribeUser(client, nil)

	// No response needed for unsubscribe
}

// handleUnsubscribeBroadcastJSON handles JSON broadcast unsubscription requests
func (rs *RealtimeService) handleUnsubscribeBroadcastJSON(client *Client) {
	rs.handleUnsubscribeBroadcast(client, nil)

	// No response needed for unsubscribe
}

// handlePing handles ping messages
func (rs *RealtimeService) handlePing(client *Client, ping *pb.PingMessage) {
	response := &pb.WSMessage{
		Type: pb.MessageType_PONG,
		Payload: &pb.WSMessage_Pong{
			Pong: &pb.PongMessage{
				Data: ping.GetData(),
			},
		},
	}

	rs.sendProtobufMessage(client, response)
}

// handleSubscribeUser handles user subscription requests
func (rs *RealtimeService) handleSubscribeUser(client *Client, subscribe *pb.SubscribeMessage) {
	client.Subscribe(SubscribeUser)

	response := &pb.WSMessage{
		Type: pb.MessageType_SUBSCRIBED,
		Payload: &pb.WSMessage_Subscribed{
			Subscribed: &pb.SubscribedMessage{
				Data: "Subscribed to user notifications",
			},
		},
	}

	rs.sendProtobufMessage(client, response)
}

// handleSubscribeBroadcast handles broadcast subscription requests
func (rs *RealtimeService) handleSubscribeBroadcast(client *Client, subscribe *pb.SubscribeMessage) {
	// Persist to database
	err := rs.dbService.SubscribeToBroadcast(context.Background(), client.userID)
	if err != nil {
		log.Error().
			Str("userID", client.userID).
			Err(err).
			Msg("Failed to persist broadcast subscription to database")
		// Continue anyway - in-memory subscription is active
	}

	response := &pb.WSMessage{
		Type: pb.MessageType_SUBSCRIBED,
		Payload: &pb.WSMessage_Subscribed{
			Subscribed: &pb.SubscribedMessage{
				Data: "Subscribed to broadcast notifications",
			},
		},
	}

	rs.sendProtobufMessage(client, response)
}

// handleUnsubscribeUser handles user unsubscription requests
func (rs *RealtimeService) handleUnsubscribeUser(client *Client, subscribe *pb.SubscribeMessage) {
	client.Unsubscribe(SubscribeUser)
}

// handleUnsubscribeBroadcast handles broadcast unsubscription requests
func (rs *RealtimeService) handleUnsubscribeBroadcast(client *Client, subscribe *pb.SubscribeMessage) {
	// Remove from database
	if err := rs.dbService.UnsubscribeFromBroadcast(context.Background(), client.userID); err != nil {
		log.Error().
			Str("userID", client.userID).
			Err(err).
			Msg("Failed to remove broadcast subscription from database")
		// Continue anyway - in-memory subscription is removed
	}
}

// sendProtobufMessage sends a protobuf message to a client
func (rs *RealtimeService) sendProtobufMessage(client *Client, msg *pb.WSMessage) {
	// Marshal the protobuf message
	data, err := proto.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal protobuf message")
		return
	}

	// Send as binary message
	if err := client.writeMessage(websocket.BinaryMessage, data); err != nil {
		log.Error().Err(err).Msg("Failed to write protobuf message")
		return
	}
}

// GetConnectionCount returns the number of active connections
func (rs *RealtimeService) GetConnectionCount() int {
	return rs.connManager.GetConnectionCount()
}

func (rs *RealtimeService) dispatchNext(holderUserID string) {
	pe, err := rs.dbService.GetNextPendingForHolder(context.Background(), holderUserID)
	if err != nil {
		log.Error().Err(err).Str("holderUserID", holderUserID).Msg("Failed to get next pending event for holder")
		return
	}
	if pe == nil {
		log.Debug().Str("holderUserID", holderUserID).Msg("No pending events for holder")
		return
	}
	if EventName(pe.EventName) == ReedRemovedEvent {
		rs.deliverReedRemoved(pe.EventID, pe.RequestID, pe.RequesterUserID, pe.ReedID, nil)
		rs.dispatchNext(holderUserID)
		return
	}
	ok, err := rs.dbService.MarkEventDispatched(context.Background(), pe.EventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", pe.EventID).Msg("Failed to mark event dispatched")
		return
	}
	if !ok {
		return // another replica claimed it
	}
	if err := rs.connManager.SendToUser(holderUserID, NewRelayRequestMsg(pe.EventID, pe.UserID, pe.ReedID)); err != nil {
		log.Error().
			Err(err).
			Str("holderUserID", holderUserID).
			Str("eventID", pe.EventID).
			Str("reedID", pe.ReedID).
			Msg("Failed to send relay request to holder")
		if resetErr := rs.dbService.ResetDispatchedAt(context.Background(), pe.EventID); resetErr != nil {
			log.Error().Err(resetErr).Str("eventID", pe.EventID).Msg("Failed to reset dispatched_at after relay send failure")
		}
		return
	}
	log.Debug().
		Str("holderUserID", holderUserID).
		Str("eventID", pe.EventID).
		Str("reedID", pe.ReedID).
		Msg("Relay request sent to holder")
}

func (rs *RealtimeService) dispatchNextIfConnected(holderUserID string) {
	if holderUserID == "" {
		return
	}
	if !rs.connManager.HasConnection(holderUserID) {
		log.Debug().Str("holderUserID", holderUserID).Msg("Holder has no active WebSocket; skipping relay dispatch")
		return
	}
	rs.dispatchNext(holderUserID)
}

// generateEventID mints a canonical event_id (requesterUserID/uuid) —
// requesterUserID is already userID@serverID, so the result is
// userID@serverID/uuid, self-describing the same way reed/like/key ids
// are. Every event_id inherits the REAL requester's identity: on a home
// server's sentinel-bookkeeping path (HandleForeignRequestReed/
// HandleForeignSubscribeProfile) callers must pass the original remote
// requester's canonical id here, never the sentinel's.
func generateEventID(requesterUserID string) string {
	return string(identity.AppendEntity(identity.IdentityID(requesterUserID), uuid.New().String()))
}

func (rs *RealtimeService) handleRequestReed(client *Client, data json.RawMessage) {
	var req RequestReedData
	if err := json.Unmarshal(data, &req); err != nil {
		return
	}
	if req.RequestID == "" || req.ReedID == "" {
		return
	}
	requestID, reedID := req.RequestID, req.ReedID

	if !rs.validateRequestID(requestID, client.userID) {
		rs.connManager.SendToUser(client.userID, NewInvalidRequestIDErrorMsg(requestID))
		return
	}

	if foreign, homeServerID := rs.isForeignReed(reedID); foreign {
		rs.handleForeignRequestReedFromClient(client, requestID, reedID, homeServerID)
		return
	}

	exists, hasHolders, eventID, err := rs.registerReedRequest(context.Background(), reedID, client.userID, client.userID, requestID, true, RequestReedEvent)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to register reed request")
		return
	}
	if !exists {
		log.Debug().Str("reedID", reedID).Msg("Requested reed does not exist, notifying requester")
		rs.connManager.SendToUser(client.userID, NewReedNotFoundMsg(requestID, reedID))
		return
	}
	if !hasHolders {
		if rs.foreignFallbackHook != nil {
			if fallbackEventID, ok := rs.tryPeerFallback(context.Background(), reedID, client.userID, requestID); ok {
				rs.connManager.SendToUser(client.userID, NewRequestAckMsg(requestID, fallbackEventID, reedID))
				return
			}
		}
		log.Debug().
			Str("reedID", reedID).
			Str("requesterID", client.userID).
			Msg("Requested reed is unheld, notifying requester")
		rs.connManager.SendToUser(client.userID, NewReedNotHeldMsg(requestID, reedID))
		return
	}

	rs.connManager.SendToUser(client.userID, NewRequestAckMsg(requestID, eventID, reedID))
}

// tryPeerFallback runs when no local holder is online for a reed this
// server is home to. Mints a speculative local pending event (mirroring
// handleForeignRequestReedFromClient's own speculative-pending-event
// pattern), then tries each peer server known to hold a copy
// (GetForeignHolderServers), sequentially, stopping at the first that
// accepts the fallback request. Exactly one pending_events row is created
// regardless of how many candidates are tried — never touching
// registerReedRequest's own event-minting path, since that path already
// returned early with hasHolders=false. Sequential rather than parallel:
// this is a rare failure-recovery path, not a hot one, and sequential
// trying avoids needing to cancel losing parallel attempts.
func (rs *RealtimeService) tryPeerFallback(ctx context.Context, reedID, requesterUserID, requestID string) (eventID string, ok bool) {
	peerServerIDs, err := rs.dbService.GetForeignHolderServers(ctx, reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to look up foreign holder servers")
		return "", false
	}
	if len(peerServerIDs) == 0 {
		return "", false
	}

	eventID = generateEventID(requesterUserID)
	if err := rs.dbService.CreatePendingReedEvent(ctx, eventID, requestID, requesterUserID, RequestReedEvent, reedID); err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to create speculative pending event for peer fallback")
		return "", false
	}

	for _, peerServerID := range peerServerIDs {
		result, peerEventID, err := rs.foreignFallbackHook(ctx, peerServerID, reedID, requesterUserID, requestID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("peerServerID", peerServerID).Msg("Peer fallback request failed")
			continue
		}
		if result != ForeignRequestOK {
			continue
		}
		if err := rs.dbService.CreateForeignPendingEvent(ctx, eventID, peerServerID, peerEventID); err != nil {
			log.Error().Err(err).Msg("Failed to record foreign pending event for peer fallback")
			continue
		}
		return eventID, true
	}

	if delErr := rs.dbService.DeletePendingEvent(ctx, eventID); delErr != nil {
		log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete speculative pending event after exhausting peer fallback candidates")
	}
	return "", false
}

// isForeignReed parses reedID's embedded serverID and reports whether it
// differs from this server's own — mirrors handlers.go's proxyIfForeign
// parse exactly, since realtime has no access to that main-package helper.
func (rs *RealtimeService) isForeignReed(reedID string) (foreign bool, homeServerID string) {
	_, embeddedServerID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(reedID))
	if !ok {
		_, embeddedServerID, ok = identity.ParseIdentityID(identity.IdentityID(reedID))
	}
	if !ok || embeddedServerID == rs.dbService.serverID {
		return false, ""
	}
	return true, embeddedServerID
}

// validateRequestID parses id's embedded userID@serverID prefix (the
// requesterID@serverID/suffix shape every client-minted request_id must
// have) and reports whether it matches requesterUserID exactly — the
// identity this WebSocket connection actually authenticated as. A client
// cannot mint a request_id claiming to be a different user or server;
// doing so (or sending a malformed id) is rejected outright rather than
// silently accepted, since accepting it would let a connection register
// pending state attributed to an identity it never proved.
func (rs *RealtimeService) validateRequestID(id, requesterUserID string) bool {
	userID, serverID, _, ok := identity.ParseKeyFingerprint(identity.IdentityID(id))
	if !ok {
		return false
	}
	return string(identity.CanonicalID(serverID, userID)) == requesterUserID
}

// registerReedRequest runs the ReedExists -> (optionally drop requester's
// stale allocation) -> GetOnlineHolders -> CreatePendingReedEvent ->
// dispatch-to-holder sequence shared by a local REQUEST_REED and a
// foreign one registered on this server's behalf via HandleForeignRequestReed.
// dropRequesterAllocation should be true only for a genuine local
// requester — a sentinel peer-relay "requester" never legitimately holds
// a stale allocation, so that DELETE is skipped for it.
// eventIdentity is who the minted event_id's canonical prefix names —
// normally the same as requesterUserID, except on a home server's
// sentinel-bookkeeping path, where requesterUserID is the sentinel (to
// satisfy pending_events' FK) but eventIdentity is the ORIGINAL remote
// requester, so the event_id still names who actually asked.
func (rs *RealtimeService) registerReedRequest(ctx context.Context, reedID, requesterUserID, eventIdentity, requestID string, dropRequesterAllocation bool, eventName EventName) (exists, hasHolders bool, eventID string, err error) {
	exists, err = rs.dbService.ReedExists(ctx, reedID)
	if err != nil {
		return false, false, "", err
	}
	if !exists {
		return false, false, "", nil
	}

	if dropRequesterAllocation {
		// Removes a stale holder row when the requester asks for a reed the
		// server thought they held — they clearly do not have the body locally.
		if _, err = rs.dbService.DeleteReedAllocation(ctx, reedID, requesterUserID); err != nil {
			return false, false, "", err
		}
	}

	var holder string
	hasHolders, holder, err = rs.dbService.GetOnlineHolders(ctx, reedID)
	if err != nil {
		return false, false, "", err
	}
	// A stale allocation (holder recorded but not currently online) is not
	// "held" from a dispatch standpoint — hasHolders must mean "someone is
	// actually here to relay-request right now," not just "an allocation
	// row exists somewhere." Otherwise this registers a pending event that
	// can never be dispatched to anyone and silently stalls forever.
	if !hasHolders || holder == "" {
		return true, false, "", nil
	}

	eventID = generateEventID(eventIdentity)
	if err = rs.dbService.CreatePendingReedEvent(ctx, eventID, requestID, requesterUserID, eventName, reedID); err != nil {
		return false, false, "", err
	}

	rs.dispatchNextIfConnected(holder)

	return true, true, eventID, nil
}

// handleForeignRequestReedFromClient is handleRequestReed's foreign
// branch: register the request with reedID's home server over peer HTTP
// (via the injected hook) instead of running registerReedRequest locally.
func (rs *RealtimeService) handleForeignRequestReedFromClient(client *Client, requestID, reedID, homeServerID string) {
	if rs.foreignRequestReedHook == nil {
		rs.connManager.SendToUser(client.userID, NewReedNotFoundMsg(requestID, reedID))
		return
	}

	// reed_identities row must exist before pending_reed_events.reed_id
	// can FK to it — this only claims reedID is a well-formed id worth
	// tracking (the same bar a local reed already clears just by being
	// signed, before anyone verifies/holds its content), not that this
	// server has verified anything about it.
	if err := rs.dbService.UpsertReedIdentity(context.Background(), reedID); err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to upsert reed identity for foreign relay request")
		return
	}

	eventID := generateEventID(client.userID)
	if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, client.userID, RequestReedEvent, reedID); err != nil {
		log.Error().Err(err).Msg("Failed to create local pending event for foreign relay request")
		return
	}

	result, peerEventID, err := rs.foreignRequestReedHook(context.Background(), reedID, client.userID, requestID)
	if err != nil || result != ForeignRequestOK {
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("homeServerID", homeServerID).Msg("Failed to register foreign reed request with home server")
		}
		if delErr := rs.dbService.DeletePendingEvent(context.Background(), eventID); delErr != nil {
			log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete speculative pending event after foreign request failure")
		}
		if result == ForeignRequestReedNotFound {
			rs.connManager.SendToUser(client.userID, NewReedNotFoundMsg(requestID, reedID))
		} else {
			rs.connManager.SendToUser(client.userID, NewReedNotHeldMsg(requestID, reedID))
		}
		return
	}

	if err := rs.dbService.CreateForeignPendingEvent(context.Background(), eventID, homeServerID, peerEventID); err != nil {
		log.Error().Err(err).Msg("Failed to record foreign pending event mapping")
		if delErr := rs.dbService.DeletePendingEvent(context.Background(), eventID); delErr != nil {
			log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete pending event after foreign_pending_events insert failure")
		}
		return
	}

	rs.connManager.SendToUser(client.userID, NewRequestAckMsg(requestID, eventID, reedID))
}

// registerAndRecordForeignRelay is the shared body behind both
// HandleForeignRequestReed (leg 1: a peer asks the reed's true home for
// its own content) and HandleForeignFallbackRequest (a peer we notified
// asks us, the true home, to relay back a copy after finding no local
// holder). Both cases run the same registerReedRequest sequence a local
// requester would, using a per-peer sentinel identity as the "requester"
// so the rest of the local relay-holder machinery (dispatchNext,
// handleRelayResponse, etc.) needs no special-casing to handle either.
func (rs *RealtimeService) registerAndRecordForeignRelay(ctx context.Context, reedID, requestingServerID, requestingUserID string) (result ForeignRequestResult, peerEventID string, err error) {
	sentinelUserID, err := rs.dbService.EnsurePeerSentinelUser(ctx, requestingServerID)
	if err != nil {
		return ForeignRequestReedNotFound, "", err
	}

	// requestID here is our own local pending_events.request_id bookkeeping
	// value, not the peer's own request id (that's recorded separately
	// below) — but it still inherits the ORIGINAL remote requester's
	// identity, not the sentinel's, so any downstream code that ever
	// surfaces it (logging, future features) reflects who actually asked,
	// not this server's internal bookkeeping stand-in.
	requestID := generateEventID(requestingUserID)
	exists, hasHolders, eventID, err := rs.registerReedRequest(ctx, reedID, sentinelUserID, requestingUserID, requestID, false, RequestReedEvent)
	if err != nil {
		return ForeignRequestReedNotFound, "", err
	}
	if !exists {
		return ForeignRequestReedNotFound, "", nil
	}
	if !hasHolders {
		return ForeignRequestReedNotHeld, "", nil
	}

	if err := rs.recordForeignRelayRequest(ctx, eventID, requestingServerID, requestingUserID); err != nil {
		return ForeignRequestReedNotFound, "", err
	}

	return ForeignRequestOK, eventID, nil
}

// HandleForeignRequestReed is leg 1's home-server-side logic: a peer is
// registering a REQUEST_REED on behalf of one of its own users, for
// content this server actually authors/owns.
func (rs *RealtimeService) HandleForeignRequestReed(ctx context.Context, canonicalReedID, requestingServerID, requestingUserID, peerRequestID string) (result ForeignRequestResult, peerEventID string, err error) {
	return rs.registerAndRecordForeignRelay(ctx, canonicalReedID, requestingServerID, requestingUserID)
}

// HandleForeignFallbackRequest is the fallback-fetch leg's callee-side
// logic: reedID's true home server (the caller) previously learned — via
// HandleHolderNotify — that we hold a cached copy, and is now asking us to
// relay it back because it found no online holder of its own. reedID is
// NOT ours; we're merely a known cache-holder. This works unmodified
// because reed_allocations.reed_id FKs to reed_identities (not reeds), so
// a local viewer's allocation for a foreign reed is already indistinguishable,
// from registerReedRequest's point of view, from a local holder for a
// locally-authored one.
func (rs *RealtimeService) HandleForeignFallbackRequest(ctx context.Context, reedID, requestingServerID, requestingUserID, peerRequestID string) (result ForeignRequestResult, peerEventID string, err error) {
	return rs.registerAndRecordForeignRelay(ctx, reedID, requestingServerID, requestingUserID)
}

// HandleHolderNotify records, on reedID's home server, that
// notifyingServerID holds a verified copy — the fact a later local
// REQUEST_REED with no online local holder falls back on
// (tryPeerFallback). Fire-and-forget from the caller's side; idempotent
// here (RecordServerHolder's own ON CONFLICT DO NOTHING).
func (rs *RealtimeService) HandleHolderNotify(ctx context.Context, reedID, notifyingServerID string) error {
	if err := rs.dbService.UpsertReedIdentity(ctx, reedID); err != nil {
		return err
	}
	return rs.dbService.RecordServerHolder(ctx, reedID, notifyingServerID)
}

// recordForeignRelayRequest inserts eventID's foreign_relay_requests row,
// rolling back the speculative pending_events row on failure — shared by
// HandleForeignRequestReed and HandleForeignSubscribeProfile.
func (rs *RealtimeService) recordForeignRelayRequest(ctx context.Context, eventID, requestingServerID, requestingUserID string) error {
	if err := rs.dbService.CreateForeignRelayRequest(ctx, eventID, requestingServerID, requestingUserID); err != nil {
		if delErr := rs.dbService.DeletePendingEvent(ctx, eventID); delErr != nil {
			log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete pending event after foreign_relay_requests insert failure")
		}
		return err
	}
	return nil
}

// HandleForeignSubscribeProfile is the profile-level sibling of
// HandleForeignRequestReed: a peer is registering interest in every one
// of authorID's reeds this requester doesn't already hold, on behalf of
// one of its own users. authorID must be local to this server (checked
// by the caller, same loop-prevention as leg 1). Returns one
// (peerEventID, reedID) pair per reed successfully registered — reeds
// this server has no online holder for are silently skipped (not an
// error; matches the local SUBSCRIBE_PROFILE path's own
// best-effort-per-reed behavior).
func (rs *RealtimeService) HandleForeignSubscribeProfile(ctx context.Context, authorID, requestingServerID, requestingUserID string) (results []ForeignSubscribeProfileResult, err error) {
	sentinelUserID, err := rs.dbService.EnsurePeerSentinelUser(ctx, requestingServerID)
	if err != nil {
		return nil, err
	}

	// Durable registration for LIVE fanout: without this, fanoutNewReedCore's
	// GetProfileSubscribers(authorID) call never sees this peer's viewer, so
	// a reed authorID publishes after this snapshot never reaches them. The
	// backfill below (GetUnallocatedReeds) only covers what already exists
	// right now. viewer_user_id is the real remote user, not the sentinel —
	// each foreign viewer needs its own row so a later publish fans out to
	// all of them individually, exactly like distinct local viewers would.
	if err := rs.dbService.UpsertRemoteIdentity(ctx, requestingUserID, requestingServerID); err != nil {
		return nil, err
	}
	if err := rs.dbService.CreateProfileSubscription(ctx, generateEventID(requestingUserID), requestingUserID, authorID); err != nil {
		return nil, err
	}

	reedIDs, err := rs.dbService.GetUnallocatedReedsForServer(ctx, authorID, requestingServerID)
	if err != nil {
		return nil, err
	}

	for _, reedID := range reedIDs {
		requestID := generateEventID(requestingUserID)
		exists, hasHolders, eventID, err := rs.registerReedRequest(ctx, reedID, sentinelUserID, requestingUserID, requestID, false, RequestReedEvent)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to register reed for foreign profile subscription")
			continue
		}
		if !exists || !hasHolders {
			continue
		}
		if err := rs.recordForeignRelayRequest(ctx, eventID, requestingServerID, requestingUserID); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to record foreign relay request for profile subscription")
			continue
		}
		results = append(results, ForeignSubscribeProfileResult{PeerEventID: eventID, ReedID: reedID})
	}

	return results, nil
}

// HandleForeignRelayResponse runs on the originating server (O): a home
// server (H) has delivered relayed content for a request O registered via
// leg 1. Resolves peerEventID back to O's own local pending_events row
// and delivers to the local requester exactly as a local RELAY_RESPONSE
// would, but does not touch dispatchNext/DeletePendingEvent — O has no
// holder queue for this event; allocation/deletion stays deferred until
// the requester's own DATA_ACK/DATA_INVALID.
func (rs *RealtimeService) HandleForeignRelayResponse(ctx context.Context, peerEventID, callerServerID string, data json.RawMessage) (found bool, err error) {
	fpe, err := rs.dbService.GetForeignPendingEventByPeerEventID(ctx, peerEventID, callerServerID)
	if err != nil {
		return false, err
	}
	if fpe == nil {
		return false, nil
	}

	pe, err := rs.dbService.GetPendingReedEvent(ctx, fpe.EventID)
	if err != nil {
		return false, err
	}
	if pe == nil {
		// Local requester's row already gone (e.g. raced with disconnect) -- not an error.
		return true, nil
	}

	var msg DataResponseMsg
	if pe.EventName == string(ReedReplyEvent) {
		msg = NewReedReplyMsg(pe.EventID, pe.RequestID, pe.ReedID, data)
	} else {
		msg = NewDataResponseMsg(pe.EventID, pe.RequestID, pe.ReedID, data)
	}
	if err := rs.connManager.SendToUser(pe.RequesterUserID, msg); err != nil {
		log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to deliver foreign-relayed data response")
	}
	return true, nil
}

// errForeignRelayOwnershipMismatch is returned by CancelForeignPendingEvent
// when callerServerID doesn't match the peer that originally registered
// peerEventID — the HTTP handler maps this to 403.
var errForeignRelayOwnershipMismatch = errors.New("foreign relay request belongs to a different peer")

// CancelForeignPendingEvent runs on the home server (H): the originating
// server's local requester disconnected before delivery completed, so H
// should drop its half of the pending state. Idempotent — an unknown
// peerEventID is a no-op, not an error.
func (rs *RealtimeService) CancelForeignPendingEvent(ctx context.Context, peerEventID, callerServerID string) error {
	frr, err := rs.dbService.GetForeignRelayRequest(ctx, peerEventID)
	if err != nil {
		return err
	}
	if frr == nil {
		return nil
	}
	if frr.RequestingServerID != callerServerID {
		return errForeignRelayOwnershipMismatch
	}
	return rs.dbService.DeletePendingEvent(ctx, peerEventID)
}

// HandleForeignAck runs on the home server (H): the originating server's
// local viewer verified and locally allocated the delivered content, so H
// records that callerServerID (as a whole, not any specific one of its
// users) now holds a copy — this is what lets a later local request that
// finds no online local holder fall back to asking this peer directly
// (tryPeerFallback), and what makes a future GetUnallocatedReedsForServer
// query for that peer stop re-offering content it already relayed
// successfully, closing the loop that previously made every profile
// subscribe resend everything regardless of what the peer already held.
// Idempotent (RecordServerHolder's own ON CONFLICT DO NOTHING) and, like
// CancelForeignPendingEvent, drops the now-fully-resolved pending_events
// row — there is no further use for it once the ack lands.
func (rs *RealtimeService) HandleForeignAck(ctx context.Context, peerEventID, callerServerID string) error {
	frr, err := rs.dbService.GetForeignRelayRequest(ctx, peerEventID)
	if err != nil {
		return err
	}
	if frr == nil {
		return nil
	}
	if frr.RequestingServerID != callerServerID {
		return errForeignRelayOwnershipMismatch
	}

	pe, err := rs.dbService.GetPendingReedEvent(ctx, peerEventID)
	if err != nil {
		return err
	}
	if pe == nil {
		return nil
	}

	if err := rs.dbService.RecordServerHolder(ctx, pe.ReedID, callerServerID); err != nil {
		return err
	}
	return rs.dbService.DeletePendingEvent(ctx, peerEventID)
}

// handlePublishReady runs new-reed fanout when a pending_fanout row exists.
func (rs *RealtimeService) handlePublishReady(client *Client, data json.RawMessage) {
	var ready PublishReadyData
	if err := json.Unmarshal(data, &ready); err != nil {
		return
	}
	reedID := ready.ReedID
	if reedID == "" {
		return
	}
	authorUserID := client.userID

	claimed, tags, err := rs.dbService.ClaimPendingFanout(context.Background(), reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Str("userID", authorUserID).Msg("Failed to claim pending fanout")
		return
	}

	if claimed {
		if shouldBroadcast(ready) {
			go rs.fanoutNewReed(reedID, tags)
		} else {
			go rs.fanoutNewReedNoBroadcast(reedID, tags)
		}
		go rs.notifyReplyAncestorsOfReply(reedID)
		go rs.notifyForeignParentOfReply(reedID)
	} else {
		exists, err := rs.dbService.ReedExists(context.Background(), reedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("userID", authorUserID).Msg("Failed to check reed for publish ready ack")
			return
		}
		if !exists {
			return
		}
	}

	ack := PublishReadyAckMsg{
		Type: "PUBLISH_READY_ACK",
		Data: PublishReadyAckData{ReedID: reedID},
	}
	if jsonBytes, err := json.Marshal(ack); err == nil {
		client.writeMessage(websocket.TextMessage, jsonBytes)
	}
}

func (rs *RealtimeService) handleRelayResponse(client *Client, data json.RawMessage) {
	var relay RelayResponseData
	if err := json.Unmarshal(data, &relay); err != nil {
		return
	}
	eventID := relay.EventID
	if eventID == "" {
		return
	}

	pe, err := rs.dbService.GetPendingReedEvent(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event")
		rs.dispatchNext(client.userID)
		return
	}
	if pe == nil {
		// Event was cancelled (e.g. UNSUBSCRIBE_PROFILE) — still advance the holder's queue.
		rs.dispatchNext(client.userID)
		return
	}

	if pe.EventName == string(BroadcastReedEvent) {
		username, err := rs.dbService.GetUsername(context.Background(), pe.UserID)
		if err != nil && err != sql.ErrNoRows {
			log.Error().Err(err).Str("userID", pe.UserID).Msg("Failed to load author username for broadcast reed")
		}
		if err != nil {
			log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Dropping broadcast reed: author account was either removed or never existed")
		} else {
			log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering broadcast reed to subscriber")
			if err := rs.connManager.SendToUser(pe.RequesterUserID, NewBroadcastReedMsg(pe.ReedID, relay.Data, username)); err != nil {
				log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to deliver broadcast reed")
			}
		}
		if err := rs.dbService.DeletePendingEvent(context.Background(), eventID); err != nil {
			log.Error().Err(err).Str("eventID", eventID).Msg("Failed to delete pending event")
		}
	} else if pe.EventName == string(PipeReedEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering pipe reed to subscriber")
		rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, relay.Data, func() DataResponseMsg {
			return NewPipeReedMsg(pe.EventID, pe.RequestID, pe.ReedID, relay.Data)
		})
		// Allocation and deletion deferred until viewer sends DATA_ACK or DATA_INVALID.
	} else if pe.EventName == string(FollowReedEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering follow reed to subscriber")
		rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, relay.Data, func() DataResponseMsg {
			return NewFollowReedMsg(pe.EventID, pe.RequestID, pe.ReedID, relay.Data)
		})
		// Allocation and deletion deferred until viewer sends DATA_ACK or DATA_INVALID.
	} else if pe.EventName == string(ReedReplyEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering reed reply to subscriber")
		rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, relay.Data, func() DataResponseMsg {
			return NewReedReplyMsg(pe.EventID, pe.RequestID, pe.ReedID, relay.Data)
		})
		// Allocation and deletion deferred until viewer sends DATA_ACK or DATA_INVALID.
	} else {
		rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, relay.Data, func() DataResponseMsg {
			return NewDataResponseMsg(pe.EventID, pe.RequestID, pe.ReedID, relay.Data)
		})
		// Allocation and deletion deferred until viewer sends DATA_ACK or DATA_INVALID.
	}

	rs.dispatchNext(client.userID)
}

// deliverOrForward delivers relayed data for eventID either straight to a
// local requester's WebSocket, or — when requesterUserID is a per-peer
// sentinel with no real connection — over peer HTTP to whichever peer
// registered eventID via GetForeignRelayRequest. Every pending-reed-event
// type that can be requested on behalf of a foreign peer (PIPE_REED,
// FOLLOW_REED, REED_REPLY, and the generic REQUEST_REED/profile-subscribe
// case) needs this same check; buildLocalMsg is only invoked in the local
// case so callers don't pay for constructing a message that's discarded.
func (rs *RealtimeService) deliverOrForward(ctx context.Context, eventID, requesterUserID string, data json.RawMessage, buildLocalMsg func() DataResponseMsg) {
	frr, ferr := rs.dbService.GetForeignRelayRequest(ctx, eventID)
	if ferr != nil {
		log.Error().Err(ferr).Str("eventID", eventID).Msg("Failed to check foreign relay request")
		return
	}
	if frr != nil {
		// requesterUserID is a per-peer sentinel identity with no real WS
		// connection — deliver over HTTP to the requesting peer instead.
		if rs.foreignDeliverHook != nil {
			if err := rs.foreignDeliverHook(ctx, frr.RequestingServerID, eventID, data); err != nil {
				log.Error().Err(err).Str("eventID", eventID).Msg("Failed to deliver relayed data to requesting peer")
			}
		}
		return
	}
	if err := rs.connManager.SendToUser(requesterUserID, buildLocalMsg()); err != nil {
		log.Error().Err(err).Str("requesterID", requesterUserID).Msg("Failed to deliver relayed data")
	}
}

// handleRelayMiss drops the reporting holder's allocation and retries another online holder.
func (rs *RealtimeService) handleRelayMiss(client *Client, data RelayMissData) {
	if data.EventID == "" {
		return
	}

	pe, err := rs.dbService.GetPendingReedEvent(context.Background(), data.EventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", data.EventID).Msg("Failed to get pending event for relay miss")
		rs.dispatchNext(client.userID)
		return
	}
	if pe == nil {
		rs.dispatchNext(client.userID)
		return
	}

	log.Info().
		Str("eventID", data.EventID).
		Str("holderID", client.userID).
		Str("authorID", pe.UserID).
		Str("reedID", pe.ReedID).
		Msg("Relay miss; dropping holder allocation and retrying")

	if _, err := rs.dbService.DeleteReedAllocation(context.Background(), pe.ReedID, client.userID); err != nil {
		log.Error().Err(err).Str("reedID", pe.ReedID).Str("holderID", client.userID).Msg("Failed to delete allocation on relay miss")
	}

	if err := rs.dbService.ResetDispatchedAt(context.Background(), data.EventID); err != nil {
		log.Error().Err(err).Str("eventID", data.EventID).Msg("Failed to reset dispatched_at on relay miss")
	}

	hasHolders, holder, err := rs.dbService.GetOnlineHolders(context.Background(), pe.ReedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", pe.ReedID).Msg("Failed to check reed holders on relay miss")
	} else if !hasHolders {
		rs.failReedNotHeld(pe)
	} else if holder != "" {
		rs.dispatchNextIfConnected(holder)
	}

	rs.dispatchNext(client.userID)
}

func (rs *RealtimeService) failReedNotHeld(pe *PendingReedEvent) {
	log.Info().
		Str("eventID", pe.EventID).
		Str("requesterID", pe.RequesterUserID).
		Str("authorID", pe.UserID).
		Str("reedID", pe.ReedID).
		Msg("No reed holders remain; notifying requester")
	if err := rs.connManager.SendToUser(pe.RequesterUserID, NewReedNotHeldMsg(pe.RequestID, pe.ReedID)); err != nil {
		log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to send reed not held")
	}
	if err := rs.dbService.DeletePendingEvent(context.Background(), pe.EventID); err != nil {
		log.Error().Err(err).Str("eventID", pe.EventID).Msg("Failed to delete pending event on reed not held")
	}
}

// handleDataAck is called when the viewer has received and verified a delivery successfully.
// New reeds: allocate. Reed removals: clear allocation. Account removals: clear peer state.
func (rs *RealtimeService) handleDataAck(client *Client, data DataAckData) {
	if data.EventID == "" {
		return
	}
	eventID := data.EventID

	pe, err := rs.dbService.GetPendingSubject(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event for data ack")
		return
	}
	if pe == nil {
		return
	}

	if EventName(pe.EventName) == ReedRemovedEvent {
		changed, err := rs.dbService.DeleteReedAllocation(context.Background(), pe.ReedID, client.userID)
		if err != nil {
			log.Error().Err(err).Str("reedID", pe.ReedID).Str("userID", client.userID).Msg("Failed to clear allocation on reed_removed ack")
		} else if changed {
			rs.notifyReedCoverage(pe.ReedID)
		}
	} else if EventName(pe.EventName) == AccountRemovedEvent {
		targets, err := rs.dbService.ClearPeerStateForRemovedAccount(context.Background(), client.userID, pe.UserID)
		if err != nil {
			log.Error().Err(err).Str("removedUserID", pe.UserID).Str("viewer", client.userID).Msg("Failed to clear peer state on account_removed ack")
		} else {
			for _, t := range targets {
				rs.notifyReedCoverage(t.ReedID)
			}
		}
	} else {
		// reed_identities normally already has a row for a foreign reed
		// by request time (handleForeignRequestReedFromClient/
		// handleForeignSubscribeProfileFromClient upsert it before
		// registering the request) — this is a defensive idempotent
		// upsert, not the primary mint site, in case some other path
		// ever reaches an ack without going through those first.
		// AllocateReed's FK to reed_identities(id) needs the row to
		// exist; skip the allocation (not the whole ack) if this fails.
		identityOK := true
		foreign, homeServerID := rs.isForeignReed(pe.ReedID)
		if foreign {
			if err := rs.dbService.UpsertReedIdentity(context.Background(), pe.ReedID); err != nil {
				log.Error().Err(err).Str("reedID", pe.ReedID).Msg("Failed to upsert reed identity on data ack")
				identityOK = false
			}
		}
		if identityOK {
			changed, err := rs.dbService.AllocateReed(context.Background(), pe.ReedID, client.userID)
			if err != nil {
				log.Error().Err(err).Str("reedID", pe.ReedID).Str("userID", client.userID).Msg("Failed to allocate reed on data ack")
			} else {
				if changed {
					rs.notifyReedCoverage(pe.ReedID)
				}
				// Tell the home server it now has a fallback target for
				// this reed — fires whenever the acked reed is foreign,
				// independent of whether this ack also closes out a
				// leg-1-originated request (the foreignAckHook call
				// below). AFTER our own allocation is persisted, never
				// before — the viewer already verified and holds this
				// content regardless of whether the notification
				// succeeds, so a failed/lost call here only makes the
				// home server's fallback-routing table stale, not this
				// server's correctness.
				if foreign && rs.foreignHolderNotifyHook != nil {
					if err := rs.foreignHolderNotifyHook(context.Background(), homeServerID, pe.ReedID); err != nil {
						log.Error().Err(err).Str("reedID", pe.ReedID).Str("homeServerID", homeServerID).Msg("Failed to notify home server of new holder")
					}
				}
				// Notify the home server AFTER our own allocation is
				// persisted, never before — the viewer already verified
				// and holds this content regardless of whether this
				// notification succeeds, so a failed/lost call here only
				// makes the home server's bookkeeping stale, not this
				// server's. See ForeignAckHook's doc comment.
				if rs.foreignAckHook != nil {
					if fpe, ferr := rs.dbService.GetForeignPendingEvent(context.Background(), eventID); ferr != nil {
						log.Error().Err(ferr).Str("eventID", eventID).Msg("Failed to look up foreign pending event for ack notify")
					} else if fpe != nil {
						if err := rs.foreignAckHook(context.Background(), fpe.HomeServerID, fpe.PeerEventID); err != nil {
							log.Error().Err(err).Str("eventID", eventID).Str("homeServerID", fpe.HomeServerID).Msg("Failed to notify home server of delivered ack")
						}
					}
				}
			}
		}
	}

	if err := rs.dbService.DeletePendingEvent(context.Background(), eventID); err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to delete pending event on data ack")
	}
}

// handleDataInvalid is called when the viewer received a reed but its signature failed verification.
// The pending event is removed without allocating the reed to the viewer.
func (rs *RealtimeService) handleDataInvalid(client *Client, data DataInvalidData) {
	if data.EventID == "" {
		return
	}

	if err := rs.dbService.DeletePendingEvent(context.Background(), data.EventID); err != nil {
		log.Error().Err(err).Str("eventID", data.EventID).Msg("Failed to delete pending event on data invalid")
	}
}

// handleKeyFetchError is called when a client received signed content over
// an already-authenticated connection but a subsequent key fetch needed to
// verify it failed. Not tied to a pending_events row — this is the client
// self-reporting an anomaly, not acking a specific delivery.
func (rs *RealtimeService) handleKeyFetchError(client *Client, data KeyFetchErrorData) {
	if data.UserID == "" || data.Fingerprint == "" {
		return
	}
	log.Warn().
		Str("reporterUserID", client.userID).
		Str("targetUserID", data.UserID).
		Str("fingerprint", data.Fingerprint).
		Msg("Client reported key fetch error")
	rs.metrics.KeyFetchError(context.Background(), client.userID, data.UserID, data.Fingerprint)
}

// handleRevokedKeyUsed is called when a client found signed content whose
// timestamp is at or after its signing key's revocation — a genuine
// revoked-key-abuse signal, surfaced for later security analysis.
func (rs *RealtimeService) handleRevokedKeyUsed(client *Client, data RevokedKeyUsedData) {
	if data.UserID == "" || data.Fingerprint == "" {
		return
	}
	log.Warn().
		Str("reporterUserID", client.userID).
		Str("targetUserID", data.UserID).
		Str("fingerprint", data.Fingerprint).
		Msg("Client reported content signed with a revoked key")
	rs.metrics.RevokedKeyUsed(context.Background(), client.userID, data.UserID, data.Fingerprint)
}

func (rs *RealtimeService) handleSubscribeProfile(client *Client, data json.RawMessage) {
	var profile SubscribeProfileData
	if err := json.Unmarshal(data, &profile); err != nil || profile.UserID == "" {
		return
	}

	subscriptionID := generateEventID(client.userID)
	if err := rs.dbService.CreateProfileSubscription(context.Background(), subscriptionID, client.userID, profile.UserID); err != nil {
		log.Error().Err(err).Msg("Failed to create profile subscription")
		return
	}

	if foreign, homeServerID := rs.isForeignReed(profile.UserID); foreign {
		rs.handleForeignSubscribeProfileFromClient(client, profile.UserID, homeServerID, subscriptionID)
		return
	}

	missingIDs, err := rs.dbService.GetUnallocatedReeds(context.Background(), profile.UserID, client.userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get unallocated reeds for viewer")
		return
	}

	for _, reedID := range missingIDs {
		eventID := generateEventID(client.userID)
		requestID := generateEventID(client.userID)
		if err := rs.dbService.CreateProfileSubscriptionEvent(context.Background(), eventID, requestID, client.userID, ProfileSubscriptionEvent, reedID, subscriptionID); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to create profile subscription event")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(context.Background(), reedID)
		if err != nil || holder == "" {
			continue
		}
		rs.dispatchNextIfConnected(holder)
	}
}

// ForeignSubscribeProfileResult is one reed of a foreign author's
// unallocated-reeds backfill, registered with the home server (leg 1's
// profile-level sibling).
type ForeignSubscribeProfileResult struct {
	PeerEventID string
	ReedID      string
}

// ForeignSubscribeProfileHook registers requesterUserID's interest in
// every one of authorID's (foreign) reeds it doesn't already hold, with
// authorID's home server over peer HTTP — the profile-level counterpart
// of ForeignRequestReedHook, since a profile backfill needs H to
// enumerate an unknown number of reeds rather than resolve one.
type ForeignSubscribeProfileHook func(ctx context.Context, authorID, requesterUserID string) ([]ForeignSubscribeProfileResult, error)

// SetForeignSubscribeProfileHook installs the profile-backfill registration hook.
func (rs *RealtimeService) SetForeignSubscribeProfileHook(hook ForeignSubscribeProfileHook) {
	rs.foreignSubscribeProfileHook = hook
}

// handleForeignSubscribeProfileFromClient is handleSubscribeProfile's
// foreign branch: ask authorID's home server for every reed of theirs
// this viewer doesn't hold, and register a local pending event (plus its
// foreign_pending_events mapping) for each one returned, exactly as
// handleForeignRequestReedFromClient does for a single reed.
func (rs *RealtimeService) handleForeignSubscribeProfileFromClient(client *Client, authorID, homeServerID, subscriptionID string) {
	if rs.foreignSubscribeProfileHook == nil {
		return
	}

	results, err := rs.foreignSubscribeProfileHook(context.Background(), authorID, client.userID)
	if err != nil {
		log.Error().Err(err).Str("authorID", authorID).Str("homeServerID", homeServerID).Msg("Failed to register foreign profile subscription")
		return
	}

	for _, result := range results {
		rs.registerForeignProfileSubscriptionEvent(context.Background(), client.userID, homeServerID, subscriptionID, result.ReedID, result.PeerEventID)
	}
}

// registerForeignProfileSubscriptionEvent records one (reedID, peerEventID)
// pair delivered by authorID's home server against requesterUserID's local
// profile subscription — the shared tail end of both the subscribe-time
// backfill (handleForeignSubscribeProfileFromClient, one call per existing
// reed returned by the peer) and the live-fanout push
// (HandleForeignNewReedNotify, one call for a single just-published reed).
// Without this row in foreign_pending_events, HandleForeignRelayResponse has
// nothing to resolve peerEventID against and silently drops the delivery.
func (rs *RealtimeService) registerForeignProfileSubscriptionEvent(ctx context.Context, requesterUserID, homeServerID, subscriptionID, reedID, peerEventID string) {
	if err := rs.dbService.UpsertReedIdentity(ctx, reedID); err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to upsert reed identity for foreign profile subscription")
		return
	}
	eventID := generateEventID(requesterUserID)
	requestID := generateEventID(requesterUserID)
	if err := rs.dbService.CreateProfileSubscriptionEvent(ctx, eventID, requestID, requesterUserID, ProfileSubscriptionEvent, reedID, subscriptionID); err != nil {
		log.Error().Err(err).Str("peerEventID", peerEventID).Msg("Failed to create local pending event for foreign profile subscription")
		return
	}
	if err := rs.dbService.CreateForeignPendingEvent(ctx, eventID, homeServerID, peerEventID); err != nil {
		log.Error().Err(err).Msg("Failed to record foreign pending event mapping for profile subscription")
		if delErr := rs.dbService.DeletePendingEvent(ctx, eventID); delErr != nil {
			log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete pending event after foreign_pending_events insert failure")
		}
	}
}

// HandleForeignNewReedNotify runs on the originating server (O): authorID's
// home server (H) is pushing a single newly-published reed for one of O's
// viewers who already has a durable profile subscription on H (created at
// SUBSCRIBE_PROFILE time, backfill or not). This is the live counterpart of
// the one-time backfill loop in handleForeignSubscribeProfileFromClient:
// without it, only reeds that existed at subscribe time ever get a
// foreign_pending_events row on O, so anything H's fanoutNewReedCore fans
// out afterward has no mapping for HandleForeignRelayResponse to resolve
// and silently 404s — the exact gap a full page reload used to paper over
// by re-running the whole backfill from scratch.
func (rs *RealtimeService) HandleForeignNewReedNotify(ctx context.Context, authorID, homeServerID, requesterUserID, reedID, peerEventID string) (found bool, err error) {
	subscriptionID, err := rs.dbService.GetProfileSubscription(ctx, requesterUserID, authorID)
	if err != nil {
		return false, err
	}
	if subscriptionID == "" {
		// Viewer unsubscribed since H queued this notify; not an error.
		return false, nil
	}
	rs.registerForeignProfileSubscriptionEvent(ctx, requesterUserID, homeServerID, subscriptionID, reedID, peerEventID)
	return true, nil
}

func (rs *RealtimeService) handleUnsubscribeProfile(client *Client, data json.RawMessage) {
	var profile UnsubscribeProfileData
	if err := json.Unmarshal(data, &profile); err != nil || profile.UserID == "" {
		return
	}

	subscriptionID, err := rs.dbService.GetProfileSubscription(context.Background(), client.userID, profile.UserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get profile subscription")
		return
	}
	if subscriptionID == "" {
		return
	}

	if err := rs.dbService.DeleteProfileSubscription(context.Background(), subscriptionID); err != nil {
		log.Error().Err(err).Str("subscriptionID", subscriptionID).Msg("Failed to delete profile subscription")
	}

	if foreign, homeServerID := rs.isForeignReed(profile.UserID); foreign && rs.foreignUnsubscribeProfileHook != nil {
		if err := rs.foreignUnsubscribeProfileHook(context.Background(), profile.UserID, client.userID); err != nil {
			log.Error().Err(err).Str("authorID", profile.UserID).Str("homeServerID", homeServerID).Msg("Failed to notify home server of profile unsubscribe")
		}
	}
}

// HandleForeignUnsubscribeProfile runs on the home server (H): a peer's
// viewer no longer wants live fanout for authorID. Deletes H's own
// profile_subscriptions row for that (viewer, author) pair, the durable
// registration HandleForeignSubscribeProfile created. Ownership is
// implicit: callerServerID must match requestingUserID's own embedded
// serverID (checked by the HTTP handler before calling this), so a peer
// can only ever unsubscribe its own users.
func (rs *RealtimeService) HandleForeignUnsubscribeProfile(ctx context.Context, authorID, requestingUserID string) error {
	subscriptionID, err := rs.dbService.GetProfileSubscription(ctx, requestingUserID, authorID)
	if err != nil {
		return err
	}
	if subscriptionID == "" {
		return nil
	}
	return rs.dbService.DeleteProfileSubscription(ctx, subscriptionID)
}

// HandleForeignSubscribeReed is leg 8's home-server logic: a peer's
// viewer wants live stats for one of this server's reeds. Registers a
// durable reed_subscriptions row keyed by the real remote viewer (so
// future stat-change fanout finds them, mirroring
// HandleForeignSubscribeProfile's own durable registration) and returns
// the current snapshot to seed the peer's initial REED_STATS. ok=false
// means the reed doesn't exist (mirrors the local ReedExists guard in
// handleSubscribeReed).
func (rs *RealtimeService) HandleForeignSubscribeReed(ctx context.Context, reedID, requestingServerID, requestingUserID string) (snapshot ForeignReedStatsSnapshot, ok bool, err error) {
	exists, err := rs.dbService.ReedExists(ctx, reedID)
	if err != nil {
		return ForeignReedStatsSnapshot{}, false, err
	}
	if !exists {
		return ForeignReedStatsSnapshot{}, false, nil
	}

	if err := rs.dbService.UpsertRemoteIdentity(ctx, requestingUserID, requestingServerID); err != nil {
		return ForeignReedStatsSnapshot{}, false, err
	}
	if err := rs.dbService.CreateReedSubscription(ctx, generateEventID(requestingUserID), requestingUserID, reedID); err != nil {
		return ForeignReedStatsSnapshot{}, false, err
	}

	echoes, coveragePercent, replies, likes, err := rs.dbService.GetReedStatsSnapshot(ctx, reedID)
	if err != nil {
		return ForeignReedStatsSnapshot{}, false, err
	}
	return ForeignReedStatsSnapshot{
		Echoes:          echoes,
		CoveragePercent: coveragePercent,
		Replies:         replies,
		Likes:           likes,
	}, true, nil
}

// HandleForeignUnsubscribeReed is leg 9's home-server logic: a peer's
// viewer no longer wants live stats for reedID. Deletes this server's own
// reed_subscriptions row for that (viewer, reed) pair.
func (rs *RealtimeService) HandleForeignUnsubscribeReed(ctx context.Context, reedID, requestingUserID string) error {
	subscriptionID, err := rs.dbService.GetReedSubscription(ctx, requestingUserID, reedID)
	if err != nil {
		return err
	}
	if subscriptionID == "" {
		return nil
	}
	return rs.dbService.DeleteReedSubscription(ctx, subscriptionID)
}

// HandleForeignReplyNotify is leg 11's home-server logic: a peer is
// telling us replyReedID (authored on their server) replies to
// parentReedID, one of our own reeds. Records the reference and runs the
// exact same local fanout a purely-local reply already gets — reply-count
// updates up the ancestor chain, plus content delivery to anyone
// subscribed to the thread (which, per notifyReedSubscribersOfReply's own
// foreign-aware branch, may itself reach further peers).
func (rs *RealtimeService) HandleForeignReplyNotify(ctx context.Context, parentReedID, replyReedID, threadID string, ts time.Time) error {
	exists, err := rs.dbService.ReedExists(ctx, parentReedID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("parent reed not found: %s", parentReedID)
	}

	if err := rs.dbService.InsertForeignReply(ctx, parentReedID, replyReedID, threadID, ts); err != nil {
		return err
	}

	reedID := parentReedID
	for {
		rs.notifyReedReplies(reedID)
		rs.notifyReedSubscribersOfReply(reedID, replyReedID)
		nextReedID, ok, err := rs.dbService.ReplyParent(ctx, reedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to resolve reply ancestor for foreign reply notify")
			return nil
		}
		if !ok {
			return nil
		}
		reedID = nextReedID
	}
}

// DeliverForeignReedStats runs on the originating server (O): the home
// server for a reed O's viewer subscribed to (leg 8) is pushing a live
// stats update (leg 10). requesterUserID must already be a genuine local
// user — enforced by the HTTP handler before this is called — so this
// simply relays the opaque, already-typed payload straight to their
// socket, exactly as it would have arrived from a local
// SendToReedSubscribers call.
func (rs *RealtimeService) DeliverForeignReedStats(ctx context.Context, requesterUserID string, payload json.RawMessage) error {
	return rs.connManager.SendToUser(requesterUserID, json.RawMessage(payload))
}

func (rs *RealtimeService) handleSubscribeReed(client *Client, msg InboundJSONMsg) {
	authorID, bareReedID := msg.UserID, msg.ReedID
	if authorID == "" || bareReedID == "" {
		return
	}
	reedID := string(identity.AppendEntity(identity.IdentityID(authorID), bareReedID))

	// Durable registration first (mirrors handleSubscribeProfile): survives
	// a reconnect and, for a foreign reed, is what lets the home server's
	// fanout find this viewer at all — connManager's map is in-memory only
	// and never leaves this process.
	subscriptionID := generateEventID(client.userID)
	if err := rs.dbService.CreateReedSubscription(context.Background(), subscriptionID, client.userID, reedID); err != nil {
		log.Error().Err(err).Str("userID", authorID).Str("reedID", reedID).Msg("Failed to create reed subscription")
		return
	}

	if foreign, homeServerID := rs.isForeignReed(reedID); foreign {
		rs.handleForeignSubscribeReedFromClient(client, authorID, reedID, homeServerID)
		return
	}

	exists, err := rs.dbService.ReedExists(context.Background(), reedID)
	if err != nil {
		log.Error().Err(err).Str("userID", authorID).Str("reedID", reedID).Msg("Failed to check reed for subscribe")
		return
	}
	if !exists {
		return
	}

	echoes, coveragePercent, replies, likes, err := rs.dbService.GetReedStatsSnapshot(context.Background(), reedID)
	if err != nil {
		log.Error().Err(err).Str("userID", authorID).Str("reedID", reedID).Msg("Failed to load reed stats for subscribe")
		return
	}

	rs.connManager.SubscribeReed(client, reedID)
	stats := ReedStatsMsg{
		Type:            "REED_STATS",
		UserID:          authorID,
		ReedID:          reedID,
		Echoes:          echoes,
		CoveragePercent: coveragePercent,
		Replies:         replies,
		Likes:           likes,
	}
	if err := rs.connManager.SendToClient(client, stats); err != nil {
		log.Error().Err(err).Str("userID", client.userID).Str("reedID", reedID).Msg("Failed to send REED_STATS")
	}
}

// ForeignReedStatsSnapshot is the initial stats snapshot returned by a
// foreign reed's home server when a peer registers a live stats
// subscription (leg 8).
type ForeignReedStatsSnapshot struct {
	Echoes          int
	CoveragePercent int
	Replies         int
	Likes           int
}

// ForeignSubscribeReedHook registers requesterUserID's interest in
// reedID's live stats with reedID's home server, returning the current
// snapshot to seed the initial REED_STATS the same way a local subscribe
// would. ok=false means the home server reports the reed doesn't exist
// (mirrors the local ReedExists guard).
type ForeignSubscribeReedHook func(ctx context.Context, reedID, requesterUserID string) (snapshot ForeignReedStatsSnapshot, ok bool, err error)

// SetForeignSubscribeReedHook installs the leg-8 (subscribe-reed) hook.
func (rs *RealtimeService) SetForeignSubscribeReedHook(hook ForeignSubscribeReedHook) {
	rs.foreignSubscribeReedHook = hook
}

// handleForeignSubscribeReedFromClient is handleSubscribeReed's foreign
// branch: register live-stats interest with reedID's home server and
// relay back the initial snapshot. The local reed_subscriptions row was
// already created by the caller — this only handles the peer round trip
// and the client's initial REED_STATS.
func (rs *RealtimeService) handleForeignSubscribeReedFromClient(client *Client, authorID, reedID, homeServerID string) {
	if rs.foreignSubscribeReedHook == nil {
		return
	}
	snapshot, ok, err := rs.foreignSubscribeReedHook(context.Background(), reedID, client.userID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Str("homeServerID", homeServerID).Msg("Failed to register foreign reed stats subscription")
		return
	}
	if !ok {
		return
	}

	stats := ReedStatsMsg{
		Type:            "REED_STATS",
		UserID:          authorID,
		ReedID:          reedID,
		Echoes:          snapshot.Echoes,
		CoveragePercent: snapshot.CoveragePercent,
		Replies:         snapshot.Replies,
		Likes:           snapshot.Likes,
	}
	if err := rs.connManager.SendToClient(client, stats); err != nil {
		log.Error().Err(err).Str("userID", client.userID).Str("reedID", reedID).Msg("Failed to send REED_STATS for foreign reed")
	}
}

func (rs *RealtimeService) handleUnsubscribeReed(client *Client, msg InboundJSONMsg) {
	authorID, bareReedID := msg.UserID, msg.ReedID
	if authorID == "" || bareReedID == "" {
		return
	}
	reedID := string(identity.AppendEntity(identity.IdentityID(authorID), bareReedID))
	rs.connManager.UnsubscribeReed(client, reedID)

	subscriptionID, err := rs.dbService.GetReedSubscription(context.Background(), client.userID, reedID)
	if err != nil {
		log.Error().Err(err).Str("userID", authorID).Str("reedID", reedID).Msg("Failed to get reed subscription")
		return
	}
	if subscriptionID == "" {
		return
	}
	if err := rs.dbService.DeleteReedSubscription(context.Background(), subscriptionID); err != nil {
		log.Error().Err(err).Str("subscriptionID", subscriptionID).Msg("Failed to delete reed subscription")
	}

	if foreign, homeServerID := rs.isForeignReed(reedID); foreign && rs.foreignUnsubscribeReedHook != nil {
		if err := rs.foreignUnsubscribeReedHook(context.Background(), reedID, client.userID); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("homeServerID", homeServerID).Msg("Failed to notify home server of reed stats unsubscribe")
		}
	}
}

func (rs *RealtimeService) handleSubscribePipe(client *Client, data json.RawMessage) {
	var pipe SubscribePipeData
	if err := json.Unmarshal(data, &pipe); err != nil {
		return
	}
	tag := NormalizePipeTag(pipe.Tag)
	if tag == "" {
		return
	}
	rs.connManager.SubscribePipe(client, tag)
	log.Debug().Str("userID", client.userID).Str("tag", tag).Msg("Client subscribed to pipe")
}

func (rs *RealtimeService) handleUnsubscribePipe(client *Client, data json.RawMessage) {
	var pipe SubscribePipeData
	if err := json.Unmarshal(data, &pipe); err != nil {
		return
	}
	tag := NormalizePipeTag(pipe.Tag)
	if tag == "" {
		return
	}
	rs.connManager.UnsubscribePipe(client, tag)
	log.Debug().Str("userID", client.userID).Str("tag", tag).Msg("Client unsubscribed from pipe")
}

// FilterSubscribedPipeTags returns extracted tags that currently have ≥1 listener.
// Used by SignReed to stash only relevant tags on pending_fanout.
func (rs *RealtimeService) FilterSubscribedPipeTags(tags []string) []string {
	return rs.connManager.FilterTagsWithListeners(tags)
}

// notifyForeignReedSubscribers pushes msg (already carrying its own Type
// field, matching whatever a local reed-stat subscriber would receive
// over WS) to every foreign viewer durably registered in
// reed_subscriptions for reedID. Local delivery is unaffected — this only
// covers the gap connManager's in-memory reedSubscribers map can never
// close, since it holds no cross-server state at all. Best-effort: a
// failed peer push only means one viewer misses one update, never
// retried — same tolerance every other live WS fanout already has for a
// client that's simply offline.
func (rs *RealtimeService) notifyForeignReedSubscribers(reedID string, msg any) {
	rs.notifyForeignReedSubscribersExcept(reedID, "", msg)
}

// notifyForeignReedSubscribersExcept is notifyForeignReedSubscribers with
// one viewer skipped — mirrors sendToReedSubscribersExceptAuthor's own
// exclude param, used by the ripple-push sites so a ripple's own author
// doesn't get their own content echoed back to another of their devices.
func (rs *RealtimeService) notifyForeignReedSubscribersExcept(reedID, excludeUserID string, msg any) {
	if rs.foreignReedStatsHook == nil {
		return
	}
	subs, err := rs.dbService.GetReedSubscribers(context.Background(), reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to load reed subscribers for foreign stats push")
		return
	}
	if len(subs) == 0 {
		return
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to marshal reed stats push payload")
		return
	}
	for _, sub := range subs {
		if excludeUserID != "" && sub.ViewerUserID == excludeUserID {
			continue
		}
		foreign, viewerServerID := rs.isForeignReed(sub.ViewerUserID)
		if !foreign {
			continue
		}
		if err := rs.foreignReedStatsHook(context.Background(), viewerServerID, sub.ViewerUserID, payload); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("viewerUserID", sub.ViewerUserID).Msg("Failed to push reed stats to foreign subscriber")
		}
	}
}

func (rs *RealtimeService) notifyReedCoverage(reedID string) {
	authorUserID := reedAuthorIdentity(reedID)
	exists, err := rs.dbService.ReedExists(context.Background(), reedID)
	if err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to check reed for coverage notify")
		return
	}
	if !exists {
		// Removed reed (or removed author) — no meaningful coverage to
		// report, and SUBSCRIBE_REED already refuses these, so nobody
		// legitimately subscribed is waiting on this broadcast.
		return
	}

	holders, percent, err := rs.dbService.GetReedCoverage(context.Background(), reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load reed coverage for notify")
		return
	}

	rs.metrics.ReedCoverage(context.Background(), authorUserID, reedID, holders, percent)

	msg := ReedCoverageMsg{
		Type:            "REED_COVERAGE",
		UserID:          authorUserID,
		ReedID:          reedID,
		CoveragePercent: percent,
	}
	if err := rs.connManager.BroadcastReedCoverage(msg); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_COVERAGE")
	}
	rs.notifyForeignReedSubscribers(reedID, msg)
}

func (rs *RealtimeService) notifyReedEchoes(reedID string) {
	authorUserID := reedAuthorIdentity(reedID)
	echoes, err := rs.dbService.CountEchoes(context.Background(), reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load reed echoes for notify")
		return
	}

	msg := ReedEchoesMsg{
		Type:   "REED_ECHOES",
		UserID: authorUserID,
		ReedID: reedID,
		Echoes: echoes,
	}
	if err := rs.connManager.SendToReedSubscribers(reedID, msg); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_ECHOES")
	}
	rs.notifyForeignReedSubscribers(reedID, msg)
}

// notifyReplyAncestorsOfReply walks replyReedID's ancestor chain and relays
// the reply to each ancestor's reed-stat subscribers. Only called from
// handlePublishReady, once the reply's author has actually claimed
// PUBLISH_READY — calling this any earlier (e.g. straight from SignReed)
// makes the author a relay target before their own client is ready to
// serve it, and the resulting relay miss deletes their allocation,
// orphaning the reed from relay entirely.
func (rs *RealtimeService) notifyReplyAncestorsOfReply(replyReedID string) {
	reedID := replyReedID
	for {
		parentReedID, ok, err := rs.dbService.ReplyParent(context.Background(), reedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to resolve reply parent")
			return
		}
		if !ok {
			return
		}
		rs.notifyReedSubscribersOfReply(parentReedID, replyReedID)
		reedID = parentReedID
	}
}

// notifyForeignParentOfReply tells replyReedID's immediate parent's home
// server about the reply, if that parent is foreign — the write side of
// cross-server replying (see ForeignReplyNotifyHook). Only the immediate
// parent needs notifying: that server's own reed_replies table already
// lets it walk further up its own ancestor chain the same way this
// server's notifyReplyAncestorsOfReply does. Runs alongside (not gating)
// notifyReplyAncestorsOfReply's own local-ancestor walk above — a purely
// local reply chain has nothing foreign to notify and this is a no-op.
func (rs *RealtimeService) notifyForeignParentOfReply(replyReedID string) {
	if rs.foreignReplyNotifyHook == nil {
		return
	}
	rec, err := rs.dbService.GetReplyRecord(context.Background(), replyReedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", replyReedID).Msg("Failed to load reply record for foreign parent notify")
		return
	}
	if rec == nil {
		return
	}
	foreign, _ := rs.isForeignReed(rec.ParentReedID)
	if !foreign {
		return
	}
	if err := rs.foreignReplyNotifyHook(context.Background(), rec.ParentReedID, replyReedID, rec.ThreadID, rec.Timestamp); err != nil {
		log.Error().Err(err).Str("reedID", replyReedID).Str("parentReedID", rec.ParentReedID).Msg("Failed to notify foreign parent's home server of reply")
	}
}

// notifyReplyAncestorsOfRemoval walks removedReedID's ancestor chain and
// delivers the removal cert to each ancestor's reed-stat subscribers —
// someone viewing a thread needs to learn a reply in it was deleted, not
// just subscribers of the removed reed itself. Removal certs are
// server-stored (unlike reply content), so unlike notifyReplyAncestorsOfReply
// there's no relay-holder race to worry about; this can run inline with the
// removed reed's own subscriber fanout.
func (rs *RealtimeService) notifyReplyAncestorsOfRemoval(removedReedID string, cert *ReedRemovalWire) {
	reedID := removedReedID
	for {
		parentReedID, ok, err := rs.dbService.ReplyParent(context.Background(), reedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to resolve reply parent for removal")
			return
		}
		if !ok {
			return
		}
		recipients := rs.connManager.ReedSubscriberUserIDs(parentReedID, "")
		rs.dispatchRemovalMany(recipients, removedReedID, cert)
		reedID = parentReedID
	}
}

// notifyReedSubscribersOfReply relays a newly posted reply's content to
// everyone subscribed to ancestorReedID — someone viewing that reed's
// thread, not necessarily following the reply's author. The reply's own
// author is the content holder, same relay-through-a-peer mechanism as
// FOLLOW_REED/PIPE_REED (the server never stores reed content).
func (rs *RealtimeService) notifyReedSubscribersOfReply(ancestorReedID, replyReedID string) {
	replyUserID := reedAuthorIdentity(replyReedID)
	recipients := rs.connManager.ReedSubscriberUserIDs(ancestorReedID, replyUserID)
	if len(recipients) > 0 {
		rs.dispatchMany(recipients, ReedReplyEvent, replyReedID)
	}
	rs.notifyForeignReedSubscribersOfReply(ancestorReedID, replyReedID, replyUserID)
}

// notifyForeignReedSubscribersOfReply is notifyReedSubscribersOfReply's
// foreign-viewer half: reed_subscriptions (durable, cross-server-visible)
// may hold viewers connManager's in-memory map never sees. A reply is a
// full new reed going through the real holder-relay system (not a
// counter), so unlike the lightweight stat push this reuses
// registerReedRequest + recordForeignRelayRequest — the same
// registered-pending-event pattern HandleForeignSubscribeProfile and
// fanoutNewReedCore's foreign branch already use — instead of
// notifyForeignReedSubscribers.
func (rs *RealtimeService) notifyForeignReedSubscribersOfReply(ancestorReedID, replyReedID, excludeUserID string) {
	if rs.foreignDeliverHook == nil {
		return
	}
	subs, err := rs.dbService.GetReedSubscribers(context.Background(), ancestorReedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", ancestorReedID).Msg("Failed to load reed subscribers for foreign reply notify")
		return
	}
	for _, sub := range subs {
		if sub.ViewerUserID == excludeUserID {
			continue
		}
		foreign, viewerServerID := rs.isForeignReed(sub.ViewerUserID)
		if !foreign {
			continue
		}
		sentinelUserID, err := rs.dbService.EnsurePeerSentinelUser(context.Background(), viewerServerID)
		if err != nil {
			log.Error().Err(err).Str("viewerUserID", sub.ViewerUserID).Msg("Failed to ensure peer sentinel for foreign reed reply notify")
			continue
		}
		requestID := generateEventID(sub.ViewerUserID)
		exists, hasHolders, eventID, err := rs.registerReedRequest(context.Background(), replyReedID, sentinelUserID, sub.ViewerUserID, requestID, false, ReedReplyEvent)
		if err != nil {
			log.Error().Err(err).Str("reedID", replyReedID).Str("viewerUserID", sub.ViewerUserID).Msg("Failed to register foreign reed reply notify")
			continue
		}
		if !exists || !hasHolders {
			continue
		}
		if err := rs.recordForeignRelayRequest(context.Background(), eventID, viewerServerID, sub.ViewerUserID); err != nil {
			log.Error().Err(err).Str("viewerUserID", sub.ViewerUserID).Msg("Failed to record foreign relay request for reed reply notify")
		}
	}
}

func (rs *RealtimeService) notifyReedReplies(reedID string) {
	authorUserID := reedAuthorIdentity(reedID)
	replies, err := rs.dbService.GetSubtreeReplyCount(context.Background(), reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load subtree replies for notify")
		return
	}

	msg := ReedRepliesMsg{
		Type:    "REED_REPLIES",
		UserID:  authorUserID,
		ReedID:  reedID,
		Replies: replies,
	}
	if err := rs.connManager.SendToReedSubscribers(reedID, msg); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_REPLIES")
	}
	rs.notifyForeignReedSubscribers(reedID, msg)
}

// notifyRipplePosted pushes a newly posted ripple response to everyone
// currently subscribed to its parent reed, except the ripple's own author
// — their client already has it from the synchronous HTTP response, so
// relaying it back would just echo to their other open tabs/devices. The
// full signed payload is carried (not just a "something changed, refetch"
// ping) so a subscribed client can run the same verify-or-discard path a
// list fetch uses without a second round-trip — see
// specs/ripples/00_design.md's Client-side verification section.
func (rs *RealtimeService) notifyRipplePosted(reedID, rippleAuthorID string, ripple RippleWire) {
	authorUserID := reedAuthorIdentity(reedID)
	msg := RipplePostedMsg{
		Type:   "RIPPLE_POSTED",
		UserID: authorUserID,
		ReedID: reedID,
		Ripple: ripple,
	}
	if err := rs.connManager.sendToReedSubscribersExceptAuthor(reedID, rippleAuthorID, msg); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast RIPPLE_POSTED")
	}
	rs.notifyForeignReedSubscribersExcept(reedID, rippleAuthorID, msg)
}

// notifyRippleUpdated pushes a soft-deleted ripple response to everyone
// currently subscribed to its parent reed, except the response's own
// author (same reasoning as notifyRipplePosted — they already got the
// 204 from their own DELETE). The original userSignature/serverSignature
// travel unchanged (soft-delete never touches them); a receiving client
// trusts the deleted flag and skips re-verification, per verifyRipple's
// tombstone short-circuit.
func (rs *RealtimeService) notifyRippleUpdated(reedID, rippleAuthorID string, ripple RippleWire) {
	authorUserID := reedAuthorIdentity(reedID)
	msg := RippleUpdatedMsg{
		Type:   "RIPPLE_UPDATED",
		UserID: authorUserID,
		ReedID: reedID,
		Ripple: ripple,
	}
	if err := rs.connManager.sendToReedSubscribersExceptAuthor(reedID, rippleAuthorID, msg); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast RIPPLE_UPDATED")
	}
	rs.notifyForeignReedSubscribersExcept(reedID, rippleAuthorID, msg)
}

func (rs *RealtimeService) notifyReedLikes(reedID string) {
	authorUserID := reedAuthorIdentity(reedID)
	likes, err := rs.dbService.CountLikes(context.Background(), reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load reed likes for notify")
		return
	}

	msg := ReedLikesMsg{
		Type:   "REED_LIKES",
		UserID: authorUserID,
		ReedID: reedID,
		Likes:  likes,
	}
	if err := rs.connManager.SendToReedSubscribers(reedID, msg); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_LIKES")
	}
	rs.notifyForeignReedSubscribers(reedID, msg)
}

func (rs *RealtimeService) handleSyncRequest(client *Client, data SyncRequestData) {
	if data.RequestID == "" {
		return
	}
	if !rs.validateRequestID(data.RequestID, client.userID) {
		rs.connManager.SendToUser(client.userID, NewInvalidRequestIDErrorMsg(data.RequestID))
		return
	}
	if err := rs.dbService.SetSyncRequestID(context.Background(), client.userID, data.RequestID); err != nil {
		log.Error().Err(err).Msg("Failed to store sync request ID")
		return
	}
	rs.catchUp(client.userID, data.RequestID)
	rs.dispatchNext(client.userID)
	rs.redispatchPendingRequests(client.userID)
}

func (rs *RealtimeService) redispatchPendingRequests(requesterUserID string) {
	requests, err := rs.dbService.GetPendingRequestsForRequester(context.Background(), requesterUserID)
	if err != nil {
		log.Error().Err(err).Str("requesterUserID", requesterUserID).Msg("Failed to get pending requests for requester")
		return
	}
	for _, req := range requests {
		if err := rs.dbService.ResetDispatchedAt(context.Background(), req.EventID); err != nil {
			log.Error().Err(err).Str("eventID", req.EventID).Msg("Failed to reset dispatched_at for pending request")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(context.Background(), req.ReedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", req.ReedID).Msg("Failed to get holder for pending request on reconnect")
			continue
		}
		if holder != "" {
			rs.dispatchNextIfConnected(holder)
		}
	}
}

func (rs *RealtimeService) handleUserCameOnline(client *Client) {
	// Nothing sent until client signals readiness via SYNC_REQUEST
}

func (rs *RealtimeService) catchUp(userID, requestID string) {
	unallocated, err := rs.dbService.GetMissingOut(context.Background(), userID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", userID).
			Msg("Failed to get unallocated reeds from followings")
		return
	}

	for _, reed := range unallocated {
		eventID := generateEventID(userID)
		if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, userID, FollowReedEvent, reed.ReedID); err != nil {
			log.Error().
				Err(err).
				Str("reedID", reed.ReedID).
				Msg("Failed to create catch-up pending event")
			continue
		}
		holder, err := rs.dbService.GetOnlineReedHolder(context.Background(), reed.ReedID)
		if err != nil {
			log.Error().
				Err(err).
				Str("reedID", reed.ReedID).
				Msg("Failed to get online holder for catch-up reed")
			continue
		}
		if holder != "" {
			rs.dispatchNextIfConnected(holder)
		}
	}

	removals, err := rs.dbService.GetMissingRemovals(context.Background(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get missing reed removals")
		return
	}
	for _, rem := range removals {
		eventID := generateEventID(userID)
		if err := rs.dbService.CreatePendingReedEvent(context.Background(), eventID, requestID, userID, ReedRemovedEvent, rem.ReedID); err != nil {
			log.Error().Err(err).Str("reedID", rem.ReedID).Msg("Failed to create catch-up reed_removed event")
			continue
		}
		rs.deliverReedRemoved(eventID, requestID, userID, rem.ReedID, &rem.Cert)
	}

	accountRemovals, err := rs.dbService.GetMissingAccountRemovals(context.Background(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get missing account removals")
		return
	}
	for _, rem := range accountRemovals {
		eventID := generateEventID(userID)
		if err := rs.dbService.CreatePendingAccountEvent(context.Background(), eventID, requestID, userID, rem.UserID); err != nil {
			log.Error().Err(err).Str("removedUserID", rem.UserID).Msg("Failed to create catch-up account_removed event")
			continue
		}
		rs.deliverAccountRemoved(eventID, requestID, userID, rem.UserID, &rem.Cert)
	}
}

// shouldBroadcast reports whether PUBLISH_READY should fan out to the broadcast stream.
// Absent or true means include broadcast; only explicit false opts out.
func shouldBroadcast(data PublishReadyData) bool {
	if len(data.Broadcast) == 0 || string(data.Broadcast) == "null" {
		return true
	}
	var include bool
	if err := json.Unmarshal(data.Broadcast, &include); err != nil {
		return true
	}
	return include
}
