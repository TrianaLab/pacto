---
"@pacto/core": minor
---

Fix two ways a fleet snapshot could report itself healthier than it was, and
report a contested lockfile instead of silently dropping its pins.

A source that dropped or invalidated a record used to keep saying it was
`available`, and the snapshot kept calling itself `complete`. The two halves of
one collection were graded differently: a deployment target kept after one bad
enum value was normalized marked its source partial, while a revision discarded
outright — no immutable digest and a bundle that could not be hashed — left the
source looking fully read, even as the snapshot's own limitations said a record
was missing. Both now count. If a source could not deliver a record, every
answer drawn from it carries the incomplete-knowledge envelope that fact
deserves, so an absent service is never read as proof the service does not
exist.

Two sources that disagree about one revision's `pacto.lock` are now reported
with a new `REVISION_LOCK_CONFLICT` limitation. A lock decides which bundle a
declared dependency or reference actually resolves to, and the same revision can
reach a snapshot from a registry and from a working copy with different lock
bytes. Serving whichever arrived first would let source completion order change
the resolved graph under an unchanged snapshot ID, so the pins are discarded —
and the reference detail now says they were discarded because contributors
disagreed, rather than reporting that the lockfile recorded no resolution at
all. That distinction matters: the old wording sent operators off to re-run
`pacto lock`, regenerate identical pins and watch nothing change. Locks are
compared on the resolutions they record, so two contributors who produced
byte-identical pins on different Pacto releases still agree.

Two limitation codes are deprecated and no longer emitted by anything.
`REVISION_CONTENT_CONFLICT` described two sources pinning one revision key to
different contract bodies, which a content-addressed key rules out; the real
disagreements it stood for are `REVISION_DOCUMENT_CONFLICT` and the new
`REVISION_LOCK_CONFLICT`. `REVISION_CONTENT_MUTABLE` described a revision
resolved through a tag or a path, which `REVISION_IDENTITY_UNRESOLVED` already
says at the point the identity is derived. Both Go constants remain exported
through v3 so existing code keeps compiling, and both are removed at v4.
