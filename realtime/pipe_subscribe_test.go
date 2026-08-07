package realtime

import "testing"

func TestPipeSubscriptionBookkeeping(t *testing.T) {
	cm := NewConnectionManager()
	client := NewClient(nil, "viewer")

	cm.SubscribePipe(client, "#Climate")
	if _, ok := client.pipeSubscriptions["climate"]; !ok {
		t.Fatal("expected normalized client pipe subscription")
	}
	if len(cm.pipeSubscribers["climate"]) != 1 {
		t.Fatal("expected one pipe subscriber")
	}

	if got := cm.FilterTagsWithListeners([]string{"Climate", "other"}); len(got) != 1 || got[0] != "climate" {
		t.Fatalf("FilterTagsWithListeners = %v, want [climate]", got)
	}

	ids := cm.PipeListenerUserIDs([]string{"climate"}, "author")
	if len(ids) != 1 || ids[0] != "viewer" {
		t.Fatalf("PipeListenerUserIDs = %v, want [viewer]", ids)
	}
	if got := cm.PipeListenerUserIDs([]string{"climate"}, "viewer"); len(got) != 0 {
		t.Fatalf("exclude self: got %v", got)
	}

	cm.UnsubscribePipe(client, "climate")
	if _, ok := client.pipeSubscriptions["climate"]; ok {
		t.Fatal("expected client pipe subscription cleared")
	}
	if _, ok := cm.pipeSubscribers["climate"]; ok {
		t.Fatal("expected pipe subscriber map entry removed")
	}
}

func TestClearPipeSubscriptionsOnUnregister(t *testing.T) {
	cm := NewConnectionManager()
	// Use a fake conn pointer via nil — unregister needs conn in map.
	// Subscribe then clearPipeSubscriptions directly (unregister closes conn).
	client := NewClient(nil, "viewer")
	cm.SubscribePipe(client, "tag1")
	cm.SubscribePipe(client, "tag2")
	cm.clearPipeSubscriptions(client)
	if len(client.pipeSubscriptions) != 0 {
		t.Fatal("expected pipe subscriptions cleared")
	}
	if len(cm.pipeSubscribers) != 0 {
		t.Fatal("expected pipe subscriber index cleared")
	}
}

func TestUnionAndSubtractUserIDs(t *testing.T) {
	got := unionUserIDs([]string{"a", "b"}, []string{"b", "c"})
	if len(got) != 3 {
		t.Fatalf("union len = %d want 3: %v", len(got), got)
	}
	sub := subtractUserIDs([]string{"a", "b", "c"}, []string{"b"})
	if len(sub) != 2 || sub[0] != "a" || sub[1] != "c" {
		t.Fatalf("subtract = %v want [a c]", sub)
	}
}

func TestNormalizePipeTag(t *testing.T) {
	if got := NormalizePipeTag(" #Foo "); got != "foo" {
		t.Fatalf("got %q want foo", got)
	}
	if got := NormalizePipeTag(""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}
