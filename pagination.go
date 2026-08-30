package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// errInvalidLimit and errInvalidBefore are returned by parseLimitParam and
// parseBeforeTimeParam respectively; callers write their own 400 response
// using the error's message so this file has no http.ResponseWriter
// dependency.
var (
	errInvalidLimit  = errors.New("Invalid limit")
	errInvalidBefore = errors.New("Invalid before cursor")
)

// parseLimitParam reads "limit" from the query string: 50 if absent,
// errInvalidLimit if present but not a positive integer. No upper bound
// is enforced here — callers clamp service-side via clampLimit.
func parseLimitParam(r *http.Request) (limit int, err error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 50, nil
	}
	n, convErr := strconv.Atoi(raw)
	if convErr != nil || n < 1 {
		return 0, errInvalidLimit
	}
	return n, nil
}

// parseBeforeTimeParam reads "before" from the query string as an RFC3339
// timestamp cursor: nil if absent/blank, a UTC second-truncated
// *time.Time on success, or errInvalidBefore on parse failure.
//
// Not used by ripples' before cursor, which is an opaque base64-JSON
// string validated by decodeRippleCursor, not an RFC3339 timestamp.
func parseBeforeTimeParam(r *http.Request) (before *time.Time, err error) {
	raw := strings.TrimSpace(r.URL.Query().Get("before"))
	if raw == "" {
		return nil, nil
	}
	t, parseErr := time.Parse(time.RFC3339, raw)
	if parseErr != nil {
		return nil, errInvalidBefore
	}
	t = t.UTC().Truncate(time.Second)
	return &t, nil
}

// clampLimit applies the standard service-side bound shared by every
// paginated list query: <=0 becomes 50, >100 becomes 100.
func clampLimit(limit int) int {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return limit
}

// paginationTrimCount is the keyset-pagination "how many rows to keep"
// arithmetic: fetched is limit+1 when a next page exists, since that
// extra row is only ever fetched to detect it.
func paginationTrimCount(fetched, limit int) (keep int, hasMore bool) {
	if fetched > limit {
		return limit, true
	}
	return fetched, false
}

// paginateRows trims items (already fetched at limit+1 rows) down to
// limit, reports whether more rows exist beyond it, and normalizes a nil
// slice to an empty one.
func paginateRows[T any](items []T, limit int) (trimmed []T, hasMore bool) {
	keep, hasMore := paginationTrimCount(len(items), limit)
	items = items[:keep]
	if items == nil {
		items = []T{}
	}
	return items, hasMore
}
