package recovery

import "testing"

func TestPasswordStrengthWarning(t *testing.T) {
	cases := []struct {
		pw      string
		wantMsg bool
	}{
		{"", false},
		{"short", true},
		{"alllowercaseonly1", true},
		{"AllLowercaseOnly!", true}, // missing digit
		{"abcdefghijklmnop", true},
		{"ABCDEFGHIJKLMNOP", true},
		{"Abcdefghijklmno1", true}, // missing symbol
		{"Abcdefghijklmno!", true}, // missing digit
		{"Abcdefghijklmn1!", false},
		{"Qzf5btn5ayp@chu5", false},
	}
	for _, tc := range cases {
		msg := PasswordStrengthWarning(tc.pw)
		if tc.wantMsg && msg == "" {
			t.Fatalf("%q: expected warning, got none", tc.pw)
		}
		if !tc.wantMsg && msg != "" {
			t.Fatalf("%q: unexpected warning %q", tc.pw, msg)
		}
	}
}
