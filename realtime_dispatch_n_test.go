//go:build !ops && !ripplescleanup

package main

import "testing"

func TestDispatchNTimes(t *testing.T) {
	tests := []struct {
		name      string
		n         int
		available int // how many calls to step return true before false
		wantSent  int
		wantCalls int
	}{
		{"sends all n when supply is plentiful", 5, 100, 5, 5},
		{"stops early when supply runs out mid-batch", 5, 3, 3, 4},
		{"nothing available at all", 5, 0, 0, 1},
		{"n is zero: never calls step", 0, 100, 0, 0},
		{"exact match: supply equals n", 5, 5, 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			step := func() bool {
				calls++
				return calls <= tt.available
			}
			sent := dispatchNTimes(tt.n, step)
			if sent != tt.wantSent {
				t.Errorf("sent = %d, want %d", sent, tt.wantSent)
			}
			if calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}
