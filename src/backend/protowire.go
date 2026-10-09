//go:build !ops && !ripplescleanup && !challengescleanup

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"

	pb "syrinx/proto"
)

// protobufContentType is the only body encoding the HTTP API speaks.
const protobufContentType = "application/x-protobuf"

// errNotProtobuf rejects a non-empty request body sent as anything else.
var errNotProtobuf = errors.New("request body must be " + protobufContentType)

// writeResponse writes msg as the protobuf body; a nil msg sends no body.
func writeResponse(w http.ResponseWriter, statusCode int, msg proto.Message) {
	if msg == nil || !msg.ProtoReflect().IsValid() {
		w.WriteHeader(statusCode)
		return
	}
	body, err := proto.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", protobufContentType)
	w.WriteHeader(statusCode)
	_, _ = w.Write(body)
}

// writeError answers with a plain-English Error body.
func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeResponse(w, statusCode, &pb.Error{Message: message})
}

func internalServerError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "Internal Server Error")
}

// readRequest decodes the protobuf body into msg. An empty body leaves msg
// empty, so routes whose fields are all optional accept no body at all.
func readRequest(r *http.Request, msg proto.Message) error {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType != protobufContentType {
		return errNotProtobuf
	}
	return proto.Unmarshal(body, msg)
}

// writeJSON is the peer-to-peer federation encoding until those calls move
// to protobuf too.
func writeJSON(w http.ResponseWriter, statusCode int, message any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(message)
}

// unixOrZero is t as unix seconds, 0 for the zero time.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

// unixPtr is t as unix seconds, 0 when unset.
func unixPtr(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return unixOrZero(*t)
}

// timeFromUnix is the inverse of unixOrZero.
func timeFromUnix(s int64) time.Time {
	if s == 0 {
		return time.Time{}
	}
	return time.Unix(s, 0).UTC()
}

// userSignatureFromPB/serverSignatureFromPB read signature blocks off a
// request; a missing block reads as empty.
func userSignatureFromPB(s *pb.UserSignature) UserSignature {
	return UserSignature{ID: s.GetId(), Armor: s.GetArmor()}
}

func serverSignatureFromPB(s *pb.ServerSignature) ServerSignature {
	return ServerSignature{ID: s.GetId(), Armor: s.GetArmor(), SignedAt: timeFromUnix(s.GetSignedAt())}
}

func pbUser(u *User) *pb.User {
	out := &pb.User{
		Id:              u.ID,
		Username:        u.Username,
		Role:            u.Role,
		Bio:             u.Bio,
		MemberSince:     unixOrZero(u.CreatedAt),
		UserSignature:   pbUserSignature(u.UserSignature),
		ServerSignature: pbServerSignature(u.ServerSignature),
	}
	if u.Invite != nil {
		out.Invite = &pb.UserInvite{Id: u.Invite.ID, UserId: u.Invite.UserID, Username: u.Invite.Username}
	}
	return out
}

func pbUserInfo(i *UserInfo) *pb.UserInfo {
	return &pb.UserInfo{
		Id:               i.ID,
		FirstReedId:      i.FirstReedID,
		FollowersCount:   int32(i.FollowersCount),
		FollowingCount:   int32(i.FollowingCount),
		ActiveKeyId:      i.ActiveKeyID,
		ProfileTimestamp: unixOrZero(i.ProfileTimestamp),
		PinnedReedIds:    i.PinnedReedIDs,
		VouchIds:         i.VouchIDs,
	}
}

func pbKey(k *Key) *pb.PublicKey {
	return &pb.PublicKey{
		Id:              k.ID,
		UserId:          k.UserID,
		Armor:           k.Armor,
		CreatedAt:       unixOrZero(k.CreatedAt),
		Revoked:         k.Revoked,
		Predecessor:     k.Predecessor,
		ServerSignature: pbServerSignature(k.ServerSignature),
		RevokedAt:       unixPtr(k.RevokedAt),
		Compromised:     k.Compromised,
	}
}

// keyFromPB reads a key a peer served.
func keyFromPB(k *pb.PublicKey) Key {
	key := Key{
		ID:              k.GetId(),
		UserID:          k.GetUserId(),
		Armor:           k.GetArmor(),
		CreatedAt:       timeFromUnix(k.GetCreatedAt()),
		Revoked:         k.GetRevoked(),
		Predecessor:     k.Predecessor,
		ServerSignature: serverSignatureFromPB(k.GetServerSignature()),
		Compromised:     k.GetCompromised(),
	}
	if k.GetRevokedAt() != 0 {
		t := timeFromUnix(k.GetRevokedAt())
		key.RevokedAt = &t
	}
	return key
}

func pbKeyRevocation(r *KeyRevocation) *pb.KeyRevocationCert {
	return &pb.KeyRevocationCert{
		Id:                 r.ID,
		UserId:             r.UserID,
		Reason:             r.Reason,
		Successor:          r.Successor,
		SuccessorSignature: r.SuccessorSignature,
		UserSignature:      pbUserSignature(r.UserSignature),
		ServerSignature:    pbServerSignature(r.ServerSignature),
	}
}

func pbServerKeyRevocation(r *serverKeyRevocationWire) *pb.ServerKeyRevocation {
	return &pb.ServerKeyRevocation{
		ServerId:           r.ServerID,
		KeyId:              r.KeyID,
		Successor:          r.Successor,
		Compromised:        r.Compromised,
		Reason:             r.Reason,
		SignedAt:           unixOrZero(r.SignedAt),
		Signature:          r.Signature,
		SuccessorSignature: r.SuccessorSignature,
	}
}

func pbAccountRemoval(a AccountRemoval) *pb.AccountRemovalCert {
	return &pb.AccountRemovalCert{
		ServerId:        a.ServerID,
		UserId:          a.UserID,
		Note:            a.Note,
		UserSignature:   pbUserSignature(a.UserSignature),
		ServerSignature: pbServerSignature(a.ServerSignature),
	}
}

func pbReedRemoval(r ReedRemoval) *pb.ReedRemovalCert {
	return &pb.ReedRemovalCert{
		ServerId:        r.ServerID,
		UserId:          r.UserID,
		ReedId:          r.ReedID,
		UserSignature:   pbUserSignature(r.UserSignature),
		ServerSignature: pbServerSignature(r.ServerSignature),
	}
}

func pbThreadRecord(r threadRecordWire) *pb.ThreadRecord {
	return &pb.ThreadRecord{
		ServerId:        r.ServerID,
		UserId:          r.UserID,
		ThreadId:        r.ThreadID,
		ReedIds:         r.ReedIDs,
		UserSignature:   pbUserSignature(r.UserSignature),
		ServerSignature: pbServerSignature(r.ServerSignature),
	}
}

func pbThreadRemoval(rm *threadRemoval) *pb.ThreadRemoval {
	return &pb.ThreadRemoval{
		Cert: &pb.ThreadRemovalCert{
			ServerId:        rm.Cert.ServerID,
			UserId:          rm.Cert.UserID,
			ThreadId:        rm.Cert.ThreadID,
			UserSignature:   pbUserSignature(rm.Cert.UserSignature),
			ServerSignature: pbServerSignature(rm.Cert.ServerSignature),
		},
		Record: pbThreadRecord(rm.Record),
	}
}

// writeAccountGone, writeReedGone and writeThreadGone answer 410 with the
// removal certificate as the Error's detail.
func writeAccountGone(w http.ResponseWriter, a AccountRemoval) {
	writeResponse(w, http.StatusGone, &pb.Error{
		Message: "Account removed",
		Detail:  &pb.Error_AccountRemoval{AccountRemoval: pbAccountRemoval(a)},
	})
}

func writeReedGone(w http.ResponseWriter, r ReedRemoval) {
	writeResponse(w, http.StatusGone, &pb.Error{
		Message: "Reed removed",
		Detail:  &pb.Error_ReedRemoval{ReedRemoval: pbReedRemoval(r)},
	})
}

func writeThreadGone(w http.ResponseWriter, rm *threadRemoval) {
	writeResponse(w, http.StatusGone, &pb.Error{
		Message: "Thread removed",
		Detail:  &pb.Error_ThreadRemoval{ThreadRemoval: pbThreadRemoval(rm)},
	})
}

func pbLikeCert(c *LikeCert) *pb.LikeCert {
	return &pb.LikeCert{
		ServerId:        c.ServerID,
		AuthorId:        c.AuthorID,
		ReedId:          c.ReedID,
		UserSignature:   pbUserSignature(c.UserSignature),
		ServerSignature: pbServerSignature(c.ServerSignature),
	}
}

// likeCertFromPB reads a like a peer countersigned.
func likeCertFromPB(c *pb.LikeCert) LikeCert {
	return LikeCert{
		ServerID:        c.GetServerId(),
		AuthorID:        c.GetAuthorId(),
		ReedID:          c.GetReedId(),
		UserSignature:   userSignatureFromPB(c.GetUserSignature()),
		ServerSignature: serverSignatureFromPB(c.GetServerSignature()),
	}
}

func pbVouch(c *VouchCert) *pb.Vouch {
	out := &pb.Vouch{
		Id:              c.ID,
		VoucherUserId:   c.VoucherUserID,
		VoucherKeyId:    c.VoucherKeyID,
		SubjectUserId:   c.SubjectUserID,
		SubjectKeyId:    c.SubjectKeyID,
		Note:            c.Note,
		UserSignature:   pbUserSignature(c.UserSignature),
		ServerSignature: pbServerSignature(c.ServerSignature),
	}
	if c.Withdrawal != nil {
		out.Withdrawal = &pb.VouchWithdrawal{
			UserSignature:   pbUserSignature(c.Withdrawal.UserSignature),
			ServerSignature: pbServerSignature(c.Withdrawal.ServerSignature),
		}
	}
	return out
}

func pbVouchList(l *VouchListResponse) *pb.VouchListResponse {
	out := &pb.VouchListResponse{NextCursor: l.NextCursor}
	for i := range l.Vouches {
		out.Vouches = append(out.Vouches, pbVouch(&l.Vouches[i]))
	}
	return out
}

func pbFollowList(l *FollowListResponse) *pb.FollowListResponse {
	out := &pb.FollowListResponse{HasMore: l.HasMore}
	for _, u := range l.Users {
		out.Users = append(out.Users, &pb.FollowListUser{UserId: u.UserID, FollowedAt: unixOrZero(u.FollowedAt)})
	}
	return out
}

func pbEchoerList(l *EchoerListResponse) *pb.EchoerListResponse {
	out := &pb.EchoerListResponse{HasMore: l.HasMore}
	for _, u := range l.Users {
		out.Users = append(out.Users, &pb.EchoerListUser{UserId: u.UserID, EchoedAt: unixOrZero(u.EchoedAt)})
	}
	return out
}

func pbReplyList(l *ReplyListResponse) *pb.ReplyListResponse {
	out := &pb.ReplyListResponse{HasMore: l.HasMore}
	for _, r := range l.Replies {
		out.Replies = append(out.Replies, &pb.ReplyListItem{UserId: r.UserID, ReedId: r.ReedID, Timestamp: unixOrZero(r.Timestamp)})
	}
	return out
}

func pbUserSearch(results []UserSearchResult) *pb.UserSearchResponse {
	out := &pb.UserSearchResponse{}
	for _, u := range results {
		out.Users = append(out.Users, &pb.UserSearchResult{Id: u.ID, Username: u.Username, ServerName: u.ServerName})
	}
	return out
}

func pbServerSignatures(sigs threadSignatures) *pb.ThreadSignatures {
	out := &pb.ThreadSignatures{ServerSignature: pbServerSignature(sigs.ServerSignature)}
	for _, s := range sigs.Reeds {
		out.Reeds = append(out.Reeds, pbServerSignature(s))
	}
	return out
}

// peerErrorMessage reads the message out of a peer's error body.
func peerErrorMessage(body []byte) string {
	var message string
	if err := json.Unmarshal(body, &message); err == nil {
		return message
	}
	return string(body)
}

// recoveryServerSignatureFromPB splits the countersigning key id the way
// recoveryServerSignature's JSON form does.
func recoveryServerSignatureFromPB(s *pb.ServerSignature) (recoveryServerSignature, error) {
	out := recoveryServerSignature{Armor: s.GetArmor(), Timestamp: timeFromUnix(s.GetSignedAt())}
	if s.GetId() == "" {
		return out, nil
	}
	fingerprint, serverID, ok := parseIdentityID(identityID(s.GetId()))
	if !ok {
		return out, fmt.Errorf("serverSignature.id is not a canonical key id: %q", s.GetId())
	}
	out.Fingerprint = fingerprint
	out.ServerID = serverID
	return out, nil
}

func pbRecoveryServerSignature(s recoveryServerSignature) *pb.ServerSignature {
	return &pb.ServerSignature{
		Id:       string(canonicalID(s.ServerID, s.Fingerprint)),
		Armor:    s.Armor,
		SignedAt: unixOrZero(s.Timestamp),
	}
}

func recoveryProfileFromPB(p *pb.RecoveryProfile) (recoveryProfile, error) {
	sig, err := recoveryServerSignatureFromPB(p.GetServerSignature())
	if err != nil {
		return recoveryProfile{}, err
	}
	out := recoveryProfile{
		ID:                   p.GetId(),
		Username:             p.GetUsername(),
		Role:                 p.GetRole(),
		MemberSince:          timeFromUnix(p.GetMemberSince()),
		Bio:                  p.GetBio(),
		ActiveKeyFingerprint: p.GetActiveKeyFingerprint(),
		UserSignature:        recoveryUserSignature{KeyID: p.GetUserSignature().GetId(), Armor: p.GetUserSignature().GetArmor()},
		ServerSignature:      sig,
		HasReeds:             p.GetHasReeds(),
	}
	if inv := p.GetInvite(); inv != nil {
		out.Invite = &Invite{ID: inv.GetId(), UserID: inv.GetUserId(), Username: inv.GetUsername()}
	}
	return out, nil
}

func pbRecoveryProfile(p recoveryProfile) *pb.RecoveryProfile {
	out := &pb.RecoveryProfile{
		Id:                   p.ID,
		Username:             p.Username,
		Role:                 p.Role,
		MemberSince:          unixOrZero(p.MemberSince),
		Bio:                  p.Bio,
		ActiveKeyFingerprint: p.ActiveKeyFingerprint,
		UserSignature:        &pb.UserSignature{Id: p.UserSignature.KeyID, Armor: p.UserSignature.Armor},
		ServerSignature:      pbRecoveryServerSignature(p.ServerSignature),
		HasReeds:             p.HasReeds,
	}
	if p.Invite != nil {
		out.Invite = &pb.UserInvite{Id: p.Invite.ID, UserId: p.Invite.UserID, Username: p.Invite.Username}
	}
	return out
}

// recoveryKeyNodeFromPB reads a nested key chain, outermost key first.
func recoveryKeyNodeFromPB(n *pb.RecoveryKeyNode) (recoveryKeyNode, error) {
	sig, err := recoveryServerSignatureFromPB(n.GetServerSignature())
	if err != nil {
		return recoveryKeyNode{}, err
	}
	out := recoveryKeyNode{
		recoveryKeyWire: recoveryKeyWire{
			Fingerprint:     n.GetFingerprint(),
			UserID:          n.GetUserId(),
			Armor:           n.GetArmor(),
			CreatedAt:       timeFromUnix(n.GetCreatedAt()),
			Revoked:         n.GetRevoked(),
			ServerSignature: sig,
		},
		Signature: n.GetSignature(),
	}
	if n.GetExpiresAt() != 0 {
		t := timeFromUnix(n.GetExpiresAt())
		out.ExpiresAt = &t
	}
	if rev := n.GetRevocation(); rev != nil {
		revSig, err := recoveryServerSignatureFromPB(rev.GetServerSignature())
		if err != nil {
			return recoveryKeyNode{}, err
		}
		out.Revocation = &recoveryRevocation{
			Fingerprint:     rev.GetFingerprint(),
			UserID:          rev.GetUserId(),
			Reason:          rev.GetReason(),
			Successor:       rev.Successor,
			UserSignature:   recoveryUserSignature{KeyID: rev.GetUserSignature().GetId(), Armor: rev.GetUserSignature().GetArmor()},
			ServerSignature: revSig,
		}
	}
	if pred := n.GetPredecessor(); pred != nil {
		p, err := recoveryKeyNodeFromPB(pred)
		if err != nil {
			return recoveryKeyNode{}, err
		}
		out.Predecessor = &p
	}
	return out, nil
}

// readRecoveryProfile decodes a bare RecoveryProfile body (POST /users/status).
func readRecoveryProfile(r *http.Request) (recoveryProfile, error) {
	var msg pb.RecoveryProfile
	if err := readRequest(r, &msg); err != nil {
		return recoveryProfile{}, err
	}
	return recoveryProfileFromPB(&msg)
}

func readRecoveryClaim(r *http.Request) (recoveryClaimRequest, error) {
	var msg pb.ClaimIdentityRequest
	if err := readRequest(r, &msg); err != nil {
		return recoveryClaimRequest{}, err
	}
	profile, err := recoveryProfileFromPB(msg.GetProfile())
	if err != nil {
		return recoveryClaimRequest{}, err
	}
	key, err := recoveryKeyNodeFromPB(msg.GetKey())
	if err != nil {
		return recoveryClaimRequest{}, err
	}
	return recoveryClaimRequest{Challenge: msg.GetChallenge(), Signature: msg.GetSignature(), Profile: profile, Key: key}, nil
}

func readRecoveryPeerIdentity(r *http.Request) (recoveryPeerIdentityRequest, error) {
	var msg pb.PeerIdentityRequest
	if err := readRequest(r, &msg); err != nil {
		return recoveryPeerIdentityRequest{}, err
	}
	profile, err := recoveryProfileFromPB(msg.GetProfile())
	if err != nil {
		return recoveryPeerIdentityRequest{}, err
	}
	key, err := recoveryKeyNodeFromPB(msg.GetKey())
	if err != nil {
		return recoveryPeerIdentityRequest{}, err
	}
	return recoveryPeerIdentityRequest{Profile: profile, Key: key}, nil
}

func readRecoveryReed(r *http.Request) (recoveryReedRequest, error) {
	var msg pb.RecoveryReedRequest
	if err := readRequest(r, &msg); err != nil {
		return recoveryReedRequest{}, err
	}
	sig, err := recoveryServerSignatureFromPB(msg.GetServerSignature())
	if err != nil {
		return recoveryReedRequest{}, err
	}
	return recoveryReedRequest{
		ReedID:          msg.GetReedId(),
		AuthorID:        msg.GetAuthorId(),
		UserSignature:   recoveryUserSignature{KeyID: msg.GetUserSignature().GetId(), Armor: msg.GetUserSignature().GetArmor()},
		ServerSignature: sig,
	}, nil
}

// decodePeerKey reads a GET /api/keys/{id} body a peer answered.
func decodePeerKey(body []byte) (Key, error) {
	var msg pb.PublicKey
	if err := proto.Unmarshal(body, &msg); err != nil {
		return Key{}, err
	}
	return keyFromPB(&msg), nil
}

// decodePeerKeyRevocation reads a GET /api/keys/{id}/revocation body a peer
// answered: exactly one of user and server is set.
func decodePeerKeyRevocation(body []byte) (user *KeyRevocation, server *serverKeyRevocationWire, err error) {
	var msg pb.KeyRevocationResponse
	if err := proto.Unmarshal(body, &msg); err != nil {
		return nil, nil, err
	}
	if u := msg.GetUser(); u != nil {
		return &KeyRevocation{
			ID:                 u.GetId(),
			UserID:             u.GetUserId(),
			Reason:             u.GetReason(),
			Successor:          u.Successor,
			SuccessorSignature: u.SuccessorSignature,
			UserSignature:      userSignatureFromPB(u.GetUserSignature()),
			ServerSignature:    serverSignatureFromPB(u.GetServerSignature()),
		}, nil, nil
	}
	if s := msg.GetServer(); s != nil {
		return nil, &serverKeyRevocationWire{
			Type:     identityTypeServerKeyRevocation,
			ServerID: s.GetServerId(),
			serverKeyRevocation: serverKeyRevocation{
				KeyID:              s.GetKeyId(),
				Successor:          s.GetSuccessor(),
				Compromised:        s.GetCompromised(),
				Reason:             s.GetReason(),
				SignedAt:           timeFromUnix(s.GetSignedAt()),
				Signature:          s.GetSignature(),
				SuccessorSignature: s.GetSuccessorSignature(),
			},
		}, nil
	}
	return nil, nil, errors.New("empty key revocation")
}

// blockCertFromPB reads a block a peer refused a request with.
func blockCertFromPB(c *pb.BlockCert) BlockCert {
	return BlockCert{
		Type:            identityTypeBlock,
		UserID:          c.GetUserId(),
		BlockedUserID:   c.GetBlockedUserId(),
		UserSignature:   userSignatureFromPB(c.GetUserSignature()),
		ServerSignature: serverSignatureFromPB(c.GetServerSignature()),
	}
}
