package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func setupEtcRefs(t *testing.T) {
	t.Helper()
	write(t, filepath.Join(etcDir, "sudoers"), `Defaults env_reset
root ALL=(ALL:ALL) ALL
%sudo ALL=(ALL:ALL) ALL
#includedir /etc/sudoers.d
`)
	write(t, filepath.Join(etcDir, "sudoers.d/web"), "# web may reload php\nweb ALL=(root) NOPASSWD: /bin/systemctl reload php8.2-fpm\n")
	write(t, filepath.Join(etcDir, "sudoers.d/team"), "User_Alias TEAM = web, webmaster\nTEAM ALL=(ALL) ALL\n")
	write(t, filepath.Join(etcDir, "sudoers.d/ghost"), "ghost ALL=(ALL) NOPASSWD: ALL\n")
	write(t, filepath.Join(etcDir, "sudoers.d/README.txt"), "web ALL=(ALL) ALL\n") // ignored by sudo

	write(t, filepath.Join(etcDir, "crontab"), "SHELL=/bin/sh\n17 * * * * root cd / && run-parts --report /etc/cron.hourly\n*/5 * * * * web /usr/bin/php "+filepath.Join(homeDir, "web")+"/cron.php\n")
	write(t, filepath.Join(etcDir, "cron.d/web"), "MAILTO=web@example.org\n@daily web /usr/local/bin/backup \\\n  --quiet\n")

	write(t, filepath.Join(etcDir, "ssh/sshd_config"), "Port 22\nAllowUsers root web@10.0.0.* webmaster\nMatch User web,sftp Address 10.0.0.0/8\n    ChrootDirectory %h\n")
	write(t, filepath.Join(etcDir, "ssh/sshd_config.d/web.conf"), "Match User web\n")
	write(t, filepath.Join(etcDir, "security/limits.d/web.conf"), "web hard nproc 50\nweb soft nofile 1024\n")
	write(t, filepath.Join(etcDir, "aliases"), "postmaster: root\nweb: web@example.org\nadmins: webmaster, web\n")

	sys := filepath.Join(etcDir, "systemd/system")
	write(t, filepath.Join(sys, "web-worker.service"), "[Service]\nUser=web\nExecStart=/usr/bin/php worker.php\n")
	write(t, filepath.Join(sys, "web-worker.timer"), "[Timer]\nOnCalendar=hourly\n")
	write(t, filepath.Join(sys, "php8.2-fpm.service.d/override.conf"), "[Service]\nUser=web\n")
	write(t, filepath.Join(sys, "export.service"), "[Service]\nExecStart=-/usr/bin/rsync -a "+filepath.Join(homeDir, "web")+"\nWorkingDirectory="+filepath.Join(homeDir, "web")+"/export\n")
	write(t, filepath.Join(homeDir, "web/.config/systemd/user/app.service"), "[Service]\nExecStart=/bin/true\n")

	old := lingerDir
	lingerDir = filepath.Join(filepath.Dir(etcDir), "linger")
	t.Cleanup(func() { lingerDir = old })
	write(t, filepath.Join(lingerDir, "web"), "")
	write(t, filepath.Join(lingerDir, "ghost"), "")
}

func TestReferences(t *testing.T) {
	setupFS(t)
	setupServices(t)
	setupEtcRefs(t)
	users, _ := readPasswdFile(true)
	inv := collectInventory(users)
	data := inv.For("web")

	var got []string
	for _, ref := range data.Refs {
		got = append(got, ref.Kind+" "+strings.TrimPrefix(ref.File, etcDir)+":"+strconv.Itoa(ref.Line))
	}
	want := "sudoers /sudoers.d/team:1,sudoers /sudoers.d/web:2,cron /crontab:3,cron /cron.d/web:2," +
		"sshd /ssh/sshd_config:2,sshd /ssh/sshd_config:3,sshd /ssh/sshd_config.d/web.conf:1," +
		"limits /security/limits.d/web.conf:1,limits /security/limits.d/web.conf:2,aliases /aliases:2,aliases /aliases:3"
	if strings.Join(got, ",") != want {
		t.Errorf("refs of web =\n%s\nwant\n%s", strings.Join(got, ","), want)
	}

	for file, removable := range map[string]bool{
		"sudoers.d/web": true, "sudoers.d/team": false, "sudoers": false, "cron.d/web": true, "crontab": false,
		"ssh/sshd_config.d/web.conf": true, "security/limits.d/web.conf": true, "aliases": false,
	} {
		if got := inv.refRemovable(filepath.Join(etcDir, file), "web"); got != removable {
			t.Errorf("refRemovable(%s) = %v, want %v", file, got, removable)
		}
	}

	var units []string
	for _, unit := range data.Units {
		units = append(units, unit.Name)
	}
	if strings.Join(units, ",") != "export.service,web-app.service,web-worker.service,php8.2-fpm.service" {
		t.Errorf("units of web = %v", units)
	}
	if !data.Linger || len(data.UserUnits) != 1 {
		t.Errorf("linger %v, user units %v", data.Linger, data.UserUnits)
	}

	kinds := make(map[string]int)
	for _, f := range inv.Findings {
		kinds[f.Kind]++
	}
	if kinds["ref_user_missing"] != 1 || kinds["linger_user_missing"] != 1 {
		t.Errorf("findings = %+v", inv.Findings)
	}

	users["web"].Configs = data.Files()
	script, err := buildDeletePlan(users["web"], inv, "/var/backups/usercleanup/web-20261018-090000.tar.gz", &Manifest{Username: "web"})
	if err != nil {
		t.Fatal(err)
	}
	q := func(path string) string { return "'" + filepath.Join(etcDir, path) + "'" }
	for _, want := range []string{
		"rm -- " + q("sudoers.d/web") + "\nvisudo -c\n",
		"# " + q("sudoers.d/team") + " line 1: User_Alias TEAM = web, webmaster",
		"rm -- " + q("cron.d/web") + "\n",
		"# " + q("crontab") + " line 3: */5 * * * * web",
		"# " + q("ssh/sshd_config") + " line 2: AllowUsers root web@10.0.0.* webmaster",
		"rm -- " + q("ssh/sshd_config.d/web.conf") + "\nsshd -t\nsystemctl reload ssh\n",
		"rm -- " + q("security/limits.d/web.conf") + "\n",
		"# " + q("aliases") + " line 3: admins: webmaster, web",
		"# newaliases\n",
		"systemctl disable --now 'web-worker.timer'\nrm -- " + q("systemd/system/web-worker.timer") +
			"\nsystemctl disable --now 'web-worker.service'\nrm -- " + q("systemd/system/web-worker.service"),
		"# " + q("systemd/system/php8.2-fpm.service.d/override.conf") + " overrides php8.2-fpm.service for this user",
		"# " + q("systemd/system/export.service") + " does not run as web but uses its files",
		"loginctl disable-linger 'web'\nloginctl terminate-user 'web' || true\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("plan misses %q:\n%s", want, script)
		}
	}
	restore := script[strings.Index(script, "# Restore files:"):]
	restore = restore[:strings.Index(restore, "\n")]
	if !strings.Contains(restore, "--exclude='"+strings.TrimPrefix(filepath.Join(etcDir, "aliases"), "/")+"'") ||
		strings.Contains(restore, "sudoers.d/web'") || strings.Contains(restore, "web-worker") {
		t.Errorf("restore must exclude shared files only: %s", restore)
	}
	// ignored by sudo, so not a reference and not touched
	if strings.Contains(script, "README.txt") && !strings.Contains(script, "# rm -- "+q("sudoers.d/README.txt")) {
		t.Errorf("README.txt must not be removed:\n%s", script)
	}
	if _, err := os.Stat(filepath.Join(etcDir, "sudoers.d/web")); err != nil {
		t.Fatal("plan must not change files")
	}
}
