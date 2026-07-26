# ──────────────────────────────────────────────────────────────────────────────
# Helper macros — must be defined before any variable that references them.
# ──────────────────────────────────────────────────────────────────────────────

# gomodver extracts the version of a Go module from go.mod, respecting replace directives.
define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

# ──────────────────────────────────────────────────────────────────────────────
# Build metadata
# ──────────────────────────────────────────────────────────────────────────────

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# Dashboard image tag. The monorepo release manifest is the single source of
# truth for published versions. `go list -m github.com/trianalab/pacto/v2`
# returns empty under the same-repo go.work workspace (it resolves to the
# workspace copy, which has no version), so it CANNOT supply the release tag and
# would bake an untagged dashboard image. Use the release manifest when present
# (monorepo), and fall back to go list only for a standalone operator checkout,
# where the module genuinely has a resolved version.
PACTO_MANIFEST := $(firstword $(wildcard ../../release/release-manifest.json release/release-manifest.json))
ifneq ($(PACTO_MANIFEST),)
PACTO_VERSION := $(shell node -e 'const fs=require("fs");process.stdout.write(JSON.parse(fs.readFileSync("$(PACTO_MANIFEST)","utf8")).units["dashboard-image"].version)')
else
PACTO_VERSION := $(shell echo '$(call gomodver,github.com/trianalab/pacto/v2)' | sed 's/^v//')
endif
DASHBOARD_IMG := ghcr.io/trianalab/pacto-dashboard:$(PACTO_VERSION)

# Ldflags for version injection
LDFLAGS := -ldflags "-s -w \
  -X main.version=$(VERSION) \
  -X main.gitCommit=$(GIT_COMMIT) \
  -X main.buildDate=$(BUILD_DATE) \
  -X main.dashboardImage=$(DASHBOARD_IMG)"

# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/trianalab/pacto-operator/pacto-controller:$(VERSION)

# Namespace used when running locally (defaults to HELM_NAMESPACE)
POD_NAMESPACE ?= $(HELM_NAMESPACE)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development — Local Process

# Common prerequisite that validates the dashboard image tag is non-empty.
.PHONY: check-dashboard-image
check-dashboard-image:
	@if [ -z "$(PACTO_VERSION)" ]; then \
		echo "Error: Could not resolve Pacto version from go.mod." >&2; \
		echo "       Ensure 'github.com/trianalab/pacto' is listed in go.mod and 'go list -m' works." >&2; \
		exit 1; \
	fi
	@echo "Dashboard image: $(DASHBOARD_IMG)"

.PHONY: run
run: manifests generate fmt vet ## Run the operator locally (no dashboard).
	POD_NAMESPACE=$(POD_NAMESPACE) go run $(LDFLAGS) ./cmd/main.go

.PHONY: run-with-dashboard
run-with-dashboard: manifests generate fmt vet check-dashboard-image ## Run the operator locally with the dashboard enabled.
	POD_NAMESPACE=$(POD_NAMESPACE) go run $(LDFLAGS) ./cmd/main.go --enable-dashboard

##@ Development — Local Kubernetes

# deploy-local and deploy-local-with-dashboard work with any kube context
# (Docker Desktop, minikube, Kind, etc.). If you use Kind, run `make kind-load`
# first so the image is available inside the cluster.

KIND_CLUSTER ?= pacto-operator-dev

.PHONY: deploy-local
deploy-local: docker-build install deploy ## Build image, install CRDs, and deploy the operator to the current kube context.
	@echo ""
	@echo "Operator deployed (dashboard disabled)."
	@echo "Image: $(IMG)"

.PHONY: deploy-local-with-dashboard
deploy-local-with-dashboard: check-dashboard-image docker-build install deploy-with-dashboard-args ## Build image, install CRDs, and deploy the operator with dashboard to the current kube context.
	@echo ""
	@echo "Operator deployed (dashboard enabled)."
	@echo "Image: $(IMG)"
	@echo "Dashboard image: $(DASHBOARD_IMG)"

.PHONY: kind-load
kind-load: ## Load the controller image into the active Kind cluster (run before deploy-local if using Kind).
	$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER)

.PHONY: deploy-kind
deploy-kind: docker-build kind-load install deploy ## Build, load into Kind, install CRDs, and deploy the operator.
	@echo ""
	@echo "Operator deployed to Kind cluster '$(KIND_CLUSTER)' (dashboard disabled)."
	@echo "Image: $(IMG)"

.PHONY: deploy-with-dashboard-args
deploy-with-dashboard-args: manifests kustomize
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	cd config/default && cp kustomization.yaml kustomization.yaml.bak && \
		"$(KUSTOMIZE)" edit add patch --path manager_dashboard_patch.yaml --kind Deployment
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -
	cd config/default && mv kustomization.yaml.bak kustomization.yaml

.PHONY: undeploy-local
undeploy-local: undeploy ## Remove the operator from the current kube context.

##@ Development — Dependency Management

.PHONY: pacto-local
pacto-local: ## Use the local ../pacto checkout as the pacto dependency (for co-development).
	@if ! grep -q 'replace github.com/trianalab/pacto/v2 =>' go.mod; then \
		go mod edit -replace github.com/trianalab/pacto/v2=../pacto && \
		echo "✓ Switched to local pacto (../pacto)"; \
	else \
		echo "Already using local pacto"; \
	fi

.PHONY: pacto-remote
pacto-remote: ## Switch back to the released pacto module (removes local replace).
	@if grep -q 'replace github.com/trianalab/pacto/v2 =>' go.mod; then \
		go mod edit -dropreplace github.com/trianalab/pacto/v2 && \
		go mod tidy && \
		echo "✓ Switched to remote pacto ($$(go list -m -f '{{.Version}}' github.com/trianalab/pacto/v2))"; \
	else \
		echo "Already using remote pacto"; \
	fi

##@ Development — Common

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list -f '{{if .TestGoFiles}}{{.ImportPath}}{{end}}' ./... | grep -v /e2e) -coverprofile cover.out

KIND_CLUSTER_E2E ?= pacto-operator-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER_E2E)"*) \
			echo "Kind cluster '$(KIND_CLUSTER_E2E)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER_E2E)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER_E2E) ;; \
	esac

.PHONY: test-integration
test-integration: ## Run integration tests (in-process OCI registry, no cluster needed).
	go test -tags=integration ./test/integration/ -v -timeout 120s

.PHONY: test-e2e
test-e2e: manifests generate fmt vet setup-envtest ## Run the Pacto 2.0 e2e acceptance matrix against envtest.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test -tags=e2e ./test/e2e/... -count=1 -v

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER_E2E)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build $(LDFLAGS) -o bin/manager cmd/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg DASHBOARD_IMAGE=$(DASHBOARD_IMG) \
		-t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name pacto-operator-builder
	$(CONTAINER_TOOL) buildx use pacto-operator-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm pacto-operator-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

##@ Chart

.PHONY: sync-crds
sync-crds: manifests ## Copy generated CRDs into the Helm chart.
	cp config/crd/bases/*.yaml charts/pacto-operator/crds/

.PHONY: helm-docs
helm-docs: ## Generate Helm chart documentation with helm-docs.
	@command -v helm-docs >/dev/null 2>&1 || { echo "helm-docs not installed: go install github.com/norwoodj/helm-docs/cmd/helm-docs@latest"; exit 1; }
	helm-docs --chart-search-root charts

.PHONY: docs-generate
docs-generate: ## Regenerate this integration's reference docs from real sources.
	python3 ../../release/scripts/gen_integration_docs.py --integration-dir .

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart.
	helm lint charts/pacto-operator

HELM_RELEASE ?= pacto-operator
HELM_NAMESPACE ?= pacto-operator-system

# Local helm targets use LoadBalancer for the dashboard Service so the
# dashboard is immediately accessible without port-forward. The chart
# default remains ClusterIP — this override only affects local installs.
HELM_DASHBOARD_SERVICE_TYPE ?= LoadBalancer

.PHONY: helm-install
helm-install: docker-build ## Build image and install the Helm chart to the current kube context.
	@CURRENT_CONTEXT=$$(kubectl config current-context 2>/dev/null || echo "none"); \
	if [ "$$CURRENT_CONTEXT" = "none" ]; then \
		echo "Error: No Kubernetes context found." >&2; exit 1; \
	fi; \
	echo "==> Using cluster: $$CURRENT_CONTEXT"; \
	echo "==> Installing $(HELM_RELEASE) with image $(IMG)..."; \
	helm install $(HELM_RELEASE) charts/pacto-operator \
		--namespace $(HELM_NAMESPACE) \
		--create-namespace \
		--set image.repository=$(shell echo $(IMG) | cut -d: -f1) \
		--set image.tag=$(VERSION) \
		--set image.pullPolicy=Never \
		--set dashboard.service.type=$(HELM_DASHBOARD_SERVICE_TYPE) \
		--wait --timeout 5m

.PHONY: helm-upgrade
helm-upgrade: docker-build ## Build image and upgrade the Helm release.
	@if ! helm status $(HELM_RELEASE) -n $(HELM_NAMESPACE) >/dev/null 2>&1; then \
		echo "Error: Release $(HELM_RELEASE) not found. Use 'make helm-install' first." >&2; exit 1; \
	fi
	@echo "==> Upgrading $(HELM_RELEASE) with image $(IMG)..."
	helm upgrade $(HELM_RELEASE) charts/pacto-operator \
		--namespace $(HELM_NAMESPACE) \
		--set image.repository=$(shell echo $(IMG) | cut -d: -f1) \
		--set image.tag=$(VERSION) \
		--set image.pullPolicy=Never \
		--set dashboard.service.type=$(HELM_DASHBOARD_SERVICE_TYPE) \
		--wait --timeout 5m

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall the Helm release.
	@helm uninstall $(HELM_RELEASE) --namespace $(HELM_NAMESPACE) 2>/dev/null || echo "Release $(HELM_RELEASE) not found"

.PHONY: helm-reinstall
helm-reinstall: helm-uninstall helm-install ## Uninstall and reinstall the Helm chart.

.PHONY: helm-status
helm-status: ## Show Helm release status.
	@helm status $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-history
helm-history: ## Show Helm release history.
	@helm history $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-get-values
helm-get-values: ## Get values for the deployed Helm release.
	@helm get values $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

##@ Deployment (Kustomize)

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: deploy-samples
deploy-samples: kustomize ## Deploy sample infrastructure and Pacto CRs.
	"$(KUSTOMIZE)" build config/samples/demo | "$(KUBECTL)" apply -f -

.PHONY: undeploy-samples
undeploy-samples: kustomize ## Remove sample Pacto CRs and infrastructure.
	"$(KUSTOMIZE)" build config/samples/demo | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
HELM_UNITTEST ?= $(LOCALBIN)/helm-unittest
CRD_REF_DOCS ?= $(LOCALBIN)/crd-ref-docs

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.20.1
CRD_REF_DOCS_VERSION ?= v0.3.0

ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')

ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.8.0
HELM_UNITTEST_VERSION ?= 0.7.2

# Project Go toolchain version, derived from go.mod (e.g. "1.26.0").
GO_VERSION ?= $(shell awk '$$1=="go" {print $$2; exit}' go.mod)
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	@# Force the local Go toolchain so the binary embeds runtime.Version() matching
	@# the project's go directive. golangci-lint policy lags Go releases by one minor
	@# version, so its module declares an older `go` directive; without this override
	@# `go install` would download that older toolchain and the resulting binary would
	@# refuse to lint code targeting a newer Go version.
	GOTOOLCHAIN=go$(GO_VERSION) $(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	@test -f .custom-gcl.yml && { \
		echo "Building custom golangci-lint with plugins..." && \
		GOTOOLCHAIN=go$(GO_VERSION) $(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	} || true

.PHONY: crd-ref-docs
crd-ref-docs: $(CRD_REF_DOCS) ## Download crd-ref-docs locally if necessary.
$(CRD_REF_DOCS): $(LOCALBIN)
	$(call go-install-tool,$(CRD_REF_DOCS),github.com/elastic/crd-ref-docs,$(CRD_REF_DOCS_VERSION))

.PHONY: helm-unittest-install
helm-unittest-install: $(HELM_UNITTEST) ## Download helm-unittest locally if necessary.
$(HELM_UNITTEST): $(LOCALBIN)
	@[ -f "$(HELM_UNITTEST)" ] || { \
		echo "Downloading helm-unittest $(HELM_UNITTEST_VERSION)..." ; \
		os=$$(uname -s | sed 's/Darwin/macos/' | sed 's/Linux/linux/') ; \
		arch=$$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/') ; \
		curl -sSL "https://github.com/helm-unittest/helm-unittest/releases/download/v$(HELM_UNITTEST_VERSION)/helm-unittest-$${os}-$${arch}-$(HELM_UNITTEST_VERSION).tgz" | \
			tar xz -C "$(LOCALBIN)" untt ; \
		mv "$(LOCALBIN)/untt" "$(HELM_UNITTEST)" ; \
	}

include ci.mk
