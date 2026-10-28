---
"@pacto/core": patch
---

Update the dashboard frontend's dependencies. The bundle the `pacto` binary and
the dashboard image serve moves to Mermaid 12.2, Cytoscape 3.34.4,
cytoscape-dagre 4.0.1, Marked 18.1 and Svelte 5.57. Mermaid 12 lays diagrams out
with ELK and draws them in its new `neo` look by default, so bundle docs keep the
dagre layout and classic look they had before.

The dashboard's accessible names now begin with the visible text of the control
(the navbar search, the graph's Reset layout button and the change count in
delivery history), as WCAG 2.5.3 asks and axe-core 4.14 checks.

Build and test tooling moves to Playwright 1.64, axe-core 4.14, jsdom 30,
svelte-check 4.7, the Svelte Vite plugin 7.3 and mermaid-cli 12. TypeScript stays
on 5.9: svelte-check and openapi-typescript do not support TypeScript 7 yet.
