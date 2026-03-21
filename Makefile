VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
GOBIN := $(shell go env GOBIN 2>/dev/null)
ifeq ($(GOBIN),)
GOPATH := $(shell go env GOPATH 2>/dev/null)
ifeq ($(GOPATH),)
GOPATH := $(HOME)/go
endif
GOBIN := $(GOPATH)/bin
endif

.PHONY: build test e2e coverage lint clean docs gen-cli-docs ci ci-test ci-static

build:
	rm -f "$(GOBIN)/pacto"
	go build $(LDFLAGS) -o "$(GOBIN)/pacto" ./cmd/pacto

test:
	go test ./... -v

e2e:
	go test -tags e2e ./tests/e2e/ -v -count=1 -parallel 16 -timeout 120s

coverage:
	go test $(shell go list ./... | grep -v /tests/ | grep -v /testutil | grep -v /cmd/gendocs) -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@go tool cover -func=coverage.out | tail -1

lint:
	gofmt -s -l $(shell find . -name '*.go')
	go vet ./...

gen-cli-docs:
	go run ./cmd/gendocs/

BUNDLE := $(shell command -v /opt/homebrew/opt/ruby@3.3/bin/bundle 2>/dev/null || command -v /opt/homebrew/opt/ruby/bin/bundle 2>/dev/null || command -v bundle 2>/dev/null)

docs:
	cd docs && $(BUNDLE) install && $(BUNDLE) exec jekyll serve --livereload

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
	gocyclo -over 15 $$(find . -name '*.go' ! -name '*_test.go' ! -path './vendor/*')

ci-lint:
	@echo "==> Running linter..."
	golangci-lint run

ci-docs:
	@echo "==> Checking CLI docs are up to date..."
	@go run ./cmd/gendocs/
	@git diff --exit-code docs/cli-reference.md || (echo "CLI docs are out of date. Run 'make gen-cli-docs' and commit." && exit 1)

clean:
	rm -f "$(GOBIN)/pacto" coverage.out coverage.html
