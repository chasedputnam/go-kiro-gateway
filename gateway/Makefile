# Kiro Gateway - Build Makefile
#
# Targets:
#   build       - Build for the local platform
#   build-all   - Cross-compile for all supported platforms
#   test        - Run all tests
#   clean       - Remove build artifacts
#   docker      - Build Docker image
#   lint        - Run go vet
#
# Version is injected via ldflags. Override with:
#   make build VERSION=1.2.3

BINARY_NAME := go-kiro-gateway
MODULE      := github.com/chasedputnam/go-kiro-gateway/gateway
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS     := -ldflags "-X main.version=$(VERSION)"
BUILD_DIR   := build

# Go build settings for static binaries.
GO       := go
GOFLAGS  := -trimpath
CGO      := 0

.PHONY: build build-all test clean lint docker help

## build: Compile binary for the local platform.
build:
	CGO_ENABLED=$(CGO) $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY_NAME) ./cmd/gateway

## build-all: Cross-compile for Linux, macOS, and Windows.
build-all: build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64

build-linux-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=$(CGO) $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/gateway

build-linux-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=$(CGO) $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/gateway

build-darwin-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=$(CGO) $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/gateway

build-darwin-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=$(CGO) $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/gateway

build-windows-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=$(CGO) $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/gateway

## test: Run all tests with race detection.
test:
	$(GO) test -race -count=1 ./...

## clean: Remove build artifacts.
clean:
	rm -f $(BINARY_NAME)
	rm -rf $(BUILD_DIR)
	$(GO) clean -cache -testcache

## lint: Run go vet on all packages.
lint:
	$(GO) vet ./...

## docker: Build Docker image.
docker:
	docker build -t $(BINARY_NAME):$(VERSION) .

## help: Show this help message.
help:
	@echo "Go Kiro Gateway Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
