package invites

import (
	"fmt"
	"strconv"
	"strings"
)

// SignupMode is the deploy-time registration policy.
type SignupMode string

const (
	ModeOpen   SignupMode = "open"
	ModeInvite SignupMode = "invite"
	ModeClosed SignupMode = "closed"
)

// MaxInvitesPerUser is the per-user invite minting cap. -1 means infinite.
type MaxInvitesPerUser int

const MaxInvitesUnlimited MaxInvitesPerUser = -1

// ParseSignupMode validates SIGNUP_MODE. Empty / unset → open.
func ParseSignupMode(raw string) (SignupMode, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ModeOpen, nil
	}
	switch SignupMode(s) {
	case ModeOpen, ModeInvite, ModeClosed:
		return SignupMode(s), nil
	default:
		return "", fmt.Errorf(
			"invalid SIGNUP_MODE %q: must be %q, %q, or %q",
			raw, ModeOpen, ModeInvite, ModeClosed,
		)
	}
}

// ParseMaxInvitesPerUser validates MAX_INVITES_PER_USER.
// Unset / empty / "-1" → unlimited (-1). Integer N >= 1 → cap at N.
// Anything else is fatal.
func ParseMaxInvitesPerUser(raw string) (MaxInvitesPerUser, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "-1" {
		return MaxInvitesUnlimited, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf(
			"invalid MAX_INVITES_PER_USER %q: must be an integer >= 1, or -1 / unset for unlimited",
			raw,
		)
	}
	if n < 1 {
		return 0, fmt.Errorf(
			"invalid MAX_INVITES_PER_USER %q: must be >= 1, or -1 / unset for unlimited",
			raw,
		)
	}
	return MaxInvitesPerUser(n), nil
}
