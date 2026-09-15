# Content privacy 05 — `ContentRejectedData.reason`

## Status

Implemented.

## Depends on

—

## Context

`CONTENT_REJECTED` already existed end-to-end (client sends, server logs
+ feeds a metric) before this change, but carried only a bare store name —
no way to distinguish *why* a piece of content was rejected. This
migration introduces several new, distinguishable failure modes (claimed
tag mismatch, decrypt failure) alongside the pre-existing generic
verification failure, so the reason needed a name.

## Wire change

```go
type ContentRejectedData struct {
    StoreName string `json:"store_name"`
    Reason    string `json:"reason,omitempty"` // new
}
```

Additive and non-breaking: an older client omitting `reason`, or a server
reading an old payload, both see an empty string, same as no reason at
all.

## Standardized reason strings

- `length_exceeded`
- `tag_claim_mismatch`
- `mention_claim_mismatch` (separate metric, `MentionClaimRejected` — see
  [03](03_mention_inbox.md) — since it needs the author's id too, not
  just a reporter and store name)
- `decrypt_failed`
- `signature_invalid`

Not an enum type — a small closed set of plain strings, to avoid
over-engineering pre-launch. `handleContentRejected` (Go) logs the reason
and passes it to `rs.metrics.ContentRejected`, which gained a `reason`
parameter (`Noop` and `OTEL` implementations both updated).

## Acceptance

- `sendContentRejected(storeName, reason?)` (SPA) and
  `ContentRejected(ctx, reporterUserID, storeName, reason)` (Go metrics
  interface) both accept the optional reason.
- The `reason` attribute appears on the `syrinx.content.rejected` OTEL
  counter when present.
