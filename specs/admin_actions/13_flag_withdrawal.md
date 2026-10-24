# Admin actions 13 — Withdrawing flags, and an author's flag history

## Status

Implemented.

## Depends on

[08](08_flags.md), [12](12_admin_records.md)

## Context

A member flags a reed once, ever: a flag is keyed by reed and reporter. Today
the member can't see the flag again or take it back, and an admin weighing a
flag sees only that reed's flags, not whether its author draws flags all the
time.

This step lets a reporter recall and withdraw their flag from the reed's
page, and gives each case the author's flag count with a link to every flag
their reeds ever received.

Two rules carry over from [08](08_flags.md) and [12](12_admin_records.md):

- **A flag is never linked to an admin action.** An admin who acts on an
  author decides whether that was in response to a flag, then dismisses the
  flag and says so in the reason. A dismissal, whether the flag had merit or
  not, is how admins act on a flag.
- **The reporter never sees the dismissal.** They learn admins acted on
  their flag only by trying to withdraw it.

## Withdrawal

A withdrawal is the reporter's signature over their flag's signature,
countersigned by the reed's home server. It ties to exactly that flag, the
way an admin decision ties to its request:

```go
buildFlagWithdrawalPayload(reedID, reporterID, keyID, flagSignature string) []byte
// {"flagSignature","keyID","reedID","reporterID","type":"flag-withdrawal"}

buildFlagWithdrawalServerPayload(reedID, reporterID, serverKeyFingerprint, userSignature string, signedAt time.Time) []byte
// {"reedID","reporterID","serverKeyFingerprint","signedAt","type":"flag-withdrawal","userSignature"}
```

A withdrawn flag is the same message with one more field filled in:

```proto
message ReedFlag {
  ...
  FlagWithdrawal withdrawal = 6;
}

message FlagWithdrawal {
  UserSignature signature = 1;
  ServerSignature server_signature = 2;  // its time is the withdrawal's
}
```

`reed_flags` gains, in `InitDB`:

```sql
  withdrawal_signature_id        INT REFERENCES user_signatures(id),
  withdrawal_server_signature_id INT REFERENCES server_signatures(id),
  CONSTRAINT reed_flags_withdrawal CHECK (
    (withdrawal_signature_id IS NULL) = (withdrawal_server_signature_id IS NULL)),
  -- Dismissed or withdrawn, never both.
  CONSTRAINT reed_flags_closed_once CHECK (
    withdrawal_signature_id IS NULL OR dismissal_id IS NULL)
```

- A flag is **open** while no dismissal names it and it isn't withdrawn.
- Withdrawing doesn't free the slot: the reporter can't flag the reed again.
- A dismissal can't name a withdrawn flag; it's refused with `409
  {"error":"flags-changed"}` like any flag that isn't open.
- A withdrawn flag is deleted with the rest of its reporter's flags when
  their account is removed.

## API

| Route | Auth | Does |
|-------|------|------|
| `GET /api/flags/{reedID}` | The reporter | Their own flag on that reed, countersigned, with their withdrawal if they made one. Nothing in it says whether it was dismissed |
| `POST /api/flags/{reedID}/withdraw` | The reporter | Withdraw it `{signature}`; answers with the flag, withdrawal countersigned |
| `GET /api/admin/flags?state=withdrawn` | Admin or root | Withdrawn flags they may see, latest withdrawal first, keyset cursor, for catch-up |
| Peer relay `flag-lookup` | The reporter's home server | Fetches the reporter's flag on a reed of the callee's, as `GET /api/flags/{reedID}` answers it |
| Peer relay `flag-withdrawal` | The reporter's home server | Hands a withdrawal of a flag on a reed of the callee's to the callee, as `flag` does |

`GET /api/flags/{reedID}` exists for a device that holds only `{reedId,
reporterId}` (a flag it learned was already raised, from a 409) and needs
the signature it withdraws over. It never reveals whether admins dismissed
the flag. A flag on another server's reed is fetched through the reporter's
home server, one call to one peer.

Withdrawing is refused with:

- `404` when the caller has no flag on the reed;
- `409 {"error":"flag-acknowledged"}` when a dismissal names it: admins
  have acted on it;
- nothing for a flag already withdrawn: the withdrawal held is returned, so
  a retry is harmless.

The `flag-acknowledged` refusal is the only way a reporter learns their
flag was dismissed, and only after they confirmed the withdrawal: there's no
endpoint that tells them without withdrawing.

## Admin records

- `ADMIN_RECORD` carries the withdrawn flag, like any flag. On an admin's
  device a withdrawn copy replaces the unwithdrawn one, never the reverse:
  the same rule as a request's closed copy.
- Catch-up pages `state=withdrawn` back after the dismissals, until a page
  holds nothing the device doesn't already hold withdrawn.
- A withdrawn flag leaves `flagOpenCases`. A case left with no open flags is
  dropped.
- Recovery restores a withdrawn flag from the reporter's device or an
  admin's. A withdrawn copy reported after an unwithdrawn one withdraws it;
  the reverse changes nothing. A withdrawal of a flag a restored dismissal
  names is refused.

## The author's flag history

On an admin's device, every flag on this server's reeds is already held
([12](12_admin_records.md)), so the history is read locally, from the
`flaggedReeds` entries whose `reedId` starts with `{authorID}/`.

- **Count:** every flag the author's reeds received, open or dismissed;
  withdrawn flags are left out. Deleted ones (their reporter's account was
  removed) are gone from it.
- **List:** every flag the author's reeds received, withdrawn ones
  included, grouped into cases as on the flags page and newest first. Each
  flag shows its state: open, dismissed (with the reason, who dismissed it
  and when), or withdrawn (with when). Open cases carry the same **Dismiss**
  and **Take action** buttons as the flags page.

An admin never sees flags on their own reeds, so the history isn't offered
on their own profile.

## SPA

### Reed page (reporter)

- The reed's actions menu keeps the **Flag** item. On a reed the viewer
  flagged it reads **Flagged**, withdrawn or not; it's never disabled.
- On a flagged reed the item opens a modal: **You flagged this reed**, with
  the date (the flag's countersignature) and the note, read-only, or "No
  note" without one, and a **Withdraw** button.
- **Withdraw** asks first: "Withdraw your flag? This can't be undone, and
  you won't be able to flag this reed again." Confirming signs and sends the
  withdrawal; cancelling closes the modal.
- On success the modal closes. The device keeps the withdrawn flag.
- On a withdrawn flag the item opens a modal with the date it was flagged
  and the date it was withdrawn, saying it can't be flagged again.
- On `flag-acknowledged` the modal shows: "Admins have already acted on
  your flag, so it can't be withdrawn." The device keeps nothing of it: the
  next attempt asks the server again.
- A device holding only `{reedId, reporterId}` fetches the flag with `GET
  /api/flags/{reedID}` before signing.

There's no list of the viewer's own flags.

### Admin

- Each case on the flags page shows, next to the author, **N flags on their
  reeds** linking to their history.
- The admin toolbox on a profile gains **Flags received**, opening the same
  history. It isn't shown on the viewer's own profile.
- The history is `/admin/flags/{userID}`, under the flags tab.
- The flags page has a third mode, **Withdrawn**, beside Pending and
  Dismissed: one case per withdrawn flag, latest withdrawal first, with no
  actions.
- Each case shows the reed as a reed (avatar, author linking to their
  profile, date, text; clicking it opens the reed), then its flags, then
  the actions. Flags show ten at a time; past ten, a pill counts them.
- A withdrawn flag never shows its note, there or in a history. The note
  stays in the signed flag; only the UI leaves it out.
- The dismissal form's reason field hints that it records what was done
  about the flag, if anything, since admins dismiss a flag after acting on
  it.

## Metrics

- `syrinx.flags.withdrawn` counts withdrawals by reporter, author, reed and
  origin, charted next to flags raised and dismissed.
- `syrinx.flags.open` is a gauge of each author's open flags, by
  `author.id`. It's set whenever an author's count changes: a flag taken,
  dismissed or withdrawn, or a reporter's flags deleted with their account.
  An author whose last open flag closes is set to 0, so they drop off.
- **Abuse** tab: **Top 10 authors with open flags**, from the gauge's latest
  value per author. The admin tab's **Most flagged authors** stays: it
  counts flags raised over the window, open or not, so it shows who draws
  flags, not who has flags waiting.

## Later

- A list of the viewer's own flags, once there's a use for it.
- A toolbox view of every direct communication an author received from
  admins, broadcasts included.

## Tests

- A reporter withdraws their flag: it leaves the open case, and admins get
  the withdrawn copy. A second withdrawal returns the same one.
- Withdrawing a dismissed flag is refused with `flag-acknowledged`; the
  reporter's own `GET` answers the same whether or not it was dismissed.
- A dismissal naming a withdrawn flag is refused with `flags-changed`.
- A withdrawn flag can't be re-raised: flagging the reed again is `409
  already-flagged`.
- A withdrawal of a flag on a peer's reed goes through the reporter's home
  server, and its refusal comes back the same way.
- Recovery: a withdrawn copy after an unwithdrawn one withdraws the flag;
  the reverse changes nothing.
- The author's count leaves withdrawn flags out; the list shows them.
- `syrinx.flags.open` follows an author's open flags through a flag, a
  withdrawal, a dismissal and a reporter's removal, down to 0.
- Payload parity: Go goldens and `test:admin-payload`.
