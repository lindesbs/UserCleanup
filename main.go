package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// User represents a user on the system
type User struct {
	Username     string
	UID          int
	GID          int
	HomeDir      string
	Shell        string
	Exists       bool
	Configs      []string
	IsSystemUser bool
}

// Global variables
var (
	users       map[string]*User
	homeDir     = "/home"
	etcDir      = "/etc"
	backupDir   = "./backups"
	dryRun      bool
	systemUsers bool
)

// readPasswdFile reads the /etc/passwd file and returns a map of users
func readPasswdFile() (map[string]*User, error) {
	file, err := os.Open("/etc/passwd")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	users := make(map[string]*User)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ":")
		if len(parts) < 7 {
			continue
		}

		uid, err := strconv.Atoi(parts[2])
		if err != nil {
			continue
		}

		gid, err := strconv.Atoi(parts[3])
		if err != nil {
			continue
		}

		// Skip system users (UID < 1000) unless explicitly requested
		isSystemUser := uid < 1000
		if isSystemUser && !systemUsers {
			continue
		}

		user := &User{
			Username:     parts[0],
			UID:          uid,
			GID:          gid,
			HomeDir:      parts[5],
			Shell:        parts[6],
			IsSystemUser: isSystemUser,
		}

		// Check if user exists (has a valid login shell)
		user.Exists = !strings.HasSuffix(user.Shell, "nologin") &&
			!strings.HasSuffix(user.Shell, "false") &&
			user.Shell != ""

		users[parts[0]] = user
	}

	return users, scanner.Err()
}

// findHomeDirectories checks if users have home directories
func findHomeDirectories(users map[string]*User) error {
	entries, err := os.ReadDir(homeDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		username := entry.Name()
		if user, exists := users[username]; exists {
			// Verify the home directory path
			if user.HomeDir != filepath.Join(homeDir, username) {
				user.HomeDir = filepath.Join(homeDir, username)
			}
		}
	}

	return nil
}

// findUserConfigs finds configuration files in /etc for each user
func findUserConfigs(users map[string]*User) error {
	return filepath.WalkDir(etcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if d.IsDir() {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return nil // Skip files we can't read
		}

		contentStr := string(content)
		for username, user := range users {
			// Check if username or home directory is mentioned in the file
			if strings.Contains(contentStr, username) ||
				strings.Contains(contentStr, user.HomeDir) {
				user.Configs = append(user.Configs, path)
			}
		}

		return nil
	})
}

// backupUser backs up a user's home directory and configurations
func backupUser(user *User) error {
	if dryRun {
		fmt.Printf("Would backup user %s to %s\n", user.Username, backupDir)
		return nil
	}

	// Create backup directory
	userBackupDir := filepath.Join(backupDir, user.Username)
	if err := os.MkdirAll(userBackupDir, 0755); err != nil {
		return err
	}

	// Backup home directory if it exists
	if _, err := os.Stat(user.HomeDir); err == nil {
		homeBackupDir := filepath.Join(userBackupDir, "home")
		if err := os.MkdirAll(homeBackupDir, 0755); err != nil {
			return err
		}

		cmd := exec.Command("cp", "-r", user.HomeDir, homeBackupDir)
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	// Backup configuration files
	if len(user.Configs) > 0 {
		configBackupDir := filepath.Join(userBackupDir, "configs")
		if err := os.MkdirAll(configBackupDir, 0755); err != nil {
			return err
		}

		for _, config := range user.Configs {
			destPath := filepath.Join(configBackupDir, strings.ReplaceAll(config, "/", "_"))
			if err := copyFile(config, destPath); err != nil {
				fmt.Printf("Warning: Failed to backup config %s: %v\n", config, err)
			}
		}
	}

	return nil
}

// deleteUser deletes a user and their files
func deleteUser(user *User) error {
	if dryRun {
		fmt.Printf("Would delete user %s\n", user.Username)
		return nil
	}

	// Delete the user
	cmd := exec.Command("userdel", "-r", user.Username)
	if err := cmd.Run(); err != nil {
		return err
	}

	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, input, 0644)
}

// printUserInfo prints information about a user
func printUserInfo(user *User) {
	statusColor := color.New(color.FgRed)
	if user.Exists {
		statusColor = color.New(color.FgGreen)
	}

	userType := "User"
	if user.IsSystemUser {
		userType = "System User"
	}

	fmt.Printf("Username: %s (%s)\n", user.Username, userType)
	fmt.Printf("UID/GID: %d/%d\n", user.UID, user.GID)
	fmt.Printf("Home Directory: %s\n", user.HomeDir)
	fmt.Printf("Shell: %s\n", user.Shell)
	fmt.Printf("Status: ")
	statusColor.Printf("%s\n", map[bool]string{true: "Active", false: "Inactive"}[user.Exists])

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

func main() {
	var rootCmd = &cobra.Command{
		Use:   "usercleanup",
		Short: "UserCleanup is a tool to manage and clean up user accounts",
		Long: `UserCleanup helps system administrators identify and manage user accounts.
It can show which users exist, which are inactive, and what configurations
are associated with each user. It also provides functionality to backup
and delete users.`,
		Version: version,
	}

	var listCmd = &cobra.Command{
		Use:   "list",
		Short: "List all users",
		Run: func(cmd *cobra.Command, args []string) {
			var err error
			users, err = readPasswdFile()
			if err != nil {
				fmt.Printf("Error reading passwd file: %v\n", err)
				os.Exit(1)
			}

			err = findHomeDirectories(users)
			if err != nil {
				fmt.Printf("Error finding home directories: %v\n", err)
			}

			err = findUserConfigs(users)
			if err != nil {
				fmt.Printf("Error finding user configurations: %v\n", err)
			}

			fmt.Printf("Found %d users\n\n", len(users))
			for _, user := range users {
				printUserInfo(user)
			}
		},
	}

	var backupCmd = &cobra.Command{
		Use:   "backup [username]",
		Short: "Backup a user's files",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			username := args[0]

			var err error
			users, err = readPasswdFile()
			if err != nil {
				fmt.Printf("Error reading passwd file: %v\n", err)
				os.Exit(1)
			}

			user, exists := users[username]
			if !exists {
				fmt.Printf("User %s not found\n", username)
				os.Exit(1)
			}

			err = findUserConfigs(users)
			if err != nil {
				fmt.Printf("Error finding user configurations: %v\n", err)
			}

			fmt.Printf("Backing up user %s...\n", username)
			if err := backupUser(user); err != nil {
				fmt.Printf("Error backing up user: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Backup completed successfully")
		},
	}

	var deleteCmd = &cobra.Command{
		Use:   "delete [username]",
		Short: "Delete a user and their files",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			username := args[0]

			var err error
			users, err = readPasswdFile()
			if err != nil {
				fmt.Printf("Error reading passwd file: %v\n", err)
				os.Exit(1)
			}

			user, exists := users[username]
			if !exists {
				fmt.Printf("User %s not found\n", username)
				os.Exit(1)
			}

			// Backup before delete
			fmt.Printf("Backing up user %s before deletion...\n", username)
			if err := backupUser(user); err != nil {
				fmt.Printf("Error backing up user: %v\n", err)
				if !dryRun {
					fmt.Println("Aborting deletion due to backup failure")
					os.Exit(1)
				}
			}

			fmt.Printf("Deleting user %s...\n", username)
			if err := deleteUser(user); err != nil {
				fmt.Printf("Error deleting user: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("User deleted successfully")
		},
	}

	// Add flags
	rootCmd.PersistentFlags().BoolVarP(&dryRun, "dry-run", "d", false, "Show what would be done without actually doing it")
	rootCmd.PersistentFlags().BoolVarP(&systemUsers, "system-users", "s", false, "Include system users (UID < 1000)")

	// Add commands
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(deleteCmd)

	// Execute
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
