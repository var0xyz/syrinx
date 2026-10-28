package main

import (
	"context"
	"database/sql"
)

// The peer event queue's writers, built into the server and ops alike:
// ops queues a rotated server key for the server to send on its next boot.

// Event kinds a peer's queue holds.
const (
	peerEventNewReed        = "new_reed"
	peerEventReedRemoval    = "reed_removal"
	peerEventThreadRemoval  = "thread_removal"
	peerEventAccountRemoval = "account_removal"
	peerEventBlock          = "block"
	peerEventFollow         = "follow"
	peerEventKeyRevocation  = "key_revocation"
	peerEventServerKey      = "server_key"
)

// peerEventExecer is a transaction or the database.
type peerEventExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// enqueuePeerEventTx owes kind about subject to every peer not revoked,
// down ones included.
func enqueuePeerEventTx(ctx context.Context, q peerEventExecer, kind, subjectID string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO peer_event_queue (server_id, kind, subject_id)
		SELECT server_id, $1, $2 FROM server_mesh WHERE revoked_at IS NULL
		ON CONFLICT DO NOTHING
	`, kind, subjectID)
	return err
}

// enqueuePublishedRemovalTx owes every peer the removal of reedID when it
// is a reed published here: peers never heard of any other.
func enqueuePublishedRemovalTx(ctx context.Context, q peerEventExecer, kind, reedID string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO peer_event_queue (server_id, kind, subject_id)
		SELECT m.server_id, $1, $2::varchar FROM server_mesh m
		WHERE m.revoked_at IS NULL
		  AND EXISTS (SELECT 1 FROM reeds r WHERE r.id = $2::varchar AND r.published_at IS NOT NULL)
		ON CONFLICT DO NOTHING
	`, kind, reedID)
	return err
}

// enqueuePeerEventForTx owes kind about the (subject, object) pair to
// serverID alone, unless it is revoked or not a peer. The pair's state is
// read when it's sent, so one already waiting just moves to the back:
// a drain answering the old id then leaves it queued.
func enqueuePeerEventForTx(ctx context.Context, q peerEventExecer, serverID, kind, subjectID, objectID string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO peer_event_queue (server_id, kind, subject_id, object_id)
		SELECT server_id, $2, $3, $4 FROM server_mesh WHERE server_id = $1 AND revoked_at IS NULL
		ON CONFLICT (server_id, kind, subject_id, object_id)
		DO UPDATE SET id = nextval(pg_get_serial_sequence('peer_event_queue', 'id'))
	`, serverID, kind, subjectID, objectID)
	return err
}
