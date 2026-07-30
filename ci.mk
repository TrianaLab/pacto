# CI-specific targets. Included by the main Makefile.
# This file is the single source of truth for all CI quality gates.
# Do not edit without verifying that the GitHub Actions pipelines still match.

BUNDLE_DIR := pactos/pacto-dashboard

# Pinned Repowise CLI version for the advisory ci-arch architecture-health leg.
REPOWISE_VERSION ?= 0.36.0

.PHONY: ci ci-static ci-static-engine ci-engine ci-dashboard ci-integration-kubernetes \
       ci-e2e-envtest ci-e2e-kind ci-e2e-kind-dashboard ci-e2e-kind-upgrade ci-e2e-kind-reconcile ci-e2e-kind-evidence ci-e2e-kind-operational-graph ci-oci ci-gates docs-generate docs-check artifact-drift release-dry-run \
       verify-k8s-standalone ci-test ci-ui ui-build ci-ui-drift ci-fmt ci-vet ci-cyclo ci-lint ci-arch ci-docs demo-fleet \
       gen-openapi gen-config-schema gen-sbom gen-bundle mermaid-check

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

# Engine static gates (fmt, vet, cyclo, lint, U+00A7 section-sign gate, CLI docs
# drift, UI build drift). check-section is blocking here, not only under `lint`.
ci-static-engine: ci-fmt ci-vet ci-cyclo ci-lint check-section ci-docs ci-ui-drift

# Engine leg: unit tests (100% coverage gate) + engine e2e + the cluster-free
# operational-graph acceptance (tests/e2e/fleet-graph.sh).
ci-engine: ci-test e2e demo-fleet

# Dashboard leg: frontend lint + tests.
ci-dashboard: ci-ui

# Kubernetes integration leg: envtest-backed unit/integration tests + chart CI.
ci-integration-kubernetes:
	$(MAKE) -C integrations/kubernetes ci-test
	$(MAKE) -C integrations/kubernetes ci-chart

# Operator acceptance matrix against envtest (no cluster required).
ci-e2e-envtest:
	$(MAKE) -C integrations/kubernetes test-e2e

# Kind-backed acceptance — Pacto's formal verification against a real cluster:
# build the operator image via the monorepo Dockerfile, install the packaged
# chart, drive a real Compliant -> Unknown -> Compliant reconcile, upgrade from
# the previous published chart, then uninstall. Runs the exact image + chart the
# release simulation builds.
# The required kind leg runs the main reconcile e2e (dashboard enabled), the
# dashboard-modes acceptance: prove the operator does not
# crashloop when the dashboard is disabled, and the v4->v5 upgrade acceptance
#: a REAL cross-major chart + CRD migration (install the
# published v4 chart + its v4 CRDs, server-side apply the new CRDs, helm upgrade to
# the v5 chart, prove existing resources survive). dashboard-modes + upgrade run
# first (fast guards); run.sh then covers the full enabled reconcile cycle.
# CI shards these self-provisioning scenarios across a matrix (each spins up its
# own kind cluster, so they run independently in parallel); `make ci-e2e-kind`
# still runs them all locally. The evidence scenario proves the operator-managed
# Evidence Server component (Deployment/Service/retained PVC, readiness, dashboard
# auto-wiring) in the existing operator chart.
ci-e2e-kind: ci-e2e-kind-dashboard ci-e2e-kind-upgrade ci-e2e-kind-reconcile ci-e2e-kind-evidence ci-e2e-kind-operational-graph

ci-e2e-kind-dashboard:
	bash tests/e2e/kind/dashboard-modes.sh

# Full operational-graph vertical + a LIVE browser acceptance: brings up operator +
# dashboard + Evidence Server + registry with reconciled CRs and ingested evidence,
# then drives the LIVE dashboard in Chromium via Playwright. Runs in CI's clean
# Docker (classic image store), where `kind load docker-image` works — Docker
# Desktop's containerd image store breaks it locally.
ci-e2e-kind-operational-graph:
	bash tests/e2e/kind/operational-graph.sh browser

ci-e2e-kind-upgrade:
	bash tests/e2e/kind/v4-to-v5-upgrade.sh

ci-e2e-kind-reconcile:
	bash tests/e2e/kind/run.sh

ci-e2e-kind-evidence:
	bash tests/e2e/kind/evidence.sh

# OCI leg: the public oci package tests + the staging release-publisher tests.
ci-oci:
	go test ./pkg/oci/...
	node --test release/orchestrator/*.test.mjs

# Integration test for the version command: runs the REAL `npm run release:version`
# in a throwaway clone with pending changesets and proves it emits a ready
# transaction detect.mjs acts on. Needs node_modules (the CI job runs npm ci).
ci-release-version:
	bash release/orchestrator/test-release-version.sh

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
# Docs deploy is a release-transaction unit: release.yml passes the EXACT released core version via PACTO_DOCS_CORE_VERSION
# so the versioned snapshot never depends on "latest release" guessing. The
# non-release docs.yml push path leaves it unset and falls back to the committed
# manifest (redeploying the current core version's docs). A k8s-only release keeps
# the same core version and refreshes the integration docs inside that snapshot.
# RELEASE docs deploy (release.yml, unit k8s-docs): publish the EXACT released core
# version and move the `latest` alias + default. Only a release transaction may
# touch a stable version or latest.
docs-deploy: docs-generate
	@ver=$${PACTO_DOCS_CORE_VERSION:-$$(python3 -c "import json;print(json.load(open('release/release-manifest.json'))['units']['core']['version'])")}; \
	echo "==> mike deploy --push --update-aliases $$ver latest (release)"; \
	mike deploy --push --update-aliases "$$ver" latest; \
	mike set-default --push latest

# Blocking Mermaid syntax gate: every fenced ```mermaid block in docs/ +
# integrations/ must render via mermaid-cli (mmdc). mkdocs --strict does NOT parse
# mermaid (pymdownx.superfences only wraps it for client-side render), so a broken
# diagram ships silently — this is the only gate that catches it. See check_mermaid.py.
mermaid-check:
	python3 release/scripts/check_mermaid.py

# Full documentation gate: regenerate from scratch, prove zero drift and zero
# second-run diff, strict build, and validate every fenced contract / CR example /
# flag / chart / artifact coordinate against the real sources. Runs mermaid-check
# first so a broken diagram fails the same gate. See docs_check.py + check_mermaid.py.
docs-check: mermaid-check
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

# Staging release dry-run — the real release simulation: builds the real artifacts
# and pushes them to a disposable local registry via the SAME shared adapters
# production uses, proving digest idempotency, fail-closed immutability + resume.
# No production coordinate. This is the single staging evidence path.
release-dry-run:
	bash release/orchestrator/dry-run.sh

# External-consumer proof for the Kubernetes integration Go module: a throwaway
# module `go get`s github.com/trianalab/pacto/integrations/kubernetes/v5@v5.0.0 and
# builds it with GOWORK=off and no replace, proving the /v5 semantic-import path is
# valid Go and consumable by outside users. Local staging tags only, no publish.
# Also invoked by release/orchestrator/dry-run.sh after the core verify-standalone.
verify-k8s-standalone:
	bash release/orchestrator/verify-k8s-standalone.sh

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
	@echo "==> Validating demo contracts (offline, full validator over the closure)..."
	@$(MAKE) -C examples/demo validate

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

# Architecture-health leg (ADVISORY — never blocks). Deterministic Repowise
# analysis: zero-LLM (`repowise init --no-prose`, no API key). Posts change-risk +
# code-health to the PR check summary in CI ($GITHUB_STEP_SUMMARY), stdout locally.
# Deliberately NOT part of the blocking `ci` aggregate above: metric hard-gates are
# noisy (a large but legitimate refactor scores "high"), so this informs review
# rather than gating. .github/workflows/repowise.yml just runs this target.
ci-arch:
	@echo "==> Running Repowise architecture-health analysis (advisory, zero-LLM)..."
	pip install --quiet "repowise==$(REPOWISE_VERSION)"
	repowise init --no-prose
	@tmp=$$(mktemp -d); \
	repowise risk "origin/$${GITHUB_BASE_REF:-main}..HEAD" --format json > $$tmp/risk.json 2>/dev/null || echo '{}' > $$tmp/risk.json; \
	repowise health --format json > $$tmp/health.json 2>/dev/null || echo '{}' > $$tmp/health.json; \
	python3 release/scripts/repowise_summary.py $$tmp/risk.json $$tmp/health.json >> "$${GITHUB_STEP_SUMMARY:-/dev/stdout}"; \
	rm -rf $$tmp

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
