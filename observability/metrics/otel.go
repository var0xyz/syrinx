package metrics

import (
	"context"

	"github.com/rs/zerolog/log"
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
	keyFetchErrors      metric.Int64Counter
	revokedKeysUsed     metric.Int64Counter
	usersBackup         metric.Int64Counter
	wsMessages          metric.Int64Counter
	rawChars            metric.Int64Histogram
	visibleChars        metric.Int64Histogram
	holders             metric.Int64Histogram
	coveragePercent     metric.Int64Histogram
}

// New builds an OTEL recorder from a meter. An instrument that fails to
// register is logged and left nil; every recording method guards against a
// nil instrument, so a registration failure degrades that one metric to a
// no-op instead of taking down the server.
func New(m metric.Meter) *OTEL {
	r := &OTEL{}

	counter := func(name string) metric.Int64Counter {
		c, err := m.Int64Counter(name)
		if err != nil {
			log.Error().Err(err).Str("instrument", name).Msg("Failed to register metric counter")
			return nil
		}
		return c
	}
	histogram := func(name string) metric.Int64Histogram {
		h, err := m.Int64Histogram(name)
		if err != nil {
			log.Error().Err(err).Str("instrument", name).Msg("Failed to register metric histogram")
			return nil
		}
		return h
	}

	r.usersCreated = counter("syrinx.users.created")
	r.usersDeleted = counter("syrinx.users.deleted")
	r.reedsPublished = counter("syrinx.reeds.published")
	r.reedsDeleted = counter("syrinx.reeds.deleted")
	r.echoesTargeted = counter("syrinx.echoes.targeted")
	r.reedsRejectedLength = counter("syrinx.reeds.rejected.length")
	r.keysRevoked = counter("syrinx.keys.revoked")
	r.keyFetchErrors = counter("syrinx.keys.fetch_errors")
	r.revokedKeysUsed = counter("syrinx.keys.revoked_used")
	r.usersBackup = counter("syrinx.users.backup")
	r.wsMessages = counter("syrinx.ws.messages")
	r.rawChars = histogram("syrinx.reed.content.raw_chars")
	r.visibleChars = histogram("syrinx.reed.content.visible_chars")
	r.holders = histogram("syrinx.reed.holders")
	r.coveragePercent = histogram("syrinx.reed.coverage_percent")
	return r
}

func (r *OTEL) UserCreated(ctx context.Context, signupMode, userID string) {
	if r.usersCreated == nil {
		return
	}
	r.usersCreated.Add(ctx, 1, metric.WithAttributes(
		attribute.String("signup.mode", signupMode),
		attribute.String("user.id_hash", UserIDHash(userID)),
	))
}

func (r *OTEL) UserDeleted(ctx context.Context, userID string, noteHas bool) {
	if r.usersDeleted == nil {
		return
	}
	r.usersDeleted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("user.id_hash", UserIDHash(userID)),
		attribute.Bool("note.has", noteHas),
	))
}

func (r *OTEL) ReedPublished(ctx context.Context, p ReedPublishedAttrs) {
	tagCount := TagCountAttr(p.TagCount)
	authorHash := UserIDHash(p.AuthorID)
	if r.reedsPublished != nil {
		r.reedsPublished.Add(ctx, 1, metric.WithAttributes(
			attribute.String("reed.kind", string(p.Kind)),
			attribute.Bool("tags.has", tagCount > 0),
			attribute.Int("tags.count", tagCount),
			attribute.String("author.id_hash", authorHash),
			attribute.String("reed.id", p.ReedID),
		))
	}
	reedAttrs := metric.WithAttributes(
		attribute.String("reed.kind", string(p.Kind)),
		attribute.String("author.id_hash", authorHash),
		attribute.String("reed.id", p.ReedID),
	)
	if r.rawChars != nil {
		r.rawChars.Record(ctx, int64(p.RawChars), reedAttrs)
	}
	if r.visibleChars != nil {
		r.visibleChars.Record(ctx, int64(p.VisibleChars), reedAttrs)
	}
}

func (r *OTEL) ReedDeleted(ctx context.Context, authorID, reedID string) {
	if r.reedsDeleted == nil {
		return
	}
	r.reedsDeleted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("author.id_hash", UserIDHash(authorID)),
		attribute.String("reed.id", reedID),
	))
}

func (r *OTEL) EchoTargeted(ctx context.Context, targetAuthorID, targetReedID string) {
	if r.echoesTargeted == nil {
		return
	}
	r.echoesTargeted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("target.author.id_hash", UserIDHash(targetAuthorID)),
		attribute.String("target.reed.id", targetReedID),
	))
}

func (r *OTEL) ReedRejectedLength(ctx context.Context, rawChars, visibleChars int) {
	if r.reedsRejectedLength == nil {
		return
	}
	r.reedsRejectedLength.Add(ctx, 1, metric.WithAttributes(
		attribute.Bool("raw.exceeds_max", rawChars > maxReedRawChars),
		attribute.Bool("visible.exceeds_max", visibleChars > maxReedVisibleChars),
	))
}

func (r *OTEL) KeyRevoked(ctx context.Context, userID string) {
	if r.keysRevoked == nil {
		return
	}
	r.keysRevoked.Add(ctx, 1, metric.WithAttributes(
		attribute.String("user.id_hash", UserIDHash(userID)),
	))
}

// KeyFetchError records a client-reported failure to fetch a key it needed
// to verify signed content it received over an already-authenticated
// connection — an anomaly, not a routine cache miss.
func (r *OTEL) KeyFetchError(ctx context.Context, reporterUserID, targetUserID, fingerprint string) {
	if r.keyFetchErrors == nil {
		return
	}
	r.keyFetchErrors.Add(ctx, 1, metric.WithAttributes(
		attribute.String("reporter.id_hash", UserIDHash(reporterUserID)),
		attribute.String("target.id_hash", UserIDHash(targetUserID)),
		attribute.String("key.fingerprint", fingerprint),
	))
}

// RevokedKeyUsed records a client-reported signed resource whose timestamp
// falls at or after its signing key's revocation — kept in the clear
// (fingerprint, not user identity) for later security analysis.
func (r *OTEL) RevokedKeyUsed(ctx context.Context, reporterUserID, targetUserID, fingerprint string) {
	if r.revokedKeysUsed == nil {
		return
	}
	r.revokedKeysUsed.Add(ctx, 1, metric.WithAttributes(
		attribute.String("reporter.id_hash", UserIDHash(reporterUserID)),
		attribute.String("target.id_hash", UserIDHash(targetUserID)),
		attribute.String("key.fingerprint", fingerprint),
	))
}

func (r *OTEL) UserBackup(ctx context.Context, userID string, kind BackupKind) {
	if r.usersBackup == nil {
		return
	}
	r.usersBackup.Add(ctx, 1, metric.WithAttributes(
		attribute.String("user.id_hash", UserIDHash(userID)),
		attribute.String("backup.kind", string(kind)),
	))
}

func (r *OTEL) ReedCoverage(ctx context.Context, authorID, reedID string, holders, coveragePercent int) {
	attrs := metric.WithAttributes(
		attribute.String("author.id_hash", UserIDHash(authorID)),
		attribute.String("reed.id", reedID),
	)
	if r.holders != nil {
		r.holders.Record(ctx, int64(holders), attrs)
	}
	if r.coveragePercent != nil {
		r.coveragePercent.Record(ctx, int64(coveragePercent), attrs)
	}
}

func (r *OTEL) WSMessage(ctx context.Context, direction Direction, msgType string) {
	if r.wsMessages == nil {
		return
	}
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
