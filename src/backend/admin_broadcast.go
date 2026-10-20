//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"syrinx/observability/metrics"
)

// Mailbox kinds of an admin's broadcast and private message.
const (
	mailboxKindBroadcast    = "broadcast"
	mailboxKindAdminMessage = "admin-message"
)

// activeLocalMembers returns every local member who isn't removed or
// suspended.
func (s *DataService) activeLocalMembers(ctx context.Context) ([]string, error) {
	return s.queryStrings(ctx, `
		SELECT u.id FROM users u
		WHERE u.suspended_by IS NULL AND u.username IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM account_removals ar WHERE ar.user_id = u.id)
	`)
}

// deliverAdminMail puts message in each recipient's mailbox and tells the
// connected ones. One failure is logged and doesn't stop the rest.
func (h *Handlers) deliverAdminMail(req *adminRequest, recipients []string, kind string) {
	ctx := context.Background()
	log := h.services.log.GetLogger(ctx)
	failed := 0
	defer func() { h.metrics.AdminMailSent(ctx, req.Action, req.RequesterID, len(recipients), failed) }()
	for _, userID := range recipients {
		id, ciphertext, err := SendMailboxMessage(ctx, h.services.db.db, h.services.crypto, userID,
			kind, req.Body, "", nil)
		if err != nil {
			log.Error().Err(err).Str("userID", userID).Str("requestID", req.ID).Msg("Failed to deliver admin mail")
			failed++
			continue
		}
		h.metrics.Mailbox(ctx, metrics.OpAdd, kind)
		if h.realtimeRelay != nil {
			h.realtimeRelay.NotifyMailboxMessage(userID, id, ciphertext)
		}
	}
}

// broadcastExecutor sends the request's message to every active member.
func (h *Handlers) broadcastExecutor() adminExecutor {
	return adminExecutor{
		execute: func(context.Context, *sql.Tx, *adminRequest) error { return nil },
		after: func(req *adminRequest) {
			go func() {
				ctx := context.Background()
				members, err := h.services.db.activeLocalMembers(ctx)
				if err != nil {
					h.services.log.GetLogger(ctx).Error().Err(err).Str("requestID", req.ID).Msg("Failed to list broadcast recipients")
					return
				}
				h.deliverAdminMail(req, members, mailboxKindBroadcast)
			}()
		},
	}
}

// unicastExecutor sends the request's private message to its one target.
func (h *Handlers) unicastExecutor() adminExecutor {
	return adminExecutor{
		execute: func(context.Context, *sql.Tx, *adminRequest) error { return nil },
		after: func(req *adminRequest) {
			go h.deliverAdminMail(req, req.remainingTargets(), mailboxKindAdminMessage)
		},
	}
}
