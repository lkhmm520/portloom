SHELL := /bin/sh

GO ?= go
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

.PHONY: all build server agent test test-race cover vet fmt fmt-check web-check check docker-build clean

all: check build

build: server agent

server:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -ldflags '$(LDFLAGS)' -o bin/portloom-server ./cmd/server

agent:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -ldflags '$(LDFLAGS)' -o bin/portloom-agent ./cmd/agent

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

vet:
	$(GO) vet ./...

fmt:
	gofmt -w $$(find . -type f -name '*.go' -not -path './vendor/*')

fmt-check:
	@test -z "$$(gofmt -l $$(find . -type f -name '*.go' -not -path './vendor/*'))" || \
		(printf '%s\n' 'Go files need formatting. Run make fmt.' >&2; exit 1)

web-check:
	@node --check web/assets/app.js
	@node --test web/assets/app_test.js
	@test -s web/index.html
	@test -s web/assets/app.css

check: fmt-check vet test web-check

docker-build:
	docker build -f Dockerfile.server -t portloom-server:$(VERSION) .
	docker build -f Dockerfile.agent -t portloom-agent:$(VERSION) .

clean:
	rm -rf bin coverage.out
