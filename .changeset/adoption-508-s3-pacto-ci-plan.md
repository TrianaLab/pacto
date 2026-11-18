---
"@pacto/core": minor
---

`pacto ci [root]` plans every service of a repository in one CI step. It finds
every pacto.yaml, compares each service's content with its last published
version in the registry, and prints one row per service: whether it changed,
the version it would publish, the classification and the impact on the
consumers the fleet source flags name. Each detected value (CI platform,
registry, base, consumer sources) prints its source. The plan also goes to the
GitHub Actions job summary or to pacto-plan.md on GitLab CI, and
`--output-format json` prints it as one JSON document. It exits like
`pacto impact`: 0 clear, 1 blocked, 2 unverified. The run only plans: it reads
the registry and writes nothing, requests no OIDC token and never publishes a
run record (`--stats-file` still writes one file per impacted service).

A directory pattern in the repository root's `.pactoignore` now also hides
that folder from discovery, in `pacto ci`, `pacto fleet --local` and
`pacto dashboard --local`. `pacto-plan.md` joins the default packaging ignores.

Every command now answers one JSON document under `--output-format json`,
failures included: a command that fails before writing its result prints
`{"exit":{"code":...,"reason":...}}`, and `pacto impact`, `pacto validate`,
`pacto publish` and `pacto init` carry the exit inside their result.
