# Invites 01 — Schema and store

## Status

Implemented (`invites` DDL with PK `(created_by, id)` + `users.invited_by`;
`syrinx/invites` store helpers + token hash).

## Depends on

[00](00_signup_mode.md)

## Context

Step 00 exposes signup mode and quota. This step persists invites and the
durable `users.invited_by` FK. No HTTP routes yet — only DDL + store helpers
the later steps call.

## Scope

- Add `invites` table DDL to `InitDB` in [`db.go`](../../../db.go).
- Add nullable `invited_by` on `users` in the `CREATE TABLE` definition
  (blank slate — no `ALTER TABLE` shim for older DBs).
- Implement store in `syrinx/invites`: hash helper, create, count-by-creator,
  get-by-token-hash, get-by-creator+id, mark-claimed, revoke.
- `token_hash` UNIQUE; composite PK covers list-by-creator leading column.

## Non-goals

- HTTP handlers / routes (02).
- Wiring into `Signup` (03).
- SPA (04–05).
- Persisting signatures in Postgres (client IndexedDB only).

## Design

### `invites` DDL

```sql
CREATE TABLE IF NOT EXISTS invites (
	created_by VARCHAR(255) NOT NULL REFERENCES users(id),
	id         VARCHAR(255) NOT NULL,
	token_hash BYTEA NOT NULL UNIQUE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	claimed_at TIMESTAMPTZ,
	claimed_by VARCHAR(255) REFERENCES users(id),
	revoked_at TIMESTAMPTZ,
	PRIMARY KEY (created_by, id)
);
```

Clients mint `id`; scoping the PK to `created_by` prevents cross-user
collisions and id squatting.

### `users.invited_by`

Include in the `CREATE TABLE users (…)` definition:

```sql
invited_by VARCHAR(255) REFERENCES users(id)
```

### Token hashing

```go
func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
```

Generate raw token (client or tests):

```go
// 32 bytes → base64.RawURLEncoding.EncodeToString
func NewToken() (raw string, hash []byte, err error)
```

Invite ids: same alphabet/length as user IDs (`ValidInviteID` / `NewInviteID`).

### Store API

```go
func (s *Store) CountByCreator(ctx, creatorID) (int, error)
func (s *Store) Insert(ctx, id, creatorID, tokenHash, createdAt) error
func (s *Store) GetByCreatorAndID(ctx, creatorID, id) (*Invite, error)
func (s *Store) GetStatusByCreatorAndID(ctx, creatorID, id) (*Invite, username, error)
func (s *Store) GetByTokenHash(ctx, hash []byte) (*Invite, error)
func (s *Store) MarkClaimed(ctx, tx, createdBy, inviteID, claimedBy string, claimedAt time.Time) (bool, error)
	// WHERE created_by=$1 AND id=$2 AND claimed_at IS NULL AND revoked_at IS NULL
func (s *Store) Revoke(ctx, inviteID, creatorID string, revokedAt time.Time) error
```

`MarkClaimed` must accept an existing `*sql.Tx` so step 03 can run it inside
the signup transaction.

Do **not** return or log raw tokens from the store.

### Status derivation (read model)

| Condition            | `status`  |
|----------------------|-----------|
| `revoked_at != null` | `revoked` |
| `claimed_at != null` | `claimed` |
| else                 | `pending` |

## Test plan

- [x] `InitDB` creates `invites` and `users.invited_by` on empty DB
- [x] `HashToken` is stable; different inputs differ
- [x] `Insert` + `GetByTokenHash` round-trip
- [x] `CountByCreator` counts used and revoked rows
- [x] `MarkClaimed` second call returns false / no row updated
- [x] `Revoke` on used invite fails distinctly from not-found
