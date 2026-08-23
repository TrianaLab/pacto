#!/usr/bin/env python3
"""Blocking Mermaid syntax gate for the Pacto docs (`make mermaid-check`).

`mkdocs build --strict` does NOT parse Mermaid: pymdownx.superfences only wraps
a ```mermaid fence in a <div class="mermaid"> for CLIENT-side rendering, so a
diagram with a syntax error ships silently and only breaks in the reader's
browser. docs_check.py validates yaml fences but never touches mermaid.

This gate closes that hole: it extracts every fenced ```mermaid block from the
site Markdown (docs/ + integrations/) and validates each by shelling out to the
mermaid CLI (mmdc), which parses+renders the diagram exactly as the browser
would. Any block that fails to render is a hard error.

Runner resolution (first that works):
  * npx --yes -p @mermaid-js/mermaid-cli mmdc   (no local install needed)
  * an `mmdc` already on PATH

If neither is available the gate cannot render, so it degrades to a
block-extraction self-test and reports that the full mmdc run is deferred to CI
(which provides Node). Run the self-test directly with `--self-test`.

Exit code is non-zero if any block fails to render.
"""
from __future__ import annotations

import glob
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))

# Mirror docs_check.py's FENCE_RE (yaml -> mermaid): capture the fence indent so
# nested/indented fences can be de-indented before handing the body to mmdc.
FENCE_RE = re.compile(r"^([ \t]*)(?:```|~~~)mermaid[^\n]*\n(.*?)^\1(?:```|~~~)", re.M | re.S)


def extract_mermaid(text: str) -> list[str]:
    blocks = []
    for m in FENCE_RE.finditer(text):
        indent, body = m.group(1), m.group(2)
        if indent:  # de-indent nested fences (same as docs_check.fenced_yaml_blocks)
            body = "\n".join(line[len(indent):] if line.startswith(indent) else line
                             for line in body.splitlines())
        blocks.append(body)
    return blocks


def markdown_files() -> list[str]:
    """Every site Markdown file: docs/ (minus vendored superpowers) + integrations/."""
    files = []
    for p in glob.glob(os.path.join(REPO_ROOT, "docs", "**", "*.md"), recursive=True):
        if os.sep + "superpowers" + os.sep in p:
            continue
        files.append(p)
    files += glob.glob(os.path.join(REPO_ROOT, "integrations", "**", "*.md"), recursive=True)
    return sorted(set(files))


NPX_MERMAID = ["npx", "--yes", "-p", "@mermaid-js/mermaid-cli"]


def mmdc_command() -> list[str] | None:
    if shutil.which("npx"):
        return NPX_MERMAID + ["mmdc"]
    if shutil.which("mmdc"):
        return ["mmdc"]
    return None


def ensure_chrome() -> None:
    """Install the headless Chrome mmdc renders with, if it isn't there yet.

    npx puts mermaid-cli (and its peer puppeteer) under ~/.npm/_npx, but
    puppeteer's postinstall puts the browser under ~/.cache/puppeteer — a
    different directory. CI caches ~/.npm only, so an npm cache hit skips the
    install *and* its browser download, and mmdc then dies with "Could not find
    chrome-headless-shell". Installing from the same -p tree keeps the browser
    matched to that puppeteer; it's a fast no-op once downloaded.
    """
    subprocess.run(NPX_MERMAID + ["puppeteer", "browsers", "install",
                                  "chrome-headless-shell"], check=False)


def validate_block(cmd: list[str], puppeteer_cfg: str, body: str) -> tuple[bool, str]:
    """Render one mermaid block; True on success, else (False, error text)."""
    fd, src = tempfile.mkstemp(suffix=".mmd", prefix="mermaid-")
    with os.fdopen(fd, "w", encoding="utf-8") as fh:
        fh.write(body)
    out = src + ".svg"
    try:
        proc = subprocess.run(
            cmd + ["-p", puppeteer_cfg, "-i", src, "-o", out],
            capture_output=True, text=True,
        )
        if proc.returncode == 0:
            return True, ""
        return False, (proc.stderr.strip() or proc.stdout.strip())
    finally:
        os.unlink(src)
        if os.path.exists(out):
            os.unlink(out)


def self_test() -> int:
    """Prove the block extractor before we trust it to gate the docs."""
    sample = (
        "# Title\n\n"
        "```mermaid\ngraph LR\n  A --> B\n```\n\n"
        "```yaml\nkey: val\n```\n\n"          # non-mermaid fence must be ignored
        "  ```mermaid\n  flowchart TD\n    X --> Y\n  ```\n"  # indented fence
    )
    blocks = extract_mermaid(sample)
    assert len(blocks) == 2, f"expected 2 mermaid blocks, got {len(blocks)}: {blocks!r}"
    assert "A --> B" in blocks[0], blocks[0]
    # indented fence must be de-indented (no leading 2-space on its body lines)
    assert "flowchart TD" in blocks[1] and "\n  flowchart" not in "\n" + blocks[1], blocks[1]
    print("self-test ok: extracted 2 mermaid blocks (yaml ignored, indented de-indented)")
    return 0


def main(argv: list[str]) -> int:
    if "--self-test" in argv:
        return self_test()

    cmd = mmdc_command()
    if cmd is None:
        print("mmdc/npx unavailable in this environment: running block-extraction "
              "self-test only; full mermaid render is deferred to CI (Node present).")
        return self_test()

    if cmd[0] == "npx":
        ensure_chrome()

    # Chromium refuses to run as root without --no-sandbox (the default on CI
    # runners), so hand mmdc a puppeteer config that passes it. Harmless locally.
    pfd, puppeteer_cfg = tempfile.mkstemp(suffix=".json", prefix="puppeteer-")
    with os.fdopen(pfd, "w", encoding="utf-8") as fh:
        json.dump({"args": ["--no-sandbox"]}, fh)

    total = failures = 0
    try:
        for path in markdown_files():
            rel = os.path.relpath(path, REPO_ROOT)
            with open(path, encoding="utf-8") as fh:
                blocks = extract_mermaid(fh.read())
            # ponytail: one mmdc (=one headless render) per block, ~a few seconds
            # each; batch per-file via `mmdc -i file.md` if the gate gets too slow.
            for i, body in enumerate(blocks, 1):
                total += 1
                ok, err = validate_block(cmd, puppeteer_cfg, body)
                if ok:
                    print(f"{rel}:{i} ok")
                else:
                    failures += 1
                    print(f"{rel}:{i} FAILED\n{err}")
    finally:
        os.unlink(puppeteer_cfg)

    print(f"\ncheck_mermaid: {total - failures}/{total} mermaid blocks valid")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
