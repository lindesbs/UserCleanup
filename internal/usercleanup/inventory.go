package usercleanup

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
	Units     []*Unit           `json:"units"`
	Refs      []*Reference      `json:"references"`
	MySQL     []*MySQLAccount   `json:"mysql"`
	Findings  []Finding         `json:"findings"`
	refFiles  map[string]*refFile
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
	Units     []*Unit           `json:"units"`
	Refs      []*Reference      `json:"references"`
	MySQL     []*MySQLAccount   `json:"mysql"`
	// UserUnits are systemd user units in the home; Linger means they
	// keep running without a login
	UserUnits []string `json:"user_units,omitempty"`
	Linger    bool     `json:"linger"`
	// Dirs are directories outside the home that are owned by the user,
	// such as webroots below /var/www or log directories
	Dirs []string `json:"dirs"`
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
		Units:      collectUnits(),
		MySQL:      collectMySQL(all),
		fileOwners: make(map[string]map[string]bool),
		refFiles:   make(map[string]*refFile),
	}
	for _, rf := range collectReferences() {
		for _, ref := range rf.refs {
			inv.refFiles[ref.File] = rf
		}
		inv.Refs = append(inv.Refs, rf.refs...)
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

// ownerByUID returns the deletable user owning path, or ""
func (inv *Inventory) ownerByUID(path string) string {
	uid, ok := fileUID(path)
	if !ok {
		return ""
	}
	for _, user := range inv.All {
		if user.UID == int(uid) && isProtected(user) == "" {
			return user.Username
		}
	}
	return ""
}

// fileUID returns the owner of path
func fileUID(path string) (uint32, bool) {
	if path == "" {
		return 0, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
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

// assignOwners decides which users a pool, site, logrotate entry or unit
// belongs to: pools and units by their user; sites by suexec user, document
// root in a home or owned by the user, and only if none of these applies by
// the user of the PHP-FPM pool they use; logrotate entries by su/create user
// or log directory in a home or owned by the user.
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
		owners = inv.addOwner(owners, inv.ownerByUID(site.Root))
		if len(owners) == 0 {
			for _, backend := range site.Backends {
				for _, pool := range poolsByEndpoint[normalizeEndpoint(backend)] {
					owners = inv.addOwner(owners, pool.User)
				}
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

	// A pool used by a site of another user must not be removed with its owner
	for _, site := range inv.Sites {
		for _, backend := range site.Backends {
			for _, pool := range poolsByEndpoint[normalizeEndpoint(backend)] {
				if len(site.Owners) > 0 && !overlaps(site.Owners, pool.Owners) {
					pool.UsedBy = appendUnique(pool.UsedBy, siteLabel(site)+" ("+strings.Join(site.Owners, ", ")+")")
				}
			}
		}
	}

	for _, unit := range inv.Units {
		owners := inv.addOwner(nil, unit.User)
		for _, path := range unit.Paths {
			for _, owner := range inv.ownersByHome(path) {
				owners = appendUnique(owners, owner)
			}
		}
		sort.Strings(owners)
		unit.Owners = owners
	}

	for _, ref := range inv.Refs {
		for _, name := range ref.Users {
			ref.Owners = inv.addOwner(ref.Owners, name)
		}
	}

	for _, entry := range inv.Logrotate {
		owners := inv.addOwner(nil, entry.Su)
		owners = inv.addOwner(owners, entry.CreateOwner)
		for _, path := range entry.Paths {
			for _, owner := range inv.ownersByHome(path) {
				owners = appendUnique(owners, owner)
			}
			owners = inv.addOwner(owners, inv.ownerByUID(filepath.Dir(path)))
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
	for _, unit := range inv.Units {
		if contains(unit.Owners, username) {
			data.Units = append(data.Units, unit)
		}
	}
	for _, ref := range inv.Refs {
		if contains(ref.Owners, username) {
			data.Refs = append(data.Refs, ref)
		}
	}
	for _, acct := range inv.MySQL {
		if acct.Owner == username {
			data.MySQL = append(data.MySQL, acct)
		}
	}
	if user := inv.All[username]; user != nil {
		data.UserUnits = userUnits(user)
	}
	data.Linger = exists(filepath.Join(lingerDir, username))
	data.Dirs = inv.ownedDirs(username, data)
	return data
}

// refRemovable reports whether a whole drop-in file (sudoers.d, cron.d,
// sshd_config.d, limits.d) only concerns username and can be removed
func (inv *Inventory) refRemovable(file, username string) bool {
	rf := inv.refFiles[file]
	if rf == nil || !rf.removable || rf.other {
		return false
	}
	for _, ref := range rf.refs {
		if len(ref.Users) != 1 || ref.Users[0] != username {
			return false
		}
	}
	return true
}

// ownedDirs returns directories outside the home that belong to the user:
// webroots, log directories and pool chdirs owned by the user's UID, widened
// to the topmost parent with the same owner (e.g. /var/www/site for
// /var/www/site/htdocs). Directories that contain a home or a site of
// another user are left out.
func (inv *Inventory) ownedDirs(username string, data UserData) []string {
	user := inv.All[username]
	if user == nil || isProtected(user) != "" {
		return nil
	}
	var candidates []string
	for _, site := range data.Sites {
		candidates = append(candidates, site.Root)
	}
	for _, entry := range data.Logrotate {
		for _, path := range entry.Paths {
			dir := filepath.Dir(path)
			if !strings.ContainsAny(dir, "*?[") {
				candidates = append(candidates, dir)
			}
		}
	}
	for _, pool := range data.Pools {
		candidates = append(candidates, pool.Chdir)
	}

	var dirs []string
	for _, dir := range candidates {
		if uid, ok := fileUID(dir); !ok || int(uid) != user.UID || isUnder(dir, user.HomeDir) {
			continue
		}
		dir = filepath.Clean(dir)
		for parent := filepath.Dir(dir); parent != "/"; parent = filepath.Dir(parent) {
			if uid, ok := fileUID(parent); !ok || int(uid) != user.UID {
				break
			}
			dir = parent
		}
		if inv.sharedDir(dir, username) {
			continue
		}
		dirs = appendUnique(dirs, dir)
	}

	sort.Strings(dirs)
	var result []string
	for _, dir := range dirs {
		if len(result) == 0 || !isUnder(dir, result[len(result)-1]) {
			result = append(result, dir)
		}
	}
	return result
}

// sharedDir reports whether dir holds a home or site of another user
func (inv *Inventory) sharedDir(dir, username string) bool {
	if dir == "/" || isUnder(homeDir, dir) {
		return true
	}
	for _, other := range inv.All {
		if other.Username != username && isUnder(other.HomeDir, dir) {
			return true
		}
	}
	for _, site := range inv.Sites {
		if isUnder(site.Root, dir) && !(len(site.Owners) == 1 && site.Owners[0] == username) {
			return true
		}
	}
	return false
}

// UnownedSites returns the sites no user could be assigned to
func (inv *Inventory) UnownedSites() []*Site {
	var sites []*Site
	for _, site := range inv.Sites {
		if len(site.Owners) == 0 {
			sites = append(sites, site)
		}
	}
	return sites
}

// siteLabel names a site for messages
func siteLabel(site *Site) string {
	if len(site.Names) > 0 {
		return site.Server + " " + strings.Join(site.Names, " ")
	}
	return site.Server + " " + filepath.Base(site.File) + " (no server name)"
}

func overlaps(a, b []string) bool {
	for _, v := range a {
		if contains(b, v) {
			return true
		}
	}
	return false
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
	for _, unit := range d.Units {
		files = appendUnique(files, unit.File)
		if unit.Timer != "" {
			files = appendUnique(files, unit.Timer)
		}
	}
	for _, ref := range d.Refs {
		files = appendUnique(files, ref.File)
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

	var orphanHomes []string
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
		orphanHomes = append(orphanHomes, path)
	}
	inOrphanHome := func(path string) string {
		for _, home := range orphanHomes {
			if isUnder(path, home) {
				return home
			}
		}
		return ""
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
		for _, site := range pool.UsedBy {
			add("pool_shared", pool.File, pool.Line, "PHP-FPM pool [%s] of %s is also used by %s", pool.Name, pool.User, site)
		}
	}

	for _, site := range inv.Sites {
		name := siteLabel(site)
		state := ""
		if !site.Enabled {
			state = " (disabled)"
		}
		if home := inOrphanHome(site.Root); home != "" {
			add("site_in_orphan_home", site.File, site.Line, "%s%s: document root %s is in orphaned home %s", name, state, site.Root, home)
		} else if site.Root != "" && !exists(site.Root) {
			add("site_root_missing", site.File, site.Line, "%s%s: document root %s does not exist", name, state, site.Root)
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
		names := []string{entry.Su}
		if entry.CreateOwner != entry.Su {
			names = append(names, entry.CreateOwner)
		}
		for _, name := range names {
			if name != "" && inv.All[name] == nil {
				add("logrotate_user_missing", entry.File, entry.Line, "user %s does not exist; logrotate will fail", name)
			}
		}
		for _, path := range entry.Paths {
			dir := filepath.Dir(path)
			if home := inOrphanHome(dir); home != "" {
				add("logrotate_in_orphan_home", entry.File, entry.Line, "%s is in orphaned home %s", path, home)
			} else if matches, _ := filepath.Glob(dir); len(matches) == 0 {
				add("logrotate_path_missing", entry.File, entry.Line, "directory of %s does not exist", path)
			}
		}
	}

	for _, unit := range inv.Units {
		if unit.User != "" && inv.All[unit.User] == nil {
			add("unit_user_missing", unit.File, 0, "systemd unit %s runs as missing user %s", unit.Name, unit.User)
		}
		for _, path := range unit.Paths {
			if home := inOrphanHome(path); home != "" {
				add("unit_in_orphan_home", unit.File, 0, "systemd unit %s uses %s in orphaned home %s", unit.Name, path, home)
			}
		}
	}

	for _, ref := range inv.Refs {
		if ref.Kind != "sudoers" && ref.Kind != "cron" {
			continue
		}
		for _, name := range ref.Users {
			if inv.All[name] == nil {
				add("ref_user_missing", ref.File, ref.Line, "%s entry for missing user %s (a new user with that name would inherit it): %s",
					ref.Kind, name, ref.Text)
			}
		}
	}

	for _, acct := range inv.MySQL {
		if acct.Error != "" {
			add("mysql_unreachable", acct.File, 0, "MySQL login of %s (%s) failed: %s", acct.Owner, acct.Label(), acct.Error)
		}
		if info, err := os.Stat(acct.File); err == nil && info.Mode().Perm()&0004 != 0 {
			add("mysql_cnf_readable", acct.File, 0, "readable by everyone, it holds the MySQL password of %s", acct.Owner)
		}
	}

	entries, _ = os.ReadDir(lingerDir)
	for _, entry := range entries {
		if inv.All[entry.Name()] == nil {
			add("linger_user_missing", filepath.Join(lingerDir, entry.Name()), 0, "lingering enabled for missing user %s", entry.Name())
		}
	}
}
