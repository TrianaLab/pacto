---
"@pacto/core": patch
---

Restructure the documentation as one information system.

An editorial and information-architecture pass over the whole surface: the nav
is ordered as a reader's path, each concept has one canonical home, duplicated
worked examples and repeated statements of the thesis are gone, and development
history is out of the product pages. Three pages were split out of pages that
were carrying two subjects — the Pacto model, dashboard architecture and
observation sources. The published surface loses about 1,500 words while staying
roughly the same length in lines: the prose is tighter and the split-out pages
add the structure back. No technical claim was dropped, and no file changed path,
so every existing URL still resolves.

Docs-only; no functional change to the engine, CLI or dashboard. This core patch
is the release that redeploys the site, which `docs.yml` deliberately does not do.
