# Signatures 00 — Design + table shapes

## Status

Accepted — design locked; landed via [01](01_schema.md)–[06](06_migrate_reed_removals.md)
and [08](08_wire_nested_blocks.md).

## Depends on

—

## Context

Inline signature columns are repeated across identity, keys, revocations,
and deletion certs. See [README](README.md).

**Blank slate — no migration, no backwards compatibility.** Rewrite schema
and code in place. No dual-write, no backfill, no expand/contract, no
old-client or old-wire support. Callers and DBs ship in lockstep (recreate
DB if needed).

## Scope

- Lock the split into `user_signatures` vs `server_signatures`.
- Lock how entities reference them (FK columns).
- Lock nested wire blocks under `userSignature` / `serverSignature`
  ([08](08_wire_nested_blocks.md)).

## Non-goals

- Implementing DDL/store/entity switches (01–06).
- Polymorphic “parent_type / parent_id” on the signature tables.
- Dual-write, backfill, or any compatibility window with inline columns.

## Design

### Why two tables

User attestations and server countersignatures differ:

| | `user_signatures` | `server_signatures` |
|--|-------------------|---------------------|
| Key | Author’s user key | Server signing key |
| `signed_at` | Not stored (not part of today’s user row) | Required (wire `serverSignature.timestamp`) |
| Typical use | Root `userSignature` on a resource | Nested `serverSignature` block |

A single table with `role` would force nullable `signed_at` and mix trust
domains. Separate tables keep constraints tight.

### Suggested DDL (illustrative)

```sql
CREATE TABLE user_signatures (
	id             SERIAL PRIMARY KEY,
	fingerprint    VARCHAR(255) NOT NULL,
	signature      TEXT NOT NULL
);

CREATE TABLE server_signatures (
	id             SERIAL PRIMARY KEY,
	fingerprint    VARCHAR(255) NOT NULL,
	signature      TEXT NOT NULL,
	signed_at      TIMESTAMP NOT NULL
);
```

Payload binding for verify still lives on the parent entity / verify
path; optional content-hash columns remain an open question.

### Entity linkage

Relationship to each live resource is **1:1** (profile/key/revoke/removal
overwrites the current attestation; we do not keep signature history on
the entity). For 1:1, put FKs on the entity — **no intermediate join
table** (that would only help for 1:N history later).

Example on `users`:

```sql
user_signature_id    INT NOT NULL REFERENCES user_signatures(id),
server_signature_id  INT NOT NULL REFERENCES server_signatures(id)
```

Same pattern elsewhere (FKs `NOT NULL` from the start; inline signature
columns are removed in the same step):

| Entity | User FK | Server FK |
|--------|---------|-----------|
| `users` | yes | yes |
| `user_keys` | no | yes |
| `user_key_revocations` | yes | yes |
| `reed_removals` | yes | yes |
| account removals (later) | yes | yes |

### Wire boundary

Handlers return nested `userSignature` / `serverSignature` blocks
([08](08_wire_nested_blocks.md)): `fingerprint` + `armor` on both;
server blocks also `serverID` and `timestamp`.

Load helpers follow FKs and assemble wire structs from signature rows.

### Cutover posture

Per entity (03–06): rewrite `InitDB` CREATE for that table to use FKs
only, update all read/write paths to the signature tables, drop the
inline signature columns from the schema definition. No dual-write
window. Recreate the DB when reshaping existing deployments.

## Resolved

1. **Blank slate:** no migration, no dual-write, no backwards compatibility.
2. `user_signatures` + `server_signatures` (not a unified `signatures` table).
3. Direct FKs on entities for the current **1:1** live attestation; no
   intermediate join tables (reserve those only if we add history later).
4. Wire JSON nests under `userSignature` / `serverSignature`
   ([08](08_wire_nested_blocks.md)).

## Open questions

1. Store a payload content-hash on signature rows for audit?
2. Keep a denormalized active-key hint on `users`, or derive only via
   `user_signature_id`?
   **Resolved in 03:** keep `users.user_fingerprint` as the active-key hint;
   signing key for the identity record comes from the user-signature row.
3. Whether reed countersignatures (today on a different path) join this
   model in a later step.

## Test plan

- [ ] Spec review: two-table split and FK matrix match README
- [ ] Blank-slate posture (no dual-write / no backfill) acknowledged
- [ ] Nested wire blocks match [08](08_wire_nested_blocks.md)
