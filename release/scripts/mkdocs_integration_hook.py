"""MkDocs hook that assembles first-party integration docs into the root site.

This is the "pre-build assembly" for the monorepo, done at build time so a plain
`mkdocs build --strict`, `mkdocs serve` and `mike deploy` all work with no extra
step and no files copied into the tracked tree.

Discovery is manifest-driven: every `integrations/*/integration.yaml` with a
`documentation.source` contributes its Markdown pages under the site path
`integrations/<id>/<page>.md`. A future integration is picked up automatically --
the only root change needed is adding its pages to the nav in mkdocs.yml.

- Authored pages (top of the integration's docs dir) get a correct GitHub edit
  link pointing back to their real source path.
- Generated pages (the `generated/` subdir) are injected without an edit link --
  they must be regenerated, never hand-edited.
- Files whose basename starts with `_` are snippet fragments (included via
  pymdownx.snippets), not standalone pages, so they are skipped.
"""
from __future__ import annotations

import glob
import os

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


def on_files(files, config):
    root = _repo_root(config)
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
