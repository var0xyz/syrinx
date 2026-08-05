# Account recovery 05 — SPA rehydration + tip publish

## Status

Proposed.

## Depends on

[03](03_rehydration_relay.md), [04](04_spa_keys_only_restore.md)

## Context

After keys-only bootstrap the user can browse and (with Approach B)
publish using the **server tip id**. Own reed bodies arrive through the
normal relay path. This step wires client progress, publish
`previousID`, missing-body UX, and complete.

## Scope

- Rehydration ledger from bootstrap `reeds[]` (per-reed
  pending/restored/exhausted).
- On relayed own reed: verify → `storeReed` → `DATA_ACK`; mark ledger.
- Tip body prioritized in display/progress; **does not** gate compose.
- Publish / compose: `previousID` = stored bootstrap `tipReedID` (omit if
  null genesis). After first successful post-recovery create, clear
  bootstrap tip override and use normal local tip.
- Quiet progress (e.g. subtle status on reeds/profile): restored count /
  total; when tip id known but tip body missing after exhaustion, inline
  banner or muted notice — not a modal wall.
- Keep retrying missing reeds while `accountRecoveryRun` is open (WS
  reconnect / periodic rehydrate POST); after user **complete**/dismiss,
  stop proactive retries (manual open-reed fetch still allowed).
- `POST /api/account-recovery/complete` when ledger done or user
  continues with gaps; mark `accountRecoveryRun` completed.

## Non-goals

- Changing SignReed HTTP beyond sending `previousID` when tip-check
  ([recovery 16](../recovery/16_reed_tip_check.md)) is implemented —
  coordinate field name with that step.
- Server recovery progress UI.
- Restoring peer content.

## Design

### Progress ledger

Client-owned (like server-recovery progress, simpler):

```ts
{
  tipReedID: string | null,
  reeds: Record<string, { status: 'pending' | 'restored' | 'exhausted' }>
}
```

Initialize from bootstrap. `exhausted` when the client received
**`REED_NOT_HELD`** (or `REED_NOT_FOUND`) for that reed. Do not block UI on
pending.

### Publish tip (Approach B)

```text
function previousIDForPublish(): string | undefined {
  if (bootstrapTipOverride != null) return bootstrapTipOverride; // may be ""
  return newestLocalOwnReedId(); // normal path
}
```

- `tipReedID === null` from server → genesis → omit `previousID`.
- Else always send bootstrap tip id until override cleared.
- On `reed_fork`, refresh tip from server (existing tip-check client
  behavior once 16 lands); update override if server tip moved.

### Missing tip body UX (resolved)

- **No modal** that blocks the app.
- If tip body `pending`: optional quiet “Restoring your reeds…” with
  count.
- If tip body `exhausted`: muted banner near compose or on own profile —
  “Your latest reed’s content could not be recovered from the network.
  You can still publish.” Keep `previousID` as the server tip id.
- Continue trying other pending reeds in the background regardless.

### Complete / dismiss

- Auto-complete when every ledger entry is `restored` or `exhausted`.
- Offer **Done** / dismiss when the user does not want to wait on
  remaining pendings (marks pending → abandoned locally, calls complete).
- Completing does not delete already-restored reeds.

### Integration with NewReedModal / reedsService

- When creating a reed, read previousID from the helper above.
- After countersign + `storeReed` success, clear `publishTipReedID`
  override.

## Test plan

- [ ] Compose enabled immediately after bootstrap with tipReedID set
- [ ] Genesis (null tip) → publish with empty previousID
- [ ] Relayed own reed lands in IndexedDB and ledger → restored
- [ ] Exhausted tip → banner; publish still sends tip id
- [ ] First new create clears tip override
- [ ] Complete called when all restored/exhausted
- [ ] Dismiss mid-pending → complete; no more proactive rehydrate
