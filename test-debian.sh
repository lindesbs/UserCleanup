#!/bin/bash

# Script to test UserCleanup on Debian using Docker

# Step 1: Cross-compile for Debian (amd64)
echo "Cross-compiling UserCleanup for Debian (amd64)..."
GOOS=linux GOARCH=amd64 go build -o UserCleanup-linux-amd64

# Check if the build was successful
if [ ! -f "UserCleanup-linux-amd64" ]; then
    echo "Error: Failed to build UserCleanup for Debian"
    exit 1
fi

# Step 2: Build the Docker image
echo "Building Docker image..."
docker build -t usercleanup-debian-test .

# Step 3: Run the Docker container with the binary mounted
echo "Running Docker container..."
docker run --rm -it \
    -v "$(pwd)/UserCleanup-linux-amd64:/app/UserCleanup" \
    --name usercleanup-test \
    usercleanup-debian-test \
    -c "chmod +x /app/UserCleanup && echo 'UserCleanup binary mounted and ready for testing' && echo 'Try running: /app/UserCleanup --help' && /bin/bash"

echo "Test completed"