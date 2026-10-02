package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

func TestHelpKeysAndOpenClose(t *testing.T) {
	ls := []readers.Reader{
		testReader{row: state.Row{Agent: "claude", Status: state.Working}},
	}
	m := New(ls, 2*time.Second)

	// 1. Open help from the main dashboard with '?'
	mod, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mod.(Model)
	if !m.inHelp {
		t.Fatal("pressing '?' should open the help")
	}
	if cmd != nil {
		t.Fatal("opening help should not return commands")
	}

	// The help view has the required sections
	allLines := strings.Join(m.helpLines(100), "\n")
	for _, sec := range []string{"KEYS", "STATUSES", "QUOTAS", "WHAT THINGS MEAN"} {
		if !strings.Contains(allLines, sec) {
			t.Fatalf("the help should contain the section %q", sec)
		}
	}

	// 2. Closing with 'q' does NOT quit (cmd == nil, inHelp == false)
	mod, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = mod.(Model)
	if m.inHelp {
		t.Fatal("'q' should close the help")
	}
	if cmd != nil {
		t.Fatal("'q' in help should NOT quit or return tea.Quit")
	}

	// 3. Open again and close with 'esc'
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mod.(Model)
	if !m.inHelp {
		t.Fatal("'?' should open the help")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mod.(Model)
	if m.inHelp {
		t.Fatal("'esc' should close the help")
	}

	// 4. Open again and close with '?'
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mod.(Model)
	if !m.inHelp {
		t.Fatal("'?' should open the help")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mod.(Model)
	if m.inHelp {
		t.Fatal("'?' inside help should close it")
	}

	// 5. From history: opening and closing help keeps inHistory
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = mod.(Model)
	if !m.inHistory {
		t.Fatal("should be in history")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mod.(Model)
	if !m.inHelp || !m.inHistory {
		t.Fatal("should be in help, remembering it came from history")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = mod.(Model)
	if m.inHelp || !m.inHistory {
		t.Fatal("closing help should go back to history")
	}

	// 6. From report: opening and closing help keeps inReport
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}}) // leaves history
	m = mod.(Model)
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = mod.(Model)
	if !m.inReport {
		t.Fatal("should be in report")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mod.(Model)
	if !m.inHelp || !m.inReport {
		t.Fatal("help should open from report")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mod.(Model)
	if m.inHelp || m.inReport {
		t.Fatal("closing help with esc should go back to the dashboard")
	}
}

func TestHelpScrolling(t *testing.T) {
	m := New(nil, 2*time.Second)
	m.height = 20
	m.inHelp = true

	// Down with down / j
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mod2 := m2.(Model)
	if mod2.helpOffset != 1 {
		t.Fatalf("after down, helpOffset should be 1, got %d", mod2.helpOffset)
	}

	// Up with up / k
	m3, _ := mod2.Update(tea.KeyMsg{Type: tea.KeyUp})
	mod3 := m3.(Model)
	if mod3.helpOffset != 0 {
		t.Fatalf("after up, helpOffset should be 0, got %d", mod3.helpOffset)
	}

	// To the end with end / G
	m4, _ := mod3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	mod4 := m4.(Model)
	if mod4.helpOffset == 0 {
		t.Fatal("'G' should move helpOffset to the end")
	}

	// To the top with home / g
	m5, _ := mod4.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	mod5 := m5.(Model)
	if mod5.helpOffset != 0 {
		t.Fatalf("'g' should reset helpOffset to 0, got %d", mod5.helpOffset)
	}
}

// The position counter sits on the title line, on the right, and scrolling
// to the end shows the help's last line.
func TestHelpTitleAndScrollEnd(t *testing.T) {
	m := Model{width: 100, height: 40, inHelp: true}
	v := reANSI.ReplaceAllString(m.helpView(), "")
	ls := strings.Split(v, "\n")
	if !strings.HasPrefix(ls[2], " HELP") || !strings.Contains(ls[2], "for more") {
		t.Fatalf("title line = %q", ls[2])
	}
	r, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	v = reANSI.ReplaceAllString(r.(Model).helpView(), "")
	all := m.helpLines(100)
	last := strings.TrimSpace(reANSI.ReplaceAllString(all[len(all)-1], ""))
	if last == "" {
		last = strings.TrimSpace(reANSI.ReplaceAllString(all[len(all)-2], ""))
	}
	if !strings.Contains(v, last) {
		t.Fatalf("with G the last line %q is not visible:\n%s", last, v)
	}
}
