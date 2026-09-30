//go:build !ops && !ripplescleanup

package main

import (
	"context"
	"fmt"
	"time"
)

// Durable reed delivery to peers (specs/federation/09_reed_delivery.md).
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
func (h *Handlers) buildNewReedPayload(ctx context.Context, reedID string) (relayNewReedPayload, error) {
	db := h.services.db
	author, signedAt, err := db.GetReedAuthorAndSignedAt(ctx, reedID)
	if err != nil {
		return relayNewReedPayload{}, fmt.Errorf("load reed: %w", err)
	}
	mentions, err := db.GetReedMentions(ctx, reedID)
	if err != nil {
		return relayNewReedPayload{}, fmt.Errorf("load mentions: %w", err)
	}
	payload := relayNewReedPayload{ReedID: reedID, AuthorID: author, SignedAt: signedAt, Mentions: mentions}

	reply, err := db.GetReplyRecord(ctx, reedID)
	if err != nil {
		return relayNewReedPayload{}, fmt.Errorf("load reply: %w", err)
	}
	if reply != nil {
		payload.Reply = &relayNewReedReply{ParentReedID: reply.ParentReedID, ThreadID: reply.ThreadID}
	}
	echoed, isBlank, ok, err := db.GetEchoTarget(ctx, reedID)
	if err != nil {
		return relayNewReedPayload{}, fmt.Errorf("load echo: %w", err)
	}
	if ok && payload.Reply == nil {
		payload.Echo = &relayNewReedEcho{EchoedReedID: echoed, IsBlank: isBlank}
	}
	return payload, nil
}

// buildReedRemovalPayload carries a local reed's removal cert and, for a
// reply, its parent.
func (h *Handlers) buildReedRemovalPayload(ctx context.Context, reedID string) (relayReedRemovalPayload, error) {
	db := h.services.db
	cert, err := db.GetReedRemoval(ctx, reedID)
	if err != nil {
		return relayReedRemovalPayload{}, fmt.Errorf("load removal: %w", err)
	}
	if cert == nil {
		return relayReedRemovalPayload{}, fmt.Errorf("no removal stored for %s", reedID)
	}
	payload := relayReedRemovalPayload{relayReedRemovalCert: relayReedRemovalCert{
		ReedID:            cert.ReedID,
		UserID:            cert.UserID,
		UserSignature:     cert.UserSignature,
		UserKeyID:         cert.UserKeyID,
		ServerSignature:   cert.ServerSignature,
		ServerFingerprint: cert.ServerFingerprint,
		ServerSignedAt:    cert.ServerSignedAt,
	}}
	if parent, ok, err := db.ReplyParent(ctx, reedID); err != nil {
		return relayReedRemovalPayload{}, fmt.Errorf("load parent: %w", err)
	} else if ok {
		payload.ParentReedID = parent
	}
	return payload, nil
}
