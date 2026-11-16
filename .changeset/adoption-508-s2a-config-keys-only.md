---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

pacto init and pacto_create no longer copy environment values into the
inferred configuration schema as defaults; sensitive keys are marked
writeOnly, cluster drafts read sensitive literals from a Secret and every
schema summary (dashboard, fleet, consume snippet, operator status) shows
writeOnly keys as (sensitive). Schema diffs never print the value of a
sensitive key. Existing configuration/schema.json files are left as they are:
remove any default that holds a secret and republish.
