package metrics

import (
	"testing"

	"github.com/gorilla/websocket"
)

func TestTagCountAttr(t *testing.T) {
	cases := map[int]int{0: 0, 1: 1, 3: 3, 4: 4, 99: 4}
	for in, want := range cases {
		if got := TagCountAttr(in); got != want {
			t.Errorf("TagCountAttr(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestJSONWSMessageType(t *testing.T) {
	if got := WSMessageType(websocket.TextMessage, []byte(`{"type":"REQUEST_REED","data":{}}`)); got != "REQUEST_REED" {
		t.Fatalf("got %q", got)
	}
	if got := WSMessageType(websocket.TextMessage, []byte(`not json`)); got != "unknown_json" {
		t.Fatalf("got %q", got)
	}
}

func TestNoopRecorder(t *testing.T) {
	var rec Noop
	rec.UserCreated(nil, "open", "user-test")
	rec.ReedPublished(nil, ReedPublishedAttrs{Kind: ReedKindEcho})
	rec.WSMessage(nil, DirectionIn, "PING")
}
