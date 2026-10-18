package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// configCheck is a read-only syntax check for a service configuration
type configCheck struct {
	binary  string
	command string
}

var configChecks = []configCheck{
	{"apache2ctl", "apache2ctl configtest"},
	{"nginx", "nginx -t"},
	{"caddy", "caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile"},
}

// lookPath is replaceable in tests
var lookPath = exec.LookPath

// shellQuote quotes a string for POSIX sh
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// availableConfigChecks returns the check commands for the services installed
func availableConfigChecks() []string {
	var checks []string
	for _, check := range configChecks {
		if _, err := lookPath(check.binary); err == nil {
			checks = append(checks, check.command)
		}
	}
	fpms, _ := filepath.Glob(filepath.Join(sbinDir, "php-fpm*"))
	for _, fpm := range fpms {
		checks = append(checks, shellQuote(fpm)+" -t")
	}
	return checks
}

// buildDeletePlan returns a shell script with the commands to remove a
// user. Nothing is executed: the administrator reviews and runs it.
func buildDeletePlan(user *User, users map[string]*User, archivePath string, manifest *Manifest) (string, error) {
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

	section("Stop running processes of the user")
	line("pkill -KILL -u %s || true", u)
	line("")

	if _, err := os.Stat(filepath.Join(cronDir, user.Username)); err == nil {
		section("Remove the crontab")
		line("crontab -r -u %s", u)
		line("")
	}

	section("Configuration files that mention %s or %s", user.Username, user.HomeDir)
	if len(user.Configs) == 0 {
		line("#    none found")
	} else {
		line("#    Found by text search only, so they may be shared with other users")
		line("#    or sites. They are commented out on purpose: edit or remove each")
		line("#    one by hand after checking it.")
		for _, config := range user.Configs {
			line("# rm -- %s", shellQuote(config))
		}
	}
	if checks := availableConfigChecks(); len(checks) > 0 {
		line("#    After changing configurations, check them before any reload:")
		for _, check := range checks {
			line("%s", check)
		}
	}
	line("")

	section("Remove the account (passwd, shadow, group entries)")
	line("userdel %s", u)
	line("")

	section("Remove the files of the user")
	if ok, reason := homeIsRemovable(user, users); ok {
		line("rm -rf -- %s", shellQuote(filepath.Clean(user.HomeDir)))
	} else {
		line("#    Home directory is not removed automatically: %s", reason)
		line("# rm -rf -- %s", shellQuote(user.HomeDir))
	}
	if _, err := os.Stat(filepath.Join(mailDir, user.Username)); err == nil {
		line("rm -f -- %s", shellQuote(filepath.Join(mailDir, user.Username)))
	}
	line("")

	line("# Restore files:   tar -xzpf %s -C /", shellQuote(archivePath))
	line("# Account entries: see account_lines in %s",
		strings.TrimSuffix(archivePath, ".tar.gz")+".manifest.json")
	return b.String(), nil
}
