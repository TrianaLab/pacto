# Contributing to Pacto

Thank you for your interest in contributing to Pacto! This guide will help you get started.

## Code of Conduct

By participating in this project, you agree to treat all contributors with respect and maintain a welcoming, inclusive environment.

## Getting Started

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- [Git](https://git-scm.com/)
- A terminal with `make` available
- [golangci-lint](https://golangci-lint.run/welcome/install/) (for linting)

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

   This runs everything the CI pipeline checks — formatting, vetting, cyclomatic complexity, linting, 100% unit test coverage, and e2e tests. **Always run `make ci` before pushing** to catch issues early.

   You can also run individual targets:

   ```bash
   make test         # unit tests
   make e2e          # end-to-end tests
   make lint         # gofmt + go vet
   make coverage     # coverage report with HTML output
   ```

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

3. **Write or update tests.** All new functionality must include tests. All bug fixes must include a regression test. The project enforces **100% statement coverage** on all packages.

4. **Run the CI pipeline locally before pushing:**

   ```bash
   make ci
   ```

   This is the same check that runs in GitHub Actions. If `make ci` passes locally, the pipeline will pass too.

5. **Write a clear commit message** following the project's convention:

   ```
   feat: add support for gRPC interface validation
   fix: resolve $ref in nested configuration schemas
   docs: update quickstart with OCI push example
   ```

   Use the format `<type>: <description>` where type is one of: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`.

6. **Open a pull request** against `main`. Fill in the PR template and link any related issues.

## Development Guidelines

### Project Structure

```
pacto/
  cmd/pacto/          # CLI entrypoint (bootstrap only)
  cmd/gendocs/        # CLI docs generator
  pkg/                # Public, reusable core packages
    contract/         #   Domain model (Contract, Bundle, types)
    validation/       #   Four-layer validator + runtime validation
    diff/             #   Change classifier
    graph/            #   Dependency resolver
    doc/              #   Markdown documentation generator
    sbom/             #   SBOM parser and differ
    override/         #   YAML override engine
    plugin/           #   Plugin protocol and runner
  internal/           # Internal packages (not importable externally)
    app/              #   Application service layer (orchestrates pkg/*)
    cli/              #   Cobra command handlers (thin adapters)
    oci/              #   OCI registry adapter
    mcp/              #   MCP server adapter
    logger/           #   Structured logging setup
    update/           #   Version update checker
    testutil/         #   Shared test utilities
  schema/             # Standalone JSON schema copy
  tests/e2e/          # End-to-end tests
  docs/               # Documentation site (Jekyll)
  scripts/            # Build and install scripts
```

Core domain logic lives in `pkg/` and can be imported by external projects. Infrastructure and CLI wiring lives in `internal/`.

### Code Style

- Follow standard Go conventions and idioms.
- Code must pass `golangci-lint` (run via `make ci`).
- Keep functions small and focused. Cyclomatic complexity must stay at 15 or below.
- Use meaningful names for variables, functions, and packages.

### Testing

- **Unit tests** live alongside the code they test (`_test.go` files).
- **End-to-end tests** live in `tests/e2e/` and use the `e2e` build tag.
- The project enforces **100% statement coverage**. `make ci` will fail if any package drops below 100%.
- Run `make coverage` to generate a coverage report and identify uncovered lines.

### CI Quality Gates

The `make ci` target runs all quality gates in order:

| Gate | What it checks |
|------|---------------|
| `ci-fmt` | All files are `gofmt`-formatted |
| `ci-vet` | `go vet` passes on all packages |
| `ci-cyclo` | No function exceeds cyclomatic complexity 15 |
| `ci-lint` | `golangci-lint` reports zero issues |
| `ci-docs` | CLI reference docs are up to date |
| `ci-test` | Unit tests pass with 100% coverage |
| `e2e` | End-to-end tests pass |

### Documentation

- Update docs if your change affects user-facing behavior, CLI flags, or the contract specification.
- Documentation lives in `docs/` and is built with Jekyll.
- Run `make docs` to preview the documentation site locally.
- CLI reference docs are auto-generated. Run `make gen-cli-docs` if you add or change CLI commands.

## Pull Request Process

1. Run `make ci` locally and ensure it passes.
2. Request a review from a maintainer.
3. Address review feedback. Push new commits rather than force-pushing so reviewers can see incremental changes.
4. Once approved, a maintainer will merge your PR.

## Releasing

Releases are managed by maintainers. The release workflow is triggered by pushing a new Git tag:

```bash
git tag v1.2.3
git push origin v1.2.3
```

## Questions?

If you're unsure about anything, feel free to [open a discussion](https://github.com/TrianaLab/pacto/issues) or ask in your pull request. We're happy to help!

## License

By contributing to Pacto, you agree that your contributions will be licensed under the [MIT License](LICENSE).
