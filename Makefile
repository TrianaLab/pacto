VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
GOBIN := $(shell go env GOBIN 2>/dev/null)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: build test e2e coverage lint clean docs

build:
	rm -f "$(GOBIN)/pacto"
	go build $(LDFLAGS) -o "$(GOBIN)/pacto" ./cmd/pacto

test:
	go test ./... -v

e2e:
	go test -tags e2e ./tests/e2e/ -v -count=1 -timeout 120s

coverage:
	go test $(shell go list ./... | grep -v /tests/ | grep -v /testutil) -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@go tool cover -func=coverage.out | tail -1

lint:
	gofmt -s -l $(shell find . -name '*.go' -not -path './plugins/*')
	go vet ./...

BUNDLE := $(shell command -v /opt/homebrew/opt/ruby@3.3/bin/bundle 2>/dev/null || command -v /opt/homebrew/opt/ruby/bin/bundle 2>/dev/null || command -v bundle 2>/dev/null)

docs:
	cd docs && $(BUNDLE) install && $(BUNDLE) exec jekyll serve --livereload

clean:
	rm -f "$(GOBIN)/pacto" coverage.out coverage.html
