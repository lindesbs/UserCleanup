#!/bin/bash

# Script to test UserCleanup on Debian using Docker

# Run from the repository root, wherever the script is called from
cd "$(dirname "$0")/.." || exit 1
BINARY=bin/usercleanup-linux-amd64

# Step 1: Cross-compile for Debian (amd64)
echo "Cross-compiling UserCleanup for Debian (amd64)..."
make linux-amd64

# Check if the build was successful
if [ ! -f "$BINARY" ]; then
    echo "Error: Failed to build UserCleanup for Debian"
    exit 1
fi

# Step 2: Build the Docker image
echo "Building Docker image..."
docker build -t usercleanup-debian-test -f test/Dockerfile test

# Step 3: Run the Docker container with the binary mounted
echo "Running Docker container..."
docker run --rm -it \
    -v "$(pwd)/$BINARY:/app/usercleanup" \
    --name usercleanup-test \
    usercleanup-debian-test \
    -c "chmod +x /app/usercleanup && echo 'UserCleanup binary mounted and ready for testing' && echo 'Try running: /app/usercleanup --help' && /bin/bash"

echo "Test completed"