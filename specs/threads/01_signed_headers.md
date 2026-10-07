# Threads 01 — `replying` and `thread` header objects

## Status

Proposed.

## Depends on

[00](00_design.md)

## Scope

- SPA `ReedType` / `Reed` (`lib/types/reed.ts`): replace `replying?: string`
  and `threadId?: string` with `replying?: { to: string; root: string }`, and
  add `thread?: { head: string; index: number }`.
- `reedAsMarkdown` flattens both to dotted keys (`replying.root`,
  `replying.to`, `thread.head`, `thread.index`), sorted with the other
  headers.
- Verifiers (`lib/verifiers/`): reject a reed carrying both `replying` and
  `thread`, or `thread` together with `echoing`.
- Rename `threadId` → `replying.root` in every SPA consumer
  (`resolveThreadId`, `reedReplies` repository, conversation components,
  routes) and in the WS proto fields that carry it.
- Server: `SignReed` form field `replying` → `replyingTo`; it keeps deriving the
  conversation root (`ResolveThreadIDForParent`, renamed
  `ResolveConversationRoot`). Rename `reed_replies.thread_id` → `root_id` and
  the federation `relayNewReedReply.ThreadID` field with it.
- `SignReed` refuses thread parts: they are only published through
  `POST /threads` ([02](02_publish.md)).

## Tests

- `reedAsMarkdown` output for a reply, a head and a later part. The server
  never parses it, so there's no Go parity side.
- Verifier rejects `replying` + `thread`.

## Non-goals

- Publishing threads (02).
