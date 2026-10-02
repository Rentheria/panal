package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

type testReader struct {
	row state.Row
}

func (l testReader) Agent() string   { return l.row.Agent }
func (l testReader) Read() state.Row { return l.row }

func TestReportKeys(t *testing.T) {
	ls := []readers.Reader{
		testReader{row: state.Row{Agent: "claude", Status: state.Working}},
	}
	m := New(ls, time.Second)

	// The 'i' key opens the report from the cards (7 days by default).
	mod, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = mod.(Model)
	if !m.inReport {
		t.Fatal("pressing 'i' should open the report")
	}
	if m.reportDaysOrDefault() != 7 {
		t.Fatalf("the initial period should be 7 days, got %d", m.reportDaysOrDefault())
	}

	// The 'right' key switches to 30 days.
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = mod.(Model)
	if m.reportDaysOrDefault() != 30 {
		t.Fatalf("after 'right' the period should be 30 days, got %d", m.reportDaysOrDefault())
	}

	// The '7' key goes back to 7 days.
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m = mod.(Model)
	if m.reportDaysOrDefault() != 7 {
		t.Fatalf("after '7' the period should be 7 days, got %d", m.reportDaysOrDefault())
	}

	// The 'tab' key toggles between 7 and 30.
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = mod.(Model)
	if m.reportDaysOrDefault() != 30 {
		t.Fatalf("tab should toggle to 30 days, got %d", m.reportDaysOrDefault())
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = mod.(Model)
	if m.reportDaysOrDefault() != 7 {
		t.Fatalf("tab should toggle to 7 days, got %d", m.reportDaysOrDefault())
	}

	// The 'esc' key goes back to the cards.
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mod.(Model)
	if m.inReport {
		t.Fatal("esc should close the report")
	}

	// 'i' opens it again and another 'i' closes it.
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = mod.(Model)
	if !m.inReport {
		t.Fatal("'i' should open the report")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = mod.(Model)
	if m.inReport {
		t.Fatal("'i' inside the report should close it")
	}

	// From history: opening the report and pressing esc goes to the dashboard.
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = mod.(Model)
	if !m.inHistory {
		t.Fatal("should be in history")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = mod.(Model)
	if !m.inReport {
		t.Fatal("should be in the report")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mod.(Model)
	if m.inReport || m.inHistory {
		t.Fatal("esc should go back to the dashboard")
	}

	// From the compact table: opening the report and closing it with 'i' keeps the table.
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}}) // leaves history
	m = mod.(Model)
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}}) // table
	m = mod.(Model)
	if !m.compact {
		t.Fatal("should be in the compact table")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = mod.(Model)
	if !m.inReport {
		t.Fatal("the report should open from the table")
	}
	mod, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = mod.(Model)
	if m.inReport || !m.compact {
		t.Fatal("'i' should go back to the compact table")
	}
}

func TestReportBarColor(t *testing.T) {
	// Thresholds: green from 80 %, amber from 50 %, red below.
	greenCases := []int{80, 85, 100}
	for _, p := range greenCases {
		if c := reportBarColor(p); c != cGreen {
			t.Errorf("for %d%% expected green (%v), got %v", p, cGreen, c)
		}
	}

	amberCases := []int{50, 60, 79}
	for _, p := range amberCases {
		if c := reportBarColor(p); c != cAmber {
			t.Errorf("for %d%% expected amber (%v), got %v", p, cAmber, c)
		}
	}

	redCases := []int{0, 10, 49}
	for _, p := range redCases {
		if c := reportBarColor(p); c != cRed {
			t.Errorf("for %d%% expected red (%v), got %v", p, cRed, c)
		}
	}
}

func TestReportWidth80(t *testing.T) {
	fixedTime := time.Date(2026, 9, 26, 15, 4, 5, 0, time.Local)
	oldNow := nowFn
	nowFn = func() time.Time { return fixedTime }
	defer func() { nowFn = oldNow }()

	ls := []readers.Reader{
		testReader{row: state.Row{Agent: "claude", Status: state.Working}},
		testReader{row: state.Row{Agent: "agy", Status: state.Working}},
		testReader{row: state.Row{Agent: "codex", Status: state.OutOfQuota}},
		testReader{row: state.Row{Agent: "opencode", Status: state.Idle}},
	}
	m := New(ls, time.Second)
	m.now = fixedTime
	m.width = 80
	m.height = 40

	// Fill runs with varied data (tokens, costs, failures, out of quota, done).
	m.runs = []history.Run{
		{
			Stamp:  "c1",
			Agent:  "claude",
			Status: runDone,
			Start:  fixedTime.Add(-2 * time.Hour),
			End:    fixedTime.Add(-1 * time.Hour),
			Tokens: 125000,
			Cost:   "10 cr",
			Dir:    "C:/test/claude",
		},
		{
			Stamp:  "c2",
			Agent:  "agy",
			Status: runFailed,
			Start:  fixedTime.Add(-4 * time.Hour),
			End:    fixedTime.Add(-3 * time.Hour),
			Tokens: 50000,
			Dir:    "C:/test/agy",
		},
		{
			Stamp:  "c3",
			Agent:  "codex",
			Status: runOutOfQuota,
			Start:  fixedTime.Add(-10 * time.Hour),
			End:    fixedTime.Add(-9 * time.Hour),
			Cost:   "5 cr",
			Dir:    "C:/test/codex",
		},
		{
			Stamp:  "c4",
			Agent:  "opencode",
			Status: runDone,
			Start:  fixedTime.Add(-20 * time.Hour),
			End:    fixedTime.Add(-19 * time.Hour),
			Tokens: 8000,
			Dir:    "C:/test/opencode",
		},
	}

	m.inReport = true
	m.reportDays = 7

	output := m.View()

	// Height must not exceed 40.
	if h := lipgloss.Height(output); h > 40 {
		t.Errorf("the report screen is %d lines tall, 40 at most", h)
	}

	// No line may exceed 80 columns.
	plain := reANSI.ReplaceAllString(output, "")
	plain = strings.ReplaceAll(plain, "\r\n", "\n")
	for i, line := range strings.Split(plain, "\n") {
		if w := lipgloss.Width(line); w > 80 {
			t.Errorf("line %d is %d columns wide (> 80): %q", i+1, w, line)
		}
	}

	// Also check with no runs.
	m.runs = nil
	emptyOutput := m.View()
	if h := lipgloss.Height(emptyOutput); h > 40 {
		t.Errorf("the empty report is %d lines tall, 40 at most", h)
	}
	plainEmpty := reANSI.ReplaceAllString(emptyOutput, "")
	plainEmpty = strings.ReplaceAll(plainEmpty, "\r\n", "\n")
	for i, line := range strings.Split(plainEmpty, "\n") {
		if w := lipgloss.Width(line); w > 80 {
			t.Errorf("line %d of the empty report is %d columns wide (> 80): %q", i+1, w, line)
		}
	}
}

func TestCellBarFullAndEmpty(t *testing.T) {
	// 0% -> 0 █, 10 ░
	b0 := barCell(0, 20)
	if strings.Count(b0, "█") != 0 || strings.Count(b0, "░") != 10 {
		t.Fatalf("at 0%% expected 0 █ and 10 ░, got %q", b0)
	}

	// 50% -> 5 █, 5 ░
	b50 := barCell(50, 20)
	if strings.Count(b50, "█") != 5 || strings.Count(b50, "░") != 5 {
		t.Fatalf("at 50%% expected 5 █ and 5 ░, got %q", b50)
	}

	// 100% -> 10 █, 0 ░
	b100 := barCell(100, 20)
	if strings.Count(b100, "█") != 10 || strings.Count(b100, "░") != 0 {
		t.Fatalf("at 100%% expected 10 █ and 0 ░, got %q", b100)
	}
}
