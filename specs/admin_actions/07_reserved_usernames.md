# Admin actions 07 — Reserved usernames

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

A handful of names can't be claimed by anyone, so nobody can pass for the
server or its operators. The list is fixed in code: there is no endpoint,
table or ledger entry for it, and a rebuilt server has it from the start.

## The list

`reservedUsernames` in `constants.go`: `admins` (the @admins handle,
[11](11_admins_mention.md)), `admin`, `__root__`, `webmaster` and `syrinx`,
plus the server's own name. Matching is case-insensitive.

## The `root` username

`root` is not on the list. Any username equal to `root`, case-insensitive,
is refused for every identity except root's own. That holds even after root
renames, and root can always take it back.

## Enforcement

Applied in `CheckUsername`, `Signup`, `CheckUsernameForRename` and
`UpdateUser`, through `UsernameExists`, which answers whether a name is
unavailable to the user claiming it. A reserved name gets the same error as
a taken one, so it doesn't stand out; `syrinx.admin.reserved_usernames.hits`
counts the refusals.

## Tests

- A reserved name, or the server's name, is refused at signup in any letter
  case.
- A non-root user can't take `root` or `ROOT`. Root renames away and back.
