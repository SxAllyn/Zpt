# Zpt-core Makefile

.PHONY: all build clean test lint bench coverage fuzz

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOCLEAN=$(GOCMD) clean
GOFMT=$(GOCMD) fmt
GOTOOL=$(GOCMD) tool
GOMOD=$(GOCMD) mod
GOINSTALL=$(GOCMD) install
GOGENERATE=$(GOCMD) generate

# Binary name
BINARY_NAME=zpt
BINARY_WINDOWS=$(BINARY_NAME).exe

# Build directories
BUILD_DIR=build
DIST_DIR=dist

# Platform detection
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Linux)
	PLATFORM=linux
endif
ifeq ($(UNAME_S),Darwin)
	PLATFORM=darwin
endif
ifeq ($(OS),Windows_NT)
	PLATFORM=windows
endif

# Default target
all: test build

# Build for current platform
build:
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/zpt

# Build for Windows
build-windows:
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/$(BINARY_WINDOWS) ./cmd/zpt

# Build for Linux
build-linux:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/zpt

# Build for macOS
build-darwin:
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/zpt

# Build all platforms
build-all: build-windows build-linux build-darwin

# Install
install:
	$(GOINSTALL) ./cmd/zpt

# Run tests
test:
	$(GOTEST) -race -cover ./...

# Run tests with coverage
test-cover:
	$(GOTEST) -race -coverprofile=coverage.out ./...
	$(GOTOOL) cover -html=coverage.out -o coverage.html

# Run benchmarks
bench:
	$(GOTEST) -bench=. -benchmem ./...

# Format code
fmt:
	$(GOFMT) ./...

# Lint code
lint:
	# Install golangci-lint first: https://golangci-lint.run/usage/install/
	golangci-lint run ./...

# Clean build artifacts
clean:
	$(GOCLEAN)
	rm -rf $(BUILD_DIR)
	rm -rf $(DIST_DIR)
	rm -f coverage.out coverage.html

# Run fuzz tests
fuzz:
	$(GOTEST) -fuzz=Fuzz ./...

# Generate code (if any)
generate:
	$(GOGENERATE) ./...

# Update dependencies
deps:
	$(GOMOD) tidy
	$(GOMOD) verify

# Check for vulnerabilities
audit:
	# Install govulncheck first: go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

# Development: watch and rebuild
watch:
	# Install air first: https://github.com/cosmtrek/air
	air

# Help
help:
	@echo "Available targets:"
	@echo "  build        - Build for current platform"
	@echo "  build-all    - Build for all platforms (Windows, Linux, macOS)"
	@echo "  test         - Run tests with race detector"
	@echo "  test-cover   - Run tests with coverage report"
	@echo "  bench        - Run benchmarks"
	@echo "  fmt          - Format code"
	@echo "  lint         - Lint code (requires golangci-lint)"
	@echo "  clean        - Clean build artifacts"
	@echo "  fuzz         - Run fuzz tests"
	@echo "  deps         - Update dependencies"
	@echo "  audit        - Check for vulnerabilities (requires govulncheck)"
	@echo "  watch        - Watch and rebuild (requires air)"
	@echo "  help         - Show this help"