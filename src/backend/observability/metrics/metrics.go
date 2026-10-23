// Package metrics records Syrinx business counters and histograms. Only
// abuse and admin counters carry entity ids, so an operator can name who;
// the rest stay bounded. When observability is disabled, Recorder is a no-op.
package metrics

import (
	"context"
	"time"
)

// Direction is WebSocket message flow relative to the server.
type Direction string

const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

// BackupKind classifies a user-initiated export recorded via POST /users/me/backup.
type BackupKind string

const (
	BackupKindIdentity BackupKind = "identity" // keys-only .sxi.gpg
	BackupKindFull     BackupKind = "full"     // full .sxb.gpg
)

// SignupModeImport labels users created during server-recovery import (claim or peer seed).
const SignupModeImport = "import"

// ReedKind classifies a published reed.
type ReedKind string

const (
	ReedKindPlain ReedKind = "plain"
	ReedKindEcho  ReedKind = "echo"
	ReedKindReply ReedKind = "reply"
	// ReedKindThread is one part of a thread.
	ReedKindThread ReedKind = "thread"
)

// RemovalKind says who removed an account.
type RemovalKind string

const (
	RemovalSelf RemovalKind = "self"
	RemovalKick RemovalKind = "kick"
)

// RevocationKind says why a key was revoked.
type RevocationKind string

const (
	RevocationRotation RevocationKind = "rotation"
	RevocationRebind   RevocationKind = "rebind"
)

// RelayLifecycle is a pending relay event's stage. Created minus deleted
// is how many relay requests are still open.
type RelayLifecycle string

const (
	RelayEventCreated RelayLifecycle = "created"
	RelayEventDeleted RelayLifecycle = "deleted"
)

// Op is the direction of a toggle-style action.
type Op string

const (
	OpAdd    Op = "add"
	OpRemove Op = "remove"
)

// Origin says whether an action started on this server or a peer's.
type Origin string

const (
	OriginLocal Origin = "local"
	OriginPeer  Origin = "peer"
)

// Surface is the transport a refusal happened on.
type Surface string

const (
	SurfaceHTTP Surface = "http"
	SurfaceWS   Surface = "ws"
)

// AdminStage is where an admin request was refused.
type AdminStage string

const (
	AdminStageOpen   AdminStage = "open"
	AdminStageDecide AdminStage = "decide"
)

// ReedPublishedAttrs carries structural publish metadata (no content or tag
// text — the server never sees either).
type ReedPublishedAttrs struct {
	Kind     ReedKind
	TagCount int
}

// Recorder emits domain metrics. Implementations must be safe for concurrent use.
type Recorder interface {
	UserCreated(ctx context.Context, signupMode string)
	UserDeleted(ctx context.Context, kind RemovalKind, noteHas bool)
	UserUpdated(ctx context.Context, renamed bool)
	ReedPublished(ctx context.Context, p ReedPublishedAttrs)
	ReedDeleted(ctx context.Context)
	ThreadPublished(ctx context.Context, parts int)
	EchoTargeted(ctx context.Context)
	KeyRevoked(ctx context.Context, kind RevocationKind)
	KeyFetchError(ctx context.Context, reporterUserID, targetUserID, keyID string)
	RevokedKeyUsed(ctx context.Context, reporterUserID, targetUserID, keyID string)
	ContentRejected(ctx context.Context, reporterUserID, authorID, storeName, reason string)
	MentionClaimRejected(ctx context.Context, authorID, reporterUserID, reason string)
	UserBackup(ctx context.Context, kind BackupKind)
	ReedCoverage(ctx context.Context, holders, coveragePercent int)
	WSMessage(ctx context.Context, direction Direction, msgType string)
	RelayEvent(ctx context.Context, lifecycle RelayLifecycle, eventKind string)
	FederationRelay(ctx context.Context, direction Direction, peerServerID, leg string, ok bool)

	AdminRequestOpened(ctx context.Context, action, requesterID, requesterRole string, targets int)
	AdminRequestRefused(ctx context.Context, action, actorID string, stage AdminStage, reason string)
	AdminRequestDecided(ctx context.Context, action, decision, requesterID, deciderID string, wait time.Duration)
	AdminRequestMoot(ctx context.Context, action string)
	AdminActionExecuted(ctx context.Context, action, targetID, requesterID, approverID string, ok bool)
	SuspendedRefusal(ctx context.Context, userID string, surface Surface)
	AdminMailSent(ctx context.Context, action, senderID string, recipients, failed int)
	AdminInvitesRevoked(ctx context.Context, action, targetID string, count int)
	ReservedUsernameHit(ctx context.Context, surface, userID string)
	ForeignKick(ctx context.Context, peerServerID string, ok bool)
	FlagCreated(ctx context.Context, reporterID, authorID, reedID string, origin Origin, noteHas bool)
	FlagsDismissed(ctx context.Context, adminID, reedID string, count int)
	LedgerAppended(ctx context.Context, entryKind string)
	ServerKeyRevocations(ctx context.Context, compromised bool, total int)

	AuthRejected(ctx context.Context, keyID, userID, reason, route string)
	WSHandshakeRejected(ctx context.Context, keyID, userID, reason string)
	WSSession(ctx context.Context, connected bool)
	SignupRejected(ctx context.Context, signupMode, reason string)
	UsernameCheck(ctx context.Context, available bool)
	CooldownHit(ctx context.Context, kind, userID string)
	BlockRefusal(ctx context.Context, viewerID, authorID string, surface Surface)

	InviteCreated(ctx context.Context, adminInvite bool)
	InviteClaimed(ctx context.Context, adminInvite bool)
	InviteDeleted(ctx context.Context)
	Follow(ctx context.Context, op Op, remote bool)
	Like(ctx context.Context, op Op)
	Ripple(ctx context.Context, op Op)
	Pin(ctx context.Context, op Op)
	Block(ctx context.Context, op Op, origin Origin)
	Vouch(ctx context.Context, op Op, ok bool, reason string)
	VerificationWindow(ctx context.Context, op string)
	DeviceBound(ctx context.Context)
	AccountRecovery(ctx context.Context, stage string, ok bool, reason string)
	ServerRecovery(ctx context.Context, stage string, ok bool)
	// ServerRecoveryClaim is the claim stage; first means the account came
	// back with it, rather than being claimed again.
	ServerRecoveryClaim(ctx context.Context, ok, first bool)
	// ServerRecoveryState is how many users are recorded, how many of those
	// were never claimed, and how many are mid-import.
	ServerRecoveryState(ctx context.Context, recorded, unclaimed, ongoing int)
	Mailbox(ctx context.Context, op Op, kind string)
	UserSearch(ctx context.Context, remote bool)
	// CryptoOperation times one OpenPGP sign or verify (op), by key algorithm.
	CryptoOperation(ctx context.Context, op, algorithm string, ok bool, took time.Duration)

	FederationLifecycle(ctx context.Context, event, peerServerID, adminID string)
	FederationDeliveryBacklog(ctx context.Context, peerServerID string, items int)
	// FederationPending moves the count of relay requests open across a
	// border: out = we wait on the peer, in = the peer waits on us.
	FederationPending(ctx context.Context, direction Direction, peerServerID string, delta int64)
}

// Noop is an inert Recorder.
type Noop struct{}

func (Noop) UserCreated(context.Context, string)                                     {}
func (Noop) UserDeleted(context.Context, RemovalKind, bool)                          {}
func (Noop) UserUpdated(context.Context, bool)                                       {}
func (Noop) ReedPublished(context.Context, ReedPublishedAttrs)                       {}
func (Noop) ReedDeleted(context.Context)                                             {}
func (Noop) ThreadPublished(context.Context, int)                                    {}
func (Noop) EchoTargeted(context.Context)                                            {}
func (Noop) KeyRevoked(context.Context, RevocationKind)                              {}
func (Noop) KeyFetchError(context.Context, string, string, string)                   {}
func (Noop) RevokedKeyUsed(context.Context, string, string, string)                  {}
func (Noop) ContentRejected(context.Context, string, string, string, string)         {}
func (Noop) MentionClaimRejected(context.Context, string, string, string)            {}
func (Noop) UserBackup(context.Context, BackupKind)                                  {}
func (Noop) ReedCoverage(context.Context, int, int)                                  {}
func (Noop) WSMessage(context.Context, Direction, string)                            {}
func (Noop) RelayEvent(context.Context, RelayLifecycle, string)                      {}
func (Noop) FederationRelay(context.Context, Direction, string, string, bool)        {}
func (Noop) AdminRequestOpened(context.Context, string, string, string, int)         {}
func (Noop) AdminRequestRefused(context.Context, string, string, AdminStage, string) {}
func (Noop) AdminRequestDecided(context.Context, string, string, string, string, time.Duration) {
}
func (Noop) AdminRequestMoot(context.Context, string)                                  {}
func (Noop) AdminActionExecuted(context.Context, string, string, string, string, bool) {}
func (Noop) SuspendedRefusal(context.Context, string, Surface)                         {}
func (Noop) AdminMailSent(context.Context, string, string, int, int)                   {}
func (Noop) AdminInvitesRevoked(context.Context, string, string, int)                  {}
func (Noop) ReservedUsernameHit(context.Context, string, string)                       {}
func (Noop) ForeignKick(context.Context, string, bool)                                 {}
func (Noop) FlagCreated(context.Context, string, string, string, Origin, bool)         {}
func (Noop) FlagsDismissed(context.Context, string, string, int)                       {}
func (Noop) LedgerAppended(context.Context, string)                                    {}
func (Noop) ServerKeyRevocations(context.Context, bool, int)                           {}
func (Noop) AuthRejected(context.Context, string, string, string, string)              {}
func (Noop) WSHandshakeRejected(context.Context, string, string, string)               {}
func (Noop) WSSession(context.Context, bool)                                           {}
func (Noop) SignupRejected(context.Context, string, string)                            {}
func (Noop) UsernameCheck(context.Context, bool)                                       {}
func (Noop) CooldownHit(context.Context, string, string)                               {}
func (Noop) BlockRefusal(context.Context, string, string, Surface)                     {}
func (Noop) InviteCreated(context.Context, bool)                                       {}
func (Noop) InviteClaimed(context.Context, bool)                                       {}
func (Noop) InviteDeleted(context.Context)                                             {}
func (Noop) Follow(context.Context, Op, bool)                                          {}
func (Noop) Like(context.Context, Op)                                                  {}
func (Noop) Ripple(context.Context, Op)                                                {}
func (Noop) Pin(context.Context, Op)                                                   {}
func (Noop) Block(context.Context, Op, Origin)                                         {}
func (Noop) Vouch(context.Context, Op, bool, string)                                   {}
func (Noop) VerificationWindow(context.Context, string)                                {}
func (Noop) DeviceBound(context.Context)                                               {}
func (Noop) AccountRecovery(context.Context, string, bool, string)                     {}
func (Noop) ServerRecovery(context.Context, string, bool)                              {}
func (Noop) ServerRecoveryClaim(context.Context, bool, bool)                           {}
func (Noop) ServerRecoveryState(context.Context, int, int, int)                        {}
func (Noop) Mailbox(context.Context, Op, string)                                       {}
func (Noop) UserSearch(context.Context, bool)                                          {}
func (Noop) CryptoOperation(context.Context, string, string, bool, time.Duration)      {}
func (Noop) FederationLifecycle(context.Context, string, string, string)               {}
func (Noop) FederationDeliveryBacklog(context.Context, string, int)                    {}
func (Noop) FederationPending(context.Context, Direction, string, int64)               {}

// TagCountAttr buckets tag counts for metric attributes (exact 0–3, then 4+).
func TagCountAttr(n int) int {
	if n > 3 {
		return 4
	}
	return n
}
