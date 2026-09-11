---
"@pacto/core": patch
---

Give the dashboard a real document outline: a collapsible section's title is now
a heading, not just a button.

Every accordion on a service page — Overview, Interfaces, Dependencies,
Configurations, Policies, Readiness and the rest — rendered its title as a bare
`<button>`. Visually that reads as a section title; to a screen reader it was a
control with no structural meaning, so the page went straight from its `<h1>` to
the `<h3>`s buried inside a section body. Users who navigate by heading got a
flat list of subsection names with nothing to say which section each belonged
to, and the skipped level is a WCAG 1.3.1 failure. The toggle is now wrapped in
an `<h2>`, the WAI-ARIA accordion pattern, so the outline reads h1 → section →
subsection. Nothing moves on screen.

Three heading levels that were only legal by accident are corrected with it:
"Skills" and "Secret Keys" were `<h4>`s that read as valid only when some earlier
section happened to supply the missing `<h3>`, and the empty services list titled
itself `<h3>` directly under the page `<h1>`. A service page that fails to load —
"Service not found", a failed remote resolve — now titles itself with an `<h1>`
rather than leaving the page with no top-level heading at all.

The route sweep that should have caught these was auditing an error state: it
reached the non-Fleet views by telling the browser the host had no fleet, but the
host it said that to answers no contract-view request, so every page under audit
was a failed fetch. It now runs against a real `pacto doc --format html` export,
which is the only place those views are served.
