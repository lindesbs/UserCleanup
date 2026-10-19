# Makefile for UserCleanup
# Provides targets for building, cross-compiling, and cleaning

BINARY := usercleanup
PKG    := ./cmd/usercleanup
BIN    := bin
DIST   := dist
VERSION := $(shell cat VERSION)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

LINUX_ARCHS  := amd64 386 arm64 arm
DARWIN_ARCHS := amd64 arm64

LINUX_TARGETS  := $(addprefix linux-,$(LINUX_ARCHS))
DARWIN_TARGETS := $(addprefix darwin-,$(DARWIN_ARCHS))

# Debian packages are built with nfpm; GOTOOLCHAIN=auto fetches the Go
# version nfpm needs if the installed one is older
NFPM := GOTOOLCHAIN=auto go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0

# Default target
.PHONY: all
all: build

# Build for the current platform
.PHONY: build
build:
	@echo "Building $(BINARY) for current platform..."
	@mkdir -p $(BIN)
	@go build $(LDFLAGS) -o $(BIN)/$(BINARY) $(PKG)

# Build for all supported platforms (Linux and macOS)
.PHONY: release
release: linux macos

# Cross-compile for Linux (all architectures). GOARM=7 matches Debian armhf
# and is ignored for the other architectures.
.PHONY: linux $(LINUX_TARGETS)
linux: $(LINUX_TARGETS)

$(LINUX_TARGETS): linux-%:
	@echo "Building $(BINARY) for Linux ($*)..."
	@mkdir -p $(BIN)
	@GOOS=linux GOARCH=$* GOARM=7 go build $(LDFLAGS) -o $(BIN)/$(BINARY)-linux-$* $(PKG)

# Cross-compile for macOS (Intel and Apple Silicon)
.PHONY: macos darwin $(DARWIN_TARGETS)
macos darwin: $(DARWIN_TARGETS)

$(DARWIN_TARGETS): darwin-%:
	@echo "Building $(BINARY) for macOS ($*)..."
	@mkdir -p $(BIN)
	@GOOS=darwin GOARCH=$* go build $(LDFLAGS) -o $(BIN)/$(BINARY)-darwin-$* $(PKG)

# Debian aliases (Debian builds are plain Linux builds)
.PHONY: debian debian-amd64 debian-386 debian-arm64 debian-arm
debian: linux
debian-amd64: linux-amd64
debian-386: linux-386
debian-arm64: linux-arm64
debian-arm: linux-arm

# Build Debian packages for all Linux architectures into $(DIST)/
# (Debian architecture names: 386 -> i386, arm -> armhf)
.PHONY: deb
deb: linux
	@mkdir -p $(DIST)
	@for arch in $(LINUX_ARCHS); do \
		case $$arch in 386) deb=i386 ;; arm) deb=armhf ;; *) deb=$$arch ;; esac; \
		echo "Packaging $(BINARY)_$(VERSION)_$$deb.deb..."; \
		mkdir -p $(BIN)/deb && cp $(BIN)/$(BINARY)-linux-$$arch $(BIN)/deb/$(BINARY) || exit 1; \
		VERSION=$(VERSION) DEB_ARCH=$$deb \
			$(NFPM) package --config packaging/nfpm.yaml --packager deb --target $(DIST)/ >/dev/null || exit 1; \
	done

# Package release binaries as tar.gz archives and Debian packages with
# checksums in $(DIST)/
.PHONY: dist
dist: release
	@echo "Packaging release $(VERSION) into $(DIST)/..."
	@rm -rf $(DIST) && mkdir -p $(DIST)
	@for f in $$(cd $(BIN) && ls $(BINARY)-linux-* $(BINARY)-darwin-*); do \
		tar -czf $(DIST)/$$f-$(VERSION).tar.gz -C $(BIN) $$f; \
	done
	@$(MAKE) --no-print-directory deb
	@cd $(DIST) && sha256sum *.tar.gz *.deb > SHA256SUMS

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
	@rm -rf $(BIN) $(DIST)

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
	@echo "  build        - Build for current platform into bin/"
	@echo "  release      - Build for all Linux and macOS targets into bin/"
	@echo "  linux        - Cross-compile for all Linux architectures"
	@echo "  linux-amd64  - Cross-compile for Linux (amd64)"
	@echo "  linux-386    - Cross-compile for Linux (386)"
	@echo "  linux-arm64  - Cross-compile for Linux (arm64)"
	@echo "  linux-arm    - Cross-compile for Linux (arm)"
	@echo "  macos        - Cross-compile for macOS (amd64 + arm64)"
	@echo "  darwin-amd64 - Cross-compile for macOS (Intel)"
	@echo "  darwin-arm64 - Cross-compile for macOS (Apple Silicon)"
	@echo "  debian[-*]   - Aliases for the linux targets"
	@echo "  deb          - Build Debian packages for all Linux architectures into dist/"
	@echo "  dist         - Package all release binaries into dist/ (tar.gz, .deb, SHA256SUMS)"
	@echo "  lint         - Check gofmt and run go vet"
	@echo "  test         - Run tests"
	@echo "  version      - Print the version from VERSION"
	@echo "  clean        - Remove bin/ and dist/"
	@echo "  deps         - Install dependencies"
	@echo "  help         - Show this help message"
