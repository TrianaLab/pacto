[![CI](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml/badge.svg)](https://github.com/TrianaLab/pacto/actions/workflows/ci.yml) [![Docs](https://img.shields.io/badge/docs-pacto.run-blue)](https://pacto.run) [![PkgGoDev](https://pkg.go.dev/badge/github.com/trianalab/pacto/v4)](https://pkg.go.dev/github.com/trianalab/pacto/v4) [![codecov](https://codecov.io/github/TrianaLab/pacto/graph/badge.svg?token=p3AJpP3BbO)](https://codecov.io/github/TrianaLab/pacto) [![GitHub Release](https://img.shields.io/github/v/release/TrianaLab/pacto)](https://github.com/TrianaLab/pacto/releases/latest) [![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/pacto-operator)](https://artifacthub.io/packages/search?repo=pacto-operator) [![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Pacto

**Pacto tells you which services a change breaks before it ships, and checks
what is actually running.**

A contract is one YAML file per service: who owns it, what it offers, which
services it calls and which of their versions it accepts. You publish it to
the OCI registry you already run.

**[Documentation](https://pacto.run)** · **[Quickstart](https://pacto.run/latest/quickstart)** · **[Specification](https://pacto.run/latest/contract-reference)** · **[Examples](https://pacto.run/latest/examples)** · **[Live demo](https://pacto.run/latest/demo/#/fleet)**

> **Why Pacto exists:** [MANIFEST.md](MANIFEST.md)

---

## Who breaks before you ship

When `auth` is about to ship 3.0 and both consumer contracts are in the
current folder, `pacto impact` reads the version range each consumer declares:

```console
$ pacto impact ./auth-2.1.0 ./auth-3.0.0 --local .
Impact: auth 2.1.0 -> 3.0.0
Classification: BREAKING
Breaking changes: 1
Potentially breaking changes: 0
Affected consumers (2):
  checkout                     direct     confidence=contractual  compat=incompatible owner=commerce
  reporting                    direct     confidence=contractual  compat=compatible   owner=analytics
```

`pacto diff` works in any pull request and says whether a change is breaking.
`pacto impact` says which consumers it breaks once Pacto can see your fleet:
pass the consumer contracts with `--root`, or read a live fleet with `--k8s`
or `--evidence-url`. With `--local` alone it sees only the contracts in that
folder, so it cannot know who else consumes the service.

```yaml
- name: Check for breaking changes
  run: pacto diff oci://ghcr.io/acme/contracts/my-service .
```

`pacto diff` exits 1 when the change is `BREAKING`, so the check fails.
[Put it in your repository](https://pacto.run/latest/ci/) sets up the whole
job.

## Check what is actually running

Pacto compares what a running service does on the network with what its
contract declares. It watches TCP connections in the Linux kernel with eBPF
(small programs the kernel runs on events) and reports a declared port nobody
listens on, or a declared dependency the service keeps failing to reach.
`pacto run` wraps one command, `pacto agent` watches a host and the Kubernetes
operator can deploy the same agent on every node (opt-in,
`nodeAgent.enabled`).

Full results need Linux 5.11 or later with BTF (kernel type information),
and root or the `BPF`, `PERFMON` and `SYS_PTRACE` capabilities. Off
Kubernetes, Linux 5.8 to 5.10 also works with the `SYS_RESOURCE` capability.
On Kubernetes the node agent has no `SYS_RESOURCE`, so nodes need 5.11 or
later. See
[Runtime conformance](https://pacto.run/latest/runtime-conformance/#requirements).

## Generate contracts from what you have

`pacto init` reads a service's Dockerfile and Kubernetes manifests and writes
a contract, with a list of what it could not tell. It takes many folders at
once, and `--plan` shows the result without writing anything:

```bash
pacto init --plan --output-format json services/checkout services/payments
pacto init --cluster --namespace shop --out drafts --plan
```

`--cluster` drafts a contract for every workload in the namespace that no
Pacto resource describes. See
[Generate contracts from what you already have](https://pacto.run/latest/bootstrap-contracts/).

## Where Pacto is going: delivery and the autonomy ladder

When you are ready, Pacto's opt-in delivery component ships the changes the
contract graph clears. Flux or Argo CD still apply every version.

> **Status:** alpha. Needs Kubernetes 1.30 or later with Flux; Argo CD is
> experimental. Signed records, and so the two automatic autonomy levels,
> need a registry that serves the OCI Referrers API. GHCR does not serve it.

Each Stage, one service in one environment, climbs
[the autonomy ladder](https://pacto.run/latest/autonomy/) from observe, where
Pacto moves nothing, to autonomous. A person confirms every step up. A change
that breaks a consumer waits for that consumer's owners, and when Pacto cannot
see enough it answers Unknown and waits for a person.

GitlessOps means signed artifacts in your registry and evidence decide which
version runs where, while your code and manifests stay in git. Read
[how delivery works](https://pacto.run/latest/integrations/kubernetes/delivery/)
and [what GitlessOps means](MANIFEST.md#what-gitlessops-means).

Pacto delivery builds on Flux's Gitless GitOps, ConfigHub's configuration as
data and Anthropic's Scion; [the manifesto](MANIFEST.md#credits) says what it
takes from each.

## Known limitations

- A default Helm install observes health passively and reports declared
  metrics `Unsupported`. Set `controller.enableProbing` and
  `controller.enableMetricsObservation` to check them; see
  [opt-in features](https://pacto.run/latest/integrations/kubernetes/limitations/#opt-in-features).
- The opt-in node agent needs Linux 5.11 or later with BTF on every node; see
  [requirements](https://pacto.run/latest/runtime-conformance/#requirements).
- Signed delivery records and evidence need a registry that serves the OCI
  Referrers API; see
  [registry conformance](https://pacto.run/latest/evidence/#registry-conformance).
- Delivery is alpha and runs only on Kubernetes; see
  [delivery](https://pacto.run/latest/integrations/kubernetes/delivery/).
- `pacto init` does not read Helm charts or Compose files; see
  [Pactify a service](https://pacto.run/latest/pactify/).

The operator's limits are listed in
[Limitations](https://pacto.run/latest/integrations/kubernetes/limitations/).

## What you keep if you stop using Pacto

Pacto invents three files:
`pacto.yaml`, the `pacto.lock` that [`pacto lock`](https://pacto.run/latest/lockfile/) writes, and an
optional [`.pactoignore`](https://pacto.run/latest/pactoignore/). Nothing outside Pacto reads any
of them. In a cluster, the `Pacto` and delivery custom resources are Pacto's
too. That is the whole of the lock-in. Everything those files wrap stays
yours, and you can check that:

- **The contract source is yours already.** `pacto.yaml` and the files beside it
  are plain YAML and JSON living in your repository, and the interfaces inside
  are the OpenAPI, AsyncAPI, gRPC and JSON Schema documents you maintained
  before Pacto existed. Delete the tooling and those files are unchanged.
- **The registry is yours already.** A published bundle is an ordinary OCI
  artifact in your own registry, such as GHCR, ECR, ACR or Artifactory. Signed
  delivery records and evidence also need a registry that serves the OCI
  Referrers API, which GHCR does not. Nothing is stored on infrastructure
  operated by the project.
- **A published bundle opens without Pacto.** It is a gzipped tar layer, so
  standard OCI tooling is enough to get the files back out (below).
- **Removing the runtime side leaves your workloads untouched.** The operator
  controller observes workloads and never modifies them, so uninstalling it
  stops the checking and leaves every workload exactly as it was. The opt-in
  delivery component writes only the version digest on the Flux or Argo CD
  source that applies each environment, so removing it leaves each environment
  on the last version it moved. Uninstalling does take Pacto's own components
  with it: the managed dashboard and Evidence Server are garbage-collected
  along with the controller, and a few cluster-scoped objects outlive the
  release and need deleting by hand. Both removal paths, and every object that
  survives them, are written down: [uninstall the CLI](https://pacto.run/latest/installation/#uninstall)
  and [uninstall the operator](https://pacto.run/latest/integrations/kubernetes/installation/#uninstall).

Unpacking a published bundle with no Pacto binary anywhere in reach needs
[`oras`](https://oras.land/docs/installation) and [`jq`](https://jqlang.org/download/),
neither of which is a Pacto tool:

```bash
REPO=ghcr.io/your-org/your-service
TAG=1.0.0
DIGEST=$(oras manifest fetch "$REPO:$TAG" | jq -r '.layers[0].digest')
oras blob fetch --output bundle.tar.gz "$REPO@$DIGEST"
tar -xzf bundle.tar.gz    # pacto.yaml, interfaces/, configuration/, sbom/
```

## How Pacto fits with other tools

Pacto composes the interface tools it sits between, such as OpenAPI and config
schemas, and complements the deploy tools, such as Helm and Terraform. Its
core makes no deployment decisions. The opt-in delivery component decides
which version runs where and leaves applying it to Flux or Argo CD.

[How Pacto relates to other tools](https://pacto.run/latest/comparison/)
compares Pacto with nearby projects, with the criteria and a source for each
row.

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/TrianaLab/pacto/main/scripts/get-pacto.sh | bash
```

[Installation](https://pacto.run/latest/installation) covers `go install`,
building from source, pinning a version, installing without `sudo` and
uninstalling.

## Documentation

Full documentation at **[pacto.run](https://pacto.run)**.

[Where to start](https://pacto.run/latest/#where-to-start) picks the page for
what you came to do.

Every term is in the [glossary](https://pacto.run/latest/glossary/), and every
command in the [CLI reference](https://pacto.run/latest/cli-reference).

---

## License

Fully open source under the [MIT](LICENSE) license, delivery included, with no
hosted service.
