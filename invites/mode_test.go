package invites

import "testing"

func TestParseSignupMode(t *testing.T) {
	cases := []struct {
		in      string
		want    SignupMode
		wantErr bool
	}{
		{"", ModeOpen, false},
		{"   ", ModeOpen, false},
		{"open", ModeOpen, false},
		{"invite", ModeInvite, false},
		{"closed", ModeClosed, false},
		{"Invite", "", true},
		{"OPEN", "", true},
		{"foo", "", true},
	}
	for _, tc := range cases {
		got, err := ParseSignupMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseSignupMode(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSignupMode(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSignupMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseMaxInvitesPerUser(t *testing.T) {
	cases := []struct {
		in      string
		want    MaxInvitesPerUser
		wantErr bool
	}{
		{"", MaxInvitesUnlimited, false},
		{"  ", MaxInvitesUnlimited, false},
		{"-1", MaxInvitesUnlimited, false},
		{"1", 1, false},
		{"3", 3, false},
		{"10", 10, false},
		{"0", 0, true},
		{"-2", 0, true},
		{"abc", 0, true},
		{"1.5", 0, true},
	}
	for _, tc := range cases {
		got, err := ParseMaxInvitesPerUser(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMaxInvitesPerUser(%q) = %d, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMaxInvitesPerUser(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMaxInvitesPerUser(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
