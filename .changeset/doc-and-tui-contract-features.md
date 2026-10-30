---
"@pacto/core": minor
---

The `pacto doc` HTML page and its Markdown output show each interface's
lifecycle, guarantees and compatibility epoch, what each dependency requires
and whether its provider meets it, how each validation stage ended and an
Environments section with each environment's check and its findings located
in the values file. A requirement reads met or refused only when Pacto checked
the provider's contract, and Not verified with the reason otherwise. The TUI
explain view lists environments, interface visibility and dependency
compatibility, as `pacto explain` does. `pacto explain`, the TUI explain view
and the MCP contract summary list the guarantees each interface offers, its
compatibility epoch and what each dependency requires. The dashboard's "On
this page" rail scrolls within itself when a page has more sections than fit
the screen, keeping the current section in view.
