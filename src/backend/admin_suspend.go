//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"net/http"

	pb "syrinx/proto"
)

// suspendedByQ is the request that suspended userID, "" when they aren't.
func suspendedByQ(ctx context.Context, q adminQuerier, userID string) (string, error) {
	var requestID sql.NullString
	err := q.QueryRowContext(ctx, `SELECT suspended_by FROM users WHERE id = $1`, userID).Scan(&requestID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return requestID.String, err
}

// GetSuspension returns the request that suspended userID, nil when they
// aren't suspended.
func (s *DataService) GetSuspension(ctx context.Context, userID string) (*adminRequest, error) {
	requestID, err := suspendedByQ(ctx, s.db, userID)
	if err != nil || requestID == "" {
		return nil, err
	}
	return s.GetAdminRequest(ctx, requestID)
}

// writeSuspended refuses a suspended user with their suspension certificate.
func writeSuspended(w http.ResponseWriter, req *adminRequest) {
	writeResponse(w, http.StatusForbidden, &pb.Error{
		Message: "Suspended",
		Detail:  &pb.Error_Suspension{Suspension: &pb.AdminAuthorization{Request: pbAdminRequest(req)}},
	})
}

// refuseIfSuspended answers 403 with the certificate when userID is
// suspended. True when it answered.
func refuseIfSuspended(ctx context.Context, w http.ResponseWriter, db *DataService, userID string) bool {
	suspension, err := db.GetSuspension(ctx, userID)
	if err != nil {
		internalServerError(w)
		return true
	}
	if suspension == nil {
		return false
	}
	writeSuspended(w, suspension)
	return true
}

// suspendExecutor suspends every remaining target and closes their sockets.
func (h *Handlers) suspendExecutor() adminExecutor {
	return adminExecutor{
		validate: func(ctx context.Context, q adminQuerier, req *adminRequest) error {
			for _, target := range req.remainingTargets() {
				by, err := suspendedByQ(ctx, q, target)
				if err != nil {
					return err
				}
				if by != "" {
					return refuse(http.StatusConflict, "already_suspended", "%s is already suspended", target)
				}
			}
			return nil
		},
		execute: func(ctx context.Context, tx *sql.Tx, req *adminRequest) error {
			for _, target := range req.remainingTargets() {
				if _, err := tx.ExecContext(ctx, `UPDATE users SET suspended_by = $1 WHERE id = $2`, req.ID, target); err != nil {
					return err
				}
			}
			return nil
		},
		after: func(req *adminRequest) {
			for _, target := range req.remainingTargets() {
				h.kickUserDevices(target)
			}
		},
	}
}

// reinstateExecutor lifts every remaining target's suspension.
func (h *Handlers) reinstateExecutor() adminExecutor {
	return adminExecutor{
		validate: func(ctx context.Context, q adminQuerier, req *adminRequest) error {
			for _, target := range req.remainingTargets() {
				by, err := suspendedByQ(ctx, q, target)
				if err != nil {
					return err
				}
				if by == "" {
					return refuse(http.StatusConflict, "not_suspended", "%s isn't suspended", target)
				}
			}
			return nil
		},
		execute: func(ctx context.Context, tx *sql.Tx, req *adminRequest) error {
			for _, target := range req.remainingTargets() {
				if _, err := tx.ExecContext(ctx, `UPDATE users SET suspended_by = NULL WHERE id = $1`, target); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
