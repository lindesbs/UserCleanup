package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// shellQuote quotes a string for POSIX sh
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// serviceChanges collects the commands for one service
type serviceChanges struct {
	title  string
	test   string
	reload string
	live   []string // commands for files that only belong to the user
	manual []string // comments for blocks in shared files
}

// serviceFor returns the key and an empty change set for a config block
func serviceFor(kind, version string) (string, *serviceChanges) {
	switch kind {
	case "apache":
		return kind, &serviceChanges{title: "Apache", test: "apache2ctl configtest", reload: "systemctl reload apache2"}
	case "nginx":
		return kind, &serviceChanges{title: "nginx", test: "nginx -t", reload: "systemctl reload nginx"}
	case "caddy":
		return kind, &serviceChanges{title: "Caddy",
			test:   "caddy validate --config " + shellQuote(filepath.Join(etcDir, "caddy", "Caddyfile")) + " --adapter caddyfile",
			reload: "systemctl reload caddy"}
	case "php":
		if version == "" {
			return "php", &serviceChanges{title: "PHP-FPM", test: "php-fpm -t", reload: "systemctl reload php-fpm"}
		}
		return "php" + version, &serviceChanges{title: "PHP-FPM " + version,
			test: "php-fpm" + version + " -t", reload: "systemctl reload php" + version + "-fpm"}
	default:
		// Only a warning: errors of other entries (e.g. an su user that is
		// already gone) would otherwise stop the script halfway.
		conf := shellQuote(filepath.Join(etcDir, "logrotate.conf"))
		return "logrotate", &serviceChanges{title: "logrotate",
			test: "logrotate -d " + conf + " >/dev/null 2>&1 || echo \"WARNING: logrotate reports errors, check: logrotate -d " +
				strings.ReplaceAll(conf, "'", "") + "\" >&2"}
	}
}

// configChanges builds the per service commands to remove the user's
// sites, pools and logrotate entries
func configChanges(username string, inv *Inventory) []*serviceChanges {
	services := make(map[string]*serviceChanges)
	var order []string
	done := make(map[string]bool)

	add := func(kind, version, file string, line, endLine int, what, link, keep string) {
		key, fresh := serviceFor(kind, version)
		svc := services[key]
		if svc == nil {
			svc = fresh
			services[key] = svc
			order = append(order, key)
		}
		if keep != "" {
			svc.manual = append(svc.manual, fmt.Sprintf("# %s lines %d-%d: %s is %s; keep it or move it to that user",
				shellQuote(file), line, endLine, what, keep))
			return
		}
		if inv.dedicated(file, username) {
			if done[file] {
				return
			}
			done[file] = true
			if link != "" {
				svc.live = append(svc.live, "rm -- "+shellQuote(link))
			}
			svc.live = append(svc.live, "rm -- "+shellQuote(file))
			return
		}
		svc.manual = append(svc.manual, fmt.Sprintf("# %s lines %d-%d: %s (file is shared, remove this block by hand)",
			shellQuote(file), line, endLine, what))
	}

	data := inv.For(username)
	for _, pool := range data.Pools {
		keep := ""
		if len(pool.UsedBy) > 0 {
			keep = "also used by " + strings.Join(pool.UsedBy, ", ")
		}
		add("php", pool.Version, pool.File, pool.Line, pool.EndLine, "pool ["+pool.Name+"]", "", keep)
	}
	for _, site := range data.Sites {
		what := "site"
		if len(site.Names) > 0 {
			what += " " + strings.Join(site.Names, " ")
		}
		add(site.Server, "", site.File, site.Line, site.EndLine, what, site.EnabledLink, "")
	}
	for _, entry := range data.Logrotate {
		add("logrotate", "", entry.File, entry.Line, entry.EndLine, strings.Join(entry.Paths, " "), "", "")
	}

	sort.SliceStable(order, func(i, j int) bool {
		// web servers first, so no site points to a removed pool
		rank := func(key string) int {
			switch {
			case key == "apache" || key == "nginx" || key == "caddy":
				return 0
			case strings.HasPrefix(key, "php"):
				return 1
			}
			return 2
		}
		return rank(order[i]) < rank(order[j])
	})
	result := make([]*serviceChanges, 0, len(order))
	for _, key := range order {
		result = append(result, services[key])
	}
	return result
}

// buildDeletePlan returns a shell script with the commands to remove a
// user. Nothing is executed: the administrator reviews and runs it.
func buildDeletePlan(user *User, inv *Inventory, archivePath string, manifest *Manifest) (string, error) {
	if reason := isProtected(user); reason != "" {
		return "", fmt.Errorf("refusing to generate delete commands: %s", reason)
	}
	if manifest == nil || manifest.Username != user.Username {
		return "", fmt.Errorf("backup %s does not belong to %s", archivePath, user.Username)
	}

	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	u := shellQuote(user.Username)
	hostname, _ := os.Hostname()
	step := 0
	section := func(title string, args ...any) {
		step++
		line("# %d. "+title, append([]any{step}, args...)...)
	}

	line("#!/bin/sh")
	line("# UserCleanup: commands to remove user %s", user.Username)
	line("# Generated %s on %s.", time.Now().Format(time.RFC3339), hostname)
	line("# usercleanup has not changed anything. Review every line, then run")
	line("# the commands yourself as root.")
	line("set -eu")
	line("")

	section("Verify the backup (%s)", filepath.Base(archivePath))
	if len(manifest.Skipped) > 0 {
		line("#    WARNING: %d entries were NOT backed up, see the manifest:", len(manifest.Skipped))
		for _, skipped := range manifest.Skipped {
			line("#      %s", skipped)
		}
	}
	line("(cd %s && sha256sum -c %s)", shellQuote(filepath.Dir(archivePath)),
		shellQuote(strings.TrimSuffix(filepath.Base(archivePath), ".tar.gz")+".sha256"))
	line("")

	data := inv.For(user.Username)

	// A pool that stays because other sites use it still runs as this
	// user; after userdel php-fpm would refuse to start, taking down all
	// PHP sites. The script must not run until the admin resolved that.
	var blockers []*Pool
	for _, pool := range data.Pools {
		if len(pool.UsedBy) > 0 {
			blockers = append(blockers, pool)
		}
	}
	if len(blockers) > 0 {
		section("BLOCKER: PHP-FPM pools of %s are still used by other sites", user.Username)
		for _, pool := range blockers {
			line("#    %s lines %d-%d: pool [%s] runs as %s and is used by %s.", shellQuote(pool.File),
				pool.Line, pool.EndLine, pool.Name, pool.User, strings.Join(pool.UsedBy, ", "))
		}
		line("#    After userdel php-fpm would no longer start. First change user, group")
		line("#    and paths (error_log, open_basedir, chdir) of these pools to the other")
		line("#    user, or move those sites to another pool. Then delete these lines.")
		line("echo \"Blocked: PHP-FPM pool still used by other sites, see step %d\" >&2", step)
		line("exit 1")
		line("")
	}

	// Configs go first: a PHP-FPM pool or vhost of a deleted user stops
	// the whole service on its next reload.
	for _, svc := range configChanges(user.Username, inv) {
		section("%s configuration", svc.title)
		for _, cmd := range svc.manual {
			line("%s", cmd)
		}
		for _, cmd := range svc.live {
			line("%s", cmd)
		}
		prefix := ""
		if len(svc.live) == 0 {
			prefix = "# "
		}
		line("%s%s", prefix, svc.test)
		if svc.reload != "" {
			line("%s%s", prefix, svc.reload)
		}
		line("")
	}

	known := make(map[string]bool)
	for _, file := range data.Files() {
		known[file] = true
	}
	var others []string
	for _, config := range user.Configs {
		if !known[config] {
			others = append(others, config)
		}
	}
	if len(others) > 0 {
		section("Other files in %s that mention %s or %s", etcDir, user.Username, user.HomeDir)
		line("#    Found by text search only, so they may be shared with other users")
		line("#    or sites. Check each one and edit or remove it by hand.")
		for _, config := range others {
			line("# rm -- %s", shellQuote(config))
		}
		line("")
	}

	if len(data.Units) > 0 {
		section("Stop and remove systemd units running as %s", user.Username)
		for _, unit := range data.Units {
			line("systemctl disable --now %s", shellQuote(unit.Name))
			line("rm -- %s", shellQuote(unit.File))
		}
		line("systemctl daemon-reload")
		line("")
	}

	section("Stop running processes of the user")
	line("pkill -KILL -u %s || true", u)
	line("")

	if _, err := os.Stat(filepath.Join(cronDir, user.Username)); err == nil {
		section("Remove the crontab")
		line("crontab -r -u %s", u)
		line("")
	}

	section("Remove the account (passwd, shadow, group entries)")
	line("userdel %s", u)
	line("")

	section("Remove the files of the user")
	if !exists(user.HomeDir) {
		line("#    Home directory %s does not exist", user.HomeDir)
	} else if ok, reason := homeIsRemovable(user, inv.All); ok {
		line("rm -rf -- %s", shellQuote(filepath.Clean(user.HomeDir)))
	} else {
		line("#    Home directory is not removed automatically: %s", reason)
		line("# rm -rf -- %s", shellQuote(user.HomeDir))
	}
	if _, err := os.Stat(filepath.Join(mailDir, user.Username)); err == nil {
		line("rm -f -- %s", shellQuote(filepath.Join(mailDir, user.Username)))
	}
	if len(data.Dirs) > 0 {
		line("#    Directories outside the home owned by %s (webroots, logs):", user.Username)
		for _, dir := range data.Dirs {
			line("rm -rf -- %s", shellQuote(dir))
		}
	}
	line("")

	line("# Restore files:   tar -xzpf %s -C /", shellQuote(archivePath))
	line("# Account entries: see account_lines in %s",
		strings.TrimSuffix(archivePath, ".tar.gz")+".manifest.json")
	return b.String(), nil
}
