package main

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMyCnf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".my.cnf")
	write(t, path, `# comment
[client]
user = web
password = "se#cr\"et\\x"
host=db.example.org   # inline comment
port = 3307
!include /etc/other.cnf

[mysqldump]
password = other

[mysql]
database = shop
default_auth = evil
socket = '/run/mysqld/web.sock'
`)
	acct, err := parseMyCnf(path)
	if err != nil {
		t.Fatal(err)
	}
	want := MySQLAccount{File: path, Host: "db.example.org", Port: "3307", Socket: "/run/mysqld/web.sock",
		User: "web", Database: "shop", password: `se#cr"et\x`}
	if fmt.Sprintf("%+v", *acct) != fmt.Sprintf("%+v", want) {
		t.Errorf("parseMyCnf = %+v, want %+v", *acct, want)
	}
	if got := acct.Endpoint(); got != "db.example.org:3307" {
		t.Errorf("Endpoint = %s", got)
	}
	if got := (&MySQLAccount{Socket: "/run/mysqld/mysqld.sock"}).Endpoint(); got != "localhost:/run/mysqld/mysqld.sock" {
		t.Errorf("Endpoint = %s", got)
	}

	// The temporary option file holds the connection options only and
	// reads back to the same values
	defaults, err := acct.defaultsFile()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(defaults)
	data, _ := os.ReadFile(defaults)
	if strings.Contains(string(data), "evil") || strings.Contains(string(data), "shop") {
		t.Errorf("defaults file has more than connection options:\n%s", data)
	}
	back, err := parseMyCnf(defaults)
	if err != nil {
		t.Fatal(err)
	}
	if back.password != acct.password || back.Host != acct.Host || back.Socket != acct.Socket {
		t.Errorf("defaults file reads back as %+v", *back)
	}
	if info, _ := os.Stat(defaults); info.Mode().Perm() != 0600 {
		t.Errorf("defaults file mode = %o", info.Mode().Perm())
	}
}

func TestSQLQuoting(t *testing.T) {
	if got := sqlIdent("we`ird"); got != "`we``ird`" {
		t.Errorf("sqlIdent = %s", got)
	}
	if got := sqlAccount("o'neil@local@host"); got != `'o''neil@local'@'host'` {
		t.Errorf("sqlAccount = %s", got)
	}
	remote := &MySQLAccount{Host: "db.example.org", Port: "3307"}
	if got := remote.adminCommand("DROP DATABASE `x`;"); got != "mysql -h 'db.example.org' -P '3307' -e 'DROP DATABASE `x`;'" {
		t.Errorf("adminCommand = %s", got)
	}
	if got := formatBytes(1536 * 1024); got != "1.5 MiB" {
		t.Errorf("formatBytes = %s", got)
	}
}

// fakeMySQL answers the client calls by the user in the defaults file
type fakeMySQL struct {
	answers map[string]string // user -> inventory output
	failing map[string]string // user -> error
	dumps   []string
}

func (f *fakeMySQL) run(_ context.Context, tool, defaults string, args []string, stdout io.Writer) error {
	acct, err := parseMyCnf(defaults)
	if err != nil {
		return err
	}
	if msg := f.failing[acct.User]; msg != "" {
		return errors.New(msg)
	}
	if tool == "mysqldump" {
		i := len(args) - 1
		for args[i] != "--" {
			i--
		}
		dbs := strings.Join(args[i+1:], " ")
		f.dumps = append(f.dumps, acct.User+": "+dbs)
		_, err := fmt.Fprintf(stdout, "-- dump of %s by %s\n", dbs, acct.User)
		return err
	}
	_, err = io.WriteString(stdout, f.answers[acct.User])
	return err
}

func setupMySQL(t *testing.T) (*fakeMySQL, map[string]*User) {
	t.Helper()
	setupFS(t)
	data, _ := os.ReadFile(passwdFile)
	write(t, passwdFile, string(data)+"blogger:x:61003:61003::"+filepath.Join(homeDir, "blogger")+":/bin/bash\n")
	write(t, filepath.Join(homeDir, "webmaster/.my.cnf"), "[client]\nuser=wm\npassword=pw1\n")
	write(t, filepath.Join(homeDir, "blogger/.my.cnf"), "[client]\npassword=pw2\n")
	if err := os.Chmod(filepath.Join(homeDir, "blogger/.my.cnf"), 0644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeMySQL{
		answers: map[string]string{
			"wm": "account\twm@localhost\t10.11.6-MariaDB\nprivilege\tUSAGE\n" +
				"schema\tshop\nschema\tcommon\nschema\tempty\n" +
				"table\tshop\torders\tInnoDB\t1000\t1572864\ntable\tshop\tcustomers\tInnoDB\t20\t16384\n" +
				"table\tcommon\tsettings\tMyISAM\t5\t2048\n",
			"blogger": "account\tblogger@localhost\t10.11.6-MariaDB\nprivilege\tUSAGE\n" +
				"schema\tcommon\nschema\tblog\ntable\tblog\twp_posts\tInnoDB\t12\t65536\n",
		},
		failing: map[string]string{},
	}
	old := runMySQLTool
	runMySQLTool = fake.run
	t.Cleanup(func() { runMySQLTool = old })
	users, err := readPasswdFile(true)
	if err != nil {
		t.Fatal(err)
	}
	return fake, users
}

func TestMySQLInventory(t *testing.T) {
	_, users := setupMySQL(t)
	inv := collectInventory(users)

	data := inv.For("webmaster")
	if len(data.MySQL) != 1 {
		t.Fatalf("webmaster has %d MySQL logins", len(data.MySQL))
	}
	acct := data.MySQL[0]
	if acct.Account != "wm@localhost" || acct.Global || acct.Error != "" {
		t.Errorf("account = %+v", acct)
	}
	var names []string
	for _, db := range acct.Databases {
		names = append(names, fmt.Sprintf("%s:%d:%v", db.Name, len(db.Tables), db.SharedWith))
	}
	if got := strings.Join(names, " "); got != "common:1:[blogger] empty:0:[] shop:2:[]" {
		t.Errorf("databases = %s", got)
	}
	if shop := acct.Databases[2]; shop.Size() != 1572864+16384 || shop.Rows() != 1020 || shop.Tables[0].Name != "customers" {
		t.Errorf("shop = %+v", shop)
	}
	// blogger has no user= and logs in with the account name
	if blogger := inv.For("blogger").MySQL; len(blogger) != 1 || blogger[0].User != "blogger" {
		t.Errorf("blogger = %+v", blogger)
	}

	var kinds []string
	for _, f := range inv.Findings {
		if strings.HasPrefix(f.Kind, "mysql") {
			kinds = append(kinds, f.Kind+" "+f.Path)
		}
	}
	if want := "mysql_cnf_readable " + filepath.Join(homeDir, "blogger/.my.cnf"); strings.Join(kinds, ",") != want {
		t.Errorf("findings = %v, want %s", kinds, want)
	}

	f, _ := parseListFilter([]string{"mysql"})
	if !f.shows(users["webmaster"], data) || f.shows(users["web"], inv.For("web")) {
		t.Error("filter mysql should show webmaster only")
	}
	if f, _ := parseListFilter([]string{"php"}); len(f.apply(data).MySQL) != 0 {
		t.Error("filter php should hide MySQL")
	}
}

func TestMySQLBackupAndPlan(t *testing.T) {
	fake, users := setupMySQL(t)
	user, inv := users["webmaster"], collectInventory(users)
	user.MySQL = inv.For("webmaster").MySQL

	dir := filepath.Join(t.TempDir(), "backups")
	archive, err := backupUser(user, users, dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MySQL) != 1 || strings.Join(manifest.MySQL[0].Databases, ",") != "common,empty,shop" ||
		manifest.MySQL[0].Endpoint != "localhost" {
		t.Fatalf("manifest dumps = %+v (calls %v)", manifest.MySQL, fake.dumps)
	}
	dumpPath := filepath.Join(dir, manifest.MySQL[0].File)
	file, err := os.Open(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(gz)
	if string(content) != "-- dump of common empty shop by wm\n" {
		t.Errorf("dump = %q", content)
	}
	if info, _ := os.Stat(dumpPath); info.Mode().Perm() != 0600 {
		t.Errorf("dump mode = %o", info.Mode().Perm())
	}
	sums, _ := os.ReadFile(strings.TrimSuffix(archive, ".tar.gz") + ".sha256")
	if !strings.Contains(string(sums), manifest.MySQL[0].SHA256+"  "+manifest.MySQL[0].File+"\n") {
		t.Errorf("checksums miss the dump:\n%s", sums)
	}

	script, err := buildDeletePlan(user, inv, archive, manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\nmysql -e 'DROP DATABASE IF EXISTS `shop`;'\n",
		"\nmysql -e 'DROP DATABASE IF EXISTS `empty`;'\n",
		"#    common is also used by blogger; keep it or drop it by hand:\n# mysql -e 'DROP DATABASE IF EXISTS `common`;'\n",
		"\nmysql -e 'DROP USER IF EXISTS '\\''wm'\\''@'\\''localhost'\\'';'\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("plan misses %q:\n%s", want, script)
		}
	}
	if strings.Index(script, "DROP USER") > strings.Index(script, "userdel") {
		t.Error("MySQL commands must come before userdel")
	}

	// A database created after the backup is not dropped
	manifest.MySQL[0].Databases = []string{"common", "empty"}
	script, _ = buildDeletePlan(user, inv, archive, manifest)
	if !strings.Contains(script, "#    shop is not in "+filepath.Base(archive)) || strings.Contains(script, "\nmysql -e 'DROP DATABASE IF EXISTS `shop`") {
		t.Errorf("shop must only be commented:\n%s", script)
	}
}

func TestMySQLFailuresAndGlobalLogins(t *testing.T) {
	fake, users := setupMySQL(t)
	fake.failing["wm"] = "ERROR 2002 (HY000): Can't connect to local server"
	fake.answers["blogger"] = "account\tblogger@%\t8.0.36\nprivilege\tSELECT\nprivilege\tPROCESS\nschema\tblog\nschema\tcommon\n"
	inv := collectInventory(users)

	wm := inv.For("webmaster").MySQL[0]
	if !strings.Contains(wm.Error, "Can't connect") {
		t.Errorf("error = %q", wm.Error)
	}
	found := false
	for _, f := range inv.Findings {
		found = found || f.Kind == "mysql_unreachable"
	}
	if !found {
		t.Error("unreachable server should be a finding")
	}
	blogger := inv.For("blogger").MySQL[0]
	if !blogger.Global || blogger.dumpDatabases() != nil {
		t.Errorf("blogger should be global and not dumped: %+v", blogger)
	}
	// Global logins do not make other databases shared
	if db := blogger.Databases[1]; len(db.SharedWith) != 0 {
		t.Errorf("common shared with %v", db.SharedWith)
	}

	user := users["blogger"]
	user.MySQL = inv.For("blogger").MySQL
	dir := filepath.Join(t.TempDir(), "backups")
	archive, err := backupUser(user, users, dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := readManifest(archive)
	if len(manifest.MySQL) != 0 || len(manifest.Skipped) != 1 || !strings.Contains(manifest.Skipped[0], "privileges on all databases") {
		t.Errorf("manifest = %+v / %v", manifest.MySQL, manifest.Skipped)
	}
	script, _ := buildDeletePlan(user, inv, archive, manifest)
	if strings.Contains(script, "\nmysql -e") || !strings.Contains(script, "# mysql -e 'DROP DATABASE IF EXISTS `blog`;'") {
		t.Errorf("global login must only produce comments:\n%s", script)
	}

	// A failing dump fails the backup and leaves nothing behind
	fake.failing["blogger"] = ""
	fake.answers["blogger"] = "account\tblogger@localhost\t8.0.36\nschema\tblog\n"
	inv = collectInventory(users)
	user.MySQL = inv.For("blogger").MySQL
	fake.failing["blogger"] = "mysqldump: Got error: 1044"
	dir = filepath.Join(t.TempDir(), "backups")
	if _, err := backupUser(user, users, dir); err == nil || !strings.Contains(err.Error(), "1044") {
		t.Fatalf("backup error = %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("failed backup left %d files", len(entries))
	}
}
