//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"net/http"
	"slices"
	"time"

	pb "syrinx/proto"
)

// adminRecord is one record every admin's device keeps: a request, a flag
// or a dismissal. Exactly one is set.
type adminRecord struct {
	Request   *adminRequest
	Flag      *reedFlag
	Dismissal *flagDismissal
}

// visibleTo reports whether viewerID, whose permissions hide the actions in
// hidden, may see rec. Nobody sees flags on their own reeds.
func (rec adminRecord) visibleTo(viewerID string, hidden []string) bool {
	var reedID string
	switch {
	case rec.Request != nil:
		return rec.Request.RequesterID == viewerID || !slices.Contains(hidden, rec.Request.Action)
	case rec.Flag != nil:
		reedID = rec.Flag.ReedID
	case rec.Dismissal != nil:
		reedID = rec.Dismissal.ReedID
	default:
		return false
	}
	author, ok := authorOf(identityID(reedID))
	return ok && string(author) != viewerID
}

func newAdminRecordMsg(rec adminRecord) *pb.WSMessage {
	out := &pb.AdminRecordMessage{}
	switch {
	case rec.Request != nil:
		out.Record = &pb.AdminRecordMessage_Request{Request: pbAdminRequest(rec.Request)}
	case rec.Flag != nil:
		out.Record = &pb.AdminRecordMessage_Flag{Flag: pbReedFlag(*rec.Flag)}
	case rec.Dismissal != nil:
		out.Record = &pb.AdminRecordMessage_Dismissal{Dismissal: pbFlagDismissal(rec.Dismissal)}
	}
	return &pb.WSMessage{Type: pb.MessageType_ADMIN_RECORD, Payload: &pb.WSMessage_AdminRecord{AdminRecord: out}}
}

// adminViewer is an admin or root, with the actions their permissions hide.
type adminViewer struct {
	ID     string
	Hidden []string
}

// ListAdminViewers lists this server's admins and root.
func (s *DataService) ListAdminViewers(ctx context.Context) ([]adminViewer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, role FROM users WHERE role IN ('admin', 'root')`)
	if err != nil {
		return nil, err
	}
	type row struct{ id, role string }
	var users []row
	for rows.Next() {
		var u row
		if err := rows.Scan(&u.id, &u.role); err != nil {
			rows.Close()
			return nil, err
		}
		users = append(users, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	viewers := make([]adminViewer, 0, len(users))
	for _, u := range users {
		perms, err := userPermissionsQ(ctx, s.db, u.id, u.role, s.serverID)
		if err != nil {
			return nil, err
		}
		viewers = append(viewers, adminViewer{ID: u.id, Hidden: hiddenAdminActions(perms)})
	}
	return viewers, nil
}

// pushAdminRecord sends rec to every connected admin and root who may see
// it. One who isn't connected catches up on their next sync.
func (h *Handlers) pushAdminRecord(ctx context.Context, rec adminRecord) {
	if h.realtimeRelay == nil {
		return
	}
	viewers, err := h.services.db.ListAdminViewers(ctx)
	if err != nil {
		h.services.log.GetLogger(ctx).Error().Err(err).Msg("Failed to list admins for an admin record")
		return
	}
	msg := newAdminRecordMsg(rec)
	for _, v := range viewers {
		if rec.visibleTo(v.ID, v.Hidden) {
			// An admin without a connection here gets it on catch-up.
			_ = h.realtimeRelay.connManager.SendToUser(v.ID, msg)
		}
	}
}

// openedBeforeRecovery reports whether req may not be acted on yet: it
// was opened before this database was rebuilt, and recovery isn't over,
// so a device may still hold a closed copy of it.
func (h *Handlers) openedBeforeRecovery(ctx context.Context, req *adminRequest) (bool, error) {
	if !recoveryMode(h.cfg.RecoveryMode).recovering() {
		return false, nil
	}
	firstBoot, err := h.services.db.FirstBootAt(ctx)
	if err != nil {
		return false, err
	}
	return req.CreatedAt.Before(firstBoot), nil
}

// errOpenedBeforeRecovery refuses a decision on a request openedBeforeRecovery.
var errOpenedBeforeRecovery = refuse(http.StatusConflict, "opened_before_recovery", "opened-before-recovery")

// FirstBootAt is when this database was created, to the second.
func (s *DataService) FirstBootAt(ctx context.Context) (time.Time, error) {
	var at time.Time
	err := s.db.QueryRowContext(ctx, `SELECT date_trunc('second', first_boot_at) FROM server_self`).Scan(&at)
	return at.UTC(), err
}
