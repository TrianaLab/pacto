# CI-specific targets. Included by the main Makefile.
# This file is the single source of truth for all CI quality gates.

.PHONY: ci ci-static ci-test ci-chart ci-fmt ci-vet ci-lint

ci: ci-static ci-test ci-chart

ci-static: ci-fmt ci-vet ci-lint

ci-test: envtest setup-envtest
	@echo "==> Running unit/integration tests with coverage..."
	@KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		go test $$(go list ./... | grep -v /e2e | grep -v /cmd | grep -v /api/ | grep -v /loader | grep -v /test/) -coverprofile=cover.out
	@total=$$(go tool cover -func=cover.out | grep '^total:' | awk '{print $$NF}'); \
	if [ "$$total" != "100.0%" ]; then \
		echo "FAIL: total coverage is $$total, expected 100.0%"; \
		go tool cover -func=cover.out | grep -v '100.0%'; \
		exit 1; \
	fi
	@echo "    total coverage: 100.0%"

ci-chart: crd-sync-check flux-vendor-check helm-deps-check helm-lint helm-template helm-golden helm-unittest helm-schema helm-docs-check docs-generate-check chart-flags-check

.PHONY: chart-flags-check
chart-flags-check: ## Every operator flag has a chart value or a written reason, every registry flag is classified, and the loader tests run.
	@echo "==> Checking every operator flag has a chart value..."
	CGO_ENABLED=0 go test ./cmd -run 'TestEveryOperatorFlagHasAChartValue|TestChartlessFlagsHaveReasons|TestEveryRegistryFlagIsClassified' -count=1
	CGO_ENABLED=0 go test ./internal/loader -count=1

# The chart renders copies of the generated CRDs. helm-golden cannot see a
# stale delivery copy, because delivery is off by default.
CHART_CRDS := charts/pacto-operator/files/crds
.PHONY: crd-sync-check
crd-sync-check:
	@echo "==> Checking the chart's CRD copies match config/crd/bases..."
	@diff -r config/crd/bases $(CHART_CRDS) >/dev/null || \
		{ diff -rq config/crd/bases $(CHART_CRDS); echo "$(CHART_CRDS) does not match config/crd/bases: run make sync-crds" >&2; exit 1; }

ci-fmt:
	@echo "==> Checking formatting..."
	@test -z "$$(gofmt -l .)" || (echo "gofmt found unformatted files:" && gofmt -l . && exit 1)

ci-vet:
	@echo "==> Running go vet..."
	go vet ./...

# Its OWN analysis cache. This module lints with a custom build (the logcheck
# plugin); the engine module lints with the stock binary. Left to the default they
# share one directory under $HOME — which CI additionally restores from an earlier
# commit — so what a linter reports comes to depend on which build wrote the cache
# first and on what the tree looked like when it did. That is how this leg started
# reporting SA5011 against `t.Fatal` guards on CI that no local run, cold cache
# included, could reproduce. A per-module cache makes the answer the code's.
ci-lint: golangci-lint
	@echo "==> Running linter..."
	GOLANGCI_LINT_CACHE="$(LOCALBIN)/.golangci-lint-cache" "$(GOLANGCI_LINT)" run

# Upstream Flux, vendored under files/flux/ by hack/vendor-flux.sh at the
# version files/flux/VERSION names (idp.flux.install). flux-vendor-check is
# offline: every vendored file matches its sha256 line in VERSION (no hand
# edit survives) and VERSION lists nothing missing, every image is a
# ghcr.io/fluxcd release tag, each controller Deployment keeps one container
# (templates/idp/flux.yaml edits the first), upgradeFrom is an earlier release
# of the same major, the docs name the version, and the packaged chart plus the
# idp render fit the release Secret. flux-vendor-verify (network) reruns the
# script and diffs. Bump: hack/vendor-flux.sh <version> <upgrade from>, then
# make helm-golden-update.
FLUX_DIR ?= charts/pacto-operator/files/flux
FLUX_VERSION_DOCS := charts/pacto-operator/README.md docs/profiles.md docs/delivery-setup.md
.PHONY: flux-vendor-check
flux-vendor-check: ## Check the vendored upstream Flux against its VERSION pin.
	@echo "==> Checking the vendored upstream Flux..."
	@set -euo pipefail; dir="$(FLUX_DIR)"; v="$$dir/VERSION"; scratch="$(LOCALBIN)/flux-vendor-check"; \
	fail() { echo "Error: $$*" >&2; exit 1; }; \
	field() { awk -v k="$$1:" '$$1 == k {print $$2}' "$$v"; }; \
	version=$$(field version); from=$$(field upgradeFrom); \
	[ -n "$$version" ] && [ -n "$$from" ] || fail "$$v names no version or upgradeFrom"; \
	sums=$$(grep -E '^[0-9a-f]{64}  ' "$$v" || true); \
	listed=$$(printf '%s\n' "$$sums" | awk 'NF {print $$2}' | sort); \
	present=$$(cd "$$dir" && ls -- *.yaml | sort); \
	[ "$$listed" = "$$present" ] || fail "$$dir and the files VERSION lists differ: rerun hack/vendor-flux.sh"; \
	if command -v sha256sum >/dev/null 2>&1; then sha="sha256sum"; else sha="shasum -a 256"; fi; \
	bad=$$(cd "$$dir" && printf '%s\n' "$$sums" | $$sha -c - 2>/dev/null | grep -v ': OK$$' || true); \
	[ -z "$$bad" ] || fail "a vendored Flux file differs from its sha256 in VERSION (never edit files/flux/, rerun hack/vendor-flux.sh): $$bad"; \
	images=$$(grep -h '^ *image: ' "$$dir"/*.yaml | awk '{print $$2}'); \
	[ -n "$$images" ] || fail "no image in $$dir"; \
	for i in $$images; do printf '%s\n' "$$i" | grep -Eq '^ghcr\.io/fluxcd/[a-z-]+:v[0-9]+\.[0-9]+\.[0-9]+$$' || fail "image $$i is not a ghcr.io/fluxcd release tag"; done; \
	for f in "$$dir"/deployment-*.yaml; do \
		[ "$$(grep -c '^        image: ' "$$f")" = 1 ] || fail "$$f has more than one container: templates/idp/flux.yaml edits only the first"; \
	done; \
	[ "$${version%%.*}" = "$${from%%.*}" ] || fail "upgradeFrom $$from is another major than $$version"; \
	[ "$$from" != "$$version" ] && [ "$$(printf '%s\n%s\n' "$$from" "$$version" | sort -V | head -1)" = "$$from" ] || \
		fail "upgradeFrom $$from is not earlier than $$version"; \
	for d in $(FLUX_VERSION_DOCS); do grep -q "Flux $$version" "$$d" || fail "$$d does not name Flux $$version"; done; \
	mkdir -p "$$scratch"; find "$$scratch" -name '*.tgz' -delete; \
	helm package charts/pacto-operator -d "$$scratch" >/dev/null; \
	helm template pacto charts/pacto-operator -n pacto --set profile=idp --set idp.flux.install=true \
		--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy | gzip -9c >"$$scratch/render.gz"; \
	size=$$(cat "$$scratch"/*.tgz "$$scratch/render.gz" | wc -c | tr -d ' '); \
	echo "    Flux $$version (upgradeFrom $$from); packaged chart plus idp render: $$size bytes"; \
	[ "$$size" -lt 921600 ] || fail "the packaged chart plus the idp render is $$size bytes, over the 900 KiB release Secret budget"
	@# ponytail: the size is an approximation of the release Secret (chart files
	@# and render, 1 MiB cap); read the real Secret in the kind leg if it nears 900 KiB.

.PHONY: flux-vendor-verify
flux-vendor-verify: ## Rerun hack/vendor-flux.sh at the pinned version and diff it against files/flux/ (network).
	@echo "==> Checking files/flux/ is the pinned upstream release..."
	@set -euo pipefail; v="$(FLUX_DIR)/VERSION"; out="$(LOCALBIN)/flux-vendor-verify"; \
	hack/vendor-flux.sh "$$(awk '$$1 == "version:" {print $$2}' "$$v")" "$$(awk '$$1 == "upgradeFrom:" {print $$2}' "$$v")" "$$out" >/dev/null; \
	diff -r "$$out" "$(FLUX_DIR)"

# zot and Dex are optional dependencies, vendored in charts/ and pinned by
# Chart.lock so the chart lints, tests and packages offline. Refresh them with
# `helm dependency update charts/pacto-operator` after a version bump.
.PHONY: helm-deps-check
helm-deps-check: ## Check the vendored dependency archives match Chart.yaml.
	@echo "==> Checking the vendored chart dependencies..."
	@out=$$(helm dependency list charts/pacto-operator); echo "$$out"; \
	! echo "$$out" | awk 'NR>1 && NF && $$NF!="ok"' | grep -q . || \
		{ echo "Error: a vendored dependency is missing or stale: run 'helm dependency update charts/pacto-operator' and commit." >&2; exit 1; }

.PHONY: helm-template
helm-template: ## Render chart templates and validate output.
	@echo "==> Rendering chart templates..."
	helm template pacto-operator charts/pacto-operator --debug > /dev/null
	@echo "==> Rendering with dashboard disabled..."
	helm template pacto-operator charts/pacto-operator --set dashboard.enabled=false > /dev/null
	@echo "==> Rendering with ingress enabled..."
	helm template pacto-operator charts/pacto-operator --set dashboard.ingress.enabled=true > /dev/null
	@echo "==> Rendering with metrics disabled..."
	helm template pacto-operator charts/pacto-operator --set metrics.enabled=false > /dev/null
	@echo "==> Rendering with delivery, with and without the capability check..."
	helm template pacto-operator charts/pacto-operator --set delivery.enabled=true \
		--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy --debug > /dev/null
	helm template pacto-operator charts/pacto-operator --set delivery.enabled=true \
		--set delivery.admissionPolicies.skipCapabilityCheck=true > /dev/null
	@echo "==> Rendering the quickstart profile (bundled zot and Dex)..."
	helm template pacto-operator charts/pacto-operator -n pacto-operator-system -f charts/pacto-operator/values-quickstart.yaml \
		--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy > /dev/null
	@echo "==> Rendering with a namespace list, with it plus delivery, and with a Secret granted by name..."
	helm template pacto-operator charts/pacto-operator --set 'controller.watchNamespaces={payments,ledger}' > /dev/null
	helm template pacto-operator charts/pacto-operator --set 'controller.watchNamespaces={payments,ledger}' \
		--set delivery.enabled=true \
		--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy > /dev/null
	helm template pacto-operator charts/pacto-operator --set 'controller.secretGrants={payments/regcred}' > /dev/null
	@echo "==> The schema refuses an invalid namespace, and watching the node agent namespace fails..."
	! helm template pacto-operator charts/pacto-operator --set 'controller.watchNamespaces={Bad_NS}' > /dev/null 2>&1
	! helm template pacto-operator charts/pacto-operator --set 'controller.watchNamespaces={pacto-node-agent}' > /dev/null 2>&1
	@echo "==> Delivery renders with no identity values, and the deprecated allowEmptyHumanGroups changes nothing..."
	diff <(helm template pacto-operator charts/pacto-operator --set delivery.enabled=true \
		--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy) \
		<(helm template pacto-operator charts/pacto-operator --set delivery.enabled=true --set delivery.identities.allowEmptyHumanGroups=true \
		--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy) > /dev/null
	@echo "==> Delivery refuses the removed admission policy keys..."
	! helm template pacto-operator charts/pacto-operator --set delivery.enabled=true \
		--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy \
		--set delivery.admissionPolicies.allowUnguarded=true > /dev/null 2>&1
	@echo "==> Helm reads values as YAML 1.1: a bare on is true, a bare kernel evidence off is false and follows the node agent..."
	helm template pacto-operator charts/pacto-operator -f charts/pacto-operator/tests/values/probing-bare-on.yaml | grep -q -- '- --enable-probing=on$$'
	helm template pacto-operator charts/pacto-operator -f charts/pacto-operator/tests/values/kernel-bare-off.yaml | grep -q -- '- --enable-kernel-evidence$$'

# The default render is byte-identical to the committed golden (versions
# normalised, so a release bump does not move it), and turning delivery on
# changes none of the operator's own identity or RBAC objects. Refresh the golden
# with `make helm-golden-update` when a default-render change is intended.
GOLDEN := charts/pacto-operator/tests/golden/default.yaml
GOLDEN_RENDER = v=$$(awk '/^version:/{print $$2}' charts/pacto-operator/Chart.yaml); \
	helm template pacto-operator charts/pacto-operator | sed "s/$$v/VERSION/g"
# The Flux-only golden: the vendored upstream Flux as templates/idp/flux.yaml
# renders it. It changes only with a Flux bump or a change to that template.
FLUX_GOLDEN := charts/pacto-operator/tests/golden/idp-flux.yaml
FLUX_GOLDEN_RENDER = v=$$(awk '/^version:/{print $$2}' charts/pacto-operator/Chart.yaml); \
	helm template pacto-operator charts/pacto-operator --set profile=idp --set idp.flux.install=true \
		--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy --show-only templates/idp/flux.yaml | sed "s/$$v/VERSION/g"
OPERATOR_IDENTITY := -s templates/serviceaccount.yaml -s templates/clusterrole.yaml -s templates/clusterrolebinding.yaml \
	-s templates/role.yaml -s templates/leader-election-role.yaml

.PHONY: helm-golden
helm-golden: ## Prove the default render is unchanged and delivery leaves the operator's RBAC alone.
	@echo "==> Checking the default render against the golden..."
	@# -B: Helm 4.3 puts a blank line before each document separator and 4.0 does not;
	@# a blank line means nothing in YAML, and every other change still fails.
	@$(GOLDEN_RENDER) | diff -u -B $(GOLDEN) - || \
		{ echo "Error: the default render changed. If intended, run 'make helm-golden-update' and commit." >&2; exit 1; }
	@echo "==> Checking the Flux-only render against its golden..."
	@$(FLUX_GOLDEN_RENDER) | diff -u -B $(FLUX_GOLDEN) - || \
		{ echo "Error: the idp.flux.install render changed. If intended (a Flux bump), run 'make helm-golden-update' and commit." >&2; exit 1; }
	@echo "==> Checking delivery.enabled leaves the operator's ServiceAccount and RBAC unchanged..."
	@diff -u <(helm template pacto-operator charts/pacto-operator $(OPERATOR_IDENTITY)) \
		<(helm template pacto-operator charts/pacto-operator $(OPERATOR_IDENTITY) --set delivery.enabled=true \
			--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy)

.PHONY: helm-golden-update
helm-golden-update: ## Rewrite the default-render and Flux-only goldens.
	@$(GOLDEN_RENDER) > $(GOLDEN)
	@$(FLUX_GOLDEN_RENDER) > $(FLUX_GOLDEN)

.PHONY: helm-unittest
helm-unittest: $(HELM_UNITTEST) ## Run Helm unit tests.
	@echo "==> Running Helm unit tests..."
	"$(HELM_UNITTEST)" charts/pacto-operator

.PHONY: helm-schema
helm-schema: ## Validate each chart's values.yaml against its values.schema.json.
	@echo "==> Validating chart schemas..."
	@command -v check-jsonschema >/dev/null 2>&1 || pip install check-jsonschema --quiet
	@for c in pacto-operator; do \
		python3 -c "import json; json.load(open('charts/$$c/values.schema.json'))" || \
			{ echo "Error: charts/$$c/values.schema.json is not valid JSON." >&2; exit 1; }; \
		check-jsonschema --schemafile charts/$$c/values.schema.json charts/$$c/values*.yaml || exit 1; \
	done

.PHONY: helm-docs-check
helm-docs-check: ## Check that helm-docs output matches committed README.
	@echo "==> Checking helm-docs drift..."
	@command -v helm-docs >/dev/null 2>&1 || { echo "Error: helm-docs not installed. Install with: go install github.com/norwoodj/helm-docs/cmd/helm-docs@latest" >&2; exit 1; }
	@helm-docs --chart-search-root charts
	@# EVERY chart's README, not just the operator's. helm-docs regenerates all of
	@# them in one pass, so a narrower check means the gate itself rewrites a chart
	@# README on every run and reports success -- and a chart whose README was
	@# hand-written silently loses it.
	@git diff --exit-code -- charts/*/README.md || \
		{ echo "Error: A Helm chart README is out of date. Run 'make helm-docs' and commit." >&2; exit 1; }

.PHONY: docs-generate-check
docs-generate-check: docs-generate ## Check that generated reference docs match committed output.
	@echo "==> Checking generated docs drift..."
	@git diff --exit-code docs/generated/ || \
		{ echo "Error: Generated reference docs are out of date. Run 'make docs-generate' and commit." >&2; exit 1; }
