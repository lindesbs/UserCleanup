package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// Paths and settings; variables so tests can point them elsewhere
var (
	homeDir      = "/home"
	etcDir       = "/etc"
	cronDir      = "/var/spool/cron/crontabs"
	mailDir      = "/var/mail"
	sbinDir      = "/usr/sbin"
	passwdFile   = "/etc/passwd"
	accountFiles = []string{
		"/etc/passwd", "/etc/shadow", "/etc/group",
		"/etc/gshadow", "/etc/subuid", "/etc/subgid",
	}
	backupDir   = "/var/backups/usercleanup"
	requireRoot = true
	dryRun      bool
	systemUsers bool
	planOutput  string
	planBackup  string
)

// printUserInfo prints information about a user
func printUserInfo(user *User) {
	userType := "User"
	if user.IsSystemUser {
		userType = "System User"
	}

	fmt.Printf("Username: %s (%s)\n", user.Username, userType)
	fmt.Printf("UID/GID: %d/%d\n", user.UID, user.GID)
	fmt.Printf("Home Directory: %s\n", user.HomeDir)
	fmt.Printf("Shell: %s\n", user.Shell)
	fmt.Printf("Login: ")
	if user.HasLogin {
		color.New(color.FgGreen).Println("yes")
	} else {
		color.New(color.FgYellow).Println("no")
	}
	if reason := isProtected(user); reason != "" {
		color.New(color.FgRed).Printf("Protected: %s\n", reason)
	}

	if len(user.Configs) > 0 {
		fmt.Println("Configuration files:")
		for _, config := range user.Configs {
			fmt.Printf("  - %s\n", config)
		}
	} else {
		fmt.Println("No configuration files found")
	}
	fmt.Println()
}

// loadUser reads all users and their configs and returns the named user.
// All users are loaded so that shared homes can be detected.
func loadUser(username string) (*User, map[string]*User) {
	users, err := readPasswdFile(true)
	if err != nil {
		fail("Error reading passwd file: %v", err)
	}
	user, exists := users[username]
	if !exists {
		fail("User %s not found", username)
	}
	if err := findUserConfigs(map[string]*User{username: user}); err != nil {
		fmt.Fprintf(os.Stderr, "Error finding user configurations: %v\n", err)
	}
	return user, users
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "usercleanup",
		Short: "UserCleanup is a tool to manage and clean up user accounts",
		Long: `UserCleanup helps system administrators identify and manage user accounts.
It shows users and the configurations associated with them, creates backups,
and generates the shell commands to remove a user. It never deletes anything
itself: the administrator reviews and runs the generated commands.`,
	}

	var listCmd = &cobra.Command{
		Use:   "list",
		Short: "List all users",
		Run: func(cmd *cobra.Command, args []string) {
			users, err := readPasswdFile(systemUsers)
			if err != nil {
				fail("Error reading passwd file: %v", err)
			}

			if err := findUserConfigs(users); err != nil {
				fmt.Fprintf(os.Stderr, "Error finding user configurations: %v\n", err)
			}

			fmt.Printf("Found %d users\n\n", len(users))
			for _, user := range sortedUsers(users) {
				printUserInfo(user)
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
			user, users := loadUser(args[0])
			archive, err := backupUser(user, users, backupDir)
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
			user, users := loadUser(args[0])
			if reason := isProtected(user); reason != "" {
				fail("Refusing to generate delete commands: %s", reason)
			}

			archive := planBackup
			if archive != "" {
				var err error
				if archive, err = filepath.Abs(archive); err != nil {
					fail("Error resolving %s: %v", planBackup, err)
				}
			} else {
				var err error
				if archive, err = latestBackup(user.Username, backupDir); err != nil {
					fail("Error looking for backups: %v", err)
				}
				if archive == "" {
					fail("No backup of %s found in %s. Run \"usercleanup backup %s\" first.",
						user.Username, backupDir, user.Username)
				}
			}
			manifest, err := readManifest(archive)
			if err != nil {
				fail("Error reading manifest of %s: %v", archive, err)
			}

			script, err := buildDeletePlan(user, users, archive, manifest)
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
			fmt.Printf("Commands written to %s. Review them, then run: sh %s\n", planOutput, planOutput)
		},
	}

	rootCmd.PersistentFlags().BoolVarP(&systemUsers, "system-users", "s", false, "Include system users (UID < 1000) in list")
	rootCmd.PersistentFlags().StringVarP(&backupDir, "backup-dir", "b", backupDir, "Absolute backup directory, outside of any home")
	backupCmd.Flags().BoolVarP(&dryRun, "dry-run", "d", false, "Show what would be backed up without writing anything")
	planCmd.Flags().StringVarP(&planOutput, "output", "o", "", "Write the commands to this file instead of stdout")
	planCmd.Flags().StringVar(&planBackup, "backup", "", "Backup archive to reference (default: newest of the user)")

	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(planCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
