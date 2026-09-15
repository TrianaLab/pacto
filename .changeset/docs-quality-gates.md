---
"@pacto/core": patch
---

Gate the documentation deterministically, and cut the corpus to fit the gate.

`make docs-lint` is a new leg of `make docs-check`, so CI and a laptop run the
identical command. Three tools, three concerns, no overlap — a rule has exactly
one home:

- **markdownlint** (`.markdownlint-cli2.jsonc`) owns Markdown syntax.
- **Vale** (`.vale.ini`, `.vale/styles/Pacto/`) owns prose: marketing language,
  buzzwords, filler, weasel words, unsupported claims and the terminology this
  project has already settled on.
- **`release/scripts/docs_lint.py`** owns structure: 1200 prose words per page,
  250 per section, headings no deeper than `####`.

No model is involved in any of it. The same input always produces the same
verdict, which is the point — a documentation rule nobody can forget is worth
more than one every contributor is asked to remember. Both linters are pinned
by version and neither is vendored; a missing tool is a hard failure, never a
silent skip.

The word budgets count running prose only. Front matter, HTML comments, fenced
code (including mermaid), tables and lists are exempt by design: what makes a
section unreadable is unbroken running text, and a table or a bulleted
reference list is already broken up. Counting them punished pages for being
reference pages. `docs_lint.py` self-tests the counter on every run, because a
counter that quietly stops counting reads exactly like a corpus in good shape.

Scope lives in one place — `scope()` hands both external tools an explicit file
list rather than a glob, covering the published site, each integration's
hand-written docs and the root Markdown a reader meets on GitHub. Generated
pages are out: `docs/cli-reference.md` and the per-integration `generated/`
trees are already drift-gated against their real sources, and prose rules would
only fight the generator.

A fourth leg resolves the links and anchors in the root Markdown files.
`mkdocs build --strict` already validates everything inside `docs/`, and the
integration hook copies `integrations/*/docs` into it, so those were covered;
the six files at the repository root were outside `docs_dir` and gated by
nothing.

**Thirteen pages were split into thirty-one to fit the page budget** —
architecture, concepts, contract-reference/sections, developers,
evidence-protocol, impact, mcp-integration, operational-graph,
platform-engineers, the demo tour, the release and testing pages under
`maintainers/`, and the Kubernetes installation page. Each split promoted its
extracted sections to a new page, added the `mkdocs.yml` nav entry and rewrote
every inbound link. This repository has no
`mkdocs-redirects`, so **a heading that moved to a new page is a permanently
dead deep link** for anyone who bookmarked it; `mike` keeps prior versions live
at their own URL prefixes, and each origin page names where its content went.
Everything else came off by deleting prose, not by moving it.
