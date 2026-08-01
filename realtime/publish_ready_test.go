package realtime

import "testing"

func TestPublishReadyIncludeBroadcast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data map[string]interface{}
		want bool
	}{
		{name: "nil data", data: nil, want: true},
		{name: "missing key", data: map[string]interface{}{"reed_id": "r1"}, want: true},
		{name: "explicit true", data: map[string]interface{}{"broadcast": true}, want: true},
		{name: "explicit false", data: map[string]interface{}{"broadcast": false}, want: false},
		{name: "null", data: map[string]interface{}{"broadcast": nil}, want: true},
		{name: "wrong type", data: map[string]interface{}{"broadcast": "false"}, want: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldBroadcast(tc.data); got != tc.want {
				t.Fatalf("shouldBroadcast() = %v, want %v", got, tc.want)
			}
		})
	}
}
