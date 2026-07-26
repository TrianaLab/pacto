# Contributing to Pacto Operator

Thank you for your interest in contributing! This guide will help you get started.

## Code of Conduct

By participating in this project, you agree to treat all contributors with respect and maintain a welcoming, inclusive environment.

## Getting Started

### Prerequisites

- [Go 1.26+](https://go.dev/dl/)
- [Docker](https://docs.docker.com/get-docker/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Kind](https://kind.sigs.k8s.io/) (for e2e tests)
- A terminal with `make` available

### Setting Up Your Development Environment

1. **Fork and clone the repository:**

   ```bash
   git clone https://github.com/<your-username>/pacto-operator.git
   cd pacto-operator
   ```

2. **Install dependencies:**

   ```bash
   go mod download
   ```

   > **Engine sibling checkout.** The operator depends on the
   > [Pacto engine](https://github.com/TrianaLab/pacto) module
   > `github.com/trianalab/pacto/v2`. On the development branch `go.mod` carries
   > a `replace github.com/trianalab/pacto/v2 => ../pacto` so the build uses a
   > sibling engine checkout. Clone the engine next to this repo (as `../pacto`)
   > for co-development. The operator imports engine packages (`pkg/evidence`,
   > `pkg/finding`) that may be newer than the pinned engine tag, so with the
   > `replace` removed a clean clone cannot build until the engine is published
   > and the `require` bumped — see [RELEASING.md](RELEASING.md). Toggle the
   > sibling with `make pacto-local` (add the replace) and `make pacto-remote`
   > (drop it).

3. **Run the CI pipeline locally:**

   ```bash
   make ci
   ```

   This runs static checks, unit/integration tests, and chart validation — the same gates as the `static`, `unit-test`, and `chart` CI jobs. **Always run `make ci` before pushing.**

   You can also run individual targets:

   ```bash
   make build        # Build the controller binary
   make test         # Unit/integration tests (envtest)
   make test-e2e     # End-to-end tests on a Kind cluster
   make lint         # golangci-lint
   ```

4. **Deploy to a local cluster:**

   ```bash
   make helm-install   # Build image + helm install (CRDs included)
   make helm-upgrade   # Rebuild + upgrade existing release
   make helm-uninstall # Remove release
   ```

   Or run the controller as a local process (requires CRDs installed first):

   ```bash
   make install        # Install CRDs via kustomize
   make run            # Run controller against current kubeconfig
   ```

## How to Contribute

### Reporting Bugs

[Open an issue](https://github.com/TrianaLab/pacto-operator/issues/new?template=bug_report.yml) using the bug report template. Include:

- Steps to reproduce the issue
- Expected vs. actual behavior
- Your environment (Kubernetes version, Go version, operator version)
- Relevant logs or error messages

### Suggesting Features

[Open a feature request](https://github.com/TrianaLab/pacto-operator/issues/new?template=feature_request.yml) using the feature request template. Describe the problem you're trying to solve and the solution you'd like to see.

### Submitting Changes

1. **Create a branch** from `main`:

   ```bash
   git checkout -b feat/my-feature
   ```

   Use a descriptive branch name with a prefix: `feat/`, `fix/`, `docs/`, `refactor/`, `test/`.

2. **Make your changes.** Keep commits focused and atomic.

3. **Write or update tests.** All new functionality must include tests. All bug fixes must include a regression test.

4. **Run the CI pipeline locally before pushing:**

   ```bash
   make ci
   ```

5. **Write a clear commit message** following the project's convention:

   ```
   feat: add support for CronJob workload validation
   fix: resolve nil pointer in observer when PVC is missing
   docs: update quickstart with Helm installation
   ```

   Use the format `<type>: <description>` where type is one of: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`.

6. **Open a pull request** against `main`. Fill in the PR template and link any related issues.

## Development Guidelines

### CI Quality Gates

The `make ci` target runs all quality gates in order:

| Gate | What it checks |
|------|---------------|
| `ci-fmt` | All files are `gofmt`-formatted |
| `ci-vet` | `go vet` passes on all packages |
| `ci-lint` | `golangci-lint` reports zero issues |
| `ci-test` | Unit/integration tests pass (envtest) |
| `ci-chart` | Helm lint, template rendering, unit tests, schema validation, docs drift |

### Testing

- **Unit/integration tests** use controller-runtime's envtest and live alongside the code they test.
- **End-to-end tests** live in `test/e2e/` and run on a Kind cluster with the `e2e` build tag.
- Run `make test` for unit/integration tests and `make test-e2e` for e2e tests.

## Pull Request Process

1. Run `make ci` locally and ensure it passes.
2. Request a review from a maintainer.
3. Address review feedback. Push new commits rather than force-pushing so reviewers can see incremental changes.
4. Once approved, a maintainer will merge your PR.

## Releasing

Releasing the operator is a **cross-repo, engine-first** procedure: the Pacto engine must be published at a version containing the imported packages, the operator's `require` bumped to it and the dev-only `../pacto` `replace` dropped, before the operator itself is released. The full steps are in [RELEASING.md](RELEASING.md).

Once the engine dependency is in order, releases are automated. When a PR is merged to `main`, the auto-release workflow determines the version bump from the PR title (conventional commit format) and creates a GitHub release, Docker image and Helm chart.

## Questions?

If you're unsure about anything, feel free to [open a discussion](https://github.com/TrianaLab/pacto-operator/issues) or ask in your pull request.

## License

By contributing to Pacto Operator, you agree that your contributions will be licensed under the [MIT License](LICENSE).
