package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Unit is a systemd service that runs as a specific user
type Unit struct {
	Name    string   `json:"name"`
	File    string   `json:"file"`
	User    string   `json:"user"`
	Enabled bool     `json:"enabled"`
	Owners  []string `json:"owners,omitempty"`
}

// collectUnits reads the local service units in /etc/systemd/system that
// set User=. The general analysis of systemd follows in a later step.
func collectUnits() []*Unit {
	dir := filepath.Join(etcDir, "systemd", "system")
	files, _ := filepath.Glob(filepath.Join(dir, "*.service"))
	sort.Strings(files)

	var units []*Unit
	for _, file := range files {
		if info, err := os.Lstat(file); err != nil || !info.Mode().IsRegular() {
			continue // links point to vendor units or /dev/null (masked)
		}
		user := unitUser(file)
		if user == "" {
			continue
		}
		name := filepath.Base(file)
		wants, _ := filepath.Glob(filepath.Join(dir, "*.wants", name))
		units = append(units, &Unit{Name: name, File: file, User: user, Enabled: len(wants) > 0})
	}
	return units
}

// unitUser returns the User= of the [Service] section
func unitUser(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()

	section, user := "", ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "["):
			section = line
		case section == "[Service]" && strings.HasPrefix(line, "User="):
			user = strings.TrimSpace(strings.TrimPrefix(line, "User="))
		}
	}
	return user
}
