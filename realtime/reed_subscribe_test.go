package realtime

import "testing"

func TestReedSubscriptionBookkeeping(t *testing.T) {
	cm := NewConnectionManager()
	client := NewClient(nil, "viewer")

	cm.SubscribeReed(client, "author", "reed1")
	if !client.reedSubscriptions["author/reed1"] {
		t.Fatal("expected client reed subscription")
	}
	if len(cm.reedSubscribers["author/reed1"]) != 1 {
		t.Fatal("expected one reed subscriber")
	}

	cm.UnsubscribeReed(client, "author", "reed1")
	if client.reedSubscriptions["author/reed1"] {
		t.Fatal("expected client reed subscription cleared")
	}
	if _, ok := cm.reedSubscribers["author/reed1"]; ok {
		t.Fatal("expected reed subscriber map entry removed")
	}
}

func TestEchoCountChangedString(t *testing.T) {
	if got := EchoCountChanged.String(); got != "EchoCountChanged" {
		t.Fatalf("String() = %q, want EchoCountChanged", got)
	}
}

func TestReplyCountChangedString(t *testing.T) {
	if got := ReplyCountChanged.String(); got != "ReplyCountChanged" {
		t.Fatalf("String() = %q, want ReplyCountChanged", got)
	}
}
