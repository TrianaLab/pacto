"""MkDocs hook: build-time assembly for the monorepo docs site.

Done at build time so a plain `mkdocs build --strict`, `mkdocs serve` and
`mike deploy` all work with no extra step and no files copied into the tracked
tree. Two things are assembled:

1. First-party integration docs. Discovery is manifest-driven: every
   `integrations/*/integration.yaml` with a `documentation.source` contributes
   its Markdown pages under the site path `integrations/<id>/<page>.md`. A future
   integration is picked up automatically -- the only root change needed is
   adding its pages to the nav in mkdocs.yml.
   - Authored pages (top of the integration's docs dir) get a correct GitHub edit
     link pointing back to their real source path.
   - Generated pages (the `generated/` subdir) are injected without an edit link --
     they must be regenerated, never hand-edited.
   - Files whose basename starts with `_` are snippet fragments (included via
     pymdownx.snippets), not standalone pages, so they are skipped.

2. The `changelog.md` page, assembled from the Changesets-generated
   `release/units/*/CHANGELOG.md` files (one section per release unit, core
   first). It is injected as virtual content -- nothing is written to the tree --
   so there is no drift and it always reflects the current release history. Before
   the first release it shows a placeholder.
"""
from __future__ import annotations

import glob
import json
import os
import re

import yaml
from mkdocs.structure.files import File


def _repo_root(config) -> str:
    return os.path.dirname(os.path.abspath(config["config_file_path"]))


def _integration_docs(config):
    """Yield (integration_id, docs_dir_abs) for each discovered integration."""
    root = _repo_root(config)
    for manifest_path in sorted(glob.glob(os.path.join(root, "integrations", "*", "integration.yaml"))):
        with open(manifest_path, encoding="utf-8") as fh:
            manifest = yaml.safe_load(fh)
        if not manifest:
            continue
        integ_id = manifest.get("id") or os.path.basename(os.path.dirname(manifest_path))
        source = (manifest.get("documentation") or {}).get("source")
        if not source:
            continue
        docs_dir = os.path.join(root, source)
        if os.path.isdir(docs_dir):
            yield integ_id, docs_dir


def _pages(docs_dir: str):
    """Yield (abs_path, basename, is_generated) for each injectable Markdown page."""
    for path in sorted(glob.glob(os.path.join(docs_dir, "*.md"))):
        base = os.path.basename(path)
        if not base.startswith("_"):
            yield path, base, False
    for path in sorted(glob.glob(os.path.join(docs_dir, "generated", "*.md"))):
        base = os.path.basename(path)
        if not base.startswith("_"):
            yield path, base, True


_FENCE = re.compile(r"^\s*(```|~~~)")
_HEADING = re.compile(r"^#{1,6}\s")

_CHANGELOG_INTRO = (
    "Version history for every Pacto release unit, generated from "
    "[Changesets](https://github.com/changesets/changesets). The core group "
    "(engine, CLI and dashboard) and the Kubernetes integration are versioned "
    "independently, so each release unit has its own section below."
)


def _demote_headings(md: str) -> str:
    """Add one level to every ATX heading outside fenced code blocks, so an
    included document nests cleanly under the page's own H1."""
    out, in_fence = [], False
    for line in md.splitlines():
        if _FENCE.match(line):
            in_fence = not in_fence
        elif not in_fence and _HEADING.match(line):
            line = "#" + line
        out.append(line)
    return "\n".join(out)


def _changelog_markdown(config) -> str:
    """Assemble the Changelog page from every release unit's CHANGELOG.md."""
    root = _repo_root(config)
    unit_dirs = glob.glob(os.path.join(root, "release", "units", "*"))
    # core first, then the rest alphabetically by directory name.
    unit_dirs.sort(key=lambda d: (os.path.basename(d) != "pacto-core", os.path.basename(d)))

    sections = []
    for unit_dir in unit_dirs:
        changelog = os.path.join(unit_dir, "CHANGELOG.md")
        if not os.path.isfile(changelog):
            continue
        display = os.path.basename(unit_dir)
        pkg = os.path.join(unit_dir, "package.json")
        if os.path.isfile(pkg):
            with open(pkg, encoding="utf-8") as fh:
                display = json.load(fh).get("name", display)
        with open(changelog, encoding="utf-8") as fh:
            lines = fh.read().strip().splitlines()
        # Drop the Changesets top-level "# <package>" H1; we add our own heading.
        if lines and lines[0].startswith("# "):
            lines = lines[1:]
        body = _demote_headings("\n".join(lines).strip())
        sections.append(f"## {display}\n\n{body}".rstrip())

    if not sections:
        return (
            f"# Changelog\n\n{_CHANGELOG_INTRO}\n\n"
            "No release has been published yet. The per-unit history appears here "
            "after the first `npm run release:version`.\n"
        )
    return f"# Changelog\n\n{_CHANGELOG_INTRO}\n\n" + "\n\n".join(sections) + "\n"


def on_files(files, config):
    root = _repo_root(config)
    files.append(File.generated(config, "changelog.md", content=_changelog_markdown(config)))
    for integ_id, docs_dir in _integration_docs(config):
        rel_docs_dir = os.path.relpath(docs_dir, root)  # e.g. integrations/kubernetes/docs
        for abs_path, base, is_generated in _pages(docs_dir):
            src_uri = f"integrations/{integ_id}/{base}"
            f = File.generated(config, src_uri, abs_src_path=abs_path)
            if not is_generated:
                # Correct the edit link back to the real source. The global edit_uri
                # is `edit/main/docs/`; the leading `../` cancels the `docs/` segment
                # so the link resolves to the integration's own docs dir.
                f.edit_uri = f"../{rel_docs_dir}/{base}"
            files.append(f)
    return files


def on_serve(server, config, builder):
    """Live-reload when an integration's source docs change (they live outside docs_dir)."""
    for _integ_id, docs_dir in _integration_docs(config):
        server.watch(docs_dir)
    return server
