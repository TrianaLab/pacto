# CI-specific targets. Included by the main Makefile.
# This file is the single source of truth for all CI quality gates.
# Do not edit without verifying that the GitHub Actions pipelines still match.

.PHONY: ci ci-static ci-test ci-fmt ci-vet ci-cyclo ci-lint ci-docs

ci: ci-static ci-test e2e

ci-static: ci-fmt ci-vet ci-cyclo ci-lint ci-docs

ci-test:
	@echo "==> Running unit tests with coverage..."
	@go test $$(go list ./... | grep -v /tests/ | grep -v /testutil | grep -v /cmd/gendocs) -coverprofile=coverage.out
	@total=$$(go tool cover -func=coverage.out | grep '^total:' | awk '{print $$NF}'); \
	if [ "$$total" != "100.0%" ]; then \
		echo "FAIL: total coverage is $$total, expected 100.0%"; \
		go tool cover -func=coverage.out | grep -v '100.0%'; \
		exit 1; \
	fi
	@echo "    total coverage: 100.0%"

ci-fmt:
	@echo "==> Checking formatting..."
	@test -z "$$(gofmt -l .)" || (echo "gofmt found unformatted files:" && gofmt -l . && exit 1)

ci-vet:
	@echo "==> Running go vet..."
	go vet ./...

ci-cyclo:
	@echo "==> Checking cyclomatic complexity..."
	go install github.com/fzipp/gocyclo/cmd/gocyclo@latest
	gocyclo -over 15 $$(find . -name '*.go' ! -path './vendor/*')

ci-lint:
	@echo "==> Running linter..."
	golangci-lint run

ci-docs:
	@echo "==> Checking CLI docs are up to date..."
	@$(MAKE) gen-cli-docs
	@git diff --exit-code docs/cli-reference.md || (echo "CLI docs are out of date. Run 'make gen-cli-docs' and commit." && exit 1)
