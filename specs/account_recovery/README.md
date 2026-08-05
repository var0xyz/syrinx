# Account recovery (key-only restore)

This directory is the **user account recovery** feature: a user still known to
a healthy server reconstitutes a working client from **private keys alone**,
with the server supplying identity + graph metadata and orchestrating peer
relay of the user’s own reed bodies.

It is **not** server DB reconstruction (`RECOVERY_MODE` under
[`../recovery/`](../recovery/README.md)). Naming:

| Term | Meaning |
|------|---------|
| **Server recovery** | Operator wiped / rebuilt DB; clients report evidence *to* the server |
| **Import** | Full encrypted backup (`.sxb.gpg`) or identity (`.sxi.gpg`) |
| **Account recovery** | Keys only; server still has the account; peers relay content *to* the user |

**Code organization:** server-side account-recovery logic lives in
`syrinx/accountrecovery` (challenge, bootstrap, rehydration bookkeeping).
Do **not** overload `syrinx/recovery` or `ongoing_recoveries`. Main wires
DDL in `InitDB`, registers routes always (not behind `RECOVERY_MODE`), and
hooks realtime rehydration into the existing relay path. SPA owns export,
keys-only `/import` fork, session install, tip-id publish, and background
rehydration UX.

| # | Title | Depends on |
|---|-------|------------|
| [00](00_design.md) | Design + tip approaches + restore fork | — |
| [01](01_key_export.md) | Identity export `.sxi.gpg` (Backup Keys) | 00 |
| [02](02_challenge_bootstrap.md) | Challenge + bootstrap API + rehydration row | 00 |
| [03](03_rehydration_relay.md) | Server-orchestrated own-reed relay + complete | 02 |
| [04](04_spa_keys_only_restore.md) | SPA keys-only `/import` fork + session | 01, 02 |
| [05](05_spa_rehydration_publish.md) | SPA rehydration + tip `previousID` + UX | 03, 04 |
| [06](06_device_takeover.md) | Device binding on bootstrap (takeover) | 02, [recovery 17](../recovery/17_device_binding.md) |
| [07](07_root_user_bootstrap.md) | Root user `id=1` startup bootstrap + `.sxi.gpg` | 01, 04 |

After 00, **01** (export) may land in parallel with **02** (API). **04**
needs both. **05** needs relay (03) and the SPA session (04). **06** waits
on device binding; until then bootstrap skips bind but the takeover warning
copy may still ship in 04. **07** (root bootstrap) uses the same identity
`.sxi.gpg` shape as [01](01_key_export.md).

---

## Status

**Proposed** (00 design locked on Approach B tip id; **01 implemented**;
02–07 below).

## Locked decisions (from 00)

| Topic | Decision |
|-------|----------|
| Entry | Same “Already a user” / `/import`; fork on backup vs keys |
| Key | **Active** private key via identity backup (`.sxi.gpg`, Backup Keys) |
| Graph | **Following** only (no followers to client) |
| Content | Own reed bodies via existing relay; no peer profiles |
| Publish tip | Approach **B**: bootstrap sends tip **id**; body not required |
| Tip-check | Keep ([recovery 16](../recovery/16_reed_tip_check.md)); do not abolish |
| Device | Import / account recovery **supersedes** older devices (06) |
| Bookkeeping | Dedicated row; **not** `ongoing_recoveries` / not `RECOVERY_MODE` |
| Root bootstrap | Empty DB requires passphrase; one-shot mint `id = "1"`, write `.sxi.gpg`, exit; admin imports via `/import` (07) — no open signup required |
