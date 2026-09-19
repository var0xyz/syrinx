//go:build !ops

package main

import (
	"reflect"
	"testing"
)

func TestNormalizeClaimedTags(t *testing.T) {
	tests := []struct {
		name   string
		claims []string
		want   []string
	}{
		{"empty", nil, nil},
		{"lowercased", []string{"Climate"}, []string{"climate"}},
		{"trimmed", []string{"  spaced  "}, []string{"spaced"}},
		{"dedup case-insensitive", []string{"One", "one", "ONE"}, []string{"one"}},
		{"first-appearance order", []string{"b", "a", "b"}, []string{"b", "a"}},
		{"blank entries dropped", []string{"", "  ", "real"}, []string{"real"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeClaimedTags(tt.claims)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}
