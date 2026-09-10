---
"@pacto/core": patch
---

Fix a bundle scan reporting no services because one directory refused to open.

Both filesystem-walking fleet sources aborted the whole walk on the first read
error and marked themselves unavailable. For `--local` that meant `pacto tui`,
`pacto fleet` and `pacto impact` answered "0 services" from a home directory:
the walk reaches TCC-guarded paths like `~/Library/Accounts` within
milliseconds, long before it reaches any contract, and gave up there. The same
scan now finds 64 services on the machine this was found on. For `--cache` the
same abort emptied the entire offline baseline when a single cache entry was
unreadable, which is what one `sudo pacto pull` leaves behind.

A refused directory is a gap, not a verdict. Both sources now record it as a
`SOURCE_PARTIAL` limitation naming the path relative to the scan root, then step
over it, exactly as an unparseable bundle already was. Past ten of them the rest
are summarised as a count, so a home directory's hundred-odd privacy directories
cannot bury the gaps a reader can act on. The walk root itself remains the one
fatal case: nothing was read, so there is no partial answer to report.
