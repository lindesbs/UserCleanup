package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Reference is a line in /etc that names a user: a sudoers rule, a system
// cron job, an sshd AllowUsers/DenyUsers/Match User, a limits entry or a
// mail alias
type Reference struct {
	Kind   string   `json:"kind"`
	File   string   `json:"file"`
	Line   int      `json:"line"`
	Text   string   `json:"text"`
	Users  []string `json:"users"` // all user names in the line, known or not
	Owners []string `json:"owners,omitempty"`
}

// refFile is a parsed file: its references and whether the file may be
// removed as a whole once its only user is gone (only drop-in directories,
// never the main files)
type refFile struct {
	refs      []*Reference
	other     bool // has significant lines that name no user
	removable bool
}

// cronSpecial are the @-schedules of cron that replace the five time fields
var cronSpecial = map[string]bool{
	"@reboot": true, "@yearly": true, "@annually": true, "@monthly": true,
	"@weekly": true, "@daily": true, "@midnight": true, "@hourly": true,
}

// userName matches what can be a login name (no %group, no ALIAS, no ALL)
var userName = regexp.MustCompile(`^[a-z_][a-z0-9_.-]*\$?$`)

// dropIns returns the files of a drop-in directory, in the order the
// programs read them; names with a dot or ending in ~ are skipped like
// sudo and cron do, unless suffix is set (then only *suffix files)
func dropIns(dir, suffix string) []string {
	entries, _ := os.ReadDir(dir)
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		if suffix != "" {
			if !strings.HasSuffix(name, suffix) {
				continue
			}
		} else if strings.Contains(name, ".") || strings.HasSuffix(name, "~") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)
	return files
}

// logicalLines returns the lines of a file without comments and empty
// lines, joining lines that end in a backslash. keepComment decides which
// lines starting with # are still significant (e.g. sudo's #include).
func logicalLines(file string, keepComment func(string) bool) (lines []string, numbers []int) {
	f, err := os.Open(file)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var cur strings.Builder
	start := 0
	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		text := strings.TrimSpace(scanner.Text())
		if cur.Len() == 0 {
			if text == "" || (strings.HasPrefix(text, "#") && (keepComment == nil || !keepComment(text))) {
				continue
			}
			start = lineNo
		}
		if strings.HasSuffix(text, "\\") {
			cur.WriteString(strings.TrimSuffix(text, "\\") + " ")
			continue
		}
		cur.WriteString(text)
		lines = append(lines, cur.String())
		numbers = append(numbers, start)
		cur.Reset()
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
		numbers = append(numbers, start)
	}
	return lines, numbers
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

// parseSudoers reads user specifications, User_Alias and Defaults:user lines
func parseSudoers(file string, removable bool) *refFile {
	rf := &refFile{removable: removable}
	isInclude := func(s string) bool { return strings.HasPrefix(s, "#include") }
	lines, numbers := logicalLines(file, isInclude)
	for i, line := range lines {
		fields := strings.Fields(line)
		var users []string
		switch {
		case isInclude(line) || strings.HasPrefix(line, "@include"):
			rf.other = true
			continue
		case fields[0] == "User_Alias":
			if _, list, ok := strings.Cut(line, "="); ok {
				users = splitList(list)
			}
		case strings.HasPrefix(fields[0], "Defaults:"):
			users = splitList(strings.TrimPrefix(fields[0], "Defaults:"))
		case strings.HasPrefix(fields[0], "Defaults") || strings.HasSuffix(fields[0], "_Alias"):
			rf.other = true
			continue
		default:
			// user list up to the first host list: "alice,bob host = ..."
			users = splitList(fields[0])
		}
		rf.add("sudoers", file, numbers[i], line, users)
	}
	return rf
}

// parseCron reads system crontabs: "min hour dom mon dow user command"
func parseCron(file string, removable bool) *refFile {
	rf := &refFile{removable: removable}
	lines, numbers := logicalLines(file, nil)
	for i, line := range lines {
		fields := strings.Fields(line)
		userField := 5
		if cronSpecial[fields[0]] {
			userField = 1
		}
		if len(fields) <= userField {
			if strings.Contains(fields[0], "=") {
				continue // environment setting
			}
			rf.other = true
			continue
		}
		if strings.Contains(fields[0], "=") && !cronSpecial[fields[0]] {
			continue
		}
		rf.add("cron", file, numbers[i], line, []string{fields[userField]})
	}
	return rf
}

// parseSshd reads AllowUsers, DenyUsers and Match User
func parseSshd(file string, removable bool) *refFile {
	rf := &refFile{removable: removable}
	lines, numbers := logicalLines(file, nil)
	for i, line := range lines {
		fields := strings.Fields(line)
		key := strings.ToLower(fields[0])
		var users []string
		switch key {
		case "allowusers", "denyusers":
			for _, pattern := range fields[1:] {
				user, _, _ := strings.Cut(pattern, "@")
				users = append(users, user)
			}
		case "match":
			for j := 1; j+1 < len(fields); j += 2 {
				if strings.EqualFold(fields[j], "user") {
					users = append(users, splitList(fields[j+1])...)
				}
			}
		}
		if users == nil {
			rf.other = true
			continue
		}
		rf.add("sshd", file, numbers[i], line, users)
	}
	return rf
}

// parseLimits reads "<domain> <type> <item> <value>" lines
func parseLimits(file string, removable bool) *refFile {
	rf := &refFile{removable: removable}
	lines, numbers := logicalLines(file, nil)
	for i, line := range lines {
		rf.add("limits", file, numbers[i], line, []string{strings.Fields(line)[0]})
	}
	return rf
}

// parseAliases reads "name: target, target" lines of /etc/aliases
func parseAliases(file string) *refFile {
	rf := &refFile{}
	lines, numbers := logicalLines(file, nil)
	for i, line := range lines {
		name, targets, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		users := []string{strings.TrimSpace(name)}
		for _, target := range splitList(targets) {
			if !strings.ContainsAny(target, "@/|:\"") {
				users = append(users, target)
			}
		}
		rf.add("aliases", file, numbers[i], line, users)
	}
	return rf
}

func (rf *refFile) add(kind, file string, line int, text string, names []string) {
	var users []string
	for _, name := range names {
		if userName.MatchString(name) {
			users = appendUnique(users, name)
		}
	}
	if len(users) == 0 {
		rf.other = true
		return
	}
	rf.refs = append(rf.refs, &Reference{Kind: kind, File: file, Line: line, Text: text, Users: users})
}

// collectReferences parses sudoers, system crontabs, sshd, limits and aliases
func collectReferences() []*refFile {
	var files []*refFile
	files = append(files, parseSudoers(filepath.Join(etcDir, "sudoers"), false))
	for _, file := range dropIns(filepath.Join(etcDir, "sudoers.d"), "") {
		files = append(files, parseSudoers(file, true))
	}
	files = append(files, parseCron(filepath.Join(etcDir, "crontab"), false))
	for _, file := range dropIns(filepath.Join(etcDir, "cron.d"), "") {
		files = append(files, parseCron(file, true))
	}
	files = append(files, parseSshd(filepath.Join(etcDir, "ssh", "sshd_config"), false))
	for _, file := range dropIns(filepath.Join(etcDir, "ssh", "sshd_config.d"), ".conf") {
		files = append(files, parseSshd(file, true))
	}
	files = append(files, parseLimits(filepath.Join(etcDir, "security", "limits.conf"), false))
	for _, file := range dropIns(filepath.Join(etcDir, "security", "limits.d"), ".conf") {
		files = append(files, parseLimits(file, true))
	}
	files = append(files, parseAliases(filepath.Join(etcDir, "aliases")))
	return files
}
