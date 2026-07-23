# Signatures 00 — Design + table shapes

## Status

Proposed.

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
- Lock that wire JSON exposes `signedFields` alongside each signature.

## Non-goals

- Implementing DDL/store/entity switches (01–06).
- Polymorphic “parent_type / parent_id” on the signature tables.
- Using `signed_fields` / `signedFields` as a query or verify input.
- Dual-write, backfill, or any compatibility window with inline columns.

## Design

### Why two tables

User attestations and server countersignatures differ:

| | `user_signatures` | `server_signatures` |
|--|-------------------|---------------------|
| Key | Author’s user key | Server signing key |
| `signed_at` | Not stored (not part of today’s user row) | Required (wire `server.timestamp`) |
| `signed_fields` | Field names covered by the signature (wire `signedFields`) | Same |
| Typical use | Root `signature` on a resource | Nested `server` block |

A single table with `role` would force nullable `signed_at` and mix trust
domains. Separate tables keep constraints tight.

### Suggested DDL (illustrative)

```sql
CREATE TABLE user_signatures (
	id             SERIAL PRIMARY KEY,
	fingerprint    VARCHAR(255) NOT NULL,
	signature      TEXT NOT NULL,
	algorithm      TEXT NOT NULL DEFAULT 'PGP+base64',
	signed_fields  TEXT[] NOT NULL DEFAULT '{}'
);

CREATE TABLE server_signatures (
	id             SERIAL PRIMARY KEY,
	fingerprint    VARCHAR(255) NOT NULL,
	signature      TEXT NOT NULL,
	signed_at      TIMESTAMP NOT NULL,
	algorithm      TEXT NOT NULL DEFAULT 'PGP+base64',
	signed_fields  TEXT[] NOT NULL DEFAULT '{}'
);
```

`signed_fields` is an informational list of parent field names that
went into the signed payload (Postgres `TEXT[]`). Not indexed or
queried in v1. Empty array when the cover set is unknown. The same list
is exposed on the wire as `signedFields` (see Wire boundary).

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

Handlers return flattened shapes (`signature`, `signatureFingerprint` /
active-key hints, `server: { … }`) and **must** include `signedFields`
so clients see which fields each signature covers:

- User attestation: root `signedFields` (string array) next to
  `signature` / `signatureFingerprint`.
- Server countersignature: `server.signedFields` on the nested
  `Signature` / `ServerSignature` object (alongside `fingerprint`,
  `signature`, `timestamp`, …).

Load helpers join (or follow FKs) and assemble the wire struct,
including `signedFields` from the signature row.

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
4. Both tables carry `signed_fields TEXT[]` (informational; default `{}`).
5. Wire JSON includes `signedFields` (root for user sigs;
   `server.signedFields` for countersigns).

## Open questions

1. Store a payload content-hash on signature rows for audit?
2. Keep a denormalized active-key hint on `users`, or derive only via
   `user_signature_id`?
3. Whether reed countersignatures (today on a different path) join this
   model in a later step.

## Test plan

- [ ] Spec review: two-table split and FK matrix match README
- [ ] Blank-slate posture (no dual-write / no backfill) acknowledged
- [ ] `signed_fields TEXT[]` present on both tables; informational only
- [ ] Wire exposes `signedFields` for user and server signatures
