package recovery

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"syrinx/identity"
	"syrinx/roles"
)

// Verifier checks detached PGP signatures (armored). Implemented by crypto.Service.
type Verifier interface {
	VerifySignature(message, signature, publicKey string) error
	VerifySignedChallenge(signature, publicKey, challenge string) error
}

// ServerKeyLookup returns the armored public half of a historical server
// signing key, or "" if unknown.
type ServerKeyLookup func(ctx context.Context, fingerprint string) (armor string, err error)

// FlattenKeysNest walks the nest outermost→oldest, verifying each key
// (and its predecessor link / optional revocation) as soon as it is
// visited, then returns the active (outermost) key and the full chain
// oldest→newest for FK-safe insert. serverID must match
// profile.serverSignature.serverID. Any failure aborts with no partial write.
func FlattenKeysNest(
	ctx context.Context,
	profile Profile,
	root KeyNode,
	serverID string,
	lookup ServerKeyLookup,
	v Verifier,
) (active FlatKey, flat []FlatKey, err error) {
	if profile.ID == "" || profile.Username == "" {
		return FlatKey{}, nil, fmt.Errorf("profile id and username are required")
	}
	if err := roles.ValidateProfileRole(profile.ID, profile.Role); err != nil {
		return FlatKey{}, nil, err
	}
	if profile.ServerSignature.ServerID != serverID {
		return FlatKey{}, nil, fmt.Errorf("profile server id mismatch")
	}
	if profile.UserSignature.Fingerprint == "" || profile.UserSignature.Armor == "" {
		return FlatKey{}, nil, fmt.Errorf("profile signature is required")
	}

	byFP := make(map[string]KeyWire)
	var newestFirst []FlatKey
	var parent *KeyNode

	for n := &root; n != nil; n = n.Predecessor {
		if n.Fingerprint == "" || n.Armor == "" {
			return FlatKey{}, nil, fmt.Errorf("incomplete nest: key missing fingerprint or armor")
		}
		if n.UserID != "" && n.UserID != profile.ID {
			return FlatKey{}, nil, fmt.Errorf("key %s userID does not match profile", n.Fingerprint)
		}
		if _, dup := byFP[n.Fingerprint]; dup {
			return FlatKey{}, nil, fmt.Errorf("duplicate fingerprint in nest: %s", n.Fingerprint)
		}

		if parent != nil {
			// Arrived at older key n, predecessor of parent. n.Signature is
			// n's detached sig over parent.Armor — verify before going deeper.
			if n.Signature == "" {
				return FlatKey{}, nil, fmt.Errorf("broken predecessor link: missing signature for key %s", parent.Fingerprint)
			}
			if err := v.VerifySignedChallenge(n.Signature, n.Armor, parent.Armor); err != nil {
				return FlatKey{}, nil, fmt.Errorf("broken predecessor signature for %s: %w", parent.Fingerprint, err)
			}
			newestFirst[len(newestFirst)-1].PredecessorFingerprint = n.Fingerprint
			newestFirst[len(newestFirst)-1].PredecessorSignature = n.Signature
		}

		if err := verifyKeyCountersig(ctx, n.KeyWire, profile.ID, serverID, lookup, v); err != nil {
			return FlatKey{}, nil, fmt.Errorf("key %s: %w", n.Fingerprint, err)
		}
		if n.Revocation != nil {
			if err := verifyRevocation(ctx, n.Revocation, n.KeyWire, profile.ID, serverID, lookup, v); err != nil {
				return FlatKey{}, nil, fmt.Errorf("revocation for %s: %w", n.Fingerprint, err)
			}
		}

		byFP[n.Fingerprint] = n.KeyWire
		newestFirst = append(newestFirst, FlatKey{Key: n.KeyWire, Revocation: n.Revocation})
		parent = n
	}

	if len(newestFirst) == 0 {
		return FlatKey{}, nil, fmt.Errorf("empty key nest")
	}
	active = newestFirst[0]

	signer, ok := byFP[profile.UserSignature.Fingerprint]
	if !ok {
		return FlatKey{}, nil, fmt.Errorf("profile signatureFingerprint not in nest")
	}

	userPayload := identity.BuildUserIdentityPayload(
		profile.Username,
		profile.UserSignature.Fingerprint,
		profile.Bio,
	)
	userSigArmor, err := decodeB64Armor(profile.UserSignature.Armor)
	if err != nil {
		return FlatKey{}, nil, fmt.Errorf("profile user signature: %w", err)
	}
	if err := v.VerifySignature(string(userPayload), userSigArmor, signer.Armor); err != nil {
		return FlatKey{}, nil, fmt.Errorf("profile user signature: %w", err)
	}

	if err := VerifyProfileServerCountersig(ctx, profile, serverID, lookup, v); err != nil {
		return FlatKey{}, nil, err
	}

	// Oldest → newest for user_keys.predecessor_fingerprint inserts.
	flat = make([]FlatKey, 0, len(newestFirst))
	for i := len(newestFirst) - 1; i >= 0; i-- {
		flat = append(flat, newestFirst[i])
	}
	return active, flat, nil
}

func profileInvitedByID(profile Profile) string {
	if profile.InvitedBy == nil {
		return ""
	}
	return profile.InvitedBy.ID
}

// VerifyProfileServerCountersig checks profile.serverSignature.serverID
// against serverID and verifies the server countersignature. It does not
// verify the user signature or key nest (those are claim/peer concerns).
func VerifyProfileServerCountersig(ctx context.Context, profile Profile, serverID string, lookup ServerKeyLookup, v Verifier) error {
	if profile.ServerSignature.ServerID != serverID {
		return fmt.Errorf("profile server id mismatch")
	}
	if profile.ServerSignature.Fingerprint == "" || profile.ServerSignature.Armor == "" || profile.ServerSignature.Timestamp.IsZero() {
		return fmt.Errorf("missing server countersignature")
	}
	serverPub, err := lookup(ctx, profile.ServerSignature.Fingerprint)
	if err != nil {
		return err
	}
	if serverPub == "" {
		return fmt.Errorf("unknown server key %s for profile", profile.ServerSignature.Fingerprint)
	}
	profilePayload := identity.BuildProfilePayload(
		profile.ID,
		profile.Username,
		profile.UserSignature.Fingerprint,
		serverID,
		profile.ServerSignature.Fingerprint,
		profile.UserSignature.Armor,
		profileInvitedByID(profile),
		profile.Role,
		profile.Bio,
		profile.MemberSince.UTC().Truncate(time.Second),
		profile.ServerSignature.Timestamp.UTC().Truncate(time.Second),
	)
	serverSigArmor, err := decodeB64Armor(profile.ServerSignature.Armor)
	if err != nil {
		return fmt.Errorf("profile server signature: %w", err)
	}
	if err := v.VerifySignature(string(profilePayload), serverSigArmor, serverPub); err != nil {
		return fmt.Errorf("profile server signature: %w", err)
	}
	return nil
}

func verifyKeyCountersig(ctx context.Context, key KeyWire, userID, serverID string, lookup ServerKeyLookup, v Verifier) error {
	if key.ServerSignature.ServerID != "" && key.ServerSignature.ServerID != serverID {
		return fmt.Errorf("server id mismatch")
	}
	if key.ServerSignature.Fingerprint == "" || key.ServerSignature.Armor == "" {
		return fmt.Errorf("missing server countersignature")
	}
	serverPub, err := lookup(ctx, key.ServerSignature.Fingerprint)
	if err != nil {
		return err
	}
	if serverPub == "" {
		return fmt.Errorf("unknown server key %s", key.ServerSignature.Fingerprint)
	}
	payload := identity.BuildPublicKeyPayload(
		serverID,
		userID,
		key.Fingerprint,
		key.ServerSignature.Fingerprint,
		key.Armor,
		key.ServerSignature.Timestamp.UTC().Truncate(time.Second),
	)
	sigArmor, err := decodeB64Armor(key.ServerSignature.Armor)
	if err != nil {
		return err
	}
	return v.VerifySignature(string(payload), sigArmor, serverPub)
}

func verifyRevocation(ctx context.Context, rev *Revocation, key KeyWire, userID, serverID string, lookup ServerKeyLookup, v Verifier) error {
	if rev.Fingerprint != key.Fingerprint {
		return fmt.Errorf("fingerprint mismatch")
	}
	if rev.UserID != "" && rev.UserID != userID {
		return fmt.Errorf("userID mismatch")
	}
	if rev.ServerSignature.ServerID != "" && rev.ServerSignature.ServerID != serverID {
		return fmt.Errorf("server id mismatch")
	}
	userPayload := identity.BuildUserRevocationPayload(userID, rev.Fingerprint, rev.Reason)
	userSigArmor, err := decodeB64Armor(rev.UserSignature.Armor)
	if err != nil {
		return err
	}
	if err := v.VerifySignature(string(userPayload), userSigArmor, key.Armor); err != nil {
		return fmt.Errorf("user signature: %w", err)
	}
	serverPub, err := lookup(ctx, rev.ServerSignature.Fingerprint)
	if err != nil {
		return err
	}
	if serverPub == "" {
		return fmt.Errorf("unknown server key %s", rev.ServerSignature.Fingerprint)
	}
	serverPayload := identity.BuildServerRevocationPayload(
		userID,
		rev.Fingerprint,
		rev.Reason,
		serverID,
		rev.ServerSignature.Fingerprint,
		rev.UserSignature.Armor,
		rev.ServerSignature.Timestamp.UTC().Truncate(time.Second),
	)
	serverSigArmor, err := decodeB64Armor(rev.ServerSignature.Armor)
	if err != nil {
		return err
	}
	return v.VerifySignature(string(serverPayload), serverSigArmor, serverPub)
}

func decodeB64Armor(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("invalid base64 encoding")
	}
	return string(raw), nil
}

// decodeKeyNestArmor walks a key nest outermost→oldest, decoding each
// KeyWire.Armor from base64 to plain armor in place. Must run once, right
// after JSON-decoding the request, before FlattenKeysNest or any crypto use
// — every downstream consumer (signature verification, payload signing)
// expects plain armor, matching how flat signature fields already work.
func decodeKeyNestArmor(root *KeyNode) error {
	for n := root; n != nil; n = n.Predecessor {
		armor, err := decodeB64Armor(n.Armor)
		if err != nil {
			return fmt.Errorf("key %s: invalid armor encoding", n.Fingerprint)
		}
		n.Armor = armor
	}
	return nil
}

// ValidateChallengeAge rejects challenges in the future or older than maxAge.
func ValidateChallengeAge(challenge int64, now time.Time, maxAge time.Duration) error {
	nowUnix := now.UTC().Unix()
	if challenge > nowUnix {
		return fmt.Errorf("challenge is in the future")
	}
	if nowUnix-challenge > int64(maxAge.Seconds()) {
		return fmt.Errorf("challenge is stale")
	}
	return nil
}

// VerifyChallengeSignature checks a base64(armored) detached sig over the
// decimal challenge string using the outermost public key.
func VerifyChallengeSignature(challenge int64, signatureB64, publicKeyArmor string, v Verifier) error {
	sigArmor, err := decodeB64Armor(signatureB64)
	if err != nil {
		return fmt.Errorf("challenge signature: %w", err)
	}
	msg := strconv.FormatInt(challenge, 10)
	if err := v.VerifySignature(msg, sigArmor, publicKeyArmor); err != nil {
		return fmt.Errorf("challenge signature: %w", err)
	}
	return nil
}
