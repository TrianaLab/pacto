---
"@pacto/core": patch
---

Stop the OCI bundle cache from reporting a failure when a concurrent pull of the
same reference commits first.

A cache entry is built in a staging directory and swapped into place with one
rename, so a reader sees a whole entry or none. Two pulls of the same reference
aim at the same destination, though, and nothing serializes them — the second
pull is usually a second `pacto` process sharing one cache directory. Both clear
the destination, one rename lands, and the loser's fails onto the winner's fresh
entry with `directory not empty`.

Nothing was wrong: the entry the losing pull set out to write was on disk, put
there whole by the other one. But the loser reported the rename error, and the
caller logs it, so a healthy parallel pull told the user `could not cache the
pulled bundle`. The commit now asks the filesystem what is actually there and
treats a lost race as the success it was.
