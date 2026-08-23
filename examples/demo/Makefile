# Pacto dashboard — WebAssembly demo build.
#
# Produces a fully static site in $(DIST): the Pacto engine compiled to wasm, the
# dashboard UI rebuilt for the target base path, and the glue that routes the
# UI's API calls into wasm. No backend, no live OCI.
#
# Assets are referenced with a RELATIVE base (./), so the built site works at
# any mount point (root, /demo/, /pacto/demo/, …) with no domain assumptions.
# Override BASE only if you need absolute asset URLs for a specific deploy.
#
#   make build                 # relative base (works anywhere)
#   make serve                 # preview locally
#
# Bundles live in ./bundles and are embedded directly (no copy step). The UI is
# built from this repo's frontend source into a scratch dir and copied into
# $(DIST) — the committed pkg/dashboard/ui build is never touched.

BASE        ?= ./
DIST        ?= dist
GOROOT      := $(shell go env GOROOT)
SERVE_PORT  ?= 8088
FRONTEND    := $(abspath ../../pkg/dashboard/frontend)
UI_BUILD    := $(abspath .ui-build)
# Pacto CORE version this demo is built from — the latest core release tag,
# surfaced in the dashboard navbar via -ldflags into the wasm. `--match 'v[0-9]*'`
# restricts to core "vX.Y.Z" tags so the k8s submodule tag
# (integrations/kubernetes/vX.Y.Z), which git describe would otherwise pick as the
# most recent, never leaks into the navbar. Uses --abbrev=0 for a clean label.
# Needs tags available (CI must checkout with fetch-depth: 0); falls back to a
# short SHA, then "dev", when no tags are present.
VERSION     ?= $(shell git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || echo dev)

.PHONY: build preflight ui wasm wasmexec inject serve test validate smoke clean diff breaking-change evolution graph explain demo-locks

build: preflight ui wasm wasmexec inject
	@echo "==> Built '$(DIST)' (base $(BASE))"

## Fail fast with clear guidance if a previous `sudo make` left root-owned
## artifacts a normal-user build can't overwrite. This build never needs root.
preflight:
	@bad=$$(find $(DIST) $(UI_BUILD) -user root 2>/dev/null | head -1); \
	if [ -n "$$bad" ]; then \
	  echo "ERROR: root-owned build artifacts found (from a previous 'sudo make')."; \
	  echo "       This build does NOT need root. Remove them once with:"; \
	  echo "         sudo rm -rf examples/demo/dist examples/demo/.ui-build"; \
	  echo "       then re-run 'make build' WITHOUT sudo."; \
	  exit 1; \
	fi

## Rebuild the dashboard UI from this repo's frontend source with the correct
## base path, into a scratch dir, then copy into $(DIST). NEVER writes to
## pkg/dashboard/ui (the committed base-/ build used by the real server).
ui:
	@rm -rf $(DIST) $(UI_BUILD) && mkdir -p $(DIST)
	cd $(FRONTEND) && npm ci --no-audit --no-fund && npx vite build --base=$(BASE) --emptyOutDir --outDir $(UI_BUILD)
	@cp -r $(UI_BUILD)/. $(DIST)/
	@echo "==> Built UI (base $(BASE))"

## Compile the engine to wasm. Bundles are embedded via //go:embed bundles.
## The Pacto version is baked in so /health (and thus the navbar) reports it.
wasm:
	@mkdir -p $(DIST)
	GOOS=js GOARCH=wasm go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(DIST)/app.wasm .
	@echo "==> Built $(DIST)/app.wasm ($$(du -h $(DIST)/app.wasm | cut -f1), version $(VERSION))"

## Copy Go's wasm runtime glue (Go 1.24+ lib/wasm location).
wasmexec:
	@mkdir -p $(DIST)
	@cp "$(GOROOT)/lib/wasm/wasm_exec.js" $(DIST)/wasm_exec.js
	@echo "==> Copied wasm_exec.js"

## Inject the wasm loader before the app's module script, add the no-JavaScript
## fallback and the SPA 404 fallback, and stamp the build. The engine assets
## (app.wasm, boot.js, wasm_exec.js) have stable names, so without a cache-bust
## query a browser would serve a stale cached engine against a freshly-hashed UI
## after a redeploy (new UI calling an old wasm). A content token derived from the
## built wasm busts all three together, so a new engine is picked up immediately.
##
## The <noscript> block goes in at build time rather than in the frontend's
## index.html, because it is specific to this demo: the real dashboard is served
## by a Go backend, this one IS the engine. Its link matches boot.js's DOCS_HREF —
## the demo deploys at /<version>/demo/ inside every mike version, so ../ reaches
## the docs for the same version. Idempotent.
inject:
	@bust=$$(git hash-object $(DIST)/app.wasm | cut -c1-12); \
	  sed "s|\"app.wasm\"|\"app.wasm?v=$$bust\"|" boot.js > $(DIST)/boot.js; \
	  grep -q 'boot.js' $(DIST)/index.html || { awk -v base="$(BASE)" -v bust="$$bust" '\
	    /<script type="module"/ && !done { \
	      print "  <script src=\"" base "wasm_exec.js?v=" bust "\"></script>"; \
	      print "  <script src=\"" base "boot.js?v=" bust "\"></script>"; \
	      done=1 \
	    } \
	    /<\/body>/ && !ns { \
	      print "  <noscript>"; \
	      print "    <div style=\"max-width:40rem;margin:0 auto;padding:4rem 1.5rem;color:var(--c-text,#f1f5f9);font-family:system-ui,sans-serif;line-height:1.6\">"; \
	      print "      <h1 style=\"font-size:1.5rem;margin:0 0 1rem\">Pacto dashboard demo</h1>"; \
	      print "      <p>This demo has no backend. The Pacto engine is compiled to WebAssembly and runs inside this tab, so it needs JavaScript enabled. Nothing you do in it leaves your browser.</p>"; \
	      print "      <p><a href=\"../examples/dashboard-demo/\" style=\"color:var(--c-accent,#818cf8)\">Read what the demo shows</a> — the same tour in prose, no JavaScript required.</p>"; \
	      print "    </div>"; \
	      print "  </noscript>"; \
	      ns=1 \
	    } { print }' $(DIST)/index.html > $(DIST)/index.html.tmp && mv -f $(DIST)/index.html.tmp $(DIST)/index.html; }; \
	  cp $(DIST)/index.html $(DIST)/404.html; \
	  printf '{"base":"%s","version":"%s","build":"%s"}\n' "$(BASE)" "$(VERSION)" "$$bust" > $(DIST)/version.json
	@echo "==> Injected loader (version $(VERSION)), 404.html, cache-busted engine, version.json"

## Regenerate the committed pacto.lock + .pactoignore for the dep-bearing demo
## bundles, OFFLINE and DETERMINISTICALLY (no live registry). Re-running this is
## byte-identical, so the committed demo data stays stable. Run after changing a
## demo bundle's content or its dependency/reference set, then commit the result.
## See README ("Demo lockfiles") for the content-hash pin caveat.
demo-locks:
	go run ./genlocks
	@echo "==> Regenerated demo lockfiles (commit the changes)"

## Run the EmbedSource host tests.
test:
	go test ./...

## Run the FULL Pacto validator over every demo bundle (every service and every
## version) OFFLINE, resolving refs by service name within ./bundles. Fails if any
## contract is not valid v2. This is the offline proof that the demo contracts are
## valid; the live `pacto validate` against ghcr.io only passes once the release
## republishes the demo bundles as v2 (a production publish, out of scope here).
validate:
	go run ./validatebundles

## Exercise the built wasm engine end-to-end in Node (needs `make build` first).
smoke:
	node smoke.mjs

## Preview locally under the same base path the site is deployed at.
serve:
	@rm -rf .serve && mkdir -p ".serve$(BASE)" && cp -r $(DIST)/. ".serve$(BASE)"
	@echo "==> Open http://localhost:$(SERVE_PORT)$(BASE)"
	@cd .serve && python3 -m http.server $(SERVE_PORT)

## -- CLI demo narrative (published demo contracts) -------------------------
## These use the locally built `pacto` (run `make build` at the repo root first)
## against the demo contracts published at $(DEMO_REPO), so they need network.
##
## They deliberately do NOT point at ./bundles. Those bundles carry the locks
## genlocks writes, whose pins are content hashes of pacto.yaml (see the header
## of ./genlocks) rather than OCI manifest digests. Any command that verifies a
## committed lock -- diff, validate, graph, push -- re-resolves the closure over
## the network and hard-fails on the mismatch, so a local-path diff can only ever
## print LOCK_DIGEST_MISMATCH. Lock verification is skipped for an oci:// ref,
## which is why the published coordinates work and the paths do not.
DEMO_REPO ?= ghcr.io/trianalab/pacto
PAYMENTS  := oci://$(DEMO_REPO)/payments-service

## Compare two contracts: make diff OLD=<ref> NEW=<ref>
diff:
	@pacto diff $(OLD) $(NEW)

## Show the payments-service breaking change (1.2.0 -> 2.0.0).
## `pacto diff` exits 1 on BREAKING *and* on any resolution failure, so the exit
## code cannot tell the two apart -- assert on the classification instead, or a
## broken demo goes green while printing an error.
breaking-change:
	@out=$$(pacto diff $(PAYMENTS):1.2.0 $(PAYMENTS):2.0.0 --output-format markdown 2>&1); \
	 echo "$$out"; \
	 echo "$$out" | grep -q '^\*\*Classification:\*\* `BREAKING`' \
	   || { echo "==> expected a BREAKING classification" >&2; exit 1; }

## Show a smaller evolution (1.0.0 -> 1.1.0): additive paths and a new config
## key, but a tightened description, so Pacto classifies it POTENTIAL_BREAKING.
evolution:
	@out=$$(pacto diff $(PAYMENTS):1.0.0 $(PAYMENTS):1.1.0 --output-format markdown 2>&1); \
	 echo "$$out"; \
	 echo "$$out" | grep -q '^\*\*Classification:\*\* `POTENTIAL_BREAKING`' \
	   || { echo "==> expected a POTENTIAL_BREAKING classification" >&2; exit 1; }

## Resolve the dependency closure of a published contract: make graph REF=<ref>
graph:
	@pacto graph $(or $(REF),$(PAYMENTS):2.1.1)

## Print a human-readable summary of a contract: make explain REF=<path or ref>
## `explain` does not verify locks, so a local bundle path works here.
explain:
	@pacto explain $(REF)

clean:
	@rm -rf $(DIST) .serve $(UI_BUILD)
	@echo "==> Cleaned"
