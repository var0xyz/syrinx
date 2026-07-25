# Normalized signature storage (`user_signatures` / `server_signatures`)

This directory is the **signature-table refactor** proposal set. Numbered
files below are independently reviewable implementation steps. Land them in
order unless a step's "Depends on" says otherwise.

**Blank slate — no migration, no backwards compatibility.** No dual-write,
no backfill, no expand/contract, no old wire/clients. Rewrite `InitDB` and
code paths; recreate the DB if an existing deployment still has inline
columns. Callers ship in lockstep with the server.

**Deferred relative to deletion.** New features (e.g. reed removals) may
still use inlined columns until this set lands; switch them in the
matching step here.

**Code organization:** shared insert/load helpers in `syrinx/signing`.
Main wires DDL. Call sites in handlers/services/recovery switch per
entity step.

| #                                    | Title                                              | Depends on |
|--------------------------------------|----------------------------------------------------|------------|
| [00](00_design.md)                   | Design + table shapes                              | —          |
| [01](01_schema.md)                   | DDL for `user_signatures` + `server_signatures`    | 00         |
| [02](02_store.md)                    | Store helpers (insert / get by id)                 | 01         |
| [03](03_migrate_users.md)            | Switch `users` to signature FKs                    | 02         |
| [04](04_migrate_user_keys.md)        | Switch `user_keys` to server signature FK          | 02         |
| [05](05_migrate_revocations.md)      | Switch `user_key_revocations` to signature FKs     | 02, 03     |
| [06](06_migrate_reed_removals.md)    | Switch `reed_removals` (and account later)         | 02; deletion 01 |
| [08](08_wire_nested_blocks.md)       | Nested `userSignature` / `serverSignature` wire    | 00; after 03–06 preferred |
| [09](09_verify_server_countersignatures.md) | Verify every signed resource before store (+ possession) | 08; prereq [08](../08_client_signature_validation.md) |

Steps 03–06 may proceed in parallel after 02 (except 05 prefers 03 if
identity active-key hints stay on `users`). Prefer one entity at a time
if staffing is serial. There is no separate “drop legacy columns” step —
each entity step removes inline signature columns when it switches.
Wire nesting ([08](08_wire_nested_blocks.md)) is a separate blank-slate
JSON reshape. Universal verify-before-store + possession
([09](09_verify_server_countersignatures.md)) follows wire nesting.

---

## Status

- **00 accepted** (design locked).
- **01 implemented** (`InitDB` DDL + smoke test).
- **02 implemented** (`syrinx/signing` store).
- **03 implemented** (`users` → signature FKs).
- **04 implemented** (`user_keys` → server signature FK).
- **05 implemented** (`user_key_revocations` → signature FKs).
- **06 implemented** (`reed_removals` + `account_removals` → signature FKs).
- **08 implemented** (nested `userSignature` / `serverSignature` wire).
- **09 proposed** (verify every signed resource before store + possession).

## Motivation

Many entities repeat the same attestation columns:

| Entity | User attestation | Server attestation |
|--------|------------------|--------------------|
| `users` | `user_signature`, `user_fingerprint` | `server_signature`, `server_fingerprint`, `server_signed_at` |
| `user_keys` | — | `server_signature`, `server_fingerprint`, `server_signed_at` |
| `user_key_revocations` | `user_signature`, `user_fingerprint` | `server_signature`, `server_fingerprint`, `server_signed_at` |
| `reed_removals` | `user_signature`, `user_fingerprint` | `server_signature`, `server_fingerprint`, `server_signed_at` |

That duplication is easy to get wrong when adding a signed resource and
embeds wire-format storage in every table. Normalize into two tables —
**`user_signatures`** and **`server_signatures`** — and point entities at
them with FKs. Wire JSON nests attestations under `userSignature` /
`serverSignature` ([08](08_wire_nested_blocks.md)).

## Shape (summary)

```text
user_signatures     server_signatures
       ^                     ^
       | FK                  | FK
   entity rows  (users, user_keys, revocations, reed_removals, …)
```

- **User** rows: fingerprint + detached signature (no server timestamp).
- **Server** rows: fingerprint + detached signature + `signed_at`
  (authoritative countersign time).

See [00](00_design.md) for DDL sketches and resolved decisions.

## Resolved

1. **Blank slate:** no migration, dual-write, or backwards compatibility;
   lockstep deploy / DB recreate only.
2. Two tables (`user_signatures`, `server_signatures`), not one with a
   `role` column.
3. Entities hold FKs to those tables (**1:1**, no intermediate join
   tables); inline columns removed in the same entity step.
4. Wire responses nest attestations under `userSignature` /
   `serverSignature` ([08](08_wire_nested_blocks.md)): `fingerprint` +
   `armor` (server blocks also `serverID` + `timestamp`).
5. Timing: deferred; do not block deletion on this set.

## Open questions

See [00](00_design.md#open-questions).
