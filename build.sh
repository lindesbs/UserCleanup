#!/bin/bash

# Cross-compilation script for Debian
# This script builds the UserCleanup program for Debian Linux systems

# Set the target OS and architecture
# For Debian, we use linux as the OS and amd64 for 64-bit systems
# Other common architectures for Debian: 386 (32-bit), arm64, arm

echo "Building UserCleanup for Debian..."

# Build for amd64 (64-bit) architecture
echo "Building for amd64 architecture..."
GOOS=linux GOARCH=amd64 go build -o UserCleanup-linux-amd64

# Build for 386 (32-bit) architecture
echo "Building for 386 architecture..."
GOOS=linux GOARCH=386 go build -o UserCleanup-linux-386

# Build for arm64 architecture
echo "Building for arm64 architecture..."
GOOS=linux GOARCH=arm64 go build -o UserCleanup-linux-arm64

# Build for arm architecture
echo "Building for arm architecture..."
GOOS=linux GOARCH=arm go build -o UserCleanup-linux-arm

echo "Cross-compilation complete!"
echo "The following binaries have been created:"
echo "- UserCleanup-linux-amd64 (for 64-bit Debian systems)"
echo "- UserCleanup-linux-386 (for 32-bit Debian systems)"
echo "- UserCleanup-linux-arm64 (for ARM 64-bit Debian systems)"
echo "- UserCleanup-linux-arm (for ARM 32-bit Debian systems)"