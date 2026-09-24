//go:build !ops && !ripplescleanup

package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/rs/zerolog/log"
	pb "syrinx/proto"
)

// realtimeBusChannel is the Postgres NOTIFY channel every replica listens
// on. One channel for all users: a replica drops what it doesn't hold, and
// at this scale that's cheaper than managing per-user channels.
const realtimeBusChannel = "ws_deliver"

// realtimeBusMaxPayload is Postgres' NOTIFY payload ceiling. Anything
// larger can't ride the bus and is sent as a bare wake-up instead.
const realtimeBusMaxPayload = 7500

// Listener reconnect backoff bounds, passed to pq.NewListener.
const (
	realtimeBusMinReconnect = 1 * time.Second
	realtimeBusMaxReconnect = 30 * time.Second
)

// realtimeBusEnvelope is what crosses between replicas. Frame is the
// base64 of a marshaled pb.WSMessage, or empty for a wake-up (see Publish).
type realtimeBusEnvelope struct {
	UserID string `json:"userID"`
	Frame  string `json:"frame,omitempty"`
}

// realtimeBus forwards WS deliveries between replicas over Postgres
// LISTEN/NOTIFY. Best-effort: notifications sent while disconnected are
// lost, and catchUp on SYNC_REQUEST repairs that on reconnect.
type realtimeBus struct {
	listener *pq.Listener
	db       *sql.DB
	deliver  func(userID string, frame []byte) bool
	done     chan struct{}
}

// newRealtimeBus opens a dedicated listener connection. pq.Listener needs
// its own connection and cannot come from the shared *sql.DB pool.
func newRealtimeBus(dbURL string, db *sql.DB, deliver func(userID string, frame []byte) bool) (*realtimeBus, error) {
	bus := &realtimeBus{db: db, deliver: deliver, done: make(chan struct{})}

	bus.listener = pq.NewListener(dbURL, realtimeBusMinReconnect, realtimeBusMaxReconnect, func(ev pq.ListenerEventType, err error) {
		switch ev {
		case pq.ListenerEventConnectionAttemptFailed, pq.ListenerEventDisconnected:
			log.Error().Err(err).Msg("Realtime bus listener disconnected")
		case pq.ListenerEventReconnected:
			// Whatever was published while we were away is gone; the
			// clients' own reconnect catch-up covers it.
			log.Warn().Msg("Realtime bus listener reconnected; notifications sent while disconnected were dropped")
		}
	})

	if err := bus.listener.Listen(realtimeBusChannel); err != nil {
		bus.listener.Close()
		return nil, fmt.Errorf("failed to listen on %s: %w", realtimeBusChannel, err)
	}
	return bus, nil
}

// Start consumes notifications until Stop. Run it in its own goroutine.
func (b *realtimeBus) Start() {
	for {
		select {
		case <-b.done:
			return
		case n := <-b.listener.NotificationChannel():
			if n == nil {
				// nil marks a reconnect, not a real notification.
				continue
			}
			b.handle(n.Extra)
		}
	}
}

// Stop closes the listener connection.
func (b *realtimeBus) Stop() {
	close(b.done)
	if err := b.listener.Close(); err != nil {
		log.Error().Err(err).Msg("Failed to close realtime bus listener")
	}
}

// handle delivers one envelope to a locally-held socket, dropping it when
// this replica isn't the one holding that user's connection.
func (b *realtimeBus) handle(payload string) {
	var env realtimeBusEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		log.Error().Err(err).Msg("Failed to decode realtime bus envelope")
		return
	}
	if env.UserID == "" || env.Frame == "" {
		return
	}

	frame, err := base64.StdEncoding.DecodeString(env.Frame)
	if err != nil {
		log.Error().Err(err).Str("userID", env.UserID).Msg("Failed to decode realtime bus frame")
		return
	}
	b.deliver(env.UserID, frame)
}

// Publish hands a marshaled frame to whichever replica holds userID's
// socket. A frame past the NOTIFY ceiling publishes as a bare wake-up,
// leaving the durable catch-up path to supply the payload.
func (b *realtimeBus) Publish(userID string, frame []byte) error {
	env := realtimeBusEnvelope{UserID: userID, Frame: base64.StdEncoding.EncodeToString(frame)}
	if len(env.Frame) > realtimeBusMaxPayload {
		log.Debug().Str("userID", userID).Int("bytes", len(frame)).Msg("Frame too large for realtime bus; publishing wake-up only")
		env.Frame = ""
	}

	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = b.db.Exec(`SELECT pg_notify($1, $2)`, realtimeBusChannel, string(payload))
	return err
}

// marshalAndPublish is the SendToUser fallback: marshal once, hand off.
func (b *realtimeBus) marshalAndPublish(userID string, msg *pb.WSMessage) error {
	frame, err := marshalWSMessage(msg)
	if err != nil {
		return err
	}
	return b.Publish(userID, frame)
}
