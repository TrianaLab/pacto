#!/usr/bin/env python3
"""Deterministic documentation quality gate for the Pacto monorepo (`make docs-lint`).

Three tools, three concerns, no overlap -- a rule has exactly one home:

  (a) markdownlint  Markdown SYNTAX      .markdownlint-cli2.jsonc
  (b) Vale          PROSE                .vale.ini + .vale/styles/Pacto/
  (c) budgets       STRUCTURE            the limits below
  (d) links         ROOT CROSS-REFS      what `mkdocs build --strict` cannot see

No model is involved in any of them: the same input always produces the same
verdict, which is the point. `make docs-check` runs this, so CI and a laptop run
the identical command.

This file owns the SCOPE. Both external tools are handed an explicit file list
rather than a glob, so "which files are gated" is answered in one place
(`scope()`) instead of drifting across two config formats.

Neither tool is vendored. markdownlint runs through npx and Vale through
`go run`, both pinned by version, because `make docs-check` already requires
Node (mermaid-check) and Go (it builds ./cmd/pacto). A missing tool is a hard
failure, never a silent skip -- a gate that quietly passes when it could not run
is worse than no gate.

Exit code is non-zero if any check fails.
"""
from __future__ import annotations

import glob
import os
import re
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

# Reused rather than re-derived: integration docs are discovered from each
# integration.yaml (so a future integration is gated with no change here), and
# generated_paths() is already the repository's answer to "what is machine-written".
from docs_check import REPO_ROOT, generated_paths, integration_docs_dirs  # noqa: E402

# Pinned. An unpinned linter turns a green PR red on someone else's release.
MARKDOWNLINT = "markdownlint-cli2@0.18.1"
VALE = "github.com/errata-ai/vale/v3/cmd/vale@v3.9.1"

# Structural budgets, in PARAGRAPH words. Front matter, fenced code, tables and
# lists do not count. That exemption is the rule, not a loophole: what makes a
# long section unreadable is unbroken running text, and a table or a bulleted
# reference list is already broken up. Counting them punished pages for being
# reference pages -- the ten-bullet source list in operational-graph.md scored
# 714 "prose" words while reading as a list.
#
# These are a BUDGET, not a description of the corpus. Fitted to the existing
# distribution they would have been 3000/500, which is the shape of the writing
# rather than a limit on it -- the three longest pages had converged on 2996,
# 2995 and 2986, so the cap was documenting the habit it was meant to break.
# 1200/250 sat below the corpus and cost ~11k words to adopt. The section cap
# does the real work: padding is a restated sentence, and a restated sentence
# always lands inside one section, so 250 bites on all 533 of them rather than
# on a handful of long pages. The page cap only answers "is this secretly three
# pages?" -- 1200 prose words is roughly five to eight sections.
#
# Raising either number is a decision about the documentation, not a fix for a
# failing build. A page that cannot make 1200 is usually two pages: split it, add
# the nav entry and rewrite the inbound anchors. That is not free -- this
# repository has no mkdocs-redirects, so every heading that moves is a dead deep
# link for anyone who bookmarked it. Splitting is the escape hatch; paying that
# price deliberately is the point.
#
# There is deliberately no maximum-paragraphs rule. It was measured and rejected:
# the longest paragraph runs in this corpus are its SHORTEST paragraphs (11
# paragraphs totalling 218 words), so the rule would penalise exactly the writing
# it was supposed to encourage. The word budgets already catch walls of text.
MAX_PAGE_WORDS = 1200
MAX_SECTION_WORDS = 250
MAX_HEADING_DEPTH = 4

# Violations printed per leg before the tail is summarised. A gate that dumps
# hundreds of lines is a gate people stop reading.
CAP = 60

ledger: list[tuple[bool, str, str]] = []


def record(ok: bool, name: str, detail: str = "") -> None:
    ledger.append((ok, name, detail))
    print(f"[{'PASS' if ok else 'FAIL'}] {name}" + (f" -- {detail}" if detail else ""))


def scope() -> list[str]:
    """Every hand-written Markdown file the gate covers, repo-relative and sorted.

    In:  docs/ (the published site), each integration's hand-written docs, and
         the root Markdown a reader meets on GitHub.
    Out: docs/superpowers/ (gitignored working notes, never published), and
         anything generated -- docs/cli-reference.md and the per-integration
         generated/ trees are drift-gated by docs_check.py against their real
         sources, and prose rules would only fight the generator.
    """
    generated = {os.path.normpath(p) for p in generated_paths()}

    def is_generated(rel: str) -> bool:
        return any(rel == g or rel.startswith(g + os.sep) for g in generated)

    files = set()
    for p in glob.glob(os.path.join(REPO_ROOT, "docs", "**", "*.md"), recursive=True):
        rel = os.path.relpath(p, REPO_ROOT)
        if not rel.startswith("docs" + os.sep + "superpowers" + os.sep) and not is_generated(rel):
            files.add(rel)
    for d in integration_docs_dirs():
        for p in glob.glob(os.path.join(d, "*.md")):
            rel = os.path.relpath(p, REPO_ROOT)
            if not is_generated(rel) and not os.path.basename(p).startswith("_"):
                files.add(rel)
    for p in glob.glob(os.path.join(REPO_ROOT, "*.md")):
        files.add(os.path.relpath(p, REPO_ROOT))
    return sorted(files)


# --- structural budgets -----------------------------------------------------

FRONT_MATTER = re.compile(r"\A---\n.*?\n---\n", re.S)
# Captures the fence indent and length so a ```` ```` fence wrapping a ``` fence
# closes on its own marker. Applied repeatedly until stable, which drains nesting.
FENCE = re.compile(r"^(?P<i>[ \t]*)(?P<f>`{3,}|~{3,})[^\n]*\n.*?^(?P=i)(?P=f)[ \t]*$\n?", re.M | re.S)
HEADING = re.compile(r"^(#{1,6}) +(\S.*)$")
LIST_ITEM = re.compile(r"^[ \t]*(?:[-*+]|\d+[.)])[ \t]")


def prose(text: str) -> str:
    """Drop front matter, HTML comments and fenced code: what is left is prose."""
    text = FRONT_MATTER.sub("", text)
    text = re.sub(r"<!--.*?-->", "", text, flags=re.S)
    prev = None
    while prev != text:
        prev, text = text, FENCE.sub("", text)
    return text


def budget_violations(rel: str, raw: str | None = None) -> list[str]:
    if raw is None:
        with open(os.path.join(REPO_ROOT, rel), encoding="utf-8") as fh:
            raw = fh.read()
    text = prose(raw)

    out: list[str] = []
    page = 0
    heading, words = "(before the first heading)", 0
    in_list = False

    def close() -> None:
        if words > MAX_SECTION_WORDS:
            out.append(f"{rel}: section '{heading}' is {words} prose words (max {MAX_SECTION_WORDS})")

    for line in text.split("\n"):
        m = HEADING.match(line)
        if m:
            close()
            depth = len(m.group(1))
            if depth > MAX_HEADING_DEPTH:
                out.append(f"{rel}: heading '{m.group(2)[:50]}' is h{depth} (max h{MAX_HEADING_DEPTH})")
            heading, words, in_list = m.group(2)[:50], 0, False
            continue
        if not line.strip():  # a blank line ends the list block
            in_list = False
            continue
        if LIST_ITEM.match(line):
            in_list = True
        # Tables and lists are structure, not running text; an item's wrapped
        # continuation lines belong to the item, so they go with it.
        if in_list or line.lstrip().startswith("|"):
            continue
        n = len(line.split())
        words += n
        page += n
    close()

    if page > MAX_PAGE_WORDS:
        out.append(f"{rel}: page is {page} prose words (max {MAX_PAGE_WORDS})")
    return out


# --- root Markdown cross-references -----------------------------------------

# `mkdocs build --strict` resolves every link and anchor inside docs_dir, and the
# integration hook copies integrations/*/docs into it, so both are already gated.
# The root Markdown a reader meets on GitHub is outside docs_dir and was gated by
# nothing at all. This closes that one hole; it is not a second link checker.
LINK = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")


def slugs(text: str) -> set[str]:
    """GitHub's heading anchors: lowercased, punctuation dropped, spaces to dashes."""
    return {
        re.sub(r"[^\w\- ]", "", m.group(2).lower()).strip().replace(" ", "-")
        for m in map(HEADING.match, prose(text).split("\n"))
        if m
    }


def link_violations(rel: str) -> list[str]:
    with open(os.path.join(REPO_ROOT, rel), encoding="utf-8") as fh:
        raw = fh.read()
    out: list[str] = []
    for target in LINK.findall(prose(raw)):
        if re.match(r"^(?:[a-z][a-z0-9+.-]*:|//|#\Z)", target):
            continue
        path, _, anchor = target.partition("#")
        dest = os.path.normpath(os.path.join(os.path.dirname(rel), path)) if path else rel
        if not os.path.exists(os.path.join(REPO_ROOT, dest)):
            out.append(f"{rel}: link target does not exist: {target}")
            continue
        if anchor and dest.endswith(".md"):
            with open(os.path.join(REPO_ROOT, dest), encoding="utf-8") as fh:
                if anchor.lower() not in slugs(fh.read()):
                    out.append(f"{rel}: no such heading: {target}")
    return out


def check_links(files: list[str]) -> None:
    bad = [v for rel in files if os.sep not in rel for v in link_violations(rel)]
    record(not bad, "(d) root Markdown links and anchors",
           f"{len(bad)} broken\n" + "\n".join("        " + b for b in bad) if bad else "")


def selftest() -> None:
    """Prove the counter still ignores what it claims to ignore.

    Without this the gate can fail open: a regression in `prose()` or in the
    list/table skip does not raise, it just quietly counts fewer words, and a
    budget nothing can exceed reads exactly like a corpus in good shape.
    The page below is one word of running text and a mountain of everything the
    counter is supposed to skip.
    """
    word = " zz" * (MAX_PAGE_WORDS + 50)
    page = (
        f"---\ntitle:{word}\n---\n"
        "# H1\n\n"
        f"<!-- {word} -->\n\n"
        f"````\n```\n{word}\n```\n````\n\n"
        f"| a | b |\n|---|---|\n| {word} | x |\n\n"
        f"- bullet{word}\n  continued{word}\n"
        f"1. numbered{word}\n\n"
        "counted\n"
    )
    assert budget_violations("x.md", page) == [], "counter is over-counting skipped blocks"
    assert budget_violations("x.md", page + word) != [], "counter no longer sees running text"
    assert budget_violations("x.md", "# H1\n\n##### deep\n") == [
        "x.md: heading 'deep' is h5 (max h4)"
    ], "heading-depth check is not firing"
    # The link leg fails open the same way: a regex that matches nothing reports
    # a clean bill of health.
    assert LINK.findall("see [x](b.md#c)") == ["b.md#c"], "link regex no longer matches"
    # Two headings, not one: a slugger that only sees the last line of a page
    # reports every anchor above it as broken, or none of them as anything.
    assert slugs("# A\n\n## Hello, `World`!\n\nbody\n") == {"a", "hello-world"}, \
        "slugger drifted from GitHub"


def check_budgets(files: list[str]) -> None:
    bad = [v for rel in files for v in budget_violations(rel)]
    record(not bad, "(c) structural budgets (page/section words, heading depth)",
           f"{len(bad)} over budget\n" + "\n".join("        " + b for b in bad) if bad else "")


# --- external linters -------------------------------------------------------

def gate(label: str, cmd: list[str], files: list[str]) -> None:
    try:
        proc = subprocess.run(cmd + files, cwd=REPO_ROOT, capture_output=True, text=True)
    except FileNotFoundError:
        record(False, label, f"{cmd[0]} not found -- it is required, not optional")
        return
    output = (proc.stdout + proc.stderr).strip()
    if proc.returncode == 0:
        record(True, label)
        return
    # Keep the tool's own file:line:rule lines; drop its banner and summary noise.
    lines = [ln.strip() for ln in output.split("\n") if re.match(r"^[\w./-]+[:(]\d+", ln.strip())]
    lines = lines or output.split("\n")
    shown = lines[:CAP]
    detail = f"{len(lines)} violations\n" + "\n".join("        " + ln for ln in shown)
    if len(lines) > CAP:
        detail += f"\n        ... and {len(lines) - CAP} more (rerun `{cmd[0]}` directly for the full list)"
    record(False, label, detail)


def main() -> int:
    selftest()
    files = scope()
    print(f"docs-lint: {len(files)} hand-written Markdown files in scope\n")

    gate("(a) markdownlint (Markdown syntax)",
         ["npx", "--yes", MARKDOWNLINT], files)
    # No --no-exit: that flag makes Vale exit 0 even when it has alerts, which
    # would turn this leg into a gate that cannot fail. MinAlertLevel is set in
    # .vale.ini, so the threshold lives with the rules.
    gate("(b) Vale (prose: marketing, filler, claims, terminology)",
         ["go", "run", VALE, "--output=line"], files)
    check_budgets(files)
    check_links(files)

    failed = [n for ok, n, _ in ledger if not ok]
    print("\n" + "=" * 60)
    print(f"docs-lint: {len(ledger) - len(failed)}/{len(ledger)} checks passed")
    if failed:
        print("FAILED: " + "; ".join(failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
