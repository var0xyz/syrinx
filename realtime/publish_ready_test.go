package realtime

import (
	"encoding/json"
	"testing"
)

func TestPublishReadyIncludeBroadcast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "empty", raw: `{}`, want: true},
		{name: "missing key", raw: `{"reed_id":"r1"}`, want: true},
		{name: "explicit true", raw: `{"broadcast":true}`, want: true},
		{name: "explicit false", raw: `{"broadcast":false}`, want: false},
		{name: "null", raw: `{"broadcast":null}`, want: true},
		{name: "wrong type", raw: `{"broadcast":"false"}`, want: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var data PublishReadyData
			if err := json.Unmarshal([]byte(tc.raw), &data); err != nil {
				t.Fatal(err)
			}
			if got := shouldBroadcast(data); got != tc.want {
				t.Fatalf("shouldBroadcast() = %v, want %v", got, tc.want)
			}
		})
	}
}
