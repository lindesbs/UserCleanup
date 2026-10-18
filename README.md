# UserCleanup

UserCleanup is a CLI tool for managing user accounts on Debian and Ubuntu systems. It helps system administrators identify and manage user accounts, showing which users exist, which are inactive, and what configurations are associated with each user.

## Features

- List all users in the home directory
- Show which users are active and which are "dead"
- Display configurations in /etc for each user
- Backup and delete users and their files

## Building

The Makefile builds the application for the current platform and cross-compiles it for Linux and macOS.

### Prerequisites

- Go 1.24 or later
- Make

### Using the Makefile

```bash
# Build for the current platform
make build

# Build for all Linux and macOS targets
make release

# Build for all Linux architectures (amd64, 386, arm64, arm)
make linux
make linux-amd64   # single architecture, also linux-386, linux-arm64, linux-arm

# Build for macOS (Intel and Apple Silicon)
make macos
make darwin-arm64  # single architecture, also darwin-amd64

# The former debian targets still work as aliases for the linux targets
make debian

# Clean build artifacts
make clean

# Install dependencies
make deps

# Show help
make help
```

## Versioning and releases

The version lives in the `VERSION` file (`MAJOR.MINOR.PATCH`) and is compiled into the binaries, so `UserCleanup --version` shows it. `make dist` builds all platforms and packs them into `dist/` with a `SHA256SUMS` file.

GitHub Actions:

- **CI** (`.github/workflows/ci.yml`) runs on every push and pull request: `make lint` (gofmt, go vet), `make test`, `make release` and shellcheck on the shell scripts.
- **Version bumped** (job in the CI workflow) runs on pull requests into `main` and fails unless `VERSION` is higher than on `main` and no tag `v<VERSION>` exists yet.
- **Release** (`.github/workflows/release.yml`) runs on every push to `main`: it builds all platforms, tags `v<VERSION>` and publishes a GitHub release with the archives.

To release: raise `VERSION` on `develop`, open a pull request from `develop` into `main`, merge it once CI is green.

## Usage

```bash
# List all users
./UserCleanup list

# Backup a user's files
./UserCleanup backup [username]

# Delete a user and their files
./UserCleanup delete [username]

# Show help
./UserCleanup --help

# Additional flags
--dry-run, -d    # Show what would be done without actually doing it
--system-users, -s  # Include system users (UID < 1000)
```

## Testing on Debian

The project includes a Docker-based testing environment to verify that the cross-compiled binaries work correctly on Debian systems.

### Using the Test Script

```bash
# Make the script executable
chmod +x test-debian.sh

# Run the test script
./test-debian.sh
```

This script will:
1. Cross-compile the UserCleanup binary for Debian (amd64)
2. Build a Docker image with a Debian environment
3. Run a Docker container with the binary mounted
4. Provide a shell where you can test the binary

### Manual Testing with Docker

You can also manually test the binary using Docker:

```bash
# Build the binary
GOOS=linux GOARCH=amd64 go build -o UserCleanup-linux-amd64

# Build the Docker image
docker build -t usercleanup-debian-test .

# Run the Docker container with the binary mounted
docker run --rm -it \
    -v "$(pwd)/UserCleanup-linux-amd64:/app/UserCleanup" \
    usercleanup-debian-test
```

## Transferring to Debian Systems

After building the appropriate binary for your target Debian system, transfer it using scp or another file transfer method:

```bash
# Example: Transfer to a Debian server
scp UserCleanup-linux-amd64 user@debian-server:/path/to/destination/UserCleanup
```

Make sure to set the executable permission on the target system:

```bash
chmod +x /path/to/destination/UserCleanup
```

## License

This project is licensed under the MIT License - see the LICENSE file for details.
