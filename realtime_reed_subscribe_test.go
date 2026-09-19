//go:build !ops && !ripplescleanup

package main

import "testing"

func TestReedSubscriptionBookkeeping(t *testing.T) {
	cm := newRealtimeConnectionManager()
	client := newRealtimeClient(nil, "viewer")

	cm.SubscribeReed(client, "reed1")
	key := realtimeReedKey("reed1")
	if _, ok := client.reedSubscriptions[key]; !ok {
		t.Fatal("expected client reed subscription")
	}
	if len(cm.reedSubscribers[key]) != 1 {
		t.Fatal("expected one reed subscriber")
	}

	cm.UnsubscribeReed(client, "reed1")
	if _, ok := client.reedSubscriptions[key]; ok {
		t.Fatal("expected client reed subscription cleared")
	}
	if _, ok := cm.reedSubscribers[key]; ok {
		t.Fatal("expected reed subscriber map entry removed")
	}
}

func TestRealtimeEchoCountChangedString(t *testing.T) {
	if got := realtimeEchoCountChanged.String(); got != "EchoCountChanged" {
		t.Fatalf("String() = %q, want EchoCountChanged", got)
	}
}

func TestRealtimeReplyCountChangedString(t *testing.T) {
	if got := realtimeReplyCountChanged.String(); got != "ReplyCountChanged" {
		t.Fatalf("String() = %q, want ReplyCountChanged", got)
	}
}
