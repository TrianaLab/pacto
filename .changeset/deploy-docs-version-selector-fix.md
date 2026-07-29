---
"@pacto/core": patch
---

Deploy the docs version-selector fix to the live site: the mike version dropdown
now opens on click, not hover, so it no longer pops over the nav tabs and swallows
their clicks. Docs-only change (PR #286); no functional change to the engine, CLI,
or dashboard. This core patch is the release that redeploys pacto.run/latest.
