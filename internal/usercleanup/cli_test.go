package usercleanup

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupFS points all paths into a temporary directory and returns its root
func setupFS(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dirs := map[string]*string{"home": &homeDir, "etc": &etcDir, "cron": &cronDir, "mail": &mailDir}
	for name, ptr := range dirs {
		old := *ptr
		*ptr = filepath.Join(root, name)
		t.Cleanup(func() { *ptr = old })
		if err := os.MkdirAll(*ptr, 0755); err != nil {
			t.Fatal(err)
		}
	}
	oldPasswd, oldAccounts, oldRoot := passwdFile, accountFiles, requireRoot
	t.Cleanup(func() {
		passwdFile, accountFiles, requireRoot = oldPasswd, oldAccounts, oldRoot
	})
	passwdFile = filepath.Join(etcDir, "passwd")
	accountFiles = []string{passwdFile, filepath.Join(etcDir, "shadow"), filepath.Join(etcDir, "group")}
	requireRoot = false

	write(t, passwdFile, "root:x:0:0:root:/root:/bin/bash\n"+
		"www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin\n"+
		"web:x:61000:61000::"+filepath.Join(homeDir, "web")+":/usr/sbin/nologin\n"+
		"webmaster:x:61001:61001::"+filepath.Join(homeDir, "webmaster")+":/bin/bash\n"+
		"shared:x:61002:61002::"+filepath.Join(homeDir, "web")+":/bin/bash\n"+
		"nobody:x:65534:65534::/nonexistent:/usr/sbin/nologin\n")
	write(t, filepath.Join(etcDir, "shadow"), "web:$6$secret:19000::::::\n")
	write(t, filepath.Join(etcDir, "group"), "web:x:61000:\nwww-data:x:33:web\n")
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0640); err != nil {
		t.Fatal(err)
	}
}

func TestReadPasswdSkipsSystemUsers(t *testing.T) {
	setupFS(t)
	users, err := readPasswdFile(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root", "www-data", "nobody"} {
		if _, ok := users[name]; ok {
			t.Errorf("%s should be skipped", name)
		}
	}
	if users["web"] == nil || users["web"].HasLogin {
		t.Errorf("web should be listed without login: %+v", users["web"])
	}
}

func TestFindUserConfigsMatchesWholeNamesOnly(t *testing.T) {
	setupFS(t)
	write(t, filepath.Join(etcDir, "php/8.2/fpm/pool.d/web.conf"), "[web]\nuser = web\n")
	write(t, filepath.Join(etcDir, "apache2/sites-available/webmaster.conf"), "DocumentRoot "+filepath.Join(homeDir, "webmaster")+"/public\n")
	write(t, filepath.Join(etcDir, "other.conf"), "webhost = example.org\n")
	write(t, filepath.Join(etcDir, "binary.bin"), "web\x00data")

	users, _ := readPasswdFile(false)
	if err := findUserConfigs(users); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(etcDir, "php/8.2/fpm/pool.d/web.conf")}
	if got := users["web"].Configs; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("web configs = %v, want %v", got, want)
	}
	if got := users["webmaster"].Configs; len(got) != 1 || !strings.HasSuffix(got[0], "webmaster.conf") {
		t.Errorf("webmaster configs = %v", got)
	}
}

func TestProtectedUsers(t *testing.T) {
	setupFS(t)
	users, _ := readPasswdFile(true)
	for _, name := range []string{"root", "www-data", "nobody"} {
		if isProtected(users[name]) == "" {
			t.Errorf("%s should be protected", name)
		}
	}
	if reason := isProtected(users["web"]); reason != "" {
		t.Errorf("web should not be protected: %s", reason)
	}
	if _, err := buildDeletePlan(users["www-data"], collectInventory(users), "/x.tar.gz", &Manifest{Username: "www-data"}); err == nil {
		t.Error("plan for www-data should be refused")
	}
}

func TestCheckBackupDir(t *testing.T) {
	setupFS(t)
	users, _ := readPasswdFile(true)
	for _, dir := range []string{"backups", filepath.Join(homeDir, "web", "b"), homeDir} {
		if err := checkBackupDir(dir, users); err == nil {
			t.Errorf("%s should be rejected", dir)
		}
	}
	// www-data (home /var/www) is a protected system user
	if err := checkBackupDir("/var/www/backups", users); err != nil {
		t.Error(err)
	}
}

func TestBackupAndPlan(t *testing.T) {
	root := setupFS(t)
	home := filepath.Join(homeDir, "web")
	write(t, filepath.Join(home, "public/index.php"), "<?php echo 1;")
	write(t, filepath.Join(home, ".ssh/id_ed25519"), "secret")
	if err := os.Symlink("/etc/hostname", filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(etcDir, "php/8.2/fpm/pool.d/web.conf"), "[web]\nuser = web\nlisten = /run/php/web.sock\n")
	write(t, filepath.Join(cronDir, "web"), "* * * * * true\n")

	users, _ := readPasswdFile(true)
	user := users["web"]
	if err := findUserConfigs(map[string]*User{"web": user}); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "backups")
	archive, err := backupUser(user, users, dir)
	if err != nil {
		t.Fatal(err)
	}

	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0700 {
		t.Errorf("backup dir mode = %o", info.Mode().Perm())
	}
	for _, suffix := range []string{".tar.gz", ".manifest.json", ".sha256"} {
		info, err := os.Stat(strings.TrimSuffix(archive, ".tar.gz") + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if suffix != ".tar.gz" && info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %o", suffix, info.Mode().Perm())
		}
	}

	entries := readArchive(t, archive)
	for _, path := range []string{
		filepath.Join(home, "public/index.php"),
		filepath.Join(home, ".ssh/id_ed25519"),
		filepath.Join(etcDir, "php/8.2/fpm/pool.d/web.conf"),
		filepath.Join(cronDir, "web"),
	} {
		if _, ok := entries[strings.TrimPrefix(path, "/")]; !ok {
			t.Errorf("archive misses %s", path)
		}
	}
	if h := entries[strings.TrimPrefix(filepath.Join(home, "link"), "/")]; h == nil || h.Typeflag != tar.TypeSymlink {
		t.Error("symlink should be stored as link")
	}

	manifest, err := readManifest(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.AccountLines[filepath.Join(etcDir, "shadow")]) != 1 || len(manifest.AccountLines[filepath.Join(etcDir, "group")]) != 2 {
		t.Errorf("account lines = %v", manifest.AccountLines)
	}
	if latest, _ := latestBackup("web", dir); latest != archive {
		t.Errorf("latestBackup = %s, want %s", latest, archive)
	}
	if latest, _ := latestBackup("we", dir); latest != "" {
		t.Errorf("latestBackup(we) = %s, want none", latest)
	}

	script, err := buildDeletePlan(user, collectInventory(users), archive, manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"sha256sum -c",
		"pkill -KILL -u 'web'",
		"crontab -r -u 'web'",
		"\nrm -- '" + filepath.Join(etcDir, "php/8.2/fpm/pool.d/web.conf") + "'\nphp-fpm8.2 -t\nsystemctl reload php8.2-fpm\n",
		"userdel 'web'\n",
		// home is shared with user "shared", so it must stay commented
		"# rm -rf -- '" + home + "'",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("plan misses %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "userdel -r") || strings.Contains(script, "\nrm -rf -- '"+home) {
		t.Errorf("plan must not remove a shared home:\n%s", script)
	}
}

func TestPlanRemovesOwnHome(t *testing.T) {
	setupFS(t)
	if err := os.MkdirAll(filepath.Join(homeDir, "webmaster"), 0755); err != nil {
		t.Fatal(err)
	}
	users, _ := readPasswdFile(true)
	user := users["webmaster"]
	script, err := buildDeletePlan(user, collectInventory(users), "/var/backups/usercleanup/webmaster-20261018-090000.tar.gz", &Manifest{Username: "webmaster"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "\nrm -rf -- '"+filepath.Join(homeDir, "webmaster")+"'\n") {
		t.Errorf("plan should remove own home:\n%s", script)
	}
	if _, err := buildDeletePlan(user, collectInventory(users), "/x.tar.gz", &Manifest{Username: "web"}); err == nil {
		t.Error("backup of another user must be rejected")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b c"); got != `'a'\''b c'` {
		t.Errorf("shellQuote = %s", got)
	}
}

func readArchive(t *testing.T, path string) map[string]*tar.Header {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	entries := make(map[string]*tar.Header)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		entries[h.Name] = h
	}
}
