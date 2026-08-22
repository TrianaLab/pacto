"""MkDocs hook: the site serves its own diagram runtime.

Material for MkDocs renders `mermaid` fences in the browser and, finding no
`mermaid` global, lazily fetches `https://unpkg.com/mermaid@11/dist/mermaid.min.js`.
That puts an unpinned third-party script on the critical path of every page a
reader opens: it drifts anywhere inside the major, it returns nothing offline or
behind a proxy that does not reach unpkg, and no lockfile in this repository
governs the bytes it hands back.

This hook stages the runtime the repository already pins -- the `mermaid`
dependency of pkg/dashboard/frontend, resolved by its package-lock.json -- into
the built site as `javascripts/mermaid.min.js`. mkdocs.yml loads it before
Material mounts the diagrams, so the theme's `typeof mermaid === "undefined"`
test is false and the CDN branch is never taken.

Nothing is copied into the tracked tree: File.generated streams the file from
node_modules straight into site_dir, exactly as the integration hook does for
pages.

A missing runtime is a hard error. Falling back to the CDN would silently
restore the dependency this exists to remove, and a silent restore is worse than
the dependency.
"""
from __future__ import annotations

import os

from mkdocs.exceptions import PluginError
from mkdocs.structure.files import File

RUNTIME_SRC = os.path.join(
    "pkg", "dashboard", "frontend", "node_modules", "mermaid", "dist", "mermaid.min.js"
)
RUNTIME_URI = "javascripts/mermaid.min.js"


def on_files(files, config):
    root = os.path.dirname(os.path.abspath(config["config_file_path"]))
    src = os.path.join(root, RUNTIME_SRC)
    if not os.path.isfile(src):
        raise PluginError(
            f"diagram runtime missing: {RUNTIME_SRC}\n"
            "The docs site serves Mermaid itself rather than fetching it from a CDN, "
            "so the frontend's pinned dependency has to be installed first:\n"
            "  make docs-build   (or: cd pkg/dashboard/frontend && npm ci)"
        )
    files.append(File.generated(config, RUNTIME_URI, abs_src_path=src))
    return files
