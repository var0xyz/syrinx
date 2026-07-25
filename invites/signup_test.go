package invites

import "testing"

func TestResolveSignup(t *testing.T) {
	pending := &Invite{ID: "inv1", CreatedBy: "alice"}
	claimed := &Invite{ID: "inv2", CreatedBy: "alice"}
	ts := claimed.CreatedAt
	claimed.ClaimedAt = &ts

	cases := []struct {
		name    string
		mode    SignupMode
		n       int
		id      string
		secret  string
		inv     *Invite
		wantID  string
		wantBy  string
		wantErr error
	}{
		{"open no creds", ModeOpen, 5, "", "", nil, "", "", nil},
		{"open pending", ModeOpen, 5, "inv1", "sec", pending, "inv1", "alice", nil},
		{"open bad", ModeOpen, 5, "inv1", "sec", nil, "", "", ErrInvalidInvite},
		{"open id mismatch", ModeOpen, 5, "other", "sec", pending, "", "", ErrInvalidInvite},
		{"open incomplete", ModeOpen, 5, "inv1", "", nil, "", "", ErrInvalidInvite},
		{"invite bootstrap", ModeInvite, 0, "", "", nil, "", "", nil},
		{"invite bootstrap with", ModeInvite, 0, "inv1", "sec", pending, "inv1", "alice", nil},
		{"invite bootstrap bad", ModeInvite, 0, "inv1", "sec", nil, "", "", ErrInvalidInvite},
		{"invite needs", ModeInvite, 1, "", "", nil, "", "", ErrInviteRequired},
		{"invite ok", ModeInvite, 1, "inv1", "sec", pending, "inv1", "alice", nil},
		{"invite claimed", ModeInvite, 1, "inv2", "sec", claimed, "", "", ErrInvalidInvite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSignup(tc.mode, tc.n, tc.id, tc.secret, tc.inv)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.InviteID != tc.wantID || got.InviterID != tc.wantBy {
				t.Fatalf("got %+v, want id=%q by=%q", got, tc.wantID, tc.wantBy)
			}
		})
	}
}
