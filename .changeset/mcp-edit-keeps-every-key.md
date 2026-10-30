---
"@pacto/core": patch
---

`pacto_edit` keeps every top-level field of the contract it rewrites. It used
to drop `publish`, `checks` and any field it did not list.
