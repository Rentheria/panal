package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/history"
)

func timelineRuns(day time.Time) []history.Run {
	h := func(hh, mm int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, time.Local)
	}
	return []history.Run{
		{Agent: "agy", Status: runDone, Start: h(9, 0), End: h(10, 30)},
		{Agent: "codex", Status: runFailed, Start: h(11, 0), End: h(11, 40)},
		{Agent: "opencode", Status: runOutOfQuota, Start: h(12, 0), End: h(12, 5)},
		{Agent: "codex", Status: runRunning, Start: h(15, 0)},
		{Agent: "agy", Status: runDone, Start: h(9, 0).AddDate(0, 0, -1), End: h(9, 30).AddDate(0, 0, -1)},
	}
}

func TestTimeline(t *testing.T) {
	now := time.Date(2026, 9, 26, 16, 0, 0, 0, time.Local)
	cs := timelineRuns(now)
	for _, width := range []int{60, 80, 132} {
		txt := stripANSI(Timeline(cs, now, now, width))
		t.Logf("width %d:\n%s", width, txt)
		for _, l := range strings.Split(txt, "\n") {
			if w := lipgloss.Width(l); w > width {
				t.Errorf("width %d: line of %d columns: %q", width, w, l)
			}
		}
		for _, want := range []string{"08", "agy", "codex", "█", "▚", "░", "▓", "│ now", "4 runs"} {
			if !strings.Contains(txt, want) {
				t.Errorf("width %d: missing %q", width, want)
			}
		}
	}
	// Yesterday: only yesterday's run, and no "now" mark.
	yesterday := stripANSI(Timeline(cs, now.AddDate(0, 0, -1), now, 80))
	if !strings.Contains(yesterday, "1 run ·") || strings.Contains(yesterday, "│ now") {
		t.Errorf("yesterday:\n%s", yesterday)
	}
	if got := dayName(now.AddDate(0, 0, -1), now); got != "yesterday" {
		t.Errorf("dayName(yesterday) = %q", got)
	}
}

func TestKey4OpensTimeline(t *testing.T) {
	fixedTime := time.Date(2026, 9, 26, 15, 4, 5, 0, time.Local)
	oldNow := nowFn
	nowFn = func() time.Time { return fixedTime }
	defer func() { nowFn = oldNow }()

	m := New(setupTestEnv(t, fixedTime), 2*time.Second)
	for _, k := range []string{"2", "4"} {
		mod, _ := m.Update(keyMsg(k))
		m = mod.(Model)
	}
	if !m.inTimeline || m.inHistory {
		t.Fatalf("4 should open the timeline and close the history: inTimeline=%v inHistory=%v", m.inTimeline, m.inHistory)
	}
	if !strings.Contains(stripANSI(m.View()), "TIMELINE") {
		t.Fatal("the view is not the timeline")
	}
	mod, _ := m.Update(keyMsg("left"))
	m = mod.(Model)
	if m.timelineDay != 1 || !strings.Contains(stripANSI(m.View()), "yesterday") {
		t.Fatalf("← should go to yesterday (timelineDay=%d)", m.timelineDay)
	}
	mod, _ = m.Update(keyMsg("esc"))
	if mod.(Model).inTimeline {
		t.Fatal("esc should go back to the dashboard")
	}
}

var reAnsi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return reAnsi.ReplaceAllString(s, "") }
