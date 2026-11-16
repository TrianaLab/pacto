---
"@pacto/core": patch
---

An HTTP-served `pacto mcp --fleet` no longer scans the bundles under the
folder it was started in by default: over HTTP every caller reads the snapshot
as the server's process, so it reads local bundles only from the `--local`
roots you name. Over stdio `--local` still defaults to `.`.
