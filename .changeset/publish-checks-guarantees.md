---
"@pacto/core": patch
---

`pacto publish` and `pacto push` check what each dependency `requires` from
its provider, as `pacto validate` does. A provider that does not meet a
required dependency's guarantee (`GUARANTEE_UNMET`) refuses the publish, and
`pacto publish --plan` shows it as the failed `validation` check. An unmet
guarantee of an optional dependency and a provider Pacto cannot fetch
(`GUARANTEE_UNVERIFIED`) stay warnings: the publish `validation` check names
them and `pacto push` prints them. `pacto pack` stays offline and does not
fetch providers.
