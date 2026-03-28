# CI-specific targets. Included by the main Makefile.
# This file is the single source of truth for all CI quality gates.
# Do not edit without verifying that the GitHub Actions pipelines still match.

BUNDLE_DIR := pactos/pacto-dashboard

.PHONY: ci ci-static ci-test ci-ui ci-fmt ci-vet ci-cyclo ci-lint ci-docs \
       gen-openapi gen-config-schema gen-sbom gen-bundle

ci: ci-static ci-test e2e gen-bundle

ci-static: ci-fmt ci-vet ci-cyclo ci-lint ci-docs

ci-test: ci-ui
	@echo "==> Running unit tests with coverage..."
	@go test $$(go list ./... | grep -v /tests/ | grep -v /testutil | grep -v /cmd/gendocs | grep -v /cmd/genbundle) -coverprofile=coverage.out
	@total=$$(go tool cover -func=coverage.out | grep '^total:' | awk '{print $$NF}'); \
	if [ "$$total" != "100.0%" ]; then \
		echo "FAIL: total coverage is $$total, expected 100.0%"; \
		go tool cover -func=coverage.out | grep -v '100.0%'; \
		exit 1; \
	fi
	@echo "    total coverage: 100.0%"

ci-ui:
	@echo "==> Running frontend tests..."
	cd pkg/dashboard/frontend && npm ci --ignore-scripts && npm test

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

# ── Bundle generation targets ────────────────────────────────────────
# The OpenAPI spec is generated via the pacto-plugin-openapi-infer plugin
# using --option source=../.. to point at the repo root (where go.mod lives).

gen-openapi:
	@echo "==> Generating OpenAPI spec..."
	pacto generate openapi-infer $(BUNDLE_DIR) --option source=../.. --option output=interfaces/openapi.json -o $(BUNDLE_DIR)

gen-config-schema:
	@echo "==> Generating configuration JSON schema..."
	@mkdir -p $(BUNDLE_DIR)/configuration
	go run ./cmd/genbundle config-schema > $(BUNDLE_DIR)/configuration/schema.json

gen-sbom:
	@echo "==> Generating SBOM with syft..."
	@mkdir -p $(BUNDLE_DIR)/sbom
	syft dir:. -o spdx-json > $(BUNDLE_DIR)/sbom/sbom.spdx.json

gen-bundle: gen-openapi gen-config-schema gen-sbom
	@echo "==> Bundle artifacts generated in $(BUNDLE_DIR)/"
