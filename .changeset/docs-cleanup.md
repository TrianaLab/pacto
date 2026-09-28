---
"@pacto/core": patch
---

Rebuild the documentation around what a reader is trying to do, and cut it in half.

The site used to be organised by subsystem, so finding out what Pacto does meant
reading about how it is built. Twelve top-level navigation sections are now
eight: the homepage, the changelog and the six a reader moves through in order —
**Try it**, **Get started**, **Guides**, **How Pacto decides**, **Reference**,
**Integrations**. Concepts and
the operational graph merged into How Pacto decides, patterns moved under
Guides, examples split across the two entry sections and Project internals left
the site altogether.

Someone who lands on the homepage can open the live dashboard demo without
installing anything, and the quickstart that follows reaches a validated
contract, a breaking change and the list of affected consumers entirely on a
local filesystem. Publishing to a registry moved to the end of that page as an
optional step, because it was the first thing standing between a new reader and
a result. Committing the contract to a repository is now its own short page, CI
integration, rather than a section buried in installation.

**Twenty-eight pages are gone.** Most of what they said lives on the page that
already owned the subject; nothing was silently dropped:

- Seven concept and graph pages — concepts, concept boundaries, collectors,
  observation sources, fleet queries, fleet sources and impact surfaces — are
  now sections of [the model](https://pacto.run/latest/model/), [the
  operational graph](https://pacto.run/latest/operational-graph/), [impact
  analysis](https://pacto.run/latest/impact/) and [fleet
  tools](https://pacto.run/latest/fleet-tools/).
- Six pattern pages are one page, [composition
  patterns](https://pacto.run/latest/patterns/), and the developer and
  platform-engineer guides no longer restate those patterns.
- Three evidence pages — the OCI storage layout, the protocol and the security
  model — are one page, [evidence](https://pacto.run/latest/evidence/).
- Three MCP pages — agent capabilities, authoring tools and catalog discovery —
  are folded into [AI assistants
  (MCP)](https://pacto.run/latest/mcp-integration/), and the per-tool argument
  tables moved to the [CLI
  reference](https://pacto.run/latest/cli-reference/) alongside the commands.
- The separate agent walkthrough is folded back into the [guided
  tour](https://pacto.run/latest/examples/demo-tour/), which now covers both
  ways through the demo in one pass.
- The architecture, tooling-architecture, release and testing pages were never
  for site readers. They are contributor documentation and now sit in
  `ARCHITECTURE.md`, `CONTRIBUTING.md` and `release/README.md` in the
  repository, next to the code they describe.
- The dashboard-architecture and compliance-scenario pages are **removed**, not
  moved. `ARCHITECTURE.md` carries one short dashboard section, not the
  fourteen the old page had, and the scenario-to-proof map is gone.

The corpus falls from 71 pages to 47 and from 101,000 words to 57,000 — **-43%
measured over everything a page contains, -47% measured over running prose
alone.** The surviving pages are shorter as well, because most of what came out
was the same thing said on three pages.

This repository has no `mkdocs-redirects`, so **every one of those twenty-eight
URLs is permanently dead**, as is any bookmark to a heading that moved onto a
different page. Readiness is the one most likely to be linked: it was a section
of the contract reference and is now [a page of its
own](https://pacto.run/latest/contract-reference/readiness/), so
`contract-reference/#readiness` no longer resolves to anything. If you have
linked to a Pacto documentation page from a runbook or a wiki, this release is
the one that breaks it. `mike` keeps prior versions live at their own URL
prefixes, which is the only mitigation there is: every version deploys under
its own number, so a bookmark to `pacto.run/latest/collectors/` still resolves
at `pacto.run/3.3.3/collectors/`. The list above is the map from what was
removed to the page that now carries it.

**Three claims the documentation used to make are now rejected by the linter.**
Vale gained tokens for "policy enforcement" and "enforcement layer" — Pacto
checks policy, it does not act on a live system, and the layer that does the
checking is named "Policy checks" — and for "blast radius", which is borrowed
jargon for something the tools report precisely as the consumers a change
affects. Three more tokens catch prose habits rather than claims: "leverage" is
always "use" here, "load-bearing" never named what it held up, and an opening
that announces the page instead of starting it is now a lint error. The
disclaimers that say what Pacto is *not* are deliberately still allowed to use
the words they disclaim, and the finding codes consumers branch on are not
claims either, so no token matches them.
