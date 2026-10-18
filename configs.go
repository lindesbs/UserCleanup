package main

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// maxConfigSize is the largest file in /etc that is searched for user references
const maxConfigSize = 2 << 20

// nameChars are the characters that may be part of a user name or path
// segment; a match must not be directly surrounded by one of them.
const nameChars = `A-Za-z0-9_.-`

// userMatcher matches references to a user in a file's content
type userMatcher struct {
	user *User
	re   *regexp.Regexp
}

func newUserMatcher(user *User) *userMatcher {
	// The user name must stand alone, e.g. "web" must not match "webmaster".
	pattern := `(^|[^` + nameChars + `])` + regexp.QuoteMeta(user.Username) + `($|[^` + nameChars + `])`
	// The home path must end at a path boundary, e.g. /home/foo must not
	// match /home/foobar. "/" or an empty home would match everything.
	if home := filepath.Clean(user.HomeDir); home != "/" && home != "." {
		pattern += `|` + regexp.QuoteMeta(home) + `($|[^` + nameChars + `])`
	}
	return &userMatcher{user: user, re: regexp.MustCompile(pattern)}
}

// isAccountFile reports whether path is one of the account databases,
// which are handled by userdel and saved separately in the manifest.
func isAccountFile(path string) bool {
	for _, f := range accountFiles {
		if path == f || path == f+"-" {
			return true
		}
	}
	return false
}

// readTextFile returns the file content, or nil if it is too large,
// not a regular file, or looks binary.
func readTextFile(path string) []byte {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxConfigSize {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil || bytes.IndexByte(content, 0) >= 0 {
		return nil
	}
	return content
}

// findUserConfigs finds configuration files in /etc that reference a user
// by name or home directory. This is a text search, so results are hints
// and must be reviewed before anything is removed.
func findUserConfigs(users map[string]*User) error {
	matchers := make([]*userMatcher, 0, len(users))
	for _, user := range sortedUsers(users) {
		user.Configs = nil
		matchers = append(matchers, newUserMatcher(user))
	}

	err := filepath.WalkDir(etcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || isAccountFile(path) {
			return nil // Skip unreadable entries and account databases
		}

		content := readTextFile(path)
		if content == nil {
			return nil
		}

		for _, m := range matchers {
			if m.re.Match(content) {
				m.user.Configs = append(m.user.Configs, path)
			}
		}
		return nil
	})

	for _, m := range matchers {
		sort.Strings(m.user.Configs)
	}
	return err
}
