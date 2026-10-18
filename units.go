package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Unit is a local systemd service that runs as a user (User=) or works with
// files of a user (WorkingDirectory=, ExecStart=, EnvironmentFile=)
type Unit struct {
	Name    string   `json:"name"`
	File    string   `json:"file"`
	DropIn  bool     `json:"drop_in,omitempty"` // File is an override of a vendor unit
	User    string   `json:"user,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	Enabled bool     `json:"enabled"`
	Timer   string   `json:"timer,omitempty"` // a .timer that starts this unit
	Owners  []string `json:"owners,omitempty"`
}

// collectUnits reads the service units and drop-ins in /etc/systemd/system
func collectUnits() []*Unit {
	dir := filepath.Join(etcDir, "systemd", "system")
	var units []*Unit

	files, _ := filepath.Glob(filepath.Join(dir, "*.service"))
	sort.Strings(files)
	for _, file := range files {
		if info, err := os.Lstat(file); err != nil || !info.Mode().IsRegular() {
			continue // links point to vendor units or /dev/null (masked)
		}
		if unit := parseUnit(file, filepath.Base(file)); unit != nil {
			units = append(units, unit)
		}
	}

	dropIns, _ := filepath.Glob(filepath.Join(dir, "*.service.d", "*.conf"))
	sort.Strings(dropIns)
	for _, file := range dropIns {
		name := strings.TrimSuffix(filepath.Base(filepath.Dir(file)), ".d")
		if unit := parseUnit(file, name); unit != nil {
			unit.DropIn = true
			units = append(units, unit)
		}
	}

	for _, unit := range units {
		wants, _ := filepath.Glob(filepath.Join(dir, "*.wants", unit.Name))
		unit.Enabled = len(wants) > 0
		timer := filepath.Join(dir, strings.TrimSuffix(unit.Name, ".service")+".timer")
		if exists(timer) {
			unit.Timer = timer
		}
	}
	return units
}

// parseUnit reads User= and the paths of the [Service] section; it returns
// nil for units that neither set a user nor use absolute paths
func parseUnit(file, name string) *Unit {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()

	unit := &Unit{Name: name, File: file}
	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if section != "[Service]" || !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "User":
			unit.User = value
		case "WorkingDirectory", "EnvironmentFile", "ExecStart":
			// ExecStart=-/path args, EnvironmentFile=-/path
			fields := strings.Fields(value)
			if len(fields) > 0 {
				path := strings.TrimLeft(fields[0], "-@:+!")
				if strings.HasPrefix(path, "/") {
					unit.Paths = appendUnique(unit.Paths, path)
				}
			}
		}
	}
	if unit.User == "" && len(unit.Paths) == 0 {
		return nil
	}
	return unit
}

// userUnits returns the systemd user units in the user's home
func userUnits(user *User) []string {
	if filepath.Clean(user.HomeDir) == "/" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(user.HomeDir, ".config", "systemd", "user", "*.service"))
	sort.Strings(files)
	return files
}
