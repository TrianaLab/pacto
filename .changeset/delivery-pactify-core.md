---
"@pacto/core": minor
---

`pacto init` now pactifies an existing service instead of only scaffolding a
new folder. `pacto init [dir]` defaults to the current folder. An empty or new
folder still gets the blank contract. A folder that already holds a service
gets a contract read from its Dockerfile and Kubernetes manifests: the name,
version and registry come from the workload and its image, the owner from
`--owner` or a `team` label, the interface from an OpenAPI file, and the
`publish` section builds the image and ships the manifests. Services the
workload's environment points at are listed as dependencies, left commented
out until you set the versions you accept. With `--fleet <dir>` it also lists
the services whose contracts declare this one. Anything it cannot tell is
reported as Unknown, never guessed. When the image is at `<registry>/<name>`
the contract bundle goes to `<registry>/contracts/<name>`, so the two never
share a tag. A file holding a Secret's values is never shipped with the
manifests; the report says to supply it another way.

`--delivery` also writes the cluster pieces: the Pacto resource, which ships
inside every version, and for each environment (`--env`, default test and
prod) the Flux OCIRepository new versions come from, the one Pacto points at
the version to run, its Kustomization and the Stage. `--plan` prints exactly
what would be added and writes nothing. `pacto init` only adds files and
missing `.pactoignore` lines. A file that is already there is left as it is,
so running it twice is safe; a kept environment file whose Stage no longer
takes candidates from where `--env` puts it is reported. Running `pacto init` in an existing folder no
longer fails.

Agents get the same through `pacto_create` with `pactify: true` (plus
`delivery`, `registry`, `environments` and `fleet_roots`). `dry_run` plans.
