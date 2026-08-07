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

// ResolveSignup applies SIGNUP_MODE invite policy given an optional invite
// row looked up by composite PK + HashSecret(secret) (nil if absent).
//
// When inviteID/secret are provided, inv must be pending and inv.ID must
// equal inviteID.
//
// Policy:
//   - invite mode → id+secret required
//   - open mode → optional
//   - closed mode is rejected by the handler before this runs
//   - if credentials are provided they must resolve to a pending invite
//
// First account: deploy with SIGNUP_MODE=open, then switch to invite or closed.
func ResolveSignup(
	mode SignupMode,
	inviteID, creatorID, secret string,
	inv *Invite,
) (ResolvedInvite, error) {
	id := strings.TrimSpace(inviteID)
	by := strings.TrimSpace(creatorID)
	sec := strings.TrimSpace(secret)
	hasCreds := id != "" || by != "" || sec != ""

	if !hasCreds {
		if mode == ModeInvite {
			return ResolvedInvite{}, ErrInviteRequired
		}
		return ResolvedInvite{}, nil
	}
	if id == "" || by == "" || sec == "" {
		return ResolvedInvite{}, ErrInvalidInvite
	}
	if inv == nil || inv.Status() != "pending" || inv.ID != id || inv.CreatedBy != by {
		return ResolvedInvite{}, ErrInvalidInvite
	}
	return ResolvedInvite{
		InviteID:  inv.ID,
		InviterID: inv.CreatedBy,
	}, nil
}
