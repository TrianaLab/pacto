"""MkDocs hook: `mkdocs serve` adds no live-reload script to a page with a CSP.

`mkdocs serve` appends an inline <script> to every HTML page it serves. The
dashboard demo that `make docs` stages at /demo/ carries a Content-Security-Policy
that allows no inline script, so the browser blocked that script and logged a CSP
error on every load of the demo. Live reload cannot run in such a page anyway, so
serve leaves it out of any page that declares a policy.

Serve-only: `mkdocs build` injects nothing, so the published site is unchanged.
"""
from __future__ import annotations

from mkdocs.livereload import LiveReloadServer

_CSP = b'http-equiv="Content-Security-Policy"'


def on_startup(command, dirty):
    # ponytail: patches a private mkdocs method; if it is ever renamed the hook does
    # nothing and the console error comes back, nothing breaks.
    inject = getattr(LiveReloadServer, "_inject_js_into_html", None)
    if command != "serve" or inject is None or getattr(inject, "_skips_csp", False):
        return

    def inject_unless_csp(self, content, epoch):
        return content if _CSP in content else inject(self, content, epoch)

    inject_unless_csp._skips_csp = True
    LiveReloadServer._inject_js_into_html = inject_unless_csp
