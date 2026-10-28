//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	pb "syrinx/proto"
)

// maxInviteGrant caps how many invites one request may grant.
const maxInviteGrant = 100

// parseInviteGrant reads an invites request's body: a count from 1 to
// maxInviteGrant in plain decimal, so the signed bytes have one spelling.
func parseInviteGrant(body string) (int, error) {
	n, err := strconv.Atoi(body)
	if err != nil || strconv.Itoa(n) != body {
		return 0, fmt.Errorf("%q isn't a whole number", body)
	}
	if n < 1 || n > maxInviteGrant {
		return 0, fmt.Errorf("must be from 1 to %d", maxInviteGrant)
	}
	return n, nil
}

// grantedInvitesSQL sums the invites executed grants gave user $1.
const grantedInvitesSQL = `
	SELECT COALESCE(SUM(c.body::int), 0)
	FROM admin_requests ar
	JOIN admin_request_targets t ON t.request_id = ar.id
	JOIN admin_request_content c ON c.request_id = ar.id
	WHERE ar.action = 'invites' AND ar.state = 'executed' AND t.user_id = $1`

// inviteLimitQ is how many invites userID may create in all, -1 for no
// limit. Root has none; everyone else gets the server cap plus grants.
func inviteLimitQ(ctx context.Context, q adminQuerier, userID, serverID string, maxInvites int) (int, error) {
	if maxInvites == maxInvitesUnlimited || isRootIdentity(userID, serverID) {
		return maxInvitesUnlimited, nil
	}
	var granted int
	if err := q.QueryRowContext(ctx, grantedInvitesSQL, userID).Scan(&granted); err != nil {
		return 0, err
	}
	return maxInvites + granted, nil
}

// invitesExecutor grants the target more invites. The executed request is
// the grant itself, so approving it changes nothing else.
func (h *Handlers) invitesExecutor() adminExecutor {
	return adminExecutor{
		validate: func(ctx context.Context, q adminQuerier, req *adminRequest) error {
			if h.cfg.MaxInvitesPerUser == maxInvitesUnlimited {
				return refuse(http.StatusConflict, "no_invite_limit", "This server has no invite limit")
			}
			return nil
		},
		execute: func(ctx context.Context, tx *sql.Tx, req *adminRequest) error { return nil },
	}
}

// GetInviteQuota answers how many invites the caller may create in all.
func (h *Handlers) GetInviteQuota(w http.ResponseWriter, r *http.Request) {
	caller, ok := r.Context().Value(userIDKey).(string)
	if !ok || caller == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	limit, err := inviteLimitQ(r.Context(), h.services.db.db, caller, h.services.db.GetServerID(), h.cfg.MaxInvitesPerUser)
	if err != nil {
		h.services.log.GetLogger(r.Context()).Error().Err(err).Msg("Error loading invite limit")
		internalServerError(w)
		return
	}
	writeResponse(w, http.StatusOK, &pb.InviteQuota{Limit: int32(limit)})
}
