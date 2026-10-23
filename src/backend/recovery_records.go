//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// Outcomes of one reported record.
const (
	recoveryRecordStored  = "stored"
	recoveryRecordPending = "pending"
	recoveryRecordRefused = "refused"
)

// maxRecoveryRecordsBatch caps one POST /recovery/records.
const maxRecoveryRecordsBatch = 100

// recoveryWaiting parks a record until on is restored: an identity, whose
// keys come with it, or a ledger entry (ledgerWaitKey).
type recoveryWaiting struct{ on string }

func (w recoveryWaiting) Error() string { return "waiting on " + w.on }

// recoveryRefusal is a record that will never verify or doesn't belong here.
type recoveryRefusal struct{ reason string }

func (r recoveryRefusal) Error() string { return r.reason }

func refuseRecord(format string, args ...any) error {
	return recoveryRefusal{reason: fmt.Sprintf(format, args...)}
}

func ledgerWaitKey(id uint64) string { return "ledger/" + strconv.FormatUint(id, 10) }

// recoveredRecord is one kind of signed record a device reports back.
type recoveredRecord interface {
	// restore verifies the record and stores it in tx, returning
	// recoveryWaiting when it names something not restored yet.
	restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error
}

// recoveryRecordEnv is what restoring a record needs.
type recoveryRecordEnv struct {
	db        *sql.DB
	serverID  string
	serverKey recoveryServerKeyLookup
	verifier  recoveryVerifier
	// peerKey finds a peer's server keys; activePeer says whether a server
	// is a peer held here and not revoked.
	peerKey    func(peerID string) recoveryServerKeyLookup
	activePeer func(ctx context.Context, serverID string) (bool, error)
}

func recoveredRecordOf(msg *pb.RecoveryRecord) (recoveredRecord, error) {
	switch r := msg.GetRecord().(type) {
	case *pb.RecoveryRecord_Like:
		return recoveredLike{likeCertFromPB(r.Like)}, nil
	case *pb.RecoveryRecord_Block:
		return recoveredBlock{blockCertFromPB(r.Block)}, nil
	case *pb.RecoveryRecord_Vouch:
		return recoveredVouch{vouchFromPB(r.Vouch)}, nil
	case *pb.RecoveryRecord_ReedRemoval:
		return recoveredReedRemoval{r.ReedRemoval}, nil
	case *pb.RecoveryRecord_AccountRemoval:
		return recoveredAccountRemoval{r.AccountRemoval}, nil
	case *pb.RecoveryRecord_Thread:
		return recoveredThread{threadRecordFromPB(r.Thread)}, nil
	case *pb.RecoveryRecord_ThreadRemoval:
		return recoveredThreadRemoval{threadRemovalFromPB(r.ThreadRemoval)}, nil
	case *pb.RecoveryRecord_LedgerEntry:
		return recoveredLedgerEntry{r.LedgerEntry}, nil
	case *pb.RecoveryRecord_AdminRequest:
		return recoveredAdminRequest{adminRequestFromPB(r.AdminRequest)}, nil
	case *pb.RecoveryRecord_Flag:
		return recoveredFlag{reedFlagFromPB(r.Flag)}, nil
	}
	return nil, refuseRecord("unknown record kind")
}

// restoreRecord restores msg, or parks it when it names something not
// restored yet. Waking what a stored record unblocks is wake's job.
func (env *recoveryRecordEnv) restoreRecord(ctx context.Context, msg *pb.RecoveryRecord) (status, reason string, err error) {
	rec, err := recoveredRecordOf(msg)
	if err != nil {
		return recoveryRecordRefused, err.Error(), nil
	}
	tx, err := env.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	err = rec.restore(ctx, tx, env)
	var waiting recoveryWaiting
	var refusal recoveryRefusal
	switch {
	case errors.As(err, &waiting):
		tx.Rollback()
		return recoveryRecordPending, waiting.Error(), env.park(ctx, waiting.on, msg)
	case errors.As(err, &refusal):
		return recoveryRecordRefused, refusal.reason, nil
	case err != nil:
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	if entry, ok := rec.(recoveredLedgerEntry); ok {
		return recoveryRecordStored, "", env.wake(ctx, ledgerWaitKey(entry.GetId()))
	}
	return recoveryRecordStored, "", nil
}

// park keeps msg until on is restored. Identical copies park once.
func (env *recoveryRecordEnv) park(ctx context.Context, on string, msg *pb.RecoveryRecord) error {
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	_, err = env.db.ExecContext(ctx, `
		INSERT INTO pending_recovery_records (waiting_on, digest, record)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, on, digest[:], body)
	return err
}

// wake retries every record parked on on. One may park again, on
// something else, or store and wake what waited on it in turn.
func (env *recoveryRecordEnv) wake(ctx context.Context, on string) error {
	rows, err := env.db.QueryContext(ctx, `
		DELETE FROM pending_recovery_records WHERE waiting_on = $1 RETURNING record
	`, on)
	if err != nil {
		return err
	}
	var bodies [][]byte
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			rows.Close()
			return err
		}
		bodies = append(bodies, body)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, body := range bodies {
		var msg pb.RecoveryRecord
		if err := proto.Unmarshal(body, &msg); err != nil {
			return err
		}
		if _, _, err := env.restoreRecord(ctx, &msg); err != nil {
			return err
		}
	}
	return nil
}

// local refuses an id of another server's: its peers aren't restored.
func (env *recoveryRecordEnv) local(id string) error {
	if _, serverID, ok := parseIdentityID(identityID(id)); !ok || serverID != env.serverID {
		return refuseRecord("%s isn't a user of this server", id)
	}
	return nil
}

// localEntity is local for a key, reed or record id, returning its owner.
func (env *recoveryRecordEnv) localEntity(id string) (string, error) {
	owner, ok := authorOf(identityID(id))
	if !ok {
		return "", refuseRecord("malformed id %s", id)
	}
	return string(owner), env.local(string(owner))
}

// countersignedHere checks sig is this server's over payload(fingerprint).
func (env *recoveryRecordEnv) countersignedHere(ctx context.Context, sig ServerSignature, payload func(fp string) []byte) error {
	fp, serverID, ok := parseIdentityID(identityID(sig.ID))
	if !ok || serverID != env.serverID || sig.Armor == "" || sig.SignedAt.IsZero() {
		return refuseRecord("not countersigned by this server")
	}
	armor, err := env.serverKey(ctx, fp, sig.SignedAt)
	if err != nil {
		return err
	}
	if armor == "" {
		return refuseRecord("unknown server key %s", fp)
	}
	if err := env.verifier.verifySignature(ctx, string(payload(fp)), sig.Armor, armor); err != nil {
		return refuseRecord("bad countersignature")
	}
	return nil
}

// signedBy checks sig is a key of owner's over payload. A key not restored
// yet parks the record until its owner's identity is.
func (env *recoveryRecordEnv) signedBy(ctx context.Context, tx *sql.Tx, owner string, sig UserSignature, payload []byte) error {
	keyOwner, err := env.localEntity(sig.ID)
	if err != nil {
		return err
	}
	if keyOwner != owner || sig.Armor == "" {
		return refuseRecord("not signed by a key of %s's", owner)
	}
	var armor string
	err = tx.QueryRowContext(ctx, `SELECT armor FROM public_keys WHERE id = $1 AND owner = $2`, sig.ID, owner).Scan(&armor)
	if err == sql.ErrNoRows {
		return recoveryWaiting{on: owner}
	}
	if err != nil {
		return err
	}
	if err := env.verifier.verifySignature(ctx, string(payload), sig.Armor, armor); err != nil {
		return refuseRecord("bad signature by %s", sig.ID)
	}
	return nil
}

// signedOnPeer checks a record made on one of this server's peers: signed
// by owner, one of its users, with a key restored from a device, and
// countersigned by owner's server. A key not restored yet parks the record.
func (env *recoveryRecordEnv) signedOnPeer(ctx context.Context, tx *sql.Tx, owner string, user UserSignature, payload []byte, server ServerSignature, serverPayload func(fp string) []byte) error {
	_, peerID, ok := parseIdentityID(identityID(owner))
	if !ok || env.peerKey == nil || env.activePeer == nil {
		return refuseRecord("%s isn't a user of a peer of this server", owner)
	}
	if active, err := env.activePeer(ctx, peerID); err != nil {
		return err
	} else if !active {
		return refuseRecord("%s isn't a peer of this server", peerID)
	}
	if keyOwner, ok := authorOf(identityID(user.ID)); !ok || string(keyOwner) != owner || user.Armor == "" {
		return refuseRecord("not signed by a key of %s's", owner)
	}
	var armor string
	err := tx.QueryRowContext(ctx, `SELECT armor FROM public_keys WHERE id = $1 AND owner = $2`, user.ID, owner).Scan(&armor)
	if err == sql.ErrNoRows {
		return recoveryWaiting{on: owner}
	}
	if err != nil {
		return err
	}
	if err := env.verifier.verifySignature(ctx, string(payload), user.Armor, armor); err != nil {
		return refuseRecord("bad signature by %s", user.ID)
	}
	fp, sigServerID, ok := parseIdentityID(identityID(server.ID))
	if !ok || sigServerID != peerID || server.Armor == "" || server.SignedAt.IsZero() {
		return refuseRecord("not countersigned by %s", peerID)
	}
	serverArmor, err := env.peerKey(peerID)(ctx, fp, server.SignedAt)
	if err != nil {
		return err
	}
	if serverArmor == "" {
		return refuseRecord("unknown server key %s", server.ID)
	}
	if err := env.verifier.verifySignature(ctx, string(serverPayload(fp)), server.Armor, serverArmor); err != nil {
		return refuseRecord("bad countersignature")
	}
	return nil
}

// mintIdentityTx adds a placeholder identity row for a local user not
// restored yet, so a record naming them can be stored.
func mintIdentityTx(ctx context.Context, tx *sql.Tx, userID string) error {
	_, serverID, ok := parseIdentityID(identityID(userID))
	if !ok {
		return refuseRecord("malformed user id %s", userID)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO identities (id, server_id) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING
	`, userID, serverID)
	return err
}

func existsTx(ctx context.Context, tx *sql.Tx, query string, args ...any) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(`+query+`)`, args...).Scan(&exists)
	return exists, err
}

// insertSignaturesTx stores a user signature and its countersignature.
func insertSignaturesTx(ctx context.Context, tx *sql.Tx, user UserSignature, server ServerSignature) (userSigID, serverSigID int64, err error) {
	if userSigID, err = insertUserSignature(ctx, tx, user.ID, user.Armor); err != nil {
		return 0, 0, err
	}
	serverSigID, err = insertServerSignature(ctx, tx, server.ID, server.Armor, server.SignedAt)
	return userSigID, serverSigID, err
}

type recoveredLike struct{ LikeCert }

func (r recoveredLike) restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	liker, err := env.localEntity(r.UserSignature.ID)
	if err != nil {
		return err
	}
	if _, err := env.localEntity(r.ReedID); err != nil {
		return err
	}
	if err := env.signedBy(ctx, tx, liker, r.UserSignature, buildReedLikeUserPayload(r.ReedID, r.UserSignature.ID)); err != nil {
		return err
	}
	if err := env.countersignedHere(ctx, r.ServerSignature, func(fp string) []byte {
		return buildReedLikeServerPayload(r.ReedID, fp, r.UserSignature.Armor, r.ServerSignature.SignedAt)
	}); err != nil {
		return err
	}
	if held, err := existsTx(ctx, tx, `SELECT 1 FROM reeds_liked WHERE liker_user_id = $1 AND reed_id = $2`, liker, r.ReedID); err != nil || held {
		return err
	}
	if err := insertReedIdentity(ctx, tx, r.ReedID); err != nil {
		return err
	}
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, r.UserSignature, r.ServerSignature)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO reeds_liked (liker_user_id, reed_id, liker_public_key_id, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4, $5)
	`, liker, r.ReedID, r.UserSignature.ID, userSigID, serverSigID)
	return err
}

type recoveredBlock struct{ BlockCert }

func (r recoveredBlock) restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	if err := env.local(r.BlockedUserID); err != nil {
		return err
	}
	if r.UserID == r.BlockedUserID {
		return refuseRecord("a user can't block themselves")
	}
	userPayload := buildBlockUserPayload(r.UserID, r.BlockedUserID, r.UserSignature.ID)
	serverPayload := func(fp string) []byte {
		return buildBlockServerPayload(r.UserID, r.BlockedUserID, fp, r.UserSignature.Armor, r.ServerSignature.SignedAt)
	}
	if err := env.local(r.UserID); err == nil {
		if err := env.signedBy(ctx, tx, r.UserID, r.UserSignature, userPayload); err != nil {
			return err
		}
		if err := env.countersignedHere(ctx, r.ServerSignature, serverPayload); err != nil {
			return err
		}
	} else if err := env.signedOnPeer(ctx, tx, r.UserID, r.UserSignature, userPayload, r.ServerSignature, serverPayload); err != nil {
		return err
	}
	if held, err := existsTx(ctx, tx, `SELECT 1 FROM user_blocks WHERE user_id = $1 AND blocked_user_id = $2`, r.UserID, r.BlockedUserID); err != nil || held {
		return err
	}
	if err := mintIdentityTx(ctx, tx, r.BlockedUserID); err != nil {
		return err
	}
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, r.UserSignature, r.ServerSignature)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_blocks (user_id, blocked_user_id, public_key_id, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4, $5)
	`, r.UserID, r.BlockedUserID, r.UserSignature.ID, userSigID, serverSigID); err != nil {
		return err
	}
	_, err = applyBlockEffectsTx(ctx, tx, r.UserID, r.BlockedUserID)
	return err
}

type recoveredVouch struct{ VouchCert }

func (r recoveredVouch) restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	if err := env.local(r.VoucherUserID); err != nil {
		return err
	}
	if owner, ok := authorOf(identityID(r.ID)); !ok || string(owner) != r.VoucherUserID {
		return refuseRecord("vouch %s isn't %s's", r.ID, r.VoucherUserID)
	}
	if owner, ok := authorOf(identityID(r.SubjectKeyID)); !ok || string(owner) != r.SubjectUserID {
		return refuseRecord("key %s isn't %s's", r.SubjectKeyID, r.SubjectUserID)
	}
	if r.UserSignature.ID != r.VoucherKeyID {
		return refuseRecord("vouch %s isn't signed by its voucher key", r.ID)
	}
	if err := env.signedBy(ctx, tx, r.VoucherUserID, r.UserSignature, buildVouchUserPayload(r.VoucherKeyID, r.SubjectKeyID, r.Note)); err != nil {
		return err
	}
	if err := env.countersignedHere(ctx, r.ServerSignature, func(fp string) []byte {
		return buildVouchServerPayload(r.SubjectKeyID, fp, r.UserSignature.Armor, r.ServerSignature.SignedAt)
	}); err != nil {
		return err
	}
	if w := r.Withdrawal; w != nil {
		if err := env.signedBy(ctx, tx, r.VoucherUserID, w.UserSignature, buildVouchWithdrawalUserPayload(r.ID)); err != nil {
			return err
		}
		if err := env.countersignedHere(ctx, w.ServerSignature, func(fp string) []byte {
			return buildVouchWithdrawalServerPayload(r.ID, fp, w.UserSignature.Armor, w.ServerSignature.SignedAt)
		}); err != nil {
			return err
		}
	}

	var withdrawn bool
	err := tx.QueryRowContext(ctx, `
		SELECT withdrawal_signature_id IS NOT NULL FROM user_vouches WHERE id = $1 FOR UPDATE
	`, r.ID).Scan(&withdrawn)
	switch {
	case err == sql.ErrNoRows:
		if err := insertVouchRow(ctx, tx, r.VouchCert); err != nil {
			return err
		}
	case err != nil:
		return err
	case withdrawn || r.Withdrawal == nil:
		return nil
	}
	if r.Withdrawal == nil {
		return nil
	}
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, r.Withdrawal.UserSignature, r.Withdrawal.ServerSignature)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_vouches SET withdrawal_signature_id = $2, withdrawal_server_signature_id = $3 WHERE id = $1
	`, r.ID, userSigID, serverSigID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM user_vouches_active WHERE vouch_id = $1`, r.ID)
	return err
}

type recoveredReedRemoval struct{ *pb.ReedRemovalCert }

func (r recoveredReedRemoval) restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	reedID, user, server := r.GetReedId(), userSignatureFromPB(r.GetUserSignature()), serverSignatureFromPB(r.GetServerSignature())
	author, err := env.localEntity(reedID)
	if err != nil {
		return err
	}
	if r.GetServerId() != env.serverID || r.GetUserId() != author {
		return refuseRecord("removal of %s names another author or server", reedID)
	}
	if err := env.signedBy(ctx, tx, author, user, buildReedRemovalUserPayload(env.serverID, reedID)); err != nil {
		return err
	}
	if err := env.countersignedHere(ctx, server, func(fp string) []byte {
		return buildReedRemovalServerPayload(env.serverID, reedID, user.ID, fp, user.Armor, server.SignedAt)
	}); err != nil {
		return err
	}
	if held, err := existsTx(ctx, tx, `SELECT 1 FROM reed_removals WHERE reed_id = $1`, reedID); err != nil || held {
		return err
	}
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, user, server)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reed_removals (reed_id, public_key_id, user_signature_id, server_signature_id)
		VALUES ($1, $2, $3, $4)
	`, reedID, user.ID, userSigID, serverSigID); err != nil {
		return err
	}
	return dropRemovedReedLinksTx(ctx, tx, reedID)
}

// dropRemovedReedLinksTx drops what a reed removal takes out of the indexes.
func dropRemovedReedLinksTx(ctx context.Context, tx *sql.Tx, reedID string) error {
	for _, q := range []string{
		`DELETE FROM reed_mentions WHERE mentioning_reed_id = $1`,
		`DELETE FROM reed_admin_mentions WHERE mentioning_reed_id = $1`,
		`DELETE FROM reed_echoes WHERE echoing_reed_id = $1 OR echoed_reed_id = $1`,
	} {
		if _, err := tx.ExecContext(ctx, q, reedID); err != nil {
			return err
		}
	}
	return nil
}

type recoveredAccountRemoval struct{ *pb.AccountRemovalCert }

func (r recoveredAccountRemoval) restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	userID, note := r.GetUserId(), r.GetNote()
	user, server := userSignatureFromPB(r.GetUserSignature()), serverSignatureFromPB(r.GetServerSignature())
	if err := env.local(userID); err != nil {
		return err
	}
	if r.GetServerId() != env.serverID {
		return refuseRecord("removal of %s names another server", userID)
	}
	if err := validateAccountNote(note); err != nil {
		return refuseRecord("%v", err)
	}
	cert := accountRemovalCert{
		UserID:            userID,
		Note:              note,
		ServerSignature:   server.Armor,
		ServerFingerprint: server.ID,
		ServerSignedAt:    server.SignedAt.UTC().Truncate(time.Second),
	}
	if auth := r.GetAdmin(); auth != nil {
		req := adminRequestFromPB(auth.GetRequest())
		if req.Action != adminActionKick || req.State != adminStateExecuted || !slices.Contains(req.Targets, userID) {
			return refuseRecord("not an executed kick of %s", userID)
		}
		if err := (recoveredAdminRequest{req}).restore(ctx, tx, env); err != nil {
			return err
		}
		if err := env.countersignedHere(ctx, server, func(fp string) []byte {
			return buildAccountKickServerPayload(env.serverID, userID, req.ID, req.RequesterSignature.Armor,
				req.Decision.Signature.Armor, fp, server.SignedAt)
		}); err != nil {
			return err
		}
		cert.AdminRequestID = req.ID
	} else {
		if err := env.signedBy(ctx, tx, userID, user, buildAccountRemovalUserPayload(env.serverID, userID, note)); err != nil {
			return err
		}
		if err := env.countersignedHere(ctx, server, func(fp string) []byte {
			return buildAccountRemovalServerPayload(env.serverID, userID, note, fp, user.Armor, server.SignedAt)
		}); err != nil {
			return err
		}
		cert.UserSignature, cert.UserKeyID = user.Armor, user.ID
	}
	if held, err := existsTx(ctx, tx, `SELECT 1 FROM account_removals WHERE user_id = $1`, userID); err != nil || held {
		return err
	}
	if err := mintIdentityTx(ctx, tx, userID); err != nil {
		return err
	}
	if err := insertAccountRemovalRowTx(ctx, tx, cert, identityID(userID)); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM reed_mentions WHERE mentioning_reed_id IN (SELECT id FROM reeds WHERE user_id = $1)`,
		`DELETE FROM reed_echoes WHERE echoing_reed_id IN (SELECT id FROM reeds WHERE user_id = $1)`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	return applyRestoredUserStateTx(ctx, tx, userID)
}

type recoveredThread struct{ threadRecordWire }

func (r recoveredThread) restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	if err := r.verify(ctx, tx, env); err != nil {
		return err
	}
	if held, err := existsTx(ctx, tx, `SELECT 1 FROM reed_threads WHERE id = $1`, r.ThreadID); err != nil || held {
		return err
	}
	for _, reedID := range r.ReedIDs {
		if err := insertReedIdentityOf(ctx, tx, reedID, env.serverID, r.UserID); err != nil {
			return err
		}
	}
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, r.UserSignature, r.ServerSignature)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO reed_threads (id, user_signature_id, server_signature_id) VALUES ($1, $2, $3)
	`, r.ThreadID, userSigID, serverSigID)
	return err
}

func (r recoveredThread) verify(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	if err := env.local(r.UserID); err != nil {
		return err
	}
	ids := r.ReedIDs
	if r.ServerID != env.serverID || len(ids) < 2 || len(ids) > MaxThreadReeds || ids[0] != r.ThreadID {
		return refuseRecord("malformed thread %s", r.ThreadID)
	}
	for i, id := range ids {
		if owner, ok := authorOf(identityID(id)); !ok || string(owner) != r.UserID || slices.Index(ids, id) != i {
			return refuseRecord("thread %s lists a reed not its author's, or twice", r.ThreadID)
		}
	}
	if err := env.signedBy(ctx, tx, r.UserID, r.UserSignature, buildThreadUserPayload(env.serverID, r.ThreadID, ids)); err != nil {
		return err
	}
	return env.countersignedHere(ctx, r.ServerSignature, func(fp string) []byte {
		return buildThreadServerPayload(env.serverID, r.ThreadID, r.UserSignature.ID, fp, r.UserSignature.Armor, r.ServerSignature.SignedAt)
	})
}

type recoveredThreadRemoval struct{ threadRemoval }

func (r recoveredThreadRemoval) restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	c, rec := r.Cert, r.Record
	if c.ThreadID != rec.ThreadID || c.UserID != rec.UserID || c.ServerID != rec.ServerID {
		return refuseRecord("thread removal doesn't match its record")
	}
	if err := (recoveredThread{rec}).restore(ctx, tx, env); err != nil {
		return err
	}
	if err := env.signedBy(ctx, tx, c.UserID, c.UserSignature, buildThreadRemovalUserPayload(env.serverID, c.ThreadID, rec.UserSignature.Armor)); err != nil {
		return err
	}
	if err := env.countersignedHere(ctx, c.ServerSignature, func(fp string) []byte {
		return buildThreadRemovalServerPayload(env.serverID, c.ThreadID, c.UserSignature.ID, fp, c.UserSignature.Armor, c.ServerSignature.SignedAt)
	}); err != nil {
		return err
	}
	if held, err := existsTx(ctx, tx, `SELECT 1 FROM thread_removals WHERE thread_id = $1`, c.ThreadID); err != nil || held {
		return err
	}
	record, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	userSigID, serverSigID, err := insertSignaturesTx(ctx, tx, c.UserSignature, c.ServerSignature)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO thread_removals (thread_id, public_key_id, user_signature_id, server_signature_id, thread_record)
		VALUES ($1, $2, $3, $4, $5)
	`, c.ThreadID, c.UserSignature.ID, userSigID, serverSigID, record); err != nil {
		return err
	}
	for _, reedID := range rec.ReedIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reed_removals (reed_id, thread_id) VALUES ($1, $2) ON CONFLICT (reed_id) DO NOTHING
		`, reedID, c.ThreadID); err != nil {
			return err
		}
		if err := dropRemovedReedLinksTx(ctx, tx, reedID); err != nil {
			return err
		}
	}
	return nil
}

type recoveredLedgerEntry struct{ *pb.LedgerEntry }

func (r recoveredLedgerEntry) restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	id, sig := r.GetId(), serverSignatureFromPB(r.GetServerSignature())
	var prevSignature string
	if r.PrevId != nil {
		err := tx.QueryRowContext(ctx, `
			SELECT ss.signature FROM ledger_entries e JOIN server_signatures ss ON ss.id = e.server_signature_id
			WHERE e.id = $1
		`, r.GetPrevId()).Scan(&prevSignature)
		if err == sql.ErrNoRows {
			return recoveryWaiting{on: ledgerWaitKey(r.GetPrevId())}
		}
		if err != nil {
			return err
		}
	}
	targets := slices.Sorted(slices.Values(r.GetTargets()))
	requestedAt := time.Time{}
	if r.GetRequestedAt() != 0 {
		requestedAt = timeFromUnix(r.GetRequestedAt())
	}
	if err := env.countersignedHere(ctx, sig, func(fp string) []byte {
		return buildLedgerEntryPayload(id, r.GetPrevId(), prevSignature, r.GetKind(), r.GetUserId(), r.GetRequesterId(),
			requestedAt, r.GetMessage(), r.GetContent(), targets, env.serverID, fp, sig.SignedAt)
	}); err != nil {
		return err
	}
	if held, err := existsTx(ctx, tx, `SELECT 1 FROM ledger_entries WHERE id = $1`, id); err != nil || held {
		return err
	}
	for _, userID := range append([]string{r.GetUserId(), r.GetRequesterId()}, targets...) {
		if userID == "" {
			continue
		}
		if err := env.local(userID); err != nil {
			return err
		}
		if err := mintIdentityTx(ctx, tx, userID); err != nil {
			return err
		}
	}
	serverSigID, err := insertServerSignature(ctx, tx, sig.ID, sig.Armor, sig.SignedAt)
	if err != nil {
		return err
	}
	var prevID, requested any
	if r.PrevId != nil {
		prevID = r.GetPrevId()
	}
	if !requestedAt.IsZero() {
		requested = requestedAt.UTC()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, prev_id, kind, user_id, requester_id, requested_at, message, content, signed_at, server_signature_id)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7, NULLIF($8, ''), $9, $10)
	`, id, prevID, r.GetKind(), r.GetUserId(), r.GetRequesterId(), requested, r.GetMessage(), r.GetContent(),
		sig.SignedAt.UTC().Truncate(time.Second), serverSigID); err != nil {
		return err
	}
	for _, target := range targets {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ledger_entry_targets (entry_id, user_id) VALUES ($1, $2)
		`, id, target); err != nil {
			return err
		}
	}
	// The next live entry takes the id after the highest restored one.
	_, err = tx.ExecContext(ctx, `
		SELECT setval(pg_get_serial_sequence('ledger_entries', 'id'), (SELECT MAX(id) FROM ledger_entries))
	`)
	return err
}

type recoveredAdminRequest struct{ *adminRequest }

func (r recoveredAdminRequest) restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	req := r.adminRequest
	if err := r.verify(ctx, tx, env); err != nil {
		return err
	}
	if held, err := existsTx(ctx, tx, `SELECT 1 FROM admin_requests WHERE id = $1`, req.ID); err != nil || held {
		return err
	}
	for _, target := range req.Targets {
		if err := mintIdentityTx(ctx, tx, target); err != nil {
			return err
		}
	}
	state, decision, closed := req.State, req.Decision, *req.ServerSignature
	req.State, req.Decision = adminStatePending, nil
	if err := insertAdminRequestTx(ctx, tx, req); err != nil {
		return err
	}
	if err := closeAdminRequestTx(ctx, tx, req, state, decision, closed); err != nil {
		return err
	}
	if len(req.Dropped) > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_request_targets SET dropped = TRUE WHERE request_id = $1 AND user_id = ANY($2)
		`, req.ID, req.Dropped); err != nil {
			return err
		}
	}
	for _, target := range req.Targets {
		if err := applyRestoredUserStateTx(ctx, tx, target); err != nil {
			return err
		}
	}
	return nil
}

// verify checks a closed request the way a client does: both admins'
// signatures, and this server's countersignatures on opening and closing.
func (r recoveredAdminRequest) verify(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	req := r.adminRequest
	if err := env.local(req.RequesterID); err != nil {
		return err
	}
	for _, target := range req.Targets {
		if err := env.local(target); err != nil {
			return err
		}
	}
	if !slices.IsSorted(req.Targets) {
		return refuseRecord("request %s's targets aren't sorted", req.ID)
	}
	if req.ServerSignature == nil || req.State == adminStatePending || (req.State == adminStateMoot) != (req.Decision == nil) {
		return refuseRecord("request %s isn't closed", req.ID)
	}
	if req.Decision != nil && req.Decision.Decision != decisionForState(req.State) {
		return refuseRecord("request %s's decision doesn't match its state", req.ID)
	}
	if err := env.signedBy(ctx, tx, req.RequesterID, req.RequesterSignature, req.payload()); err != nil {
		return err
	}
	decisionArmor := ""
	if d := req.Decision; d != nil {
		if d.Decision == adminDecisionApprove && d.DeciderID == req.RequesterID && !isRootIdentity(req.RequesterID, env.serverID) {
			return refuseRecord("request %s was approved by its requester", req.ID)
		}
		if err := env.local(d.DeciderID); err != nil {
			return err
		}
		payload := buildAdminDecisionPayload(req.ID, d.Decision, d.DeciderID, d.Signature.ID, req.RequesterSignature.Armor)
		if err := env.signedBy(ctx, tx, d.DeciderID, d.Signature, payload); err != nil {
			return err
		}
		decisionArmor = d.Signature.Armor
	}
	opened := req.RequesterServerSignature
	if err := env.countersignedHere(ctx, opened, func(fp string) []byte {
		return buildAdminRequestOpenedPayload(req.ID, req.RequesterSignature.Armor, env.serverID, fp, opened.SignedAt)
	}); err != nil {
		return err
	}
	closed := *req.ServerSignature
	return env.countersignedHere(ctx, closed, func(fp string) []byte {
		return buildAdminRequestServerPayload(req.ID, req.State, req.RequesterSignature.Armor, decisionArmor, env.serverID, fp, closed.SignedAt)
	})
}

type recoveredFlag struct{ reedFlag }

func (r recoveredFlag) restore(ctx context.Context, tx *sql.Tx, env *recoveryRecordEnv) error {
	if err := env.local(r.ReporterID); err != nil {
		return err
	}
	author, err := env.localEntity(r.ReedID)
	if err != nil {
		return err
	}
	if author == r.ReporterID || isRootIdentity(author, env.serverID) || countCharacters(r.Note) > maxFlagNoteChars {
		return refuseRecord("flag on %s isn't one this server takes", r.ReedID)
	}
	payload := buildReedFlagPayload(r.ReedID, r.ReporterID, r.Signature.ID, r.Note, r.CreatedAt)
	if err := env.signedBy(ctx, tx, r.ReporterID, r.Signature, payload); err != nil {
		return err
	}
	if held, err := existsTx(ctx, tx, `SELECT 1 FROM reed_flags WHERE reed_id = $1 AND reporter_id = $2`, r.ReedID, r.ReporterID); err != nil || held {
		return err
	}
	if err := insertReedIdentityOf(ctx, tx, r.ReedID, env.serverID, author); err != nil {
		return err
	}
	sigID, err := insertUserSignature(ctx, tx, r.Signature.ID, r.Signature.Armor)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO reed_flags (reed_id, author_id, reporter_id, note, public_key_id, user_signature_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, r.ReedID, author, r.ReporterID, r.Note, r.Signature.ID, sigID, r.CreatedAt.UTC())
	return err
}

// applyRestoredUserStateTx settles what restored records say about userID
// once both are back: a removed account's cleared profile, the latest
// suspension or reinstatement, and keys an admin rebind bound.
func applyRestoredUserStateTx(ctx context.Context, tx *sql.Tx, userID string) error {
	// Nobody can claim a removed account.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM unclaimed_accounts WHERE user_id = $1
			AND EXISTS (SELECT 1 FROM account_removals WHERE user_id = $1)
	`, userID); err != nil {
		return err
	}
	var profileUserSigID, profileServerSigID sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT u.user_signature_id, u.server_signature_id FROM users u
		WHERE u.id = $1 AND EXISTS(SELECT 1 FROM account_removals ar WHERE ar.user_id = u.id)
			AND u.server_signature_id IS NOT NULL
	`, userID).Scan(&profileUserSigID, &profileServerSigID)
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		return err
	default:
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET username = NULL, user_signature_id = NULL, server_signature_id = NULL WHERE id = $1
		`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_signatures WHERE id = $1`, profileUserSigID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM server_signatures WHERE id = $1`, profileServerSigID); err != nil {
			return err
		}
		if err := bumpActiveUsers(ctx, tx, -1); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET suspended_by = (
			SELECT CASE WHEN ar.action = 'suspend' THEN ar.id END
			FROM admin_requests ar JOIN admin_request_targets t ON t.request_id = ar.id
			WHERE t.user_id = $1 AND ar.state = 'executed' AND ar.action IN ('suspend', 'reinstate')
			ORDER BY ar.closed_at DESC, ar.id DESC
			LIMIT 1
		) WHERE id = $1
	`, userID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE public_keys pk SET restricted = TRUE
		FROM admin_requests ar
		JOIN admin_request_targets t ON t.request_id = ar.id
		JOIN admin_request_content c ON c.request_id = ar.id
		WHERE t.user_id = $1 AND pk.owner = $1 AND ar.action = 'rebind' AND ar.state = 'executed'
			AND pk.armor = c.body
	`, userID)
	return err
}
