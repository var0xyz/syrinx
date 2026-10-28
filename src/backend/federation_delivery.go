//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

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
		return nil, errPeerEventGone
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

// errPeerEventGone means an event's subject no longer exists, so there is
// nothing left to send.
var errPeerEventGone = errors.New("peer event subject is gone")

// peerEventSender builds an event's call. refused, when set, handles the
// peer refusing body for good; settled runs once the event leaves the queue.
type peerEventSender struct {
	build   func(ctx context.Context, ev peerEvent) (path string, body proto.Message, err error)
	refused func(ctx context.Context, peerID string, ev peerEvent, body proto.Message)
	settled func(ctx context.Context, ev peerEvent)
}

// peerEventSenderFor returns how to send kind, or false for one nothing
// sends.
func (h *Handlers) peerEventSenderFor(kind string) (peerEventSender, bool) {
	switch kind {
	case peerEventNewReed:
		return peerEventSender{build: func(ctx context.Context, ev peerEvent) (string, proto.Message, error) {
			payload, err := h.buildNewReedPayload(ctx, ev.SubjectID)
			if errors.Is(err, sql.ErrNoRows) {
				return "", nil, errPeerEventGone
			}
			return "/api/federation/relay/new-reed", payload, err
		}}, true
	case peerEventReedRemoval:
		return peerEventSender{build: func(ctx context.Context, ev peerEvent) (string, proto.Message, error) {
			payload, err := h.buildReedRemovalPayload(ctx, ev.SubjectID)
			return "/api/federation/relay/reed-removal", payload, err
		}}, true
	case peerEventAccountRemoval:
		return peerEventSender{build: func(ctx context.Context, ev peerEvent) (string, proto.Message, error) {
			cert, err := h.services.db.GetAccountRemoval(ctx, ev.SubjectID)
			if err != nil {
				return "", nil, err
			}
			if cert == nil {
				return "", nil, errPeerEventGone
			}
			payload := &pb.RelayAccountRemovalNotifyPayload{
				UserId:            ev.SubjectID,
				Note:              cert.Note,
				UserSignature:     cert.UserSignature,
				UserKeyId:         cert.UserKeyID,
				ServerSignature:   cert.ServerSignature,
				ServerFingerprint: cert.ServerFingerprint,
				ServerSignedAt:    unixOrZero(cert.ServerSignedAt),
			}
			if cert.Admin != nil {
				payload.Admin = &pb.AdminAuthorization{Request: pbAdminRequest(cert.Admin)}
			}
			return "/api/federation/relay/account-removal-notify", payload, nil
		}}, true
	case peerEventBlock:
		return peerEventSender{build: func(ctx context.Context, ev peerEvent) (string, proto.Message, error) {
			cert, err := h.services.db.GetBlock(ctx, ev.SubjectID, ev.ObjectID)
			if err != nil {
				return "", nil, err
			}
			if cert != nil {
				return "/api/federation/relay/block-notify", pbBlockCert(cert), nil
			}
			return "/api/federation/relay/unblock-notify", &pb.RelayUnblockPayload{UserId: ev.SubjectID, BlockedUserId: ev.ObjectID}, nil
		}}, true
	case peerEventFollow:
		return peerEventSender{
			build: func(ctx context.Context, ev peerEvent) (string, proto.Message, error) {
				cert, err := h.services.db.GetFollowCert(ctx, ev.SubjectID, ev.ObjectID)
				if err != nil {
					return "", nil, err
				}
				if cert == nil {
					return "", nil, errPeerEventGone
				}
				return "/api/federation/relay/follow-notify", pbFollowCert(cert), nil
			},
			refused: func(ctx context.Context, peerID string, ev peerEvent, body proto.Message) {
				sent, ok := body.(*pb.FollowCert)
				if !ok || !sent.GetFollow() {
					return
				}
				if err := h.services.db.UndoRefusedFollow(ctx, ev.SubjectID, ev.ObjectID, timeFromUnix(sent.GetServerSignature().GetSignedAt())); err != nil {
					h.services.log.GetLogger(ctx).Error().Err(err).Str("peerServerID", peerID).Msg("Failed to undo refused follow")
				}
			},
			settled: func(ctx context.Context, ev peerEvent) {
				if err := h.services.db.DropSettledUnfollow(ctx, ev.SubjectID, ev.ObjectID); err != nil {
					h.services.log.GetLogger(ctx).Error().Err(err).Msg("Failed to drop delivered unfollow")
				}
			},
		}, true
	case peerEventKeyRevocation:
		return peerEventSender{build: func(ctx context.Context, ev peerEvent) (string, proto.Message, error) {
			rev, err := h.services.db.GetKeyRevocation(ctx, ev.SubjectID)
			if err != nil {
				return "", nil, err
			}
			if rev == nil {
				return "", nil, errPeerEventGone
			}
			return "/api/federation/relay/key-revocation", pbKeyRevocation(rev), nil
		}}, true
	case peerEventServerKey:
		// The call itself, signed with our current key, is the notice.
		return peerEventSender{build: func(context.Context, peerEvent) (string, proto.Message, error) {
			return "/api/federation/relay/server-key", nil, nil
		}}, true
	case peerEventThreadRemoval:
		return peerEventSender{build: func(ctx context.Context, ev peerEvent) (string, proto.Message, error) {
			rm, err := h.services.db.GetThreadRemoval(ctx, ev.SubjectID)
			if err != nil {
				return "", nil, err
			}
			if rm == nil {
				return "", nil, errPeerEventGone
			}
			return "/api/federation/relay/thread-removal", pbThreadRemoval(rm), nil
		}}, true
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
				sender.refused(ctx, peerID, *ev, body)
			}
		}
		if !settle() {
			return
		}
		if sender.settled != nil {
			sender.settled(ctx, *ev)
		}
		if err := db.RefreshPeerEventsClaim(ctx, peerID); err != nil {
			log.Error().Err(err).Msg("Failed to refresh peer event queue claim")
			return
		}
	}
}
