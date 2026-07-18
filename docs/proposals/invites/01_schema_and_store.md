# Invites 01 — Schema and store

## Status

Proposed.

## Depends on

[00](00_signup_mode.md)

## Context

Step 00 exposes signup mode and quota. This step persists invites and the
durable `users.invited_by` FK. No HTTP routes yet — only DDL + store helpers
the later steps call.

## Scope

- Add `invites` table DDL to `InitDB` in [`db.go`](../../../db.go).
- Add nullable `invited_by` column on `users` (new installs via `CREATE TABLE`
  update; existing DBs via `ALTER TABLE … ADD COLUMN IF NOT EXISTS` in the
  same init path the project already uses for schema evolution, or fold into
  the `CREATE TABLE` definition for greenfield — match whatever pattern
  nearby tables use; today most tables are `CREATE TABLE IF NOT EXISTS` only,
  so **include `invited_by` in the `users` CREATE** and add a separate
  `ALTER TABLE users ADD COLUMN IF NOT EXISTS invited_by …` so existing
  deployments pick it up without a migrator).
- Implement store in `syrinx/invites`: hash helper, create, count-by-creator,
  get-by-token-hash, mark-claimed, revoke, list-by-creator.
- Indexes needed for lookup by `token_hash` and list-by-`created_by`.

## Non-goals

- HTTP handlers / routes (02).
- Wiring into `Signup` (03).
- SPA (04–05).
- Signing invite rows.

## Design

### `invites` DDL

```sql
CREATE TABLE IF NOT EXISTS invites (
	id         VARCHAR(255) PRIMARY KEY,
	token_hash BYTEA NOT NULL UNIQUE,
	created_by VARCHAR(255) NOT NULL REFERENCES users(id),
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	claimed_at TIMESTAMPTZ,
	claimed_by VARCHAR(255) REFERENCES users(id),
	revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_invites_created_by
	ON invites(created_by);
```

Optional check constraint (nice-to-have, not required): claimed and revoked are
mutually exclusive in practice via application logic (`revoked_at` only set
when `claimed_at IS NULL`).

### `users.invited_by`

```sql
ALTER TABLE users
	ADD COLUMN IF NOT EXISTS invited_by VARCHAR(255) REFERENCES users(id);
```

Also add the column to the `CREATE TABLE users (…)` definition for new
databases so greenfield schemas match.

No index required for v1 (lookups are by user id primary key when loading a
profile).

### Token hashing

```go
func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
```

Generate raw token:

```go
// 32 bytes → base64.RawURLEncoding.EncodeToString
func NewToken() (raw string, hash []byte, err error)
```

IDs for invite rows: reuse the same random-id helper / alphabet length as
user IDs (`generateUserID` style), or a dedicated `generateInviteID` in the
invites package with the same entropy. Collision → retry insert.

### Store API (suggested)

```go
type Invite struct {
	ID        string
	CreatedBy string
	CreatedAt time.Time
	ClaimedAt *time.Time
	ClaimedBy *string
	RevokedAt *time.Time
}

type Store struct{ DB *sql.DB /* or tx-capable interface */ }

func (s *Store) CountByCreator(ctx, creatorID) (int, error)
func (s *Store) Insert(ctx, id, creatorID, tokenHash, createdAt) error
func (s *Store) ListByCreator(ctx, creatorID) ([]Invite, error)
func (s *Store) GetByTokenHash(ctx, hash []byte) (*Invite, error)
func (s *Store) MarkClaimed(ctx, tx, inviteID, claimedBy string, claimedAt time.Time) (bool, error)
	// conditional: WHERE id=$1 AND claimed_at IS NULL AND revoked_at IS NULL
	// returns whether a row was updated
func (s *Store) Revoke(ctx, inviteID, creatorID string, revokedAt time.Time) (status, error)
	// issuer-only; distinguish not-found / not-owner / already-claimed / ok
```

`MarkClaimed` must accept an existing `*sql.Tx` so step 03 can run it inside
the signup transaction.

Do **not** return or log raw tokens from the store — the store only ever
sees hashes after create returns the raw string to the handler once.

### Status derivation (read model)

| Condition            | `status`  |
|----------------------|-----------|
| `revoked_at != null` | `revoked` |
| `claimed_at != null` | `claimed` |
| else                 | `pending` |

Prefer checking revoked before used if both were somehow set (should not
happen).

## Test plan

- [ ] `InitDB` creates `invites` and `users.invited_by` on empty DB
- [ ] Re-running `InitDB` on existing DB adds `invited_by` via `IF NOT EXISTS`
- [ ] `HashToken` is stable; different inputs differ
- [ ] `Insert` + `GetByTokenHash` round-trip
- [ ] `CountByCreator` counts used and revoked rows
- [ ] `MarkClaimed` second call returns false / no row updated
- [ ] `Revoke` on used invite fails distinctly from not-found
