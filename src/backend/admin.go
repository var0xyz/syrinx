//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/lib/pq"
	"google.golang.org/protobuf/proto"

	"syrinx/observability/metrics"
	pb "syrinx/proto"
)

// Permissions an admin can hold. Root holds every one of them.
const (
	permAdminCreate      = "admin.create"
	permAdminDemote      = "admin.demote"
	permUserKick         = "user.kick"
	permUserSuspend      = "user.suspend"
	permUserRebind       = "user.rebind"
	permMessageUnicast   = "message.unicast"
	permMessageBroadcast = "message.broadcast"
	permMeshInvite       = "mesh.invite"
	permMeshConnect      = "mesh.connect"
	permMeshDisconnect   = "mesh.disconnect"
)

// adminPermissions lists every permission, in display order.
var adminPermissions = []string{
	permAdminCreate, permAdminDemote,
	permUserKick, permUserSuspend, permUserRebind,
	permMessageUnicast, permMessageBroadcast,
	permMeshInvite, permMeshConnect, permMeshDisconnect,
}

// parsePermissionSet reads a permission set written as sorted, comma-joined
// names. Any other form is refused, so the signed bytes have one spelling.
func parsePermissionSet(body string) ([]string, error) {
	if body == "" {
		return nil, nil
	}
	perms := strings.Split(body, ",")
	for i, p := range perms {
		if !slices.Contains(adminPermissions, p) {
			return nil, fmt.Errorf("unknown permission %q", p)
		}
		if i > 0 && perms[i-1] >= p {
			return nil, fmt.Errorf("permissions must be sorted and unique")
		}
	}
	return perms, nil
}

// formatPermissionSet writes perms in the one form parsePermissionSet accepts.
func formatPermissionSet(perms []string) string {
	sorted := slices.Clone(perms)
	sort.Strings(sorted)
	return strings.Join(slices.Compact(sorted), ",")
}

// Admin request actions.
const (
	adminActionSuspend     = "suspend"
	adminActionReinstate   = "reinstate"
	adminActionKick        = "kick"
	adminActionPromote     = "promote"
	adminActionDemote      = "demote"
	adminActionPermissions = "permissions"
	adminActionRebind      = "rebind"
	adminActionBroadcast   = "broadcast"
	adminActionUnicast     = "unicast"
)

// adminActionPermission is the permission each action needs, except
// `permissions`, whose depends on what it adds and removes.
var adminActionPermission = map[string]string{
	adminActionSuspend:   permUserSuspend,
	adminActionReinstate: permUserSuspend,
	adminActionKick:      permUserKick,
	adminActionPromote:   permAdminCreate,
	adminActionDemote:    permAdminDemote,
	adminActionRebind:    permUserRebind,
	adminActionBroadcast: permMessageBroadcast,
	adminActionUnicast:   permMessageUnicast,
}

// mailActions are the requests an admin sees only while holding their
// permission, or as their requester.
var mailActions = []string{adminActionBroadcast, adminActionUnicast}

// hiddenAdminActions lists the mail actions perms doesn't cover.
func hiddenAdminActions(perms []string) []string {
	var hidden []string
	for _, a := range mailActions {
		if !slices.Contains(perms, adminActionPermission[a]) {
			hidden = append(hidden, a)
		}
	}
	return hidden
}

// Request states and decisions.
const (
	adminStatePending   = "pending"
	adminStateExecuted  = "executed"
	adminStateRejected  = "rejected"
	adminStateWithdrawn = "withdrawn"
	adminStateMoot      = "moot"

	adminDecisionApprove  = "approve"
	adminDecisionReject   = "reject"
	adminDecisionWithdraw = "withdraw"
)

// maxAdminReasonChars caps a request's public reason.
const maxAdminReasonChars = 1000

// adminRequestClockSkew bounds how far a request's signed createdAt may be
// from the server's clock.
const adminRequestClockSkew = 5 * time.Minute

// remainingTargets are the targets that haven't been dropped.
func (r *adminRequest) remainingTargets() []string {
	return slices.DeleteFunc(slices.Clone(r.Targets), func(t string) bool {
		return slices.Contains(r.Dropped, t)
	})
}

// payload is the bytes the requester signed.
func (r *adminRequest) payload() []byte {
	return buildAdminRequestPayload(r.ID, r.Action, r.RequesterID, r.RequesterSignature.ID,
		r.Reason, r.Targets, r.Body, r.CreatedAt)
}

// decisionForState is the decision that closes a request in state, if any.
func decisionForState(state string) string {
	switch state {
	case adminStateExecuted:
		return adminDecisionApprove
	case adminStateRejected:
		return adminDecisionReject
	case adminStateWithdrawn:
		return adminDecisionWithdraw
	}
	return ""
}

// stateForDecision is the state a decision closes a request in.
func stateForDecision(decision string) string {
	switch decision {
	case adminDecisionApprove:
		return adminStateExecuted
	case adminDecisionReject:
		return adminStateRejected
	case adminDecisionWithdraw:
		return adminStateWithdrawn
	}
	return ""
}

// adminRefusal is a request or decision the rules refuse, with the status,
// a fixed reason code for metrics, and the rule that failed.
type adminRefusal struct {
	status  int
	reason  string
	message string
}

func (e *adminRefusal) Error() string { return e.message }

func refuse(status int, reason, format string, args ...any) error {
	return &adminRefusal{status: status, reason: reason, message: fmt.Sprintf(format, args...)}
}

// recordAdminRefusal counts err against action when it's a refusal.
func (h *Handlers) recordAdminRefusal(ctx context.Context, err error, action, actorID string, stage metrics.AdminStage) {
	var refusal *adminRefusal
	if errors.As(err, &refusal) {
		h.metrics.AdminRequestRefused(ctx, action, actorID, stage, refusal.reason)
	}
}

// writeAdminError answers err as its refusal, or as a 500.
func (h *Handlers) writeAdminError(w http.ResponseWriter, r *http.Request, err error, msg string) {
	var refusal *adminRefusal
	if errors.As(err, &refusal) {
		writeError(w, refusal.status, refusal.message)
		return
	}
	h.services.log.GetLogger(r.Context()).Error().Err(err).Msg(msg)
	internalServerError(w)
}

// withAdminTx runs fn in a transaction, committing when it returns nil.
func withAdminTx(db *sql.DB, ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// adminQuerier is a *sql.DB or a *sql.Tx.
type adminQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// adminExecutor runs one action. validate checks the request still fits
// its targets, when it is opened and again at approval; execute runs in the
// approval transaction; after runs once it has committed.
type adminExecutor struct {
	validate func(ctx context.Context, q adminQuerier, req *adminRequest) error
	execute  func(ctx context.Context, tx *sql.Tx, req *adminRequest) error
	after    func(req *adminRequest)
}

// adminExecutor returns the executor for action, nil while none exists.
func (h *Handlers) adminExecutor(action string) *adminExecutor {
	if ex, ok := h.adminExecutorOverride[action]; ok {
		return &ex
	}
	var ex adminExecutor
	switch action {
	case adminActionSuspend:
		ex = h.suspendExecutor()
	case adminActionReinstate:
		ex = h.reinstateExecutor()
	case adminActionKick:
		ex = h.kickExecutor()
	case adminActionPromote:
		ex = h.promoteExecutor()
	case adminActionPermissions:
		ex = h.permissionsExecutor()
	case adminActionDemote:
		ex = h.demoteExecutor()
	case adminActionRebind:
		ex = h.rebindExecutor()
	case adminActionBroadcast:
		ex = h.broadcastExecutor()
	case adminActionUnicast:
		ex = h.unicastExecutor()
	default:
		return nil
	}
	return &ex
}

// userRoleQ is the role of userID, "" when they have no users row.
func userRoleQ(ctx context.Context, q adminQuerier, userID string) (string, error) {
	var role string
	err := q.QueryRowContext(ctx, `SELECT role FROM users WHERE id = $1`, userID).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return role, err
}

// userPermissionsQ returns userID's permissions, sorted. Root holds all.
func userPermissionsQ(ctx context.Context, q adminQuerier, userID, role, serverID string) ([]string, error) {
	if isRoot(userID, role, serverID) {
		perms := slices.Clone(adminPermissions)
		sort.Strings(perms)
		return perms, nil
	}
	if role != roleAdmin {
		return nil, nil
	}
	rows, err := q.QueryContext(ctx, `
		SELECT permission FROM user_permissions WHERE user_id = $1 ORDER BY permission COLLATE "C"
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		perms = append(perms, p)
	}
	return perms, rows.Err()
}

// GetUserPermissions returns userID's permissions, sorted. Root holds all.
func (s *DataService) GetUserPermissions(ctx context.Context, userID string) ([]string, error) {
	role, err := userRoleQ(ctx, s.db, userID)
	if err != nil {
		return nil, err
	}
	return userPermissionsQ(ctx, s.db, userID, role, s.serverID)
}

// invitedByQ reports whether inviterID directly invited userID.
func invitedByQ(ctx context.Context, q adminQuerier, inviterID, userID string) (bool, error) {
	var found bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users u
			WHERE u.id = $2 AND invite_creator(u.invite_id) = $1
		)
	`, inviterID, userID).Scan(&found)
	return found, err
}

// adminActionNeedsOutsideApprover lists the actions a requester's direct
// invitee can't decide on: kicks and permission changes.
var adminActionNeedsOutsideApprover = map[string]bool{
	adminActionKick:        true,
	adminActionPromote:     true,
	adminActionDemote:      true,
	adminActionPermissions: true,
}

// hasAccountRemovalQ reports whether userID's account was removed.
func hasAccountRemovalQ(ctx context.Context, q adminQuerier, userID string) (bool, error) {
	var removed bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM account_removals WHERE user_id = $1)
	`, userID).Scan(&removed)
	return removed, err
}

const adminRequestSelect = `
	SELECT r.id, r.action, r.requester_id, COALESCE(r.reason, ''), r.created_at, r.state,
		us.public_key_id, us.signature,
		rs.private_key_id, rs.signature, rs.signed_at,
		COALESCE(c.body, ''),
		r.closed_by, cs.public_key_id, cs.signature,
		ss.private_key_id, ss.signature, ss.signed_at
	FROM admin_requests r
	JOIN user_signatures us ON us.id = r.requester_signature_id
	JOIN server_signatures rs ON rs.id = r.requester_server_signature_id
	LEFT JOIN admin_request_content c ON c.request_id = r.id
	LEFT JOIN user_signatures cs ON cs.id = r.closer_signature_id
	LEFT JOIN server_signatures ss ON ss.id = r.server_signature_id`

func scanAdminRequest(row interface{ Scan(dest ...any) error }) (*adminRequest, error) {
	var req adminRequest
	var closedBy, closerKeyID, closerSig, serverKeyID, serverSig sql.NullString
	var signedAt sql.NullTime
	err := row.Scan(
		&req.ID, &req.Action, &req.RequesterID, &req.Reason, &req.CreatedAt, &req.State,
		&req.RequesterSignature.ID, &req.RequesterSignature.Armor,
		&req.RequesterServerSignature.ID, &req.RequesterServerSignature.Armor, &req.RequesterServerSignature.SignedAt,
		&req.Body,
		&closedBy, &closerKeyID, &closerSig,
		&serverKeyID, &serverSig, &signedAt,
	)
	if err != nil {
		return nil, err
	}
	req.CreatedAt = req.CreatedAt.UTC()
	req.RequesterServerSignature.SignedAt = req.RequesterServerSignature.SignedAt.UTC()
	if closedBy.Valid && closerSig.Valid {
		req.Decision = &adminDecision{
			Decision:  decisionForState(req.State),
			DeciderID: closedBy.String,
			Signature: UserSignature{ID: closerKeyID.String, Armor: closerSig.String},
		}
	}
	if serverSig.Valid {
		req.ServerSignature = &ServerSignature{ID: serverKeyID.String, Armor: serverSig.String, SignedAt: signedAt.Time.UTC()}
	}
	return &req, nil
}

// loadAdminTargetsQ fills req's targets and dropped targets.
func loadAdminTargetsQ(ctx context.Context, q adminQuerier, req *adminRequest) error {
	rows, err := q.QueryContext(ctx, `
		SELECT user_id, dropped FROM admin_request_targets WHERE request_id = $1
	`, req.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	req.Targets, req.Dropped = nil, nil
	for rows.Next() {
		var userID string
		var dropped bool
		if err := rows.Scan(&userID, &dropped); err != nil {
			return err
		}
		req.Targets = append(req.Targets, userID)
		if dropped {
			req.Dropped = append(req.Dropped, userID)
		}
	}
	sort.Strings(req.Targets)
	sort.Strings(req.Dropped)
	return rows.Err()
}

// getAdminRequestQ loads one request with its targets, nil when unknown.
// forUpdate locks the request row for the rest of the transaction.
func getAdminRequestQ(ctx context.Context, q adminQuerier, id string, forUpdate bool) (*adminRequest, error) {
	query := adminRequestSelect + ` WHERE r.id = $1`
	if forUpdate {
		query += ` FOR UPDATE OF r`
	}
	req, err := scanAdminRequest(q.QueryRowContext(ctx, query, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return req, loadAdminTargetsQ(ctx, q, req)
}

// GetAdminRequest loads one request, nil when unknown.
func (s *DataService) GetAdminRequest(ctx context.Context, id string) (*adminRequest, error) {
	return getAdminRequestQ(ctx, s.db, id, false)
}

// ErrAdminRequestExists: a request with that ID was already opened.
var ErrAdminRequestExists = errors.New("admin request already exists")

// insertAdminRequestTx stores a newly opened request with its targets,
// content and the server's countersignature.
func insertAdminRequestTx(ctx context.Context, tx *sql.Tx, req *adminRequest) error {
	sigID, err := insertUserSignature(ctx, tx, req.RequesterSignature.ID, req.RequesterSignature.Armor)
	if err != nil {
		return err
	}
	ss := req.RequesterServerSignature
	serverSigID, err := insertServerSignature(ctx, tx, ss.ID, ss.Armor, ss.SignedAt)
	if err != nil {
		return err
	}
	var reason sql.NullString
	if req.Reason != "" {
		reason = sql.NullString{String: req.Reason, Valid: true}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO admin_requests (
			id, action, requester_id, requester_key_id, requester_signature_id,
			requester_server_signature_id, reason, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, req.ID, req.Action, req.RequesterID, req.RequesterSignature.ID, sigID, serverSigID, reason, req.CreatedAt)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return ErrAdminRequestExists
	}
	if err != nil {
		return fmt.Errorf("insert admin request: %w", err)
	}
	if req.Body != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO admin_request_content (request_id, body) VALUES ($1, $2)
		`, req.ID, req.Body); err != nil {
			return fmt.Errorf("insert admin request content: %w", err)
		}
	}
	for _, target := range req.Targets {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO admin_request_targets (request_id, user_id) VALUES ($1, $2)
		`, req.ID, target); err != nil {
			return fmt.Errorf("insert admin request target: %w", err)
		}
	}
	return nil
}

// closeAdminRequestTx records how a pending request closed: its decision,
// when there is one, and the server's countersignature.
func closeAdminRequestTx(ctx context.Context, tx *sql.Tx, req *adminRequest, state string, decision *adminDecision, serverSig ServerSignature) error {
	serverSigID, err := insertServerSignature(ctx, tx, serverSig.ID, serverSig.Armor, serverSig.SignedAt)
	if err != nil {
		return err
	}
	var closedBy, closerKeyID sql.NullString
	var closerSigID sql.NullInt64
	if decision != nil {
		id, err := insertUserSignature(ctx, tx, decision.Signature.ID, decision.Signature.Armor)
		if err != nil {
			return err
		}
		closedBy = sql.NullString{String: decision.DeciderID, Valid: true}
		closerKeyID = sql.NullString{String: decision.Signature.ID, Valid: true}
		closerSigID = sql.NullInt64{Int64: id, Valid: true}
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE admin_requests
		SET state = $2, closed_by = $3, closer_key_id = $4, closer_signature_id = $5,
			server_signature_id = $6, closed_at = $7
		WHERE id = $1 AND state = 'pending'
	`, req.ID, state, closedBy, closerKeyID, closerSigID, serverSigID, serverSig.SignedAt.UTC())
	if err != nil {
		return fmt.Errorf("close admin request: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return refuse(http.StatusConflict, "not_pending", "Request is no longer pending")
	}
	req.State = state
	req.Decision = decision
	req.ServerSignature = &serverSig
	return nil
}

// adminRequestCursor pages by the signed createdAt, then ID.
type adminRequestCursor struct {
	CreatedAt time.Time `json:"c"`
	ID        string    `json:"i"`
}

func encodeAdminRequestCursor(c adminRequestCursor) string {
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeAdminRequestCursor(s string) (*adminRequestCursor, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor encoding: %w", err)
	}
	var c adminRequestCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("invalid cursor payload: %w", err)
	}
	return &c, nil
}

// ListAdminRequests pages requests newest first. state is "pending",
// "closed" or "" for both.
// ListAdminRequests leaves out requests for hidden actions, except viewerID's own.
func (s *DataService) ListAdminRequests(ctx context.Context, state, cursor string, limit int, viewerID string, hidden []string) ([]*adminRequest, string, error) {
	where := []string{"TRUE"}
	var args []any
	if len(hidden) > 0 {
		args = append(args, pq.Array(hidden), viewerID)
		where = append(where, fmt.Sprintf("(r.action <> ALL($%d) OR r.requester_id = $%d)", len(args)-1, len(args)))
	}
	switch state {
	case adminStatePending:
		where = append(where, "r.state = 'pending'")
	case "closed":
		where = append(where, "r.state <> 'pending'")
	}
	if cursor != "" {
		c, err := decodeAdminRequestCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, c.CreatedAt, c.ID)
		where = append(where, fmt.Sprintf("(r.created_at, r.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	query := adminRequestSelect + ` WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY r.created_at DESC, r.id DESC LIMIT $%d`, len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	var reqs []*adminRequest
	for rows.Next() {
		req, err := scanAdminRequest(rows)
		if err != nil {
			rows.Close()
			return nil, "", err
		}
		reqs = append(reqs, req)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(reqs) > limit {
		reqs = reqs[:limit]
		last := reqs[limit-1]
		next = encodeAdminRequestCursor(adminRequestCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	for _, req := range reqs {
		if err := loadAdminTargetsQ(ctx, s.db, req); err != nil {
			return nil, "", err
		}
	}
	return reqs, next, nil
}

// targetCountFits checks how many targets action takes.
func targetCountFits(action string, n int) bool {
	switch action {
	case adminActionBroadcast:
		return n == 0
	case adminActionUnicast, adminActionPromote, adminActionDemote, adminActionPermissions, adminActionRebind:
		return n == 1
	}
	return n >= 1
}

// grantedPermissions is the permission set a promote or permissions
// request grants, nil for every other action.
func grantedPermissions(req *adminRequest) ([]string, error) {
	if req.Action != adminActionPromote && req.Action != adminActionPermissions {
		return nil, nil
	}
	return parsePermissionSet(req.Body)
}

// requiredPermissionsQ returns the permissions acting on req needs. A
// permissions change needs admin.create to add and admin.demote to remove.
func requiredPermissionsQ(ctx context.Context, q adminQuerier, req *adminRequest, serverID string) ([]string, error) {
	if req.Action != adminActionPermissions {
		return []string{adminActionPermission[req.Action]}, nil
	}
	next, err := grantedPermissions(req)
	if err != nil {
		return nil, refuse(http.StatusBadRequest, "bad_permissions", "Invalid permission set: %v", err)
	}
	target := req.Targets[0]
	role, err := userRoleQ(ctx, q, target)
	if err != nil {
		return nil, err
	}
	current, err := userPermissionsQ(ctx, q, target, role, serverID)
	if err != nil {
		return nil, err
	}
	var required []string
	for _, p := range next {
		if !slices.Contains(current, p) {
			required = append(required, permAdminCreate)
			break
		}
	}
	for _, p := range current {
		if !slices.Contains(next, p) {
			required = append(required, permAdminDemote)
			break
		}
	}
	if len(required) == 0 {
		return nil, refuse(http.StatusConflict, "permissions_unchanged", "The permissions are unchanged")
	}
	return required, nil
}

// checkActorPermissionsQ refuses actorID unless they hold every permission
// acting on req needs and every permission it grants.
func checkActorPermissionsQ(ctx context.Context, q adminQuerier, req *adminRequest, actorID, actorRole, serverID string) error {
	held, err := userPermissionsQ(ctx, q, actorID, actorRole, serverID)
	if err != nil {
		return err
	}
	required, err := requiredPermissionsQ(ctx, q, req, serverID)
	if err != nil {
		return err
	}
	granted, err := grantedPermissions(req)
	if err != nil {
		return refuse(http.StatusBadRequest, "bad_permissions", "Invalid permission set: %v", err)
	}
	for _, p := range append(required, granted...) {
		if !slices.Contains(held, p) {
			return refuse(http.StatusForbidden, "missing_permission", "Missing permission %s", p)
		}
	}
	return nil
}

// validateNewAdminRequest checks a request before it is opened, everything
// but its signature.
func (h *Handlers) validateNewAdminRequest(ctx context.Context, req *adminRequest, requesterRole string) error {
	q := h.services.db.db
	serverID := h.services.db.GetServerID()

	if _, err := uuid.Parse(req.ID); err != nil {
		return refuse(http.StatusBadRequest, "bad_id", "Request ID must be a UUID")
	}
	if _, ok := adminActionPermission[req.Action]; !ok && req.Action != adminActionPermissions {
		return refuse(http.StatusBadRequest, "unknown_action", "Unknown action")
	}
	if d := time.Since(req.CreatedAt); d > adminRequestClockSkew || d < -adminRequestClockSkew {
		return refuse(http.StatusBadRequest, "clock_skew", "createdAt is too far from the server's clock")
	}
	if !slices.IsSorted(req.Targets) || len(slices.Compact(slices.Clone(req.Targets))) != len(req.Targets) {
		return refuse(http.StatusBadRequest, "bad_targets", "Targets must be sorted and unique")
	}
	if !targetCountFits(req.Action, len(req.Targets)) {
		return refuse(http.StatusBadRequest, "bad_targets", "Wrong number of targets for %s", req.Action)
	}
	for _, target := range req.Targets {
		if _, targetServerID, ok := parseIdentityID(identityID(target)); !ok || targetServerID != serverID {
			return refuse(http.StatusBadRequest, "foreign_target", "Target %s isn't a local user", target)
		}
		if isRootIdentity(target, serverID) {
			return refuse(http.StatusForbidden, "root_target", "Root can't be a target")
		}
		role, err := userRoleQ(ctx, q, target)
		if err != nil {
			return err
		}
		if role == "" {
			return refuse(http.StatusNotFound, "target_not_found", "Target %s not found", target)
		}
		removed, err := hasAccountRemovalQ(ctx, q, target)
		if err != nil {
			return err
		}
		if removed {
			return refuse(http.StatusGone, "target_removed", "Target %s was removed", target)
		}
	}

	noReason := req.Action == adminActionBroadcast || req.Action == adminActionUnicast
	reasonLen := countCharacters(req.Reason)
	if noReason && req.Reason != "" {
		return refuse(http.StatusBadRequest, "bad_reason", "A %s has no reason", req.Action)
	}
	if !noReason && (strings.TrimSpace(req.Reason) == "" || reasonLen > maxAdminReasonChars) {
		return refuse(http.StatusBadRequest, "bad_reason", "A reason of 1 to %d characters is required", maxAdminReasonChars)
	}

	switch req.Action {
	case adminActionBroadcast, adminActionUnicast:
		if strings.TrimSpace(req.Body) == "" || countCharacters(req.Body) > MaxMailboxMessageChars {
			return refuse(http.StatusBadRequest, "bad_message", "A message of 1 to %d characters is required", MaxMailboxMessageChars)
		}
	case adminActionPromote, adminActionPermissions:
		if _, err := parsePermissionSet(req.Body); err != nil {
			return refuse(http.StatusBadRequest, "bad_permissions", "Invalid permission set: %v", err)
		}
	case adminActionRebind:
		if strings.TrimSpace(req.Body) == "" {
			return refuse(http.StatusBadRequest, "bad_body", "A public key is required")
		}
	default:
		if req.Body != "" {
			return refuse(http.StatusBadRequest, "bad_body", "A %s has no body", req.Action)
		}
	}

	if !isAdmin(requesterRole) {
		return refuse(http.StatusForbidden, "not_admin", "Admin required")
	}
	if err := checkActorPermissionsQ(ctx, q, req, req.RequesterID, requesterRole, serverID); err != nil {
		return err
	}

	ex := h.adminExecutor(req.Action)
	if ex == nil {
		return refuse(http.StatusNotImplemented, "unavailable", "The %s action isn't available", req.Action)
	}
	if ex.validate != nil {
		return ex.validate(ctx, q, req)
	}
	return nil
}

// checkDeciderQ applies the approver rules to deciderID approving or
// rejecting req. Roles and permissions are read now, not when it opened.
func checkDeciderQ(ctx context.Context, q adminQuerier, req *adminRequest, deciderID, serverID string) error {
	role, err := userRoleQ(ctx, q, deciderID)
	if err != nil {
		return err
	}
	if !isAdmin(role) {
		return refuse(http.StatusForbidden, "not_admin", "Only an admin can decide on a request")
	}
	deciderIsRoot := isRoot(deciderID, role, serverID)
	if deciderID == req.RequesterID && !deciderIsRoot {
		return refuse(http.StatusForbidden, "self_decision", "The requester can't decide on their own request")
	}
	if slices.Contains(req.Targets, deciderID) {
		return refuse(http.StatusForbidden, "target_decision", "A target can't decide on the request")
	}
	if adminActionNeedsOutsideApprover[req.Action] {
		invited, err := invitedByQ(ctx, q, req.RequesterID, deciderID)
		if err != nil {
			return err
		}
		if invited {
			return refuse(http.StatusForbidden, "invitee_decision", "Someone the requester invited can't decide on a kick or a permission change")
		}
	}
	if err := checkActorPermissionsQ(ctx, q, req, deciderID, role, serverID); err != nil {
		return err
	}
	if !deciderIsRoot {
		for _, target := range req.remainingTargets() {
			targetRole, err := userRoleQ(ctx, q, target)
			if err != nil {
				return err
			}
			if targetRole == roleAdmin {
				return refuse(http.StatusForbidden, "root_required", "Only root can decide on a request about an admin")
			}
		}
	}
	return nil
}

func pbAdminRequest(req *adminRequest) *pb.AdminRequest {
	out := &pb.AdminRequest{
		Id:                 req.ID,
		Action:             req.Action,
		RequesterId:        req.RequesterID,
		Reason:             req.Reason,
		Targets:            req.Targets,
		Body:               req.Body,
		CreatedAt:          unixOrZero(req.CreatedAt),
		RequesterSignature: pbUserSignature(req.RequesterSignature),
		State:              req.State,
		DroppedTargets:     req.Dropped,
	}
	if req.Decision != nil {
		out.Decision = &pb.AdminDecision{
			Decision:  req.Decision.Decision,
			DeciderId: req.Decision.DeciderID,
			Signature: pbUserSignature(req.Decision.Signature),
		}
	}
	if req.ServerSignature != nil {
		out.ServerSignature = pbServerSignature(*req.ServerSignature)
	}
	return out
}

// adminCaller returns the signed-in caller and their role, answering 401
// when there is none.
func (h *Handlers) adminCaller(w http.ResponseWriter, r *http.Request) (userID, role string, ok bool) {
	userID, _ = r.Context().Value(userIDKey).(string)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return "", "", false
	}
	role, err := h.services.db.GetUserRole(r.Context(), userID)
	if err != nil {
		h.services.log.GetLogger(r.Context()).Error().Err(err).Str("userID", userID).Msg("Error loading role")
		internalServerError(w)
		return "", "", false
	}
	return userID, role, true
}

// callerSigningKey resolves keyID as one of userID's own, unrevoked keys.
func (h *Handlers) callerSigningKey(ctx context.Context, keyID, userID string) (*Key, error) {
	key, err := h.services.db.GetPublicKey(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if key == nil || key.UserID != userID || key.Revoked {
		return nil, refuse(http.StatusUnauthorized, "bad_key", "Signing key isn't an active key of the caller")
	}
	return key, nil
}

// OpenAdminRequest handles POST /admin/requests: an admin signs a request
// that a second admin must approve.
func (h *Handlers) OpenAdminRequest(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	callerID, role, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	if !isAdmin(role) {
		h.metrics.AdminRequestRefused(r.Context(), "", callerID, metrics.AdminStageOpen, "not_admin")
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	var msg pb.AdminRequest
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	if msg.GetRequesterId() != callerID {
		writeError(w, http.StatusForbidden, "requesterID must be the caller")
		return
	}
	req := &adminRequest{
		ID:          strings.TrimSpace(msg.GetId()),
		Action:      msg.GetAction(),
		RequesterID: callerID,
		Reason:      msg.GetReason(),
		Targets:     msg.GetTargets(),
		Body:        msg.GetBody(),
		CreatedAt:   timeFromUnix(msg.GetCreatedAt()),
		RequesterSignature: UserSignature{
			ID:    msg.GetRequesterSignature().GetId(),
			Armor: msg.GetRequesterSignature().GetArmor(),
		},
		State: adminStatePending,
	}
	if err := h.validateNewAdminRequest(r.Context(), req, role); err != nil {
		h.recordAdminRefusal(r.Context(), err, req.Action, callerID, metrics.AdminStageOpen)
		h.writeAdminError(w, r, err, "Error validating admin request")
		return
	}
	key, err := h.callerSigningKey(r.Context(), req.RequesterSignature.ID, callerID)
	if err != nil {
		h.recordAdminRefusal(r.Context(), err, req.Action, callerID, metrics.AdminStageOpen)
		h.writeAdminError(w, r, err, "Error loading signing key")
		return
	}
	if err := h.services.crypto.verifySignature(string(req.payload()), req.RequesterSignature.Armor, key.Armor); err != nil {
		h.metrics.AdminRequestRefused(r.Context(), req.Action, callerID, metrics.AdminStageOpen, "bad_signature")
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	req.RequesterServerSignature, err = h.countersign(buildAdminRequestOpenedPayload(req.ID, req.RequesterSignature.Armor,
		h.services.db.GetServerID(), h.signingKey.Fingerprint, now), now)
	if err != nil {
		log.Error().Err(err).Msg("Error countersigning admin request")
		internalServerError(w)
		return
	}
	err = withAdminTx(h.services.db.db, r.Context(), func(tx *sql.Tx) error {
		return insertAdminRequestTx(r.Context(), tx, req)
	})
	if errors.Is(err, ErrAdminRequestExists) {
		h.metrics.AdminRequestRefused(r.Context(), req.Action, callerID, metrics.AdminStageOpen, "exists")
		writeError(w, http.StatusConflict, "A request with that ID already exists")
		return
	}
	if err != nil {
		h.writeAdminError(w, r, err, "Error storing admin request")
		return
	}
	log.Info().Str("requestID", req.ID).Str("action", req.Action).Str("requesterID", callerID).Msg("Admin request opened")
	h.metrics.AdminRequestOpened(r.Context(), req.Action, callerID, role, len(req.Targets))
	writeResponse(w, http.StatusCreated, pbAdminRequest(req))
}

// ListAdminRequests handles GET /admin/requests?state=pending|closed.
func (h *Handlers) ListAdminRequests(w http.ResponseWriter, r *http.Request) {
	callerID, role, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	if !isAdmin(role) {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	query := r.URL.Query()
	state := query.Get("state")
	if state != "" && state != adminStatePending && state != "closed" {
		writeError(w, http.StatusBadRequest, "state must be pending or closed")
		return
	}
	perms, err := userPermissionsQ(r.Context(), h.services.db.db, callerID, role, h.services.db.GetServerID())
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading permissions")
		return
	}
	reqs, next, err := h.services.db.ListAdminRequests(r.Context(), state, query.Get("cursor"), 50, callerID, hiddenAdminActions(perms))
	if err != nil {
		h.writeAdminError(w, r, err, "Error listing admin requests")
		return
	}
	out := &pb.AdminRequestList{NextCursor: next}
	for _, req := range reqs {
		out.Requests = append(out.Requests, pbAdminRequest(req))
	}
	writeResponse(w, http.StatusOK, out)
}

// GetAdminRequest handles GET /admin/requests/{id}.
func (h *Handlers) GetAdminRequest(w http.ResponseWriter, r *http.Request) {
	callerID, role, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	if !isAdmin(role) {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	req, err := h.services.db.GetAdminRequest(r.Context(), mux.Vars(r)["id"])
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading admin request")
		return
	}
	if req == nil {
		writeError(w, http.StatusNotFound, "Request not found")
		return
	}
	perms, err := userPermissionsQ(r.Context(), h.services.db.db, callerID, role, h.services.db.GetServerID())
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading permissions")
		return
	}
	if req.RequesterID != callerID && slices.Contains(hiddenAdminActions(perms), req.Action) {
		writeError(w, http.StatusNotFound, "Request not found")
		return
	}
	writeResponse(w, http.StatusOK, pbAdminRequest(req))
}

// DecideAdminRequest handles POST /admin/requests/{id}/decision: an
// approval or rejection by an eligible admin, or the requester's withdrawal.
func (h *Handlers) DecideAdminRequest(w http.ResponseWriter, r *http.Request) {
	log := h.services.log.GetLogger(r.Context())
	callerID, _, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	var msg pb.AdminDecision
	if err := readRequest(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	decision := &adminDecision{
		Decision:  msg.GetDecision(),
		DeciderID: msg.GetDeciderId(),
		Signature: UserSignature{ID: msg.GetSignature().GetId(), Armor: msg.GetSignature().GetArmor()},
	}
	state := stateForDecision(decision.Decision)
	if state == "" {
		writeError(w, http.StatusBadRequest, "decision must be approve, reject or withdraw")
		return
	}
	if decision.DeciderID != callerID {
		writeError(w, http.StatusForbidden, "deciderID must be the caller")
		return
	}

	req, err := h.services.db.GetAdminRequest(r.Context(), mux.Vars(r)["id"])
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading admin request")
		return
	}
	if req == nil {
		writeError(w, http.StatusNotFound, "Request not found")
		return
	}
	if req.State != adminStatePending {
		h.metrics.AdminRequestRefused(r.Context(), req.Action, callerID, metrics.AdminStageDecide, "not_pending")
		writeError(w, http.StatusConflict, "Request is no longer pending")
		return
	}
	key, err := h.callerSigningKey(r.Context(), decision.Signature.ID, callerID)
	if err != nil {
		h.recordAdminRefusal(r.Context(), err, req.Action, callerID, metrics.AdminStageDecide)
		h.writeAdminError(w, r, err, "Error loading signing key")
		return
	}
	payload := buildAdminDecisionPayload(req.ID, decision.Decision, callerID, decision.Signature.ID, req.RequesterSignature.Armor)
	if err := h.services.crypto.verifySignature(string(payload), decision.Signature.Armor, key.Armor); err != nil {
		h.metrics.AdminRequestRefused(r.Context(), req.Action, callerID, metrics.AdminStageDecide, "bad_signature")
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	var ex *adminExecutor
	if decision.Decision == adminDecisionApprove {
		if ex = h.adminExecutor(req.Action); ex == nil {
			writeError(w, http.StatusNotImplemented, "The action isn't available")
			return
		}
	}
	serverID := h.services.db.GetServerID()
	now := time.Now().UTC().Truncate(time.Second)
	serverSig, err := h.countersign(buildAdminRequestServerPayload(req.ID, state, req.RequesterSignature.Armor,
		decision.Signature.Armor, serverID, h.signingKey.Fingerprint, now), now)
	if err != nil {
		log.Error().Err(err).Msg("Error countersigning admin decision")
		internalServerError(w)
		return
	}

	var entry *ledgerEntry
	err = withAdminTx(h.services.db.db, r.Context(), func(tx *sql.Tx) error {
		locked, err := getAdminRequestQ(r.Context(), tx, req.ID, true)
		if err != nil {
			return err
		}
		if locked == nil || locked.State != adminStatePending {
			return refuse(http.StatusConflict, "not_pending", "Request is no longer pending")
		}
		if decision.Decision == adminDecisionWithdraw {
			if callerID != locked.RequesterID {
				return refuse(http.StatusForbidden, "not_requester", "Only the requester can withdraw a request")
			}
		} else if err := checkDeciderQ(r.Context(), tx, locked, callerID, serverID); err != nil {
			return err
		}
		if ex != nil {
			locked.Decision = decision
			if ex.validate != nil {
				if err := ex.validate(r.Context(), tx, locked); err != nil {
					return err
				}
			}
			if err := ex.execute(r.Context(), tx, locked); err != nil {
				return err
			}
		}
		if err := closeAdminRequestTx(r.Context(), tx, locked, state, decision, serverSig); err != nil {
			return err
		}
		req = locked
		if state != adminStateExecuted {
			return nil
		}
		if entry = ledgerApprovedEntry(locked, callerID, h.rebindKeyID(locked)); entry == nil {
			return nil
		}
		return h.appendLedgerTx(r.Context(), tx, entry)
	})
	if err != nil {
		var refusal *adminRefusal
		if errors.As(err, &refusal) {
			h.metrics.AdminRequestRefused(r.Context(), req.Action, callerID, metrics.AdminStageDecide, refusal.reason)
		} else if ex != nil {
			h.recordAdminExecuted(r.Context(), req, callerID, false)
		}
		h.writeAdminError(w, r, err, "Error deciding admin request")
		return
	}
	h.pushLedgerEntries(entry)
	if ex != nil && ex.after != nil {
		ex.after(req)
	}
	var wait time.Duration
	if decision.Decision != adminDecisionWithdraw {
		wait = now.Sub(req.CreatedAt)
	}
	h.metrics.AdminRequestDecided(r.Context(), req.Action, decision.Decision, req.RequesterID, callerID, wait)
	if state == adminStateExecuted {
		h.recordAdminExecuted(r.Context(), req, callerID, true)
	}
	log.Info().Str("requestID", req.ID).Str("decision", decision.Decision).Str("deciderID", callerID).Msg("Admin request closed")
	writeResponse(w, http.StatusOK, pbAdminRequest(req))
}

// recordAdminExecuted counts an approved action once per remaining target,
// once with no target for a broadcast.
func (h *Handlers) recordAdminExecuted(ctx context.Context, req *adminRequest, approverID string, ok bool) {
	targets := req.remainingTargets()
	if len(targets) == 0 {
		targets = []string{""}
	}
	for _, target := range targets {
		h.metrics.AdminActionExecuted(ctx, req.Action, target, req.RequesterID, approverID, ok)
	}
}

// recordInvitesRevoked counts the open invites action revoked from target.
func (h *Handlers) recordInvitesRevoked(action, target string, n int64) {
	if n > 0 {
		h.metrics.AdminInvitesRevoked(context.Background(), action, target, int(n))
	}
}

// dropFromAdminRequests marks a removed user dropped from every pending
// request, and closes as moot each request left with no targets.
func (h *Handlers) dropFromAdminRequests(ctx context.Context, userID string) {
	log := h.services.log.GetLogger(ctx)
	ids, err := h.services.db.queryStrings(ctx, `
		UPDATE admin_request_targets t SET dropped = TRUE
		FROM admin_requests r
		WHERE r.id = t.request_id AND r.state = 'pending' AND t.user_id = $1 AND NOT t.dropped
		RETURNING t.request_id
	`, userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to drop removed user from admin requests")
		return
	}
	for _, id := range ids {
		if err := h.closeIfMoot(ctx, id); err != nil {
			log.Error().Err(err).Str("requestID", id).Msg("Failed to close moot admin request")
		}
	}
}

// closeIfMoot closes requestID as moot when every one of its targets has
// been dropped. A broadcast has no targets, so it never goes moot.
func (h *Handlers) closeIfMoot(ctx context.Context, requestID string) error {
	req, err := h.services.db.GetAdminRequest(ctx, requestID)
	if err != nil || req == nil || req.State != adminStatePending {
		return err
	}
	if len(req.Targets) == 0 || len(req.remainingTargets()) > 0 {
		return nil
	}
	now := time.Now().UTC().Truncate(time.Second)
	serverSig, err := h.countersign(buildAdminRequestServerPayload(req.ID, adminStateMoot, req.RequesterSignature.Armor,
		"", h.services.db.GetServerID(), h.signingKey.Fingerprint, now), now)
	if err != nil {
		return err
	}
	closed := false
	err = withAdminTx(h.services.db.db, ctx, func(tx *sql.Tx) error {
		locked, err := getAdminRequestQ(ctx, tx, requestID, true)
		if err != nil || locked == nil || locked.State != adminStatePending || len(locked.remainingTargets()) > 0 {
			return err
		}
		closed = true
		return closeAdminRequestTx(ctx, tx, locked, adminStateMoot, nil, serverSig)
	})
	if err == nil && closed {
		h.metrics.AdminRequestMoot(ctx, req.Action)
	}
	return err
}

// adminRequestFromPB reads a request as it travels in a certificate.
func adminRequestFromPB(msg *pb.AdminRequest) *adminRequest {
	req := &adminRequest{
		ID:          msg.GetId(),
		Action:      msg.GetAction(),
		RequesterID: msg.GetRequesterId(),
		Reason:      msg.GetReason(),
		Targets:     slices.Clone(msg.GetTargets()),
		Dropped:     slices.Clone(msg.GetDroppedTargets()),
		Body:        msg.GetBody(),
		CreatedAt:   timeFromUnix(msg.GetCreatedAt()),
		RequesterSignature: UserSignature{
			ID:    msg.GetRequesterSignature().GetId(),
			Armor: msg.GetRequesterSignature().GetArmor(),
		},
		State: msg.GetState(),
	}
	if d := msg.GetDecision(); d != nil {
		req.Decision = &adminDecision{
			Decision:  d.GetDecision(),
			DeciderID: d.GetDeciderId(),
			Signature: UserSignature{ID: d.GetSignature().GetId(), Armor: d.GetSignature().GetArmor()},
		}
	}
	if sig := msg.GetServerSignature(); sig != nil {
		req.ServerSignature = &ServerSignature{ID: sig.GetId(), Armor: sig.GetArmor(), SignedAt: timeFromUnix(sig.GetSignedAt())}
	}
	return req
}

// verifyForeignAdminAuthorization checks that two of peerServerID's admins
// approved action on target: both signatures, by keys of theirs. Their
// roles and permissions are the peer's to have checked.
func (h *Handlers) verifyForeignAdminAuthorization(ctx context.Context, peerServerID string, auth *pb.AdminAuthorization, action, target string) (*adminRequest, error) {
	if auth.GetRequest() == nil {
		return nil, fmt.Errorf("no admin request")
	}
	req := adminRequestFromPB(auth.GetRequest())
	return req, h.verifyForeignAdminRequest(ctx, peerServerID, req, action, target)
}

// verifyForeignAdminRequest is verifyForeignAdminAuthorization for a
// request already read off the wire.
func (h *Handlers) verifyForeignAdminRequest(ctx context.Context, peerServerID string, req *adminRequest, action, target string) error {
	if req.Action != action || req.State != adminStateExecuted || req.Decision == nil ||
		req.Decision.Decision != adminDecisionApprove || req.ServerSignature == nil {
		return fmt.Errorf("not an approved %s request", action)
	}
	if !slices.Contains(req.Targets, target) {
		return fmt.Errorf("%s isn't a target of the request", target)
	}
	if req.Decision.DeciderID == req.RequesterID && !isRootIdentity(req.RequesterID, peerServerID) {
		return fmt.Errorf("the requester approved their own request")
	}
	signers := []struct {
		userID string
		sig    UserSignature
		bytes  []byte
	}{
		{req.RequesterID, req.RequesterSignature, req.payload()},
		{req.Decision.DeciderID, req.Decision.Signature, buildAdminDecisionPayload(req.ID, req.Decision.Decision,
			req.Decision.DeciderID, req.Decision.Signature.ID, req.RequesterSignature.Armor)},
	}
	for _, s := range signers {
		if !requesterKeyBelongsTo(s.sig.ID, s.userID, peerServerID) {
			return fmt.Errorf("key %s isn't %s's", s.sig.ID, s.userID)
		}
		key, err := h.resolvePublicKey(ctx, s.sig.ID)
		if err != nil || key == nil {
			return fmt.Errorf("resolve %s's key: %v", s.userID, err)
		}
		if err := h.services.crypto.verifySignature(string(s.bytes), s.sig.Armor, key.Armor); err != nil {
			return fmt.Errorf("%s's signature: %w", s.userID, err)
		}
	}
	return nil
}

// marshalAdminAuthorization is a peer's approved request as it's kept: the
// signed authorization it arrived as.
func marshalAdminAuthorization(req *adminRequest) ([]byte, error) {
	return proto.Marshal(&pb.AdminAuthorization{Request: pbAdminRequest(req)})
}

func unmarshalAdminAuthorization(b []byte) (*adminRequest, error) {
	var auth pb.AdminAuthorization
	if err := proto.Unmarshal(b, &auth); err != nil {
		return nil, err
	}
	if auth.GetRequest() == nil {
		return nil, fmt.Errorf("no admin request")
	}
	return adminRequestFromPB(auth.GetRequest()), nil
}

// GetUserPermissions handles GET /admin/users/{userID}/permissions: an
// admin may read anyone's, every user their own.
func (h *Handlers) GetUserPermissions(w http.ResponseWriter, r *http.Request) {
	callerID, callerRole, ok := h.adminCaller(w, r)
	if !ok {
		return
	}
	userID := mux.Vars(r)["userID"]
	if userID != callerID && !isAdmin(callerRole) {
		writeError(w, http.StatusForbidden, "Admin required")
		return
	}
	role, err := h.services.db.GetUserRole(r.Context(), userID)
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading role")
		return
	}
	if role == "" {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	perms, err := h.services.db.GetUserPermissions(r.Context(), userID)
	if err != nil {
		h.writeAdminError(w, r, err, "Error loading permissions")
		return
	}
	writeResponse(w, http.StatusOK, &pb.UserPermissions{Role: role, Permissions: perms})
}
