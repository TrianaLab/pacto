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

ci-chart: helm-lint helm-template helm-golden helm-unittest helm-schema helm-docs-check docs-generate-check

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
	@echo "==> Rendering with delivery, with and without admission policies..."
	helm template pacto-operator charts/pacto-operator --set delivery.enabled=true \
		--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy --debug > /dev/null
	helm template pacto-operator charts/pacto-operator --set delivery.enabled=true \
		--set delivery.admissionPolicies.enabled=false --set delivery.admissionPolicies.allowUnguarded=true > /dev/null
	@echo "==> Delivery refuses to render unguarded by accident..."
	! helm template pacto-operator charts/pacto-operator --set delivery.enabled=true \
		--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy \
		--set delivery.admissionPolicies.enabled=false > /dev/null 2>&1

# The default render is byte-identical to the committed golden (versions
# normalised, so a release bump does not move it), and turning delivery on
# changes none of the operator's own identity or RBAC objects. Refresh the golden
# with `make helm-golden-update` when a default-render change is intended.
GOLDEN := charts/pacto-operator/tests/golden/default.yaml
GOLDEN_RENDER = v=$$(awk '/^version:/{print $$2}' charts/pacto-operator/Chart.yaml); \
	helm template pacto-operator charts/pacto-operator | sed "s/$$v/VERSION/g"
OPERATOR_IDENTITY := -s templates/serviceaccount.yaml -s templates/clusterrole.yaml -s templates/clusterrolebinding.yaml \
	-s templates/role.yaml -s templates/leader-election-role.yaml

.PHONY: helm-golden
helm-golden: ## Prove the default render is unchanged and delivery leaves the operator's RBAC alone.
	@echo "==> Checking the default render against the golden..."
	@$(GOLDEN_RENDER) | diff -u $(GOLDEN) - || \
		{ echo "Error: the default render changed. If intended, run 'make helm-golden-update' and commit." >&2; exit 1; }
	@echo "==> Checking delivery.enabled leaves the operator's ServiceAccount and RBAC unchanged..."
	@diff -u <(helm template pacto-operator charts/pacto-operator $(OPERATOR_IDENTITY)) \
		<(helm template pacto-operator charts/pacto-operator $(OPERATOR_IDENTITY) --set delivery.enabled=true \
			--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy)

.PHONY: helm-golden-update
helm-golden-update: ## Rewrite the default-render golden.
	@$(GOLDEN_RENDER) > $(GOLDEN)

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
		check-jsonschema --schemafile charts/$$c/values.schema.json charts/$$c/values.yaml || exit 1; \
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
