package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/runs"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

// ------------------------------------------------------------- timeline --
//
// Tab 4: one line per agent with its runs of the day on an hour axis, to see
// at a glance who worked when, what failed and where it ran out of quota.
// ← → change the day.

// What is drawn in each cell, from least to most important: if the same slot
// has a run that finished and one that failed, the failure wins.
type timelineMark int

const (
	markEmpty timelineMark = iota
	markDone
	markRunning
	markQuota
	markFailed
)

func markOf(runStatus runs.Status) timelineMark {
	switch runStatus {
	case runDone:
		return markDone
	case runRunning:
		return markRunning
	case runOutOfQuota, runNoPermission:
		return markQuota
	case runSkipped:
		return markEmpty
	default:
		return markFailed
	}
}

// Glyph and color of each mark: the shape also says what happened, so it
// reads without color.
func paintMark(mk timelineMark, agent string) string {
	switch mk {
	case markDone:
		return lipgloss.NewStyle().Foreground(agentColor(agent)).Render("█")
	case markRunning:
		return lipgloss.NewStyle().Foreground(agentColor(agent)).Render("▓")
	case markQuota:
		return lipgloss.NewStyle().Foreground(cAmber).Render("░")
	case markFailed:
		return lipgloss.NewStyle().Foreground(cRed).Render("▚")
	}
	return lipgloss.NewStyle().Foreground(cEmpty).Render("·")
}

// timelineRange: the hours the day's axis spans. By default 8 to 18 (or
// until now, if it is today), widened so all the runs fit.
func timelineRange(cs []history.Run, day, now time.Time) (from, to time.Time) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	end := start.Add(24 * time.Hour)
	from, to = start.Add(8*time.Hour), start.Add(18*time.Hour)
	today := !now.Before(start) && now.Before(end)
	if today && now.After(to) {
		to = now
	}
	for _, c := range cs {
		f := runEnd(c, now)
		if c.Start.Before(from) {
			from = c.Start
		}
		if f.After(to) {
			to = f
		}
	}
	if from.Before(start) {
		from = start
	}
	if to.After(end) {
		to = end
	}
	// To the local hour on the dot (Truncate works in UTC and is off in
	// half-hour time zones).
	onTheDot := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	}
	from = onTheDot(from)
	if t := onTheDot(to); t.Before(to) {
		to = t.Add(time.Hour)
	}
	return from, to
}

func runEnd(c history.Run, now time.Time) time.Time {
	if c.End.IsZero() || c.Status == runRunning {
		return now
	}
	return c.End
}

// runsOfDay: the runs that overlap that day.
func runsOfDay(cs []history.Run, day, now time.Time) []history.Run {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	end := start.Add(24 * time.Hour)
	var out []history.Run
	for _, c := range cs {
		if c.Start.IsZero() || c.Status == runSkipped {
			continue
		}
		if c.Start.Before(end) && runEnd(c, now).After(start) {
			out = append(out, c)
		}
	}
	return out
}

// timelineTrack draws an agent's runs in n cells between from and to.
func timelineTrack(cs []history.Run, agent string, from, to, now time.Time, n int, nowCol int) string {
	marks := make([]timelineMark, n)
	step := to.Sub(from) / time.Duration(n)
	for _, c := range cs {
		if c.Agent != agent {
			continue
		}
		mk := markOf(c.Status)
		start, end := c.Start, runEnd(c, now)
		a := int(start.Sub(from) / step)
		b := int(end.Sub(from) / step)
		for i := max(a, 0); i <= min(b, n-1); i++ {
			if mk > marks[i] {
				marks[i] = mk
			}
		}
	}
	var sb strings.Builder
	for i, mk := range marks {
		if mk == markEmpty && i == nowCol {
			sb.WriteString(lipgloss.NewStyle().Foreground(cDim).Render("│"))
			continue
		}
		sb.WriteString(paintMark(mk, agent))
	}
	return sb.String()
}

// timelineAxis: the hours in their columns, every hour or every 2 hours if
// they don't fit.
func timelineAxis(from, to time.Time, n int) string {
	hours := int(to.Sub(from) / time.Hour)
	every := 1
	for hours > 0 && n/(hours/every) < 4 {
		every++
	}
	axis := []rune(strings.Repeat(" ", n+2))
	for h := 0; h <= hours; h += every {
		col := h * n / max(hours, 1)
		txt := []rune(from.Add(time.Duration(h) * time.Hour).Format("15"))
		if col+len(txt) > len(axis) {
			break
		}
		copy(axis[col:], txt)
	}
	return strings.TrimRight(string(axis), " ")
}

const timelineLabelWidth = 10

// Timeline draws the tab's body for that day.
func Timeline(runs []history.Run, day, now time.Time, width int) string {
	dimS := lipgloss.NewStyle().Foreground(cDim)
	cs := runsOfDay(runs, day, now)
	var b strings.Builder
	if len(cs) == 0 {
		b.WriteString(dimS.Render("  no runs that day · ← → to see another") + "\n")
		return b.String()
	}
	from, to := timelineRange(cs, day, now)
	n := max(width-2-timelineLabelWidth-2, 12)
	nowCol := -1
	if !now.Before(from) && now.Before(to) {
		nowCol = int(now.Sub(from) * time.Duration(n) / to.Sub(from))
	}

	b.WriteString(strings.Repeat(" ", 2+timelineLabelWidth) + dimS.Render(timelineAxis(from, to, n)) + "\n")
	agents := append([]string(nil), agentOrder...)
	for _, c := range cs { // any agent not in the fixed list
		newer := true
		for _, a := range agents {
			newer = newer && a != c.Agent
		}
		if newer {
			agents = append(agents, c.Agent)
		}
	}
	for _, a := range agents {
		lbl := cell(a, timelineLabelWidth, lipgloss.NewStyle().Foreground(agentColor(a)).Bold(true))
		b.WriteString("  " + lbl + timelineTrack(cs, a, from, to, now, n, nowCol) + "\n")
	}
	b.WriteString("\n")

	sep := "   "
	if width < 80 {
		sep = "  "
	}
	legend := "  " + paintMark(markDone, "claude") + dimS.Render(" done"+sep) +
		paintMark(markRunning, "claude") + dimS.Render(" running"+sep) +
		paintMark(markQuota, "") + dimS.Render(" out of quota"+sep) +
		paintMark(markFailed, "") + dimS.Render(" failed")
	if nowCol >= 0 {
		legend += dimS.Render(sep + "│ now")
	}
	b.WriteString(legend + "\n\n")

	// Day summary: how many, how much agent time and the longest one.
	var total, longD time.Duration
	var long history.Run
	failures := 0
	for _, c := range cs {
		d := runEnd(c, now).Sub(c.Start)
		total += d
		if d > longD {
			long, longD = c, d
		}
		if markOf(c.Status) == markFailed {
			failures++
		}
	}
	res := fmt.Sprintf("  %s · %s of agent work", plural(len(cs), "run"), readers.Ago(total))
	if failures > 0 {
		res += fmt.Sprintf(" · %d failed", failures)
	}
	res += fmt.Sprintf(" · longest: %s, %s", long.Agent, readers.Ago(longD))
	b.WriteString(dimS.Render(truncate(res, width)) + "\n")
	return b.String()
}

// dayName: "today", "yesterday" or "Thursday 24 Sep".
func dayName(day, now time.Time) string {
	d := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()) }
	// Rounded: a day with a DST change lasts 23 or 25 h.
	switch int(math.Round(d(now).Sub(d(day)).Hours() / 24)) {
	case 0:
		return "today"
	case 1:
		return "yesterday"
	}
	return day.Format("Monday 02 Jan")
}

func (m Model) timelineView() string {
	width := max(m.width, 40)
	now := m.now
	if now.IsZero() {
		now = nowFn()
	}
	day := now.AddDate(0, 0, -m.timelineDay)
	head := Header(m.rows, now, width, "timeline") + "\n" +
		tabBar("timeline", width) + "\n" +
		m.alertBand(width)
	footer := Footer(width, "←→", "other day", "esc", "back", "?", "help", "q", "quit")

	var b strings.Builder
	b.WriteString(head)
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("  TIMELINE · "+dayName(day, now)) + "\n\n")
	cs := m.runs
	if m.timelineDay > 0 && m.timelineAll != nil {
		cs = m.timelineAll
	}
	b.WriteString(Timeline(cs, day, now, width))
	return clipHeight(b.String(), m.screenHeight()-lipgloss.Height(footer)) + footer
}

// loadTimeline: for a past day all runs are read (m.runs only holds the latest
// 200, which on a busy day don't reach back to yesterday). It runs in Update,
// when the day changes, not while drawing.
func (m *Model) loadTimeline() {
	if m.timelineDay == 0 || m.hist == nil {
		m.timelineAll = nil
		return
	}
	if m.timelineAll == nil {
		m.timelineAll = m.hist.Read("", 0)
		m.closeOrphans(m.timelineAll)
	}
}

// closeOrphans: a run the history reports as running whose agent is no longer
// running (its process died without writing the end) is closed as interrupted at
// its last activity. Otherwise the timeline would draw it running until today.
// Not claude's: its card comes from the status line, not from a run file.
func (m *Model) closeOrphans(cs []history.Run) {
	for i := range cs {
		c := &cs[i]
		if c.Status != runRunning || c.Agent == "claude" || m.isRunning(*c) {
			continue
		}
		c.Status = runInterrupted
		c.End = readers.LatestActivity(c.Log, c.Start)
	}
}

func (m *Model) isRunning(c history.Run) bool {
	for _, f := range append(append([]state.Row(nil), m.rows...), m.offRows...) {
		alive := f.Status == state.Working || f.Status == state.Stuck
		if f.Agent == c.Agent && alive && (f.Start.IsZero() || absDur(f.Start.Sub(c.Start)) < 2*time.Second) {
			return true
		}
	}
	return false
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
