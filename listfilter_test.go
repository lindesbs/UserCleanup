package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseListFilter(t *testing.T) {
	f, err := parseListFilter([]string{"Users, apache", "php"})
	if err != nil {
		t.Fatal(err)
	}
	if !f["users"] || !f["apache"] || !f["php"] || len(f) != 3 {
		t.Errorf("got %v", f)
	}
	if _, err := parseListFilter([]string{"apache,postgres"}); err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Errorf("unknown area not rejected: %v", err)
	}
	if f, _ := parseListFilter(nil); !f.has("findings") || !f.site("nginx") {
		t.Error("empty filter must show everything")
	}
}

func TestListFilterApply(t *testing.T) {
	data := UserData{
		Sites:     []*Site{{Server: "apache", File: "a"}, {Server: "nginx", File: "n"}},
		Pools:     []*Pool{{Name: "web"}},
		Logrotate: []*LogrotateEntry{{File: "l"}},
		Units:     []*Unit{{Name: "x.service"}},
		Refs:      []*Reference{{Kind: "sudoers"}, {Kind: "cron"}},
		Linger:    true,
		Dirs:      []string{"/var/www/web"},
	}

	f, _ := parseListFilter([]string{"apache,cron"})
	got := f.apply(data)
	if len(got.Sites) != 1 || got.Sites[0].Server != "apache" {
		t.Errorf("sites: %v", got.Sites)
	}
	if len(got.Refs) != 1 || got.Refs[0].Kind != "cron" {
		t.Errorf("refs: %v", got.Refs)
	}
	if got.Pools != nil || got.Logrotate != nil || got.Units != nil || got.Linger || got.Dirs != nil {
		t.Errorf("unselected areas kept: %+v", got)
	}

	f, _ = parseListFilter([]string{"etc,sites,systemd"})
	got = f.apply(data)
	if len(got.Refs) != 2 || len(got.Sites) != 2 || len(got.Units) != 1 || !got.Linger {
		t.Errorf("etc/sites/systemd: %+v", got)
	}
}

func TestListFilterShows(t *testing.T) {
	setupFS(t)
	user := &User{Username: "web", HomeDir: filepath.Join(homeDir, "web")}
	data := UserData{Pools: []*Pool{{Name: "web", File: "/etc/php/pool.d/web.conf"}}}

	for areas, want := range map[string]bool{
		"users": true, "php": true, "apache": false, "cron": false, "files": false,
	} {
		f, _ := parseListFilter([]string{areas})
		if got := f.shows(user, data); got != want {
			t.Errorf("%s: shows = %v, want %v", areas, got, want)
		}
	}

	write(t, filepath.Join(cronDir, "web"), "* * * * * true\n")
	user.Configs = []string{"/etc/php/pool.d/web.conf", "/etc/motd"}
	for _, areas := range []string{"cron", "files"} {
		f, _ := parseListFilter([]string{areas})
		if !f.shows(user, data) {
			t.Errorf("%s: user not shown", areas)
		}
	}
	if others := otherConfigs(user, data); len(others) != 1 || others[0] != "/etc/motd" {
		t.Errorf("other configs: %v", others)
	}
}
