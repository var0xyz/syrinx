//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/lib/pq"
	"github.com/rs/zerolog/log"

	pb "syrinx/proto"
)

// ledgerPageSize caps the entries one ledger page returns.
const ledgerPageSize = 100

const ledgerEntrySelect = `
	SELECT e.id, e.prev_id, e.kind, COALESCE(e.user_id, ''), COALESCE(e.requester_id, ''), e.requested_at,
		e.message, COALESCE(e.content, ''),
		ss.private_key_id, ss.signature, ss.signed_at
	FROM ledger_entries e
	JOIN server_signatures ss ON ss.id = e.server_signature_id`

func scanLedgerEntry(row interface{ Scan(dest ...any) error }) (*ledgerEntry, error) {
	var e ledgerEntry
	var prevID sql.NullInt64
	var requestedAt sql.NullTime
	err := row.Scan(&e.ID, &prevID, &e.Kind, &e.UserID, &e.RequesterID, &requestedAt, &e.Message, &e.Content,
		&e.ServerSignature.ID, &e.ServerSignature.Armor, &e.ServerSignature.SignedAt)
	if err != nil {
		return nil, err
	}
	e.PrevID = uint64(prevID.Int64)
	if requestedAt.Valid {
		e.RequestedAt = requestedAt.Time.UTC()
	}
	e.ServerSignature.SignedAt = e.ServerSignature.SignedAt.UTC()
	return &e, nil
}

// loadLedgerTargets fills the targets of entries.
func (s *DataService) loadLedgerTargets(ctx context.Context, entries []*ledgerEntry) error {
	if len(entries) == 0 {
		return nil
	}
	byID := make(map[uint64]*ledgerEntry, len(entries))
	ids := make([]int64, 0, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
		ids = append(ids, int64(e.ID))
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT entry_id, user_id FROM ledger_entry_targets WHERE entry_id = ANY($1)
	`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uint64
		var userID string
		if err := rows.Scan(&id, &userID); err != nil {
			return err
		}
		byID[id].Targets = append(byID[id].Targets, userID)
	}
	for _, e := range entries {
		sort.Strings(e.Targets)
	}
	return rows.Err()
}

// GetLedgerEntry loads one entry, nil when unknown.
func (s *DataService) GetLedgerEntry(ctx context.Context, id uint64) (*ledgerEntry, error) {
	e, err := scanLedgerEntry(s.db.QueryRowContext(ctx, ledgerEntrySelect+` WHERE e.id = $1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, s.loadLedgerTargets(ctx, []*ledgerEntry{e})
}

// GetLedgerHead loads the entry that starts the chain, nil while the
// ledger is empty.
func (s *DataService) GetLedgerHead(ctx context.Context) (*ledgerEntry, error) {
	e, err := scanLedgerEntry(s.db.QueryRowContext(ctx, ledgerEntrySelect+` WHERE e.prev_id IS NULL`))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, s.loadLedgerTargets(ctx, []*ledgerEntry{e})
}

// GetLedgerTail returns up to limit entries that follow afterID, in chain
// order.
func (s *DataService) GetLedgerTail(ctx context.Context, afterID uint64, limit int) ([]*ledgerEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE chain(id, depth) AS (
			SELECT id, 1 FROM ledger_entries WHERE prev_id = $1
			UNION ALL
			SELECT n.id, c.depth + 1 FROM ledger_entries n JOIN chain c ON n.prev_id = c.id
			WHERE c.depth < $2
		)
	`+ledgerEntrySelect+` JOIN chain c ON c.id = e.id ORDER BY c.depth`, afterID, limit)
	if err != nil {
		return nil, err
	}
	var entries []*ledgerEntry
	for rows.Next() {
		e, err := scanLedgerEntry(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, s.loadLedgerTargets(ctx, entries)
}

// ledgerActionNames is how each action reads in a ledger message.
var ledgerActionNames = map[string]string{
	adminActionSuspend:     "Suspension",
	adminActionReinstate:   "Reinstatement",
	adminActionKick:        "Kick",
	adminActionPromote:     "Promotion",
	adminActionDemote:      "Demotion",
	adminActionPermissions: "Permission change",
}

// ledgerApprovedEntry is the entry for req approved by approverID, nil for
// a private message. rebindKeyID names the key a rebind adds.
func ledgerApprovedEntry(req *adminRequest, approverID, rebindKeyID string) *ledgerEntry {
	e := &ledgerEntry{
		Kind:        ledgerKindRequest,
		UserID:      approverID,
		RequesterID: req.RequesterID,
		RequestedAt: req.RequesterServerSignature.SignedAt,
		Targets:     req.Targets,
	}
	switch req.Action {
	case adminActionUnicast:
		return nil
	case adminActionBroadcast:
		e.Message = fmt.Sprintf("[%s] Broadcast", req.ID)
		e.Content = req.Body
	case adminActionRebind:
		e.Message = fmt.Sprintf("[%s] Key rebind to %s. Reason: %s", req.ID, rebindKeyID, req.Reason)
	case adminActionPromote, adminActionPermissions:
		e.Message = fmt.Sprintf("[%s] %s. Reason: %s", req.ID, ledgerActionNames[req.Action], req.Reason)
		e.Content = req.Body
	default:
		e.Message = fmt.Sprintf("[%s] %s. Reason: %s", req.ID, ledgerActionNames[req.Action], req.Reason)
	}
	return e
}

// appendLedgerTx signs and stores e in tx.
func (h *Handlers) appendLedgerTx(ctx context.Context, tx *sql.Tx, e *ledgerEntry) error {
	return appendLedgerEntryTx(ctx, tx, h.countersign, h.services.db.GetServerID(), h.signingKey.Fingerprint, e)
}

func pbLedgerEntry(e *ledgerEntry) *pb.LedgerEntry {
	out := &pb.LedgerEntry{
		Id:              e.ID,
		Kind:            e.Kind,
		UserId:          e.UserID,
		RequesterId:     e.RequesterID,
		RequestedAt:     unixOrZero(e.RequestedAt),
		Message:         e.Message,
		Content:         e.Content,
		Targets:         e.Targets,
		ServerSignature: pbServerSignature(e.ServerSignature),
	}
	if e.PrevID != 0 {
		prev := e.PrevID
		out.PrevId = &prev
	}
	return out
}

func newLedgerEntryMsg(e *ledgerEntry) *pb.WSMessage {
	return &pb.WSMessage{
		Type:    pb.MessageType_LEDGER_ENTRY,
		Payload: &pb.WSMessage_LedgerEntry{LedgerEntry: &pb.LedgerEntryMessage{Entry: pbLedgerEntry(e)}},
	}
}

// pushLedgerEntries sends committed entries to every connected member.
// One that misses it backfills from the API.
func (h *Handlers) pushLedgerEntries(entries ...*ledgerEntry) {
	for _, e := range entries {
		if e == nil || e.ID == 0 {
			continue
		}
		h.metrics.LedgerAppended(context.Background(), e.Kind)
		if h.realtimeRelay == nil {
			continue
		}
		if err := h.realtimeRelay.connManager.SendToAll(newLedgerEntryMsg(e)); err != nil {
			log.Error().Err(err).Uint64("entryID", e.ID).Msg("Failed to push ledger entry")
		}
	}
}

// ledgerReader answers 401 unless a signed-in local member is reading.
func (h *Handlers) ledgerReader(w http.ResponseWriter, r *http.Request) bool {
	userID, _ := r.Context().Value(userIDKey).(string)
	if _, serverID, ok := parseIdentityID(identityID(userID)); !ok || serverID != h.services.db.GetServerID() {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return false
	}
	return true
}

// GetLedger handles GET /ledger: the head and the first page of the
// chain, or with ?after={id} the page that follows that entry.
func (h *Handlers) GetLedger(w http.ResponseWriter, r *http.Request) {
	if !h.ledgerReader(w, r) {
		return
	}
	page := &pb.LedgerPage{}
	var afterID uint64
	if raw := r.URL.Query().Get("after"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "after must be an entry ID")
			return
		}
		after, err := h.services.db.GetLedgerEntry(r.Context(), id)
		if err != nil {
			h.writeAdminError(w, r, err, "Error loading ledger entry")
			return
		}
		if after == nil {
			writeError(w, http.StatusNotFound, "Entry not found")
			return
		}
		afterID = id
	} else {
		head, err := h.services.db.GetLedgerHead(r.Context())
		if err != nil {
			h.writeAdminError(w, r, err, "Error loading ledger head")
			return
		}
		if head == nil {
			writeResponse(w, http.StatusOK, page)
			return
		}
		page.Head = pbLedgerEntry(head)
		afterID = head.ID
	}
	tail, err := h.services.db.GetLedgerTail(r.Context(), afterID, ledgerPageSize)
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading ledger")
		return
	}
	for _, e := range tail {
		page.Tail = append(page.Tail, pbLedgerEntry(e))
	}
	writeResponse(w, http.StatusOK, page)
}

// GetLedgerEntryHandler handles GET /ledger/{id}.
func (h *Handlers) GetLedgerEntryHandler(w http.ResponseWriter, r *http.Request) {
	if !h.ledgerReader(w, r) {
		return
	}
	id, err := strconv.ParseUint(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be an entry ID")
		return
	}
	e, err := h.services.db.GetLedgerEntry(r.Context(), id)
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading ledger entry")
		return
	}
	if e == nil {
		writeError(w, http.StatusNotFound, "Entry not found")
		return
	}
	writeResponse(w, http.StatusOK, pbLedgerEntry(e))
}

// requirePermission answers 403 unless userID holds perm.
func (h *Handlers) requirePermission(w http.ResponseWriter, r *http.Request, userID, perm string) bool {
	perms, err := h.services.db.GetUserPermissions(r.Context(), userID)
	if err != nil {
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("userID", userID).Msg("Error loading permissions")
		internalServerError(w)
		return false
	}
	if !slices.Contains(perms, perm) {
		writeError(w, http.StatusForbidden, "Missing permission "+perm)
		return false
	}
	return true
}
