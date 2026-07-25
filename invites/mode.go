package invites

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
