package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func tuiKey(m tuiModel, keys ...string) (tuiModel, tea.Cmd) {
	var cmd tea.Cmd
	for _, key := range keys {
		var msg tea.KeyMsg
		switch key {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		}
		var model tea.Model
		model, cmd = m.Update(msg)
		m = model.(tuiModel)
	}
	return m, cmd
}

func TestTUI(t *testing.T) {
	_, _ = setupMySQL(t)
	root := t.TempDir()
	oldDir, oldWarnings := backupDir, backupWarnings
	backupDir, backupWarnings = filepath.Join(root, "backups"), os.Stderr
	t.Cleanup(func() { backupDir, backupWarnings = oldDir, oldWarnings })
	wd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	write(t, filepath.Join(etcDir, "php/8.2/fpm/pool.d/webmaster.conf"), "[webmaster]\nuser = webmaster\nlisten = /run/php/wm.sock\n")

	m := newTUIModel()
	if !strings.Contains(m.View(), "Collecting") {
		t.Errorf("loading view:\n%s", m.View())
	}
	model, _ := m.Update(tuiLoad())
	model, _ = model.(tuiModel).Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = model.(tuiModel)

	// System users are hidden; the list shows what belongs to each user
	view := m.View()
	if strings.Contains(view, "www-data") || !strings.Contains(view, "webmaster") {
		t.Fatalf("list view:\n%s", view)
	}
	if !strings.Contains(view, "1 pools, 3 databases") {
		t.Errorf("summary of webmaster missing:\n%s", view)
	}
	for m.selected().Username != "webmaster" {
		m, _ = tuiKey(m, "down")
	}

	// Details, MySQL tab
	m, _ = tuiKey(m, "enter", "5")
	if view := m.View(); !strings.Contains(view, "database shop: 2 tables") || strings.Contains(view, "PHP-FPM pools") {
		t.Errorf("MySQL tab:\n%s", view)
	}

	// Plan without backup asks for one
	m, _ = tuiKey(m, "p")
	if view := m.View(); !strings.Contains(view, "Press b to create a backup first") {
		t.Errorf("plan without backup:\n%s", view)
	}

	// Backup needs a confirmation; n cancels
	m, cmd := tuiKey(m, "b", "n")
	if cmd != nil || m.status != "Cancelled" {
		t.Errorf("backup not cancelled: %q", m.status)
	}
	m, _ = tuiKey(m, "b")
	if !strings.Contains(m.View(), "Back up webmaster to") {
		t.Errorf("no confirmation:\n%s", m.View())
	}
	m, cmd = tuiKey(m, "y")
	if cmd == nil {
		t.Fatal("y should start the backup")
	}
	model, _ = m.Update(cmd())
	m = model.(tuiModel)
	if !strings.Contains(m.status, "Backup written to "+backupDir) {
		t.Fatalf("status = %q", m.status)
	}

	// Now the plan tab shows the commands
	if view := m.View(); !strings.Contains(view, "Press w to write") || !strings.Contains(m.plans["webmaster"].script, "userdel 'webmaster'") {
		t.Errorf("plan after backup:\n%s", view)
	}

	// w writes them to a new file only after confirmation
	m, cmd = tuiKey(m, "w", "y")
	model, _ = m.Update(cmd())
	m = model.(tuiModel)
	path := filepath.Join(root, "remove-webmaster.sh")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "DROP DATABASE IF EXISTS `shop`") {
		t.Fatalf("written plan: %v\n%s", err, data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Errorf("plan mode = %o", info.Mode().Perm())
	}
	m, cmd = tuiKey(m, "w", "y")
	model, _ = m.Update(cmd())
	if status := model.(tuiModel).status; !strings.HasPrefix(status, "Not written") {
		t.Errorf("existing file must not be overwritten: %q", status)
	}

	// Findings screen and back
	m, _ = tuiKey(m, "f")
	if view := m.View(); !strings.Contains(view, "mysql_cnf_readable") {
		t.Errorf("findings:\n%s", view)
	}
	m, _ = tuiKey(m, "esc")
	if m.screen != screenList {
		t.Error("esc should go back to the list")
	}
	if _, cmd := tuiKey(m, "q"); cmd == nil {
		t.Error("q should quit")
	}
}
