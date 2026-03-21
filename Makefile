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

.PHONY: build test e2e coverage lint clean docs gen-cli-docs

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

clean:
	rm -f "$(GOBIN)/pacto" coverage.out coverage.html

include ci.mk
