//go:build !ops && !ripplescleanup

package main

import (
	"testing"

	pb "syrinx/proto"
)

// TestShouldBroadcastReed checks PUBLISH_READY's broadcast resolution —
// has_broadcast unset (or broadcast:true) means include the broadcast
// stream; only an explicit has_broadcast+broadcast:false opts out.
func TestShouldBroadcastReed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		hasBroadcast bool
		broadcast    bool
		want         bool
	}{
		{name: "unset", hasBroadcast: false, broadcast: false, want: true},
		{name: "explicit true", hasBroadcast: true, broadcast: true, want: true},
		{name: "explicit false", hasBroadcast: true, broadcast: false, want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pr := &pb.PublishReadyMessage{ReedId: "r1", HasBroadcast: tc.hasBroadcast, Broadcast: tc.broadcast}
			if got := shouldBroadcastReed(pr); got != tc.want {
				t.Fatalf("shouldBroadcastReed() = %v, want %v", got, tc.want)
			}
		})
	}
}
