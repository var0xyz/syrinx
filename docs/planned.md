# Planned features

These are not built yet. They are part of the direction—features that complete the picture of small communities that can talk among themselves without becoming another engagement factory or surveillance surface.

## Likes on posts

Reeds need a lightweight way to say “I saw this and it mattered” without turning the product into a scoreboard.

Likes on mainstream networks are fuel for ranking, addiction loops, and social punishment. The point here is the opposite: a quiet signal between people who already chose to share a space. No public like-count arms race as the primary UI. No algorithm that promotes whatever got the most taps. Affirmation stays human-scale—useful in a closed community, useless as a growth metric.

Exact wire format and whether likes fan out like reeds or stay lighter is still open. The constraint is fixed: likes must not become the dark-pattern core of the feed.

## Federation

Syrinx instances are meant to stay **small and independent**. Federation is how those islands form a mesh without collapsing into one corporate graph.

Operators run their own community—invite policy, recovery, trust boundaries. Federation lets communities **connect and communicate** with each other: discover, follow, or exchange across instance borders when both sides opt in. The home page vision is deliberate: underground communities away from the panopticon, **interconnected through federation**, not absorbed into a single global timeline.

This is the hard long-term piece. It has to preserve cryptographic authenticity, respect signup and privacy posture on each side, and avoid recreating “one search box over everyone.” Until it ships, each instance is a world of its own—which is already a feature.

## Ephemeral comments

Publish a comment on a reed and it **disappears after one week**—unless someone replies. A reply keeps that thread alive for **another week**. Silence lets it go.

Humans change their minds. Permanent comment sections turn every half-formed reaction into a forever-exhibit: quote-mined, context-stripped, weaponized years later. Ephemeral comments let you speak in the moment without becoming a slave to that moment. Conversation that stays warm stays visible; conversation that cools falls away.

This is related to—but different from—reed permanence and signed deletion. Reeds are intentional publications with a honesty-about-holders story. Comments are social tissue around a reed: high churn, lower permanence by default. The week-and-extend rule is the product statement: presence requires ongoing attention, not archival guilt.

## Private messaging

One-to-one messages that are **end-to-end encrypted** and delivered **directly toward the recipient**—not stored or readable on the server as a message archive.

The Values line already promises this; the work is shipping a polished path that matches the rest of the stack: local keys, verify-before-trust, no “the server has a copy for your convenience.” Relays may still help with NAT the same way reed content does—**in transit**, not as a library of chat history the operator can browse.

Chat was deferred so the PoC could ship as a canary: prove the distribution and authenticity model first, and only grow into messaging if people actually want Syrinx. When DMs return, they should be first-class reclaim-the-internet features—not bolted-on widgets.

## Group chats

Same privacy posture as private messaging, for more than two people: encrypted group conversation inside a community (and, eventually, across federation where that makes sense).

Group chat is where most real coordination happens—and where centralized apps harvest the most. The design pressure is higher than DMs: membership changes, sender keys, history for new members, and the temptation to “just keep it on the server.” Syrinx should resist that temptation. Groups inherit the closed-community model: you know who is in the room, the server is not the room’s memory, and leaving or dissolving a group should not require trusting an operator’s delete button alone.

## Discoverability

Search and indexing are useful—and they are how open networks get scraped, ranked, and surveilled. Syrinx strikes a balance by making **discoverability opt-in**.

By default, your content is for the people and communities you already reach: follows, relays, invites, federation you chose. Nothing is assumed to belong in a global index. If you want reeds (or profile surface) to be **indexed and searchable**, you turn that on deliberately. Opting in is a convenience choice; staying out is the privacy default.

How far search reaches (instance-local vs federated indexes), what metadata an index may hold, and how revocation of opt-in propagates are open design questions. The product rule is not: content is public unless hidden. It is: **content is not searchable unless you said so.**

---

When any of these land, this page should shrink or move items into [Architecture](/architecture), [Content](/content), and [Philosophy](/philosophy) as appropriate. Until then, this is the roadmap in prose.
