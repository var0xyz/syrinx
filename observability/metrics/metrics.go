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

// ReedKind classifies a published reed.
type ReedKind string

const (
	ReedKindPlain ReedKind = "plain"
	ReedKindEcho  ReedKind = "echo"
	ReedKindReply ReedKind = "reply"
)

// ReedPublishedAttrs carries structural publish metadata (no content or tag text).
type ReedPublishedAttrs struct {
	Kind        ReedKind
	AuthorID    string
	ReedID      string
	TagCount    int
	RawChars    int
	VisibleChars int
}

// Recorder emits domain metrics. Implementations must be safe for concurrent use.
type Recorder interface {
	UserCreated(ctx context.Context, signupMode string)
	UserDeleted(ctx context.Context, userID string, noteHas bool)
	ReedPublished(ctx context.Context, p ReedPublishedAttrs)
	ReedDeleted(ctx context.Context, authorID, reedID string)
	EchoTargeted(ctx context.Context, targetAuthorID, targetReedID string)
	ReedRejectedLength(ctx context.Context, rawChars, visibleChars int)
	KeyRevoked(ctx context.Context, userID string)
	ReedCoverage(ctx context.Context, authorID, reedID string, holders, coveragePercent int)
	WSMessage(ctx context.Context, direction Direction, msgType string)
}

// Noop is an inert Recorder.
type Noop struct{}

func (Noop) UserCreated(context.Context, string)                              {}
func (Noop) UserDeleted(context.Context, string, bool)                        {}
func (Noop) ReedPublished(context.Context, ReedPublishedAttrs)                {}
func (Noop) ReedDeleted(context.Context, string, string)                      {}
func (Noop) EchoTargeted(context.Context, string, string)                     {}
func (Noop) ReedRejectedLength(context.Context, int, int)                     {}
func (Noop) KeyRevoked(context.Context, string)                               {}
func (Noop) ReedCoverage(context.Context, string, string, int, int)           {}
func (Noop) WSMessage(context.Context, Direction, string)                     {}

// TagCountAttr buckets tag counts for metric attributes (exact 0–3, then 4+).
func TagCountAttr(n int) int {
	if n > 3 {
		return 4
	}
	return n
}
