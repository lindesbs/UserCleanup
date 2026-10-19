# UserCleanup

UserCleanup is a CLI tool for cleaning up user accounts on Debian and Ubuntu
servers. It shows each user together with everything that belongs to them
(web server sites, PHP-FPM pools, logrotate, systemd, cron, sudoers and other
files in `/etc`), finds orphaned or broken entries, creates backups and
generates the shell commands to remove a user. It never deletes anything
itself: the administrator reviews and runs the generated commands.

## Features

- List users with their Apache, nginx and Caddy sites, PHP-FPM pools,
  logrotate entries, systemd units, cron jobs and other files in `/etc`
- Find orphaned or broken entries (homes without a user, pools or sites of
  deleted users, missing document roots, ...)
- Show the MySQL/MariaDB databases and tables of a user's `~/.my.cnf` login
- Back up a user into a checksummed `tar.gz` with a JSON manifest (plus a
  `mysqldump` if the user has a MySQL login)
- Generate a reviewable shell script that removes a user and their entries
- JSON output and filtering by area (`list --only`)
- Interactive terminal interface (`usercleanup tui`)

## Installation

### Debian package (Debian, Ubuntu)

Download the `.deb` for your architecture (`amd64`, `arm64`, `armhf`,
`i386`) and `SHA256SUMS` from the
[GitHub releases](https://github.com/lindesbs/UserCleanup/releases):

```bash
sha256sum -c --ignore-missing SHA256SUMS
sudo apt install ./usercleanup_<version>_amd64.deb
usercleanup --version
```

The package installs `/usr/bin/usercleanup` plus the README and license in
`/usr/share/doc/usercleanup/`. Remove it with `sudo apt remove usercleanup`;
backups in `/var/backups/usercleanup` are kept.

### Archive (Linux, macOS)

```bash
sha256sum -c --ignore-missing SHA256SUMS
tar -xzf usercleanup-linux-amd64-<version>.tar.gz
sudo install -m 0755 usercleanup-linux-amd64 /usr/local/bin/usercleanup
```

### From source

Build it (see [Development](#development)) and copy the binary or the
package to the server:

```bash
make deb
scp dist/usercleanup_<version>_amd64.deb user@server:/tmp/
ssh user@server sudo apt install /tmp/usercleanup_<version>_amd64.deb
```

## Usage

UserCleanup never deletes anything. It creates backups and prints the shell
commands to remove a user; you review them and run them yourself. `backup`
must run as root (except with `--dry-run`); run `list` and `plan` as root
too, so all files in `/etc` and the homes can be read. The examples assume
the binary is installed as `usercleanup` in the `PATH`.

```bash
# List all users (with -s also system users) with their Apache, nginx and
# Caddy sites, PHP-FPM pools, logrotate entries and other files in /etc that
# mention them, followed by orphaned or broken entries
usercleanup list
usercleanup list <username>
usercleanup list --json

# Show only some areas (comma separated, also with <username> and --json).
# Users with nothing in these areas are left out; "list --help" lists all
# areas: users, sites, apache, nginx, caddy, php, systemd, cron, etc,
# sudoers, sshd, limits, aliases, logrotate, mysql, dirs, files, unowned,
# findings
usercleanup list --only users            # accounts only
usercleanup list --only mysql            # MySQL databases and tables
usercleanup list --only apache,php       # Apache sites and PHP-FPM pools
usercleanup list --only findings         # orphaned or broken entries
usercleanup list <username> --only systemd,cron

# Back up a user (run as root). Writes <user>-<time>.tar.gz with owners and
# permissions preserved, a .manifest.json (incl. passwd/shadow/group entries)
# and a .sha256 file into /var/backups/usercleanup (mode 0700). If the user
# has a ~/.my.cnf, a mysqldump of its databases is written next to it
# (<user>-<time>.mysql.sql.gz, also in the .sha256 file).
usercleanup backup <username>
usercleanup backup <username> --dry-run   # only show what would be saved

# Print the commands to remove a user. Requires a backup (the newest one is
# used unless --backup is given). System and service users are refused.
usercleanup plan <username>
usercleanup plan <username> -o remove-<username>.sh   # write to a file
sh remove-<username>.sh                                 # after reviewing it

# Interactive interface: browse users and their areas (tabs: sites, PHP,
# MySQL, systemd/cron, /etc, logrotate, dirs), see the findings (f), create
# a backup (b) and show the generated commands (p) or write them to
# ./remove-<username>.sh (w, never overwrites a file). Nothing is deleted.
usercleanup tui

# Restore a backup
tar -xzpf /var/backups/usercleanup/<user>-<time>.tar.gz -C /

# Global flags
--version           # Print the version
--help, -h          # Help for usercleanup or a command
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

## Development

### Prerequisites

- Go 1.24 or later
- Make
- Docker (only for `test/test-debian.sh`)

`make deb` runs [nfpm](https://nfpm.goreleaser.com) via `go run`, so nothing
else needs to be installed; nfpm needs Go 1.26, which Go downloads
automatically if the installed version is older.

### Project layout

```
cmd/usercleanup/       entry point (package main), holds the version variable
internal/usercleanup/  application code and its unit tests (*_test.go)
test/                  test scripts outside of go test
  test-debian.sh       builds the Linux binary and opens a Debian container
  Dockerfile           Debian image used by test-debian.sh
  testenv/             sets up a disposable test server (see test/testenv/README.md)
packaging/nfpm.yaml    Debian package definition (name, maintainer, files)
.github/               CI and release workflows, version check script
bin/                   compiled binaries (generated, not in git)
dist/                  release archives and .deb packages (generated, not in git)
VERSION                release version, compiled into the binary
```

Source code, tests and build output are kept apart: code lives in `cmd/`
and `internal/`, test scripts in `test/`, everything the build produces in
`bin/` and `dist/`. The unit tests stay next to the code they test because
Go requires that for tests using package internals.

### Make targets

```bash
make build         # current platform -> bin/usercleanup (default target)
make release       # all Linux and macOS targets -> bin/
make linux         # Linux amd64, 386, arm64, arm
make linux-amd64   # single architecture, also linux-386, linux-arm64, linux-arm
make macos         # macOS amd64 and arm64
make darwin-arm64  # single architecture, also darwin-amd64
make debian        # alias for linux, builds binaries only (also debian-amd64, ...)
make deb           # Debian packages for all Linux architectures -> dist/
make dist          # release + tar.gz archives, .deb packages and SHA256SUMS -> dist/
make lint          # gofmt check and go vet
make test          # unit tests (go test ./...)
make version       # print the version from VERSION
make clean         # remove bin/ and dist/
make deps          # go mod download and go mod tidy
make help          # list all targets
```

Plain `go build ./cmd/usercleanup` also works; the binary then reports the
version `dev`.

## Versioning and releases

The version lives in the `VERSION` file (`MAJOR.MINOR.PATCH`) and is compiled
into the binaries via `-ldflags "-X main.version=..."`, so
`usercleanup --version` shows it, and is used as the Debian package version.
`make dist` builds all platforms and puts into `dist/`:

- `usercleanup-<os>-<arch>-<version>.tar.gz` for Linux (amd64, 386, arm64, arm) and macOS (amd64, arm64)
- `usercleanup_<version>_<arch>.deb` for Debian/Ubuntu (amd64, i386, arm64, armhf)
- `SHA256SUMS` over all of them

The Linux arm binary is built for ARMv7 (`GOARM=7`), matching Debian `armhf`.

GitHub Actions:

- **CI** (`.github/workflows/ci.yml`) runs on every push and pull request: `make lint` (gofmt, go vet), `make test`, `make release`, `make deb` (installs and removes the amd64 package on the runner) and shellcheck on the shell scripts in `test/` and `.github/scripts/`.
- **Version bumped** (job in the CI workflow) runs on pull requests into `main` and fails unless `VERSION` is higher than on `main` and no tag `v<VERSION>` exists yet.
- **Release** (`.github/workflows/release.yml`) runs on every push to `main`: it builds all platforms, tags `v<VERSION>` and publishes a GitHub release with the archives and Debian packages from `dist/`.

To release: raise `VERSION` on `develop`, open a pull request from `develop` into `main`, merge it once CI is green.

## Testing

### Unit tests

```bash
make test
```

The tests in `internal/usercleanup/*_test.go` point all system paths
(`/home`, `/etc`, ...) into a temporary directory, so they need neither root
nor a Debian system.

### Debian container

```bash
./test/test-debian.sh
```

The script can be called from any directory. It:

1. Cross-compiles `bin/usercleanup-linux-amd64` (`make linux-amd64`)
2. Builds a Debian image from `test/Dockerfile`
3. Starts a container with the binary mounted at `/app/usercleanup`
4. Opens a shell in which you can run `/app/usercleanup --help`

The same by hand:

```bash
make linux-amd64
docker build -t usercleanup-debian-test -f test/Dockerfile test
docker run --rm -it \
    -v "$(pwd)/bin/usercleanup-linux-amd64:/app/usercleanup" \
    usercleanup-debian-test
```

### Test server

`test/testenv/testenv.sh` sets up users, sites, PHP-FPM pools, cron, sudoers
and systemd units with deliberate problem cases on a **disposable** Debian or
Ubuntu server and removes them again. See
[test/testenv/README.md](test/testenv/README.md).

```bash
sudo ./test/testenv/testenv.sh setup
usercleanup list
sudo ./test/testenv/testenv.sh teardown
```

## License

UserCleanup is licensed under the MIT License, see [LICENSE](LICENSE).
