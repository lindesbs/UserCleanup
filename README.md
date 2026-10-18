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
# List all users (with -s also system users) with their Apache, nginx and
# Caddy sites, PHP-FPM pools, logrotate entries and other files in /etc that
# mention them, followed by orphaned or broken entries
./UserCleanup list
./UserCleanup list <username>
./UserCleanup list --json

# Show only some areas (comma separated, also with <username> and --json).
# Users with nothing in these areas are left out; "list --help" lists all
# areas: users, sites, apache, nginx, caddy, php, systemd, cron, etc,
# sudoers, sshd, limits, aliases, logrotate, dirs, files, unowned, findings
./UserCleanup list --only users            # accounts only
./UserCleanup list --only apache,php       # Apache sites and PHP-FPM pools
./UserCleanup list --only findings         # orphaned or broken entries
./UserCleanup list <username> --only systemd,cron

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

### What is collected

| Source | Files | Assigned to a user by |
|---|---|---|
| Apache | `/etc/apache2/sites-available`, `sites-enabled`, `/etc/httpd/conf.d` | `SuexecUserGroup`/`AssignUserID`, `DocumentRoot` in the home, PHP-FPM socket of the user's pool |
| nginx | `/etc/nginx/sites-available`, `sites-enabled`, `conf.d` | `root` in the home, `fastcgi_pass` to the user's pool |
| Caddy | `/etc/caddy/Caddyfile` and its imports | `root` in the home, `php_fastcgi` to the user's pool |
| PHP-FPM | `/etc/php/*/fpm/pool.d`, `/etc/php-fpm.d` | `user =` of the pool |
| logrotate | `/etc/logrotate.conf`, `/etc/logrotate.d` | `su`/`create` user, log directory in the home or owned by the user |
| systemd | `/etc/systemd/system/*.service`, drop-ins `*.service.d/*.conf`, timers | `User=`, or `WorkingDirectory=`/`ExecStart=`/`EnvironmentFile=` in the home |
| systemd user services | `~/.config/systemd/user`, `/var/lib/systemd/linger` | home, linger file |
| sudo | `/etc/sudoers`, `/etc/sudoers.d` | user specifications, `User_Alias`, `Defaults:user` |
| cron | `/etc/crontab`, `/etc/cron.d` | user column of each job |
| SSH | `/etc/ssh/sshd_config`, `sshd_config.d` | `AllowUsers`, `DenyUsers`, `Match User` |
| limits | `/etc/security/limits.conf`, `limits.d` | domain column |
| mail | `/etc/aliases` | alias name and local targets |

A site belongs to the user of its PHP-FPM pool only if neither a suexec
user nor the owner of the document root identifies it; a pool used by sites
of other users is reported and never removed with its owner. Directories
outside the home that the user owns (webroots below /var/www, log
directories) are backed up and removed with the user. Sites that cannot be
assigned to anyone are listed separately.

Orphaned or broken entries: directories in /home without a user, pools
running as a missing user or with a missing `chdir`, pools shared between
users, sites with a missing document root, user or PHP-FPM socket, sites
and logrotate entries in an orphaned home, logrotate entries with a missing
user or log directory, systemd units of missing users or using files in
an orphaned home, sudoers and cron entries of missing users (a new user
with that name would inherit them), lingering for missing users.

### The generated commands

1. Verify the backup checksum.
2. Per service (web servers first, then PHP-FPM, then logrotate): remove
   config files that only contain blocks of this user (plus the
   sites-enabled link), then run the config test and reload. Blocks in
   files shared with other users or sites, and the main Caddyfile, are
   listed with their line numbers to edit by hand; the test and reload for
   that service are then commented out too.
   The same applies to sudoers, cron, sshd and limits drop-in files and
   lines in `/etc/sudoers`, `/etc/crontab`, `sshd_config` and `/etc/aliases`
   (`visudo -c`, `sshd -t`, `newaliases`).
3. Other files in /etc that mention the user (text search), commented out.
4. Stop and remove the user's systemd units and timers (overrides of vendor
   units and root services using the user's files only as comments),
   disable lingering, stop the user's processes,
   remove the crontab, `userdel`, remove the home directory only if it is
   exactly `/home/<user>` and not shared with another user, and remove the
   directories outside the home owned by the user.

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
