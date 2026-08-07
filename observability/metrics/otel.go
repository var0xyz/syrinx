package metrics

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const scope = "syrinx/business"

// OTEL implements Recorder with the OpenTelemetry SDK.
type OTEL struct {
	usersCreated        metric.Int64Counter
	usersDeleted        metric.Int64Counter
	reedsPublished      metric.Int64Counter
	reedsDeleted        metric.Int64Counter
	echoesTargeted      metric.Int64Counter
	reedsRejectedLength metric.Int64Counter
	keysRevoked         metric.Int64Counter
	usersBackup         metric.Int64Counter
	wsMessages          metric.Int64Counter
	rawChars            metric.Int64Histogram
	visibleChars        metric.Int64Histogram
	holders             metric.Int64Histogram
	coveragePercent     metric.Int64Histogram
}

// New builds an OTEL recorder from a meter. Panics only if instrument registration fails.
func New(m metric.Meter) *OTEL {
	r := &OTEL{}
	var err error

	r.usersCreated, err = m.Int64Counter("syrinx.users.created")
	if err != nil {
		panic(err)
	}
	r.usersDeleted, err = m.Int64Counter("syrinx.users.deleted")
	if err != nil {
		panic(err)
	}
	r.reedsPublished, err = m.Int64Counter("syrinx.reeds.published")
	if err != nil {
		panic(err)
	}
	r.reedsDeleted, err = m.Int64Counter("syrinx.reeds.deleted")
	if err != nil {
		panic(err)
	}
	r.echoesTargeted, err = m.Int64Counter("syrinx.echoes.targeted")
	if err != nil {
		panic(err)
	}
	r.reedsRejectedLength, err = m.Int64Counter("syrinx.reeds.rejected.length")
	if err != nil {
		panic(err)
	}
	r.keysRevoked, err = m.Int64Counter("syrinx.keys.revoked")
	if err != nil {
		panic(err)
	}
	r.usersBackup, err = m.Int64Counter("syrinx.users.backup")
	if err != nil {
		panic(err)
	}
	r.wsMessages, err = m.Int64Counter("syrinx.ws.messages")
	if err != nil {
		panic(err)
	}
	r.rawChars, err = m.Int64Histogram("syrinx.reed.content.raw_chars")
	if err != nil {
		panic(err)
	}
	r.visibleChars, err = m.Int64Histogram("syrinx.reed.content.visible_chars")
	if err != nil {
		panic(err)
	}
	r.holders, err = m.Int64Histogram("syrinx.reed.holders")
	if err != nil {
		panic(err)
	}
	r.coveragePercent, err = m.Int64Histogram("syrinx.reed.coverage_percent")
	if err != nil {
		panic(err)
	}
	return r
}

func (r *OTEL) UserCreated(ctx context.Context, signupMode string) {
	r.usersCreated.Add(ctx, 1, metric.WithAttributes(
		attribute.String("signup.mode", signupMode),
	))
}

func (r *OTEL) UserDeleted(ctx context.Context, userID string, noteHas bool) {
	r.usersDeleted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("user.id", userID),
		attribute.Bool("note.has", noteHas),
	))
}

func (r *OTEL) ReedPublished(ctx context.Context, p ReedPublishedAttrs) {
	tagCount := TagCountAttr(p.TagCount)
	r.reedsPublished.Add(ctx, 1, metric.WithAttributes(
		attribute.String("reed.kind", string(p.Kind)),
		attribute.Bool("tags.has", tagCount > 0),
		attribute.Int("tags.count", tagCount),
		attribute.String("author.id", p.AuthorID),
		attribute.String("reed.id", p.ReedID),
	))
	reedAttrs := metric.WithAttributes(
		attribute.String("reed.kind", string(p.Kind)),
		attribute.String("author.id", p.AuthorID),
		attribute.String("reed.id", p.ReedID),
	)
	r.rawChars.Record(ctx, int64(p.RawChars), reedAttrs)
	r.visibleChars.Record(ctx, int64(p.VisibleChars), reedAttrs)
}

func (r *OTEL) ReedDeleted(ctx context.Context, authorID, reedID string) {
	r.reedsDeleted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("author.id", authorID),
		attribute.String("reed.id", reedID),
	))
}

func (r *OTEL) EchoTargeted(ctx context.Context, targetAuthorID, targetReedID string) {
	r.echoesTargeted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("target.author.id", targetAuthorID),
		attribute.String("target.reed.id", targetReedID),
	))
}

func (r *OTEL) ReedRejectedLength(ctx context.Context, rawChars, visibleChars int) {
	r.reedsRejectedLength.Add(ctx, 1, metric.WithAttributes(
		attribute.Bool("raw.exceeds_max", rawChars > maxReedRawChars),
		attribute.Bool("visible.exceeds_max", visibleChars > maxReedVisibleChars),
	))
}

func (r *OTEL) KeyRevoked(ctx context.Context, userID string) {
	r.keysRevoked.Add(ctx, 1, metric.WithAttributes(
		attribute.String("user.id", userID),
	))
}

func (r *OTEL) UserBackup(ctx context.Context, userID string, kind BackupKind) {
	r.usersBackup.Add(ctx, 1, metric.WithAttributes(
		attribute.String("user.id_hash", UserIDHash(userID)),
		attribute.String("backup.kind", string(kind)),
	))
}

func (r *OTEL) ReedCoverage(ctx context.Context, authorID, reedID string, holders, coveragePercent int) {
	attrs := metric.WithAttributes(
		attribute.String("author.id", authorID),
		attribute.String("reed.id", reedID),
	)
	r.holders.Record(ctx, int64(holders), attrs)
	r.coveragePercent.Record(ctx, int64(coveragePercent), attrs)
}

func (r *OTEL) WSMessage(ctx context.Context, direction Direction, msgType string) {
	r.wsMessages.Add(ctx, 1, metric.WithAttributes(
		attribute.String("ws.direction", string(direction)),
		attribute.String("ws.message.type", msgType),
	))
}

// Limits mirrored from main.MaxReedRawChars / MaxReedVisibleChars for rejection attrs.
const (
	maxReedRawChars     = 1400
	maxReedVisibleChars = 140
)
