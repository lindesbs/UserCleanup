package usercleanup

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// tuiTab is one view of a user in the TUI: the areas of "list --only", or
// the generated commands
type tuiTab struct {
	name  string
	areas []string
}

var tuiTabs = []tuiTab{
	{"All", nil},
	{"Account", []string{"users"}},
	{"Web", []string{"sites"}},
	{"PHP", []string{"php"}},
	{"MySQL", []string{"mysql"}},
	{"systemd/cron", []string{"systemd", "cron"}},
	{"/etc", []string{"etc", "files"}},
	{"Logrotate", []string{"logrotate"}},
	{"Dirs", []string{"dirs"}},
	{"Plan", nil},
}

// planTab is the index of the tab with the generated commands
var planTab = len(tuiTabs) - 1

type tuiScreen int

const (
	screenLoading tuiScreen = iota
	screenList
	screenDetail
	screenFindings
)

// tuiModel is the state of the TUI. It only reads, backs up and writes
// the generated commands to a file; it never runs them.
type tuiModel struct {
	screen tuiScreen
	err    error
	all    map[string]*User
	users  []*User
	inv    *Inventory

	cursor, offset int
	tab            int
	view           viewport.Model
	width, height  int

	status  string
	confirm *tuiConfirm
	// plans are the generated commands per user, with the backup used
	plans map[string]tuiPlan
}

type tuiPlan struct {
	script, archive string
	err             error
}

// tuiConfirm is a question answered with y or n
type tuiConfirm struct {
	prompt string
	run    func() tea.Cmd
}

type tuiLoadedMsg struct {
	all   map[string]*User
	users []*User
	inv   *Inventory
	err   error
}

type tuiBackupMsg struct {
	user     string
	archive  string
	manifest *Manifest
	err      error
}

// tuiLoad collects the data of all users, like "list" without filter
func tuiLoad() tea.Msg {
	all, err := readPasswdFile(true)
	if err != nil {
		return tuiLoadedMsg{err: fmt.Errorf("error reading passwd file: %v", err)}
	}
	inv := collectInventory(all)
	shown := make(map[string]*User)
	for name, user := range all {
		if !user.IsSystemUser || systemUsers {
			shown[name] = user
		}
	}
	if err := findUserConfigs(shown); err != nil {
		return tuiLoadedMsg{err: fmt.Errorf("error finding user configurations: %v", err)}
	}
	users := sortedUsers(shown)
	for _, user := range users {
		prepareUser(user, inv)
	}
	return tuiLoadedMsg{all: all, users: users, inv: inv}
}

func newTUIModel() tuiModel {
	return tuiModel{screen: screenLoading, view: viewport.New(80, 20), width: 80, height: 24,
		plans: make(map[string]tuiPlan)}
}

func (m tuiModel) Init() tea.Cmd {
	return tuiLoad
}

// selected returns the user under the cursor
func (m tuiModel) selected() *User {
	if m.cursor < 0 || m.cursor >= len(m.users) {
		return nil
	}
	return m.users[m.cursor]
}

// listHeight is the number of user rows that fit on the screen
func (m tuiModel) listHeight() int {
	if h := m.height - 6; h > 1 {
		return h
	}
	return 1
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.view.Width, m.view.Height = msg.Width, max(msg.Height-6, 1)
		m.refresh()
		return m, nil

	case tuiLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.all, m.users, m.inv = msg.all, msg.users, msg.inv
		m.screen = screenList
		m.status = fmt.Sprintf("%d users, %d findings", len(m.users), len(m.inv.Findings))
		return m, nil

	case tuiBackupMsg:
		if msg.err != nil {
			m.status = "Backup of " + msg.user + " failed: " + msg.err.Error()
			return m, nil
		}
		m.status = "Backup written to " + msg.archive
		if n := len(msg.manifest.Skipped); n > 0 {
			m.status += fmt.Sprintf(" (%d entries not backed up, see the manifest)", n)
		}
		delete(m.plans, msg.user)
		m.refresh()
		return m, nil

	case tuiWrittenMsg:
		if msg.err != nil {
			m.status = "Not written: " + msg.err.Error()
		} else {
			m.status = "Commands written to " + msg.path + ". Review them, then run: sudo sh " + msg.path
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}

	if m.screen == screenDetail || m.screen == screenFindings {
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m tuiModel) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.confirm != nil {
		confirm := m.confirm
		m.confirm = nil
		if key == "y" || key == "Y" {
			return m, confirm.run()
		}
		m.status = "Cancelled"
		return m, nil
	}
	if m.screen == screenLoading {
		if key == "q" {
			return m, tea.Quit
		}
		return m, nil
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "esc", "backspace":
		if m.screen != screenList {
			m.screen = screenList
			return m, nil
		}
	case "f":
		m.screen = screenFindings
		m.refresh()
		m.view.GotoTop()
		return m, nil
	case "b":
		return m.askBackup()
	case "p":
		if m.selected() != nil {
			m.screen, m.tab = screenDetail, planTab
			m.refresh()
			m.view.GotoTop()
		}
		return m, nil
	case "w":
		return m.askWritePlan()
	}

	switch m.screen {
	case screenList:
		switch key {
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup":
			m.move(-m.listHeight())
		case "pgdown":
			m.move(m.listHeight())
		case "home", "g":
			m.move(-len(m.users))
		case "end", "G":
			m.move(len(m.users))
		case "enter", "right", "l":
			if m.selected() != nil {
				m.screen, m.tab = screenDetail, 0
				m.refresh()
				m.view.GotoTop()
			}
		}
		return m, nil
	case screenDetail:
		switch key {
		case "tab", "right", "l":
			m.tab = (m.tab + 1) % len(tuiTabs)
		case "shift+tab", "left", "h":
			m.tab = (m.tab + len(tuiTabs) - 1) % len(tuiTabs)
		case "n":
			m.move(1)
		case "N":
			m.move(-1)
		default:
			if n := strings.Index("1234567890", key); len(key) == 1 && n >= 0 && n < len(tuiTabs) {
				m.tab = n
			} else {
				var cmd tea.Cmd
				m.view, cmd = m.view.Update(msg)
				return m, cmd
			}
		}
		m.refresh()
		m.view.GotoTop()
		return m, nil
	}
	var cmd tea.Cmd
	m.view, cmd = m.view.Update(msg)
	return m, cmd
}

// move moves the cursor and keeps it on the screen
func (m *tuiModel) move(delta int) {
	m.cursor = min(max(m.cursor+delta, 0), max(len(m.users)-1, 0))
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if h := m.listHeight(); m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.screen == screenDetail {
		m.refresh()
	}
}

func (m tuiModel) askBackup() (tea.Model, tea.Cmd) {
	user := m.selected()
	if user == nil || m.screen == screenFindings {
		return m, nil
	}
	m.confirm = &tuiConfirm{
		prompt: fmt.Sprintf("Back up %s to %s? [y/N]", user.Username, backupDir),
		run: func() tea.Cmd {
			return func() tea.Msg {
				archive, err := backupUser(user, m.all, backupDir)
				msg := tuiBackupMsg{user: user.Username, archive: archive, err: err}
				if err == nil {
					msg.manifest, msg.err = readManifest(archive)
				}
				return msg
			}
		},
	}
	return m, nil
}

func (m tuiModel) askWritePlan() (tea.Model, tea.Cmd) {
	user := m.selected()
	if user == nil || m.screen == screenFindings {
		return m, nil
	}
	plan := m.plan(user)
	if plan.err != nil {
		m.status = "No commands: " + plan.err.Error()
		return m, nil
	}
	path, _ := filepath.Abs("remove-" + user.Username + ".sh")
	m.confirm = &tuiConfirm{
		prompt: fmt.Sprintf("Write the commands for %s to %s? [y/N]", user.Username, path),
		run: func() tea.Cmd {
			return func() tea.Msg {
				return tuiWrittenMsg{path: path, err: writeNewFile(path, plan.script)}
			}
		},
	}
	return m, nil
}

type tuiWrittenMsg struct {
	path string
	err  error
}

// writeNewFile writes content with mode 0600 and never overwrites a file
func writeNewFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// plan returns the generated commands of a user, cached per backup
func (m tuiModel) plan(user *User) tuiPlan {
	if plan, ok := m.plans[user.Username]; ok {
		return plan
	}
	var plan tuiPlan
	plan.script, plan.archive, plan.err = planForUser(user, m.inv, "")
	m.plans[user.Username] = plan
	return plan
}

// refresh renders the content of the detail or findings screen
func (m *tuiModel) refresh() {
	var b bytes.Buffer
	switch m.screen {
	case screenFindings:
		if m.inv == nil {
			return
		}
		printUnownedSites(&b, m.inv.UnownedSites())
		printFindings(&b, m.inv.Findings)
	case screenDetail:
		user := m.selected()
		if user == nil {
			return
		}
		if m.tab == planTab {
			plan := m.plan(user)
			switch {
			case plan.err != nil && isProtected(user) == "" && plan.archive == "":
				fmt.Fprintf(&b, "No commands for %s: %v\n\nPress b to create a backup first.\n", user.Username, plan.err)
			case plan.err != nil:
				fmt.Fprintf(&b, "No commands for %s: %v\n", user.Username, plan.err)
			default:
				fmt.Fprintf(&b, "# Nothing has been run. Press w to write these commands to a file.\n%s", plan.script)
			}
			break
		}
		f, _ := parseListFilter(tuiTabs[m.tab].areas)
		data := m.inv.For(user.Username)
		if !f.has("users") && !f.shows(user, data) {
			fmt.Fprintf(&b, "Nothing in %s for %s.\n", tuiTabs[m.tab].name, user.Username)
			break
		}
		printUserInfo(&b, user, data, f)
	default:
		return
	}
	m.view.SetContent(b.String())
}

var (
	tuiTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("62")).Padding(0, 1)
	tuiSelected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("240"))
	tuiDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	tuiWarn     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	tuiTabOn    = lipgloss.NewStyle().Bold(true).Underline(true)
	tuiPrompt   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
)

// userSummary lists what belongs to the user in one short line
func (m tuiModel) userSummary(user *User) string {
	data := m.inv.For(user.Username)
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	databases := 0
	for _, acct := range data.MySQL {
		databases += len(acct.Databases)
	}
	add(len(data.Sites), "sites")
	add(len(data.Pools), "pools")
	add(databases, "databases")
	add(len(data.Units)+len(data.UserUnits), "units")
	add(len(data.Refs), "/etc entries")
	add(len(data.Logrotate), "logrotate")
	add(len(data.Dirs), "dirs")
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func (m tuiModel) View() string {
	var b strings.Builder
	header := tuiTitle.Render("UserCleanup " + appVersion)
	switch m.screen {
	case screenLoading:
		return header + "\n\nCollecting users, sites, pools, databases and /etc entries...\n"
	case screenList:
		b.WriteString(header + "  " + tuiDim.Render("backups: "+backupDir) + "\n\n")
		end := min(m.offset+m.listHeight(), len(m.users))
		for i := m.offset; i < end; i++ {
			user := m.users[i]
			name := fmt.Sprintf("%-16s %6d  ", user.Username, user.UID)
			summary := m.userSummary(user)
			if isProtected(user) != "" {
				summary = "protected · " + summary
			}
			row := name + summary
			if runes := []rune(row); m.width > 4 && len(runes) > m.width-2 {
				row = string(runes[:m.width-3]) + "…"
			}
			if i == m.cursor {
				b.WriteString(tuiSelected.Render("> "+row) + "\n")
			} else {
				b.WriteString("  " + row + "\n")
			}
		}
		if len(m.users) == 0 {
			b.WriteString("  No users found (use -s for system users)\n")
		}
		for i := end - m.offset; i < m.listHeight(); i++ {
			b.WriteString("\n")
		}
		b.WriteString(m.footer("↑/↓ select · enter details · b backup · p commands · w write commands · f findings · q quit"))
	case screenDetail:
		user := m.selected()
		b.WriteString(header + "  " + tuiWarn.Render(user.Username) + "\n")
		var tabs []string
		for i, tab := range tuiTabs {
			label := fmt.Sprintf("%d %s", (i+1)%10, tab.name)
			if i == m.tab {
				tabs = append(tabs, tuiTabOn.Render(label))
			} else {
				tabs = append(tabs, tuiDim.Render(label))
			}
		}
		b.WriteString(strings.Join(tabs, "  ") + "\n")
		b.WriteString(m.view.View() + "\n")
		b.WriteString(m.footer("tab/←/→ area · ↑/↓ scroll · n/N next user · b backup · w write commands · esc back · q quit"))
	case screenFindings:
		b.WriteString(header + "  " + tuiWarn.Render("Sites without owner and findings") + "\n\n")
		b.WriteString(m.view.View() + "\n")
		b.WriteString(m.footer("↑/↓ scroll · esc back · q quit"))
	}
	return b.String()
}

// footer shows the question, the status line and the keys
func (m tuiModel) footer(keys string) string {
	var lines []string
	if m.confirm != nil {
		lines = append(lines, tuiPrompt.Render(m.confirm.prompt))
	} else {
		lines = append(lines, m.status)
	}
	lines = append(lines, tuiDim.Render(keys))
	return strings.Join(lines, "\n")
}

// runTUI starts the interactive interface
func runTUI() error {
	// Warnings would break the screen; they are shown from the manifest
	backupWarnings = io.Discard
	final, err := tea.NewProgram(newTUIModel(), tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	if m, ok := final.(tuiModel); ok && m.err != nil {
		return m.err
	}
	return nil
}
