# Invites 00 — `SIGNUP_MODE` + `MAX_INVITES_PER_USER`, server info, closed gate

## Status

Implemented (`SIGNUP_MODE` via tooxie/env `values=`;
`maxInvitesPerUser` on `/api/server/info`; closed gate on Signup /
CheckUsername). Mode `invite` accepted and exposed; consume lands in
[03](03_signup_consume.md) (already Implemented in this tree).

## Depends on

—

## Context

Signup is unconditionally open today. Operators need a deploy-time switch
among open registration, invite-gated registration, and fully closed
registration, plus a per-user invite minting cap. The SPA needs those values
on the unauthenticated `/api/server/info` response so it can hide the home
“Sign Up” button and disable invite creation without being signed in.

This step lands config parsing and the `closed` gate only. Mode `invite`
is accepted and exposed but still behaves like `open` for signup until
[03](03_signup_consume.md).

## Scope

- Add to `AppConfig` in [`main.go`](../../../main.go):
  - `SignupMode` with `env:"optional,default='open',values='open,invite,closed',name='SIGNUP_MODE'"`
    (tooxie/env enforces the allow-list; no custom parser).
  - `MaxInvitesPerUser int` with `env:"optional,default='-1',name='MAX_INVITES_PER_USER'"`;
    after `MustAssert`, reject values other than `-1` or `>= 1`.
- Extend `ServerInfo` / `GetServerInfo` with `signupMode` and
  `maxInvitesPerUser` (`-1` = infinite).
- When mode is `closed`, `Signup` and `CheckUsername` return **403** with a
  stable plain/JSON error message (e.g. `"Signups are closed on this server"`).
- Document both vars in [`.env.example`](../../../.env.example):
  - `SIGNUP_MODE` commented or set to `open` as today-equivalent.
  - `MAX_INVITES_PER_USER=3` as the example default, with a comment that
    `-1` or leaving the variable unset disables the cap (infinite).

## Non-goals

- `invites` table, create/list APIs, consume-at-signup (01–03).
- SPA changes (04–05), except whatever already reads `server/info` keeps
  working with the extra fields.
- Changing signature-auth allowlists.
- Enforcing `invite` mode yet.

## Design

### Mode values

Exact strings (case-sensitive):

| Value | Meaning |
|-------|---------|
| `open` | Anyone may sign up |
| `invite` | Invite required |
| `closed` | No new signups |

Empty / unset `SIGNUP_MODE` → `open`. Any other string → process exits with
a message listing the allowed values.

### Quota values

| Input | Result |
|-------|--------|
| unset / empty | infinite (`-1`) |
| `-1` | infinite |
| integer `N` where `N >= 1` | cap at `N` |
| `0`, other negatives, non-integer, whitespace junk | fatal |

Typed representation in Go (suggested):

```go
type SignupMode string // "open" | "invite" | "closed"

// MaxInvitesPerUser is the minting cap. -1 means infinite.
type MaxInvitesPerUser int
```

### `ServerInfo` wire

```go
type ServerInfo struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	SignupMode        string `json:"signupMode"`
	MaxInvitesPerUser int    `json:"maxInvitesPerUser"` // -1 = infinite
}
```

### Closed gate placement

Early in `Handlers.Signup` and `Handlers.CheckUsername`, before form parsing
heavy work is fine either before or after parse — prefer **before** crypto /
DB work. `RecoveryMode` is checked first and unconditionally, ahead of the
`SignupMode` gate — see `specs/recovery/README.md` for why (username sniping
against not-yet-reclaimed identities):

```go
if h.cfg.RecoveryMode {
	writeResponse(w, http.StatusForbidden, "Signups are closed while this server is in recovery mode")
	return
}
if h.cfg.SignupMode == invites.ModeClosed {
	writeResponse(w, http.StatusForbidden, "Signups are closed on this server")
	return
}
```

Mode `invite`: `Signup` and `CheckUsername` require the same valid
`inviteID` + `inviteSecret` pair as signup consume (see step 03). Both must
include `inviteCreatorID` (`uid` in the share link).

### Package stub

Create `invites/` with at least:

- `mode.go` — `SignupMode` / `MaxInvitesPerUser` constants used by later steps

`RegisterRoutes` can wait for step 02.

## Test plan

- [x] Unset `SIGNUP_MODE` → mode `open`, info reports `"open"`
- [x] `SIGNUP_MODE=invite` → boots; info reports `"invite"`; signup still succeeds (until 03)
- [x] `SIGNUP_MODE=closed` → signup 403; check-username 403; info reports `"closed"`
- [x] `SIGNUP_MODE=Invite` / `foo` → process exits non-zero
- [x] Unset max → info `maxInvitesPerUser: -1`
- [x] `MAX_INVITES_PER_USER=-1` → `-1`
- [x] `MAX_INVITES_PER_USER=10` → `10`
- [x] `MAX_INVITES_PER_USER=0` / `-2` / `abc` → fatal
- [x] `.env.example` documents both variables; `MAX_INVITES_PER_USER=3` with
      a comment on disabling via `-1` or unset
