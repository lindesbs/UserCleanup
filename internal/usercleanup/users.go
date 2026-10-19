package usercleanup

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// User represents a user on the system
type User struct {
	Username     string   `json:"username"`
	UID          int      `json:"uid"`
	GID          int      `json:"gid"`
	HomeDir      string   `json:"home_dir"`
	Shell        string   `json:"shell"`
	HasLogin     bool     `json:"has_login"`
	Configs      []string `json:"configs"`
	IsSystemUser bool     `json:"system_user"`
	// Extra are directories outside the home to include in the backup
	Extra []string `json:"-"`
	// MySQL are the logins of ~/.my.cnf whose databases are dumped
	MySQL []*MySQLAccount `json:"-"`
}

// protectedUsers can never be the target of generated delete commands,
// regardless of their UID.
var protectedUsers = map[string]bool{
	"root": true, "nobody": true, "www-data": true, "caddy": true,
	"nginx": true, "apache": true, "mysql": true, "mariadb": true,
	"postgres": true, "redis": true, "memcache": true, "postfix": true,
	"dovecot": true, "bind": true, "sshd": true, "systemd-network": true,
	"systemd-resolve": true, "systemd-timesync": true, "messagebus": true,
	"_apt": true, "daemon": true, "bin": true, "sys": true, "sync": true,
}

// nobodyUID is the UID of the "nobody" user on Debian
const nobodyUID = 65534

// readPasswdFile reads the passwd file and returns a map of users.
// If includeSystem is false, users with UID < 1000 and nobody are skipped.
func readPasswdFile(includeSystem bool) (map[string]*User, error) {
	file, err := os.Open(passwdFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	users := make(map[string]*User)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), ":")
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

		isSystemUser := uid < 1000 || uid == nobodyUID
		if isSystemUser && !includeSystem {
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

		// A user without a login shell can still own a running website
		// (typical for SFTP/web users), so this is only informational.
		user.HasLogin = user.Shell != "" &&
			!strings.HasSuffix(user.Shell, "nologin") &&
			!strings.HasSuffix(user.Shell, "false")

		users[user.Username] = user
	}

	return users, scanner.Err()
}

// sortedUsers returns the users sorted by username
func sortedUsers(users map[string]*User) []*User {
	list := make([]*User, 0, len(users))
	for _, user := range users {
		list = append(list, user)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Username < list[j].Username })
	return list
}

// isProtected reports why a user must not be deleted, or "" if it may be
func isProtected(user *User) string {
	switch {
	case protectedUsers[user.Username]:
		return fmt.Sprintf("%s is on the list of protected service users", user.Username)
	case user.UID < 1000 || user.UID == nobodyUID:
		return fmt.Sprintf("%s is a system user (UID %d)", user.Username, user.UID)
	}
	return ""
}

// homeIsRemovable reports whether the user's home directory may be removed
// by a generated command. It must be exactly <homeDir>/<username> and must
// not be used as or contain the home of any other user.
func homeIsRemovable(user *User, all map[string]*User) (bool, string) {
	home := filepath.Clean(user.HomeDir)
	if home != filepath.Join(homeDir, user.Username) {
		return false, fmt.Sprintf("home %s is not %s", user.HomeDir, filepath.Join(homeDir, user.Username))
	}
	for _, other := range all {
		if other.Username == user.Username {
			continue
		}
		otherHome := filepath.Clean(other.HomeDir)
		if otherHome == home || strings.HasPrefix(otherHome, home+"/") {
			return false, fmt.Sprintf("home %s is also used by %s", user.HomeDir, other.Username)
		}
	}
	return true, ""
}

// accountLines returns all lines for the user from the account databases
// (passwd, shadow, group, gshadow, subuid, subgid), keyed by file path.
// Group lines are included if the user is the group name or a member.
func accountLines(username string) map[string][]string {
	result := make(map[string][]string)
	for _, path := range accountFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Split(line, ":")
			if len(parts) < 2 {
				continue
			}
			match := parts[0] == username
			if !match && (strings.HasSuffix(path, "group") || strings.HasSuffix(path, "gshadow")) {
				members := parts[len(parts)-1]
				for _, m := range strings.Split(members, ",") {
					if m == username {
						match = true
					}
				}
			}
			if match {
				result[path] = append(result[path], line)
			}
		}
	}
	return result
}
