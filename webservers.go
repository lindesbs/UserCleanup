package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Site is a virtual host of Apache or nginx, or a site block of Caddy
type Site struct {
	Server      string   `json:"server"` // apache, nginx or caddy
	File        string   `json:"file"`
	Line        int      `json:"line"`
	EndLine     int      `json:"end_line"`
	Enabled     bool     `json:"enabled"`
	EnabledLink string   `json:"enabled_link,omitempty"` // symlink in sites-enabled
	Names       []string `json:"names"`
	Root        string   `json:"root,omitempty"`
	User        string   `json:"user,omitempty"` // SuexecUserGroup / AssignUserID
	Backends    []string `json:"backends,omitempty"`
	Owners      []string `json:"owners,omitempty"`
}

// siteFile is a config file of a web server and how it is enabled
type siteFile struct {
	path        string
	enabled     bool
	enabledLink string
}

// siteFiles returns the files in a Debian style sites-available directory
// with their state in sites-enabled, plus files that only exist in
// sites-enabled and all *.conf files of the extra directories.
func siteFiles(available, enabled string, extra ...string) []siteFile {
	var files []siteFile
	seen := make(map[string]bool)

	links := make(map[string]string) // resolved target -> link
	entries, _ := os.ReadDir(enabled)
	for _, entry := range entries {
		link := filepath.Join(enabled, entry.Name())
		if target, err := filepath.EvalSymlinks(link); err == nil {
			links[target] = link
		}
	}

	availEntries, _ := os.ReadDir(available)
	for _, entry := range availEntries {
		path := filepath.Join(available, entry.Name())
		if !entry.Type().IsRegular() {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		link := links[resolved]
		files = append(files, siteFile{path: path, enabled: link != "", enabledLink: link})
		seen[resolved] = true
	}

	for _, entry := range entries {
		path := filepath.Join(enabled, entry.Name())
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || seen[resolved] {
			continue
		}
		if info, err := os.Stat(resolved); err == nil && info.Mode().IsRegular() {
			files = append(files, siteFile{path: path, enabled: true})
			seen[resolved] = true
		}
	}

	for _, dir := range extra {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.conf"))
		sort.Strings(matches)
		for _, path := range matches {
			if resolved, err := filepath.EvalSymlinks(path); err == nil && !seen[resolved] {
				files = append(files, siteFile{path: path, enabled: true})
				seen[resolved] = true
			}
		}
	}
	return files
}

// splitFields splits a config line into fields, keeping double quoted
// strings together and removing the quotes
func splitFields(line string) []string {
	var fields []string
	var cur strings.Builder
	inQuote, hasField := false, false
	for _, r := range line {
		switch {
		case r == '"':
			inQuote = !inQuote
			hasField = true
		case !inQuote && (r == ' ' || r == '\t'):
			if hasField {
				fields = append(fields, cur.String())
				cur.Reset()
				hasField = false
			}
		default:
			cur.WriteRune(r)
			hasField = true
		}
	}
	if hasField {
		fields = append(fields, cur.String())
	}
	return fields
}

// stripComment removes a trailing # comment that is not inside quotes
func stripComment(line string) string {
	inQuote := false
	for i, r := range line {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == '#' && !inQuote && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

// fcgiBackend extracts a PHP-FPM endpoint from a handler or proxy value
// such as "proxy:unix:/run/php/web.sock|fcgi://localhost" or
// "fcgi://127.0.0.1:9000/var/www/$1".
func fcgiBackend(value string) string {
	if i := strings.Index(value, "unix:"); i >= 0 {
		socket := value[i+len("unix:"):]
		if j := strings.IndexAny(socket, "|\" "); j >= 0 {
			socket = socket[:j]
		}
		return socket
	}
	if i := strings.Index(value, "fcgi://"); i >= 0 {
		addr := value[i+len("fcgi://"):]
		if j := strings.IndexAny(addr, "/\" "); j >= 0 {
			addr = addr[:j]
		}
		if addr == "localhost" {
			return "" // only the socket part above is meaningful then
		}
		return addr
	}
	return ""
}

// parseApacheFile reads the <VirtualHost> blocks of an Apache config file
func parseApacheFile(file siteFile) []*Site {
	f, err := os.Open(file.path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var sites []*Site
	var cur *Site
	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		fields := splitFields(strings.TrimSpace(stripComment(scanner.Text())))
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(strings.TrimPrefix(fields[0], "<"))
		args := fields[1:]

		switch {
		case name == "virtualhost":
			cur = &Site{Server: "apache", File: file.path, Line: lineNo, Enabled: file.enabled, EnabledLink: file.enabledLink}
		case name == "/virtualhost>":
			if cur != nil {
				cur.EndLine = lineNo
				sites = append(sites, cur)
				cur = nil
			}
		case cur == nil:
		case name == "servername" || name == "serveralias":
			cur.Names = append(cur.Names, args...)
		case name == "documentroot" && len(args) > 0:
			cur.Root = filepath.Clean(args[0])
		case (name == "suexecusergroup" || name == "assignuserid") && len(args) > 0:
			cur.User = args[0]
		case name == "sethandler" || name == "proxypassmatch" || name == "proxypass":
			for _, arg := range args {
				if backend := fcgiBackend(arg); backend != "" {
					cur.Backends = appendUnique(cur.Backends, backend)
				}
			}
		}
	}
	return sites
}

// ngxNode is a directive of an nginx config, with its block if it has one
type ngxNode struct {
	name     string
	args     []string
	line     int
	endLine  int
	children []*ngxNode
}

// ngxToken is a word, quoted string, or one of { } ;
type ngxToken struct {
	text   string
	line   int
	symbol bool
}

func tokenizeNginx(data string) []ngxToken {
	var tokens []ngxToken
	line := 1
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case c == '\n':
			line++
		case c == ' ' || c == '\t' || c == '\r':
		case c == '#':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			i-- // let the loop see the newline
		case c == '{' || c == '}' || c == ';':
			tokens = append(tokens, ngxToken{text: string(c), line: line, symbol: true})
		case c == '"' || c == '\'':
			start := line
			j := i + 1
			for j < len(data) && data[j] != c {
				if data[j] == '\\' {
					j++
				} else if data[j] == '\n' {
					line++
				}
				j++
			}
			if j > len(data) {
				j = len(data)
			}
			tokens = append(tokens, ngxToken{text: data[i+1 : j], line: start})
			i = j
		default:
			j := i
			for j < len(data) && !strings.ContainsRune(" \t\r\n{};", rune(data[j])) {
				j++
			}
			tokens = append(tokens, ngxToken{text: data[i:j], line: line})
			i = j - 1
		}
	}
	return tokens
}

// parseNginxTokens builds the directive tree; pos is advanced past the block
func parseNginxTokens(tokens []ngxToken, pos *int) []*ngxNode {
	var nodes []*ngxNode
	var cur *ngxNode
	for *pos < len(tokens) {
		tok := tokens[*pos]
		*pos++
		switch {
		case tok.symbol && tok.text == "}":
			return nodes
		case tok.symbol && tok.text == ";":
			if cur != nil {
				nodes = append(nodes, cur)
				cur = nil
			}
		case tok.symbol && tok.text == "{":
			if cur == nil {
				cur = &ngxNode{line: tok.line}
			}
			cur.children = parseNginxTokens(tokens, pos)
			if *pos > 0 && *pos <= len(tokens) {
				cur.endLine = tokens[*pos-1].line
			}
			nodes = append(nodes, cur)
			cur = nil
		case cur == nil:
			cur = &ngxNode{name: tok.text, line: tok.line}
		default:
			cur.args = append(cur.args, tok.text)
		}
	}
	return nodes
}

// parseNginxFile reads the server blocks of an nginx config file
func parseNginxFile(file siteFile) []*Site {
	data, err := os.ReadFile(file.path)
	if err != nil {
		return nil
	}
	pos := 0
	tree := parseNginxTokens(tokenizeNginx(string(data)), &pos)

	var sites []*Site
	var walk func(nodes []*ngxNode)
	walk = func(nodes []*ngxNode) {
		for _, node := range nodes {
			if node.name != "server" || node.children == nil {
				walk(node.children)
				continue
			}
			site := &Site{Server: "nginx", File: file.path, Line: node.line, EndLine: node.endLine,
				Enabled: file.enabled, EnabledLink: file.enabledLink}
			var visit func(nodes []*ngxNode, depth int)
			visit = func(nodes []*ngxNode, depth int) {
				for _, n := range nodes {
					switch {
					case n.name == "server_name" && depth == 0:
						site.Names = append(site.Names, n.args...)
					case n.name == "root" && len(n.args) > 0 && (depth == 0 || site.Root == ""):
						site.Root = filepath.Clean(n.args[0])
					case n.name == "fastcgi_pass" && len(n.args) > 0:
						site.Backends = appendUnique(site.Backends, strings.TrimPrefix(n.args[0], "unix:"))
					}
					visit(n.children, depth+1)
				}
			}
			visit(node.children, 0)
			sites = append(sites, site)
		}
	}
	walk(tree)
	return sites
}

// parseCaddyfile reads the site blocks of a Caddyfile and the files it imports
func parseCaddyfile(path string, seen map[string]bool) []*Site {
	if seen[path] {
		return nil
	}
	seen[path] = true

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var sites []*Site
	var cur *Site
	depth := 0
	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		fields := splitFields(line)

		if depth == 0 {
			switch {
			case fields[0] == "import" && len(fields) > 1:
				pattern := fields[1]
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(filepath.Dir(path), pattern)
				}
				matches, _ := filepath.Glob(pattern)
				sort.Strings(matches)
				for _, match := range matches {
					sites = append(sites, parseCaddyfile(match, seen)...)
				}
			case strings.HasSuffix(line, "{"):
				head := strings.TrimSpace(strings.TrimSuffix(line, "{"))
				// "{" alone is the global options block, "(name)" a snippet
				if head != "" && !strings.HasPrefix(head, "(") {
					cur = &Site{Server: "caddy", File: path, Line: lineNo, Enabled: true}
					for _, name := range strings.FieldsFunc(head, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
						cur.Names = append(cur.Names, name)
					}
				}
			}
		} else if cur != nil {
			switch fields[0] {
			case "root":
				// root [<matcher>] <path>
				if len(fields) > 1 {
					cur.Root = filepath.Clean(fields[len(fields)-1])
				}
			case "php_fastcgi", "reverse_proxy":
				for _, arg := range fields[1:] {
					if strings.HasPrefix(arg, "unix/") {
						cur.Backends = appendUnique(cur.Backends, strings.TrimPrefix(arg, "unix/"))
					} else if fields[0] == "php_fastcgi" && strings.Contains(arg, ":") {
						cur.Backends = appendUnique(cur.Backends, arg)
					}
				}
			}
		}

		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth < 0 {
			depth = 0
		}
		if depth == 0 && cur != nil {
			cur.EndLine = lineNo
			sites = append(sites, cur)
			cur = nil
		}
	}
	return sites
}

// collectSites returns all sites of Apache, nginx and Caddy
func collectSites() []*Site {
	var sites []*Site
	apache := filepath.Join(etcDir, "apache2")
	for _, file := range siteFiles(filepath.Join(apache, "sites-available"), filepath.Join(apache, "sites-enabled"),
		filepath.Join(etcDir, "httpd", "conf.d")) {
		sites = append(sites, parseApacheFile(file)...)
	}
	nginx := filepath.Join(etcDir, "nginx")
	for _, file := range siteFiles(filepath.Join(nginx, "sites-available"), filepath.Join(nginx, "sites-enabled"),
		filepath.Join(nginx, "conf.d")) {
		sites = append(sites, parseNginxFile(file)...)
	}
	sites = append(sites, parseCaddyfile(filepath.Join(etcDir, "caddy", "Caddyfile"), make(map[string]bool))...)
	return sites
}

func appendUnique(list []string, value string) []string {
	for _, v := range list {
		if v == value {
			return list
		}
	}
	return append(list, value)
}
