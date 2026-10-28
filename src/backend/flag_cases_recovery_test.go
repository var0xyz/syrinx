//go:build !ops

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	pb "syrinx/proto"
)

// In lockdown, flags from before the wipe come back frozen, their
// dismissals with them; once operational they can be acted on, and a
// flag from before the wipe reported after that is dropped.
func TestRecoveryE2E_FlagCasesFrozenThenFinal(t *testing.T) {
	sc := newRecoveryScenario(t)
	open := queryRows(t, sc.a.db, `SELECT reed_id FROM flag_cases WHERE dismissed_at IS NULL AND voided_at IS NULL`)
	if len(open) != 1 {
		t.Fatalf("scenario open cases = %v, want one", open)
	}
	reedID := open[0]

	b := restoreIdentity(t, sc.a)
	for _, u := range sc.everyone {
		b.recover(t, sc.deviceOf(t, u))
	}
	if got := queryRows(t, b.db, `SELECT COUNT(*) FROM flag_cases WHERE restored AND dismissed_at IS NOT NULL`); got[0] != "1" {
		t.Fatalf("restored dismissed cases = %s, want the scenario's one", got[0])
	}
	caseID := queryRows(t, b.db, `SELECT id FROM flag_cases WHERE reed_id = '`+reedID+`'`)[0]
	dismiss := func() *httptest.ResponseRecorder {
		sig := b.sign(t, sc.dave, buildFlagDismissalPayload(caseID, reedID, "Fine", sc.dave.keyID))
		msg := &pb.DismissCaseRequest{Reason: "Fine", Signature: &pb.UserSignature{Id: sc.dave.keyID, Armor: sig}}
		return b.authed(t, sc.dave, http.MethodPost, "/api/admin/flags/cases/"+caseID+"/dismiss", msg, map[string]string{"caseID": caseID}, b.h.DismissCase)
	}
	if rr := dismiss(); rr.Code != http.StatusConflict {
		t.Fatalf("dismissing a restored case in lockdown: %d %s, want 409", rr.Code, rr.Body.String())
	}
	b.h.cfg.RecoveryMode = string(recoveryModeOperational)
	if rr := dismiss(); rr.Code != http.StatusOK {
		t.Fatalf("dismissing it once operational: %d %s", rr.Code, rr.Body.String())
	}

	c := restoreIdentity(t, sc.a)
	c.h.cfg.RecoveryMode = string(recoveryModeOperational)
	for _, u := range sc.everyone {
		c.recover(t, sc.deviceOf(t, u))
	}
	if got := queryRows(t, c.db, `SELECT COUNT(*) FROM reed_flags`); got[0] != "0" {
		t.Errorf("flags restored once operational = %s, want none", got[0])
	}
}
