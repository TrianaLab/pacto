# CI-specific targets. Included by the main Makefile.
# This file is the single source of truth for all CI quality gates.
# Do not edit without verifying that the GitHub Actions pipelines still match.

BUNDLE_DIR := pactos/pacto-dashboard

.PHONY: ci ci-static ci-static-engine ci-engine ci-dashboard ci-integration-kubernetes \
       ci-e2e-envtest ci-e2e-kind ci-oci ci-gates docs-generate docs-check artifact-drift release-dry-run \
       ci-test ci-ui ui-build ci-ui-drift ci-fmt ci-vet ci-cyclo ci-lint ci-docs \
       gen-openapi gen-config-schema gen-sbom gen-bundle

# ── Monorepo CI matrix (go.work) ─────────────────────────────────────
# The root aggregate. Every leg delegates to the REAL underlying gate across the
# workspace modules. This is what CONTRIBUTING tells contributors to run and what
# .github/workflows/ci.yml requires.
ci: ci-static ci-gates ci-engine ci-dashboard ci-integration-kubernetes ci-e2e-envtest ci-oci

# Cross-cutting architecture + release gates. tests/architecture is the import-
# boundary gate (core must stay k8s-free); tests/release holds the one-publisher,
# demo-ref and release-plan gates. Both live under /tests/, which ci-test EXCLUDES
# (grep -v /tests/), so they get their own leg here and an ALWAYS-run CI job — a
# gate that never runs is not a gate.
ci-gates:
	go test ./tests/architecture/... ./tests/release/...

# Static leg: engine + kubernetes integration static gates.
ci-static: ci-static-engine
	$(MAKE) -C integrations/kubernetes ci-static

# Engine static gates (fmt, vet, cyclo, lint, CLI docs drift, UI build drift).
ci-static-engine: ci-fmt ci-vet ci-cyclo ci-lint ci-docs ci-ui-drift

# Engine leg: unit tests (100% coverage gate) + engine e2e.
ci-engine: ci-test e2e

# Dashboard leg: frontend lint + tests.
ci-dashboard: ci-ui

# Kubernetes integration leg: envtest-backed unit/integration tests + chart CI.
ci-integration-kubernetes:
	$(MAKE) -C integrations/kubernetes ci-test
	$(MAKE) -C integrations/kubernetes ci-chart

# Operator acceptance matrix against envtest (no cluster required).
ci-e2e-envtest:
	$(MAKE) -C integrations/kubernetes test-e2e

# Kind-backed acceptance. ponytail: cluster provisioning is a later milestone;
# envtest (ci-e2e-envtest) is the real gate today. This only ensures a Kind
# cluster exists so later e2e work can attach.
ci-e2e-kind:
	$(MAKE) -C integrations/kubernetes setup-test-e2e

# OCI leg: the public oci package tests + the staging release-publisher tests.
ci-oci:
	go test ./pkg/oci/...
	node --test release/scripts/publish.test.mjs
	node --test release/orchestrator/*.test.mjs

# Regenerate every generated doc across the workspace. Core CLI reference first,
# then every discovered integration's own generator (via its integration.yaml
# documentation.generateCommand) so a future integration is picked up with no
# change here.
docs-generate: gen-cli-docs
	@for m in integrations/*/integration.yaml; do \
		[ -f "$$m" ] || continue; \
		cmd=$$(python3 -c "import yaml,sys; d=yaml.safe_load(open('$$m')) or {}; print((d.get('documentation') or {}).get('generateCommand',''))"); \
		[ -n "$$cmd" ] && { echo "==> $$cmd"; eval "$$cmd"; }; \
	done

# Preview the assembled site locally (the hook assembles integration docs at build time).
docs-serve:
	mkdocs serve

# Publish the versioned site with mike. The site version tracks Pacto CORE (from
# release/release-manifest.json). Integration docs carry their OWN version stamp in
# the generated compatibility table, so a Kubernetes-only release re-runs this with
# the SAME core version and regenerated integration docs: the version selector entry
# is unchanged, only the integration content + compatibility badge move.
#
# mike commits the built site to the gh-pages branch (under <version>/, updating the
# `latest` alias + versions.json) and pushes it. GitHub Pages must be configured to
# serve from that branch (Settings > Pages > "Deploy from a branch: gh-pages /root");
# mike creates gh-pages on first run. The WASM demo is included when built into
# docs/demo/ first (the deploy workflow folds it there; locally run `make docs-build`).
# Docs deploy is a release-transaction unit (release/DESIGN-release-safety.md item
# 11): release.yml passes the EXACT released core version via PACTO_DOCS_CORE_VERSION
# so the versioned snapshot never depends on "latest release" guessing. The
# non-release docs.yml push path leaves it unset and falls back to the committed
# manifest (redeploying the current core version's docs). A k8s-only release keeps
# the same core version and refreshes the integration docs inside that snapshot.
docs-deploy: docs-generate
	@ver=$${PACTO_DOCS_CORE_VERSION:-$$(python3 -c "import json;print(json.load(open('release/release-manifest.json'))['units']['core']['version'])")}; \
	echo "==> mike deploy --push --update-aliases $$ver latest"; \
	mike deploy --push --update-aliases "$$ver" latest

# Full documentation gate: regenerate from scratch, prove zero drift and zero
# second-run diff, strict build, and validate every fenced contract / CR example /
# flag / chart / artifact coordinate against the real sources. See docs_check.py.
docs-check:
	python3 release/scripts/docs_check.py

# artifact-drift = one-publisher-per-artifact gate + apply-release-plan
# idempotency (re-applying the plan must not mutate any tracked release-state file).
artifact-drift:
	@echo "==> one-publisher-per-artifact gate..."
	go test ./tests/release/...
	@echo "==> apply-release-plan idempotency..."
	node release/scripts/build-release-plan.mjs
	node release/scripts/apply-release-plan.mjs
	@git diff --quiet -- release/release-manifest.json release/release-plan.json \
		integrations/kubernetes/go.mod integrations/kubernetes/integration.yaml \
		integrations/kubernetes/charts/pacto-operator/Chart.yaml \
		integrations/kubernetes/charts/pacto-operator/values.yaml \
		integrations/kubernetes/charts/pacto-operator/README.md \
		|| { echo "artifact drift: apply-release-plan is not idempotent (re-run mutated tracked files)"; exit 1; }
	@echo "    artifact-drift: OK"

# Staging release dry-run: regenerate the plan, then preflight the publisher
# (plan/manifest publish-integrity refusals) WITHOUT contacting any registry.
release-dry-run:
	node release/scripts/build-release-plan.mjs
	bash release/scripts/verify-standalone.sh
	node release/scripts/publish.mjs --dry-run

ci-test:
	@echo "==> Running unit tests with race detector and coverage..."
	@go test -race $$(go list ./... | grep -v /tests/ | grep -v /testutil | grep -v /cmd/gendocs | grep -v /cmd/genbundle | grep -v /examples/) -coverprofile=coverage.out
	@total=$$(go tool cover -func=coverage.out | grep '^total:' | awk '{print $$NF}'); \
	if [ "$$total" != "100.0%" ]; then \
		echo "FAIL: total coverage is $$total, expected 100.0%"; \
		go tool cover -func=coverage.out | grep -v '100.0%'; \
		exit 1; \
	fi
	@echo "    total coverage: 100.0%"
	@echo "==> Running example tests (no coverage gate)..."
	@go test -race ./examples/...

ci-ui:
	@echo "==> Running frontend lint & tests..."
	cd pkg/dashboard/frontend && npm ci --ignore-scripts --prefer-offline --fetch-retries=5 --fetch-retry-factor=2 --fetch-retry-mintimeout=20000 --fetch-retry-maxtimeout=120000 && npm run lint && npm test

ci-fmt:
	@echo "==> Checking formatting..."
	@test -z "$$(gofmt -l .)" || (echo "gofmt found unformatted files:" && gofmt -l . && exit 1)

ci-vet:
	@echo "==> Running go vet..."
	go vet ./...

ci-cyclo:
	@echo "==> Checking cyclomatic complexity..."
	go install github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0
	gocyclo -over 15 $$(find . -name '*.go' ! -path './vendor/*' ! -path './integrations/*' ! -path './tests/*' ! -name 'zz_generated*.go' ! -path './release/*')

ci-lint:
	@echo "==> Running linter..."
	golangci-lint run

ci-docs:
	@echo "==> Checking CLI docs are up to date..."
	@$(MAKE) gen-cli-docs
	@git diff --exit-code docs/cli-reference.md || (echo "CLI docs are out of date. Run 'make gen-cli-docs' and commit." && exit 1)

# Rebuild the committed dashboard UI from frontend source. Single source of truth
# for the build incantation — reused by ci-ui-drift and the ui-rebuild workflow.
ui-build:
	@echo "==> Building dashboard UI..."
	cd pkg/dashboard/frontend && npm ci --ignore-scripts --prefer-offline --fetch-retries=5 --fetch-retry-factor=2 --fetch-retry-mintimeout=20000 --fetch-retry-maxtimeout=120000 && npm run build

ci-ui-drift: ui-build
	@echo "==> Checking committed dashboard UI build is up to date..."
	@git diff --exit-code pkg/dashboard/ui/ || (echo "Committed pkg/dashboard/ui/ is out of date. Run 'make ui-build' and commit." && exit 1)

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
