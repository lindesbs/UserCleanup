package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupServices writes web server, PHP-FPM and logrotate configs:
// web has a dedicated Apache site, a pool and logrotate entry, shares an
// Apache file and the Caddyfile with webmaster; ghost is a deleted user.
func setupServices(t *testing.T) {
	t.Helper()
	// Without the user "shared" from setupFS, so web's home is its own
	write(t, passwdFile, "root:x:0:0:root:/root:/bin/bash\n"+
		"www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin\n"+
		"web:x:1000:1000::"+filepath.Join(homeDir, "web")+":/usr/sbin/nologin\n"+
		"webmaster:x:1001:1001::"+filepath.Join(homeDir, "webmaster")+":/bin/bash\n")
	webHome := filepath.Join(homeDir, "web")
	masterHome := filepath.Join(homeDir, "webmaster")
	for _, dir := range []string{webHome + "/public", masterHome + "/public", filepath.Join(homeDir, "ghost")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	apache := filepath.Join(etcDir, "apache2")
	write(t, filepath.Join(apache, "sites-available/web.conf"), `# Site of web
<VirtualHost *:443>
    ServerName web.example.org
    ServerAlias www.web.example.org
    DocumentRoot "`+webHome+`/public"
    <FilesMatch "\.php$">
        SetHandler "proxy:unix:/run/php/web.sock|fcgi://localhost"
    </FilesMatch>
</VirtualHost>
`)
	write(t, filepath.Join(apache, "sites-available/shared.conf"), `<VirtualHost *:80>
    ServerName a.example.org
    DocumentRoot `+webHome+`/public
</VirtualHost>
<VirtualHost *:80>
    ServerName b.example.org
    DocumentRoot `+masterHome+`/public
    SuexecUserGroup webmaster webmaster
</VirtualHost>
`)
	write(t, filepath.Join(apache, "sites-available/old.conf"), `<VirtualHost *:80>
    ServerName old.example.org
    DocumentRoot /home/ghost/htdocs
    SetHandler "proxy:unix:/run/php/ghost.sock|fcgi://localhost"
</VirtualHost>
`)
	if err := os.MkdirAll(filepath.Join(apache, "sites-enabled"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web.conf", "shared.conf"} {
		if err := os.Symlink("../sites-available/"+name, filepath.Join(apache, "sites-enabled", name)); err != nil {
			t.Fatal(err)
		}
	}

	write(t, filepath.Join(etcDir, "nginx/conf.d/web.conf"), `server {
    listen 80;
    server_name nginx.web.example.org; # comment
    root /srv/none;
    location ~ \.php$ {
        fastcgi_pass unix:/run/php/web.sock;
    }
}
`)

	write(t, filepath.Join(etcDir, "caddy/Caddyfile"), `{
    email admin@example.org
}
(common) {
    encode gzip
}
caddy.web.example.org, www.caddy.web.example.org {
    root * `+webHome+`/public
    php_fastcgi unix//run/php/web.sock
}
import sites/*.caddy
`)
	write(t, filepath.Join(etcDir, "caddy/sites/master.caddy"), `master.example.org {
    root * `+masterHome+`/public
    file_server
}
`)

	write(t, filepath.Join(etcDir, "php/8.2/fpm/pool.d/web.conf"), `; pool of web
[web]
user = web
group = web
listen = /run/php/web.sock
php_admin_value[open_basedir] = `+webHome+`
`)
	write(t, filepath.Join(etcDir, "php/8.2/fpm/pool.d/ghost.conf"), `[ghost]
user = ghost
listen = /run/php/ghost.sock
chdir = /home/ghost/htdocs
`)

	write(t, filepath.Join(etcDir, "logrotate.d/web"), webHome+`/logs/*.log
`+webHome+`/logs/*.err {
    daily
    su web web
    create 0640 web web
    postrotate
        if [ -f /run/x.pid ]; then kill -USR1 $(cat /run/x.pid); fi
    endscript
}
`)
	write(t, filepath.Join(etcDir, "logrotate.d/ghost"), `/home/ghost/logs/*.log {
    su ghost ghost
}
`)
}

func TestParsers(t *testing.T) {
	setupFS(t)
	setupServices(t)

	sites := collectSites()
	byName := make(map[string]*Site)
	for _, site := range sites {
		byName[site.Names[0]] = site
	}
	if len(sites) != 7 {
		t.Fatalf("got %d sites: %v", len(sites), byName)
	}

	web := byName["web.example.org"]
	if web.Root != filepath.Join(homeDir, "web/public") || !web.Enabled || web.Line != 2 || web.EndLine != 9 ||
		len(web.Names) != 2 || strings.Join(web.Backends, ",") != "/run/php/web.sock" || web.EnabledLink == "" {
		t.Errorf("apache site = %+v", web)
	}
	if old := byName["old.example.org"]; old.Enabled {
		t.Error("old.example.org should be disabled")
	}
	if b := byName["b.example.org"]; b.User != "webmaster" {
		t.Errorf("suexec user = %q", b.User)
	}
	if n := byName["nginx.web.example.org"]; n.Server != "nginx" || n.Root != "/srv/none" || strings.Join(n.Backends, ",") != "/run/php/web.sock" {
		t.Errorf("nginx site = %+v", n)
	}
	c := byName["caddy.web.example.org"]
	if c.Server != "caddy" || len(c.Names) != 2 || c.Root != filepath.Join(homeDir, "web/public") || strings.Join(c.Backends, ",") != "/run/php/web.sock" {
		t.Errorf("caddy site = %+v", c)
	}
	if m := byName["master.example.org"]; m == nil || !strings.HasSuffix(m.File, "master.caddy") {
		t.Errorf("imported caddy site = %+v", m)
	}

	pools := collectPools()
	if len(pools) != 2 || pools[1].Name != "web" || pools[1].Version != "8.2" || pools[1].Listen != "/run/php/web.sock" ||
		pools[1].Settings["php_admin_value[open_basedir]"] == "" || pools[1].Line != 2 {
		t.Errorf("pools = %+v %+v", pools[0], pools[1])
	}

	logs := collectLogrotate()
	var webLog *LogrotateEntry
	for _, entry := range logs {
		if strings.HasSuffix(entry.File, "/web") {
			webLog = entry
		}
	}
	if webLog == nil || len(webLog.Paths) != 2 || webLog.Su != "web" || webLog.CreateOwner != "web" || webLog.Line != 1 || webLog.EndLine != 9 {
		t.Errorf("logrotate = %+v", webLog)
	}
}

func TestInventoryOwnersAndFindings(t *testing.T) {
	setupFS(t)
	setupServices(t)
	users, _ := readPasswdFile(true)
	inv := collectInventory(users)

	data := inv.For("web")
	var names []string
	for _, site := range data.Sites {
		names = append(names, site.Names[0])
	}
	// nginx site has no root in a home, but uses the pool of web
	want := "a.example.org,web.example.org,nginx.web.example.org,caddy.web.example.org"
	if strings.Join(names, ",") != want {
		t.Errorf("sites of web = %s, want %s", strings.Join(names, ","), want)
	}
	if len(data.Pools) != 1 || len(data.Logrotate) != 1 {
		t.Errorf("pools/logrotate of web = %d/%d", len(data.Pools), len(data.Logrotate))
	}

	apache := filepath.Join(etcDir, "apache2/sites-available")
	if !inv.dedicated(filepath.Join(apache, "web.conf"), "web") {
		t.Error("web.conf should be dedicated to web")
	}
	if inv.dedicated(filepath.Join(apache, "shared.conf"), "web") {
		t.Error("shared.conf must not be dedicated")
	}
	if inv.dedicated(filepath.Join(etcDir, "caddy/Caddyfile"), "web") {
		t.Error("the main Caddyfile must never be dedicated")
	}

	kinds := make(map[string]int)
	for _, f := range inv.Findings {
		kinds[f.Kind]++
	}
	for kind, n := range map[string]int{
		"orphan_home": 1, "pool_user_missing": 1, "pool_chdir_missing": 1, "site_root_missing": 2,
		"logrotate_user_missing": 1, "logrotate_path_missing": 3,
	} {
		if kinds[kind] != n {
			t.Errorf("%s findings = %d, want %d (%+v)", kind, kinds[kind], n, inv.Findings)
		}
	}
	// the ghost socket is still served by the ghost pool
	if kinds["site_pool_missing"] != 0 {
		t.Errorf("unexpected site_pool_missing: %+v", inv.Findings)
	}
}

func TestPlanWithServices(t *testing.T) {
	setupFS(t)
	setupServices(t)
	users, _ := readPasswdFile(true)
	inv := collectInventory(users)
	user := users["web"]

	script, err := buildDeletePlan(user, inv, "/var/backups/usercleanup/web-20261018-090000.tar.gz", &Manifest{Username: "web"})
	if err != nil {
		t.Fatal(err)
	}
	q := func(path string) string { return "'" + filepath.Join(etcDir, path) + "'" }
	for _, want := range []string{
		// dedicated Apache site: link and file removed, then test and reload
		"rm -- " + q("apache2/sites-enabled/web.conf") + "\nrm -- " + q("apache2/sites-available/web.conf") +
			"\napache2ctl configtest\nsystemctl reload apache2\n",
		// shared Apache file stays manual
		"# " + q("apache2/sites-available/shared.conf") + " lines 1-4: site a.example.org",
		"rm -- " + q("nginx/conf.d/web.conf") + "\nnginx -t\nsystemctl reload nginx\n",
		// Caddyfile is shared: only manual, so test and reload are commented out
		"# " + q("caddy/Caddyfile") + " lines 7-10: site caddy.web.example.org www.caddy.web.example.org",
		"# caddy validate",
		"# systemctl reload caddy",
		"rm -- " + q("php/8.2/fpm/pool.d/web.conf") + "\nphp-fpm8.2 -t\nsystemctl reload php8.2-fpm\n",
		"rm -- " + q("logrotate.d/web") + "\nlogrotate -d " + q("logrotate.conf") + " >/dev/null 2>&1 || {",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("plan misses %q:\n%s", want, script)
		}
	}
	if strings.Index(script, "systemctl reload apache2") > strings.Index(script, "systemctl reload php8.2-fpm") ||
		strings.Index(script, "systemctl reload php8.2-fpm") > strings.Index(script, "userdel") {
		t.Errorf("wrong order: web servers, then PHP-FPM, then userdel:\n%s", script)
	}
	if strings.Contains(script, "master") || strings.Contains(script, "ghost") {
		t.Errorf("plan touches other users:\n%s", script)
	}
}
