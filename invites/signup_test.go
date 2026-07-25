package invites

import "testing"

func TestResolveSignup(t *testing.T) {
	pending := &Invite{ID: "inv1", CreatedBy: "alice"}
	claimed := &Invite{ID: "inv2", CreatedBy: "alice"}
	ts := claimed.CreatedAt
	claimed.ClaimedAt = &ts

	cases := []struct {
		name      string
		mode      SignupMode
		n         int
		token     string
		inv       *Invite
		wantID    string
		wantBy    string
		wantErr   error
	}{
		{"open no token", ModeOpen, 5, "", nil, "", "", nil},
		{"open pending", ModeOpen, 5, "tok", pending, "inv1", "alice", nil},
		{"open bad", ModeOpen, 5, "tok", nil, "", "", ErrInvalidInvite},
		{"invite bootstrap", ModeInvite, 0, "", nil, "", "", nil},
		{"invite bootstrap with token", ModeInvite, 0, "tok", pending, "inv1", "alice", nil},
		{"invite bootstrap bad token", ModeInvite, 0, "tok", nil, "", "", ErrInvalidInvite},
		{"invite needs token", ModeInvite, 1, "", nil, "", "", ErrInviteRequired},
		{"invite ok", ModeInvite, 1, "tok", pending, "inv1", "alice", nil},
		{"invite claimed", ModeInvite, 1, "tok", claimed, "", "", ErrInvalidInvite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSignup(tc.mode, tc.n, tc.token, tc.inv)
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
