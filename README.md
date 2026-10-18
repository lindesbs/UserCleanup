# UserCleanup

UserCleanup is a CLI tool for managing user accounts on Debian and Ubuntu systems. It helps system administrators identify and manage user accounts, showing which users exist, which are inactive, and what configurations are associated with each user.

## Features

- List all users in the home directory
- Show which users are active and which are "dead"
- Display configurations in /etc for each user
- Backup and delete users and their files

## Building for Debian

This project includes tools for cross-compiling the application to run on various Debian architectures.

### Prerequisites

- Go 1.24 or later
- Make (optional, for using the Makefile)

### Using the Makefile

The project includes a Makefile with targets for cross-compiling to different Debian architectures:

```bash
# Build for all Debian architectures
make debian

# Build for specific Debian architecture
make debian-amd64  # For 64-bit systems
make debian-386    # For 32-bit systems
make debian-arm64  # For ARM 64-bit systems
make debian-arm    # For ARM 32-bit systems

# Clean build artifacts
make clean

# Install dependencies
make deps

# Show help
make help
```

### Using the Shell Script

Alternatively, you can use the provided shell script:

```bash
# Make the script executable
chmod +x build.sh

# Run the script to build for all Debian architectures
./build.sh
```

## Usage

UserCleanup never deletes anything. It creates backups and prints the shell
commands to remove a user; you review them and run them yourself.

```bash
# List all users (with -s also system users) and the files in /etc that mention them
./UserCleanup list

# Back up a user (run as root). Writes <user>-<time>.tar.gz with owners and
# permissions preserved, a .manifest.json (incl. passwd/shadow/group entries)
# and a .sha256 file into /var/backups/usercleanup (mode 0700).
./UserCleanup backup <username>
./UserCleanup backup <username> --dry-run   # only show what would be saved

# Print the commands to remove a user. Requires a backup (the newest one is
# used unless --backup is given). System and service users are refused.
./UserCleanup plan <username>
./UserCleanup plan <username> -o remove-<username>.sh   # write to a file
sh remove-<username>.sh                                 # after reviewing it

# Restore a backup
tar -xzpf /var/backups/usercleanup/<user>-<time>.tar.gz -C /

# Global flags
--backup-dir, -b    # Absolute backup directory (default /var/backups/usercleanup),
                    # must not be inside a user's home
--system-users, -s  # Include system users (UID < 1000) in list
```

The generated commands verify the backup checksum, stop the user's
processes, remove the crontab, call `userdel` and remove the home directory
only if it is exactly `/home/<user>` and not shared with another user.
Configuration files found in /etc are listed commented out, because the
text search can also match files shared with other sites; check and edit
them by hand, then run the listed config tests (apache2ctl, nginx, caddy,
php-fpm) before reloading any service.

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
