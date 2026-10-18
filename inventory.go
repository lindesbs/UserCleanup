package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Inventory is everything collected about users, sites, pools and logrotate
type Inventory struct {
	All       map[string]*User  `json:"-"`
	Sites     []*Site           `json:"sites"`
	Pools     []*Pool           `json:"pools"`
	Logrotate []*LogrotateEntry `json:"logrotate"`
	Findings  []Finding         `json:"findings"`
	// fileOwners maps each parsed config file to the owners of its blocks;
	// "" stands for a block without a known owner
	fileOwners map[string]map[string]bool
}

// Finding is something that looks orphaned or broken
type Finding struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Line   int    `json:"line,omitempty"`
	Detail string `json:"detail"`
}

// UserData is what belongs to one user
type UserData struct {
	Sites     []*Site           `json:"sites"`
	Pools     []*Pool           `json:"pools"`
	Logrotate []*LogrotateEntry `json:"logrotate"`
}

// isUnder reports whether path is dir or below it. "/" and empty
// directories never match, so users without a real home own nothing.
func isUnder(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	if dir == "/" {
		return false
	}
	return path == dir || strings.HasPrefix(path, dir+"/")
}

// normalizeEndpoint makes PHP-FPM listen addresses comparable:
// "9000", "127.0.0.1:9000" and "localhost:9000" are the same
func normalizeEndpoint(endpoint string) string {
	endpoint = strings.TrimSuffix(strings.TrimPrefix(endpoint, "unix:"), "/")
	if strings.HasPrefix(endpoint, "/") {
		return filepath.Clean(endpoint)
	}
	if !strings.Contains(endpoint, ":") {
		return "127.0.0.1:" + endpoint
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return endpoint
	}
	if host == "localhost" || host == "" {
		host = "127.0.0.1"
	}
	return host + ":" + port
}

// collectInventory parses all sources and assigns the results to users
func collectInventory(all map[string]*User) *Inventory {
	inv := &Inventory{
		All:        all,
		Sites:      collectSites(),
		Pools:      collectPools(),
		Logrotate:  collectLogrotate(),
		fileOwners: make(map[string]map[string]bool),
	}
	inv.assignOwners()
	inv.findOrphans()
	return inv
}

// ownersByHome returns the users whose home contains path. System users
// are left out: www-data has /var/www as home but does not own every site.
func (inv *Inventory) ownersByHome(path string) []string {
	var owners []string
	for _, user := range sortedUsers(inv.All) {
		if isProtected(user) == "" && isUnder(path, user.HomeDir) {
			owners = append(owners, user.Username)
		}
	}
	return owners
}

func (inv *Inventory) addOwner(owners []string, name string) []string {
	if name == "" || inv.All[name] == nil {
		return owners
	}
	return appendUnique(owners, name)
}

func (inv *Inventory) noteFile(file string, owners []string) {
	set := inv.fileOwners[file]
	if set == nil {
		set = make(map[string]bool)
		inv.fileOwners[file] = set
	}
	if len(owners) == 0 {
		set[""] = true
	}
	for _, owner := range owners {
		set[owner] = true
	}
}

// assignOwners decides which users a pool, site or logrotate entry belongs to:
// pools by their user, sites by suexec user, document root in a home or a
// backend pool, logrotate entries by su/create user or log path in a home.
func (inv *Inventory) assignOwners() {
	poolsByEndpoint := make(map[string][]*Pool)
	for _, pool := range inv.Pools {
		pool.Owners = inv.addOwner(nil, pool.User)
		if pool.Listen != "" {
			ep := normalizeEndpoint(pool.Listen)
			poolsByEndpoint[ep] = append(poolsByEndpoint[ep], pool)
		}
		inv.noteFile(pool.File, pool.Owners)
	}

	for _, site := range inv.Sites {
		owners := inv.addOwner(nil, site.User)
		for _, owner := range inv.ownersByHome(site.Root) {
			owners = appendUnique(owners, owner)
		}
		for _, backend := range site.Backends {
			for _, pool := range poolsByEndpoint[normalizeEndpoint(backend)] {
				owners = inv.addOwner(owners, pool.User)
			}
		}
		sort.Strings(owners)
		site.Owners = owners
		inv.noteFile(site.File, owners)
		if site.File == filepath.Join(etcDir, "caddy", "Caddyfile") {
			// The main Caddyfile also holds global options and imports
			inv.noteFile(site.File, nil)
		}
	}

	for _, entry := range inv.Logrotate {
		owners := inv.addOwner(nil, entry.Su)
		owners = inv.addOwner(owners, entry.CreateOwner)
		for _, path := range entry.Paths {
			for _, owner := range inv.ownersByHome(path) {
				owners = appendUnique(owners, owner)
			}
		}
		sort.Strings(owners)
		entry.Owners = owners
		inv.noteFile(entry.File, owners)
	}
}

// For returns what belongs to a user
func (inv *Inventory) For(username string) UserData {
	var data UserData
	for _, site := range inv.Sites {
		if contains(site.Owners, username) {
			data.Sites = append(data.Sites, site)
		}
	}
	for _, pool := range inv.Pools {
		if contains(pool.Owners, username) {
			data.Pools = append(data.Pools, pool)
		}
	}
	for _, entry := range inv.Logrotate {
		if contains(entry.Owners, username) {
			data.Logrotate = append(data.Logrotate, entry)
		}
	}
	return data
}

// Files returns all config files (and enabled links) with something of the user
func (d UserData) Files() []string {
	var files []string
	for _, site := range d.Sites {
		files = appendUnique(files, site.File)
		if site.EnabledLink != "" {
			files = appendUnique(files, site.EnabledLink)
		}
	}
	for _, pool := range d.Pools {
		files = appendUnique(files, pool.File)
	}
	for _, entry := range d.Logrotate {
		files = appendUnique(files, entry.File)
	}
	sort.Strings(files)
	return files
}

// dedicated reports whether every block in file belongs only to username,
// so the whole file can be removed without touching anyone else.
func (inv *Inventory) dedicated(file, username string) bool {
	owners := inv.fileOwners[file]
	return len(owners) == 1 && owners[username]
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// findOrphans looks for leftovers of deleted users and broken references
func (inv *Inventory) findOrphans() {
	add := func(kind, path string, line int, format string, args ...any) {
		inv.Findings = append(inv.Findings, Finding{Kind: kind, Path: path, Line: line, Detail: fmt.Sprintf(format, args...)})
	}
	uids := make(map[uint32]bool)
	homes := make(map[string]bool)
	for _, user := range inv.All {
		uids[uint32(user.UID)] = true
		homes[filepath.Clean(user.HomeDir)] = true
	}

	entries, _ := os.ReadDir(homeDir)
	for _, entry := range entries {
		path := filepath.Join(homeDir, entry.Name())
		if !entry.IsDir() || homes[path] {
			continue
		}
		detail := "no user has this home directory"
		if info, err := os.Stat(path); err == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok && !uids[st.Uid] {
				detail += fmt.Sprintf(", owner UID %d has no account", st.Uid)
			}
		}
		add("orphan_home", path, 0, "%s", detail)
	}

	endpoints := make(map[string]bool)
	for _, pool := range inv.Pools {
		if pool.Listen != "" {
			endpoints[normalizeEndpoint(pool.Listen)] = true
		}
		if pool.User != "" && inv.All[pool.User] == nil {
			add("pool_user_missing", pool.File, pool.Line, "PHP-FPM pool [%s] runs as missing user %s; php-fpm will not start", pool.Name, pool.User)
		}
		if pool.Chdir != "" && !exists(pool.Chdir) {
			add("pool_chdir_missing", pool.File, pool.Line, "PHP-FPM pool [%s] chdir %s does not exist", pool.Name, pool.Chdir)
		}
	}

	for _, site := range inv.Sites {
		name := site.Server
		if len(site.Names) > 0 {
			name += " " + site.Names[0]
		}
		if site.Root != "" && !exists(site.Root) {
			add("site_root_missing", site.File, site.Line, "%s: document root %s does not exist", name, site.Root)
		}
		if site.User != "" && inv.All[site.User] == nil {
			add("site_user_missing", site.File, site.Line, "%s: runs as missing user %s", name, site.User)
		}
		for _, backend := range site.Backends {
			ep := normalizeEndpoint(backend)
			if strings.HasPrefix(ep, "/") && !endpoints[ep] {
				add("site_pool_missing", site.File, site.Line, "%s: no PHP-FPM pool listens on %s", name, backend)
			}
		}
	}

	for _, entry := range inv.Logrotate {
		for _, name := range []string{entry.Su, entry.CreateOwner} {
			if name != "" && inv.All[name] == nil {
				add("logrotate_user_missing", entry.File, entry.Line, "user %s does not exist; logrotate will fail", name)
			}
		}
		for _, path := range entry.Paths {
			dir := filepath.Dir(path)
			if matches, _ := filepath.Glob(dir); len(matches) == 0 {
				add("logrotate_path_missing", entry.File, entry.Line, "directory of %s does not exist", path)
			}
		}
	}
}
