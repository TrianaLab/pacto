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

An offering answer names who reports on it on the CLI, TUI, MCP and dashboard:
`adapters` lists each adapter slug, producer and signature with the
environments and phases it covers, `conflicting` counts requests whose adapters
disagree and `unattributed` counts requests no adapter reported on. A report
with no slug reads "unnamed adapter". `unresolvedUses` counts the declared
references that may name a provider of the offering but did not resolve, and
above 0 the answer carries `OFFERING_USES_UNRESOLVED` and is `partial`.

Phase tallies count an unsigned Ready apart in `readyUnsigned`, and the CLI
prints "N ready (M unsigned)". The dashboard shows an unsigned Ready as
"Ready (unsigned)" in the info tone, so only a signed Ready reads positive.

Impact's `CONFIG_REF_CONSUMERS_UNRESOLVED` and
`POLICY_REF_CONSUMERS_UNRESOLVED` now count hidden consumers and count each
consumer once instead of once per revision, so their numbers can change.

The demo adds a `security-scanning` offering, provided by `platform-security`,
with a signed and an unsigned report from the `scanner` adapter.
