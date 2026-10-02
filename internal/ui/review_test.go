package ui

// Tests for the fixes from the 28-Sep-2026 review.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

func TestOrphanRunIsClosed(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	start := now.Add(-30 * time.Hour)
	m := New(nil, time.Second)
	m.now = now
	m.rows = []state.Row{{Agent: "codex", Status: state.Failed, Start: start}, {Agent: "agy", Status: state.Working, Start: now.Add(-time.Minute)}}
	cs := []history.Run{
		{Agent: "codex", Status: runRunning, Start: start},               // its process died
		{Agent: "agy", Status: runRunning, Start: now.Add(-time.Minute)}, // really running
	}
	m.closeOrphans(cs)
	if cs[0].Status != runInterrupted || cs[0].End.IsZero() {
		t.Fatalf("the orphan should be closed: %+v", cs[0])
	}
	if cs[1].Status != runRunning {
		t.Fatalf("the one really running keeps running: %+v", cs[1])
	}
	// It no longer fills the following days of the timeline.
	if txt := stripANSI(Timeline(cs[:1], now, now, 80)); !strings.Contains(txt, "no runs that day") {
		t.Fatalf("the run from two days ago should not show today:\n%s", txt)
	}
}

func TestRereadingQuotaDoesNotMarkUnseen(t *testing.T) {
	m := New(nil, time.Second)
	m.now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	m.runs = []history.Run{{Agent: "codex", Status: runOutOfQuota, End: m.now.Add(-5 * time.Hour)}}
	if m.justFinished("codex", state.Idle) {
		t.Fatal("idle to out of quota with no recent run: it only re-read the quota")
	}
	if !m.justFinished("codex", state.Working) {
		t.Fatal("if it was working, a run did end")
	}
	m.runs[0].End = m.now.Add(-time.Minute)
	if !m.justFinished("codex", state.Idle) {
		t.Fatal("a run that ended a minute ago (between two refreshes) counts")
	}
}

func TestEscInHelpLeavesTimeline(t *testing.T) {
	m := New(nil, time.Second)
	for _, k := range []string{"4", "?", "esc"} {
		mod, _ := m.Update(keyMsg(k))
		m = mod.(Model)
	}
	if m.inTimeline || m.inHelp {
		t.Fatal("esc in the help goes back to the dashboard, from the timeline too")
	}
}

func TestDayNameWithDSTChange(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone database")
	}
	// 8 March 2026 lasts 23 h in New York (DST starts): midnight to midnight
	// is not 24 h.
	now := time.Date(2026, 3, 9, 10, 0, 0, 0, loc)
	if got := dayName(now.AddDate(0, 0, -1), now); got != "yesterday" {
		t.Fatalf("dayName = %q, want \"yesterday\"", got)
	}
}

func TestWelcomeDoesNotSplitColors(t *testing.T) {
	empty := isolateSources(t)
	m := New(nil, time.Second)
	m.sources = []sourceSeen{{}}
	m.sources[0].What, m.sources[0].Path, m.sources[0].Env = "delegated runs", empty+`\.panal\runs`, "PANAL_RUNS"
	for _, width := range []int{40, 60, 80} {
		for _, l := range strings.Split(m.welcomeView(width), "\n") {
			if w := lipgloss.Width(l); w > width {
				t.Errorf("width %d: line of %d columns: %q", width, w, stripANSI(l))
			}
			// A split color code leaves an ESC without its "m".
			if i := strings.LastIndex(l, "\x1b["); i >= 0 && !strings.Contains(l[i:], "m") {
				t.Errorf("width %d: split color code: %q", width, l)
			}
		}
	}
}
