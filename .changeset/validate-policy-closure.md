---
"@pacto/core": minor
---

`pacto validate --closure` checks every contract in the dependency closure
against the policies the root contract resolves. A dependency that cannot be
resolved fails the run, and each finding names the dependency it came from.
