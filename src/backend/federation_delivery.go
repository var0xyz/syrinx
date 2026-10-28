//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// Durable reed delivery to peers.
// Each (peer, local author) stream is drained by at most one goroutine at a
// time, one item after another, so a removal never overtakes its creation.

// peerDeliveryTimeout bounds one delivery call to a peer.
const peerDeliveryTimeout = 10 * time.Second

// deliverAuthorToPeers drains authorID's stream to every peer that is up.
// Called when the author publishes or removes a reed.
func (h *Handlers) deliverAuthorToPeers(authorID string) {
	ctx := context.Background()
	peers, err := h.services.db.ListDeliveryPeers(ctx)
	if err != nil {
		h.services.log.GetLogger(ctx).Error().Err(err).Msg("Failed to list peers for delivery")
		return
	}
	for _, peer := range peers {
		go h.drainPeerStream(peer, authorID)
	}
}

// deliverBehindStreams drains every stream that is behind, for one peer or,
// with an empty peerServerID, for all of them. This is the keepalive that
// retries a stream stuck on an earlier failure.
func (h *Handlers) deliverBehindStreams(peerServerID string) {
	ctx := context.Background()
	log := h.services.log.GetLogger(ctx)
	peers, err := h.services.db.ListDeliveryPeers(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list peers for delivery")
		return
	}
	for _, peer := range peers {
		if peerServerID != "" && peer.ID != peerServerID {
			continue
		}
		authors, err := h.services.db.BehindAuthors(ctx, peer.ID)
		if err != nil {
			log.Error().Err(err).Str("peerServerID", peer.ID).Msg("Failed to find streams behind for peer")
			continue
		}
		h.metrics.FederationDeliveryBacklog(ctx, peer.ID, len(authors))
		for _, author := range authors {
			go h.drainPeerStream(peer, author)
		}
	}
}

// drainPeerStream sends a peer everything it hasn't received from authorID,
// oldest first, stopping at the first transport error or 5xx. A 4xx means
// the peer will never take that item, so it is logged and skipped.
func (h *Handlers) drainPeerStream(peer PeerServer, authorID string) {
	h.drainPeerStreamWith(peer, authorID, h.sendPeerStreamItem)
}

// peerStreamSender delivers one stream item, returning the peer's status.
type peerStreamSender func(ctx context.Context, peer PeerServer, item peerStreamItem) (int, error)

func (h *Handlers) drainPeerStreamWith(peer PeerServer, authorID string, send peerStreamSender) {
	ctx := context.Background()
	log := h.services.log.GetLogger(ctx).With().Str("peerServerID", peer.ID).Str("authorID", authorID).Logger()
	db := h.services.db

	claimed, err := db.ClaimPeerStream(ctx, peer.ID, authorID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to claim peer stream")
		return
	}
	if !claimed {
		return
	}
	defer func() {
		if err := db.ReleasePeerStream(ctx, peer.ID, authorID); err != nil {
			log.Error().Err(err).Msg("Failed to release peer stream")
		}
	}()

	for {
		item, err := db.NextPeerStreamItem(ctx, peer.ID, authorID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to read peer stream")
			return
		}
		if item == nil {
			return
		}
		status, err := send(ctx, peer, *item)
		switch {
		case err != nil || status >= 500:
			log.Warn().Err(err).Int("status", status).Str("reedID", item.ReedID).Msg("Peer delivery failed, will retry")
			return
		case status >= 400:
			log.Error().Int("status", status).Str("reedID", item.ReedID).Msg("Peer refused delivery, skipping item")
		}
		ok, err := db.AdvancePeerStream(ctx, peer.ID, authorID, *item)
		if err != nil {
			log.Error().Err(err).Msg("Failed to advance peer stream")
			return
		}
		if !ok {
			return
		}
	}
}

func (h *Handlers) sendPeerStreamItem(ctx context.Context, peer PeerServer, item peerStreamItem) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, peerDeliveryTimeout)
	defer cancel()
	if item.Kind == streamKindRemoval {
		rm, err := h.services.db.GetThreadRemoval(ctx, item.ReedID)
		if err != nil {
			return 0, err
		}
		if rm != nil {
			return h.callPeerRelayEndpoint(ctx, peer.ID, peer.BaseURL, "/api/federation/relay/thread-removal", pbThreadRemoval(rm), nil)
		}
		payload, err := h.buildReedRemovalPayload(ctx, item.ReedID)
		if err != nil {
			return 0, err
		}
		return h.callPeerRelayEndpoint(ctx, peer.ID, peer.BaseURL, "/api/federation/relay/reed-removal", payload, nil)
	}
	payload, err := h.buildNewReedPayload(ctx, item.ReedID)
	if err != nil {
		return 0, err
	}
	return h.callPeerRelayEndpoint(ctx, peer.ID, peer.BaseURL, "/api/federation/relay/new-reed", payload, nil)
}

// buildNewReedPayload describes a local reed from what is stored about it.
func (h *Handlers) buildNewReedPayload(ctx context.Context, reedID string) (*pb.RelayNewReedPayload, error) {
	db := h.services.db
	author, signedAt, err := db.GetReedAuthorAndSignedAt(ctx, reedID)
	if err != nil {
		return nil, fmt.Errorf("load reed: %w", err)
	}
	mentions, err := db.GetReedMentions(ctx, reedID)
	if err != nil {
		return nil, fmt.Errorf("load mentions: %w", err)
	}
	adminServers, err := db.GetReedAdminMentions(ctx, reedID)
	if err != nil {
		return nil, fmt.Errorf("load admin mentions: %w", err)
	}
	for _, serverID := range adminServers {
		mentions = append(mentions, adminsHandle+"@"+serverID)
	}
	payload := &pb.RelayNewReedPayload{ReedId: reedID, AuthorId: author, SignedAt: unixOrZero(signedAt), Mentions: mentions}

	reply, err := db.GetReplyRecord(ctx, reedID)
	if err != nil {
		return nil, fmt.Errorf("load reply: %w", err)
	}
	if reply != nil {
		payload.Reply = &pb.RelayNewReedReply{ParentReedId: reply.ParentReedID, RootId: reply.RootID}
	}
	echoed, isBlank, ok, err := db.GetEchoTarget(ctx, reedID)
	if err != nil {
		return nil, fmt.Errorf("load echo: %w", err)
	}
	if ok && payload.Reply == nil {
		payload.Echo = &pb.RelayNewReedEcho{EchoedReedId: echoed, IsBlank: isBlank}
	}
	return payload, nil
}

// buildReedRemovalPayload carries a local reed's removal cert and, for a
// reply, its parent.
func (h *Handlers) buildReedRemovalPayload(ctx context.Context, reedID string) (*pb.RelayReedRemovalPayload, error) {
	db := h.services.db
	cert, err := db.GetReedRemoval(ctx, reedID)
	if err != nil {
		return nil, fmt.Errorf("load removal: %w", err)
	}
	if cert == nil {
		return nil, fmt.Errorf("no removal stored for %s", reedID)
	}
	payload := &pb.RelayReedRemovalPayload{Cert: &pb.RelayReedRemovalCert{
		ReedId:            cert.ReedID,
		UserId:            cert.UserID,
		UserSignature:     cert.UserSignature,
		UserKeyId:         cert.UserKeyID,
		ServerSignature:   cert.ServerSignature,
		ServerFingerprint: cert.ServerFingerprint,
		ServerSignedAt:    unixOrZero(cert.ServerSignedAt),
	}}
	if parent, ok, err := db.ReplyParent(ctx, reedID); err != nil {
		return nil, fmt.Errorf("load parent: %w", err)
	} else if ok {
		payload.ParentReedId = parent
	}
	return payload, nil
}

// Event kinds a peer's queue holds.
const (
	peerEventNewReed        = "new_reed"
	peerEventReedRemoval    = "reed_removal"
	peerEventThreadRemoval  = "thread_removal"
	peerEventAccountRemoval = "account_removal"
	peerEventBlock          = "block"
	peerEventFollow         = "follow"
	peerEventKeyRevocation  = "key_revocation"
	peerEventServerKey      = "server_key"
)

// errPeerEventGone means an event's subject no longer exists, so there is
// nothing left to send.
var errPeerEventGone = errors.New("peer event subject is gone")

// peerEventSender builds an event's call and, optionally, handles the
// peer refusing it for good.
type peerEventSender struct {
	build   func(ctx context.Context, ev peerEvent) (path string, body proto.Message, err error)
	refused func(ctx context.Context, peerID string, ev peerEvent)
}

// peerEventSenderFor returns how to send kind, or false for one nothing
// sends.
func (h *Handlers) peerEventSenderFor(kind string) (peerEventSender, bool) {
	switch kind {
	}
	return peerEventSender{}, false
}

// peerEventCall sends one event's body to a peer and returns its status.
type peerEventCall func(ctx context.Context, peerID, baseURL, path string, body proto.Message) (int, error)

func (h *Handlers) callPeerEvent(ctx context.Context, peerID, baseURL, path string, body proto.Message) (int, error) {
	return h.callPeerRelayEndpoint(ctx, peerID, baseURL, path, body, nil)
}

// drainPeers drains peerID's queue, or every peer's when it is empty.
// Each drains in its own goroutine, so a slow peer holds up only itself.
func (h *Handlers) drainPeers(peerID string) {
	if peerID != "" {
		go h.drainPeerEvents(peerID)
		return
	}
	ctx := context.Background()
	peers, err := h.services.db.ListPeersOwedEvents(ctx)
	if err != nil {
		h.services.log.GetLogger(ctx).Error().Err(err).Msg("Failed to list peers to drain")
		return
	}
	for _, id := range peers {
		go h.drainPeerEvents(id)
	}
}

func (h *Handlers) drainPeerEvents(peerID string) {
	h.drainPeerEventsWith(peerID, h.peerEventSenderFor, h.callPeerEvent)
}

// drainPeerEventsWith sends peerID its queue, oldest first. A 2xx or a
// refusal (4xx but 429) settles an event; anything else stops the drain,
// and the next trigger resumes from the same event.
func (h *Handlers) drainPeerEventsWith(peerID string, senderFor func(kind string) (peerEventSender, bool), call peerEventCall) {
	ctx := context.Background()
	log := h.services.log.GetLogger(ctx).With().Str("peerServerID", peerID).Logger()
	db := h.services.db

	claimed, err := db.ClaimPeerEvents(ctx, peerID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to claim peer event queue")
		return
	}
	if !claimed {
		return
	}
	defer func() {
		if err := db.ReleasePeerEvents(ctx, peerID); err != nil {
			log.Error().Err(err).Msg("Failed to release peer event queue")
		}
	}()
	if n, err := db.CountPeerEvents(ctx, peerID); err == nil {
		h.metrics.FederationDeliveryBacklog(ctx, peerID, n)
	}

	for {
		ev, err := db.NextPeerEvent(ctx, peerID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to read peer event queue")
			return
		}
		if ev == nil {
			return
		}
		evLog := log.With().Str("kind", ev.Kind).Str("subjectID", ev.SubjectID).Str("objectID", ev.ObjectID).Logger()
		settle := func() bool {
			if err := db.DeletePeerEvent(ctx, ev.ID); err != nil {
				evLog.Error().Err(err).Msg("Failed to delete peer event")
				return false
			}
			return true
		}
		sender, ok := senderFor(ev.Kind)
		if !ok {
			evLog.Error().Msg("No sender for peer event kind, dropping it")
			if !settle() {
				return
			}
			continue
		}
		path, body, err := sender.build(ctx, *ev)
		if errors.Is(err, errPeerEventGone) {
			evLog.Warn().Msg("Peer event subject is gone, dropping it")
			if !settle() {
				return
			}
			continue
		}
		if err != nil {
			evLog.Error().Err(err).Msg("Failed to build peer event")
			return
		}
		status, err := call(ctx, peerID, ev.BaseURL, path, body)
		switch {
		case err != nil || status >= 500 || status == http.StatusTooManyRequests:
			evLog.Warn().Err(err).Int("status", status).Msg("Peer didn't take event, will retry")
			return
		case status >= 400:
			evLog.Error().Int("status", status).Msg("Peer refused event, dropping it")
			if sender.refused != nil {
				sender.refused(ctx, peerID, *ev)
			}
		}
		if !settle() {
			return
		}
		if err := db.RefreshPeerEventsClaim(ctx, peerID); err != nil {
			log.Error().Err(err).Msg("Failed to refresh peer event queue claim")
			return
		}
	}
}
