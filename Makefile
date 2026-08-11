VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.gitCommit=$(GIT_COMMIT) -X main.buildDate=$(BUILD_DATE)"
GOBIN := $(shell go env GOBIN 2>/dev/null)
ifeq ($(GOBIN),)
GOPATH := $(shell go env GOPATH 2>/dev/null)
ifeq ($(GOPATH),)
GOPATH := $(HOME)/go
endif
GOBIN := $(GOPATH)/bin
endif

IMAGE := ghcr.io/trianalab/pacto/dashboard

.PHONY: build test e2e coverage lint check-section clean docs docs-build demo-preview-clean gen-cli-docs docker-build docker-run \
        e2e-operational-graph e2e-operational-graph-core e2e-operational-graph-up e2e-operational-graph-status \
        e2e-operational-graph-logs e2e-operational-graph-down e2e-otel e2e-dashboard-wasm e2e-dashboard-kind \
        e2e-evidence-kind e2e-reconcile-kind e2e-upgrade-kind e2e-observed e2e-docs \
        e2e-observation-kind e2e-observation-kind-up e2e-observation-kind-status \
        e2e-observation-kind-logs e2e-observation-kind-down \
        e2e-dashboard-kind-browser e2e-all-operational-graph \
        generate-dashboard-openapi generate-dashboard-sdk check-dashboard-sdk-drift

# Local docs preview includes the in-browser WASM dashboard demo at /demo/.
DOCS_DEMO := docs/demo

build:
	rm -f "$(GOBIN)/pacto"
	go build $(LDFLAGS) -o "$(GOBIN)/pacto" ./cmd/pacto

test:
	go test -race ./... -v

e2e:
	go test -tags e2e ./tests/e2e/ -v -count=1 -parallel 16 -timeout 120s

# Hermetic operational-graph acceptance (no cluster): builds pacto and drives the
# whole fleet story end to end against local fixtures (graph, signed evidence
# ingestion, OTel observation, reconciliation, impact). Verifiable anywhere Go
# runs — the live-Kubernetes source is covered by the kind acceptance.
demo-fleet:
	bash tests/e2e/fleet-graph.sh

# ── Local e2e lifecycle ──────────────────────────────────────────────
# Thin, user-facing aliases over the ci-e2e-* targets (in ci.mk) + demo-fleet so
# a contributor can run ONE acceptance scenario without the whole `make ci` matrix.
# Each -kind scenario self-provisions its own kind cluster (honoring KIND_CLUSTER
# for reuse) and, on failure, dumps cluster diagnostics via tests/e2e/kind/lib.sh.
# To keep a failed cluster + namespace for interactive inspection instead of
# tearing it down, set KEEP_E2E_CLUSTER=1 — the inspect knob for every -kind
# scenario:  KEEP_E2E_CLUSTER=1 make e2e-reconcile-kind
#
# The full operational-graph vertical additionally has a dev lifecycle so you can
# bring up a fully-configured install (operator + dashboard + Evidence Server +
# registry + reconciled Pacto CRs + ingested evidence) and test the product end to
# end in a browser:
#   make e2e-operational-graph-up      # bring it up and leave it running
#   make e2e-operational-graph-status  # component health
#   make e2e-operational-graph-logs    # component logs
#   make e2e-operational-graph-down    # tear it down
# e2e-dashboard-wasm builds the in-browser WASM dashboard demo and runs the
# Playwright browser suite against it (Chromium). It is the browser-level
# acceptance for the redesigned Operational Graph + Impact UI, with no cluster.

# Full operational-graph vertical in a local kind cluster: operator + dashboard +
# Evidence Server + in-cluster registry, with reconciled Pacto CRs (a declared
# dependency edge) and a signed EvidenceEnvelope ingested as an external target —
# everything a fully-configured install shows. Tears down unless KEEP_E2E_CLUSTER=1.
e2e-operational-graph:
	bash tests/e2e/kind/operational-graph.sh

# Cluster-free core: the hermetic operational-graph acceptance (graph, evidence,
# OTel, reconcile, impact) — the same run as demo-fleet, verifiable anywhere Go runs.
e2e-operational-graph-core: demo-fleet

# Bring the full vertical UP and LEAVE it running for manual, end-to-end testing
# (prints how to reach the dashboard). Inspect with -status/-logs; tear down with -down.
e2e-operational-graph-up:
	KEEP_E2E_CLUSTER=1 bash tests/e2e/kind/operational-graph.sh up
e2e-operational-graph-status:
	bash tests/e2e/kind/operational-graph.sh status
e2e-operational-graph-logs:
	bash tests/e2e/kind/operational-graph.sh logs
e2e-operational-graph-down:
	bash tests/e2e/kind/operational-graph.sh down

# OTel observation acceptance is step 3 of the operational-graph story above; there
# is no collector-backed cluster scenario, so this runs the same cluster-free run.
e2e-otel: demo-fleet

# Browser E2E for the WASM dashboard demo. Builds the demo, ensures Chromium is
# installed, then runs the Playwright suite against the built app.
e2e-dashboard-wasm:
	$(MAKE) -C examples/demo build
	cd pkg/dashboard/frontend && npm ci --ignore-scripts && npx playwright install chromium && npm run test:e2e

e2e-dashboard-kind: ci-e2e-kind-dashboard
e2e-evidence-kind: ci-e2e-kind-evidence
e2e-reconcile-kind: ci-e2e-kind-reconcile
e2e-upgrade-kind: ci-e2e-kind-upgrade

# Operator-managed offline observation packaging in a local kind cluster: an
# externally managed PVC and a ConfigMap each carry a trace export, the operator
# mounts them read-only under their declared names, and the live Product API is
# asserted for identity, observed edges, reconciliation and failed-source
# behavior. Same up/status/logs/down lifecycle as the operational-graph vertical.
e2e-observation-kind: ci-e2e-kind-observation
e2e-observation-kind-up:
	KEEP_E2E_CLUSTER=1 bash tests/e2e/kind/observation.sh up
e2e-observation-kind-status:
	bash tests/e2e/kind/observation.sh status
e2e-observation-kind-logs:
	bash tests/e2e/kind/observation.sh logs
e2e-observation-kind-down:
	bash tests/e2e/kind/observation.sh down

# ── Operational-graph story acceptances (section M) ─────────────────────────
# Observed relationships end to end in a real browser: the demo folds observed
# edges into the snapshot, so the Operational Graph's Observed layer and the impact
# shadow-consumer are real, not placebos.
e2e-observed:
	$(MAKE) -C examples/demo build
	cd pkg/dashboard/frontend && npm ci --ignore-scripts && npx playwright install chromium && npx playwright test --project=desktop --grep "observed"

# Bundle-doc Mermaid diagrams actually render to SVG in a real browser.
e2e-docs:
	$(MAKE) -C examples/demo build
	cd pkg/dashboard/frontend && npm ci --ignore-scripts && npx playwright install chromium && npx playwright test --project=desktop e2e/mermaid.spec.ts

# Live Kind dashboard browser acceptance (section I): the full vertical (operator +
# dashboard + Evidence Server + registry + reconciled CRs + ingested evidence) plus
# a Playwright/Chromium run against the LIVE, port-forwarded dashboard.
e2e-dashboard-kind-browser:
	bash tests/e2e/kind/operational-graph.sh browser

# The whole operational-graph story: cluster-free core, the WASM browser suite, and
# the live-Kind vertical + browser acceptance.
e2e-all-operational-graph: e2e-operational-graph-core e2e-dashboard-wasm e2e-dashboard-kind-browser

coverage:
	go test -race $(shell go list ./... | grep -v /tests/ | grep -v /testutil | grep -v /cmd/gendocs | grep -v /cmd/genbundle | grep -v /examples/) -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@go tool cover -func=coverage.out | tail -1

lint: check-section
	gofmt -s -l $(shell find . -name '*.go')
	go vet ./...

# check-section is the reusable U+00A7 gate: zero section-sign glyphs in authored
# files (generated UI bundles excluded by path). See scripts/check-section-sign.sh.
check-section:
	@sh scripts/check-section-sign.sh

gen-cli-docs:
	go run ./cmd/gendocs/

# --- Generated dashboard TypeScript SDK (ADR-6) ---------------------------------
# Huma/OpenAPI is the single source of truth for dashboard HTTP transport. The
# TypeScript request/response types are generated deterministically from that
# contract by a pinned generator (openapi-typescript), so there is no hand-written
# wire schema to drift. The generated artifacts are committed and drift-checked.
SDK_DIR := pkg/dashboard/frontend/src/lib/generated

# generate-dashboard-openapi writes the deterministic OpenAPI contract that feeds
# the SDK generator. Huma marshals with sorted keys, so re-running is byte-stable.
generate-dashboard-openapi:
	go run ./cmd/genbundle dashboard-openapi > $(SDK_DIR)/openapi.json

# generate-dashboard-sdk regenerates the OpenAPI contract and the TypeScript SDK
# from it with the pinned generator. Requires `npm ci` in the frontend first.
generate-dashboard-sdk: generate-dashboard-openapi
	cd pkg/dashboard/frontend && npm run gen:sdk

# check-dashboard-sdk-drift proves the committed OpenAPI + SDK are current: it
# regenerates both from the live Go/Huma definitions and fails if anything differs.
# A backend schema or operation change without regenerated frontend artifacts fails
# here. Run from a clean checkout after `npm ci --ignore-scripts`.
check-dashboard-sdk-drift: generate-dashboard-sdk
	git diff --exit-code -- $(SDK_DIR) || { \
		echo "dashboard SDK is stale: run 'make generate-dashboard-sdk' and commit $(SDK_DIR)"; \
		exit 1; \
	}

# Documentation (MkDocs Material). Install via `brew install mkdocs-material`
# or `pip install -r docs/requirements.txt`. Both targets first build the
# in-browser WASM demo into docs/demo/ so the local preview exposes it, mirroring
# the deployed site. mkdocs honors site_url, so it serves at root locally
# and the demo lives at /demo/ both locally and in production.
docs: $(DOCS_DEMO)/app.wasm
	mkdocs serve

docs-build: $(DOCS_DEMO)/app.wasm
	mkdocs build

# Build the WASM dashboard demo into docs/demo/ (gitignored) with a relative
# asset base, so `mkdocs serve`/`build` serve it correctly at any mount. Rebuilt
# when missing OR when any demo source changes (dashboard frontend, demo bundles,
# demo Go/boot glue) so `make docs` always reflects the current source. Force a
# full refresh with `make demo-preview-clean`.
DEMO_SOURCES := $(shell find pkg/dashboard/frontend/src examples/demo/bundles examples/demo/partners -type f 2>/dev/null) \
	$(wildcard examples/demo/*.go) examples/demo/boot.js examples/demo/Makefile \
	pkg/dashboard/frontend/package.json pkg/dashboard/frontend/package-lock.json
$(DOCS_DEMO)/app.wasm: $(DEMO_SOURCES)
	$(MAKE) -C examples/demo build DIST=$(CURDIR)/$(DOCS_DEMO)

demo-preview-clean:
	rm -rf $(DOCS_DEMO)

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg GIT_COMMIT=$(GIT_COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t $(IMAGE):$(VERSION) .

docker-run: docker-build
	docker run --rm -p 3000:3000 \
		-v "$(HOME)/.kube/config:/home/pacto/.kube/config:ro" \
		-v "$(HOME)/.cache/pacto:/home/pacto/.cache/pacto" \
		$(IMAGE):$(VERSION)

clean:
	rm -f "$(GOBIN)/pacto" coverage.out coverage.html

include ci.mk
