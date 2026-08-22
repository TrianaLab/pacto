# Contributing to Pacto

Thank you for your interest in contributing to Pacto! This guide will help you get started.

## Code of Conduct

By participating in this project, you agree to treat all contributors with respect and maintain a welcoming, inclusive environment.

## Getting Started

### Prerequisites

To build and test the Go engine:

- [Go 1.26+](https://go.dev/dl/)
- [Git](https://git-scm.com/)
- A terminal with `make` available
- [golangci-lint](https://golangci-lint.run/welcome/install/) (for linting)

`make ci` runs the gates for the whole monorepo, so it needs more than that:

- [Node 22+](https://nodejs.org/) and npm — the dashboard frontend gates and the release orchestrator's Node tests
- [Helm](https://helm.sh/docs/intro/install/) and [helm-docs](https://github.com/norwoodj/helm-docs) — the operator chart gates
- [Python 3.12+](https://www.python.org/downloads/) with `pip install -r docs/requirements.txt` — the documentation gates
- Network access on first run: `ci-cyclo` fetches `gocyclo`, and the operator's tests download envtest assets

### Setting Up Your Development Environment

1. **Fork and clone the repository:**

   ```bash
   git clone https://github.com/<your-username>/pacto.git
   cd pacto
   ```

2. **Install dependencies:**

   ```bash
   go mod download
   ```

3. **Build the binary:**

   ```bash
   make build
   ```

4. **Run the full CI pipeline locally:**

   ```bash
   make ci
   ```

   This runs the same gates GitHub Actions runs on a pull request — formatting, vetting, cyclomatic complexity, linting, unit tests at 100% total coverage, the CLI integration suite, the frontend suite, the operator's envtest suite and the chart gates. **Always run `make ci` before pushing** to catch issues early. The kind, Compose and browser acceptances are not part of it; they need a Docker daemon and run in their own CI jobs.

   You can also run individual targets:

   ```bash
   make test              # unit tests
   make test-integration  # in-process CLI integration suite (build tag `integration`)
   make lint              # gofmt + go vet
   make coverage          # coverage report with HTML output
   ```

   `make e2e` is a deprecated alias for `make test-integration`.

   Acceptance scenarios can be run one at a time (thin aliases over the
   `test-acceptance-*` targets):

   ```bash
   make e2e-operational-graph       # kind: FULL vertical (operator + dashboard + Evidence Server + registry), asserts end to end
   make e2e-operational-graph-core  # cluster-free fleet story: graph, evidence, OTel, reconcile, impact
   make e2e-otel                    # OTel observation acceptance (part of the operational-graph-core run)
   make e2e-dashboard-wasm          # browser (Playwright) suite against the built WASM dashboard demo
   make e2e-reconcile-kind          # kind: full operator reconcile cycle (dashboard enabled)
   make e2e-dashboard-kind          # kind: dashboard enabled/disabled lifecycle, no crashloop
   make e2e-evidence-kind           # kind: operator-managed Evidence Server + real in-cluster ingestion
   make e2e-upgrade-kind            # kind: real v4 -> v5 chart + CRD migration
   ```

   The `-kind` scenarios each self-provision a kind cluster (reused via
   `KIND_CLUSTER`) and, on failure, dump cluster diagnostics before exiting. To
   keep a failed cluster and its namespace for inspection instead of tearing it
   down, set `KEEP_E2E_CLUSTER=1` (e.g. `KEEP_E2E_CLUSTER=1 make e2e-reconcile-kind`).

   **Test the whole product locally.** To bring up a fully-configured install
   (operator + dashboard + Evidence Server + an in-cluster registry, with
   reconciled Pacto CRs — including a declared dependency edge — and a signed
   EvidenceEnvelope ingested as an external target) and leave it running so you can
   click through the Operational Graph and Impact in a browser:

   ```bash
   make e2e-operational-graph-up      # bring it up and leave it running (prints how to reach the dashboard)
   make e2e-operational-graph-status  # component health
   make e2e-operational-graph-logs    # component logs
   make e2e-operational-graph-down    # tear it down
   ```

   After `-up`, port-forward the dashboard and open the graph:

   ```bash
   export KUBECONFIG=$(mktemp) && kind get kubeconfig --name pacto-og > $KUBECONFIG
   kubectl -n pacto-system port-forward svc/pacto-dashboard 8080:3000
   open http://localhost:8080/#/fleet
   ```

   For the same product story with **no cluster at all**, the in-browser WASM demo
   runs the real dashboard + API compiled to wasm over embedded data:

   ```bash
   make -C examples/demo build && make -C examples/demo serve   # then open the printed URL
   ```

   The docs site (which embeds the WASM demo at `/demo/`) is built by
   `make docs` / `make docs-build`.

## How to Contribute

### Reporting Bugs

If you find a bug, please [open an issue](https://github.com/TrianaLab/pacto/issues/new?template=bug_report.yml) using the bug report template. Include:

- Steps to reproduce the issue
- Expected vs. actual behavior
- Your environment (OS, Go version, Pacto version)
- Relevant logs or error messages

### Suggesting Features

Have an idea? [Open a feature request](https://github.com/TrianaLab/pacto/issues/new?template=feature_request.yml) using the feature request template. Describe the problem you're trying to solve and the solution you'd like to see.

### Submitting Changes

1. **Create a branch** from `main`:

   ```bash
   git checkout -b feat/my-feature
   ```

   Use a descriptive branch name with a prefix: `feat/`, `fix/`, `docs/`, `refactor/`, `test/`.

2. **Make your changes.** Keep commits focused and atomic.

3. **Write or update tests.** All new functionality must include tests. All bug fixes must include a regression test. The project enforces **100% total statement coverage** across the measured packages — see [Testing](#testing).

4. **Run the CI pipeline locally before pushing:**

   ```bash
   make ci
   ```

   This is the same set of gates GitHub Actions runs on a pull request. The kind, Compose and browser acceptances run only in CI.

5. **Write a clear commit message** following the project's convention:

   ```
   feat: add support for gRPC interface validation
   fix(oci): resolve $ref in nested configuration schemas
   feat!: rename the readiness gate field
   ```

   Individual commit messages are not checked automatically, but your **pull request title** is: `.github/workflows/pr-title.yml` requires `<type>[(scope)][!]: <description>`, where type is one of `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `ci`, `perf`, `build` or `style`. Put `!` before the colon for a breaking change.

6. **Open a pull request** against `main`. Fill in the PR template and link any related issues.

## Development Guidelines

### Project Structure

```
pacto/
  cmd/pacto/          # CLI entrypoint (bootstrap only)
  cmd/gendocs/        # CLI docs generator
  pkg/                # Public, reusable core packages
    contract/         #   Domain model (Contract, Bundle, types)
    validation/       #   Three-layer validator (structural, cross-field, policy)
    diff/             #   Change classifier
    graph/            #   Dependency resolver
    doc/              #   Markdown documentation generator
    sbom/             #   SBOM parser and differ
    override/         #   YAML override engine
    plugin/           #   Plugin protocol and runner
  internal/           # Internal packages (not importable externally)
    app/              #   Application service layer (orchestrates pkg/*)
    cli/              #   Cobra command handlers (thin adapters)
    mcp/              #   MCP server adapter
    update/           #   Version update checker
    testutil/         #   Shared test utilities
  schema/             # Standalone JSON schema copy
  tests/              # Tests that are not about one package
    integration/      #   CLI driven in process against a real registry
    architecture/     #   Structural rules about the repository itself
    acceptance/local/ #   Whole user stories, no cluster
    acceptance/kind/  #   The product against a real Kubernetes cluster
    release/          #   The release system produces what it claims
  docs/               # Documentation site (MkDocs)
  scripts/            # Build and install scripts
```

Core domain logic lives in `pkg/` and can be imported by external projects. Infrastructure and CLI wiring lives in `internal/`.

### Code Style

- Follow standard Go conventions and idioms.
- Code must pass `golangci-lint` (run via `make ci`).
- Keep functions small and focused. Cyclomatic complexity must stay at 15 or below.
- Use meaningful names for variables, functions, and packages.

### Testing

Pacto has eight test levels. A test belongs to exactly one, chosen by what it
proves — never by filename or language. **[Testing
architecture](docs/maintainers/testing.md) is the full guide, including how to
pick the level for a new test.** The short version:

| Level | Lives in | Run with |
|-------|----------|----------|
| Unit | beside the code (`_test.go`, `*.test.ts`) | `make test`, `make ci-ui` |
| Integration | `tests/integration/`, `integrations/kubernetes/test/` | `make test-integration` |
| Architecture / invariant | `tests/architecture/` | `make ci-gates` |
| Local acceptance, cluster-free | `tests/acceptance/local/` | `make test-acceptance-local` |
| Kind / system acceptance | `tests/acceptance/kind/` | `make test-acceptance-kind` |
| Browser acceptance, deterministic | `pkg/dashboard/frontend/e2e/` | `make test-browser` |
| Live-browser acceptance | `pkg/dashboard/frontend/e2e-live/` | `make test-browser-live` |
| Release verification | `tests/release/`, `release/orchestrator/` | `make ci-gates`, `make ci-oci`, `make release-dry-run` |

- The project enforces **100% total statement coverage**. `ci-test` measures every package except `tests/`, `testutil`, `cmd/gendocs`, `cmd/genbundle` and `examples/`, and fails if the *total* is not 100.0%.
- Run `make coverage` to generate a coverage report and identify uncovered lines.

### CI Quality Gates

`make ci` runs seven legs, in this order:

| Leg | What it checks |
|-----|---------------|
| `ci-static` | `gofmt`, `go vet`, cyclomatic complexity, `golangci-lint`, the U+00A7 section-sign gate, and drift in the CLI reference, the dashboard UI build and the generated dashboard SDK — plus the operator module's own static leg |
| `ci-gates` | Architecture/invariant (`tests/architecture/`) and release-verification (`tests/release/`) gates |
| `ci-engine` | Unit tests at 100% total coverage, the in-process CLI integration suite, and the cluster-free local acceptance |
| `ci-dashboard` | Frontend lint and the Vitest suite |
| `ci-integration-kubernetes` | The operator's envtest suite and the Helm chart gates (lint, template, unittest, schema, docs drift) |
| `ci-e2e-envtest` | The operator acceptance matrix against a real API server, with no cluster |
| `ci-oci` | The public `pkg/oci` tests and the release orchestrator's Node tests |

Docker-dependent legs — `ci-e2e-compose` and the `test-acceptance-kind-*` scenarios — are not part of `make ci` and run as their own CI jobs.

### Documentation

- Update docs if your change affects user-facing behavior, CLI flags, or the contract specification.
- Documentation lives in `docs/` and is built with [MkDocs Material](https://squidfunk.github.io/mkdocs-material/) (`mkdocs.yml`), versioned with [mike](https://github.com/jimporter/mike).
- Run `make docs` to build the site, then `make docs-serve` to preview it locally.
- CLI reference docs are auto-generated. Run `make gen-cli-docs` if you add or change CLI commands.

### Operator (Kubernetes integration)

The Kubernetes operator is a nested Go module under `integrations/kubernetes/`, resolved against the engine through the root `go.work` — there is no separate engine checkout to clone. Its dev commands run from that directory and additionally require [Docker](https://docs.docker.com/get-docker/), [kubectl](https://kubernetes.io/docs/tasks/tools/) and [Kind](https://kind.sigs.k8s.io/):

```bash
cd integrations/kubernetes
make ci             # static checks + envtest tests + Helm chart validation
make test           # unit/integration tests (controller-runtime envtest)
make test-e2e       # e2e acceptance matrix against envtest
make lint           # golangci-lint
```

Deploy to a local cluster:

```bash
make install        # install CRDs via kustomize
make run            # run the controller against your current kube context
make helm-install   # build image + helm install (CRDs included)
make helm-upgrade   # rebuild + upgrade the existing release
make helm-uninstall # remove the release
```

The operator's `make ci` adds a Helm chart gate (`ci-chart`: helm lint, template rendering, unit tests, schema validation, docs drift) on top of the standard fmt/vet/lint/test gates.

## Pull Request Process

1. Run `make ci` locally and ensure it passes.
2. Request a review from a maintainer.
3. Address review feedback. Push new commits rather than force-pushing so reviewers can see incremental changes.
4. Once approved, a maintainer will merge your PR.

## Releasing

Releases are managed by maintainers through a transaction-driven pipeline — not by
pushing a Git tag by hand.

Contributors: include a **changeset** with any user-facing change. Run

```bash
npx changeset
```

and commit the generated `.changeset/*.md` file describing the change and its semver
bump. The fixed changeset groups in `.changeset/config.json` keep the core and
Kubernetes module versions consistent.

Maintainers cut a release by running `npm run release:version` (which runs
`changeset version`, then builds and applies the release plan) and letting the
release workflow (`.github/workflows/release.yml`) build, publish and sign the
artifacts.

## Questions?

If you're unsure about anything, feel free to [open an issue](https://github.com/TrianaLab/pacto/issues/new/choose) or ask in your pull request. We're happy to help!

## License

By contributing to Pacto, you agree that your contributions will be licensed under the [MIT License](LICENSE).
