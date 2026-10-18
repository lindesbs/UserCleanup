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
# sudoers, sshd, limits, aliases, logrotate, mysql, dirs, files, unowned,
# findings
./UserCleanup list --only users            # accounts only
./UserCleanup list --only mysql            # MySQL databases and tables
./UserCleanup list --only apache,php       # Apache sites and PHP-FPM pools
./UserCleanup list --only findings         # orphaned or broken entries
./UserCleanup list <username> --only systemd,cron

# Back up a user (run as root). Writes <user>-<time>.tar.gz with owners and
# permissions preserved, a .manifest.json (incl. passwd/shadow/group entries)
# and a .sha256 file into /var/backups/usercleanup (mode 0700). If the user
# has a ~/.my.cnf, a mysqldump of its databases is written next to it
# (<user>-<time>.mysql.sql.gz, also in the .sha256 file).
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
| MySQL/MariaDB | `~/.my.cnf` (`[client]`, `[mysql]`) | the login in the user's home; databases and tables it can see (`information_schema`) |

MySQL: usercleanup reads host, port, socket, user and password from
`~/.my.cnf` and runs the `mysql` client (or `mariadb`) with a temporary
option file that holds only these values, so options like `plugin-dir`
from the user's file are never used as root. Passwords are never shown.
Table sizes are data plus index length, row counts are estimates for
InnoDB. Logins with privileges on all databases (like an admin login) are
listed, but their databases are not assigned to the user, not dumped and
only appear as comments in the commands. A database that another user's
`~/.my.cnf` also reaches is marked as shared. If the server cannot be
reached or no client is installed, this is shown as a note and a finding.

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
with that name would inherit them), lingering for missing users,
`~/.my.cnf` files that are readable by everyone or whose login fails.

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
4. MySQL: `DROP DATABASE IF EXISTS` for the databases of the user's login
   and `DROP USER IF EXISTS` for the login, run with root's own MySQL
   access (`mysql -e ...`, with `-h`/`-P` or `-S` from `~/.my.cnf`).
   Databases that are shared, not in the backup, or seen by a login with
   privileges on all databases are only listed as comments.
5. Stop and remove the user's systemd units and timers (overrides of vendor
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
