//go:build !ops

package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"syrinx/observability/metrics"
)

// adminRecorder keeps the admin metrics a test asserts on, one line each.
type adminRecorder struct {
	metrics.Noop
	mu    sync.Mutex
	lines []string
}

func (r *adminRecorder) add(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *adminRecorder) has(t *testing.T, line string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.lines, line) {
		t.Errorf("missing metric %q in %q", line, r.lines)
	}
}

func (r *adminRecorder) AdminRequestOpened(_ context.Context, action, requesterID, role string, targets int) {
	r.add("opened %s %s %s %d", action, requesterID, role, targets)
}

func (r *adminRecorder) AdminRequestRefused(_ context.Context, action, actorID string, stage metrics.AdminStage, reason string) {
	r.add("refused %s %s %s %s", action, actorID, stage, reason)
}

func (r *adminRecorder) AdminRequestDecided(_ context.Context, action, decision, requesterID, deciderID string, _ time.Duration) {
	r.add("decided %s %s %s %s", action, decision, requesterID, deciderID)
}

func (r *adminRecorder) AdminActionExecuted(_ context.Context, action, targetID, requesterID, approverID string, ok bool) {
	r.add("executed %s %s %s %s %t", action, targetID, requesterID, approverID, ok)
}

func (r *adminRecorder) UserDeleted(_ context.Context, userID string, kind metrics.RemovalKind, _ bool) {
	r.add("deleted %s %s", userID, kind)
}

func TestAdminMetrics_CarryRawIDsAndReasons(t *testing.T) {
	f := newAdminFixture(t)
	f.useRealExecutors(adminActionKick)
	rec := &adminRecorder{}
	f.h.metrics = rec

	refused := f.request(t, "a1", adminActionKick, "Spam", "", "root")
	if rr := f.serve(t, f.h.OpenAdminRequest, http.MethodPost, "/api/admin/requests", "a1", refused, nil); rr.Code != http.StatusForbidden {
		t.Fatalf("root target: status=%d", rr.Code)
	}
	rec.has(t, fmt.Sprintf("refused kick %s open root_target", f.ids["a1"]))

	f.kick(t, "u1")
	rec.has(t, fmt.Sprintf("opened kick %s admin 1", f.ids["a1"]))
	rec.has(t, fmt.Sprintf("decided kick approve %s %s", f.ids["a1"], f.ids["a2"]))
	rec.has(t, fmt.Sprintf("executed kick %s %s %s true", f.ids["u1"], f.ids["a1"], f.ids["a2"]))
	rec.has(t, fmt.Sprintf("deleted %s kick", f.ids["u1"]))
}
