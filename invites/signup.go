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
// and an optional invite row looked up by HashSecret(secret) (nil if absent).
//
// When inviteID/secret are provided, inv must be pending and inv.ID must
// equal inviteID.
//
// Policy:
//   - invite mode with users present → id+secret required
//   - invite mode with empty users (bootstrap) → optional
//   - open mode → optional
//   - if credentials are provided they must resolve to a pending invite
func ResolveSignup(
	mode SignupMode,
	userCount int,
	inviteID, secret string,
	inv *Invite,
) (ResolvedInvite, error) {
	id := strings.TrimSpace(inviteID)
	sec := strings.TrimSpace(secret)
	hasCreds := id != "" || sec != ""

	requireInvite := mode == ModeInvite && userCount > 0
	if !hasCreds {
		if requireInvite {
			return ResolvedInvite{}, ErrInviteRequired
		}
		return ResolvedInvite{}, nil
	}
	if id == "" || sec == "" {
		return ResolvedInvite{}, ErrInvalidInvite
	}
	if inv == nil || inv.Status() != "pending" || inv.ID != id {
		return ResolvedInvite{}, ErrInvalidInvite
	}
	return ResolvedInvite{
		InviteID:  inv.ID,
		InviterID: inv.CreatedBy,
	}, nil
}
