//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// reedRemovalWire is the wire shape of a signed reed-removal certificate.
// Still JSON: used for HTTP federation payloads and DB storage, not just
// the client-facing WS wire (which now carries it via pb.ReedRemovalCert).
type reedRemovalWire struct {
	Type            string          `json:"type"`
	ServerID        string          `json:"serverID"`
	UserID          string          `json:"userID"`
	ReedID          string          `json:"reedID"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

// accountRemovalWire is the wire shape of a signed account-removal cert.
// Still JSON: used for HTTP federation and DB storage, not just the
// client-facing WS wire (now pb.AccountRemovalCert).
type accountRemovalWire struct {
	Type            string          `json:"type"`
	ServerID        string          `json:"serverID"`
	UserID          string          `json:"userID"`
	Note            string          `json:"note"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
}

// pbUserSignature/pbServerSignature convert the JSON signature blocks
// (shared with HTTP federation and DB storage) to their protobuf
// equivalents for the client-facing WS wire.
func pbUserSignature(s UserSignature) *pb.UserSignature {
	return &pb.UserSignature{Id: s.ID, Armor: s.Armor}
}

func pbServerSignature(s ServerSignature) *pb.ServerSignature {
	return &pb.ServerSignature{Id: s.ID, Armor: s.Armor, SignedAt: s.SignedAt.UTC().Unix()}
}

func pbReedRemovalCert(w reedRemovalWire) *pb.ReedRemovalCert {
	return &pb.ReedRemovalCert{
		ServerId:        w.ServerID,
		UserId:          w.UserID,
		ReedId:          w.ReedID,
		UserSignature:   pbUserSignature(w.UserSignature),
		ServerSignature: pbServerSignature(w.ServerSignature),
	}
}

func pbAccountRemovalCert(w accountRemovalWire) *pb.AccountRemovalCert {
	return &pb.AccountRemovalCert{
		ServerId:        w.ServerID,
		UserId:          w.UserID,
		Note:            w.Note,
		UserSignature:   pbUserSignature(w.UserSignature),
		ServerSignature: pbServerSignature(w.ServerSignature),
	}
}

func pbRipple(r RippleWire) *pb.Ripple {
	replyingTo := ""
	if r.ReplyingTo != nil {
		replyingTo = *r.ReplyingTo
	}
	return &pb.Ripple{
		Hash:            r.Hash,
		ThreadId:        r.ThreadID,
		UserId:          r.UserID,
		Content:         r.Content,
		ReplyingTo:      replyingTo,
		Deleted:         r.Deleted,
		PostedAt:        r.PostedAt.UTC().Unix(),
		UserSignature:   pbUserSignature(r.UserSignature),
		ServerSignature: pbServerSignature(r.ServerSignature),
	}
}

// marshalWSMessage stamps TypeName from Type and marshals to bytes — the one
// place every outbound WSMessage send derives its human-readable mirror.
func marshalWSMessage(msg *pb.WSMessage) ([]byte, error) {
	msg.TypeName = msg.Type.String()
	return proto.Marshal(msg)
}

// realtimeJSONString JSON-encodes a Go string (adds quoting/escaping) — used
// to wrap ciphertext for deliverOrForward's federation transport param,
// which stays JSON since federation is not part of this WS binary cutover.
func realtimeJSONString(s string) json.RawMessage {
	raw, _ := json.Marshal(s)
	return raw
}

// newReedRemovalWire builds the WS/HTTP wire cert from a stored removal cert.
func newReedRemovalWire(serverID string, cert reedRemovalCert) reedRemovalWire {
	return reedRemovalWire{
		Type:     identityTypeReed,
		ServerID: serverID,
		UserID:   cert.UserID,
		ReedID:   cert.ReedID,
		UserSignature: UserSignature{
			ID:    cert.UserKeyID,
			Armor: cert.UserSignature,
		},
		ServerSignature: ServerSignature{
			ID:       cert.ServerFingerprint,
			Armor:    cert.ServerSignature,
			SignedAt: cert.ServerSignedAt.UTC(),
		},
	}
}

// newAccountRemovalWire builds the WS/HTTP wire cert from a stored removal cert.
func newAccountRemovalWire(serverID string, cert accountRemovalCert) accountRemovalWire {
	return accountRemovalWire{
		Type:     identityTypeAccount,
		ServerID: serverID,
		UserID:   cert.UserID,
		Note:     cert.Note,
		UserSignature: UserSignature{
			ID:    cert.UserKeyID,
			Armor: cert.UserSignature,
		},
		ServerSignature: ServerSignature{
			ID:       cert.ServerFingerprint,
			Armor:    cert.ServerSignature,
			SignedAt: cert.ServerSignedAt.UTC(),
		},
	}
}

// userUpdateBroadcast is profile metadata pushed on user updates (reserved).
type userUpdateBroadcast struct {
	Username string `json:"username"`
	Bio      string `json:"bio"`
}

// realtimeEventName identifies the reason a pending relay event was created.
type realtimeEventName string

const (
	requestReedEvent         realtimeEventName = "request_reed"
	profileSubscriptionEvent realtimeEventName = "profile_subscription"
	followReedEvent          realtimeEventName = "follow_reed"
	broadcastReedEvent       realtimeEventName = "broadcast_reed"
	pipeReedEvent            realtimeEventName = "pipe_reed"
	reedRemovedEvent         realtimeEventName = "reed_removed"
	accountRemovedEvent      realtimeEventName = "account_removed"
	reedReplyEvent           realtimeEventName = "reed_reply"
	archiveReedEvent         realtimeEventName = "archive_reed"
	mentionEvent             realtimeEventName = "mention"
)

// newRelayRequestMsg is sent from the server to a holder to request reed
// content. ReedID is the canonical id (userID@serverID/uuid) — already
// globally unique and already embeds the author, so no separate author
// field is needed to disambiguate it. RequesterID is who the holder must
// encrypt the content to before responding — the server relays ciphertext
// blindly and never sees the body.
func newRelayRequestMsg(eventID, reedID, requesterID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_RELAY_REQUEST,
		Id:   eventID,
		Payload: &pb.WSMessage_RelayRequest{
			RelayRequest: &pb.RelayRequestMessage{ReedId: reedID, RequesterId: requesterID},
		},
	}
}

// newRequestAckMsg confirms to a requester that their relay request was registered.
func newRequestAckMsg(requestID, eventID, reedID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_REQUEST_ACK,
		Id:   eventID,
		Payload: &pb.WSMessage_RequestAck{
			RequestAck: &pb.RequestAckMessage{RequestId: requestID, ReedId: reedID},
		},
	}
}

// newPageAckMsg answers a PROFILE_PAGE. count and hasMore describe the
// author's own page, before subtracting what the viewer holds.
func newPageAckMsg(userID string, page, count uint32, hasMore bool) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_PAGE_ACK,
		Payload: &pb.WSMessage_PageAck{
			PageAck: &pb.PageAckMessage{UserId: userID, Page: page, Count: count, HasMore: hasMore},
		},
	}
}

func newDataResponseMsg(eventID, requestID, ciphertext, reedID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_DATA_RESPONSE,
		Id:   eventID,
		Payload: &pb.WSMessage_DataResponse{
			DataResponse: &pb.DataResponseMessage{RequestId: requestID, Ciphertext: ciphertext, ReedId: reedID},
		},
	}
}

// newBroadcastReedMsg builds a BROADCAST_REED delivery message (no request_id or event id needed).
func newBroadcastReedMsg(ciphertext, username, reedID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_BROADCAST_REED,
		Payload: &pb.WSMessage_DataResponse{
			DataResponse: &pb.DataResponseMessage{Ciphertext: ciphertext, Username: username, ReedId: reedID},
		},
	}
}

// newPipeReedMsg builds a PIPE_REED delivery (pipe subscription push).
// Carries the event id so the viewer can DATA_ACK after verify+store (same as DATA_RESPONSE).
func newPipeReedMsg(eventID, requestID, ciphertext, reedID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_PIPE_REED,
		Id:   eventID,
		Payload: &pb.WSMessage_DataResponse{
			DataResponse: &pb.DataResponseMessage{RequestId: requestID, Ciphertext: ciphertext, ReedId: reedID},
		},
	}
}

// newFollowReedMsg builds a FOLLOW_REED delivery (followcast / follow catch-up push).
func newFollowReedMsg(eventID, requestID, ciphertext, reedID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_FOLLOW_REED,
		Id:   eventID,
		Payload: &pb.WSMessage_DataResponse{
			DataResponse: &pb.DataResponseMessage{RequestId: requestID, Ciphertext: ciphertext, ReedId: reedID},
		},
	}
}

// newArchiveReedMsg builds an ARCHIVE_REED delivery to an admin/root
// resilience holder. No feed/UI semantics — the client stores and holds
// the reed without touching any social-graph state.
func newArchiveReedMsg(eventID, requestID, ciphertext, reedID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_ARCHIVE_REED,
		Id:   eventID,
		Payload: &pb.WSMessage_DataResponse{
			DataResponse: &pb.DataResponseMessage{RequestId: requestID, Ciphertext: ciphertext, ReedId: reedID},
		},
	}
}

// newReedReplyMsg builds a REED_REPLY delivery: pushed to subscribers of a
// reed (and its ancestors) when a new reply lands, distinct from FOLLOW_REED
// so it doesn't also feed the follow-feed cache — the recipient isn't
// necessarily following the reply's author, they're just viewing the thread.
// Used both for same-server subscriber fanout and for a foreign viewer whose
// home server relayed it to us on their behalf, so there is no separate
// cross-server wire type.
func newReedReplyMsg(eventID, requestID, ciphertext, reedID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_REED_REPLY,
		Id:   eventID,
		Payload: &pb.WSMessage_DataResponse{
			DataResponse: &pb.DataResponseMessage{RequestId: requestID, Ciphertext: ciphertext, ReedId: reedID},
		},
	}
}

// newMentionMsg builds a MENTION delivery: the reed's ciphertext, over
// the same holder-relay path as REED_REPLY (a mention always needs the
// reed itself, so a pointer-only push would just add a round trip).
func newMentionMsg(eventID, requestID, ciphertext, reedID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_MENTION,
		Id:   eventID,
		Payload: &pb.WSMessage_DataResponse{
			DataResponse: &pb.DataResponseMessage{RequestId: requestID, Ciphertext: ciphertext, ReedId: reedID},
		},
	}
}

// newReedRemovedMsg builds a REED_REMOVED delivery with the full signed cert.
func newReedRemovedMsg(eventID, requestID string, cert reedRemovalWire) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_REED_REMOVED,
		Id:   eventID,
		Payload: &pb.WSMessage_ReedRemoved{
			ReedRemoved: &pb.ReedRemovedMessage{RequestId: requestID, Cert: pbReedRemovalCert(cert)},
		},
	}
}

// newAccountRemovedMsg builds an ACCOUNT_REMOVED delivery with the full signed cert.
func newAccountRemovedMsg(eventID, requestID string, cert accountRemovalWire) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_ACCOUNT_REMOVED,
		Id:   eventID,
		Payload: &pb.WSMessage_AccountRemoved{
			AccountRemoved: &pb.AccountRemovedMessage{RequestId: requestID, Cert: pbAccountRemovalCert(cert)},
		},
	}
}

// RELAY_MISS, RELAY_ERROR, DATA_ACK, and DATA_INVALID carry no payload
// beyond the event id, which lives on WSMessage.Id — no dedicated payload
// message needed for any of them.

// newMailboxMsg sends the canonical ref (userID@serverID/id), not the bare
// row id — the client has no business reconstructing this itself, and
// user_mailbox.id alone is only unique per-user, not globally.
func newMailboxMsg(userID, id, ciphertext string) *pb.WSMessage {
	canonical := string(appendEntity(identityID(userID), id))
	return &pb.WSMessage{
		Type: pb.MessageType_MAILBOX,
		Payload: &pb.WSMessage_Mailbox{
			Mailbox: &pb.MailboxMessage{Id: canonical, Ciphertext: ciphertext},
		},
	}
}

// newReedNotFoundMsg is sent from the server to a requester when the requested reed does not exist.
func newReedNotFoundMsg(requestID, reedID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_REED_NOT_FOUND,
		Payload: &pb.WSMessage_ReedNotFound{
			ReedNotFound: &pb.ReedNotFoundMessage{RequestId: requestID, ReedId: reedID},
		},
	}
}

// newReedNotHeldMsg is sent when reed metadata exists but no peer holds the body.
func newReedNotHeldMsg(requestID, reedID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_REED_NOT_HELD,
		Payload: &pb.WSMessage_ReedNotHeld{
			ReedNotHeld: &pb.ReedNotHeldMessage{RequestId: requestID, ReedId: reedID},
		},
	}
}

// newInvalidRequestIDErrorMsg is sent when an inbound message's request_id
// doesn't embed the identity of the connection that sent it (malformed,
// or claiming a different user/server than this WebSocket authenticated
// as) — the client should discard the offending local record rather than
// retry it, since the server never created any pending state for it.
func newInvalidRequestIDErrorMsg(requestID string) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_INVALID_REQUEST_ID_ERROR,
		Payload: &pb.WSMessage_InvalidRequestIdError{
			InvalidRequestIdError: &pb.InvalidRequestIdErrorMessage{RequestId: requestID},
		},
	}
}

// newShutdownMsg is sent before the server closes a client's socket for a
// graceful shutdown, so the client can reconnect immediately instead of
// waiting on a connection that silently went dead.
func newShutdownMsg() *pb.WSMessage {
	return &pb.WSMessage{
		Type:    pb.MessageType_SIGTERM,
		Payload: &pb.WSMessage_Shutdown{Shutdown: &pb.ShutdownMessage{}},
	}
}

// newReedStatsMsg is pushed when a client subscribes to reed stats.
func newReedStatsMsg(reedID string, echoes, coveragePercent, replies, likes int) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_REED_STATS,
		Payload: &pb.WSMessage_ReedStats{
			ReedStats: &pb.ReedStatsMessage{
				ReedId:          reedID,
				Echoes:          int32(echoes),
				CoveragePercent: int32(coveragePercent),
				Replies:         int32(replies),
				Likes:           int32(likes),
			},
		},
	}
}

// newReedCoverageMsg notifies reed subscribers of holder coverage changes.
func newReedCoverageMsg(reedID string, coveragePercent int) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_REED_COVERAGE,
		Payload: &pb.WSMessage_ReedCoverage{
			ReedCoverage: &pb.ReedCoverageMessage{ReedId: reedID, CoveragePercent: int32(coveragePercent)},
		},
	}
}

// newReedEchoesMsg notifies reed subscribers of echo count changes.
func newReedEchoesMsg(reedID string, echoes int) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_REED_ECHOES,
		Payload: &pb.WSMessage_ReedEchoes{
			ReedEchoes: &pb.ReedEchoesMessage{ReedId: reedID, Echoes: int32(echoes)},
		},
	}
}

// newReedRepliesMsg notifies reed subscribers of reply subtree count changes.
func newReedRepliesMsg(reedID string, replies int) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_REED_REPLIES,
		Payload: &pb.WSMessage_ReedReplies{
			ReedReplies: &pb.ReedRepliesMessage{ReedId: reedID, Replies: int32(replies)},
		},
	}
}

// newReedLikesMsg notifies reed subscribers of like count changes.
func newReedLikesMsg(reedID string, likes int) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_REED_LIKES,
		Payload: &pb.WSMessage_ReedLikes{
			ReedLikes: &pb.ReedLikesMessage{ReedId: reedID, Likes: int32(likes)},
		},
	}
}

// newRipplePostedMsg notifies reed subscribers a new ripple response landed.
func newRipplePostedMsg(authorUserID, reedID string, ripple RippleWire) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_RIPPLE_POSTED,
		Payload: &pb.WSMessage_RipplePosted{
			RipplePosted: &pb.RipplePostedMessage{UserId: authorUserID, ReedId: reedID, Ripple: pbRipple(ripple)},
		},
	}
}

// newRippleUpdatedMsg notifies subscribers a ripple was soft-deleted
// (content patched to "[DELETED]"); there is no separate RIPPLE_DELETED.
func newRippleUpdatedMsg(authorUserID, reedID string, ripple RippleWire) *pb.WSMessage {
	return &pb.WSMessage{
		Type: pb.MessageType_RIPPLE_UPDATED,
		Payload: &pb.WSMessage_RippleUpdated{
			RippleUpdated: &pb.RippleUpdatedMessage{UserId: authorUserID, ReedId: reedID, Ripple: pbRipple(ripple)},
		},
	}
}

// =========== //
//   realtime  //
// =========== //

// realtimeBroadcastType represents the type of broadcast message.
type realtimeBroadcastType int

const (
	realtimeUserUpdate  realtimeBroadcastType = iota
	realtimeReedDeleted                       // legacy unused; prefer realtimeReedRemoved
	realtimeReedRemoved
	realtimeAccountRemoved
	realtimeEchoCountChanged  // UserID/ReedID = echoed target; refresh REED_ECHOES for subscribers
	realtimeReplyCountChanged // UserID/ReedID = ancestor reed; refresh REED_REPLIES subtree count for subscribers
	realtimeLikeCountChanged  // UserID/ReedID = liked reed; refresh REED_LIKES for subscribers
	realtimeReplyPosted       // UserID/ReedID = ancestor reed to notify; ReplyUserID/ReplyReedID = the new reply (content holder)
	realtimeRipplePosted      // UserID/ReedID = parent reed; Ripple = the new ripple response (full signed payload)
	realtimeRippleUpdated     // UserID/ReedID = parent reed; Ripple = the soft-deleted ripple response (deleted=true, content="[DELETED]")
)

// realtimeBroadcastMessage represents a message sent from the main app to
// the realtime service. Only the payload field matching Type is set.
type realtimeBroadcastMessage struct {
	Type     realtimeBroadcastType
	ServerID string
	UserID   string
	ReedID   string

	// ReplyPosted only: identifies the new reply itself, distinct from
	// UserID/ReedID above (the ancestor being notified).
	ReplyUserID string
	ReplyReedID string

	ReedRemoval    *reedRemovalWire
	AccountRemoval *accountRemovalWire
	UserUpdate     *userUpdateBroadcast

	// RipplePosted/RippleUpdated only: the full signed ripple response.
	Ripple *RippleWire
}

// realtimeClientSubscriptionFlags tracks per-client subscription toggles.
type realtimeClientSubscriptionFlags struct {
	user      bool
	broadcast bool
}

// realtimeSubscriptionType represents the type of subscription.
type realtimeSubscriptionType int

const (
	realtimeSubscribeUser realtimeSubscriptionType = iota
	realtimeSubscribeBroadcast
	realtimeUnsubscribeUser
	realtimeUnsubscribeBroadcast
)

// realtimeClient represents a connected WebSocket client.
type realtimeClient struct {
	conn             *websocket.Conn
	userID           string
	subscriptions    realtimeClientSubscriptionFlags
	lastPing         time.Time
	writeMu          sync.Mutex
	wsRecordOutbound func(messageType int, data []byte)
}

// realtimeConnectionManager manages WebSocket connections and subscriptions.
// Subscription state lives in Postgres (reed_subscriptions,
// pipe_subscriptions), not here — only the sockets themselves are
// process-local, since a *websocket.Conn can't be shared across replicas.
type realtimeConnectionManager struct {
	userConnections map[string]map[*websocket.Conn]*realtimeClient
	mutex           sync.RWMutex

	// Set when running with a cross-replica bus; nil means single-replica,
	// where a user with no local socket is simply offline.
	bus *realtimeBus
}

// String returns the string representation of realtimeBroadcastType.
func (bt realtimeBroadcastType) String() string {
	switch bt {
	case realtimeUserUpdate:
		return "UserUpdate"
	case realtimeReedDeleted:
		return "ReedDeleted"
	case realtimeReedRemoved:
		return "ReedRemoved"
	case realtimeAccountRemoved:
		return "AccountRemoved"
	case realtimeEchoCountChanged:
		return "EchoCountChanged"
	case realtimeReplyCountChanged:
		return "ReplyCountChanged"
	case realtimeLikeCountChanged:
		return "LikeCountChanged"
	case realtimeReplyPosted:
		return "ReplyPosted"
	case realtimeRipplePosted:
		return "RipplePosted"
	case realtimeRippleUpdated:
		return "RippleUpdated"
	default:
		return "Unknown"
	}
}

// newRealtimeClient creates a new client.
func newRealtimeClient(conn *websocket.Conn, userID string) *realtimeClient {
	return &realtimeClient{
		conn:     conn,
		userID:   userID,
		lastPing: time.Now(),
	}
}

// IsSubscribed checks if a client is subscribed to a specific type.
func (c *realtimeClient) IsSubscribed(subType realtimeSubscriptionType) bool {
	switch subType {
	case realtimeSubscribeUser:
		return c.subscriptions.user
	case realtimeSubscribeBroadcast:
		return c.subscriptions.broadcast
	default:
		return false
	}
}

// Subscribe adds a subscription for the client.
func (c *realtimeClient) Subscribe(subType realtimeSubscriptionType) {
	switch subType {
	case realtimeSubscribeUser:
		c.subscriptions.user = true
	case realtimeSubscribeBroadcast:
		c.subscriptions.broadcast = true
	}
}

// Unsubscribe removes a subscription for the client.
func (c *realtimeClient) Unsubscribe(subType realtimeSubscriptionType) {
	switch subType {
	case realtimeSubscribeUser:
		c.subscriptions.user = false
	case realtimeSubscribeBroadcast:
		c.subscriptions.broadcast = false
	}
}

// newRealtimeConnectionManager creates a new connection manager.
func newRealtimeConnectionManager() *realtimeConnectionManager {
	return &realtimeConnectionManager{
		userConnections: make(map[string]map[*websocket.Conn]*realtimeClient),
	}
}

// Start starts the connection manager's ping loop.
func (cm *realtimeConnectionManager) Start() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		cm.pingClients()
	}
}

// RegisterClient registers a new client (synchronous so delivery can run immediately).
func (cm *realtimeConnectionManager) RegisterClient(client *realtimeClient) {
	cm.registerClient(client)
}

// UnregisterClient removes a client. Returns true when the user still has at
// least one other active WebSocket (partial disconnect — skip offline cleanup).
func (cm *realtimeConnectionManager) UnregisterClient(client *realtimeClient) bool {
	return cm.unregisterClient(client)
}

// registerClient handles client registration. A user keeps a single active
// session: any prior sockets are closed so RELAY_REQUEST / fanout cannot land
// on a zombie connection the SPA no longer reads.
func (cm *realtimeConnectionManager) registerClient(client *realtimeClient) {
	log.Info().Msg("Registering client " + client.userID + " with connection " + client.conn.RemoteAddr().String())
	cm.mutex.Lock()

	var stale []*realtimeClient
	if existing := cm.userConnections[client.userID]; existing != nil {
		for conn, old := range existing {
			if conn == client.conn {
				continue
			}
			delete(existing, conn)
			stale = append(stale, old)
		}
	}
	if cm.userConnections[client.userID] == nil {
		cm.userConnections[client.userID] = make(map[*websocket.Conn]*realtimeClient)
	}
	cm.userConnections[client.userID][client.conn] = client

	log.Info().
		Str("userID", client.userID).
		Int("totalConnections", len(cm.userConnections[client.userID])).
		Int("replaced", len(stale)).
		Msg("Client registered")
	cm.mutex.Unlock()

	for _, old := range stale {
		old.conn.Close()
	}
}

// unregisterClient removes the client. Returns true if the user still has
// another registered connection.
func (cm *realtimeConnectionManager) unregisterClient(client *realtimeClient) bool {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	if userConns, exists := cm.userConnections[client.userID]; exists {
		delete(userConns, client.conn)
		if len(userConns) == 0 {
			delete(cm.userConnections, client.userID)
		}
	}

	client.conn.Close()

	remaining := cm.userConnections[client.userID]
	stillOnline := len(remaining) > 0

	log.Info().
		Str("userID", client.userID).
		Bool("stillOnline", stillOnline).
		Msg("Client unregistered")
	return stillOnline
}

// DisconnectUser closes every active WebSocket for userID (e.g. after device rebind).
func (cm *realtimeConnectionManager) DisconnectUser(userID string) {
	cm.mutex.Lock()
	userConns, exists := cm.userConnections[userID]
	if !exists || len(userConns) == 0 {
		cm.mutex.Unlock()
		return
	}
	clients := make([]*realtimeClient, 0, len(userConns))
	for conn, client := range userConns {
		delete(userConns, conn)
		clients = append(clients, client)
	}
	delete(cm.userConnections, userID)
	cm.mutex.Unlock()

	for _, client := range clients {
		client.conn.Close()
	}

	log.Info().
		Str("userID", userID).
		Int("disconnected", len(clients)).
		Msg("Disconnected user WebSocket clients")
}

// SendToUser marshals msg as a protobuf WSMessage and sends it as a binary
// frame to every active connection for the user. Delivering to all avoids
// losing messages when a superseded socket is still briefly registered;
// clients that already closed a socket simply drop the write.
func (cm *realtimeConnectionManager) SendToUser(userID string, msg *pb.WSMessage) error {
	cm.mutex.RLock()
	userConns, exists := cm.userConnections[userID]
	if !exists || len(userConns) == 0 {
		cm.mutex.RUnlock()
		// Not ours — hand off to whichever replica holds the socket. A nil
		// return here means "delivered or handed off", not "delivered".
		if cm.bus != nil {
			return cm.bus.marshalAndPublish(userID, msg)
		}
		return fmt.Errorf("no active connection for user %s", userID)
	}
	clients := make([]*realtimeClient, 0, len(userConns))
	for _, c := range userConns {
		clients = append(clients, c)
	}
	cm.mutex.RUnlock()

	data, err := marshalWSMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}

	var lastErr error
	sent := 0
	for _, c := range clients {
		if err := c.writeMessage(websocket.BinaryMessage, data); err != nil {
			lastErr = err
			continue
		}
		sent++
	}
	if sent == 0 {
		if lastErr != nil {
			return fmt.Errorf("failed to send message: %w", lastErr)
		}
		return fmt.Errorf("no active connection for user %s", userID)
	}
	return nil
}

// isUserOnline reports presence from online_users, not this replica's
// socket map, so a recipient on another replica still counts as online.
// Errs toward true: a failed lookup shouldn't silently drop a dispatch.
func (rs *realtimeService) isUserOnline(userID string) bool {
	online, err := rs.db.IsUserOnline(context.Background(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to check user presence")
		return true
	}
	return online
}

// SetBus installs the cross-replica delivery bus.
func (cm *realtimeConnectionManager) SetBus(bus *realtimeBus) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cm.bus = bus
}

// deliverLocal writes an already-marshaled frame to this replica's own
// sockets for userID, reporting whether any write landed. This is the bus
// receive side, so it never falls back to the bus itself.
func (cm *realtimeConnectionManager) deliverLocal(userID string, frame []byte) bool {
	cm.mutex.RLock()
	clients := make([]*realtimeClient, 0, len(cm.userConnections[userID]))
	for _, c := range cm.userConnections[userID] {
		clients = append(clients, c)
	}
	cm.mutex.RUnlock()

	sent := false
	for _, c := range clients {
		if err := c.writeMessage(websocket.BinaryMessage, frame); err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Failed to deliver bus frame")
			continue
		}
		sent = true
	}
	return sent
}

// HasConnection reports whether any active WebSocket is registered for the user.
func (cm *realtimeConnectionManager) HasConnection(userID string) bool {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	userConns, exists := cm.userConnections[userID]
	return exists && len(userConns) > 0
}

// pingClients sends ping messages to all clients.
func (cm *realtimeConnectionManager) pingClients() {
	cm.mutex.RLock()
	clients := make([]*realtimeClient, 0)
	for _, userConns := range cm.userConnections {
		for _, c := range userConns {
			clients = append(clients, c)
		}
	}
	cm.mutex.RUnlock()

	for _, c := range clients {
		cm.sendPing(c)
	}
}

// BroadcastShutdown tells every connected client the server is going away
// (SIGTERM/SIGINT) so it can drop the socket and start reconnecting right
// away, instead of waiting on a dead connection with no more pings. Best
// effort — the process is exiting either way, so a failed write here is
// just logged, not retried. This is the server-initiated counterpart to
// the client noticing an ordinary network drop.
func (cm *realtimeConnectionManager) BroadcastShutdown() {
	cm.mutex.RLock()
	clients := make([]*realtimeClient, 0)
	for _, userConns := range cm.userConnections {
		for _, c := range userConns {
			clients = append(clients, c)
		}
	}
	cm.mutex.RUnlock()

	for _, c := range clients {
		if err := cm.SendToClient(c, newShutdownMsg()); err != nil {
			log.Error().Err(err).Str("userID", c.userID).Msg("Failed to send SIGTERM notice")
		}
	}
	for _, c := range clients {
		c.conn.Close()
	}
}

// sendPing sends a ping message to a connection.
func (cm *realtimeConnectionManager) sendPing(client *realtimeClient) {
	ping := &pb.WSMessage{
		Type: pb.MessageType_PING,
		Payload: &pb.WSMessage_Ping{
			Ping: &pb.PingMessage{
				Data: "ping",
			},
		},
	}

	cm.sendProtobufMessage(client, ping)
}

// sendProtobufMessage sends a protobuf message to a connection.
func (cm *realtimeConnectionManager) sendProtobufMessage(client *realtimeClient, msg *pb.WSMessage) {
	data, err := marshalWSMessage(msg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal protobuf message")
		return
	}

	if err := client.writeMessage(websocket.BinaryMessage, data); err != nil {
		log.Error().Err(err).Msg("Failed to write protobuf message")
	}
}

func (c *realtimeClient) writeMessage(messageType int, data []byte) error {
	if c.wsRecordOutbound != nil {
		c.wsRecordOutbound(messageType, data)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(messageType, data)
}

// GetConnectionCount returns the total number of active connections.
func (cm *realtimeConnectionManager) GetConnectionCount() int {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	total := 0
	for _, userConns := range cm.userConnections {
		total += len(userConns)
	}
	return total
}

// SendToClient writes a protobuf payload to one client.
func (cm *realtimeConnectionManager) SendToClient(client *realtimeClient, msg *pb.WSMessage) error {
	data, err := marshalWSMessage(msg)
	if err != nil {
		return err
	}
	return client.writeMessage(websocket.BinaryMessage, data)
}

// normalizePipeTag lowercases and strips a leading # (SPA / SignReed parity).
func normalizePipeTag(tag string) string {
	tag = strings.TrimSpace(tag)
	tag = strings.TrimPrefix(tag, "#")
	return strings.ToLower(strings.TrimSpace(tag))
}

// reedSubscriberUserIDs returns the distinct users subscribed to reedID,
// excluding excludeUserID ("" excludes nobody). Backed by reed_subscriptions,
// so the recipient set is identical on every replica.
func (rs *realtimeService) reedSubscriberUserIDs(reedID, excludeUserID string) []string {
	subscribers, err := rs.db.GetReedSubscriberUserIDs(context.Background(), reedID, excludeUserID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to load reed subscribers")
		return nil
	}
	return subscribers
}

// sendToReedSubscribers sends msg to every user subscribed to reedID,
// skipping excludeUserID ("" skips nobody). Per-user via SendToUser, so a
// subscriber on another replica is reached over the bus.
func (rs *realtimeService) sendToReedSubscribers(reedID, excludeUserID string, msg *pb.WSMessage) error {
	for _, userID := range rs.reedSubscriberUserIDs(reedID, excludeUserID) {
		if err := rs.connManager.SendToUser(userID, msg); err != nil {
			log.Debug().Err(err).Str("userID", userID).Str("reedID", reedID).Msg("Failed to send reed subscription message")
		}
	}
	return nil
}

// BroadcastReedCoverage sends a coverage update to all subscribers of a reed.
func (rs *realtimeService) BroadcastReedCoverage(reedID string, msg *pb.WSMessage) error {
	if reedID == "" {
		return fmt.Errorf("reed coverage payload missing reedID")
	}
	return rs.sendToReedSubscribers(reedID, "", msg)
}

// authenticateWebSocket authenticates a WebSocket connection. userID is
// recovered from the verified publicKeyId, never a client-supplied param.
func authenticateWebSocket(r *http.Request, db *DataService, cryptoSvc *cryptoService) (string, error) {
	// Extract required authentication parameters from query string
	// (WebSocket doesn't support custom headers in all browsers).
	publicKeyID := r.URL.Query().Get("publicKeyId")
	signature := r.URL.Query().Get("signature")
	timestamp := r.URL.Query().Get("timestamp")

	if publicKeyID == "" || signature == "" || timestamp == "" {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Bool("hasSignature", signature != "").
			Str("timestamp", timestamp).
			Msg("Missing authentication parameters")
		return "", fmt.Errorf("missing authentication parameters")
	}

	// Validate timestamp for replay protection
	if err := cryptoSvc.validateTimestamp(timestamp); err != nil {
		log.Error().
			Str("timestamp", timestamp).
			Err(err).
			Msg("Invalid timestamp")
		return "", fmt.Errorf("invalid timestamp: %w", err)
	}

	// Get public key for the fingerprint, along with its revocation state.
	publicKey, revoked, err := getRealtimeAuthPublicKey(r.Context(), db, publicKeyID)
	if err != nil {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Err(err).
			Msg("Error retrieving public key")
		return "", fmt.Errorf("error retrieving public key: %w", err)
	}

	if publicKey == "" {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Msg("Public key not found")
		return "", fmt.Errorf("public key not found")
	}

	// Reject websocket auth signed by a revoked key. Same threat model as
	// the HTTP signatureAuthMiddleware: an attacker holding a compromised
	// old key must not be able to open a live subscription (and thereby
	// receive fanout traffic, or later, submit signed messages) after the
	// legitimate owner has rotated and revoked. A subscription opened
	// before revocation stays open; producing a *new* auth handshake with
	// a revoked key is what we forbid.
	if revoked {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Msg("WebSocket auth rejected: key is revoked")
		return "", fmt.Errorf("key is revoked")
	}

	// WebSocket sessions are always per-end-user (no peer-server use case
	// today, unlike the HTTP proxy path) — a 2-part server-key id has no
	// userID to recover and is rejected here.
	userID, _, _, ok := parseKeyFingerprint(identityID(publicKeyID))
	if !ok {
		log.Error().Str("publicKeyId", publicKeyID).Msg("WebSocket auth rejected: not a user key")
		return "", fmt.Errorf("not a user key")
	}
	selfIdentity := canonicalID(db.GetServerID(), userID)
	removed, err := hasAccountRemoval(r.Context(), db.db, userID, db.GetServerID())
	if err != nil {
		return "", fmt.Errorf("error checking account removal: %w", err)
	}
	if removed {
		log.Error().Str("userID", string(selfIdentity)).Msg("WebSocket auth rejected: account removed")
		return "", fmt.Errorf("account removed")
	}

	// Decode base64 signature first
	decodedSignature, err := base64Decode(signature)
	if err != nil {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Err(err).
			Msg("Failed to decode base64 signature")
		return "", fmt.Errorf("failed to decode base64 signature: %w", err)
	}

	// Verify signature against timestamp directly
	if err := cryptoSvc.verifySignature(timestamp, decodedSignature, publicKey); err != nil {
		log.Error().
			Str("publicKeyId", publicKeyID).
			Str("timestamp", timestamp).
			Err(err).
			Msg("Signature verification failed")
		return "", fmt.Errorf("signature verification failed: %w", err)
	}

	log.Info().
		Str("userID", string(selfIdentity)).
		Str("publicKeyId", publicKeyID).
		Msg("WebSocket authentication successful")

	return string(selfIdentity), nil
}

// getRealtimeAuthPublicKey retrieves a key's armor and revocation state by canonical id.
func getRealtimeAuthPublicKey(ctx context.Context, db *DataService, fingerprint string) (string, bool, error) {
	var armor string
	var revoked bool
	err := db.db.QueryRowContext(ctx, `
		SELECT pk.armor,
		       EXISTS(
			SELECT 1 FROM public_key_revocations rv
			WHERE rv.key_id = pk.id
		)
		FROM public_keys pk
		WHERE pk.id = $1
	`, fingerprint).Scan(&armor, &revoked)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}

	return armor, revoked, nil
}

type realtimeErrorMessage struct {
	Error string `json:"error"`
}

func rejectRealtimeConnection(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(realtimeErrorMessage{Error: reason})
}

// realtimeService represents the main realtime service.
type realtimeService struct {
	connManager   *realtimeConnectionManager
	db            *DataService
	crypto        *cryptoService
	allowedOrigin string
	metrics       metrics.Recorder
	ongoingCheck  func(userID string) (bool, error)
	deviceCheck   func(userID, deviceID string) error

	// Cross-server REQUEST_REED relay hooks: realtimeService has no signing
	// key, HTTP client, or federation-table access of its own, so the
	// actual peer HTTP calls are injected from the rest of root (mirrors
	// SetDeviceCheck/SetOngoingCheck's existing injection direction).
	foreignRequestReedHook          realtimeForeignRequestReedHook
	foreignDeliverHook              realtimeForeignDeliverHook
	foreignNotHeldHook              realtimeForeignNotHeldHook
	foreignCancelHook               realtimeForeignCancelHook
	foreignSubscribeProfileHook     realtimeForeignSubscribeProfileHook
	foreignProfilePageHook          realtimeForeignProfilePageHook
	foreignAckHook                  realtimeForeignAckHook
	foreignUnsubscribeProfileHook   realtimeForeignUnsubscribeProfileHook
	foreignSubscribeReedHook        realtimeForeignSubscribeReedHook
	foreignUnsubscribeReedHook      realtimeForeignUnsubscribeReedHook
	foreignReedStatsHook            realtimeForeignReedStatsHook
	foreignReplyNotifyHook          realtimeForeignReplyNotifyHook
	foreignReplyRemovalToViewerHook realtimeForeignReplyRemovalToViewerHook
	foreignHolderNotifyHook         realtimeForeignHolderNotifyHook
	foreignFallbackHook             realtimeForeignFallbackRequestHook
	foreignNewReedNotifyHook        realtimeForeignNewReedNotifyHook
}

// newRealtimeService creates a new realtime service.
func newRealtimeService(db *DataService, cryptoSvc *cryptoService, allowedOrigin string) *realtimeService {
	connManager := newRealtimeConnectionManager()
	log.Info().Msg("[OK] Realtime services initialized successfully")

	return &realtimeService{
		connManager:   connManager,
		db:            db,
		crypto:        cryptoSvc,
		allowedOrigin: allowedOrigin,
		metrics:       metrics.Noop{},
	}
}

// SetMetrics installs the business-metrics recorder.
func (rs *realtimeService) SetMetrics(rec metrics.Recorder) {
	if rec == nil {
		rs.metrics = metrics.Noop{}
		return
	}
	rs.metrics = rec
}

// SetOngoingCheck installs an optional import-gate check used after WebSocket
// auth succeeds. When the check returns true, the connection is rejected with 403.
func (rs *realtimeService) SetOngoingCheck(check func(userID string) (bool, error)) {
	rs.ongoingCheck = check
}

// SetDeviceCheck installs the active-device check used after WebSocket auth succeeds.
func (rs *realtimeService) SetDeviceCheck(check func(userID, deviceID string) error) {
	rs.deviceCheck = check
}

// realtimeForeignRequestResult reports the outcome of registering a
// REQUEST_REED with a reed's home server.
type realtimeForeignRequestResult int

const (
	realtimeForeignRequestOK realtimeForeignRequestResult = iota
	realtimeForeignRequestReedNotFound
	realtimeForeignRequestReedNotHeld
	// realtimeForeignRequestAccepted means the home server has a known
	// holder for the reed but none is online right now — it created a
	// pending event that will resolve on its own once a holder reconnects
	// (see registerReedRequest), but can't dispatch or deliver
	// immediately. Distinct from realtimeForeignRequestOK so a caller
	// trying multiple peers (tryPeerFallback) can keep looking for an
	// immediate 200 before settling for this as a fallback.
	realtimeForeignRequestAccepted
)

// realtimeForeignRequestReedHook registers requesterUserID's interest in
// reedID's home server over peer HTTP (leg 1), returning the home server's
// own event id on success.
type realtimeForeignRequestReedHook func(ctx context.Context, reedID, requesterUserID, localRequestID string) (result realtimeForeignRequestResult, peerEventID string, err error)

// SetForeignRequestReedHook installs the leg-1 (register-request) hook.
func (rs *realtimeService) SetForeignRequestReedHook(hook realtimeForeignRequestReedHook) {
	rs.foreignRequestReedHook = hook
}

// realtimeForeignDeliverHook delivers relayed data for peerEventID back to
// requestingServerID over peer HTTP (leg 2), called on the home server once
// a local holder relays content for a peer-registered request.
type realtimeForeignDeliverHook func(ctx context.Context, requestingServerID, peerEventID string, data json.RawMessage) error

// SetForeignDeliverHook installs the leg-2 (deliver-response) hook.
func (rs *realtimeService) SetForeignDeliverHook(hook realtimeForeignDeliverHook) {
	rs.foreignDeliverHook = hook
}

// realtimeForeignNotHeldHook notifies requestingServerID over peer HTTP that
// peerEventID's home server exhausted every holder and is giving up — the
// failure counterpart of realtimeForeignDeliverHook, called on the home
// server once it has no one left to relay a peer-registered request to.
type realtimeForeignNotHeldHook func(ctx context.Context, requestingServerID, peerEventID string) error

// SetForeignNotHeldHook installs the give-up-notify hook.
func (rs *realtimeService) SetForeignNotHeldHook(hook realtimeForeignNotHeldHook) {
	rs.foreignNotHeldHook = hook
}

// realtimeForeignCancelHook notifies homeServerID over peer HTTP (leg 4)
// that peerEventID's originating requester disconnected and its pending
// event should be dropped.
type realtimeForeignCancelHook func(ctx context.Context, homeServerID, peerEventID string) error

// SetForeignCancelHook installs the leg-4 (cancel-request) hook.
func (rs *realtimeService) SetForeignCancelHook(hook realtimeForeignCancelHook) {
	rs.foreignCancelHook = hook
}

// realtimeForeignUnsubscribeProfileHook notifies homeServerID over peer
// HTTP that requestingUserID no longer wants live fanout for authorID —
// the teardown counterpart of realtimeForeignSubscribeProfileHook's durable
// registration (leg 1b). Without this, B's profile_subscriptions row for a
// departed A viewer would never be cleaned up and B would keep trying to
// fan out to them forever.
type realtimeForeignUnsubscribeProfileHook func(ctx context.Context, authorID, requestingUserID string) error

// SetForeignUnsubscribeProfileHook installs the leg-7 (unsubscribe-profile) hook.
func (rs *realtimeService) SetForeignUnsubscribeProfileHook(hook realtimeForeignUnsubscribeProfileHook) {
	rs.foreignUnsubscribeProfileHook = hook
}

// realtimeForeignUnsubscribeReedHook notifies reedID's home server over
// peer HTTP that requestingUserID no longer wants live stats for it — the
// teardown counterpart of realtimeForeignSubscribeReedHook's registration
// (leg 8).
type realtimeForeignUnsubscribeReedHook func(ctx context.Context, reedID, requestingUserID string) error

// SetForeignUnsubscribeReedHook installs the leg-9 (unsubscribe-reed) hook.
func (rs *realtimeService) SetForeignUnsubscribeReedHook(hook realtimeForeignUnsubscribeReedHook) {
	rs.foreignUnsubscribeReedHook = hook
}

// realtimeForeignReplyNotifyHook tells parentReedID's home server over peer
// HTTP that replyReedID (authored on this server) replies to it — the
// write side of cross-server replying. Without this, a reply to a foreign
// reed is created successfully but never surfaces on the parent's home
// server at all: no reed_replies row there means its reply-count/thread
// queries never find it.
type realtimeForeignReplyNotifyHook func(ctx context.Context, parentReedID, replyReedID, threadID string, ts time.Time) error

// SetForeignReplyNotifyHook installs the leg-11 (reply-notify) hook.
func (rs *realtimeService) SetForeignReplyNotifyHook(hook realtimeForeignReplyNotifyHook) {
	rs.foreignReplyNotifyHook = hook
}

// realtimeForeignReplyRemovalToViewerHook tells viewerUserID's home server
// that removedReedID (a reply) is gone, so a foreign viewer with the
// parent reed's thread open live gets the same removal notice a local
// viewer already gets from dispatchRemovalTo — this is the counterpart to
// notifyForeignReedSubscribersOfReply, for removal instead of posting.
type realtimeForeignReplyRemovalToViewerHook func(ctx context.Context, viewerUserID, removedReedID string, cert *reedRemovalWire) error

// SetForeignReplyRemovalToViewerHook installs the leg-20 hook.
func (rs *realtimeService) SetForeignReplyRemovalToViewerHook(hook realtimeForeignReplyRemovalToViewerHook) {
	rs.foreignReplyRemovalToViewerHook = hook
}

// realtimeForeignReedStatsHook pushes a pre-built WS message (REED_COVERAGE,
// REED_ECHOES, REED_REPLIES, REED_LIKES, RIPPLE_POSTED, or RIPPLE_UPDATED —
// already JSON-marshaled) to requestingServerID over peer HTTP (leg 10),
// for one reed-stats subscriber on that peer. Opaque payload, not a typed
// snapshot: the receiving server relays it to its own local client
// unmodified, exactly like every other relayed-content path in this file —
// the client independently verifies whatever needs verifying (a ripple's
// signature; a bare count needs none).
type realtimeForeignReedStatsHook func(ctx context.Context, requestingServerID, requestingUserID string, payload json.RawMessage) error

// SetForeignReedStatsHook installs the leg-10 (reed-stats push) hook.
func (rs *realtimeService) SetForeignReedStatsHook(hook realtimeForeignReedStatsHook) {
	rs.foreignReedStatsHook = hook
}

// realtimeForeignAckHook notifies homeServerID over peer HTTP (leg 5) that
// peerEventID's delivered content was verified and locally allocated on
// the originating server — the home server should mirror that allocation
// on its own side (against its per-peer sentinel) so a future
// GetUnallocatedReeds-style query for this peer excludes it, instead of
// re-offering content the peer already has on every subscribe.
// Fire-and-forget: the originating server has already persisted its own
// allocation before calling this (the client's verified content is never
// lost even if this notification fails — only the home server's
// bookkeeping goes stale, the same pre-existing class of gap as an
// unacked local pending event).
type realtimeForeignAckHook func(ctx context.Context, homeServerID, peerEventID string) error

// SetForeignAckHook installs the leg-5 (ack-delivered) hook.
func (rs *realtimeService) SetForeignAckHook(hook realtimeForeignAckHook) {
	rs.foreignAckHook = hook
}

// realtimeForeignHolderNotifyHook tells homeServerID over peer HTTP that
// this server now holds a verified copy of reedID (a reed it doesn't own)
// — fire-and-forget, called whenever a local client acks a foreign reed,
// regardless of which delivery path brought it. Distinct from
// realtimeForeignAckHook: that closes out a specific leg-1-originated
// pending event; this simply informs the home server it has a new
// fallback target for reedID, and can fire even when there is no matching
// foreign_pending_events row (e.g. content that arrived via ordinary
// FOLLOW_REED/REED_REPLY fanout, not a REQUEST_REED this server itself
// initiated).
type realtimeForeignHolderNotifyHook func(ctx context.Context, homeServerID, reedID string) error

// SetForeignHolderNotifyHook installs the holder-notify hook.
func (rs *realtimeService) SetForeignHolderNotifyHook(hook realtimeForeignHolderNotifyHook) {
	rs.foreignHolderNotifyHook = hook
}

// realtimeForeignFallbackRequestHook asks peerServerID — a server
// previously notified (via realtimeForeignHolderNotifyHook) that it holds
// a copy of reedID — to relay that copy back to one of THIS server's own
// local users. Unlike realtimeForeignRequestReedHook (leg 1), which always
// targets reedID's own home server, this targets an explicit candidate
// peer chosen from GetForeignHolderServers, since reedID's embedded home
// server is this server itself in the fallback scenario.
type realtimeForeignFallbackRequestHook func(ctx context.Context, peerServerID, reedID, requesterUserID, localRequestID string) (result realtimeForeignRequestResult, peerEventID string, err error)

// SetForeignFallbackRequestHook installs the fallback-request hook.
func (rs *realtimeService) SetForeignFallbackRequestHook(hook realtimeForeignFallbackRequestHook) {
	rs.foreignFallbackHook = hook
}

// realtimeForeignNewReedNotifyHook pushes one newly-published reed (authorID
// is local to this server, the home server H) out to requestingServerID (O)
// so O can register it against a durable profile subscription it already
// holds — the live counterpart of realtimeForeignSubscribeProfileHook's
// one-time backfill (leg 1b). Without this push, O never learns
// peerEventID exists, so H's eventual RELAY_RESPONSE for it has nothing on
// O's side to resolve against and is silently dropped (leg 2's
// DeliverRelayResponseFromPeer 404s). Fire-and-forget from the caller's
// side: a delivery failure here just means that one viewer misses this
// reed's live push and falls back to picking it up on their next
// resubscribe/reload, exactly as if this hook didn't exist.
type realtimeForeignNewReedNotifyHook func(ctx context.Context, requestingServerID, authorID, requesterUserID, reedID, peerEventID string) error

// SetForeignNewReedNotifyHook installs the leg-17 (new-reed-notify) hook.
func (rs *realtimeService) SetForeignNewReedNotifyHook(hook realtimeForeignNewReedNotifyHook) {
	rs.foreignNewReedNotifyHook = hook
}

// DisconnectUser closes all WebSocket connections for a user (device rebind kick).
// Its caller passes a bare userID, but connManager's registry is keyed by
// the "userID@serverID" form (see authenticateWebSocket) — convert here at
// the boundary.
func (rs *realtimeService) DisconnectUser(userID string) {
	selfIdentity := canonicalID(rs.db.GetServerID(), userID)
	rs.connManager.DisconnectUser(string(selfIdentity))
}

// Shutdown notifies every connected client the server is going away and
// closes their sockets. Call this before the process exits so clients can
// reconnect immediately instead of waiting on a connection that silently
// went dead.
func (rs *realtimeService) Shutdown() {
	rs.connManager.BroadcastShutdown()
}

func (rs *realtimeService) deviceMismatch(userID, deviceID string) bool {
	if rs.deviceCheck == nil {
		return false
	}
	return rs.deviceCheck(userID, deviceID) != nil
}

func (rs *realtimeService) ongoingImport(userID string) (bool, error) {
	if rs.ongoingCheck == nil {
		return false, nil
	}
	return rs.ongoingCheck(userID)
}

// Start starts the realtime service.
func (rs *realtimeService) Start(broadcastChan <-chan realtimeBroadcastMessage) {
	go rs.connManager.Start()
	go rs.handleBroadcasts(broadcastChan)
	go rs.startPeriodicCleanup()

	log.Info().Msg("[OK] Realtime service started")
}

// handleBroadcasts handles incoming broadcast messages from the main app.
func (rs *realtimeService) handleBroadcasts(broadcastChan <-chan realtimeBroadcastMessage) {
	for message := range broadcastChan {
		log.Debug().
			Str("type", message.Type.String()).
			Str("userID", message.UserID).
			Str("reedID", message.ReedID).
			Msg("Received broadcast message")

		if message.Type == realtimeEchoCountChanged {
			reedID := string(appendEntity(identityID(message.UserID), message.ReedID))
			rs.notifyReedEchoes(reedID)
		}

		if message.Type == realtimeReplyCountChanged {
			reedID := string(appendEntity(identityID(message.UserID), message.ReedID))
			rs.notifyReedReplies(reedID)
		}

		if message.Type == realtimeLikeCountChanged {
			reedID := string(appendEntity(identityID(message.UserID), message.ReedID))
			rs.notifyReedLikes(reedID)
		}

		if message.Type == realtimeReplyPosted {
			reedID := string(appendEntity(identityID(message.UserID), message.ReedID))
			replyReedID := string(appendEntity(identityID(message.ReplyUserID), message.ReplyReedID))
			rs.notifyReedSubscribersOfReply(reedID, replyReedID)
		}

		if message.Type == realtimeRipplePosted && message.Ripple != nil {
			reedID := string(appendEntity(identityID(message.UserID), message.ReedID))
			rs.notifyRipplePosted(reedID, message.Ripple.UserID, *message.Ripple)
		}

		if message.Type == realtimeRippleUpdated && message.Ripple != nil {
			reedID := string(appendEntity(identityID(message.UserID), message.ReedID))
			rs.notifyRippleUpdated(reedID, message.Ripple.UserID, *message.Ripple)
		}

		if message.Type == realtimeReedRemoved {
			reedID := string(appendEntity(identityID(message.UserID), message.ReedID))
			log.Info().
				Str("userID", message.UserID).
				Str("reedID", reedID).
				Msg("Reed removed; fanout cert")
			rs.fanoutReedRemoval(message.UserID, reedID, message.ReedRemoval)
		}

		if message.Type == realtimeAccountRemoved {
			log.Info().
				Str("userID", message.UserID).
				Msg("Account removed; fanout cert")
			rs.fanoutAccountRemoval(message.UserID, message.AccountRemoval)
		}
	}
}

// fanoutAccountRemoval delivers removedUserID's account-removal cert to
// every local follower, broadcast subscriber, and profile subscriber —
// shared by the local realtimeAccountRemoved broadcast and
// HandleForeignAccountRemoval (a peer holding removedUserID's content
// telling us it happened).
func (rs *realtimeService) fanoutAccountRemoval(removedUserID string, cert *accountRemovalWire) {
	followers, err := rs.db.GetOnlineFollowers(context.Background(), removedUserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get online followers for account removal")
	}
	broadcastRecipients, err := rs.db.GetBroadcastSubscribers(context.Background(), removedUserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get broadcast subscribers for account removal")
	}
	rs.dispatchAccountRemovalMany(followers, removedUserID, cert)
	rs.dispatchAccountRemovalMany(broadcastRecipients, removedUserID, cert)

	profileSubscribers, err := rs.db.GetProfileSubscribers(context.Background(), removedUserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get profile subscribers for account removal")
	}
	for _, sub := range profileSubscribers {
		rs.dispatchAccountRemovalTo(sub.ViewerUserID, removedUserID, cert)
	}
}

// fanoutReedRemoval delivers reedID's removal cert to every local follower,
// broadcast subscriber, profile subscriber, and reed-thread subscriber of
// authorUserID/reedID — shared by the local realtimeReedRemoved broadcast
// and HandleForeignReedRemoval (a peer holding this reed telling us it
// happened).
func (rs *realtimeService) fanoutReedRemoval(authorUserID, reedID string, cert *reedRemovalWire) {
	followers, err := rs.db.GetOnlineFollowers(context.Background(), authorUserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get online followers for reed removal")
	}
	broadcastRecipients, err := rs.db.GetBroadcastSubscribers(context.Background(), authorUserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get broadcast subscribers for reed removal")
	}
	rs.dispatchRemovalMany(followers, reedID, cert)
	rs.dispatchRemovalMany(broadcastRecipients, reedID, cert)

	profileSubscribers, err := rs.db.GetProfileSubscribers(context.Background(), authorUserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get profile subscribers for reed removal")
	}
	for _, sub := range profileSubscribers {
		rs.dispatchRemovalTo(sub.ViewerUserID, reedID, cert)
	}

	// Anyone viewing this reed's thread (SUBSCRIBE_REED) needs to know it's
	// gone too — same gap as ReplyPosted: a reed-stat subscriber isn't
	// necessarily a follower/broadcast/profile subscriber.
	reedSubscribers := rs.reedSubscriberUserIDs(reedID, "")
	rs.dispatchRemovalMany(reedSubscribers, reedID, cert)

	// If the removed reed was itself a reply, everyone subscribed to an
	// ancestor further up the thread also needs the removal notice — they
	// were shown the reply and need to know it's gone.
	rs.notifyReplyAncestorsOfRemoval(reedID, cert)
}

// fanoutNewReed dispatches a newly published reed to followers, broadcast subs,
// profile subs, and pipe listeners for the claimed tags. reedID is canonical.
// excludeFromFollowers drops recipients who are already getting REED_REPLY
// for this same reed via notifyParentSubscribersOfReply (see handlePublishReady).
func (rs *realtimeService) fanoutNewReed(reedID string, tags []string, excludeFromFollowers []string) {
	authorUserID := reedAuthorIdentity(reedID)
	log.Info().
		Str("userID", authorUserID).
		Str("reedID", reedID).
		Int("tags", len(tags)).
		Msg("Fanning out new reed")

	broadcastRecipients, err := rs.db.GetBroadcastSubscribers(context.Background(), authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get broadcast subscribers from database")
	}

	rs.fanoutNewReedCore(reedID, broadcastRecipients, tags, excludeFromFollowers)
}

// fanoutNewReedNoBroadcast dispatches to followers, profile subs, and pipe
// listeners only (no broadcast stream). reedID is canonical.
func (rs *realtimeService) fanoutNewReedNoBroadcast(reedID string, tags []string, excludeFromFollowers []string) {
	log.Info().
		Str("userID", reedAuthorIdentity(reedID)).
		Str("reedID", reedID).
		Int("tags", len(tags)).
		Msg("Fanning out new reed (no broadcast)")

	rs.fanoutNewReedCore(reedID, nil, tags, excludeFromFollowers)
}

func (rs *realtimeService) fanoutNewReedCore(reedID string, broadcastRecipients, tags, excludeFromFollowers []string) {
	authorUserID := reedAuthorIdentity(reedID)

	onlineAdmins, err := rs.db.GetOnlineAdmins(context.Background(), authorUserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get online admins for reed archival")
	}

	followers, err := rs.db.GetOnlineFollowers(context.Background(), authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get online followers from database")
	}

	pipeListeners, err := rs.db.GetPipeListeners(context.Background(), normalizePipeTags(tags), authorUserID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get pipe listeners from database")
	}
	// Pipe listeners always get PIPE_REED (push). Followers who are not on the
	// pipe get FOLLOW_REED. Overlap prefers PIPE_REED (one event).
	followersOnly := subtractUserIDs(followers, pipeListeners)
	// Followers already subscribed to the parent reed are about to get
	// REED_REPLY from notifyParentSubscribersOfReply — drop them here so they
	// don't also get a redundant FOLLOW_REED for the same reply.
	followersOnly = subtractUserIDs(followersOnly, excludeFromFollowers)

	// An admin who already gets the reed via FOLLOW_REED/PIPE_REED doesn't
	// also need ARCHIVE_REED — the reed lands in their local store either
	// way, so archival-only delivery is for admins with no other route to it.
	archiveOnly := subtractUserIDs(onlineAdmins, unionUserIDs(followersOnly, pipeListeners))
	rs.dispatchMany(archiveOnly, archiveReedEvent, reedID)

	durable := unionUserIDs(followersOnly, pipeListeners)
	broadcastOnly := subtractUserIDs(broadcastRecipients, durable)
	// Admins covered by ARCHIVE_REED above don't also need BROADCAST_REED.
	broadcastOnly = subtractUserIDs(broadcastOnly, archiveOnly)

	log.Info().
		Str("userID", authorUserID).
		Int("followersOnly", len(followersOnly)).
		Int("pipeListeners", len(pipeListeners)).
		Int("broadcastSubscribers", len(broadcastOnly)).
		Msg("Dispatching new reed to recipients")
	rs.dispatchMany(followersOnly, followReedEvent, reedID)
	rs.dispatchMany(pipeListeners, pipeReedEvent, reedID)
	rs.dispatchMany(broadcastOnly, broadcastReedEvent, reedID)

	profileSubscribers, err := rs.db.GetProfileSubscribers(context.Background(), authorUserID)
	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to get profile subscribers from database")
	}

	for _, sub := range profileSubscribers {
		foreign, viewerServerID := rs.isForeignReed(sub.ViewerUserID)
		// pending_events.requester_user_id FKs to online_users, which a
		// foreign viewer never has a row in — store NULL for the foreign
		// case (createProfileSubscriptionEvent's convention) instead of a
		// sentinel. Delivery still ends up at the real viewer:
		// handleRelayResponse's default branch checks foreign_relay_requests
		// (recorded below) and routes to foreignDeliverHook instead of a
		// local WS send. eventID/requestID always inherit the REAL viewer's
		// identity (generateEventID's own contract), never a placeholder.
		fkRequesterUserID := sub.ViewerUserID
		if foreign {
			fkRequesterUserID = ""
		}

		eventID := generateRealtimeEventID(sub.ViewerUserID)
		requestID := generateRealtimeEventID(sub.ViewerUserID)
		if err := rs.createProfileSubscriptionEvent(context.Background(), eventID, requestID, fkRequesterUserID, profileSubscriptionEvent, reedID, sub.SubscriptionID); err != nil {
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
			// against on the viewer's side (see realtimeForeignNewReedNotifyHook).
			if rs.foreignNewReedNotifyHook != nil {
				if err := rs.foreignNewReedNotifyHook(context.Background(), viewerServerID, authorUserID, sub.ViewerUserID, reedID, eventID); err != nil {
					log.Error().Err(err).Str("viewerUserID", sub.ViewerUserID).Msg("Failed to notify viewer's server of new reed for live profile fanout")
				}
			}
		}
	}

	rs.dispatchNIfConnected(authorUserID, initialFanoutBurst)
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
func (rs *realtimeService) dispatchRemovalMany(recipients []string, reedID string, cert *reedRemovalWire) {
	for _, recipientID := range recipients {
		rs.dispatchRemovalTo(recipientID, reedID, cert)
	}
}

func (rs *realtimeService) dispatchRemovalTo(recipientID, reedID string, cert *reedRemovalWire) {
	requestID, err := rs.db.GetSyncRequestID(context.Background(), recipientID)
	if err != nil || requestID == "" {
		return
	}
	eventID := generateRealtimeEventID(recipientID)
	if err := rs.createPendingReedEvent(context.Background(), eventID, requestID, recipientID, reedRemovedEvent, reedID); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Msg("Failed to create reed_removed pending event")
		return
	}
	rs.deliverReedRemoved(eventID, requestID, recipientID, reedID, cert)
}

func (rs *realtimeService) dispatchAccountRemovalMany(recipients []string, removedUserID string, cert *accountRemovalWire) {
	for _, recipientID := range recipients {
		rs.dispatchAccountRemovalTo(recipientID, removedUserID, cert)
	}
}

func (rs *realtimeService) dispatchAccountRemovalTo(recipientID, removedUserID string, cert *accountRemovalWire) {
	requestID, err := rs.db.GetSyncRequestID(context.Background(), recipientID)
	if err != nil || requestID == "" {
		return
	}
	eventID := generateRealtimeEventID(recipientID)
	if err := rs.createPendingAccountEvent(context.Background(), eventID, requestID, recipientID, removedUserID); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Msg("Failed to create account_removed pending event")
		return
	}
	rs.deliverAccountRemoved(eventID, requestID, recipientID, removedUserID, cert)
}

func (rs *realtimeService) deliverAccountRemoved(eventID, requestID, recipientID, removedUserID string, cert *accountRemovalWire) {
	wire := accountRemovalWire{}
	if cert != nil {
		wire = *cert
	} else {
		var err error
		wire, err = rs.db.GetAccountRemovalWire(context.Background(), removedUserID)
		if err != nil || wire.UserID == "" {
			log.Error().Err(err).Str("userID", removedUserID).Msg("Failed to load account removal cert for delivery")
			return
		}
	}
	ok, err := rs.db.MarkEventDispatched(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to mark account_removed dispatched")
		return
	}
	if !ok {
		return
	}
	if err := rs.connManager.SendToUser(recipientID, newAccountRemovedMsg(eventID, requestID, wire)); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Str("userID", removedUserID).Msg("Failed to send ACCOUNT_REMOVED")
	}
}

func (rs *realtimeService) deliverReedRemoved(eventID, requestID, recipientID, reedID string, cert *reedRemovalWire) {
	wire := reedRemovalWire{}
	if cert != nil {
		wire = *cert
	} else {
		var err error
		wire, err = rs.db.GetReedRemovalWire(context.Background(), reedID)
		if err != nil || wire.UserID == "" {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to load reed removal cert for delivery")
			return
		}
	}
	ok, err := rs.db.MarkEventDispatched(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to mark reed_removed dispatched")
		return
	}
	if !ok {
		return
	}
	if err := rs.connManager.SendToUser(recipientID, newReedRemovedMsg(eventID, requestID, wire)); err != nil {
		log.Error().Err(err).Str("recipientID", recipientID).Str("reedID", reedID).Msg("Failed to send REED_REMOVED")
	}
}

// dispatchMany creates a pending reed event for each recipient and triggers
// relay dispatch to the reed's author (also the holder for published-reed
// fanout). reedID is canonical.
func (rs *realtimeService) dispatchMany(recipients []string, eventName realtimeEventName, reedID string) {
	if foreign, homeServerID := rs.isForeignReed(reedID); foreign {
		rs.dispatchManyForeign(recipients, eventName, reedID, homeServerID)
		return
	}

	for _, recipientID := range recipients {
		requestID, err := rs.db.GetSyncRequestID(context.Background(), recipientID)
		if err != nil || requestID == "" {
			// User hasn't sent SYNC_REQUEST yet; they'll receive this via catchUp on connect.
			log.Debug().
				Str("recipientID", recipientID).
				Str("eventName", string(eventName)).
				Msg("Skipping recipient: no sync_request_id set")
			continue
		}
		eventID := generateRealtimeEventID(recipientID)
		if err := rs.createPendingReedEvent(context.Background(), eventID, requestID, recipientID, eventName, reedID); err != nil {
			log.Error().
				Err(err).
				Str("recipientID", recipientID).
				Msg("Failed to create pending event")
			continue
		}
		log.Debug().
			Str("recipientID", recipientID).
			Str("eventID", eventID).
			Msg("Pending event created")
		// Dispatch is windowed, once, in fanoutNewReedCore's trailing
		// dispatchN call — not per-recipient here, which would flood
		// the author's single connection with no backpressure.
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
func (rs *realtimeService) dispatchManyForeign(recipients []string, eventName realtimeEventName, reedID, homeServerID string) {
	if rs.foreignRequestReedHook == nil {
		return
	}
	if err := rs.db.UpsertReedIdentity(context.Background(), reedID); err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to upsert reed identity for foreign dispatch")
		return
	}
	for _, recipientID := range recipients {
		requestID := generateRealtimeEventID(recipientID)
		eventID := generateRealtimeEventID(recipientID)
		if err := rs.createPendingReedEvent(context.Background(), eventID, requestID, recipientID, eventName, reedID); err != nil {
			log.Error().Err(err).Str("recipientID", recipientID).Msg("Failed to create local pending event for foreign dispatch")
			continue
		}
		result, peerEventID, err := rs.foreignRequestReedHook(context.Background(), reedID, recipientID, requestID)
		if err != nil || result != realtimeForeignRequestOK {
			if err != nil {
				log.Error().Err(err).Str("reedID", reedID).Str("homeServerID", homeServerID).Msg("Failed to register foreign dispatch with home server")
			}
			if delErr := rs.deletePendingEvent(context.Background(), eventID); delErr != nil {
				log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete speculative pending event after foreign dispatch failure")
			}
			continue
		}
		if err := rs.db.CreateForeignPendingEvent(context.Background(), eventID, homeServerID, peerEventID); err != nil {
			log.Error().Err(err).Msg("Failed to record foreign pending event mapping for foreign dispatch")
			if delErr := rs.deletePendingEvent(context.Background(), eventID); delErr != nil {
				log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete pending event after foreign_pending_events insert failure")
			}
		}
	}
}

// Presence heartbeat windows. Clients send PONG on realtimePongInterval;
// a row surviving realtimePresenceTTL without one is treated as dead. The
// TTL is twice the interval so a single dropped beat isn't an eviction.
const (
	realtimePongInterval  = 1 * time.Minute
	realtimePresenceTTL   = 2 * time.Minute
	realtimeReapFrequency = 30 * time.Second
)

// startPeriodicCleanup evicts presence rows whose PONG heartbeat lapsed.
// Subscriptions cascade off online_users, so this also reclaims them for a
// client that vanished without a clean disconnect.
func (rs *realtimeService) startPeriodicCleanup() {
	ticker := time.NewTicker(realtimeReapFrequency)
	defer ticker.Stop()

	for range ticker.C {
		rs.reapStalePresence()
	}
}

// reapStalePresence is one eviction pass. An evicted user still holding a
// local socket is disconnected too, so the client reconnects and rebuilds
// its presence row instead of sitting on a connection the DB forgot.
func (rs *realtimeService) reapStalePresence() {
	evicted, err := rs.db.ReapStalePresence(context.Background(), realtimePresenceTTL)
	if err != nil {
		log.Error().Err(err).Msg("Failed to reap stale presence rows")
		return
	}
	if len(evicted) == 0 {
		return
	}

	for _, userID := range evicted {
		if rs.connManager.HasConnection(userID) {
			rs.connManager.DisconnectUser(userID)
		}
	}
	log.Info().Int("evicted", len(evicted)).Msg("Evicted stale presence rows")
}

// HandleWebSocket handles WebSocket connections.
func (rs *realtimeService) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	log.Info().Msg("WebSocket connection attempt")

	userID, err := authenticateWebSocket(r, rs.db, rs.crypto)
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
		rejectRealtimeConnection(w, "Device mismatch: this session is not bound to the active device.")
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
		rejectRealtimeConnection(w, "Finish recovery import first.")
		return
	}

	if _, ok := w.(http.Hijacker); !ok {
		log.Error().Msg("Response writer does not implement http.Hijacker")
		http.Error(w, "WebSocket not supported", http.StatusInternalServerError)
		return
	}

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

	client := newRealtimeClient(conn, userID)
	client.wsRecordOutbound = func(messageType int, data []byte) {
		rs.metrics.WSMessage(context.Background(), metrics.DirectionOut, metrics.WSMessageType(messageType, data))
	}
	rs.connManager.RegisterClient(client)

	if err := rs.db.MarkUserOnline(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).Msg("Failed to mark user as online")
		return
	}

	rs.handleUserCameOnline(client)

	log.Info().
		Str("userID", userID).
		Msg("WebSocket client connected")

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

	if err := rs.db.MarkUserOffline(context.Background(), userID); err != nil {
		log.Error().Err(err).Msg("Failed to mark user as offline")
	}

	if err := rs.db.UnsubscribeFromBroadcast(context.Background(), userID); err != nil {
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
		foreignPending, err := rs.db.GetForeignPendingEventsByRequester(context.Background(), userID)
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
	if deleted, err := rs.db.DeletePendingEventsByUser(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to delete pending events on disconnect")
	} else {
		for _, d := range deleted {
			rs.metrics.RelayEvent(context.Background(), metrics.RelayEventDeleted, d.EventName, d.EventID)
		}
	}

	// Notify any foreign authors' home servers that this viewer's live
	// fanout subscriptions are going away — must run BEFORE the delete
	// below removes the local rows. Same best-effort/non-blocking
	// treatment as the foreignCancelHook block above.
	if rs.foreignUnsubscribeProfileHook != nil {
		viewerSubs, err := rs.db.GetProfileSubscriptionsByViewer(context.Background(), userID)
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

	if err := rs.db.DeleteProfileSubscriptionsByViewer(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to delete profile subscriptions on disconnect")
	}

	// Same treatment for reed-stats subscriptions: notify foreign reeds'
	// home servers before the local rows are deleted below.
	if rs.foreignUnsubscribeReedHook != nil {
		reedSubs, err := rs.db.GetReedSubscriptionsByViewer(context.Background(), userID)
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

	if err := rs.db.DeleteReedSubscriptionsByViewer(context.Background(), userID); err != nil {
		log.Error().
			Str("userID", userID).
			Err(err).
			Msg("Failed to delete reed subscriptions on disconnect")
	}

	log.Info().
		Str("userID", userID).
		Msg("WebSocket client disconnected")
}

// checkOrigin validates the Origin header against the allowed origin.
func (rs *realtimeService) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")

	if origin == "" {
		log.Warn().Msg("WebSocket connection rejected: missing Origin header")
		return false
	}

	if rs.allowedOrigin == "" {
		log.Warn().
			Str("origin", origin).
			Msg("WebSocket connection rejected: no allowed origin configured")
		return false
	}

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

// handleClientMessages handles incoming messages from a client.
func (rs *realtimeService) handleClientMessages(client *realtimeClient) {
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

		rs.metrics.WSMessage(context.Background(), metrics.DirectionIn, metrics.WSMessageType(messageType, data))

		switch messageType {
		case websocket.BinaryMessage:
			rs.handleProtobufMessage(client, data)
		case websocket.TextMessage:
			log.Warn().Msg("Rejecting text WebSocket frame; only binary protobuf frames are accepted")
			client.conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "text frames are not supported; send binary protobuf"),
				time.Now().Add(time.Second))
			return
		default:
			log.Warn().Int("messageType", messageType).Msg("Received unsupported message type, ignoring")
			continue
		}
	}
}

// handleProtobufMessage handles every inbound WebSocket message, all of
// which travel as a binary protobuf WSMessage.
func (rs *realtimeService) handleProtobufMessage(client *realtimeClient, data []byte) {
	var msg pb.WSMessage
	if err := proto.Unmarshal(data, &msg); err != nil {
		log.Error().Err(err).Msg("Failed to unmarshal protobuf message")
		return
	}

	log.Debug().Str("type", msg.Type.String()).Msg("Received protobuf WebSocket message")

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

	case pb.MessageType_REQUEST_REED:
		rs.handleRequestReed(client, msg.GetRequestReed())

	case pb.MessageType_SYNC_REQUEST:
		rs.handleSyncRequest(client, msg.GetSyncRequest().GetRequestId())

	case pb.MessageType_RELAY_RESPONSE:
		rs.handleRelayResponse(client, msg.Id, msg.GetRelayResponse().GetCiphertext())

	case pb.MessageType_RELAY_MISS:
		rs.handleRelayMiss(client.userID, msg.Id)

	case pb.MessageType_RELAY_ERROR:
		rs.handleRelayError(client.userID, msg.Id)

	case pb.MessageType_DATA_ACK:
		rs.handleDataAck(client, msg.Id)

	case pb.MessageType_DATA_INVALID:
		rs.handleDataInvalid(client, msg.Id)

	case pb.MessageType_MAILBOX_ACK:
		rs.handleMailboxAck(client, msg.GetMailboxAck().GetId())

	case pb.MessageType_KEY_FETCH_ERROR:
		kfe := msg.GetKeyFetchError()
		rs.handleKeyFetchError(client, kfe.GetUserId(), kfe.GetKeyId())

	case pb.MessageType_REVOKED_KEY_USED:
		rku := msg.GetRevokedKeyUsed()
		rs.handleRevokedKeyUsed(client, rku.GetUserId(), rku.GetKeyId())

	case pb.MessageType_CONTENT_REJECTED:
		cr := msg.GetContentRejected()
		rs.handleContentRejected(client, cr.GetStoreName(), cr.GetReason())

	case pb.MessageType_SUBSCRIBE_PROFILE:
		rs.handleSubscribeProfile(client, msg.GetSubscribeProfile().GetUserId())

	case pb.MessageType_UNSUBSCRIBE_PROFILE:
		rs.handleUnsubscribeProfile(client, msg.GetUnsubscribeProfile().GetUserId())

	case pb.MessageType_PROFILE_PAGE:
		pp := msg.GetProfilePage()
		rs.handleProfilePage(client, pp.GetUserId(), pp.GetPage())

	case pb.MessageType_PUBLISH_READY:
		pr := msg.GetPublishReady()
		rs.handlePublishReady(client, pr.GetReedId(), shouldBroadcastReed(pr))

	case pb.MessageType_EVICTION:
		rs.handleEviction(client, msg.GetEviction().GetReedId())

	case pb.MessageType_SUBSCRIBE_REED:
		rs.handleSubscribeReed(client, msg.GetSubscribeReed().GetReedId())

	case pb.MessageType_UNSUBSCRIBE_REED:
		rs.handleUnsubscribeReed(client, msg.GetUnsubscribeReed().GetReedId())

	case pb.MessageType_SUBSCRIBE_PIPE:
		rs.handleSubscribePipe(client, msg.GetSubscribePipe().GetTag())

	case pb.MessageType_UNSUBSCRIBE_PIPE:
		rs.handleUnsubscribePipe(client, msg.GetUnsubscribePipe().GetTag())

	case pb.MessageType_PONG:
		rs.handlePong(client)

	default:
		log.Warn().Str("type", msg.Type.String()).Msg("Unknown protobuf WebSocket message type")
	}
}

// handlePing handles ping messages.
func (rs *realtimeService) handlePing(client *realtimeClient, ping *pb.PingMessage) {
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

// handlePong records a client-initiated liveness heartbeat. The SPA sends
// PONG once a minute; presence rows that stop being refreshed are evicted by
// reapStalePresence, which is what lets a crashed replica's stale rows expire.
func (rs *realtimeService) handlePong(client *realtimeClient) {
	if err := rs.db.RecordPong(context.Background(), client.userID); err != nil {
		log.Error().Err(err).Str("userID", client.userID).Msg("Failed to record PONG heartbeat")
	}
}

// handleSubscribeUser handles user subscription requests.
func (rs *realtimeService) handleSubscribeUser(client *realtimeClient, subscribe *pb.SubscribeMessage) {
	client.Subscribe(realtimeSubscribeUser)
}

// handleSubscribeBroadcast handles broadcast subscription requests.
func (rs *realtimeService) handleSubscribeBroadcast(client *realtimeClient, subscribe *pb.SubscribeMessage) {
	err := rs.db.SubscribeToBroadcast(context.Background(), client.userID)
	if err != nil {
		log.Error().
			Str("userID", client.userID).
			Err(err).
			Msg("Failed to persist broadcast subscription to database")
	}
}

// handleUnsubscribeUser handles user unsubscription requests.
func (rs *realtimeService) handleUnsubscribeUser(client *realtimeClient, subscribe *pb.SubscribeMessage) {
	client.Unsubscribe(realtimeSubscribeUser)
}

// handleUnsubscribeBroadcast handles broadcast unsubscription requests.
func (rs *realtimeService) handleUnsubscribeBroadcast(client *realtimeClient, subscribe *pb.SubscribeMessage) {
	if err := rs.db.UnsubscribeFromBroadcast(context.Background(), client.userID); err != nil {
		log.Error().
			Str("userID", client.userID).
			Err(err).
			Msg("Failed to remove broadcast subscription from database")
	}
}

// sendProtobufMessage sends a protobuf message to a client.
func (rs *realtimeService) sendProtobufMessage(client *realtimeClient, msg *pb.WSMessage) {
	data, err := marshalWSMessage(msg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal protobuf message")
		return
	}

	if err := client.writeMessage(websocket.BinaryMessage, data); err != nil {
		log.Error().Err(err).Msg("Failed to write protobuf message")
		return
	}
}

// GetConnectionCount returns the number of active connections.
func (rs *realtimeService) GetConnectionCount() int {
	return rs.connManager.GetConnectionCount()
}

// dispatchRequestTimeout bounds how long the server waits for a holder to
// answer one dispatched RELAY_REQUEST (RELAY_RESPONSE/MISS/ERROR) before
// giving up on them and retrying with another holder.
const dispatchRequestTimeout = 5 * time.Second

// initialFanoutBurst is how many pending events go to the author (the only
// holder at t=0) up front, kept small since the author alone pays for each
// one plus any retry — fanoutRefillBurst takes over once other holders exist.
const initialFanoutBurst = 2

// fanoutRefillBurst is how many further events get dispatched to a holder
// each time one of their in-flight relay requests resolves (response, miss,
// error, or timeout) — the windowed backlog's refill size.
const fanoutRefillBurst = 5

// profilePageSize is how many of an author's reeds one PROFILE_PAGE covers.
// Shared with the client, which paginates in the same steps.
const profilePageSize = 50

// dispatchNext claims and sends the holder's oldest undispatched pending
// event, if any. Returns true if something was dispatched — dispatchN uses
// this to know when a holder's queue has run dry.
func (rs *realtimeService) dispatchNext(holderUserID string) bool {
	pe, err := rs.db.GetNextPendingForHolder(context.Background(), holderUserID)
	if err != nil {
		log.Error().Err(err).Str("holderUserID", holderUserID).Msg("Failed to get next pending event for holder")
		return false
	}
	if pe == nil {
		log.Debug().Str("holderUserID", holderUserID).Msg("No pending events for holder")
		return false
	}
	if realtimeEventName(pe.EventName) == reedRemovedEvent {
		rs.deliverReedRemoved(pe.EventID, pe.RequestID, pe.RequesterUserID, pe.ReedID, nil)
		return rs.dispatchNext(holderUserID)
	}
	ok, err := rs.db.MarkEventDispatched(context.Background(), pe.EventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", pe.EventID).Msg("Failed to mark event dispatched")
		return false
	}
	if !ok {
		return false // another replica claimed it
	}
	// pe.RequesterUserID is empty for a foreign-attributed event (no local
	// online_users row backs a remote requester) — the holder needs the
	// real remote user id to resolve their key and encrypt to them, so
	// look it up from foreign_relay_requests instead.
	requesterID := pe.RequesterUserID
	if frr, ferr := rs.db.GetForeignRelayRequest(context.Background(), pe.EventID); ferr != nil {
		log.Error().Err(ferr).Str("eventID", pe.EventID).Msg("Failed to check foreign relay request for dispatch")
	} else if frr != nil {
		requesterID = frr.RequestingUserID
	}
	if err := rs.connManager.SendToUser(holderUserID, newRelayRequestMsg(pe.EventID, pe.ReedID, requesterID)); err != nil {
		log.Error().
			Err(err).
			Str("holderUserID", holderUserID).
			Str("eventID", pe.EventID).
			Str("reedID", pe.ReedID).
			Msg("Failed to send relay request to holder")
		if resetErr := rs.db.ResetDispatchedAt(context.Background(), pe.EventID); resetErr != nil {
			log.Error().Err(resetErr).Str("eventID", pe.EventID).Msg("Failed to reset dispatched_at after relay send failure")
		}
		return false
	}
	log.Debug().
		Str("holderUserID", holderUserID).
		Str("eventID", pe.EventID).
		Str("reedID", pe.ReedID).
		Msg("Relay request sent to holder")

	eventID := pe.EventID
	time.AfterFunc(dispatchRequestTimeout, func() {
		rs.handleRelayTimeout(holderUserID, eventID)
	})
	return true
}

// dispatchN calls dispatchNext up to n times, stopping early once the
// holder's queue is empty (dispatchNext returns false).
func (rs *realtimeService) dispatchN(holderUserID string, n int) {
	dispatchNTimes(n, func() bool { return rs.dispatchNext(holderUserID) })
}

// dispatchNTimes calls step up to n times, stopping the first time it
// returns false. Extracted from dispatchN so the loop/early-stop logic is
// testable without a live realtimeService.
func dispatchNTimes(n int, step func() bool) int {
	sent := 0
	for i := 0; i < n; i++ {
		if !step() {
			return sent
		}
		sent++
	}
	return sent
}

func (rs *realtimeService) dispatchNextIfConnected(holderUserID string) {
	if holderUserID == "" {
		return
	}
	if !rs.isUserOnline(holderUserID) {
		log.Debug().Str("holderUserID", holderUserID).Msg("Holder has no active WebSocket; skipping relay dispatch")
		return
	}
	rs.dispatchNext(holderUserID)
}

// dispatchNIfConnected is dispatchN guarded by HasConnection, matching
// dispatchNextIfConnected's guard for the single-dispatch case.
func (rs *realtimeService) dispatchNIfConnected(holderUserID string, n int) {
	if holderUserID == "" {
		return
	}
	if !rs.isUserOnline(holderUserID) {
		log.Debug().Str("holderUserID", holderUserID).Msg("Holder has no active WebSocket; skipping relay dispatch")
		return
	}
	rs.dispatchN(holderUserID, n)
}

// generateRealtimeEventID mints a canonical event_id (requesterUserID/uuid)
// — requesterUserID is already userID@serverID, so the result is
// userID@serverID/uuid, self-describing the same way reed/like/key ids
// are. Every event_id inherits the REAL requester's identity: on a home
// server's sentinel-bookkeeping path (HandleForeignRequestReed/
// HandleForeignSubscribeProfile) callers must pass the original remote
// requester's canonical id here, never the sentinel's.
func generateRealtimeEventID(requesterUserID string) string {
	return string(appendEntity(identityID(requesterUserID), uuid.New().String()))
}

// createPendingReedEvent wraps db.CreatePendingReedEvent, additionally
// recording a "created" RelayEvent lifecycle sample — the counterpart to
// deletePendingEvent's "deleted"/"fulfilled" so wasted-bandwidth analysis
// (created with no matching fulfilled/deleted) can correlate by event.id_hash.
func (rs *realtimeService) createPendingReedEvent(ctx context.Context, eventID, requestID, requesterUserID string, eventName realtimeEventName, reedID string) error {
	if err := rs.db.CreatePendingReedEvent(ctx, eventID, requestID, requesterUserID, eventName, reedID); err != nil {
		return err
	}
	rs.metrics.RelayEvent(ctx, metrics.RelayEventCreated, string(eventName), eventID)
	return nil
}

// createPendingAccountEvent mirrors createPendingReedEvent for account-removal events.
func (rs *realtimeService) createPendingAccountEvent(ctx context.Context, eventID, requestID, requesterUserID, removedUserID string) error {
	if err := rs.db.CreatePendingAccountEvent(ctx, eventID, requestID, requesterUserID, removedUserID); err != nil {
		return err
	}
	rs.metrics.RelayEvent(ctx, metrics.RelayEventCreated, string(accountRemovedEvent), eventID)
	return nil
}

// createProfileSubscriptionEvent mirrors createPendingReedEvent for profile-subscription events.
func (rs *realtimeService) createProfileSubscriptionEvent(ctx context.Context, eventID, requestID, requesterUserID string, eventName realtimeEventName, reedID, subscriptionID string) error {
	if err := rs.db.CreateProfileSubscriptionEvent(ctx, eventID, requestID, requesterUserID, eventName, reedID, subscriptionID); err != nil {
		return err
	}
	rs.metrics.RelayEvent(ctx, metrics.RelayEventCreated, string(eventName), eventID)
	return nil
}

// deletePendingEvent wraps db.DeletePendingEvent, additionally recording a
// "deleted" RelayEvent lifecycle sample when a row was actually removed
// (eventName is empty if it was already gone — nothing to record). Use
// this for a delete that's cleanup/cancellation; use the fulfilled sample
// alongside a delivery instead when the event was actually answered.
func (rs *realtimeService) deletePendingEvent(ctx context.Context, eventID string) error {
	eventName, err := rs.db.DeletePendingEvent(ctx, eventID)
	if err != nil {
		return err
	}
	if eventName != "" {
		rs.metrics.RelayEvent(ctx, metrics.RelayEventDeleted, eventName, eventID)
	}
	return nil
}

// handleRequestReed is REQUEST_REED's entry point: the SPA only
// ever sends this as a binary protobuf frame (specs/protobuf/).
func (rs *realtimeService) handleRequestReed(client *realtimeClient, req *pb.RequestReedMessage) {
	if req == nil || req.GetRequestId() == "" || req.GetReedId() == "" {
		return
	}
	rs.requestReed(client, req.GetRequestId(), req.GetReedId())
}

func (rs *realtimeService) requestReed(client *realtimeClient, requestID, reedID string) {
	if !rs.validateRequestID(requestID, client.userID) {
		rs.connManager.SendToUser(client.userID, newInvalidRequestIDErrorMsg(requestID))
		return
	}

	if foreign, homeServerID := rs.isForeignReed(reedID); foreign {
		rs.handleForeignRequestReedFromClient(client, requestID, reedID, homeServerID)
		return
	}

	exists, hasHolders, _, eventID, err := rs.registerReedRequest(context.Background(), reedID, client.userID, client.userID, requestID, true, requestReedEvent)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to register reed request")
		return
	}
	if !exists {
		log.Debug().Str("reedID", reedID).Msg("Requested reed does not exist, notifying requester")
		rs.connManager.SendToUser(client.userID, newReedNotFoundMsg(requestID, reedID))
		return
	}
	if !hasHolders {
		if rs.foreignFallbackHook != nil {
			if fallbackEventID, ok := rs.tryPeerFallback(context.Background(), reedID, client.userID, requestID); ok {
				rs.connManager.SendToUser(client.userID, newRequestAckMsg(requestID, fallbackEventID, reedID))
				return
			}
		}
		log.Debug().
			Str("reedID", reedID).
			Str("requesterID", client.userID).
			Msg("Requested reed is unheld, notifying requester")
		rs.connManager.SendToUser(client.userID, newReedNotHeldMsg(requestID, reedID))
		return
	}

	rs.connManager.SendToUser(client.userID, newRequestAckMsg(requestID, eventID, reedID))
}

// tryPeerFallback runs when no local holder is online for a reed this
// server is home to. Mints a speculative local pending event (mirroring
// handleForeignRequestReedFromClient's own speculative-pending-event
// pattern), then tries each peer server known to hold a copy
// (GetForeignHolderServers), sequentially. Only a genuine realtimeForeignRequestOK
// (a peer with an online holder right now) stops the loop immediately —
// a realtimeForeignRequestAccepted (a peer with a known holder that's offline) is
// remembered as a fallback candidate but the loop keeps trying other
// peers, preferring an immediate delivery over a sleeping one. If nothing
// ever returns OK, the best-remembered Accepted candidate is committed to
// once every peer has been tried. Exactly one pending_events row is
// created regardless of how many candidates are tried — never touching
// registerReedRequest's own event-minting path, since that path already
// returned early with hasHolders=false. Sequential rather than parallel:
// this is a rare failure-recovery path, not a hot one, and sequential
// trying avoids needing to cancel losing parallel attempts.
func (rs *realtimeService) tryPeerFallback(ctx context.Context, reedID, requesterUserID, requestID string) (eventID string, ok bool) {
	peerServerIDs, err := rs.db.GetForeignHolderServers(ctx, reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to look up foreign holder servers")
		return "", false
	}
	if len(peerServerIDs) == 0 {
		return "", false
	}

	eventID = generateRealtimeEventID(requesterUserID)
	if err := rs.createPendingReedEvent(ctx, eventID, requestID, requesterUserID, requestReedEvent, reedID); err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to create speculative pending event for peer fallback")
		return "", false
	}

	var acceptedPeerServerID, acceptedPeerEventID string
	for _, peerServerID := range peerServerIDs {
		result, peerEventID, err := rs.foreignFallbackHook(ctx, peerServerID, reedID, requesterUserID, requestID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("peerServerID", peerServerID).Msg("Peer fallback request failed")
			continue
		}
		if result == realtimeForeignRequestAccepted {
			if acceptedPeerServerID == "" {
				acceptedPeerServerID, acceptedPeerEventID = peerServerID, peerEventID
			}
			continue
		}
		if result != realtimeForeignRequestOK {
			continue
		}
		if err := rs.db.CreateForeignPendingEvent(ctx, eventID, peerServerID, peerEventID); err != nil {
			log.Error().Err(err).Msg("Failed to record foreign pending event for peer fallback")
			continue
		}
		return eventID, true
	}

	if acceptedPeerServerID != "" {
		if err := rs.db.CreateForeignPendingEvent(ctx, eventID, acceptedPeerServerID, acceptedPeerEventID); err != nil {
			log.Error().Err(err).Msg("Failed to record foreign pending event for peer fallback")
		} else {
			return eventID, true
		}
	}

	if delErr := rs.deletePendingEvent(ctx, eventID); delErr != nil {
		log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete speculative pending event after exhausting peer fallback candidates")
	}
	return "", false
}

// isForeignReed parses reedID's embedded serverID and reports whether it
// differs from this server's own — mirrors proxyIfForeign's parse exactly.
func (rs *realtimeService) isForeignReed(reedID string) (foreign bool, homeServerID string) {
	_, embeddedServerID, _, ok := parseKeyFingerprint(identityID(reedID))
	if !ok {
		_, embeddedServerID, ok = parseIdentityID(identityID(reedID))
	}
	if !ok || embeddedServerID == rs.db.GetServerID() {
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
func (rs *realtimeService) validateRequestID(id, requesterUserID string) bool {
	userID, serverID, _, ok := parseKeyFingerprint(identityID(id))
	if !ok {
		return false
	}
	return string(canonicalID(serverID, userID)) == requesterUserID
}

// registerReedRequest runs the ReedExists -> (optionally drop requester's
// stale allocation) -> GetOnlineHolders -> CreatePendingReedEvent ->
// dispatch-to-holder sequence shared by a local REQUEST_REED and a
// foreign one registered on this server's behalf via HandleForeignRequestReed.
// dropRequesterAllocation should be true only for a genuine local
// requester — a foreign "requester" (empty FK value) never legitimately
// holds a stale allocation, so that DELETE is skipped for it.
// eventIdentity is who the minted event_id's canonical prefix names —
// normally the same as requesterUserID, except on a home server's
// foreign-bookkeeping path, where requesterUserID is empty (no local
// online_users row backs a remote requester) but eventIdentity is the
// ORIGINAL remote requester, so the event_id still names who actually asked.
//
// hasHolders means "someone has ever fetched and held this reed" —
// independent of whether they're online right now. holderOnline is the
// narrower "someone is here to dispatch to immediately" signal. A pending
// event is created whenever hasHolders is true, even with no one online:
// it just sits with dispatched_at NULL until a holder for this reed
// connects — the existing SYNC_REQUEST -> dispatchNext(holderUserID) path
// already finds and dispatches any such waiting event the moment that
// happens, no separate wake-up mechanism needed.
func (rs *realtimeService) registerReedRequest(ctx context.Context, reedID, requesterUserID, eventIdentity, requestID string, dropRequesterAllocation bool, eventName realtimeEventName) (exists, hasHolders, holderOnline bool, eventID string, err error) {
	exists, err = rs.db.ReedExists(ctx, reedID)
	if err != nil {
		return false, false, false, "", err
	}
	if !exists {
		return false, false, false, "", nil
	}

	if dropRequesterAllocation {
		// Removes a stale holder row when the requester asks for a reed the
		// server thought they held — they clearly do not have the body locally.
		if _, err = rs.db.DeleteReedAllocation(ctx, reedID, requesterUserID); err != nil {
			return false, false, false, "", err
		}
	}

	var holder string
	hasHolders, holder, err = rs.db.GetOnlineHolders(ctx, reedID)
	if err != nil {
		return false, false, false, "", err
	}
	if !hasHolders {
		return true, false, false, "", nil
	}
	holderOnline = holder != ""

	eventID = generateRealtimeEventID(eventIdentity)
	if err = rs.createPendingReedEvent(ctx, eventID, requestID, requesterUserID, eventName, reedID); err != nil {
		return false, false, false, "", err
	}

	if holderOnline {
		rs.dispatchNextIfConnected(holder)
	}

	return true, true, holderOnline, eventID, nil
}

// handleForeignRequestReedFromClient is handleRequestReed's foreign
// branch: register the request with reedID's home server over peer HTTP
// (via the injected hook) instead of running registerReedRequest locally.
func (rs *realtimeService) handleForeignRequestReedFromClient(client *realtimeClient, requestID, reedID, homeServerID string) {
	if rs.foreignRequestReedHook == nil {
		rs.connManager.SendToUser(client.userID, newReedNotFoundMsg(requestID, reedID))
		return
	}

	// reed_identities row must exist before pending_reed_events.reed_id
	// can FK to it — this only claims reedID is a well-formed id worth
	// tracking (the same bar a local reed already clears just by being
	// signed, before anyone verifies/holds its content), not that this
	// server has verified anything about it.
	if err := rs.db.UpsertReedIdentity(context.Background(), reedID); err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to upsert reed identity for foreign relay request")
		return
	}

	eventID := generateRealtimeEventID(client.userID)
	if err := rs.createPendingReedEvent(context.Background(), eventID, requestID, client.userID, requestReedEvent, reedID); err != nil {
		log.Error().Err(err).Msg("Failed to create local pending event for foreign relay request")
		return
	}

	result, peerEventID, err := rs.foreignRequestReedHook(context.Background(), reedID, client.userID, requestID)
	if err != nil || result != realtimeForeignRequestOK {
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("homeServerID", homeServerID).Msg("Failed to register foreign reed request with home server")
		}
		if delErr := rs.deletePendingEvent(context.Background(), eventID); delErr != nil {
			log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete speculative pending event after foreign request failure")
		}
		if result == realtimeForeignRequestReedNotFound {
			rs.connManager.SendToUser(client.userID, newReedNotFoundMsg(requestID, reedID))
		} else {
			rs.connManager.SendToUser(client.userID, newReedNotHeldMsg(requestID, reedID))
		}
		return
	}

	if err := rs.db.CreateForeignPendingEvent(context.Background(), eventID, homeServerID, peerEventID); err != nil {
		log.Error().Err(err).Msg("Failed to record foreign pending event mapping")
		if delErr := rs.deletePendingEvent(context.Background(), eventID); delErr != nil {
			log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete pending event after foreign_pending_events insert failure")
		}
		return
	}

	rs.connManager.SendToUser(client.userID, newRequestAckMsg(requestID, eventID, reedID))
}

// registerAndRecordForeignRelay is the shared body behind both
// HandleForeignRequestReed (leg 1: a peer asks the reed's true home for
// its own content) and HandleForeignFallbackRequest (a peer we notified
// asks us, the true home, to relay back a copy after finding no local
// holder). Both cases run the same registerReedRequest sequence a local
// requester would, with an empty FK-column requester (no local
// online_users row can back a foreign requester) so the rest of the
// local relay-holder machinery (dispatchNext, handleRelayResponse, etc.)
// needs no special-casing to handle either.
func (rs *realtimeService) registerAndRecordForeignRelay(ctx context.Context, reedID, requestingServerID, requestingUserID string) (result realtimeForeignRequestResult, peerEventID string, err error) {
	// requestID here is our own local pending_events.request_id bookkeeping
	// value, not the peer's own request id (that's recorded separately
	// below) — but it still inherits the ORIGINAL remote requester's
	// identity, not a placeholder, so any downstream code that ever
	// surfaces it (logging, future features) reflects who actually asked,
	// not this server's internal bookkeeping stand-in.
	requestID := generateRealtimeEventID(requestingUserID)
	exists, hasHolders, holderOnline, eventID, err := rs.registerReedRequest(ctx, reedID, "", requestingUserID, requestID, false, requestReedEvent)
	if err != nil {
		return realtimeForeignRequestReedNotFound, "", err
	}
	if !exists {
		return realtimeForeignRequestReedNotFound, "", nil
	}
	if !hasHolders {
		return realtimeForeignRequestReedNotHeld, "", nil
	}

	if err := rs.recordForeignRelayRequest(ctx, eventID, requestingServerID, requestingUserID); err != nil {
		return realtimeForeignRequestReedNotFound, "", err
	}

	if !holderOnline {
		return realtimeForeignRequestAccepted, eventID, nil
	}
	return realtimeForeignRequestOK, eventID, nil
}

// HandleForeignRequestReed is leg 1's home-server-side logic: a peer is
// registering a REQUEST_REED on behalf of one of its own users, for
// content this server actually authors/owns.
func (rs *realtimeService) HandleForeignRequestReed(ctx context.Context, canonicalReedID, requestingServerID, requestingUserID, peerRequestID string) (result realtimeForeignRequestResult, peerEventID string, err error) {
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
func (rs *realtimeService) HandleForeignFallbackRequest(ctx context.Context, reedID, requestingServerID, requestingUserID, peerRequestID string) (result realtimeForeignRequestResult, peerEventID string, err error) {
	return rs.registerAndRecordForeignRelay(ctx, reedID, requestingServerID, requestingUserID)
}

// HandleHolderNotify records, on reedID's home server, that
// notifyingServerID holds a verified copy — the fact a later local
// REQUEST_REED with no online local holder falls back on
// (tryPeerFallback). Fire-and-forget from the caller's side; idempotent
// here (RecordServerHolder's own ON CONFLICT DO NOTHING).
func (rs *realtimeService) HandleHolderNotify(ctx context.Context, reedID, notifyingServerID string) error {
	if err := rs.db.UpsertReedIdentity(ctx, reedID); err != nil {
		return err
	}
	return rs.db.RecordServerHolder(ctx, reedID, notifyingServerID)
}

// NotifyMailboxMessage attempts live WS delivery of a just-stored
// user_mailbox row. Called right after the caller's own SendMailboxMessage
// insert commits — the encrypt+insert happens outside this file (needs
// cryptoService + DataService), so this is just the live-send half. If the
// recipient isn't connected, nothing else happens here; the row stays and
// reaches them via catchUp on reconnect.
func (rs *realtimeService) NotifyMailboxMessage(userID, id, ciphertext string) {
	if err := rs.connManager.SendToUser(userID, newMailboxMsg(userID, id, ciphertext)); err != nil {
		log.Debug().Err(err).Str("userID", userID).Str("mailboxID", id).Msg("Mailbox recipient not connected, will deliver on catch-up")
	}
}

// recordForeignRelayRequest inserts eventID's foreign_relay_requests row,
// rolling back the speculative pending_events row on failure — shared by
// HandleForeignRequestReed and HandleForeignSubscribeProfile.
func (rs *realtimeService) recordForeignRelayRequest(ctx context.Context, eventID, requestingServerID, requestingUserID string) error {
	if err := rs.db.CreateForeignRelayRequest(ctx, eventID, requestingServerID, requestingUserID); err != nil {
		if delErr := rs.deletePendingEvent(ctx, eventID); delErr != nil {
			log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete pending event after foreign_relay_requests insert failure")
		}
		return err
	}
	return nil
}

// realtimeForeignSubscribeProfileResult is one reed of a foreign author's
// unallocated-reeds backfill, registered with the home server (leg 1's
// profile-level sibling).
type realtimeForeignSubscribeProfileResult struct {
	PeerEventID string
	ReedID      string
}

// realtimeForeignSubscribeProfileHook registers requesterUserID for live
// fanout of authorID's (foreign) future reeds with authorID's home server
// over peer HTTP.
type realtimeForeignSubscribeProfileHook func(ctx context.Context, authorID, requesterUserID string) error

// SetForeignSubscribeProfileHook installs the profile live-fanout registration hook.
func (rs *realtimeService) SetForeignSubscribeProfileHook(hook realtimeForeignSubscribeProfileHook) {
	rs.foreignSubscribeProfileHook = hook
}

// realtimeForeignProfilePageHook fetches one page of a foreign author's
// reeds from their home server, with that page's count and hasMore.
type realtimeForeignProfilePageHook func(ctx context.Context, authorID, requesterUserID string, page int) ([]realtimeForeignSubscribeProfileResult, int, bool, error)

// SetForeignProfilePageHook installs the profile history-page hook.
func (rs *realtimeService) SetForeignProfilePageHook(hook realtimeForeignProfilePageHook) {
	rs.foreignProfilePageHook = hook
}

// HandleForeignSubscribeProfile registers a peer's viewer for live fanout
// of authorID's future reeds; authorID must be local here. History is
// pulled separately by HandleForeignProfilePage.
func (rs *realtimeService) HandleForeignSubscribeProfile(ctx context.Context, authorID, requestingServerID, requestingUserID string) error {
	// Without this row, fanoutNewReedCore's GetProfileSubscribers(authorID)
	// never sees this peer's viewer. viewer_user_id is the real remote user,
	// so each foreign viewer fans out individually like a local one would.
	if err := rs.db.UpsertRemoteIdentity(ctx, requestingUserID, requestingServerID); err != nil {
		return err
	}
	if _, err := rs.db.CreateProfileSubscription(ctx, generateRealtimeEventID(requestingUserID), requestingUserID, authorID); err != nil {
		return err
	}
	return nil
}

// HandleForeignProfilePage registers relay requests for one page of a local
// author's reeds for a peer's viewer. count and hasMore describe the page
// itself, so they survive whatever the subtraction and skips below drop.
func (rs *realtimeService) HandleForeignProfilePage(ctx context.Context, authorID, requestingServerID, requestingUserID string, page int) (results []realtimeForeignSubscribeProfileResult, count int, hasMore bool, err error) {
	if err := rs.db.UpsertRemoteIdentity(ctx, requestingUserID, requestingServerID); err != nil {
		return nil, 0, false, err
	}

	reedIDs, hasMore, err := rs.db.GetAuthorReedPage(ctx, authorID, page, profilePageSize)
	if err != nil {
		return nil, 0, false, err
	}
	count = len(reedIDs)

	missing, err := rs.db.SubtractServerAllocatedReeds(ctx, reedIDs, requestingServerID)
	if err != nil {
		return nil, 0, false, err
	}

	for _, reedID := range missing {
		requestID := generateRealtimeEventID(requestingUserID)
		exists, hasHolders, _, eventID, err := rs.registerReedRequest(ctx, reedID, "", requestingUserID, requestID, false, requestReedEvent)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to register reed for foreign profile page")
			continue
		}
		if !exists || !hasHolders {
			continue
		}
		if err := rs.recordForeignRelayRequest(ctx, eventID, requestingServerID, requestingUserID); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to record foreign relay request for profile page")
			continue
		}
		results = append(results, realtimeForeignSubscribeProfileResult{PeerEventID: eventID, ReedID: reedID})
	}

	return results, count, hasMore, nil
}

func (rs *realtimeService) handleSubscribeProfile(client *realtimeClient, userID string) {
	if userID == "" {
		return
	}

	if _, err := rs.db.CreateProfileSubscription(context.Background(), generateRealtimeEventID(client.userID), client.userID, userID); err != nil {
		log.Error().Err(err).Msg("Failed to create profile subscription")
		return
	}

	if foreign, _ := rs.isForeignReed(userID); foreign {
		rs.handleForeignSubscribeProfileFromClient(client, userID)
	}
}

// handleProfilePage registers relay requests for one page of an author's
// reed history. Independent of any profile subscription: the events carry
// no subscription id and survive the viewer navigating away.
func (rs *realtimeService) handleProfilePage(client *realtimeClient, userID string, page uint32) {
	if userID == "" {
		return
	}
	if page < 1 {
		page = 1
	}

	if foreign, homeServerID := rs.isForeignReed(userID); foreign {
		rs.handleForeignProfilePageFromClient(client, userID, homeServerID, page)
		return
	}

	reedIDs, hasMore, err := rs.db.GetAuthorReedPage(context.Background(), userID, int(page), profilePageSize)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get author reed page")
		return
	}

	// Acked before the work below so the client's pagination state never
	// waits on however many events this page turns out to need.
	rs.connManager.SendToUser(client.userID, newPageAckMsg(userID, page, uint32(len(reedIDs)), hasMore))

	missing, err := rs.db.SubtractHeldReeds(context.Background(), reedIDs, client.userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to subtract held reeds for viewer")
		return
	}

	for _, reedID := range missing {
		eventID := generateRealtimeEventID(client.userID)
		requestID := generateRealtimeEventID(client.userID)
		if err := rs.createPendingReedEvent(context.Background(), eventID, requestID, client.userID, profileSubscriptionEvent, reedID); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to create profile page event")
			continue
		}
		holder, err := rs.db.GetOnlineReedHolder(context.Background(), reedID)
		if err != nil || holder == "" {
			continue
		}
		rs.dispatchNextIfConnected(holder)
	}
}

// handleForeignSubscribeProfileFromClient is handleSubscribeProfile's
// foreign branch: register this viewer for live fanout with authorID's
// home server. History comes separately via handleForeignProfilePageFromClient.
func (rs *realtimeService) handleForeignSubscribeProfileFromClient(client *realtimeClient, authorID string) {
	if rs.foreignSubscribeProfileHook == nil {
		return
	}

	if err := rs.foreignSubscribeProfileHook(context.Background(), authorID, client.userID); err != nil {
		log.Error().Err(err).Str("authorID", authorID).Msg("Failed to register foreign profile subscription")
	}
}

// handleForeignProfilePageFromClient is handleProfilePage's foreign branch:
// ask authorID's home server for one page of their reeds, and register a
// local pending event (plus its foreign_pending_events mapping) per result.
func (rs *realtimeService) handleForeignProfilePageFromClient(client *realtimeClient, authorID, homeServerID string, page uint32) {
	if rs.foreignProfilePageHook == nil {
		return
	}

	results, count, hasMore, err := rs.foreignProfilePageHook(context.Background(), authorID, client.userID, int(page))
	if err != nil {
		log.Error().Err(err).Str("authorID", authorID).Str("homeServerID", homeServerID).Msg("Failed to fetch foreign profile page")
		return
	}

	// The home server's own page numbers, not len(results) — results is
	// already net of what this server holds.
	rs.connManager.SendToUser(client.userID, newPageAckMsg(authorID, page, uint32(count), hasMore))

	for _, result := range results {
		rs.registerForeignProfilePageEvent(context.Background(), client.userID, homeServerID, result.ReedID, result.PeerEventID)
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
func (rs *realtimeService) registerForeignProfileSubscriptionEvent(ctx context.Context, requesterUserID, homeServerID, subscriptionID, reedID, peerEventID string) {
	if err := rs.db.UpsertReedIdentity(ctx, reedID); err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to upsert reed identity for foreign profile subscription")
		return
	}
	eventID := generateRealtimeEventID(requesterUserID)
	requestID := generateRealtimeEventID(requesterUserID)
	if err := rs.createProfileSubscriptionEvent(ctx, eventID, requestID, requesterUserID, profileSubscriptionEvent, reedID, subscriptionID); err != nil {
		log.Error().Err(err).Str("peerEventID", peerEventID).Msg("Failed to create local pending event for foreign profile subscription")
		return
	}
	if err := rs.db.CreateForeignPendingEvent(ctx, eventID, homeServerID, peerEventID); err != nil {
		log.Error().Err(err).Msg("Failed to record foreign pending event mapping for profile subscription")
		if delErr := rs.deletePendingEvent(ctx, eventID); delErr != nil {
			log.Error().Err(delErr).Str("eventID", eventID).Msg("Failed to delete pending event after foreign_pending_events insert failure")
		}
	}
}

// registerForeignProfilePageEvent is registerForeignProfileSubscriptionEvent
// for a history page: the event carries no subscription id, so it is not
// cascade-deleted when the viewer unsubscribes.
func (rs *realtimeService) registerForeignProfilePageEvent(ctx context.Context, requesterUserID, homeServerID, reedID, peerEventID string) {
	if err := rs.db.UpsertReedIdentity(ctx, reedID); err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to upsert reed identity for foreign profile page")
		return
	}
	eventID := generateRealtimeEventID(requesterUserID)
	requestID := generateRealtimeEventID(requesterUserID)
	if err := rs.createPendingReedEvent(ctx, eventID, requestID, requesterUserID, profileSubscriptionEvent, reedID); err != nil {
		log.Error().Err(err).Str("peerEventID", peerEventID).Msg("Failed to create local pending event for foreign profile page")
		return
	}
	if err := rs.db.CreateForeignPendingEvent(ctx, eventID, homeServerID, peerEventID); err != nil {
		log.Error().Err(err).Msg("Failed to record foreign pending event mapping for profile page")
		if delErr := rs.deletePendingEvent(ctx, eventID); delErr != nil {
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
func (rs *realtimeService) HandleForeignNewReedNotify(ctx context.Context, authorID, homeServerID, requesterUserID, reedID, peerEventID string) (found bool, err error) {
	subscriptionID, err := rs.db.GetProfileSubscription(ctx, requesterUserID, authorID)
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

// HandleForeignReplyRemovalNotify runs on the viewer's own server: a peer
// (the parent reed's home server) is telling us one of our local users had
// a reply removed from a thread they're watching. Delivers exactly like a
// local removal via the existing dispatchRemovalTo — the viewer's client
// can't tell the difference.
func (rs *realtimeService) HandleForeignReplyRemovalNotify(ctx context.Context, viewerUserID, removedReedID string, cert *reedRemovalWire) {
	rs.dispatchRemovalTo(viewerUserID, removedReedID, cert)
}

// HandleForeignAccountRemoval runs on a peer holding removedUserID's
// content: the cert is already stored, so just run the same local fanout
// a same-server account removal gets (followers, broadcast, profile
// subscribers) — a local recipient can't tell the difference.
func (rs *realtimeService) HandleForeignAccountRemoval(removedUserID string, cert *accountRemovalWire) {
	rs.fanoutAccountRemoval(removedUserID, cert)
}

// HandleForeignReedRemoval runs on a peer holding reedID's content: the
// cert is already stored, so just run the same local fanout a same-server
// reed removal gets — a local recipient can't tell the difference.
func (rs *realtimeService) HandleForeignReedRemoval(authorUserID, reedID string, cert *reedRemovalWire) {
	rs.fanoutReedRemoval(authorUserID, reedID, cert)
}

func (rs *realtimeService) handleUnsubscribeProfile(client *realtimeClient, userID string) {
	if userID == "" {
		return
	}

	subscriptionID, err := rs.db.GetProfileSubscription(context.Background(), client.userID, userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get profile subscription")
		return
	}
	if subscriptionID == "" {
		return
	}

	if err := rs.db.DeleteProfileSubscription(context.Background(), subscriptionID); err != nil {
		log.Error().Err(err).Str("subscriptionID", subscriptionID).Msg("Failed to delete profile subscription")
	}

	if foreign, homeServerID := rs.isForeignReed(userID); foreign && rs.foreignUnsubscribeProfileHook != nil {
		if err := rs.foreignUnsubscribeProfileHook(context.Background(), userID, client.userID); err != nil {
			log.Error().Err(err).Str("authorID", userID).Str("homeServerID", homeServerID).Msg("Failed to notify home server of profile unsubscribe")
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
func (rs *realtimeService) HandleForeignUnsubscribeProfile(ctx context.Context, authorID, requestingUserID string) error {
	subscriptionID, err := rs.db.GetProfileSubscription(ctx, requestingUserID, authorID)
	if err != nil {
		return err
	}
	if subscriptionID == "" {
		return nil
	}
	return rs.db.DeleteProfileSubscription(ctx, subscriptionID)
}

// realtimeForeignReedStatsSnapshot is the initial stats snapshot returned
// by a foreign reed's home server when a peer registers a live stats
// subscription (leg 8).
type realtimeForeignReedStatsSnapshot struct {
	Echoes          int
	CoveragePercent int
	Replies         int
	Likes           int
}

// realtimeForeignSubscribeReedHook registers requesterUserID's interest in
// reedID's live stats with reedID's home server, returning the current
// snapshot to seed the initial REED_STATS the same way a local subscribe
// would. ok=false means the home server reports the reed doesn't exist
// (mirrors the local ReedExists guard).
type realtimeForeignSubscribeReedHook func(ctx context.Context, reedID, requesterUserID string) (snapshot realtimeForeignReedStatsSnapshot, ok bool, err error)

// SetForeignSubscribeReedHook installs the leg-8 (subscribe-reed) hook.
func (rs *realtimeService) SetForeignSubscribeReedHook(hook realtimeForeignSubscribeReedHook) {
	rs.foreignSubscribeReedHook = hook
}

// HandleForeignSubscribeReed is leg 8's home-server logic: a peer's
// viewer wants live stats for one of this server's reeds. Registers a
// durable reed_subscriptions row keyed by the real remote viewer (so
// future stat-change fanout finds them, mirroring
// HandleForeignSubscribeProfile's own durable registration) and returns
// the current snapshot to seed the peer's initial REED_STATS. ok=false
// means the reed doesn't exist (mirrors the local ReedExists guard in
// handleSubscribeReed).
func (rs *realtimeService) HandleForeignSubscribeReed(ctx context.Context, reedID, requestingServerID, requestingUserID string) (snapshot realtimeForeignReedStatsSnapshot, ok bool, err error) {
	exists, err := rs.db.ReedExists(ctx, reedID)
	if err != nil {
		return realtimeForeignReedStatsSnapshot{}, false, err
	}
	if !exists {
		return realtimeForeignReedStatsSnapshot{}, false, nil
	}

	if err := rs.db.UpsertRemoteIdentity(ctx, requestingUserID, requestingServerID); err != nil {
		return realtimeForeignReedStatsSnapshot{}, false, err
	}
	if err := rs.db.CreateReedSubscription(ctx, generateRealtimeEventID(requestingUserID), requestingUserID, reedID); err != nil {
		return realtimeForeignReedStatsSnapshot{}, false, err
	}

	echoes, coveragePct, replies, likes, err := rs.db.GetReedStatsSnapshot(ctx, reedID)
	if err != nil {
		return realtimeForeignReedStatsSnapshot{}, false, err
	}
	return realtimeForeignReedStatsSnapshot{
		Echoes:          echoes,
		CoveragePercent: coveragePct,
		Replies:         replies,
		Likes:           likes,
	}, true, nil
}

// HandleForeignUnsubscribeReed is leg 9's home-server logic: a peer's
// viewer no longer wants live stats for reedID. Deletes this server's own
// reed_subscriptions row for that (viewer, reed) pair.
func (rs *realtimeService) HandleForeignUnsubscribeReed(ctx context.Context, reedID, requestingUserID string) error {
	subscriptionID, err := rs.db.GetReedSubscription(ctx, requestingUserID, reedID)
	if err != nil {
		return err
	}
	if subscriptionID == "" {
		return nil
	}
	return rs.db.DeleteReedSubscription(ctx, subscriptionID)
}

// HandleForeignReplyNotify is leg 11's home-server logic: a peer is
// telling us replyReedID (authored on their server) replies to
// parentReedID, one of our own reeds. Records the reference and runs the
// exact same local fanout a purely-local reply already gets — reply-count
// updates up the ancestor chain, plus content delivery to anyone
// subscribed to the thread (which, per notifyReedSubscribersOfReply's own
// foreign-aware branch, may itself reach further peers).
func (rs *realtimeService) HandleForeignReplyNotify(ctx context.Context, parentReedID, replyReedID, threadID string, ts time.Time) error {
	exists, err := rs.db.ReedExists(ctx, parentReedID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("parent reed not found: %s", parentReedID)
	}

	if err := rs.db.InsertForeignReply(ctx, parentReedID, replyReedID, threadID, ts); err != nil {
		return err
	}

	reedID := parentReedID
	for {
		rs.notifyReedReplies(reedID)
		rs.notifyReedSubscribersOfReply(reedID, replyReedID)
		nextReedID, ok, err := rs.db.ReplyParent(ctx, reedID)
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
// stats update (leg 10). payload is a JSON string of the base64 bytes of
// the protobuf WSMessage notifyForeignReedSubscribers marshaled on H's side.
func (rs *realtimeService) DeliverForeignReedStats(ctx context.Context, requesterUserID string, payload json.RawMessage) error {
	var raw []byte
	if err := json.Unmarshal(payload, &raw); err != nil {
		return fmt.Errorf("failed to decode foreign reed stats payload: %w", err)
	}
	var msg pb.WSMessage
	if err := proto.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal foreign reed stats payload: %w", err)
	}
	return rs.connManager.SendToUser(requesterUserID, &msg)
}

func (rs *realtimeService) handleSubscribeReed(client *realtimeClient, reedID string) {
	if reedID == "" {
		return
	}

	// Durable registration first (mirrors handleSubscribeProfile): survives
	// a reconnect and, for a foreign reed, is what lets the home server's
	// fanout find this viewer at all — connManager's map is in-memory only
	// and never leaves this process.
	subscriptionID := generateRealtimeEventID(client.userID)
	if err := rs.db.CreateReedSubscription(context.Background(), subscriptionID, client.userID, reedID); err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to create reed subscription")
		return
	}

	if foreign, homeServerID := rs.isForeignReed(reedID); foreign {
		rs.handleForeignSubscribeReedFromClient(client, reedID, homeServerID)
		return
	}

	exists, err := rs.db.ReedExists(context.Background(), reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to check reed for subscribe")
		return
	}
	if !exists {
		return
	}

	echoes, coveragePct, replies, likes, err := rs.db.GetReedStatsSnapshot(context.Background(), reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to load reed stats for subscribe")
		return
	}

	stats := newReedStatsMsg(reedID, echoes, coveragePct, replies, likes)
	if err := rs.connManager.SendToClient(client, stats); err != nil {
		log.Error().Err(err).Str("userID", client.userID).Str("reedID", reedID).Msg("Failed to send REED_STATS")
	}
}

// handleForeignSubscribeReedFromClient is handleSubscribeReed's foreign
// branch: register live-stats interest with reedID's home server and
// relay back the initial snapshot. The local reed_subscriptions row was
// already created by the caller — this only handles the peer round trip
// and the client's initial REED_STATS.
func (rs *realtimeService) handleForeignSubscribeReedFromClient(client *realtimeClient, reedID, homeServerID string) {
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

	stats := newReedStatsMsg(reedID, snapshot.Echoes, snapshot.CoveragePercent, snapshot.Replies, snapshot.Likes)
	if err := rs.connManager.SendToClient(client, stats); err != nil {
		log.Error().Err(err).Str("userID", client.userID).Str("reedID", reedID).Msg("Failed to send REED_STATS for foreign reed")
	}
}

func (rs *realtimeService) handleUnsubscribeReed(client *realtimeClient, reedID string) {
	if reedID == "" {
		return
	}
	subscriptionID, err := rs.db.GetReedSubscription(context.Background(), client.userID, reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to get reed subscription")
		return
	}
	if subscriptionID == "" {
		return
	}
	if err := rs.db.DeleteReedSubscription(context.Background(), subscriptionID); err != nil {
		log.Error().Err(err).Str("subscriptionID", subscriptionID).Msg("Failed to delete reed subscription")
	}

	if foreign, homeServerID := rs.isForeignReed(reedID); foreign && rs.foreignUnsubscribeReedHook != nil {
		if err := rs.foreignUnsubscribeReedHook(context.Background(), reedID, client.userID); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("homeServerID", homeServerID).Msg("Failed to notify home server of reed stats unsubscribe")
		}
	}
}

func (rs *realtimeService) handleSubscribePipe(client *realtimeClient, rawTag string) {
	tag := normalizePipeTag(rawTag)
	if tag == "" {
		return
	}
	if err := rs.db.SubscribePipe(context.Background(), client.userID, tag); err != nil {
		log.Error().Err(err).Str("userID", client.userID).Str("tag", tag).Msg("Failed to subscribe to pipe")
		return
	}
	log.Debug().Str("userID", client.userID).Str("tag", tag).Msg("Client subscribed to pipe")
}

func (rs *realtimeService) handleUnsubscribePipe(client *realtimeClient, rawTag string) {
	tag := normalizePipeTag(rawTag)
	if tag == "" {
		return
	}
	if err := rs.db.UnsubscribePipe(context.Background(), client.userID, tag); err != nil {
		log.Error().Err(err).Str("userID", client.userID).Str("tag", tag).Msg("Failed to unsubscribe from pipe")
		return
	}
	log.Debug().Str("userID", client.userID).Str("tag", tag).Msg("Client unsubscribed from pipe")
}

// FilterSubscribedPipeTags returns extracted tags that currently have ≥1 listener.
// Used by SignReed to stash only relevant tags on pending_fanout.
func (rs *realtimeService) FilterSubscribedPipeTags(tags []string) []string {
	live, err := rs.db.GetTagsWithListeners(context.Background(), normalizePipeTags(tags))
	if err != nil {
		log.Error().Err(err).Msg("Failed to filter pipe tags with listeners")
		return nil
	}
	return live
}

// normalizePipeTags maps normalizePipeTag over tags, dropping empties.
// Tags are always normalized before they reach SQL, never inside it.
func normalizePipeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		if tag := normalizePipeTag(raw); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

// notifyForeignReedSubscribers pushes msg (the same protobuf WSMessage a
// local reed-stat subscriber would receive over WS) to every foreign
// viewer durably registered in reed_subscriptions for reedID. Local
// delivery is unaffected — this only covers viewers homed on another
// server, which this server's own fanout never reaches.
// Best-effort: a failed peer push only means
// one viewer misses one update, never retried — same tolerance every
// other live WS fanout already has for a client that's simply offline.
func (rs *realtimeService) notifyForeignReedSubscribers(reedID string, msg *pb.WSMessage) {
	rs.notifyForeignReedSubscribersExcept(reedID, "", msg)
}

// notifyForeignReedSubscribersExcept is notifyForeignReedSubscribers with
// one viewer skipped — mirrors sendToReedSubscribersExceptAuthor's own
// exclude param, used by the ripple-push sites so a ripple's own author
// doesn't get their own content echoed back to another of their devices.
func (rs *realtimeService) notifyForeignReedSubscribersExcept(reedID, excludeUserID string, msg *pb.WSMessage) {
	if rs.foreignReedStatsHook == nil {
		return
	}
	subs, err := rs.db.GetReedSubscribers(context.Background(), reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to load reed subscribers for foreign stats push")
		return
	}
	if len(subs) == 0 {
		return
	}
	raw, err := marshalWSMessage(msg)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to marshal reed stats push payload")
		return
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to encode reed stats push payload")
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

func (rs *realtimeService) notifyReedCoverage(reedID string) {
	authorUserID := reedAuthorIdentity(reedID)
	exists, err := rs.db.ReedExists(context.Background(), reedID)
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

	holders, percent, err := rs.db.GetReedCoverage(context.Background(), reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load reed coverage for notify")
		return
	}

	rs.metrics.ReedCoverage(context.Background(), authorUserID, reedID, holders, percent)

	msg := newReedCoverageMsg(reedID, percent)
	if err := rs.BroadcastReedCoverage(reedID, msg); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_COVERAGE")
	}
	rs.notifyForeignReedSubscribers(reedID, msg)
}

func (rs *realtimeService) notifyReedEchoes(reedID string) {
	authorUserID := reedAuthorIdentity(reedID)
	echoes, err := rs.db.CountEchoes(context.Background(), reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load reed echoes for notify")
		return
	}

	msg := newReedEchoesMsg(reedID, echoes)
	if err := rs.sendToReedSubscribers(reedID, "", msg); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_ECHOES")
	}
	rs.notifyForeignReedSubscribers(reedID, msg)
}

// notifyParentSubscribersOfReply relays replyReedID to its direct parent's
// reed-stat subscribers only — not the whole ancestor chain. Only called
// from handlePublishReady, once the reply's author has actually claimed
// PUBLISH_READY — calling this any earlier (e.g. straight from SignReed)
// makes the author a relay target before their own client is ready to
// serve it, and the resulting relay miss deletes their allocation,
// orphaning the reed from relay entirely.
func (rs *realtimeService) notifyParentSubscribersOfReply(replyReedID string) {
	parentReedID, ok, err := rs.db.ReplyParent(context.Background(), replyReedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", replyReedID).Msg("Failed to resolve reply parent")
		return
	}
	if !ok {
		return
	}
	rs.notifyReedSubscribersOfReply(parentReedID, replyReedID)
}

// notifyForeignParentOfReply tells replyReedID's direct parent's home
// server about the reply, if that parent is foreign — the write side of
// cross-server replying. A no-op for a purely local reply.
func (rs *realtimeService) notifyForeignParentOfReply(replyReedID string) {
	if rs.foreignReplyNotifyHook == nil {
		return
	}
	rec, err := rs.db.GetReplyRecord(context.Background(), replyReedID)
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

// notifyMentionedUsers pushes reedID to each online local user it
// mentions. An offline recipient is picked up by catchUp instead.
func (rs *realtimeService) notifyMentionedUsers(reedID string) {
	mentionedUserIDs, err := rs.db.GetOnlineMentionedUsers(context.Background(), reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Msg("Failed to load mentions for notify")
		return
	}
	if len(mentionedUserIDs) > 0 {
		rs.dispatchMany(mentionedUserIDs, mentionEvent, reedID)
	}
}

// notifyParentAuthorOfReply pushes replyReedID to its direct parent's
// online author (skip on self-reply), unless they're already a thread
// subscriber — notifyReedSubscribersOfReply covers that case already.
func (rs *realtimeService) notifyParentAuthorOfReply(replyReedID string) {
	parentReedID, ok, err := rs.db.ReplyParent(context.Background(), replyReedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", replyReedID).Msg("Failed to resolve reply parent for author notify")
		return
	}
	if !ok {
		return
	}
	parentAuthorID := reedAuthorIdentity(parentReedID)
	replyAuthorID := reedAuthorIdentity(replyReedID)
	if parentAuthorID == "" || parentAuthorID == replyAuthorID {
		return
	}
	if !rs.isUserOnline(parentAuthorID) {
		return
	}
	for _, sub := range rs.reedSubscriberUserIDs(parentReedID, replyAuthorID) {
		if sub == parentAuthorID {
			return
		}
	}
	rs.dispatchMany([]string{parentAuthorID}, reedReplyEvent, replyReedID)
}

// notifyReplyAncestorsOfRemoval walks removedReedID's ancestor chain,
// delivering the removal cert to each ancestor's reed-stat subscribers —
// server-stored, so this can run inline, no relay race to worry about.
func (rs *realtimeService) notifyReplyAncestorsOfRemoval(removedReedID string, cert *reedRemovalWire) {
	reedID := removedReedID
	for {
		parentReedID, ok, err := rs.db.ReplyParent(context.Background(), reedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to resolve reply parent for removal")
			return
		}
		if !ok {
			return
		}
		recipients := rs.reedSubscriberUserIDs(parentReedID, "")
		rs.dispatchRemovalMany(recipients, removedReedID, cert)
		rs.notifyForeignReplyAncestorsOfRemoval(parentReedID, removedReedID, cert)
		reedID = parentReedID
	}
}

// notifyForeignReplyAncestorsOfRemoval is notifyReplyAncestorsOfRemoval's
// foreign-viewer half — connManager only sees this server's own live
// connections, so a viewer on a peer server with parentReedID's thread
// open needs a separate signed notify to their home server.
func (rs *realtimeService) notifyForeignReplyAncestorsOfRemoval(parentReedID, removedReedID string, cert *reedRemovalWire) {
	if rs.foreignReplyRemovalToViewerHook == nil {
		return
	}
	subs, err := rs.db.GetReedSubscribers(context.Background(), parentReedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", parentReedID).Msg("Failed to load reed subscribers for foreign removal notify")
		return
	}
	for _, sub := range subs {
		foreign, _ := rs.isForeignReed(sub.ViewerUserID)
		if !foreign {
			continue
		}
		if err := rs.foreignReplyRemovalToViewerHook(context.Background(), sub.ViewerUserID, removedReedID, cert); err != nil {
			log.Error().Err(err).Str("viewerUserID", sub.ViewerUserID).Str("removedReedID", removedReedID).Msg("Failed to notify foreign viewer of reply removal")
		}
	}
}

// HandleForeignReplyRemovalAtParent is leg 13's home-server logic (H, the
// parentReedID's home server): a peer already deleted its own copy of
// removedReedID and its local reed_replies row; the durable subscriber data
// for parentReedID's thread only ever lives here on H (reed_subscriptions
// rows are created by HandleForeignSubscribeReed, which only runs on a
// reed's own home server), so H — not the removing peer — is the only
// server that can find and notify parentReedID's viewers, local or foreign.
// Mirrors notifyReplyAncestorsOfRemoval's own body, just starting one level
// up since removedReedID itself isn't hosted here.
func (rs *realtimeService) HandleForeignReplyRemovalAtParent(parentReedID, removedReedID string, cert *reedRemovalWire) {
	reedID := parentReedID
	for {
		recipients := rs.reedSubscriberUserIDs(reedID, "")
		rs.dispatchRemovalMany(recipients, removedReedID, cert)
		rs.notifyForeignReplyAncestorsOfRemoval(reedID, removedReedID, cert)
		nextReedID, ok, err := rs.db.ReplyParent(context.Background(), reedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to resolve reply ancestor for foreign reply removal")
			return
		}
		if !ok {
			return
		}
		reedID = nextReedID
	}
}

// notifyReedSubscribersOfReply relays a newly posted reply's content to
// everyone subscribed to ancestorReedID — someone viewing that reed's
// thread, not necessarily following the reply's author. The reply's own
// author is the content holder, same relay-through-a-peer mechanism as
// FOLLOW_REED/PIPE_REED (the server never stores reed content).
func (rs *realtimeService) notifyReedSubscribersOfReply(ancestorReedID, replyReedID string) {
	replyUserID := reedAuthorIdentity(replyReedID)
	recipients := rs.reedSubscriberUserIDs(ancestorReedID, replyUserID)
	if len(recipients) > 0 {
		rs.dispatchMany(recipients, reedReplyEvent, replyReedID)
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
func (rs *realtimeService) notifyForeignReedSubscribersOfReply(ancestorReedID, replyReedID, excludeUserID string) {
	if rs.foreignDeliverHook == nil {
		return
	}
	subs, err := rs.db.GetReedSubscribers(context.Background(), ancestorReedID)
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
		requestID := generateRealtimeEventID(sub.ViewerUserID)
		exists, hasHolders, _, eventID, err := rs.registerReedRequest(context.Background(), replyReedID, "", sub.ViewerUserID, requestID, false, reedReplyEvent)
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

func (rs *realtimeService) notifyReedReplies(reedID string) {
	authorUserID := reedAuthorIdentity(reedID)
	replies, err := rs.db.GetSubtreeReplyCount(context.Background(), reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load subtree replies for notify")
		return
	}

	msg := newReedRepliesMsg(reedID, replies)
	if err := rs.sendToReedSubscribers(reedID, "", msg); err != nil {
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
func (rs *realtimeService) notifyRipplePosted(reedID, rippleAuthorID string, ripple RippleWire) {
	authorUserID := reedAuthorIdentity(reedID)
	msg := newRipplePostedMsg(authorUserID, reedID, ripple)
	if err := rs.sendToReedSubscribers(reedID, rippleAuthorID, msg); err != nil {
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
func (rs *realtimeService) notifyRippleUpdated(reedID, rippleAuthorID string, ripple RippleWire) {
	authorUserID := reedAuthorIdentity(reedID)
	msg := newRippleUpdatedMsg(authorUserID, reedID, ripple)
	if err := rs.sendToReedSubscribers(reedID, rippleAuthorID, msg); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast RIPPLE_UPDATED")
	}
	rs.notifyForeignReedSubscribersExcept(reedID, rippleAuthorID, msg)
}

func (rs *realtimeService) notifyReedLikes(reedID string) {
	authorUserID := reedAuthorIdentity(reedID)
	likes, err := rs.db.CountLikes(context.Background(), reedID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", authorUserID).
			Str("reedID", reedID).
			Msg("Failed to load reed likes for notify")
		return
	}

	msg := newReedLikesMsg(reedID, likes)
	if err := rs.sendToReedSubscribers(reedID, "", msg); err != nil {
		log.Error().Err(err).Str("userID", authorUserID).Str("reedID", reedID).Msg("Failed to broadcast REED_LIKES")
	}
	rs.notifyForeignReedSubscribers(reedID, msg)
}

func (rs *realtimeService) handleSyncRequest(client *realtimeClient, requestID string) {
	if requestID == "" {
		return
	}
	if !rs.validateRequestID(requestID, client.userID) {
		rs.connManager.SendToUser(client.userID, newInvalidRequestIDErrorMsg(requestID))
		return
	}
	if err := rs.db.SetSyncRequestID(context.Background(), client.userID, requestID); err != nil {
		log.Error().Err(err).Msg("Failed to store sync request ID")
		return
	}
	rs.catchUp(client.userID, requestID)
	rs.dispatchNext(client.userID)
	rs.redispatchPendingRequests(client.userID)
}

func (rs *realtimeService) redispatchPendingRequests(requesterUserID string) {
	requests, err := rs.db.GetPendingRequestsForRequester(context.Background(), requesterUserID)
	if err != nil {
		log.Error().Err(err).Str("requesterUserID", requesterUserID).Msg("Failed to get pending requests for requester")
		return
	}
	for _, req := range requests {
		if err := rs.db.ResetDispatchedAt(context.Background(), req.EventID); err != nil {
			log.Error().Err(err).Str("eventID", req.EventID).Msg("Failed to reset dispatched_at for pending request")
			continue
		}
		holder, err := rs.db.GetOnlineReedHolder(context.Background(), req.ReedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", req.ReedID).Msg("Failed to get holder for pending request on reconnect")
			continue
		}
		if holder != "" {
			rs.dispatchNextIfConnected(holder)
		}
	}
}

func (rs *realtimeService) handleUserCameOnline(client *realtimeClient) {
	// Nothing sent until client signals readiness via SYNC_REQUEST
}

func (rs *realtimeService) catchUp(userID, requestID string) {
	unallocated, err := rs.db.GetMissingOut(context.Background(), userID)
	if err != nil {
		log.Error().
			Err(err).
			Str("userID", userID).
			Msg("Failed to get unallocated reeds from followings")
		return
	}

	for _, reed := range unallocated {
		eventID := generateRealtimeEventID(userID)
		if err := rs.createPendingReedEvent(context.Background(), eventID, requestID, userID, followReedEvent, reed.ReedID); err != nil {
			log.Error().
				Err(err).
				Str("reedID", reed.ReedID).
				Msg("Failed to create catch-up pending event")
			continue
		}
		holder, err := rs.db.GetOnlineReedHolder(context.Background(), reed.ReedID)
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

	missingMentions, err := rs.db.GetMissingMentions(context.Background(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get missing mentions")
		return
	}
	for _, reed := range missingMentions {
		eventID := generateRealtimeEventID(userID)
		if err := rs.createPendingReedEvent(context.Background(), eventID, requestID, userID, mentionEvent, reed.ReedID); err != nil {
			log.Error().Err(err).Str("reedID", reed.ReedID).Msg("Failed to create catch-up mention event")
			continue
		}
		holder, err := rs.db.GetOnlineReedHolder(context.Background(), reed.ReedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reed.ReedID).Msg("Failed to get online holder for catch-up mention")
			continue
		}
		if holder != "" {
			rs.dispatchNextIfConnected(holder)
		}
	}

	missingReplies, err := rs.db.GetMissingReplies(context.Background(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get missing replies")
		return
	}
	for _, reed := range missingReplies {
		eventID := generateRealtimeEventID(userID)
		if err := rs.createPendingReedEvent(context.Background(), eventID, requestID, userID, reedReplyEvent, reed.ReedID); err != nil {
			log.Error().Err(err).Str("reedID", reed.ReedID).Msg("Failed to create catch-up reply event")
			continue
		}
		holder, err := rs.db.GetOnlineReedHolder(context.Background(), reed.ReedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reed.ReedID).Msg("Failed to get online holder for catch-up reply")
			continue
		}
		if holder != "" {
			rs.dispatchNextIfConnected(holder)
		}
	}

	removals, err := rs.db.GetMissingRemovals(context.Background(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get missing reed removals")
		return
	}
	for _, rem := range removals {
		eventID := generateRealtimeEventID(userID)
		if err := rs.createPendingReedEvent(context.Background(), eventID, requestID, userID, reedRemovedEvent, rem.ReedID); err != nil {
			log.Error().Err(err).Str("reedID", rem.ReedID).Msg("Failed to create catch-up reed_removed event")
			continue
		}
		rs.deliverReedRemoved(eventID, requestID, userID, rem.ReedID, &rem.Cert)
	}

	accountRemovals, err := rs.db.GetMissingAccountRemovals(context.Background(), userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get missing account removals")
		return
	}
	for _, rem := range accountRemovals {
		eventID := generateRealtimeEventID(userID)
		if err := rs.createPendingAccountEvent(context.Background(), eventID, requestID, userID, rem.UserID); err != nil {
			log.Error().Err(err).Str("removedUserID", rem.UserID).Msg("Failed to create catch-up account_removed event")
			continue
		}
		rs.deliverAccountRemoved(eventID, requestID, userID, rem.UserID, &rem.Cert)
	}

	mailbox, err := GetPendingMailbox(context.Background(), rs.db.db, userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get pending mailbox messages")
		return
	}
	for _, m := range mailbox {
		if err := rs.connManager.SendToUser(userID, newMailboxMsg(userID, m.ID, m.Ciphertext)); err != nil {
			log.Error().Err(err).Str("userID", userID).Str("mailboxID", m.ID).Msg("Failed to send MAILBOX on catch-up")
		}
	}
}

// shouldBroadcastReed reports whether PUBLISH_READY should fan out to the
// broadcast stream: absent or true means include it, only explicit false opts out.
func shouldBroadcastReed(pr *pb.PublishReadyMessage) bool {
	return !pr.GetHasBroadcast() || pr.GetBroadcast()
}

// handlePublishReady runs new-reed fanout when a pending_fanout row exists.
func (rs *realtimeService) handlePublishReady(client *realtimeClient, reedID string, broadcast bool) {
	if reedID == "" {
		return
	}
	authorUserID := client.userID

	claimed, tags, err := rs.db.ClaimPendingFanout(context.Background(), reedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Str("userID", authorUserID).Msg("Failed to claim pending fanout")
		return
	}

	if claimed {
		var excludeFromFollowers []string
		if parentReedID, ok, err := rs.db.ReplyParent(context.Background(), reedID); err != nil {
			log.Error().Err(err).Str("reedID", reedID).Msg("Failed to resolve reply parent for follower exclusion")
		} else if ok {
			excludeFromFollowers = rs.reedSubscriberUserIDs(parentReedID, authorUserID)
		}

		if broadcast {
			go rs.fanoutNewReed(reedID, tags, excludeFromFollowers)
		} else {
			go rs.fanoutNewReedNoBroadcast(reedID, tags, excludeFromFollowers)
		}
		go rs.notifyParentSubscribersOfReply(reedID)
		go rs.notifyForeignParentOfReply(reedID)
		go rs.notifyParentAuthorOfReply(reedID)
		go rs.notifyMentionedUsers(reedID)
	} else {
		exists, err := rs.db.ReedExists(context.Background(), reedID)
		if err != nil {
			log.Error().Err(err).Str("reedID", reedID).Str("userID", authorUserID).Msg("Failed to check reed for publish ready ack")
			return
		}
		if !exists {
			return
		}
	}

	ack := &pb.WSMessage{
		Type: pb.MessageType_PUBLISH_READY_ACK,
		Payload: &pb.WSMessage_PublishReadyAck{
			PublishReadyAck: &pb.PublishReadyAckMessage{ReedId: reedID},
		},
	}
	rs.sendProtobufMessage(client, ack)
}

// handleEviction drops this client's allocation for a reed it is about to
// delete locally, then acks. The ack is unconditional: a retry after a
// lost ack must still be told it may proceed with the local delete.
func (rs *realtimeService) handleEviction(client *realtimeClient, reedID string) {
	if reedID == "" {
		return
	}

	changed, err := rs.db.DeleteReedAllocation(context.Background(), reedID, client.userID)
	if err != nil {
		log.Error().Err(err).Str("reedID", reedID).Str("userID", client.userID).Msg("Failed to clear allocation on eviction")
		return
	}
	if changed {
		rs.notifyReedCoverage(reedID)
	}

	ack := &pb.WSMessage{
		Type: pb.MessageType_EVICTION_ACK,
		Payload: &pb.WSMessage_EvictionAck{
			EvictionAck: &pb.EvictionAckMessage{ReedId: reedID},
		},
	}
	rs.sendProtobufMessage(client, ack)
}

func (rs *realtimeService) handleRelayResponse(client *realtimeClient, eventID, ciphertext string) {
	if eventID == "" {
		return
	}

	pe, err := rs.db.GetPendingReedEvent(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event")
		rs.dispatchN(client.userID, fanoutRefillBurst)
		return
	}
	if pe == nil {
		// Already resolved (e.g. a retransmit) — no duplicate "fulfilled"
		// sample, just advance the holder's queue.
		rs.dispatchN(client.userID, fanoutRefillBurst)
		return
	}

	rs.metrics.RelayEvent(context.Background(), metrics.RelayEventFulfilled, pe.EventName, eventID)

	if pe.EventName == string(broadcastReedEvent) {
		username, err := rs.db.GetRealtimeUsername(context.Background(), pe.UserID)
		if err != nil && err != sql.ErrNoRows {
			log.Error().Err(err).Str("userID", pe.UserID).Msg("Failed to load author username for broadcast reed")
		}
		if err != nil {
			log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Dropping broadcast reed: author account was either removed or never existed")
		} else {
			log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering broadcast reed to subscriber")
			rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, realtimeJSONString(ciphertext), func() *pb.WSMessage {
				return newBroadcastReedMsg(ciphertext, username, pe.ReedID)
			})
		}
		// Broadcast is ephemeral (no DATA_ACK expected) — delete right away,
		// unlike the deferred-until-ack pattern the branches below use.
		if err := rs.deletePendingEvent(context.Background(), eventID); err != nil {
			log.Error().Err(err).Str("eventID", eventID).Msg("Failed to delete pending event")
		}
	} else if pe.EventName == string(pipeReedEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering pipe reed to subscriber")
		rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, realtimeJSONString(ciphertext), func() *pb.WSMessage {
			return newPipeReedMsg(pe.EventID, pe.RequestID, ciphertext, pe.ReedID)
		})
	} else if pe.EventName == string(followReedEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering follow reed to subscriber")
		rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, realtimeJSONString(ciphertext), func() *pb.WSMessage {
			return newFollowReedMsg(pe.EventID, pe.RequestID, ciphertext, pe.ReedID)
		})
	} else if pe.EventName == string(archiveReedEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering archive reed to admin")
		rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, realtimeJSONString(ciphertext), func() *pb.WSMessage {
			return newArchiveReedMsg(pe.EventID, pe.RequestID, ciphertext, pe.ReedID)
		})
	} else if pe.EventName == string(reedReplyEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering reed reply to subscriber")
		rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, realtimeJSONString(ciphertext), func() *pb.WSMessage {
			return newReedReplyMsg(pe.EventID, pe.RequestID, ciphertext, pe.ReedID)
		})
	} else if pe.EventName == string(mentionEvent) {
		log.Info().Str("requesterID", pe.RequesterUserID).Str("reedID", pe.ReedID).Msg("Delivering mention to recipient")
		rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, realtimeJSONString(ciphertext), func() *pb.WSMessage {
			return newMentionMsg(pe.EventID, pe.RequestID, ciphertext, pe.ReedID)
		})
	} else {
		rs.deliverOrForward(context.Background(), eventID, pe.RequesterUserID, realtimeJSONString(ciphertext), func() *pb.WSMessage {
			return newDataResponseMsg(eventID, pe.RequestID, ciphertext, pe.ReedID)
		})
	}

	rs.dispatchN(client.userID, fanoutRefillBurst)
}

// deliverOrForward delivers relayed data for eventID either straight to a
// local requester's WebSocket, or — when requesterUserID is a per-peer
// sentinel with no real connection — over peer HTTP to whichever peer
// registered eventID via GetForeignRelayRequest. Every pending-reed-event
// type that can be requested on behalf of a foreign peer (PIPE_REED,
// FOLLOW_REED, REED_REPLY, and the generic REQUEST_REED/profile-subscribe
// case) needs this same check; buildLocalMsg is only invoked in the local
// case so callers don't pay for constructing a message that's discarded.
// data stays JSON — it crosses the (unmigrated) HTTP federation boundary,
// not the client-facing WS wire.
func (rs *realtimeService) deliverOrForward(ctx context.Context, eventID, requesterUserID string, data json.RawMessage, buildLocalMsg func() *pb.WSMessage) {
	frr, ferr := rs.db.GetForeignRelayRequest(ctx, eventID)
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

// handleRelayMiss: the holder truly lacks the content — drop their
// allocation, then retry via handleFailedRelay's shared shape.
func (rs *realtimeService) handleRelayMiss(holderUserID, eventID string) {
	rs.handleFailedRelay(holderUserID, eventID, true)
}

// handleRelayError: the holder has the content but couldn't relay it (a
// failed key fetch) — keep their allocation, retry via handleFailedRelay.
func (rs *realtimeService) handleRelayError(holderUserID, eventID string) {
	rs.handleFailedRelay(holderUserID, eventID, false)
}

// handleRelayTimeout: no response within dispatchRequestTimeout. Same
// shape as handleRelayError — silence proves nothing about the content.
func (rs *realtimeService) handleRelayTimeout(holderUserID, eventID string) {
	log.Warn().Str("eventID", eventID).Str("holderID", holderUserID).Msg("Relay request timed out; retrying")
	rs.handleFailedRelay(holderUserID, eventID, false)
}

// handleFailedRelay is the shared retry shape behind handleRelayMiss,
// handleRelayError, and handleRelayTimeout: reset dispatch, requery for a
// holder, retry or give up.
func (rs *realtimeService) handleFailedRelay(holderUserID, eventID string, deleteAllocation bool) {
	if eventID == "" {
		return
	}

	pe, err := rs.db.GetPendingReedEvent(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event for relay miss/error")
		rs.dispatchN(holderUserID, fanoutRefillBurst)
		return
	}
	if pe == nil {
		rs.dispatchN(holderUserID, fanoutRefillBurst)
		return
	}

	log.Info().
		Str("eventID", eventID).
		Str("holderID", holderUserID).
		Str("authorID", pe.UserID).
		Str("reedID", pe.ReedID).
		Bool("deleteAllocation", deleteAllocation).
		Msg("Relay miss/error; retrying")

	if deleteAllocation {
		if _, err := rs.db.DeleteReedAllocation(context.Background(), pe.ReedID, holderUserID); err != nil {
			log.Error().Err(err).Str("reedID", pe.ReedID).Str("holderID", holderUserID).Msg("Failed to delete allocation on relay miss")
		}
	}

	if err := rs.db.ResetDispatchedAt(context.Background(), eventID); err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to reset dispatched_at on relay miss/error")
	}

	hasHolders, holder, err := rs.db.GetOnlineHolders(context.Background(), pe.ReedID)
	if err != nil {
		log.Error().Err(err).Str("reedID", pe.ReedID).Msg("Failed to check reed holders on relay miss/error")
	} else if !hasHolders {
		rs.failReedNotHeld(pe)
	} else if holder != "" {
		rs.dispatchNextIfConnected(holder)
	}

	rs.dispatchN(holderUserID, fanoutRefillBurst)
}

// failReedNotHeld gives up on pe: no holder is left to relay it to. A local
// requester is notified directly over their own WebSocket. A foreign
// requester has no such connection here — this server's own bookkeeping is
// deleted first (this server's duty is done either way), then the
// requesting peer is notified best-effort via foreignNotHeldHook so their
// side doesn't wait forever; a failed/lost notification only leaves the
// peer's own state stale, not this server's.
func (rs *realtimeService) failReedNotHeld(pe *pendingReedEvent) {
	log.Info().
		Str("eventID", pe.EventID).
		Str("requesterID", pe.RequesterUserID).
		Str("authorID", pe.UserID).
		Str("reedID", pe.ReedID).
		Msg("No reed holders remain; notifying requester")

	if pe.RequesterUserID != "" {
		if err := rs.connManager.SendToUser(pe.RequesterUserID, newReedNotHeldMsg(pe.RequestID, pe.ReedID)); err != nil {
			log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to send reed not held")
		}
	}

	var frr *foreignRelayRequest
	if pe.RequesterUserID == "" {
		var err error
		frr, err = rs.db.GetForeignRelayRequest(context.Background(), pe.EventID)
		if err != nil {
			log.Error().Err(err).Str("eventID", pe.EventID).Msg("Failed to check foreign relay request on reed not held")
		}
	}

	if err := rs.deletePendingEvent(context.Background(), pe.EventID); err != nil {
		log.Error().Err(err).Str("eventID", pe.EventID).Msg("Failed to delete pending event on reed not held")
	}

	if frr != nil && rs.foreignNotHeldHook != nil {
		if err := rs.foreignNotHeldHook(context.Background(), frr.RequestingServerID, pe.EventID); err != nil {
			log.Error().Err(err).Str("eventID", pe.EventID).Str("requestingServerID", frr.RequestingServerID).Msg("Failed to notify requesting peer of relay give-up")
		}
	}
}

// handleDataAck is called when the viewer has received and verified a delivery successfully.
// New reeds: allocate. Reed removals: clear allocation. Account removals: clear peer state.
func (rs *realtimeService) handleDataAck(client *realtimeClient, eventID string) {
	if eventID == "" {
		return
	}

	pe, err := rs.db.GetPendingSubject(context.Background(), eventID)
	if err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to get pending event for data ack")
		return
	}
	if pe == nil {
		return
	}

	if realtimeEventName(pe.EventName) == reedRemovedEvent {
		changed, err := rs.db.DeleteReedAllocation(context.Background(), pe.ReedID, client.userID)
		if err != nil {
			log.Error().Err(err).Str("reedID", pe.ReedID).Str("userID", client.userID).Msg("Failed to clear allocation on reed_removed ack")
		} else if changed {
			rs.notifyReedCoverage(pe.ReedID)
		}
	} else if realtimeEventName(pe.EventName) == accountRemovedEvent {
		targets, err := rs.db.ClearPeerStateForRemovedAccount(context.Background(), client.userID, pe.UserID)
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
			if err := rs.db.UpsertReedIdentity(context.Background(), pe.ReedID); err != nil {
				log.Error().Err(err).Str("reedID", pe.ReedID).Msg("Failed to upsert reed identity on data ack")
				identityOK = false
			}
		}
		if identityOK {
			changed, err := rs.db.AllocateReed(context.Background(), pe.ReedID, client.userID)
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
				// server's. See realtimeForeignAckHook's doc comment.
				if rs.foreignAckHook != nil {
					if fpe, ferr := rs.db.GetForeignPendingEvent(context.Background(), eventID); ferr != nil {
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

	if err := rs.deletePendingEvent(context.Background(), eventID); err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to delete pending event on data ack")
	}
}

// handleDataInvalid is called when the viewer received a reed but its signature failed verification.
// The pending event is removed without allocating the reed to the viewer.
func (rs *realtimeService) handleDataInvalid(client *realtimeClient, eventID string) {
	if eventID == "" {
		return
	}

	if err := rs.deletePendingEvent(context.Background(), eventID); err != nil {
		log.Error().Err(err).Str("eventID", eventID).Msg("Failed to delete pending event on data invalid")
	}
}

// handleMailboxAck deletes the acked user_mailbox row. data.ID is the
// canonical ref (userID@serverID/id) this server sent in newMailboxMsg;
// DeleteMailboxMessage's query is scoped by bare id + userID, so split it
// back apart first. Unlike handleDataAck, there is no pending_events
// bookkeeping to reconcile — the row itself is the only delivery record.
func (rs *realtimeService) handleMailboxAck(client *realtimeClient, mailboxID string) {
	userID, serverID, id, ok := parseKeyFingerprint(identityID(mailboxID))
	if !ok || string(canonicalID(serverID, userID)) != client.userID {
		return
	}
	if err := rs.db.DeleteMailboxMessage(context.Background(), id, client.userID); err != nil {
		log.Error().Err(err).Str("id", id).Str("userID", client.userID).Msg("Failed to delete mailbox message on ack")
	}
}

// handleKeyFetchError is called when a client received signed content over
// an already-authenticated connection but a subsequent key fetch needed to
// verify it failed. Not tied to a pending_events row — this is the client
// self-reporting an anomaly, not acking a specific delivery.
func (rs *realtimeService) handleKeyFetchError(client *realtimeClient, targetUserID, keyID string) {
	if targetUserID == "" || keyID == "" {
		return
	}
	log.Warn().
		Str("reporterUserID", client.userID).
		Str("targetUserID", targetUserID).
		Str("keyID", keyID).
		Msg("Client reported key fetch error")
	rs.metrics.KeyFetchError(context.Background(), client.userID, targetUserID, keyID)
}

// handleRevokedKeyUsed is called when a client found signed content whose
// timestamp is at or after its signing key's revocation — a genuine
// revoked-key-abuse signal, surfaced for later security analysis.
func (rs *realtimeService) handleRevokedKeyUsed(client *realtimeClient, targetUserID, keyID string) {
	if targetUserID == "" || keyID == "" {
		return
	}
	log.Warn().
		Str("reporterUserID", client.userID).
		Str("targetUserID", targetUserID).
		Str("keyID", keyID).
		Msg("Client reported content signed with a revoked key")
	rs.metrics.RevokedKeyUsed(context.Background(), client.userID, targetUserID, keyID)
}

// handleContentRejected is called when a client received a signed resource,
// failed to verify it, and refused to store it — the client rejecting
// content, not a server-side signature check.
func (rs *realtimeService) handleContentRejected(client *realtimeClient, storeName, reason string) {
	if storeName == "" {
		return
	}
	log.Warn().
		Str("reporterUserID", client.userID).
		Str("storeName", storeName).
		Str("reason", reason).
		Msg("Client rejected content that failed verification")
	rs.metrics.ContentRejected(context.Background(), client.userID, storeName, reason)
}

// errRealtimeForeignRelayOwnershipMismatch is returned by
// CancelForeignPendingEvent when callerServerID doesn't match the peer
// that originally registered peerEventID — the HTTP handler maps this to 403.
var errRealtimeForeignRelayOwnershipMismatch = errors.New("foreign relay request belongs to a different peer")

// CancelForeignPendingEvent runs on the home server (H): the originating
// server's local requester disconnected before delivery completed, so H
// should drop its half of the pending state. Idempotent — an unknown
// peerEventID is a no-op, not an error.
func (rs *realtimeService) CancelForeignPendingEvent(ctx context.Context, peerEventID, callerServerID string) error {
	frr, err := rs.db.GetForeignRelayRequest(ctx, peerEventID)
	if err != nil {
		return err
	}
	if frr == nil {
		return nil
	}
	if frr.RequestingServerID != callerServerID {
		return errRealtimeForeignRelayOwnershipMismatch
	}
	return rs.deletePendingEvent(ctx, peerEventID)
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
func (rs *realtimeService) HandleForeignAck(ctx context.Context, peerEventID, callerServerID string) error {
	frr, err := rs.db.GetForeignRelayRequest(ctx, peerEventID)
	if err != nil {
		return err
	}
	if frr == nil {
		return nil
	}
	if frr.RequestingServerID != callerServerID {
		return errRealtimeForeignRelayOwnershipMismatch
	}

	pe, err := rs.db.GetPendingReedEvent(ctx, peerEventID)
	if err != nil {
		return err
	}
	if pe == nil {
		return nil
	}

	if err := rs.db.RecordServerHolder(ctx, pe.ReedID, callerServerID); err != nil {
		return err
	}
	return rs.deletePendingEvent(ctx, peerEventID)
}

// HandleForeignRelayResponse runs on the originating server (O): a home
// server (H) has delivered relayed content for a request O registered via
// leg 1. Resolves peerEventID back to O's own local pending_events row
// and delivers to the local requester exactly as a local RELAY_RESPONSE
// would, but does not touch dispatchNext/DeletePendingEvent — O has no
// holder queue for this event; allocation/deletion stays deferred until
// the requester's own DATA_ACK/DATA_INVALID.
func (rs *realtimeService) HandleForeignRelayResponse(ctx context.Context, peerEventID, callerServerID string, data json.RawMessage) (found bool, err error) {
	fpe, err := rs.db.GetForeignPendingEventByPeerEventID(ctx, peerEventID, callerServerID)
	if err != nil {
		return false, err
	}
	if fpe == nil {
		return false, nil
	}

	pe, err := rs.db.GetPendingReedEvent(ctx, fpe.EventID)
	if err != nil {
		return false, err
	}
	if pe == nil {
		// Local requester's row already gone (e.g. raced with disconnect) -- not an error.
		return true, nil
	}

	var ciphertext string
	if err := json.Unmarshal(data, &ciphertext); err != nil {
		return false, err
	}

	var msg *pb.WSMessage
	if pe.EventName == string(reedReplyEvent) {
		msg = newReedReplyMsg(pe.EventID, pe.RequestID, ciphertext, pe.ReedID)
	} else {
		msg = newDataResponseMsg(pe.EventID, pe.RequestID, ciphertext, pe.ReedID)
	}
	if err := rs.connManager.SendToUser(pe.RequesterUserID, msg); err != nil {
		log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to deliver foreign-relayed data response")
	}
	return true, nil
}

// HandleForeignRelayNotHeld runs on the originating server (O): the home
// server (H) exhausted every holder for a request O registered via leg 1
// and is giving up. Resolves peerEventID back to O's own local
// pending_events row, notifies the local requester the same way a local
// give-up would (newReedNotHeldMsg), and deletes O's half of the pending
// state — there's no content coming, so unlike a delivered response
// there's nothing left to defer until a DATA_ACK. Idempotent: an unknown
// peerEventID (already resolved, or never registered by this peer) is a
// no-op, not an error.
func (rs *realtimeService) HandleForeignRelayNotHeld(ctx context.Context, peerEventID, callerServerID string) (found bool, err error) {
	fpe, err := rs.db.GetForeignPendingEventByPeerEventID(ctx, peerEventID, callerServerID)
	if err != nil {
		return false, err
	}
	if fpe == nil {
		return false, nil
	}

	pe, err := rs.db.GetPendingReedEvent(ctx, fpe.EventID)
	if err != nil {
		return false, err
	}
	if pe == nil {
		// Local requester's row already gone (e.g. raced with disconnect) -- not an error.
		return true, nil
	}

	if err := rs.connManager.SendToUser(pe.RequesterUserID, newReedNotHeldMsg(pe.RequestID, pe.ReedID)); err != nil {
		log.Error().Err(err).Str("requesterID", pe.RequesterUserID).Msg("Failed to notify requester of foreign relay give-up")
	}
	if err := rs.deletePendingEvent(ctx, pe.EventID); err != nil {
		log.Error().Err(err).Str("eventID", pe.EventID).Msg("Failed to delete pending event on foreign relay give-up")
	}
	return true, nil
}

// SetBus installs the cross-replica delivery bus on the connection manager.
func (rs *realtimeService) SetBus(bus *realtimeBus) {
	rs.connManager.SetBus(bus)
}

// DeliverLocal writes a bus frame to this replica's own sockets for userID.
func (rs *realtimeService) DeliverLocal(userID string, frame []byte) bool {
	return rs.connManager.deliverLocal(userID, frame)
}
