SHELL := /bin/sh

# Optional local-only overrides. .local/ is excluded from the repo and build context.
# Example: GOPROXY=https://your-go-module-mirror
-include .local/env.mk

BINARY := windwatch
DIST := dist
IMAGE ?= windwatch:latest
GOPROXY ?= $(if $(WINDWATCH_GOPROXY),$(WINDWATCH_GOPROXY),https://proxy.golang.org,direct)
export GOPROXY

VERSION ?= $(shell tr -d '\r' < VERSION 2>/dev/null || echo 0.0.0)
VERSION_TAG := v$(VERSION)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

GO_BUILD_FLAGS := -trimpath -buildvcs=false
LDFLAGS := -s -w \
	-X github.com/fynhq/windwatch/internal/version.Version=$(VERSION_TAG) \
	-X github.com/fynhq/windwatch/internal/version.Commit=$(COMMIT) \
	-X github.com/fynhq/windwatch/internal/version.BuildTime=$(BUILD_TIME)

.PHONY: all build dist docker-build docker-up docker-down fmt fmt-check vet test race check version clean

all: check build

build:
	CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/windwatch

dist: check
	rm -rf $(DIST)
	mkdir -p $(DIST)
	@set -e; for arch in amd64 arm64; do \
		echo "building linux/$$arch"; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build $(GO_BUILD_FLAGS) -ldflags '$(LDFLAGS)' -o $(DIST)/windwatch-linux-$$arch-$(VERSION_TAG) ./cmd/windwatch; \
	done
	cd $(DIST) && sha256sum windwatch-linux-amd64-$(VERSION_TAG) windwatch-linux-arm64-$(VERSION_TAG) > SHA256SUMS
	@echo "artifacts:"; ls -lh $(DIST)

docker-build:
	docker build \
		--build-arg VERSION="$(VERSION_TAG)" \
		--build-arg COMMIT="$(COMMIT)" \
		--build-arg BUILD_TIME="$(BUILD_TIME)" \
		--build-arg GOPROXY="$(GOPROXY)" \
		-t "$(IMAGE)" .

docker-up:
	@test -f compose.yaml || { echo "compose.yaml not found; copy compose.example.yaml first" >&2; exit 1; }
	WINDWATCH_VERSION="$(VERSION_TAG)" \
	WINDWATCH_COMMIT="$(COMMIT)" \
	WINDWATCH_BUILD_TIME="$(BUILD_TIME)" \
	WINDWATCH_GOPROXY="$(GOPROXY)" \
	docker compose up -d --build

docker-down:
	@test -f compose.yaml || { echo "compose.yaml not found; copy compose.example.yaml first" >&2; exit 1; }
	docker compose down

fmt:
	gofmt -w .

fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then echo "gofmt needed:"; echo "$$files"; exit 1; fi

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

check: fmt-check vet test

version:
	@echo $(VERSION_TAG)

clean:
	rm -rf $(BINARY) $(DIST)
