---
"@pacto/core": minor
---

Stop a warm OCI cache from answering for a registry it has not checked, and make
`--no-cache` mean the same thing everywhere.

**A cached tag is revalidated before it is served.** A digest names its own bytes,
so an entry written under one can never go stale. A tag is mutable: the disk entry
records what the tag pointed at when some earlier process wrote it, and re-pushing
the tag left the cache handing back the old artifact under the new name. That is
worse than stale — it is what disarmed `pacto lock`, whose whole job is to notice
a dependency's bytes changing under a fixed reference. A developer with a warm
cache got "no drift" on a dependency that had been republished.

`PullPinned` now resolves the tag's current digest before serving a disk entry.
It costs one manifest lookup and re-downloads nothing when the tag has not moved.
A registry that cannot be reached is not evidence that it did, so the entry is
still served and an offline reader keeps reading the cache. Only the disk leg
asks: an in-memory hit was put there by this same process, which already observed
the registry for that reference, so one command still resolves one tag to one
artifact. `pacto pull --local-only` and the other offline readers are untouched
and still never contact a registry.

**`--no-cache` now reaches the fleet sources, and says so when it cannot help.**
The flag was read straight off the command line by `pacto fleet`, `pacto tui` and
`pacto impact`, so setting it through `PACTO_NO_CACHE` or through `no-cache: true`
in the config file disabled the bundle store's cache but left the cached-bundle
fleet source running — the disk cache the caller asked to ignore still contributed
services to the snapshot. The resolved decision is now written back onto the flag,
so all three spellings mean one thing.

And a bundle store with no cache to disable is now an error rather than a silent
no-op. Proceeding quietly ran the whole command against the very cache the caller
asked it to ignore, and reported success.
