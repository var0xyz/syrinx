// Package metrics records anonymized Syrinx business counters and histograms.
// When observability is disabled, Recorder degrades to a no-op.
package metrics

import "context"

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
)

// RelayLifecycle is a pending relay event's stage, for wasted-bandwidth
// analysis: created-but-never-fulfilled-or-deleted means it leaked (no
// holder ever answered); fulfilled more than once for the same
// event.id_hash means a duplicate/redundant relay response was processed.
type RelayLifecycle string

const (
	RelayEventCreated   RelayLifecycle = "created"
	RelayEventFulfilled RelayLifecycle = "fulfilled"
	RelayEventDeleted   RelayLifecycle = "deleted"
)

// ReedPublishedAttrs carries structural publish metadata (no content or tag text).
type ReedPublishedAttrs struct {
	Kind         ReedKind
	AuthorID     string
	ReedID       string
	TagCount     int
	RawChars     int
	VisibleChars int
}

// Recorder emits domain metrics. Implementations must be safe for concurrent use.
type Recorder interface {
	UserCreated(ctx context.Context, signupMode, userID string)
	UserDeleted(ctx context.Context, userID string, noteHas bool)
	ReedPublished(ctx context.Context, p ReedPublishedAttrs)
	ReedDeleted(ctx context.Context, authorID, reedID string)
	EchoTargeted(ctx context.Context, targetAuthorID, targetReedID string)
	ReedRejectedLength(ctx context.Context, rawChars, visibleChars int)
	KeyRevoked(ctx context.Context, userID string)
	KeyFetchError(ctx context.Context, reporterUserID, targetUserID, keyID string)
	RevokedKeyUsed(ctx context.Context, reporterUserID, targetUserID, keyID string)
	ContentRejected(ctx context.Context, reporterUserID, storeName string)
	UserBackup(ctx context.Context, userID string, kind BackupKind)
	ReedCoverage(ctx context.Context, authorID, reedID string, holders, coveragePercent int)
	WSMessage(ctx context.Context, direction Direction, msgType string)
	RelayEvent(ctx context.Context, lifecycle RelayLifecycle, eventKind, eventID string)
	FederationRelay(ctx context.Context, direction Direction, peerServerID, leg string, ok bool)
}

// Noop is an inert Recorder.
type Noop struct{}

func (Noop) UserCreated(context.Context, string, string)                      {}
func (Noop) UserDeleted(context.Context, string, bool)                        {}
func (Noop) ReedPublished(context.Context, ReedPublishedAttrs)                {}
func (Noop) ReedDeleted(context.Context, string, string)                      {}
func (Noop) EchoTargeted(context.Context, string, string)                     {}
func (Noop) ReedRejectedLength(context.Context, int, int)                     {}
func (Noop) KeyRevoked(context.Context, string)                               {}
func (Noop) KeyFetchError(context.Context, string, string, string)            {}
func (Noop) RevokedKeyUsed(context.Context, string, string, string)           {}
func (Noop) ContentRejected(context.Context, string, string)                  {}
func (Noop) UserBackup(context.Context, string, BackupKind)                   {}
func (Noop) ReedCoverage(context.Context, string, string, int, int)           {}
func (Noop) WSMessage(context.Context, Direction, string)                     {}
func (Noop) RelayEvent(context.Context, RelayLifecycle, string, string)       {}
func (Noop) FederationRelay(context.Context, Direction, string, string, bool) {}

// TagCountAttr buckets tag counts for metric attributes (exact 0–3, then 4+).
func TagCountAttr(n int) int {
	if n > 3 {
		return 4
	}
	return n
}
