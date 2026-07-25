package invites

import (
	"errors"
	"strings"
)

// Signup policy errors. Handlers map these to 403 with stable strings.
var (
	ErrInviteRequired = errors.New("invite required")
	ErrInvalidInvite  = errors.New("invalid or claimed invite")
)

// ResolvedInvite is the invite (if any) to consume during signup.
type ResolvedInvite struct {
	InviteID  string
	InviterID string
}

// ResolveSignup applies SIGNUP_MODE invite policy given a locked user count
// and an optional invite row looked up by token hash (nil if absent/unknown).
//
// Policy:
//   - invite mode with users present → token required
//   - invite mode with empty users (bootstrap) → token optional
//   - open mode → token optional
//   - if a token is provided it must resolve to a pending invite
func ResolveSignup(mode SignupMode, userCount int, rawToken string, inv *Invite) (ResolvedInvite, error) {
	token := strings.TrimSpace(rawToken)
	hasToken := token != ""

	requireInvite := mode == ModeInvite && userCount > 0
	if !hasToken {
		if requireInvite {
			return ResolvedInvite{}, ErrInviteRequired
		}
		return ResolvedInvite{}, nil
	}

	if inv == nil || inv.Status() != "pending" {
		return ResolvedInvite{}, ErrInvalidInvite
	}
	return ResolvedInvite{
		InviteID:  inv.ID,
		InviterID: inv.CreatedBy,
	}, nil
}
