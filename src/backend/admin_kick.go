//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// kickExecutor removes every remaining target, signed by the two admins in
// place of the user, and revokes their unclaimed invites.
func (h *Handlers) kickExecutor() adminExecutor {
	var kicked []accountRemovalCert
	revokedInvites := map[string]int64{}
	var flagged []string
	return adminExecutor{
		execute: func(ctx context.Context, tx *sql.Tx, req *adminRequest) error {
			serverID := h.services.db.GetServerID()
			for _, target := range req.remainingTargets() {
				now := time.Now().UTC().Truncate(time.Second)
				sig, err := h.countersign(ctx, buildAccountKickServerPayload(serverID, target, req.ID,
					req.RequesterSignature.Armor, req.Decision.Signature.Armor, h.signingKey.Fingerprint, now), now)
				if err != nil {
					return err
				}
				cert := accountRemovalCert{
					UserID:            target,
					ServerSignature:   sig.Armor,
					ServerFingerprint: sig.ID,
					ServerSignedAt:    now,
					AdminRequestID:    req.ID,
				}
				authors, err := authorsOpenFlaggedByQ(ctx, tx, target)
				if err != nil {
					return err
				}
				flagged = append(flagged, authors...)
				if err := storeAccountRemovalTx(ctx, tx, cert, identityID(target)); err != nil {
					return err
				}
				res, err := tx.ExecContext(ctx, `
					UPDATE invites SET revoked_at = $2
					WHERE created_by = $1 AND claimed_at IS NULL AND revoked_at IS NULL
				`, target, now)
				if err != nil {
					return err
				}
				revokedInvites[target], _ = res.RowsAffected()
				kicked = append(kicked, cert)
			}
			return nil
		},
		after: func(req *adminRequest) {
			h.reportOpenFlags(context.Background(), flagged...)
			for _, cert := range kicked {
				cert.Admin = req
				h.signOutRemovedUser(cert.UserID)
				h.afterAccountRemoval(context.Background(), cert.UserID, cert)
				h.metrics.UserDeleted(context.Background(), metrics.RemovalKick, false)
				h.recordInvitesRevoked(req.Action, cert.UserID, revokedInvites[cert.UserID])
			}
		},
	}
}

// inviteTreeNode is one user in an invite tree.
type inviteTreeNode struct {
	UserID    string
	Username  string
	Role      string
	InvitedBy string
	Suspended bool
	Removed   bool
}

const inviteTreeSelect = `
	SELECT u.id, COALESCE(u.username, ''), u.role, COALESCE(invite_creator(u.invite_id), ''),
		u.suspended_by IS NOT NULL,
		EXISTS(SELECT 1 FROM account_removals ar WHERE ar.user_id = u.id)
	FROM users u`

func scanInviteTreeNodes(rows *sql.Rows, err error) ([]inviteTreeNode, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []inviteTreeNode
	for rows.Next() {
		var n inviteTreeNode
		if err := rows.Scan(&n.UserID, &n.Username, &n.Role, &n.InvitedBy, &n.Suspended, &n.Removed); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// GetInviteTree returns userID's inviter, userID, and everyone below them
// in the invite tree, in thread order with each node's depth. Nil when
// userID isn't a local user.
func (s *DataService) GetInviteTree(ctx context.Context, userID string) ([]inviteTreeNode, []int, error) {
	self, err := scanInviteTreeNodes(s.db.QueryContext(ctx, inviteTreeSelect+` WHERE u.id = $1`, userID))
	if err != nil || len(self) == 0 {
		return nil, nil, err
	}
	below, err := scanInviteTreeNodes(s.db.QueryContext(ctx, `
		WITH RECURSIVE subtree(id) AS (
			SELECT u.id FROM users u WHERE invite_creator(u.invite_id) = $1
			UNION
			SELECT u.id FROM users u JOIN subtree t ON invite_creator(u.invite_id) = t.id
		)
	`+inviteTreeSelect+` WHERE u.id IN (SELECT id FROM subtree) ORDER BY u.created_at, u.id`, userID))
	if err != nil {
		return nil, nil, err
	}

	var nodes []inviteTreeNode
	var depths []int
	if inviter := self[0].InvitedBy; inviter != "" {
		top, err := scanInviteTreeNodes(s.db.QueryContext(ctx, inviteTreeSelect+` WHERE u.id = $1`, inviter))
		if err != nil {
			return nil, nil, err
		}
		if len(top) == 1 {
			top[0].InvitedBy = ""
			nodes, depths = append(nodes, top[0]), append(depths, 0)
		}
	}
	children := map[string][]inviteTreeNode{}
	for _, n := range below {
		children[n.InvitedBy] = append(children[n.InvitedBy], n)
	}
	var walk func(n inviteTreeNode, depth int)
	walk = func(n inviteTreeNode, depth int) {
		nodes, depths = append(nodes, n), append(depths, depth)
		for _, c := range children[n.UserID] {
			walk(c, depth+1)
		}
	}
	walk(self[0], 1)
	return nodes, depths, nil
}

// GetInviteTree handles GET /admin/users/{userID}/invite-tree.
func (h *Handlers) GetInviteTree(w http.ResponseWriter, r *http.Request) {
	_, role, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	if !isAdmin(role) {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	nodes, depths, err := h.services.db.GetInviteTree(r.Context(), mux.Vars(r)["userID"])
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading invite tree")
		return
	}
	if nodes == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	out := &pb.InviteTree{}
	for i, n := range nodes {
		out.Nodes = append(out.Nodes, &pb.InviteTreeNode{
			UserId:    n.UserID,
			Username:  n.Username,
			Role:      n.Role,
			Depth:     uint32(depths[i]),
			InvitedBy: n.InvitedBy,
			Suspended: n.Suspended,
			Removed:   n.Removed,
		})
	}
	writeResponse(w, http.StatusOK, out)
}
