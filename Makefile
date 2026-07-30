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
        e2e-operational-graph e2e-otel e2e-dashboard-kind e2e-evidence-kind e2e-reconcile-kind e2e-upgrade-kind

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
# tearing it down, set KEEP_E2E_CLUSTER=1 — the single inspect knob. There are
# deliberately NO per-scenario -status/-logs/-down targets: the scripts
# self-provision and the failure-dump trap covers the real need.
#   KEEP_E2E_CLUSTER=1 make e2e-reconcile-kind
# There is no e2e-dashboard-wasm / e2e-docs target: the in-browser WASM dashboard
# demo and the docs site are built + exercised by `make docs`, `make docs-build`
# and `make -C examples/demo build` (the docs targets below).

# Cluster-free operational-graph acceptance (graph, evidence, OTel, reconcile,
# impact) — the same hermetic run as demo-fleet, verifiable anywhere Go runs.
e2e-operational-graph: demo-fleet

# OTel observation acceptance is step 3 of the operational-graph story above; there
# is no collector-backed cluster scenario, so this runs the same cluster-free run.
e2e-otel: demo-fleet

e2e-dashboard-kind: ci-e2e-kind-dashboard
e2e-evidence-kind: ci-e2e-kind-evidence
e2e-reconcile-kind: ci-e2e-kind-reconcile
e2e-upgrade-kind: ci-e2e-kind-upgrade

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
DEMO_SOURCES := $(shell find pkg/dashboard/frontend/src examples/demo/bundles -type f 2>/dev/null) \
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
