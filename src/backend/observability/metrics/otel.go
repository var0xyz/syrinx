package metrics

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const scope = "syrinx/business"

// OTEL implements Recorder with the OpenTelemetry SDK.
type OTEL struct {
	usersCreated         metric.Int64Counter
	usersDeleted         metric.Int64Counter
	usersUpdated         metric.Int64Counter
	reedsPublished       metric.Int64Counter
	reedsTags            metric.Int64Counter
	reedsDeleted         metric.Int64Counter
	threadsPublished     metric.Int64Counter
	threadSize           metric.Int64Histogram
	echoesTargeted       metric.Int64Counter
	keysRevoked          metric.Int64Counter
	keyFetchErrors       metric.Int64Counter
	revokedKeysUsed      metric.Int64Counter
	contentRejected      metric.Int64Counter
	mentionClaimRejected metric.Int64Counter
	usersBackup          metric.Int64Counter
	wsMessages           metric.Int64Counter
	federationRelay      metric.Int64Counter
	holders              metric.Int64Histogram
	coveragePercent      metric.Int64Histogram

	adminRequestsOpened    metric.Int64Counter
	adminRequestsRefused   metric.Int64Counter
	adminRequestsDecided   metric.Int64Counter
	adminRequestsMoot      metric.Int64Counter
	adminDecisionSeconds   metric.Int64Histogram
	adminActionsExecuted   metric.Int64Counter
	suspendedRefusals      metric.Int64Counter
	adminMailSent          metric.Int64Counter
	adminInvitesRevoked    metric.Int64Counter
	reservedNamesHits      metric.Int64Counter
	foreignKicks           metric.Int64Counter
	flagsCreated           metric.Int64Counter
	flagsDismissed         metric.Int64Counter
	ledgerAppended         metric.Int64Counter
	serverKeyRevocations   metric.Int64Gauge
	authRejected           metric.Int64Counter
	wsHandshakeRejected    metric.Int64Counter
	wsConnections          metric.Int64UpDownCounter
	relayPending           metric.Int64UpDownCounter
	federationPending      metric.Int64UpDownCounter
	wsSessions             metric.Int64Counter
	signupRejected         metric.Int64Counter
	usernameChecks         metric.Int64Counter
	cooldownHits           metric.Int64Counter
	blockRefusals          metric.Int64Counter
	invitesCreated         metric.Int64Counter
	invitesClaimed         metric.Int64Counter
	invitesDeleted         metric.Int64Counter
	follows                metric.Int64Counter
	likes                  metric.Int64Counter
	ripples                metric.Int64Counter
	pins                   metric.Int64Counter
	blocks                 metric.Int64Counter
	vouches                metric.Int64Counter
	verificationWindows    metric.Int64Counter
	devicesBound           metric.Int64Counter
	accountRecovery        metric.Int64Counter
	serverRecovery         metric.Int64Counter
	mailbox                metric.Int64Counter
	userSearches           metric.Int64Counter
	federationLifecycle    metric.Int64Counter
	federationDeliveryOwed metric.Int64Gauge
	cryptoDuration         metric.Int64Histogram
}

// New builds an OTEL recorder from a meter. An instrument that fails to
// register is logged and left nil; every recording method guards against a
// nil instrument, so a registration failure degrades that one metric to a
// no-op instead of taking down the server.
func New(m metric.Meter) *OTEL {
	r := &OTEL{}

	logFailure := func(name string, err error) {
		log.Error().Err(err).Str("instrument", name).Msg("Failed to register metric instrument")
	}
	counter := func(name string) metric.Int64Counter {
		c, err := m.Int64Counter(name)
		if err != nil {
			logFailure(name, err)
			return nil
		}
		return c
	}
	histogram := func(name string) metric.Int64Histogram {
		h, err := m.Int64Histogram(name)
		if err != nil {
			logFailure(name, err)
			return nil
		}
		return h
	}
	gauge := func(name string) metric.Int64Gauge {
		g, err := m.Int64Gauge(name)
		if err != nil {
			logFailure(name, err)
			return nil
		}
		return g
	}

	r.usersCreated = counter("syrinx.users.created")
	r.usersDeleted = counter("syrinx.users.deleted")
	r.usersUpdated = counter("syrinx.users.updated")
	r.reedsPublished = counter("syrinx.reeds.published")
	r.reedsTags = counter("syrinx.reeds.tags")
	r.reedsDeleted = counter("syrinx.reeds.deleted")
	r.threadsPublished = counter("syrinx.threads.published")
	r.threadSize = histogram("syrinx.thread.size")
	r.echoesTargeted = counter("syrinx.echoes.targeted")
	r.keysRevoked = counter("syrinx.keys.revoked")
	r.keyFetchErrors = counter("syrinx.keys.fetch_errors")
	r.revokedKeysUsed = counter("syrinx.keys.revoked_used")
	r.contentRejected = counter("syrinx.content.rejected")
	r.mentionClaimRejected = counter("syrinx.mentions.claim_rejected")
	r.usersBackup = counter("syrinx.users.backup")
	r.wsMessages = counter("syrinx.ws.messages")
	r.federationRelay = counter("syrinx.federation.relay")
	r.holders = histogram("syrinx.reed.holders")
	r.coveragePercent = histogram("syrinx.reed.coverage_percent")

	r.adminRequestsOpened = counter("syrinx.admin.requests.opened")
	r.adminRequestsRefused = counter("syrinx.admin.requests.refused")
	r.adminRequestsDecided = counter("syrinx.admin.requests.decided")
	r.adminRequestsMoot = counter("syrinx.admin.requests.moot")
	r.adminDecisionSeconds = histogram("syrinx.admin.request.decision_seconds")
	r.adminActionsExecuted = counter("syrinx.admin.actions.executed")
	r.suspendedRefusals = counter("syrinx.admin.suspended_refusals")
	r.adminMailSent = counter("syrinx.admin.mail.sent")
	r.adminInvitesRevoked = counter("syrinx.admin.invites.revoked")
	r.reservedNamesHits = counter("syrinx.admin.reserved_usernames.hits")
	r.foreignKicks = counter("syrinx.admin.foreign_kicks")
	r.flagsCreated = counter("syrinx.flags.created")
	r.flagsDismissed = counter("syrinx.flags.dismissed")
	r.ledgerAppended = counter("syrinx.ledger.appended")
	r.serverKeyRevocations = gauge("syrinx.server_key.revocations")
	r.authRejected = counter("syrinx.auth.rejected")
	r.wsHandshakeRejected = counter("syrinx.ws.handshake.rejected")
	if c, err := m.Int64UpDownCounter("syrinx.ws.connections"); err != nil {
		logFailure("syrinx.ws.connections", err)
	} else {
		r.wsConnections = c
	}
	if c, err := m.Int64UpDownCounter("syrinx.relay.pending"); err != nil {
		logFailure("syrinx.relay.pending", err)
	} else {
		r.relayPending = c
	}
	if c, err := m.Int64UpDownCounter("syrinx.federation.pending"); err != nil {
		logFailure("syrinx.federation.pending", err)
	} else {
		r.federationPending = c
	}
	r.wsSessions = counter("syrinx.ws.sessions")
	r.signupRejected = counter("syrinx.signup.rejected")
	r.usernameChecks = counter("syrinx.username.checks")
	r.cooldownHits = counter("syrinx.cooldown.hits")
	r.blockRefusals = counter("syrinx.blocks.refusals")
	r.invitesCreated = counter("syrinx.invites.created")
	r.invitesClaimed = counter("syrinx.invites.claimed")
	r.invitesDeleted = counter("syrinx.invites.deleted")
	r.follows = counter("syrinx.follows")
	r.likes = counter("syrinx.likes")
	r.ripples = counter("syrinx.ripples")
	r.pins = counter("syrinx.pins")
	r.blocks = counter("syrinx.blocks")
	r.vouches = counter("syrinx.vouches")
	r.verificationWindows = counter("syrinx.verification_windows")
	r.devicesBound = counter("syrinx.devices.bound")
	r.accountRecovery = counter("syrinx.account_recovery")
	r.serverRecovery = counter("syrinx.server_recovery")
	r.mailbox = counter("syrinx.mailbox")
	r.userSearches = counter("syrinx.search.users")
	r.federationLifecycle = counter("syrinx.federation.lifecycle")
	r.federationDeliveryOwed = gauge("syrinx.federation.delivery.backlog")
	// Microseconds: an Ed25519 signature takes ~25 µs, below a millisecond.
	r.cryptoDuration = histogram("syrinx.crypto.duration_us")
	return r
}

// add increments c by one, a no-op when c failed to register.
func add(ctx context.Context, c metric.Int64Counter, attrs ...attribute.KeyValue) {
	addN(ctx, c, 1, attrs...)
}

// addN increments c by n, a no-op when c failed to register.
func addN(ctx context.Context, c metric.Int64Counter, n int64, attrs ...attribute.KeyValue) {
	if c == nil {
		return
	}
	c.Add(ctx, n, metric.WithAttributes(attrs...))
}

func record(ctx context.Context, h metric.Int64Histogram, v int64, attrs ...attribute.KeyValue) {
	if h == nil {
		return
	}
	h.Record(ctx, v, metric.WithAttributes(attrs...))
}

func set(ctx context.Context, g metric.Int64Gauge, v int64, attrs ...attribute.KeyValue) {
	if g == nil {
		return
	}
	g.Record(ctx, v, metric.WithAttributes(attrs...))
}

func str(k, v string) attribute.KeyValue { return attribute.String(k, v) }

// CryptoOperation records one sign or verify; its count is the operation rate.
func (r *OTEL) CryptoOperation(ctx context.Context, op, algorithm string, ok bool, took time.Duration) {
	record(ctx, r.cryptoDuration, took.Microseconds(),
		str("crypto.op", op),
		str("crypto.algorithm", algorithm),
		attribute.Bool("ok", ok),
	)
}

func (r *OTEL) UserCreated(ctx context.Context, signupMode string) {
	add(ctx, r.usersCreated, str("signup.mode", signupMode))
}

func (r *OTEL) UserDeleted(ctx context.Context, kind RemovalKind, noteHas bool) {
	add(ctx, r.usersDeleted, str("removal.kind", string(kind)), attribute.Bool("note.has", noteHas))
}

func (r *OTEL) UserUpdated(ctx context.Context, renamed bool) {
	add(ctx, r.usersUpdated, attribute.Bool("renamed", renamed))
}

func (r *OTEL) ReedPublished(ctx context.Context, p ReedPublishedAttrs) {
	tagCount := TagCountAttr(p.TagCount)
	add(ctx, r.reedsPublished,
		str("reed.kind", string(p.Kind)),
		attribute.Bool("tags.has", tagCount > 0),
		attribute.Int("tags.count", tagCount),
	)
	// Exact, unlike the bucketed tags.count attribute.
	if p.TagCount > 0 {
		addN(ctx, r.reedsTags, int64(p.TagCount), str("reed.kind", string(p.Kind)))
	}
}

func (r *OTEL) ReedDeleted(ctx context.Context) {
	add(ctx, r.reedsDeleted)
}

func (r *OTEL) ThreadPublished(ctx context.Context, parts int) {
	add(ctx, r.threadsPublished)
	record(ctx, r.threadSize, int64(parts))
}

func (r *OTEL) EchoTargeted(ctx context.Context) {
	add(ctx, r.echoesTargeted)
}

func (r *OTEL) KeyRevoked(ctx context.Context, kind RevocationKind) {
	add(ctx, r.keysRevoked, str("revocation.kind", string(kind)))
}

// KeyFetchError records a client-reported failure to fetch a key it needed
// to verify signed content it received over an already-authenticated
// connection — an anomaly, not a routine cache miss.
func (r *OTEL) KeyFetchError(ctx context.Context, reporterUserID, targetUserID, keyID string) {
	add(ctx, r.keyFetchErrors,
		str("reporter.id", reporterUserID),
		str("target.id", targetUserID),
		str("key.id", keyID),
	)
}

// RevokedKeyUsed records a client-reported signed resource whose timestamp
// falls at or after its signing key's revocation.
func (r *OTEL) RevokedKeyUsed(ctx context.Context, reporterUserID, targetUserID, keyID string) {
	add(ctx, r.revokedKeysUsed,
		str("reporter.id", reporterUserID),
		str("target.id", targetUserID),
		str("key.id", keyID),
	)
}

// ContentRejected records a client-reported signed resource that failed
// verification and was refused a local write. reason may be empty.
func (r *OTEL) ContentRejected(ctx context.Context, reporterUserID, authorID, storeName, reason string) {
	add(ctx, r.contentRejected,
		str("reporter.id", reporterUserID),
		str("author.id", authorID),
		str("store.name", storeName),
		str("reason", reason),
	)
}

// MentionClaimRejected records a claimed mention that wasn't really
// present in the reed. Carries both parties, not just the reporter.
func (r *OTEL) MentionClaimRejected(ctx context.Context, authorID, reporterUserID, reason string) {
	add(ctx, r.mentionClaimRejected,
		str("author.id", authorID),
		str("reporter.id", reporterUserID),
		str("reason", reason),
	)
}

func (r *OTEL) UserBackup(ctx context.Context, kind BackupKind) {
	add(ctx, r.usersBackup, str("backup.kind", string(kind)))
}

func (r *OTEL) ReedCoverage(ctx context.Context, holders, coveragePercent int) {
	record(ctx, r.holders, int64(holders))
	record(ctx, r.coveragePercent, int64(coveragePercent))
}

func (r *OTEL) WSMessage(ctx context.Context, direction Direction, msgType string) {
	add(ctx, r.wsMessages, str("ws.direction", string(direction)), str("ws.message.type", msgType))
}

// RelayEvent moves the count of open pending relay events by one.
func (r *OTEL) RelayEvent(ctx context.Context, lifecycle RelayLifecycle, eventKind string) {
	if r.relayPending == nil {
		return
	}
	delta := int64(1)
	if lifecycle == RelayEventDeleted {
		delta = -1
	}
	r.relayPending.Add(ctx, delta, metric.WithAttributes(str("event.kind", eventKind)))
}

// FederationRelay records one federation HTTP call, either side: this
// server handling an inbound call from peerServerID, or calling out to it.
func (r *OTEL) FederationRelay(ctx context.Context, direction Direction, peerServerID, leg string, ok bool) {
	add(ctx, r.federationRelay,
		str("federation.direction", string(direction)),
		str("peer.server_id", peerServerID),
		str("federation.leg", leg),
		attribute.Bool("ok", ok),
	)
}

func (r *OTEL) AdminRequestOpened(ctx context.Context, action, requesterID, requesterRole string, targets int) {
	add(ctx, r.adminRequestsOpened,
		str("action", action),
		str("requester.id", requesterID),
		str("requester.role", requesterRole),
		attribute.Int("targets.count", targets),
	)
}

func (r *OTEL) AdminRequestRefused(ctx context.Context, action, actorID string, stage AdminStage, reason string) {
	add(ctx, r.adminRequestsRefused,
		str("action", action),
		str("actor.id", actorID),
		str("stage", string(stage)),
		str("reason", reason),
	)
}

// AdminRequestDecided records a decision; wait is open-to-decision time,
// recorded for approvals and rejections only.
func (r *OTEL) AdminRequestDecided(ctx context.Context, action, decision, requesterID, deciderID string, wait time.Duration) {
	add(ctx, r.adminRequestsDecided,
		str("action", action),
		str("decision", decision),
		str("requester.id", requesterID),
		str("decider.id", deciderID),
	)
	if wait > 0 {
		record(ctx, r.adminDecisionSeconds, int64(wait.Seconds()), str("action", action))
	}
}

func (r *OTEL) AdminRequestMoot(ctx context.Context, action string) {
	add(ctx, r.adminRequestsMoot, str("action", action))
}

func (r *OTEL) AdminActionExecuted(ctx context.Context, action, targetID, requesterID, approverID string, ok bool) {
	add(ctx, r.adminActionsExecuted,
		str("action", action),
		str("target.id", targetID),
		str("requester.id", requesterID),
		str("approver.id", approverID),
		attribute.Bool("ok", ok),
	)
}

func (r *OTEL) SuspendedRefusal(ctx context.Context, userID string, surface Surface) {
	add(ctx, r.suspendedRefusals, str("user.id", userID), str("surface", string(surface)))
}

func (r *OTEL) AdminMailSent(ctx context.Context, action, senderID string, recipients, failed int) {
	add(ctx, r.adminMailSent,
		str("action", action),
		str("sender.id", senderID),
		attribute.Int("recipients.count", recipients),
		attribute.Int("failed.count", failed),
	)
}

func (r *OTEL) AdminInvitesRevoked(ctx context.Context, action, targetID string, count int) {
	addN(ctx, r.adminInvitesRevoked, int64(count), str("action", action), str("target.id", targetID))
}

func (r *OTEL) ReservedUsernameHit(ctx context.Context, surface, userID string) {
	add(ctx, r.reservedNamesHits, str("surface", surface), str("user.id", userID))
}

func (r *OTEL) ForeignKick(ctx context.Context, peerServerID string, ok bool) {
	add(ctx, r.foreignKicks, str("peer.server_id", peerServerID), attribute.Bool("ok", ok))
}

func (r *OTEL) FlagCreated(ctx context.Context, reporterID, authorID, reedID string, origin Origin, noteHas bool) {
	add(ctx, r.flagsCreated,
		str("reporter.id", reporterID),
		str("author.id", authorID),
		str("reed.id", reedID),
		str("origin", string(origin)),
		attribute.Bool("note.has", noteHas),
	)
}

func (r *OTEL) FlagsDismissed(ctx context.Context, adminID, reedID string, count int) {
	add(ctx, r.flagsDismissed,
		str("admin.id", adminID),
		str("reed.id", reedID),
		attribute.Int("flags.count", count),
	)
}

func (r *OTEL) LedgerAppended(ctx context.Context, entryKind string) {
	add(ctx, r.ledgerAppended, str("entry.kind", entryKind))
}

// ServerKeyRevocations reports how many server-key revocations exist, set
// at boot; a jump between boots is a rotation.
func (r *OTEL) ServerKeyRevocations(ctx context.Context, compromised bool, total int) {
	set(ctx, r.serverKeyRevocations, int64(total), attribute.Bool("compromised", compromised))
}

// AuthRejected records a request the signature-auth middleware refused.
// keyID is as claimed by the caller; userID is empty when the key is unknown.
func (r *OTEL) AuthRejected(ctx context.Context, keyID, userID, reason, route string) {
	add(ctx, r.authRejected,
		str("key.id", keyID),
		str("user.id", userID),
		str("reason", reason),
		str("route", route),
	)
}

// WSHandshakeRejected records a refused WebSocket handshake. keyID is as
// claimed; userID is empty until the handshake authenticated.
func (r *OTEL) WSHandshakeRejected(ctx context.Context, keyID, userID, reason string) {
	add(ctx, r.wsHandshakeRejected, str("key.id", keyID), str("user.id", userID), str("reason", reason))
}

func (r *OTEL) WSSession(ctx context.Context, connected bool) {
	event, delta := "disconnect", int64(-1)
	if connected {
		event, delta = "connect", 1
	}
	add(ctx, r.wsSessions, str("event", event))
	if r.wsConnections != nil {
		r.wsConnections.Add(ctx, delta)
	}
}

func (r *OTEL) SignupRejected(ctx context.Context, signupMode, reason string) {
	add(ctx, r.signupRejected, str("signup.mode", signupMode), str("reason", reason))
}

func (r *OTEL) UsernameCheck(ctx context.Context, available bool) {
	add(ctx, r.usernameChecks, attribute.Bool("available", available))
}

func (r *OTEL) CooldownHit(ctx context.Context, kind, userID string) {
	add(ctx, r.cooldownHits, str("kind", kind), str("user.id", userID))
}

func (r *OTEL) BlockRefusal(ctx context.Context, viewerID, authorID string, surface Surface) {
	add(ctx, r.blockRefusals,
		str("viewer.id", viewerID),
		str("author.id", authorID),
		str("surface", string(surface)),
	)
}

func (r *OTEL) InviteCreated(ctx context.Context, adminInvite bool) {
	add(ctx, r.invitesCreated, attribute.Bool("admin_invite", adminInvite))
}

func (r *OTEL) InviteClaimed(ctx context.Context, adminInvite bool) {
	add(ctx, r.invitesClaimed, attribute.Bool("admin_invite", adminInvite))
}

func (r *OTEL) InviteDeleted(ctx context.Context) {
	add(ctx, r.invitesDeleted)
}

func (r *OTEL) Follow(ctx context.Context, op Op, remote bool) {
	add(ctx, r.follows, str("op", string(op)), attribute.Bool("remote", remote))
}

func (r *OTEL) Like(ctx context.Context, op Op) {
	add(ctx, r.likes, str("op", string(op)))
}

func (r *OTEL) Ripple(ctx context.Context, op Op) {
	add(ctx, r.ripples, str("op", string(op)))
}

func (r *OTEL) Pin(ctx context.Context, op Op) {
	add(ctx, r.pins, str("op", string(op)))
}

func (r *OTEL) Block(ctx context.Context, op Op, origin Origin) {
	add(ctx, r.blocks, str("op", string(op)), str("origin", string(origin)))
}

func (r *OTEL) Vouch(ctx context.Context, op Op, ok bool, reason string) {
	add(ctx, r.vouches, str("op", string(op)), attribute.Bool("ok", ok), str("reason", reason))
}

func (r *OTEL) VerificationWindow(ctx context.Context, op string) {
	add(ctx, r.verificationWindows, str("op", op))
}

func (r *OTEL) DeviceBound(ctx context.Context) {
	add(ctx, r.devicesBound)
}

func (r *OTEL) AccountRecovery(ctx context.Context, stage string, ok bool, reason string) {
	add(ctx, r.accountRecovery, str("stage", stage), attribute.Bool("ok", ok), str("reason", reason))
}

func (r *OTEL) ServerRecovery(ctx context.Context, stage string, ok bool) {
	add(ctx, r.serverRecovery, str("stage", stage), attribute.Bool("ok", ok))
}

func (r *OTEL) Mailbox(ctx context.Context, op Op, kind string) {
	add(ctx, r.mailbox, str("op", string(op)), str("kind", kind))
}

func (r *OTEL) UserSearch(ctx context.Context, remote bool) {
	add(ctx, r.userSearches, attribute.Bool("remote", remote))
}

func (r *OTEL) FederationLifecycle(ctx context.Context, event, peerServerID, adminID string) {
	add(ctx, r.federationLifecycle,
		str("event", event),
		str("peer.server_id", peerServerID),
		str("admin.id", adminID),
	)
}

// FederationPending moves the count of relay requests open with a peer.
func (r *OTEL) FederationPending(ctx context.Context, direction Direction, peerServerID string, delta int64) {
	if r.federationPending == nil {
		return
	}
	r.federationPending.Add(ctx, delta, metric.WithAttributes(
		str("federation.direction", string(direction)),
		str("peer.server_id", peerServerID),
	))
}

// FederationDeliveryBacklog reports how many author streams to a peer are
// behind, sampled each time the behind streams are retried.
func (r *OTEL) FederationDeliveryBacklog(ctx context.Context, peerServerID string, items int) {
	set(ctx, r.federationDeliveryOwed, int64(items), str("peer.server_id", peerServerID))
}
