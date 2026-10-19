package usercleanup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// Paths and settings; variables so tests can point them elsewhere
var (
	homeDir      = "/home"
	etcDir       = "/etc"
	cronDir      = "/var/spool/cron/crontabs"
	mailDir      = "/var/mail"
	lingerDir    = "/var/lib/systemd/linger"
	passwdFile   = "/etc/passwd"
	accountFiles = []string{
		"/etc/passwd", "/etc/shadow", "/etc/group",
		"/etc/gshadow", "/etc/subuid", "/etc/subgid",
	}
	backupDir   = "/var/backups/usercleanup"
	requireRoot = true
	dryRun      bool
	systemUsers bool
	jsonOutput  bool
	listOnly    []string
	planOutput  string
	planBackup  string
)

// lines returns "file:line-end" for display
func lines(file string, line, endLine int) string {
	return fmt.Sprintf("%s:%d-%d", file, line, endLine)
}

// printUserInfo prints the areas of a user that the filter selects; data
// is the unfiltered data of the user
func printUserInfo(w io.Writer, user *User, all UserData, f listFilter) {
	data := f.apply(all)
	userType := "User"
	if user.IsSystemUser {
		userType = "System User"
	}
	bold := color.New(color.Bold)

	if !f.has("users") {
		bold.Fprintf(w, "Username: %s\n", user.Username)
	} else {
		bold.Fprintf(w, "Username: %s (%s)\n", user.Username, userType)
		fmt.Fprintf(w, "UID/GID: %d/%d\n", user.UID, user.GID)
		fmt.Fprintf(w, "Home Directory: %s", user.HomeDir)
		if !exists(user.HomeDir) {
			color.New(color.FgYellow).Fprint(w, " (missing)")
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Shell: %s\n", user.Shell)
		fmt.Fprintf(w, "Login: ")
		if user.HasLogin {
			color.New(color.FgGreen).Fprintln(w, "yes")
		} else {
			color.New(color.FgYellow).Fprintln(w, "no")
		}
		if reason := isProtected(user); reason != "" {
			color.New(color.FgRed).Fprintf(w, "Protected: %s\n", reason)
		}
	}

	if len(data.Sites) > 0 {
		fmt.Fprintln(w, "Sites:")
		for _, site := range data.Sites {
			state := "enabled"
			if !site.Enabled {
				state = "disabled"
			}
			fmt.Fprintf(w, "  - %s (%s)\n", siteLabel(site), state)
			if site.Root != "" {
				fmt.Fprintf(w, "      root: %s\n", site.Root)
			}
			for _, backend := range site.Backends {
				fmt.Fprintf(w, "      php: %s\n", backend)
			}
			if len(site.Owners) > 1 {
				fmt.Fprintf(w, "      shared with: %s\n", strings.Join(site.Owners, ", "))
			}
			fmt.Fprintf(w, "      file: %s\n", lines(site.File, site.Line, site.EndLine))
		}
	}
	if len(data.Pools) > 0 {
		fmt.Fprintln(w, "PHP-FPM pools:")
		for _, pool := range data.Pools {
			fmt.Fprintf(w, "  - [%s] PHP %s, user %s, listen %s\n", pool.Name, pool.Version, pool.User, pool.Listen)
			keys := make([]string, 0, len(pool.Settings))
			for key := range pool.Settings {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				fmt.Fprintf(w, "      %s = %s\n", key, pool.Settings[key])
			}
			for _, site := range pool.UsedBy {
				color.New(color.FgYellow).Fprintf(w, "      also used by: %s\n", site)
			}
			fmt.Fprintf(w, "      file: %s\n", lines(pool.File, pool.Line, pool.EndLine))
		}
	}
	if len(data.Units) > 0 {
		for _, unit := range data.Units {
			if unit.Enabled {
				color.New(color.FgYellow).Fprintln(w, "Runs enabled services: check before removing this user")
				break
			}
		}
		fmt.Fprintln(w, "systemd units:")
		for _, unit := range data.Units {
			state := "disabled"
			if unit.Enabled {
				state = "enabled"
			}
			fmt.Fprintf(w, "  - %s (%s)\n      file: %s\n", unit.Name, state, unit.File)
			if unit.User != user.Username {
				runsAs := unit.User
				if runsAs == "" {
					runsAs = "root"
				}
				fmt.Fprintf(w, "      runs as %s but uses: %s\n", runsAs, strings.Join(unit.HomePaths(user), ", "))
			}
			if unit.Timer != "" {
				fmt.Fprintf(w, "      timer: %s\n", unit.Timer)
			}
		}
	}
	if crontab := filepath.Join(cronDir, user.Username); f.crontab() && exists(crontab) {
		fmt.Fprintf(w, "Crontab: %s\n", crontab)
	}
	if len(data.UserUnits) > 0 || data.Linger {
		fmt.Fprintf(w, "systemd user services: %d", len(data.UserUnits))
		if data.Linger {
			fmt.Fprint(w, " (linger enabled: they run without login)")
		}
		fmt.Fprintln(w)
		for _, unit := range data.UserUnits {
			fmt.Fprintf(w, "  - %s\n", unit)
		}
	}
	printMySQL(w, data.MySQL)
	if len(data.Refs) > 0 {
		fmt.Fprintln(w, "Entries in /etc:")
		for _, ref := range data.Refs {
			fmt.Fprintf(w, "  - %s %s:%d\n      %s\n", ref.Kind, ref.File, ref.Line, ref.Text)
		}
	}
	if len(data.Dirs) > 0 {
		fmt.Fprintln(w, "Directories outside the home:")
		for _, dir := range data.Dirs {
			fmt.Fprintf(w, "  - %s\n", dir)
		}
	}
	if len(data.Logrotate) > 0 {
		fmt.Fprintln(w, "Logrotate:")
		for _, entry := range data.Logrotate {
			fmt.Fprintf(w, "  - %s\n", strings.Join(entry.Paths, " "))
			fmt.Fprintf(w, "      file: %s\n", lines(entry.File, entry.Line, entry.EndLine))
		}
	}

	if others := otherConfigs(user, all); f.has("files") && len(others) > 0 {
		fmt.Fprintln(w, "Other files in /etc that mention the user:")
		for _, config := range others {
			fmt.Fprintf(w, "  - %s\n", config)
		}
	}
	fmt.Fprintln(w)
}

// printMySQL prints the databases and tables of the user's MySQL logins
func printMySQL(w io.Writer, accounts []*MySQLAccount) {
	if len(accounts) == 0 {
		return
	}
	fmt.Fprintln(w, "MySQL:")
	for _, acct := range accounts {
		fmt.Fprintf(w, "  - %s: %s", acct.File, acct.Label())
		if acct.Server != "" {
			fmt.Fprintf(w, " (server %s)", acct.Server)
		}
		fmt.Fprintln(w)
		if acct.Database != "" {
			fmt.Fprintf(w, "      default database: %s\n", acct.Database)
		}
		if acct.Error != "" {
			color.New(color.FgYellow).Fprintf(w, "      not checked: %s\n", acct.Error)
			continue
		}
		if acct.Global {
			color.New(color.FgYellow).Fprintln(w, "      account has privileges on all databases; they are not assigned to this user")
		}
		if len(acct.Databases) == 0 {
			fmt.Fprintln(w, "      no databases")
		}
		for _, db := range acct.Databases {
			fmt.Fprintf(w, "      database %s: %d tables, %s, ~%d rows\n", db.Name, len(db.Tables), formatBytes(db.Size()), db.Rows())
			if len(db.SharedWith) > 0 {
				color.New(color.FgYellow).Fprintf(w, "        also used by: %s\n", strings.Join(db.SharedWith, ", "))
			}
			for _, table := range db.Tables {
				fmt.Fprintf(w, "        - %s (%s, ~%d rows, %s)\n", table.Name, table.Engine, table.Rows, formatBytes(table.Size))
			}
		}
	}
}

// printUnownedSites prints sites that could not be assigned to a user
func printUnownedSites(w io.Writer, sites []*Site) {
	if len(sites) == 0 {
		return
	}
	color.New(color.Bold).Fprintf(w, "Sites without a known owner (%d):\n", len(sites))
	for _, site := range sites {
		state := "enabled"
		if !site.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(w, "  - %s (%s)\n", siteLabel(site), state)
		if site.Root != "" {
			fmt.Fprintf(w, "      root: %s\n", site.Root)
		}
		fmt.Fprintf(w, "      file: %s\n", lines(site.File, site.Line, site.EndLine))
	}
	fmt.Fprintln(w)
}

// printFindings prints orphaned and broken entries
func printFindings(w io.Writer, findings []Finding) {
	if len(findings) == 0 {
		color.New(color.FgGreen).Fprintln(w, "No orphaned or broken entries found")
		return
	}
	color.New(color.FgRed, color.Bold).Fprintf(w, "Orphaned or broken entries (%d):\n", len(findings))
	for _, f := range findings {
		location := f.Path
		if f.Line > 0 {
			location = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		fmt.Fprintf(w, "  - [%s] %s\n      %s\n", f.Kind, location, f.Detail)
	}
}

// loadUser reads all users, the inventory and the user's configs and
// returns the named user. All users are loaded so that shared homes and
// shared config files can be detected.
func loadUser(username string) (*User, *Inventory) {
	users, err := readPasswdFile(true)
	if err != nil {
		fail("Error reading passwd file: %v", err)
	}
	user, found := users[username]
	if !found {
		fail("User %s not found", username)
	}
	if err := findUserConfigs(map[string]*User{username: user}); err != nil {
		fmt.Fprintf(os.Stderr, "Error finding user configurations: %v\n", err)
	}
	inv := collectInventory(users)
	prepareUser(user, inv)
	return user, inv
}

// prepareUser adds what the inventory found to the user's backup sources.
// Parsed files belong into the backup even if the text search missed them.
func prepareUser(user *User, inv *Inventory) {
	data := inv.For(user.Username)
	for _, file := range data.Files() {
		user.Configs = appendUnique(user.Configs, file)
	}
	user.Extra = data.Dirs
	user.MySQL = data.MySQL
	sort.Strings(user.Configs)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// appVersion is the version passed to Execute, shown by the TUI
var appVersion = "dev"

// Execute runs the command line interface; version is shown by --version
func Execute(version string) {
	appVersion = version
	var rootCmd = &cobra.Command{
		Use:   "usercleanup",
		Short: "UserCleanup is a tool to manage and clean up user accounts",
		Long: `UserCleanup helps system administrators identify and manage user accounts.
It shows users with their Apache, nginx and Caddy sites, PHP-FPM pools,
logrotate entries and MySQL databases (from ~/.my.cnf), finds orphaned entries, creates backups, and generates
the shell commands to remove a user. It never deletes anything itself: the
administrator reviews and runs the generated commands.`,
		Version: version,
	}

	var listCmd = &cobra.Command{
		Use:   "list [username]",
		Short: "List users with their sites, pools and logrotate entries, and orphaned entries",
		Long: `Lists users with everything that belongs to them, sites without a known
owner and orphaned or broken entries. --only limits the output to some
areas, e.g. "--only users" for the accounts or "--only apache,php" for the
Apache sites and PHP-FPM pools. Users with nothing in the selected areas
are left out unless a username is given. Areas:
` + areaHelp(),
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			filter, err := parseListFilter(listOnly)
			if err != nil {
				fail("%v", err)
			}
			all, err := readPasswdFile(true)
			if err != nil {
				fail("Error reading passwd file: %v", err)
			}
			skipMySQL = !filter.has("mysql") && !filter.has("findings")
			inv := collectInventory(all)

			users := make(map[string]*User)
			for name, user := range all {
				if len(args) == 1 && name != args[0] {
					continue
				}
				if len(args) == 0 && user.IsSystemUser && !systemUsers {
					continue
				}
				users[name] = user
			}
			if len(args) == 1 && len(users) == 0 {
				fail("User %s not found", args[0])
			}
			if filter.needsConfigs() {
				if err := findUserConfigs(users); err != nil {
					fmt.Fprintf(os.Stderr, "Error finding user configurations: %v\n", err)
				}
			}

			var shown []*User
			for _, user := range sortedUsers(users) {
				if len(args) == 1 || filter.shows(user, inv.For(user.Username)) {
					shown = append(shown, user)
				}
			}
			// Sites without an owner and findings only belong to the overview
			unowned := []*Site{}
			showUnowned := len(args) == 0 && (filter.has("unowned") || filter.siteAreas())
			if showUnowned {
				unowned = append(unowned, filter.sites(inv.UnownedSites())...)
			}
			showFindings := len(args) == 0 && filter.has("findings")

			if jsonOutput {
				type userJSON struct {
					*User
					UserData
				}
				out := struct {
					Users        []userJSON `json:"users"`
					UnownedSites *[]*Site   `json:"unowned_sites,omitempty"`
					Findings     *[]Finding `json:"findings,omitempty"`
				}{Users: []userJSON{}}
				if showUnowned {
					out.UnownedSites = &unowned
				}
				if showFindings {
					findings := append([]Finding{}, inv.Findings...)
					out.Findings = &findings
				}
				for _, user := range shown {
					out.Users = append(out.Users, userJSON{user, filter.apply(inv.For(user.Username))})
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(out); err != nil {
					fail("Error writing JSON: %v", err)
				}
				return
			}

			if filter.has("users") {
				fmt.Printf("Found %d users\n\n", len(shown))
			}
			for _, user := range shown {
				printUserInfo(os.Stdout, user, inv.For(user.Username), filter)
			}
			if showUnowned {
				printUnownedSites(os.Stdout, unowned)
			}
			if showFindings {
				printFindings(os.Stdout, inv.Findings)
			}
		},
	}

	var backupCmd = &cobra.Command{
		Use:   "backup <username>",
		Short: "Back up a user's home, configurations, crontab and mail",
		Long: `Writes <username>-<time>.tar.gz with owners and permissions preserved,
plus a .manifest.json (including the account entries) and a .sha256 file,
into the backup directory. The directory is created with mode 0700.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			user, inv := loadUser(args[0])
			archive, err := backupUser(user, inv.All, backupDir)
			if err != nil {
				fail("Error backing up user: %v", err)
			}
			if !dryRun {
				fmt.Printf("Backup written to %s\n", archive)
			}
		},
	}

	var planCmd = &cobra.Command{
		Use:     "plan <username>",
		Aliases: []string{"delete"},
		Short:   "Print the shell commands to remove a user (nothing is deleted)",
		Long: `Prints a shell script with the commands to remove a user. Nothing is
executed: review the commands and run them yourself. A backup of the user
must exist (see "backup"); the newest one in the backup directory is used
unless --backup is given. System and service users are refused.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			user, inv := loadUser(args[0])
			if reason := isProtected(user); reason != "" {
				fail("Refusing to generate delete commands: %s", reason)
			}

			script, _, err := planForUser(user, inv, planBackup)
			if err != nil {
				fail("%v", err)
			}

			if planOutput == "" {
				fmt.Print(script)
				return
			}
			if err := os.WriteFile(planOutput, []byte(script), 0600); err != nil {
				fail("Error writing %s: %v", planOutput, err)
			}
			fmt.Printf("Commands written to %s. Review them, then run: sudo sh %s\n", planOutput, planOutput)
		},
	}

	rootCmd.PersistentFlags().BoolVarP(&systemUsers, "system-users", "s", false, "Include system users (UID < 1000) in list")
	rootCmd.PersistentFlags().StringVarP(&backupDir, "backup-dir", "b", backupDir, "Absolute backup directory, outside of any home")
	listCmd.Flags().BoolVar(&jsonOutput, "json", false, "Print the result as JSON")
	listCmd.Flags().StringSliceVar(&listOnly, "only", nil, "Show only these areas, comma separated (see list --help)")
	listCmd.RegisterFlagCompletionFunc("only", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return sortedAreas(), cobra.ShellCompDirectiveNoFileComp
	})
	backupCmd.Flags().BoolVarP(&dryRun, "dry-run", "d", false, "Show what would be backed up without writing anything")
	planCmd.Flags().StringVarP(&planOutput, "output", "o", "", "Write the commands to this file instead of stdout")
	planCmd.Flags().StringVar(&planBackup, "backup", "", "Backup archive to reference (default: newest of the user)")

	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(&cobra.Command{
		Use:     "tui",
		Aliases: []string{"ui"},
		Short:   "Browse users interactively, create backups and generate the commands",
		Long: `Starts an interactive terminal interface: all users with their sites,
pools, databases, units and /etc entries, the findings, backups (b) and the
generated commands (p), which can be written to a file (w). Like the other
commands it never deletes anything.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runTUI(); err != nil {
				fail("%v", err)
			}
		},
	})

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
