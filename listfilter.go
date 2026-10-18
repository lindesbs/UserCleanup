package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// listAreas are the areas "list --only" can show, with a description
var listAreas = map[string]string{
	"users":     "account data: UID/GID, home, shell, login",
	"sites":     "web sites of all servers",
	"apache":    "Apache sites",
	"nginx":     "nginx sites",
	"caddy":     "Caddy sites",
	"php":       "PHP-FPM pools",
	"systemd":   "systemd units, user units and linger",
	"cron":      "crontab and cron entries in /etc",
	"etc":       "all entries in /etc (sudoers, cron, sshd, limits, aliases)",
	"sudoers":   "sudoers entries",
	"sshd":      "sshd entries",
	"limits":    "limits entries",
	"aliases":   "mail aliases",
	"logrotate": "logrotate entries",
	"dirs":      "directories outside the home",
	"files":     "other files in /etc that mention the user",
	"unowned":   "sites without a known owner",
	"findings":  "orphaned or broken entries",
}

// listFilter selects the areas "list" shows; an empty filter shows all
type listFilter map[string]bool

// sortedAreas returns the area names in alphabetical order
func sortedAreas() []string {
	areas := make([]string, 0, len(listAreas))
	for area := range listAreas {
		areas = append(areas, area)
	}
	sort.Strings(areas)
	return areas
}

// parseListFilter validates the areas given with --only; each value may
// hold several areas separated by commas
func parseListFilter(values []string) (listFilter, error) {
	f := listFilter{}
	for _, value := range values {
		for _, area := range strings.Split(value, ",") {
			area = strings.ToLower(strings.TrimSpace(area))
			if area == "" {
				continue
			}
			if _, ok := listAreas[area]; !ok {
				return nil, fmt.Errorf("unknown area %q, valid areas: %s", area, strings.Join(sortedAreas(), ", "))
			}
			f[area] = true
		}
	}
	return f, nil
}

// has reports whether area is shown
func (f listFilter) has(area string) bool {
	return len(f) == 0 || f[area]
}

// site reports whether a site of the given server is shown
func (f listFilter) site(server string) bool {
	return f.has("sites") || f[server]
}

// ref reports whether an entry in /etc of the given kind is shown
func (f listFilter) ref(kind string) bool {
	return f.has("etc") || f[kind]
}

// crontab reports whether the user's crontab is shown
func (f listFilter) crontab() bool {
	return f.has("cron")
}

// siteAreas reports whether the filter asks for any kind of site
func (f listFilter) siteAreas() bool {
	return len(f) == 0 || f["sites"] || f["apache"] || f["nginx"] || f["caddy"]
}

// sites returns the sites the filter shows
func (f listFilter) sites(sites []*Site) []*Site {
	var out []*Site
	for _, site := range sites {
		if f.site(site.Server) {
			out = append(out, site)
		}
	}
	return out
}

// apply returns the part of data the filter shows
func (f listFilter) apply(data UserData) UserData {
	if len(f) == 0 {
		return data
	}
	var out UserData
	out.Sites = f.sites(data.Sites)
	if f.has("php") {
		out.Pools = data.Pools
	}
	if f.has("systemd") {
		out.Units, out.UserUnits, out.Linger = data.Units, data.UserUnits, data.Linger
	}
	for _, ref := range data.Refs {
		if f.ref(ref.Kind) {
			out.Refs = append(out.Refs, ref)
		}
	}
	if f.has("logrotate") {
		out.Logrotate = data.Logrotate
	}
	if f.has("dirs") {
		out.Dirs = data.Dirs
	}
	return out
}

// otherConfigs returns the files in /etc that mention the user but are not
// part of any parsed entry of the user
func otherConfigs(user *User, data UserData) []string {
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
	return others
}

// shows reports whether the filter leaves anything to show for the user;
// data is the unfiltered data of the user
func (f listFilter) shows(user *User, data UserData) bool {
	if f.has("users") {
		return true
	}
	d := f.apply(data)
	return len(d.Sites) > 0 || len(d.Pools) > 0 || len(d.Units) > 0 ||
		len(d.UserUnits) > 0 || d.Linger || len(d.Refs) > 0 ||
		len(d.Logrotate) > 0 || len(d.Dirs) > 0 ||
		(f.crontab() && exists(filepath.Join(cronDir, user.Username))) ||
		(f.has("files") && len(otherConfigs(user, data)) > 0)
}

// needsConfigs reports whether the slow text search through /etc is needed
func (f listFilter) needsConfigs() bool {
	return f.has("files")
}

// areaHelp lists the areas for the help text
func areaHelp() string {
	var b strings.Builder
	for _, area := range sortedAreas() {
		fmt.Fprintf(&b, "  %-10s %s\n", area, listAreas[area])
	}
	return b.String()
}
