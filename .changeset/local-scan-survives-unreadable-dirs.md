---
"@pacto/core": patch
---

Fix a local bundle scan reporting no services because one directory refused to open.

`--local` walks a directory tree, and any read error aborted the whole walk and
marked the source unavailable. On macOS that meant `pacto tui`, `pacto fleet` and
`pacto impact` answered "0 services" from a home directory: the walk reaches
TCC-guarded paths like `~/Library/Accounts` within milliseconds, long before it
reaches any contract, and gave up there. The same scan now finds 64 services on
the machine this was found on.

A refused directory is a gap, not a verdict. It is recorded as a `SOURCE_PARTIAL`
limitation naming the path relative to the scan root, then stepped over, exactly
as an unparseable bundle already was. Past ten of them the rest are summarised as
a count, so a home directory's hundred-odd privacy directories cannot bury the
gaps a reader can act on.
