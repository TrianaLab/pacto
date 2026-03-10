# Contributing to Pacto

Thank you for your interest in contributing to Pacto! This guide will help you get started.

## Code of Conduct

By participating in this project, you agree to treat all contributors with respect and maintain a welcoming, inclusive environment.

## Getting Started

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- [Git](https://git-scm.com/)
- A terminal with `make` available

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

4. **Run the tests:**

   ```bash
   make test    # unit tests
   make e2e     # end-to-end tests
   make lint    # linter
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

3. **Write or update tests.** All new functionality should include tests. All bug fixes should include a regression test.

4. **Run the full check suite before pushing:**

   ```bash
   make lint
   make test
   make e2e
   ```

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
  cmd/pacto/       # CLI entrypoint
  internal/        # Internal packages (not importable by external projects)
  pkg/             # Public library packages
  schema/          # JSON schemas for contract validation
  tests/e2e/       # End-to-end tests
  plugins/         # Plugin implementations
  docs/            # Documentation site (Jekyll)
  scripts/         # Build and install scripts
```

### Code Style

- Follow standard Go conventions and idioms.
- Code must pass `golangci-lint` (run via `make lint` or CI).
- Keep functions small and focused. Avoid deep nesting.
- Use meaningful names for variables, functions, and packages.

### Testing

- **Unit tests** live alongside the code they test (`_test.go` files).
- **End-to-end tests** live in `tests/e2e/` and use the `e2e` build tag.
- Aim for meaningful test coverage. Cover edge cases and error paths, not just the happy path.
- Run `make coverage` to generate a coverage report.

### Documentation

- Update docs if your change affects user-facing behavior, CLI flags, or the contract specification.
- Documentation lives in `docs/` and is built with Jekyll.
- Run `make docs` to preview the documentation site locally.

## Pull Request Process

1. Ensure CI passes (lint, unit tests, e2e tests).
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
