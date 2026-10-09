# Admin actions 07 — Reserved usernames

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Root can keep names like `admin` or `__root__` from being claimed. This is
the one action with no approval and no ledger entry: it is root only, and it
doesn't act on anyone.

## Schema

```sql
CREATE TABLE IF NOT EXISTS reserved_usernames (
  username   VARCHAR(255) PRIMARY KEY,   -- stored lowercased
  created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
```

## The `root` username

`root` is not a row in this table. It is a constant check: any username equal
to `root`, case-insensitive, is refused for every identity except root's own.
That holds even after root renames, and root can always take it back.

## Enforcement

Applied in `CheckUsername`, `Signup`, `CheckUsernameForRename` and
`UpdateUser`:

- `LOWER(username)` in `reserved_usernames` → refused, with the same error as
  a taken username, so a reserved name looks taken and doesn't stand out.
- `LOWER(username) = 'root'` and the caller isn't root → refused the same way.

A reservation affects only future claims. A user who already holds the name
keeps it, and a rename back to the name they hold is not a new claim.

## API

Root only (signature auth, `isRoot`):

| Route | Does |
|-------|------|
| `GET /api/admin/reserved-usernames` | List |
| `PUT /api/admin/reserved-usernames/{username}` | Reserve (idempotent) |
| `DELETE /api/admin/reserved-usernames/{username}` | Release |

## Tests

- A reserved name is refused at signup in any letter case.
- An existing holder keeps it and can update their profile.
- A non-root user can't take `root` or `ROOT`. Root renames away and back.
- An admin gets 403 on all three routes.
