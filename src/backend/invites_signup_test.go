//go:build !ops

package main

import "testing"

func TestResolveSignup(t *testing.T) {
	pending := &inviteRecord{ID: "inv1", CreatedBy: "alice"}
	claimed := &inviteRecord{ID: "inv2", CreatedBy: "alice"}
	ts := claimed.CreatedAt
	claimed.ClaimedAt = &ts

	cases := []struct {
		name    string
		mode    inviteSignupMode
		id      string
		secret  string
		inv     *inviteRecord
		wantID  string
		wantErr error
	}{
		{"open no creds", signupModeOpen, "", "", nil, "", nil},
		{"open pending", signupModeOpen, "inv1", "sec", pending, "inv1", nil},
		{"open bad", signupModeOpen, "inv1", "sec", nil, "", errInvalidInvite},
		{"open id mismatch", signupModeOpen, "other", "sec", pending, "", errInvalidInvite},
		{"open incomplete", signupModeOpen, "inv1", "", nil, "", errInvalidInvite},
		{"invite empty needs", signupModeInvite, "", "", nil, "", errInviteRequired},
		{"invite ok", signupModeInvite, "inv1", "sec", pending, "inv1", nil},
		{"invite claimed", signupModeInvite, "inv2", "sec", claimed, "", errInvalidInvite},
		{"invite incomplete", signupModeInvite, "inv1", "", nil, "", errInvalidInvite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSignup(tc.mode, tc.id, tc.secret, tc.inv)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.InviteID != tc.wantID {
				t.Fatalf("got %+v, want id=%q", got, tc.wantID)
			}
		})
	}
}
