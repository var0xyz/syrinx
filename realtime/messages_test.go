package realtime

import (
	"encoding/json"
	"testing"
)

func TestNewReedNotHeldMsg(t *testing.T) {
	t.Parallel()

	msg := NewReedNotHeldMsg("req-1", "reed-1")
	if msg.Type != "REED_NOT_HELD" {
		t.Fatalf("Type = %q, want REED_NOT_HELD", msg.Type)
	}
	if msg.Data.RequestID != "req-1" || msg.Data.ReedID != "reed-1" {
		t.Fatalf("unexpected data: %+v", msg.Data)
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if decoded["type"] != "REED_NOT_HELD" {
		t.Fatalf("wire type = %v", decoded["type"])
	}
	data, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data object")
	}
	if data["request_id"] != "req-1" || data["reed_id"] != "reed-1" {
		t.Fatalf("unexpected wire data: %+v", data)
	}
}

func TestReedNotHeldDistinctFromReedNotFound(t *testing.T) {
	t.Parallel()

	held := NewReedNotHeldMsg("req-1", "reed-1")
	found := NewReedNotFoundMsg("req-1", "reed-1")
	if held.Type == found.Type {
		t.Fatalf("REED_NOT_HELD and REED_NOT_FOUND must differ on the wire")
	}
}
