# Conversations 01 — Verify publish payload; normalize `replying` ref

## Status

Implemented (form fields; server rebuilds canonical markdown).

## Depends on

[00](00_design.md)

## Context

`POST /reeds` must verify the author's detached signature over a coherent reed
payload before countersigning, without persisting content. Clients already
sign `reedAsMarkdown()` locally; the server reconstructs the same envelope from
form fields and verifies against those bytes.

Separately, `replying` uses the same `userID@serverID/reedID` form as
`echoing` (historically it was a bare reed id).

## Scope

- Accept form fields `content`, optional `echoing` / `replying` on `POST /reeds`
  (not a client-supplied `markdown` body).
- Rebuild canonical markdown via `ReedAsMarkdown` (must match SPA
  `reedAsMarkdown`) and verify the detached user signature over those bytes.
- Enforce content limits (140 visible / 1400 raw).
- Normalize `replying` to `userID@serverID/reedID`; validate reed refs and target
  existence (local `serverID` only for now).
- Federation beyond local targets remains future work; the wire format already
  carries `serverID`.
- Pass echo refs into the index step ([02](02_index_and_api.md)).

## Non-goals

- Storing or serving reed body/markdown on the server.
- SPA conversation UI ([03](03_spa_reed_detail.md)) beyond ref-format changes.
- Resolving targets on foreign servers (format includes `serverID`; routing later).

## Design

### `POST /reeds` request

Form fields (`application/x-www-form-urlencoded`):

| Field | Required | Description |
|-------|----------|-------------|
| `reedID` | yes | Reed id (also used as `id:` header when rebuilding markdown) |
| `signature` | yes | Base64-encoded armored **detached** PGP signature |
| `content` | yes | Body only (may be empty for bare echo); not the full envelope |
| `echoing` | no | `userID@serverID/reedID` of the echoed reed |
| `replying` | no | `userID@serverID/reedID` of the parent reed |

Processing order in `SignReed`:

1. Auth → `userID`.
2. Read `content` / `echoing` / `replying`; enforce character limits on
   `content`.
3. Parse and validate reed refs; reject bare / legacy ids; require target reed
   exists and is unremoved (`Target reed not found`).
4. `markdown := ReedAsMarkdown(reedID, userID, content, echoing, replying)`.
5. Decode `signature` → armored detached sig; load author's active public key.
6. `VerifySignature(markdown, detachedSig, pubKey)` — reject 400 on failure.
7. Countersign + `CreateReedWithEcho` (index echo when present).

Canonical markdown rules (SPA and Go must match):

- Frontmatter keys sorted alphabetically.
- Separator `: ` (colon + space).
- Headers always include `id` and `userID`; include `echoing` / `replying`
  only when non-empty.

### Reed ref parsing

```go
type ReedRef struct {
    AuthorID string
    ServerID string
    ReedID   string
}

func ParseReedRef(raw string) (ref ReedRef, ok bool)
func FormatReedRef(ref ReedRef) string
```

Rules:

- Empty → absent (not an error).
- Form: `userID@serverID/reedID` (all parts non-empty).
- `reedId` must pass `validateUUID25`.
- Bare `reedId` or `authorId!reedId` → 400 (`Invalid echoing/replying reference`).
- `serverID` must match this instance when resolving targets (foreign targets
  → `Target reed not found` for now).

A reed may have both `echoing` and `replying` set; index the echo when present.

### Client changes

**Publish** — send form fields, not markdown:

```ts
await api.createReed(reed.id, reed.userSignature.armor, {
  content: reed.content,
  echoing: reed.echoing,
  replying: reed.replying,
});
```

When replying: `reed.replying = formatReedRef(parent.userID, serverId, parent.id)`.

Signing still uses local `reedAsMarkdown()` / `asMarkdown()`; only the wire
format changed.

### Tests

- `ReedAsMarkdown` matches SPA envelope for echo-only / reply / both / neither.
- Happy path: valid fields + matching sig → 201.
- Wrong sig → 400.
- Invalid / missing target refs → 400.
- Content over limits → 400.

## Risks

- **Breaking API** — publishers must send form fields; do not send `markdown`.
- **Canonical mismatch** — if SPA and Go diverge on header order/spacing,
  verification fails; keep `ReedAsMarkdown` / `reedAsMarkdown` in lockstep.
- **Target existence check** — cannot reply/echo a reed the server has not
  countersigned yet; client can retry.
