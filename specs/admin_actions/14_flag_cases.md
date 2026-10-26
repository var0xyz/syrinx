# Admin actions 14 — Flag cases

## Status

Proposed.

## Depends on

[08](08_flags.md), [12](12_admin_records.md), [13](13_flag_withdrawal.md)

## Context

A dismissal is signed over the **list of reporters** whose flags it closes
(`buildFlagDismissalPayload`). That list has to be stored apart from the
flags (`flag_dismissal_reporters`) for two reasons:

- an account removal deletes the reporter's flags, dismissed ones included,
  and the dismissal must still verify;
- in recovery a dismissal can come back before, or without, some of the
  flags it closed, and the flags held can't rebuild the signed list.

The table duplicates `reed_flags.dismissal_id` in normal operation, and the
signed list ties a dismissal to every one of its flags.

This step groups a reed's flags into **cases**. A dismissal closes a case,
and its signature covers the case, not a list. A flag carries its case, and
proves it with its countersignature.

## Locked decisions

| Topic | Decision |
|---|---|
| Case | One reed's flags, from the first open one until a dismissal closes them or every one is withdrawn |
| One open case | A reed has at most one open case. A flag joins it, or opens one in the same transaction when there isn't one |
| Membership | `reed_flags.case_id` is a non-nullable foreign key. The server says which case a flag joined in the flag's countersignature |
| Dismissal | Closes a case. Signed by the admin over `{caseID, reedID, reason}`, countersigned by the server. No reporter list, no count |
| Late flags | A flag that joins a case just before it's dismissed is dismissed with it. Admins see it in the Dismissed section; further flags open a new case |
| Void | A case whose flags are all withdrawn is **void**, with no signature of its own: each withdrawal is signed. The next flag opens a new case |
| Final | A dismissed or void case never reopens and never takes a flag |
| Order | A case lists its flags newest first |
| Live | Every flag, withdrawal and dismissal is pushed to admins as it happens (`ADMIN_RECORD`), as today |
| Reporter privacy | Unchanged from [13](13_flag_withdrawal.md): the reporter never sees their case's state. `GET /api/flags/{reedID}` answers the flag with its case id and nothing about the case; they learn of a dismissal only by trying to withdraw |
| Removed reporter | Their flags in **open** cases are deleted with their account. Their flags in dismissed and void cases stay, shown as from a removed account |
| Cleanup | A single admin who can see a case may delete it once it was dismissed, or voided, more than 6 months ago. Its flags go with it. Not a request, not in the ledger |
| Recovery | A flag comes back from any device that holds it; an admin's copy may carry its case's dismissal. While `RECOVERY_MODE` isn't `off`, a case opened before `first_boot_at` can't be dismissed, and its flags can't be withdrawn |
| Blank slate | No migration. `flag_dismissals` and `flag_dismissal_reporters` are dropped from `InitDB` |

## Schema

```sql
-- One reed's flags until a dismissal closes them or all are withdrawn.
-- id is minted by this server when the case opens.
CREATE TABLE IF NOT EXISTS flag_cases (
  id           VARCHAR(255) PRIMARY KEY,  -- server-minted UUIDv7
  reed_id      VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
  opened_at    TIMESTAMP NOT NULL,        -- its first flag's countersignature time
  -- The dismissal: all set or none.
  dismissed_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
  reason       TEXT,                      -- 1 to 140 characters, checked by the API
  public_key_id       VARCHAR(255) REFERENCES public_keys(id),
  user_signature_id   INT REFERENCES user_signatures(id),
  server_signature_id INT REFERENCES server_signatures(id),
  dismissed_at TIMESTAMP,                 -- when this server countersigned it
  -- When its last open flag was withdrawn.
  voided_at    TIMESTAMP,

  CONSTRAINT flag_cases_dismissal CHECK (
    (server_signature_id IS NULL) = (user_signature_id IS NULL) AND
    (server_signature_id IS NULL) = (dismissed_at IS NULL) AND
    (server_signature_id IS NULL) = (reason IS NULL)),
  CONSTRAINT flag_cases_closed_once CHECK (dismissed_at IS NULL OR voided_at IS NULL)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_flag_cases_open
  ON flag_cases (reed_id) WHERE dismissed_at IS NULL AND voided_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_flag_cases_dismissed
  ON flag_cases (dismissed_at DESC, id DESC) WHERE dismissed_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_flag_cases_voided
  ON flag_cases (voided_at DESC, id DESC) WHERE voided_at IS NOT NULL;
```

The reason keeps today's 140-character limit (`maxDismissalReasonChars`).

`reed_flags`:

- `dismissal_id` becomes `case_id VARCHAR(255) NOT NULL REFERENCES
  flag_cases(id) ON DELETE CASCADE`.
- `reed_flags_closed_once` goes: a withdrawn flag in a dismissed case can't
  happen, because withdrawing refuses once the case is dismissed.
- `idx_reed_flags_open` is `ON reed_flags (case_id) WHERE
  withdrawal_signature_id IS NULL`; open cases are found through
  `idx_flag_cases_open`.
- `idx_reed_flags_dismissal` goes.

A flag is **open** when its case is open and it isn't withdrawn.

## Payloads

`identity.go`, mirrored in `signing.ts`, through `canonicalJSON`:

```go
// The reporter's signature: unchanged.
buildReedFlagPayload(reedID, reporterID, keyID, note string) []byte

// Gains caseID: the case the server put the flag in.
buildReedFlagServerPayload(reedID, reporterID, caseID, serverKeyFingerprint, userSignature string, signedAt time.Time) []byte
// {"caseID","reedID","reporterID","serverKeyFingerprint","signedAt","type":"reed-flag","userSignature"}

// Replaces the reporter list with the case.
buildFlagDismissalPayload(caseID, reedID, reason, keyID string) []byte
// {"caseID","keyID","reason","reedID","type":"flag-dismissal"}

buildFlagDismissalServerPayload(caseID, reedID, serverKeyFingerprint, userSignature string, signedAt time.Time) []byte
// {"caseID","reedID","serverKeyFingerprint","signedAt","type":"flag-dismissal","userSignature"}
```

Withdrawal payloads are unchanged: a withdrawal signs the flag's signature,
which identifies the flag.

## Wire

```proto
message ReedFlag {
  string reed_id = 1 [json_name = "reedID"];
  string reporter_id = 2 [json_name = "reporterID"];
  string note = 3;
  UserSignature signature = 4;
  ServerSignature server_signature = 5;
  FlagWithdrawal withdrawal = 6;
  string case_id = 7 [json_name = "caseID"];
  // Admins' copies only, once the case is dismissed. Never sent to the
  // reporter.
  FlagDismissal dismissal = 8;
}

message FlagDismissal {
  string case_id = 1 [json_name = "caseID"];
  string reed_id = 2 [json_name = "reedID"];
  string dismissed_by = 3;
  string reason = 4;
  UserSignature signature = 5;
  ServerSignature server_signature = 6;
}

// A reed's flags as one case, for admins.
message FlagCase {
  string id = 1;
  string reed_id = 2 [json_name = "reedID"];
  string author_id = 3 [json_name = "authorID"];
  uint32 opened_at = 4;
  repeated ReedFlag flags = 5;     // newest first
  FlagDismissal dismissal = 6;     // once dismissed
  uint32 voided_at = 7;            // once void
}

message FlagCaseList {
  repeated FlagCase cases = 1;
  string next_cursor = 2;
}
```

`FlaggedReed` and `FlaggedReedList` go. `AdminRecordMessage` keeps `flag`
and `dismissal`, and gains `string deleted_case_id = 4` for a cleanup.

## API

| Route | Auth | Does |
|---|---|---|
| `POST /api/flags` | Any local member | Unchanged, except the countersigned flag carries its `case_id` |
| `GET /api/flags/{reedID}` | The reporter | Their flag with its `case_id`, never its `dismissal` |
| `POST /api/flags/{reedID}/withdraw` | The reporter | Unchanged. The withdrawal that leaves a case with no open flag voids it |
| `GET /api/admin/flags?state=open\|dismissed\|void` | Admin or root | Cases they may see: open ones by their newest flag, dismissed ones by dismissal, void ones by `voided_at`, newest first, keyset cursor. Replaces `state=withdrawn` |
| `POST /api/admin/flags/cases/{caseID}/dismiss` | Admin or root, if they can see it | `{reason, signature}`. Refused with `404` for an unknown case, `409 {"error":"case-closed"}` for one dismissed or void, `409 {"error":"opened-before-recovery"}` while recovering. Replaces `POST /api/admin/flags/{reedID}/dismiss` |
| `DELETE /api/admin/flags/cases/{caseID}` | Admin or root, if they can see it | Deletes a case closed more than 6 months ago, with its flags and their signatures. `409 {"error":"case-too-recent"}` otherwise, `409 {"error":"case-open"}` for an open one |
| Peer relay `flag`, `flag-lookup`, `flag-withdrawal` | The reporter's home server | Unchanged; the flag they carry has its `case_id` |

## Server

### Taking a flag

`insertReedFlagTx` (`flags.go`), in one transaction:

1. `SELECT id FROM flag_cases WHERE reed_id = $1 AND dismissed_at IS NULL
   AND voided_at IS NULL FOR UPDATE`.
2. None: insert a new case with a fresh UUIDv7, `opened_at` = the flag's
   time. A concurrent insert loses on `idx_flag_cases_open` and retries
   step 1.
3. Countersign the flag with that `caseID`, insert it.

### Withdrawing

`setFlagWithdrawalTx`: after storing the withdrawal, if the case has no
flag left without one, set `voided_at` to the withdrawal's time.
Refused with `flag-acknowledged` when the case is dismissed, as today.

### Dismissing

`DismissFlags` becomes `DismissCase(caseID, viewerID, d)`: lock the case,
refuse if it's closed or not visible to the viewer, check the admin's
signature over `buildFlagDismissalPayload`, countersign, write the dismissal
columns. No `ErrFlagsChanged`, no reporter check.

### Account removal

`settleAccountRemovalTx` deletes the removed account's flags in open cases
only. A case left with no flags at all is deleted; one left with only
withdrawn flags is voided.

### Cleanup

`DeleteFlagCase`: lock the case, refuse unless `dismissed_at` or
`voided_at` is more than 6 months old, delete it (flags cascade) and the
signature rows of the case and its flags, then push `ADMIN_RECORD
{deleted_case_id}` to every connected admin who could see it.

### Recovery

A flag record is a `ReedFlag`, optionally with `dismissal` set.

1. Verify the reporter's signature and the countersignature, `caseID`
   included, as `restoreFlagTx` does.
2. Upsert the case by `caseID` (`ON CONFLICT DO NOTHING`), with the reed,
   its author and `opened_at` = this flag's time, lowered to the earliest
   restored flag's.
3. Insert the flag unless held; a withdrawn copy withdraws a held one, as
   today.
4. With `dismissal` set: verify it against `buildFlagDismissalPayload` and
   its countersignature, and write it on the case unless the case already
   has one. A dismissal is final: a later record never changes it.
5. A case with flags held and none open, and no dismissal, is voided at its
   latest withdrawal.

A flag whose reporter's account was removed is restored only when its case
is dismissed, or the record carries the dismissal.

**Open question: two cases on one reed, neither known closed.** A case only
opens once the previous one closed, so of two cases on a reed the older
one is closed. But recovery may hold the older case's flags without its
dismissal (only reporters' devices reported them) and without all of them
withdrawn. `idx_flag_cases_open` then refuses the newer case. Options:

- the newer case's flags wait until the older case is known closed, which
  may never happen if no admin device reports its dismissal;
- the older case is marked closed without a dismissal (a third closed
  state, `superseded_at`, set when a newer case on the reed is restored).
  It takes no flags, can't be dismissed, and shows under Dismissed with
  "dismissed, record not recovered".

The second keeps every flag restorable. To decide before step 4.

`flag_dismissal_reporters` and the separate dismissal record kind go:
`RecoveryRecord.flag_dismissal` is removed.

## SPA

### Stores

- `flaggedReeds` keeps every flag, now with `caseId`, and on admins' devices
  `dismissal` once the case is dismissed. A copy with a dismissal replaces
  one without, never the reverse; a withdrawn copy replaces an unwithdrawn
  one, as today.
- `flagCases` replaces `flagDismissals` and `flagOpenCases`: one row per
  case, `{id, reedId, authorId, openedAt, latestAt, dismissal?, voidedAt?}`,
  indexed by state and time. `latestAt` is its newest flag's time.
- `removeReporter` drops the removed account's flags in open cases, deletes
  a case left with none and voids one left with only withdrawn flags.
- `ADMIN_RECORD {deleted_case_id}` deletes the case and its flags.

### Catch-up

On every sync, for an admin or root: page `state=open` fully, then
`dismissed` and `void` back until a page holds nothing new, storing each
case with its flags. A device that missed a cleanup keeps its copy; see
[Accepted limits](#accepted-limits).

### Admin flags page

- Three modes: **Pending**, **Dismissed**, **Void**. Each row is a case:
  the reed, then its flags newest first, then its actions.
- **Dismiss** acts on the case. The confirmation no longer needs to say the
  flags changed.
- A dismissed or void case older than 6 months shows **Delete**, with a
  confirmation: "Delete this case and its flags? This can't be undone."
- A flag from a removed account shows **[removed account]** as reporter.
- The author's flag history ([13](13_flag_withdrawal.md)) groups by case
  instead of by dismissal.

### Reporter

Unchanged. The reporter's copy has a `caseId` and never a `dismissal`.

## Metrics

- `syrinx.flags.dismissed` keeps its attributes; add `case.size` (the
  number of flags in the case).
- New `syrinx.flags.cases_deleted` counter.
- `syrinx.flags.open` is unchanged.
- Dashboard generator: a panel for cases deleted, next to flags dismissed.

## Steps

1. **Schema and payloads.** `flag_cases`, the `reed_flags` changes, the four
   payload builders and their `signing.ts` mirrors, Go goldens and
   `test:admin-payload`.
2. **Taking, withdrawing, dismissing.** The server changes above, the new
   routes, `FlagCase` listing; the old dismiss route and `state=withdrawn`
   go.
3. **Removal and cleanup.** `settleAccountRemovalTx`, `DeleteFlagCase` and
   its route, the `deleted_case_id` push.
4. **Recovery.** The restore steps above; the dismissal record kind and
   `flag_dismissal_reporters` go.
5. **SPA.** Stores, catch-up, the flags page, the history, the reporter's
   copy.
6. **Docs and specs.** `docs/admins.md` flags section: cases, void cases,
   cleanup after 6 months. Mark the dismissal sections of
   [08](08_flags.md), [12](12_admin_records.md) and
   [13](13_flag_withdrawal.md) superseded by this step; update the
   admin-actions README and `specs/README.md`.

## Accepted limits

- An admin device that was offline during a cleanup keeps the case. If it
  reports it in a later recovery, the case is restored and can be deleted
  again.
- A recovery that never gets back some of a case's flags restores the case
  with the flags it got. The dismissal still verifies.

## Tests

- A first flag opens a case; a second joins it; a flag after the dismissal
  opens a new one. Two concurrent first flags end in one case.
- The flag's countersignature names its case and fails verification with
  any other `caseID`.
- A dismissal closes the case with every flag in it, including one that
  joined after the admin listed it. A second dismissal is `case-closed`.
- Withdrawing every flag in a case voids it; the next flag opens a new
  case. Withdrawing in a dismissed case is `flag-acknowledged`.
- The reporter's `GET /api/flags/{reedID}` never carries a dismissal.
- An account removal deletes the account's flags in open cases, deletes a
  case left empty, voids one left with only withdrawn flags, and leaves
  dismissed cases whole.
- Deleting a case dismissed under 6 months ago is `case-too-recent`; an open
  one is `case-open`; one past 6 months goes with its flags and signatures
  and is pushed as deleted.
- Recovery: a flag with its dismissal restores a dismissed case; a later
  flag of the same case without one joins it; a dismissal never changes
  once held; a removed reporter's flag in an open case is refused; while
  recovering, dismissing a case opened before `first_boot_at` is
  `opened-before-recovery`.
- Payload parity: Go goldens and `test:admin-payload`.
