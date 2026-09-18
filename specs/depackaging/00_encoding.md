# Depackaging 00 — `encoding`

## Status

Implemented (root's own call sites only). `base64Encode`/`base64Decode`
now live in `utils.go`; `encoding/encoding.go` still exists and stays
exported since `recovery`, `realtime`, and `invites` haven't merged yet —
directory deletion deferred to whichever of those steps lands last.

## Depends on

—

## Context

`encoding/encoding.go` is 22 lines: two functions, `Base64Encode` and
`Base64Decode`, both one-line wrappers around `encoding/base64`. There is
no second file, no type, no state. It exists as a package purely so its two
functions can be called as `encoding.Base64Encode(...)` instead of
`base64Encode(...)` — a naming convenience that costs an import line at
every call site (7 files: `handlers.go`, `middlewares.go`, `root.go`,
`recovery/bundle.go`, `recovery/nest.go`, `realtime/auth.go`,
`invites/handlers.go`) for no isolation benefit, since these two functions
have no dependents that need them kept separate from root.

## Collisions

None. `grep` for `Base64Encode`/`Base64Decode` and for a bare `Encoding`
type anywhere in root's `*.go` files returns nothing.

## Move plan

1. Move `Base64Encode`/`Base64Decode` into `utils.go` at the repo root —
   it already plays this exact role (currently just
   `trimInvisibleChars`, a small general-purpose string helper with the
   same `!ops && !ripplescleanup` build tag most callers need). Rename to
   `base64Encode`/`base64Decode` (unexported — nothing outside root calls
   them once `deletion`/`invites`/`recovery`/`realtime` also merge per
   this spec's later steps; if those land after this step, keep them
   exported until the last external caller merges, then unexport in that
   step).
2. Add the section header, since `utils.go` already has other content
   (`trimInvisibleChars`) this needs to be delineated from:
   ```go
   // ============ //
   //   encoding   //
   // ============ //
   ```
3. Update every call site (`handlers.go`, `middlewares.go`, `root.go`,
   `recovery/bundle.go`, `recovery/nest.go`, `realtime/auth.go`,
   `invites/handlers.go`) to drop the `encoding.` prefix and the
   `"syrinx/encoding"` import. The four package-relative call sites
   (`recovery/*`, `realtime/*`, `invites/*`) keep the `syrinx/encoding`
   import and qualifier until *those* packages merge in a later step —
   don't break them prematurely.
4. Delete the `encoding/` directory once its file list is empty (i.e.
   once `encoding_test.go`'s equivalent tests are also ported — check
   for an `encoding/*_test.go` file and move its cases into a root test
   file covering the same two functions).

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` all pass; `encoding/`
directory no longer exists; `grep -rn '"syrinx/encoding"'` returns no
results outside packages not yet merged by this spec.
