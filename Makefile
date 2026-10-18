# Makefile for UserCleanup
# Provides targets for building, cross-compiling, and cleaning

BINARY := UserCleanup

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
	@go build -o $(BINARY)

# Build for all supported platforms (Linux and macOS)
.PHONY: release
release: linux macos

# Cross-compile for Linux (all architectures)
.PHONY: linux $(LINUX_TARGETS)
linux: $(LINUX_TARGETS)

$(LINUX_TARGETS): linux-%:
	@echo "Building $(BINARY) for Linux ($*)..."
	@GOOS=linux GOARCH=$* go build -o $(BINARY)-linux-$*

# Cross-compile for macOS (Intel and Apple Silicon)
.PHONY: macos darwin $(DARWIN_TARGETS)
macos darwin: $(DARWIN_TARGETS)

$(DARWIN_TARGETS): darwin-%:
	@echo "Building $(BINARY) for macOS ($*)..."
	@GOOS=darwin GOARCH=$* go build -o $(BINARY)-darwin-$*

# Debian aliases (Debian builds are plain Linux builds)
.PHONY: debian debian-amd64 debian-386 debian-arm64 debian-arm
debian: linux
debian-amd64: linux-amd64
debian-386: linux-386
debian-arm64: linux-arm64
debian-arm: linux-arm

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -f $(BINARY) $(BINARY)-linux-* $(BINARY)-darwin-*

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
	@echo "  clean        - Remove build artifacts"
	@echo "  deps         - Install dependencies"
	@echo "  help         - Show this help message"
