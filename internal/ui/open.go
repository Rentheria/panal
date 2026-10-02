package ui

import (
	"os"
	"os/exec"
)

// currentDir: the dir (worktree) of what is being viewed: the selected run in
// the history or, on the dashboard, the selected agent's (or its last run's).
func (m Model) currentDir() string {
	if m.inHistory {
		if cs := m.filteredRuns(); m.historyCursor >= 0 && m.historyCursor < len(cs) {
			return cs[m.historyCursor].Dir
		}
		return ""
	}
	sel, ok := m.selected()
	if !ok {
		return ""
	}
	if c := m.rows[sel].Dir; c != "" {
		return c
	}
	if c, ok := m.lastRun(m.rows[sel].Agent); ok {
		return c.Dir
	}
	return ""
}

// What a dir is opened with: c in VS Code, w in a new Windows Terminal tab.
// Tests can replace them.
var (
	openInEditor   = []string{"code"}
	openInTerminal = []string{"wt", "-w", "0", "nt", "-d"}
	launch         = func(args []string) error { return exec.Command(args[0], args[1:]...).Start() }
)

// openDir opens the selected item's dir (c or w key). It only launches the
// program: the dashboard touches nothing.
func (m *Model) openDir(key string) {
	dir := m.currentDir()
	if dir == "" {
		m.alert, m.alertTime = "this run doesn't say which dir it worked in", m.now
		return
	}
	if _, err := os.Stat(dir); err != nil {
		m.alert, m.alertTime = "the dir no longer exists: "+dir, m.now
		return
	}
	cmd, name := openInEditor, "VS Code"
	if key == "w" {
		cmd, name = openInTerminal, "the terminal"
	}
	if err := launch(append(append([]string(nil), cmd...), dir)); err != nil {
		m.alert, m.alertTime = "could not open "+name+": "+err.Error(), m.now
		return
	}
	m.alert, m.alertTime = "opening "+dir+" in "+name, m.now
}
