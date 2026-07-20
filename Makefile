SHELL := /bin/sh
GO ?= go
NPM ?= npm
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

.PHONY: all build server agent test test-race cover vet fmt fmt-check web-check docs-install docs-dev docs-build check integration-test docker-build clean

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
	@files="$$(gofmt -l $$(find . -type f -name '*.go' -not -path './vendor/*'))"; test -z "$$files" || { echo 'Go files need formatting. Run make fmt.' >&2; exit 1; }
web-check:
	@node --check web/assets/app.js
	@node --test web/assets/app_test.js
	@test -s web/index.html
	@test -s web/assets/app.css
docs-install: node_modules/.package-lock.json
node_modules/.package-lock.json: package.json package-lock.json
	$(NPM) ci
docs-dev: docs-install
	$(NPM) run docs:dev
docs-build: docs-install
	$(NPM) run docs:build
check: fmt-check vet test web-check docs-build
integration-test:
	./tests/installers_test.sh
	./tests/managed_sshd_image_test.sh
	./tests/server_compose_cold_start_test.sh
	./tests/beginner_compose_conflict_guard_test.sh
	./tests/beginner_compose_lifecycle_test.sh
	./tests/installers_e2e_test.sh
	./tests/two_host_flow_test.sh
docker-build:
	docker build --build-arg VERSION=$(VERSION) -f Dockerfile.server -t portloom-server:$(VERSION) .
	docker build --build-arg VERSION=$(VERSION) -f Dockerfile.agent -t portloom-agent:$(VERSION) .
	docker build -f Dockerfile.sshd -t portloom-sshd:$(VERSION) .
	docker build -f Dockerfile.docs -t portloom-docs:$(VERSION) .
clean:
	rm -rf bin coverage.out docs/.vitepress/dist docs/.vitepress/cache
