---
"@pacto/core": patch
---

Fix the docs version selector so it opens on click. After the previous fix it no
longer opened on hover (intended) but a `:focus-within` rule out-ranked the open
class on click, so the dropdown stayed collapsed. Gated the hover/focus suppress
rules with `:not(.md-version--open)` and raised the open rule's specificity.
Docs-only; this core patch is the release that redeploys pacto.run/latest.
