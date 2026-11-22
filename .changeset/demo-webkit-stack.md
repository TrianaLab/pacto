---
"@pacto/core": patch
---

The in-browser dashboard demo loads in Safari and every iOS browser again: the inferred-record and cluster-id patterns bound their long lengths in code, so compiling them no longer overflows a WebKit worker's call stack.
