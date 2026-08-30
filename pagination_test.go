package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseLimitParam(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		wantLimit int
		wantErr   bool
	}{
		{"absent", "", 50, false},
		{"valid", "limit=10", 10, false},
		{"zero", "limit=0", 0, true},
		{"negative", "limit=-1", 0, true},
		{"non-numeric", "limit=abc", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/?"+c.query, nil)
			limit, err := parseLimitParam(r)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if err == nil && limit != c.wantLimit {
				t.Fatalf("limit = %d, want %d", limit, c.wantLimit)
			}
		})
	}
}

func TestParseBeforeTimeParam(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		before, err := parseBeforeTimeParam(r)
		if err != nil || before != nil {
			t.Fatalf("before = %v, err = %v, want nil, nil", before, err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/?before=2024-01-02T03:04:05Z", nil)
		before, err := parseBeforeTimeParam(r)
		if err != nil || before == nil {
			t.Fatalf("before = %v, err = %v, want non-nil, nil", before, err)
		}
		want := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		if !before.Equal(want) {
			t.Fatalf("before = %v, want %v", before, want)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/?before=not-a-time", nil)
		_, err := parseBeforeTimeParam(r)
		if err == nil {
			t.Fatal("err = nil, want non-nil")
		}
	})
}

func TestClampLimit(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 50},
		{-5, 50},
		{10, 10},
		{100, 100},
		{101, 100},
		{1000, 100},
	}
	for _, c := range cases {
		if got := clampLimit(c.in); got != c.want {
			t.Errorf("clampLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPaginationTrimCount(t *testing.T) {
	cases := []struct {
		fetched, limit int
		wantKeep       int
		wantHasMore    bool
	}{
		{0, 10, 0, false},
		{5, 10, 5, false},
		{10, 10, 10, false},
		{11, 10, 10, true},
	}
	for _, c := range cases {
		keep, hasMore := paginationTrimCount(c.fetched, c.limit)
		if keep != c.wantKeep || hasMore != c.wantHasMore {
			t.Errorf("paginationTrimCount(%d, %d) = (%d, %v), want (%d, %v)",
				c.fetched, c.limit, keep, hasMore, c.wantKeep, c.wantHasMore)
		}
	}
}

func TestPaginateRows(t *testing.T) {
	t.Run("trims and reports hasMore", func(t *testing.T) {
		items := []int{1, 2, 3, 4, 5, 6}
		got, hasMore := paginateRows(items, 5)
		if !hasMore {
			t.Fatal("hasMore = false, want true")
		}
		if len(got) != 5 {
			t.Fatalf("len(got) = %d, want 5", len(got))
		}
	})

	t.Run("no trim when under limit", func(t *testing.T) {
		items := []int{1, 2}
		got, hasMore := paginateRows(items, 5)
		if hasMore {
			t.Fatal("hasMore = true, want false")
		}
		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2", len(got))
		}
	})

	t.Run("nil slice normalizes to empty", func(t *testing.T) {
		var items []int
		got, hasMore := paginateRows(items, 5)
		if hasMore {
			t.Fatal("hasMore = true, want false")
		}
		if got == nil {
			t.Fatal("got = nil, want non-nil empty slice")
		}
		if len(got) != 0 {
			t.Fatalf("len(got) = %d, want 0", len(got))
		}
	})
}
