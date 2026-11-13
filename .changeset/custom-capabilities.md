---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

Provisioning keys can be scoped to provider repositories with
`allowedProviderRepos` in the structured trust config. The Evidence Server
refuses a provisioning report whose declared configuration or dependency ref
names a repository outside the scope with HTTP 403 and the code
`provider_not_allowed`, and it logs a warning at startup for every provisioning
key with no scope. Older binaries reject a trust config that uses the field.
