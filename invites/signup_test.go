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
		id      string
		secret  string
		inv     *Invite
		wantID  string
		wantBy  string
		wantErr error
	}{
		{"open no creds", ModeOpen, "", "", nil, "", "", nil},
		{"open pending", ModeOpen, "inv1", "sec", pending, "inv1", "alice", nil},
		{"open bad", ModeOpen, "inv1", "sec", nil, "", "", ErrInvalidInvite},
		{"open id mismatch", ModeOpen, "other", "sec", pending, "", "", ErrInvalidInvite},
		{"open incomplete", ModeOpen, "inv1", "", nil, "", "", ErrInvalidInvite},
		{"invite empty needs", ModeInvite, "", "", nil, "", "", ErrInviteRequired},
		{"invite ok", ModeInvite, "inv1", "sec", pending, "inv1", "alice", nil},
		{"invite claimed", ModeInvite, "inv2", "sec", claimed, "", "", ErrInvalidInvite},
		{"invite incomplete", ModeInvite, "inv1", "", nil, "", "", ErrInvalidInvite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSignup(tc.mode, tc.id, tc.secret, tc.inv)
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
