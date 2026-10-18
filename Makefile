# Makefile for UserCleanup
# Provides targets for building, cross-compiling, and cleaning

# Default target
.PHONY: all
all: build

# Build for the current platform
.PHONY: build
build:
	@echo "Building UserCleanup for current platform..."
	@go build -o UserCleanup

# Cross-compile for Debian (multiple architectures)
.PHONY: debian
debian: debian-amd64 debian-386 debian-arm64 debian-arm

# Cross-compile for Debian (amd64)
.PHONY: debian-amd64
debian-amd64:
	@echo "Building UserCleanup for Debian (amd64)..."
	@GOOS=linux GOARCH=amd64 go build -o UserCleanup-linux-amd64

# Cross-compile for Debian (386)
.PHONY: debian-386
debian-386:
	@echo "Building UserCleanup for Debian (386)..."
	@GOOS=linux GOARCH=386 go build -o UserCleanup-linux-386

# Cross-compile for Debian (arm64)
.PHONY: debian-arm64
debian-arm64:
	@echo "Building UserCleanup for Debian (arm64)..."
	@GOOS=linux GOARCH=arm64 go build -o UserCleanup-linux-arm64

# Cross-compile for Debian (arm)
.PHONY: debian-arm
debian-arm:
	@echo "Building UserCleanup for Debian (arm)..."
	@GOOS=linux GOARCH=arm go build -o UserCleanup-linux-arm

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -f UserCleanup UserCleanup-linux-*

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
	@echo "  all         - Build for current platform (default)"
	@echo "  build       - Build for current platform"
	@echo "  debian      - Cross-compile for all Debian architectures"
	@echo "  debian-amd64 - Cross-compile for Debian (amd64)"
	@echo "  debian-386  - Cross-compile for Debian (386)"
	@echo "  debian-arm64 - Cross-compile for Debian (arm64)"
	@echo "  debian-arm  - Cross-compile for Debian (arm)"
	@echo "  clean       - Remove build artifacts"
	@echo "  deps        - Install dependencies"
	@echo "  help        - Show this help message"