package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Pool is a PHP-FPM pool
type Pool struct {
	File     string            `json:"file"`
	Line     int               `json:"line"`
	EndLine  int               `json:"end_line"`
	Name     string            `json:"name"`
	Version  string            `json:"version,omitempty"`
	User     string            `json:"user,omitempty"`
	Group    string            `json:"group,omitempty"`
	Listen   string            `json:"listen,omitempty"`
	Chdir    string            `json:"chdir,omitempty"`
	Settings map[string]string `json:"settings,omitempty"` // php_value / php_admin_value / env
	Owners   []string          `json:"owners,omitempty"`
}

// LogrotateEntry is one block of a logrotate config
type LogrotateEntry struct {
	File        string   `json:"file"`
	Line        int      `json:"line"`
	EndLine     int      `json:"end_line"`
	Paths       []string `json:"paths"`
	Su          string   `json:"su,omitempty"`
	CreateOwner string   `json:"create_owner,omitempty"`
	Owners      []string `json:"owners,omitempty"`
}

// collectPools reads all PHP-FPM pool files
func collectPools() []*Pool {
	var files []string
	for _, pattern := range []string{
		filepath.Join(etcDir, "php", "*", "fpm", "pool.d", "*.conf"),
		filepath.Join(etcDir, "php-fpm.d", "*.conf"),
	} {
		matches, _ := filepath.Glob(pattern)
		files = append(files, matches...)
	}
	sort.Strings(files)

	var pools []*Pool
	for _, file := range files {
		pools = append(pools, parsePoolFile(file)...)
	}
	return pools
}

// phpVersion returns the version directory of a pool file below /etc/php
func phpVersion(file string) string {
	rel, err := filepath.Rel(filepath.Join(etcDir, "php"), file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return strings.SplitN(rel, string(filepath.Separator), 2)[0]
}

// parsePoolFile reads the [pool] sections of a PHP-FPM config file
func parsePoolFile(file string) []*Pool {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()

	var pools []*Pool
	var cur *Pool
	lastLine := 0
	finish := func() {
		if cur != nil && cur.Name != "global" {
			cur.EndLine = lastLine
			pools = append(pools, cur)
		}
		cur = nil
	}

	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			finish()
			cur = &Pool{File: file, Line: lineNo, Name: strings.Trim(line, "[]"), Version: phpVersion(file),
				Settings: make(map[string]string)}
			lastLine = lineNo
			continue
		}
		if cur == nil {
			continue
		}
		lastLine = lineNo
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch {
		case key == "user":
			cur.User = value
		case key == "group":
			cur.Group = value
		case key == "listen":
			cur.Listen = value
		case key == "chdir":
			cur.Chdir = value
		case strings.HasPrefix(key, "php_") || strings.HasPrefix(key, "env["):
			cur.Settings[key] = value
		}
	}
	finish()
	return pools
}

// scriptDirectives start a shell script that runs until "endscript"
var scriptDirectives = map[string]bool{
	"postrotate": true, "prerotate": true, "firstaction": true,
	"lastaction": true, "preremove": true,
}

// collectLogrotate reads /etc/logrotate.conf and /etc/logrotate.d
func collectLogrotate() []*LogrotateEntry {
	files := []string{filepath.Join(etcDir, "logrotate.conf")}
	entries, _ := os.ReadDir(filepath.Join(etcDir, "logrotate.d"))
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, filepath.Join(etcDir, "logrotate.d", entry.Name()))
		}
	}

	var result []*LogrotateEntry
	for _, file := range files {
		result = append(result, parseLogrotateFile(file)...)
	}
	return result
}

// parseLogrotateFile reads the "<paths> { ... }" blocks of a logrotate file
func parseLogrotateFile(file string) []*LogrotateEntry {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()

	var result []*LogrotateEntry
	var cur *LogrotateEntry
	var pending []string
	pendingLine := 0
	inScript := false

	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if inScript {
			inScript = strings.Fields(line)[0] != "endscript"
			continue
		}

		if cur == nil {
			before, _, hasBrace := strings.Cut(line, "{")
			fields := splitFields(before)
			if !hasBrace {
				// Paths may be listed over several lines before the "{"
				if len(fields) > 0 && (strings.HasPrefix(fields[0], "/") || strings.HasPrefix(fields[0], "~")) {
					if pending == nil {
						pendingLine = lineNo
					}
					pending = append(pending, fields...)
				} else {
					pending = nil
				}
				continue
			}
			if pending == nil {
				pendingLine = lineNo
			}
			cur = &LogrotateEntry{File: file, Line: pendingLine, Paths: append(pending, fields...)}
			pending = nil
			continue
		}

		if strings.HasPrefix(line, "}") {
			cur.EndLine = lineNo
			result = append(result, cur)
			cur = nil
			continue
		}
		fields := splitFields(line)
		switch {
		case scriptDirectives[fields[0]]:
			inScript = true
		case fields[0] == "su" && len(fields) > 1:
			cur.Su = fields[1]
		case fields[0] == "create" && len(fields) > 1:
			// create [mode [owner [group]]]
			args := fields[1:]
			if strings.Trim(args[0], "01234567") == "" {
				args = args[1:]
			}
			if len(args) > 0 {
				cur.CreateOwner = args[0]
			}
		}
	}
	return result
}
