# Makefile for UserCleanup
# Provides targets for building, cross-compiling, and cleaning

BINARY := UserCleanup
VERSION := $(shell cat VERSION)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

LINUX_ARCHS  := amd64 386 arm64 arm
DARWIN_ARCHS := amd64 arm64

LINUX_TARGETS  := $(addprefix linux-,$(LINUX_ARCHS))
DARWIN_TARGETS := $(addprefix darwin-,$(DARWIN_ARCHS))

# Default target
.PHONY: all
all: build

# Build for the current platform
.PHONY: build
build:
	@echo "Building $(BINARY) for current platform..."
	@go build $(LDFLAGS) -o $(BINARY)

# Build for all supported platforms (Linux and macOS)
.PHONY: release
release: linux macos

# Cross-compile for Linux (all architectures)
.PHONY: linux $(LINUX_TARGETS)
linux: $(LINUX_TARGETS)

$(LINUX_TARGETS): linux-%:
	@echo "Building $(BINARY) for Linux ($*)..."
	@GOOS=linux GOARCH=$* go build $(LDFLAGS) -o $(BINARY)-linux-$*

# Cross-compile for macOS (Intel and Apple Silicon)
.PHONY: macos darwin $(DARWIN_TARGETS)
macos darwin: $(DARWIN_TARGETS)

$(DARWIN_TARGETS): darwin-%:
	@echo "Building $(BINARY) for macOS ($*)..."
	@GOOS=darwin GOARCH=$* go build $(LDFLAGS) -o $(BINARY)-darwin-$*

# Debian aliases (Debian builds are plain Linux builds)
.PHONY: debian debian-amd64 debian-386 debian-arm64 debian-arm
debian: linux
debian-amd64: linux-amd64
debian-386: linux-386
debian-arm64: linux-arm64
debian-arm: linux-arm

# Package release binaries as tar.gz archives with checksums in dist/
.PHONY: dist
dist: release
	@echo "Packaging release $(VERSION) into dist/..."
	@rm -rf dist && mkdir -p dist
	@for f in $(BINARY)-linux-* $(BINARY)-darwin-*; do \
		tar -czf dist/$$f-$(VERSION).tar.gz $$f; \
	done
	@cd dist && sha256sum *.tar.gz > SHA256SUMS

# Static checks: formatting and go vet
.PHONY: lint
lint:
	@echo "Checking formatting..."
	@test -z "$$(gofmt -l .)" || (echo "Files need gofmt:"; gofmt -l .; exit 1)
	@echo "Running go vet..."
	@go vet ./...

# Run tests
.PHONY: test
test:
	@echo "Running tests..."
	@go test ./...

# Print the version from the VERSION file
.PHONY: version
version:
	@echo $(VERSION)

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BINARY) $(BINARY)-linux-* $(BINARY)-darwin-* dist

# Install dependencies
.PHONY: deps
deps:
	@echo "Installing dependencies..."
	@go mod download
	@go mod tidy

# Help target
.PHONY: help
help:
	@echo "UserCleanup Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  all          - Build for current platform (default)"
	@echo "  build        - Build for current platform"
	@echo "  release      - Build for all Linux and macOS targets"
	@echo "  linux        - Cross-compile for all Linux architectures"
	@echo "  linux-amd64  - Cross-compile for Linux (amd64)"
	@echo "  linux-386    - Cross-compile for Linux (386)"
	@echo "  linux-arm64  - Cross-compile for Linux (arm64)"
	@echo "  linux-arm    - Cross-compile for Linux (arm)"
	@echo "  macos        - Cross-compile for macOS (amd64 + arm64)"
	@echo "  darwin-amd64 - Cross-compile for macOS (Intel)"
	@echo "  darwin-arm64 - Cross-compile for macOS (Apple Silicon)"
	@echo "  debian[-*]   - Aliases for the linux targets"
	@echo "  dist         - Package all release binaries into dist/ (tar.gz + SHA256SUMS)"
	@echo "  lint         - Check gofmt and run go vet"
	@echo "  test         - Run tests"
	@echo "  version      - Print the version from VERSION"
	@echo "  clean        - Remove build artifacts"
	@echo "  deps         - Install dependencies"
	@echo "  help         - Show this help message"
