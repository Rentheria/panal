package ui

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/alerts"
	"github.com/AlbertoVasquezR/panal/internal/credits"
	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/mascots"
	"github.com/AlbertoVasquezR/panal/internal/runs"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

// -------------------------------------------------------------- credits --

func (m *Model) codexUsage() credits.Usage {
	if m.creditReader == nil {
		return credits.Usage{}
	}
	return credits.Summarize(m.creditReader.Points(nowFn()), nowFn())
}

// spendText: "today 2.4 cr · ~14/day · ~71 days". bad: at this pace it lasts
// less than a week.
func spendText(c credits.Usage) (string, bool) {
	t := "today " + figure(c.Today) + " cr"
	if c.PerDay > 0 {
		t += " · ~" + figure(c.PerDay) + "/day"
		t += fmt.Sprintf(" · ~%.0f days", c.DaysLeft)
	}
	return t, c.PerDay > 0 && c.DaysLeft < 7
}

func figure(x float64) string {
	if x < 10 {
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", x), "0"), ".")
	}
	return fmt.Sprintf("%.0f", x)
}

// --------------------------------------------------------------- alerts --

type alert struct{ title, text string }

// detectAlerts compares with the previous refresh: a run that stopped running
// (with how it broke the task, if it did) and an agent that got stuck. The
// first refresh only records the state: old news is not alerted on start.
func (m *Model) detectAlerts() []alert {
	first := m.seenRuns == nil
	if first {
		m.seenRuns = map[string]runs.Status{}
		m.statuses = map[string]state.Status{}
	}
	if m.quotaAlerted == nil {
		m.quotaAlerted = map[string]bool{}
	}
	var out []alert
	for _, c := range m.runs {
		keyID := c.Stamp + "+" + c.Agent
		before, seen := m.seenRuns[keyID]
		m.seenRuns[keyID] = c.Status
		if first || c.Status == runRunning || (seen && before != runRunning) {
			continue
		}
		// It finished since the previous refresh (or started and finished in between).
		if !seen && c.End.Before(m.now.Add(-time.Minute)) {
			continue
		}
		txt, _ := resultBadge(c.Status)
		a := alert{title: c.Agent + " " + txt, text: summarizeTask(c.Task)}
		if r, ok := m.checkChanges(c); ok && r.HasViolations() {
			a.title = "⚠ " + a.title + " — " + r.Warnings[0]
		}
		out = append(out, a)
	}
	all := append(append([]state.Row{}, m.rows...), m.offRows...)
	var mascotChanges []mascotChange
	for _, f := range all {
		before, seen := m.statuses[f.Agent]
		m.statuses[f.Agent] = f.Status
		if !first && seen {
			if r, ok := ReactionForChange(before, f.Status); ok && (r == mascots.WakeUp || m.justFinished(f.Agent, before)) {
				m.react(f.Agent, r)
				m.noteUnseen(f.Agent, r)
				mascotChanges = append(mascotChanges, mascotChange{f.Agent, r})
			}
		}
		if f.Times >= alertFrom && (f.Status == state.Working || f.Status == state.Stuck) {
			keyID := f.Agent + "\x00" + f.Repeating
			if m.repeatAlerted == nil {
				m.repeatAlerted = map[string]bool{}
			}
			if !m.repeatAlerted[keyID] {
				m.repeatAlerted[keyID] = true
				if !first { // on start, don't alert what was already going on
					out = append(out, alert{title: fmt.Sprintf("⟳ %s repeated the same thing %d times", f.Agent, f.Times), text: truncate(f.Repeating, 120)})
				}
			}
		}
		if !first && seen && before != state.Stuck && f.Status == state.Stuck {
			out = append(out, alert{title: "✖ " + f.Agent + " got stuck", text: summarizeTask(f.Task)})
		}
		for _, b := range f.Quota.Bars {
			if b.ExhaustsAt.IsZero() {
				continue
			}
			now := m.now
			if now.IsZero() {
				now = nowFn()
			}
			missing := b.ExhaustsAt.Sub(now)
			if missing <= 0 || missing >= 60*time.Minute {
				continue
			}
			// The reset is rounded to the hour: some CLIs report it with jittery
			// seconds and it would alert twice for the same window.
			keyID := fmt.Sprintf("%s+%s+%s", f.Agent, b.Name, b.ResetsAt.Round(time.Hour).Format(time.RFC3339))
			if m.quotaAlerted[keyID] {
				continue
			}
			m.quotaAlerted[keyID] = true
			txt := fmt.Sprintf("%s runs out ~%s", b.Name, b.ExhaustsAt.Local().Format("15:04"))
			if !b.ResetsAt.IsZero() {
				txt += fmt.Sprintf(", resets %s", b.ResetsAt.Local().Format("15:04"))
			}
			out = append(out, alert{
				title: fmt.Sprintf("%s: quota running out", f.Agent),
				text:  txt,
			})
		}
	}
	m.socialReactions(mascotChanges)
	m.syncUnseen()
	return out
}

func sendAlert(a alert) tea.Cmd {
	mode, outbound := AlertMode, OutboundAlerts
	return func() tea.Msg {
		alerts.Send(mode, "Panal · "+a.title, a.text)
		if mode != alerts.None {
			alerts.SendOutbound(outbound, "Panal · "+a.title, a.text)
		}
		return nil
	}
}

// alertBand: the latest alert, at the top, while it is fresh.
func (m Model) alertBand(width int) string {
	if m.alert == "" || m.now.Sub(m.alertTime) > alertTTL {
		return ""
	}
	c := cBlue
	if strings.HasPrefix(m.alert, "⚠") || strings.HasPrefix(m.alert, "✖") {
		c = cRed
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true).
		Render(truncate(" "+m.alertTime.Format("15:04")+"  "+m.alert, width)) + "\n"
}

// ------------------------------------------------------------------ log --

// logViewer shows a run's log or its diff full screen and follows it while it
// grows (if it comes from a file).
type logViewer struct {
	run    history.Run
	title  string   // if not empty, a fixed title instead of "LOG · agent"
	lines  []string // already cleaned (no colors) or given directly
	size   int64
	top    int  // first visible line
	follow bool // stuck to the end: moves on its own when new text arrives
	noise  bool // show agy's noise (Go log lines and traces)
	diff   bool // diff mode: colors lines and doesn't follow the file
	err    string
}

// How much of the end of the file is read: agy logs reach several MB.
const logTail = 1 << 20

var (
	reANSI  = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	reNoise = regexp.MustCompile(`^([IWEF]\d{4} \d\d:\d\d:\d\d\.\d+ |third_party/|\t?/?[\w./-]+\.go:\d+$)`)
	cCyan   = lipgloss.Color("#26C6DA")
)

func newViewer(c history.Run) *logViewer {
	v := &logViewer{run: c, follow: true}
	v.reload()
	return v
}

// newDiffViewer creates a viewer for a run's diff.
func newDiffViewer(c history.Run, text string) *logViewer {
	text = strings.ReplaceAll(text, "\r", "")
	ls := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return &logViewer{
		run:   c,
		title: "DIFF · " + c.Agent,
		lines: ls,
		diff:  true,
	}
}

// newTextViewer creates a viewer for fixed text with its title.
func newTextViewer(title string, lines []string, diff bool) *logViewer {
	return &logViewer{
		title: title,
		lines: lines,
		diff:  diff,
	}
}

// reload re-reads the end of the file if its size changed.
func (v *logViewer) reload() {
	if v.diff || v.run.Log == "" {
		return
	}
	st, err := os.Stat(v.run.Log)
	if err != nil {
		v.err = "can't read the log"
		return
	}
	if st.Size() == v.size && v.lines != nil {
		return
	}
	f, err := os.Open(v.run.Log)
	if err != nil {
		v.err = "can't read the log"
		return
	}
	defer f.Close()
	from := max(0, st.Size()-logTail)
	f.Seek(from, io.SeekStart)
	b, _ := io.ReadAll(f)
	text := strings.ReplaceAll(reANSI.ReplaceAllString(string(b), ""), "\r", "")
	ls := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if from > 0 && len(ls) > 1 {
		ls = ls[1:] // the first line arrived cut off
	}
	v.size, v.lines, v.err = st.Size(), ls, ""
}

func (v *logViewer) visible() []string {
	if v.diff || v.noise {
		return v.lines
	}
	var out []string
	for _, l := range v.lines {
		if !reNoise.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

// key moves the viewer; false if it should close.
func (v *logViewer) key(k string, height int) bool {
	n := len(v.visible())
	if v.follow {
		v.top = max(0, n-height) // the view sticks it to the end when drawing
	}
	switch k {
	case "esc", "q", "l":
		return false
	case "d":
		if v.diff {
			return false
		}
	case "up", "k":
		v.top--
	case "down", "j":
		v.top++
	case "pgup":
		v.top -= height
	case "pgdown", " ":
		v.top += height
	case "home", "g":
		v.top = 0
	case "end", "G":
		v.top = n
	case "r":
		if !v.diff {
			v.noise = !v.noise
			v.top = n
		}
	}
	v.top = min(max(0, v.top), max(0, n-height))
	// Scrolling to the end follows again; scrolling up lets go.
	if !v.diff {
		v.follow = v.top >= n-height
	}
	return true
}

func (m *Model) openLog() {
	var c history.Run
	ok := false
	if m.inHistory {
		if cs := m.filteredRuns(); m.historyCursor < len(cs) {
			c, ok = cs[m.historyCursor], true
		}
	} else if sel := min(m.table.Cursor(), len(m.rows)-1); sel >= 0 {
		c, ok = m.lastRun(m.rows[sel].Agent)
	}
	if !ok || c.Log == "" {
		m.alert, m.alertTime = "no log to show (Claude Code isn't run by panal delegate)", m.now
		return
	}
	m.reg = newViewer(c)
}

// logHeight: lines of text that fit between header and footer.
func (m Model) logHeight() int {
	return max(3, m.screenHeight()-4)
}

// colorizeDiff colors lines in diff mode by their prefix.
func colorizeDiff(s string) string {
	switch {
	case strings.HasPrefix(s, "+++") || strings.HasPrefix(s, "---"):
		return lipgloss.NewStyle().Foreground(cDim).Render(s)
	case strings.HasPrefix(s, "+"):
		return lipgloss.NewStyle().Foreground(cGreen).Render(s)
	case strings.HasPrefix(s, "-"):
		return lipgloss.NewStyle().Foreground(cRed).Render(s)
	case strings.HasPrefix(s, "@@"):
		return lipgloss.NewStyle().Foreground(cCyan).Render(s)
	case strings.HasPrefix(s, "diff --git") || strings.HasPrefix(s, "commit "):
		return lipgloss.NewStyle().Bold(true).Render(s)
	default:
		return s
	}
}

func (m Model) logViewView() string {
	v := m.reg
	width := max(m.width, 40)
	height := m.logHeight()
	ls := v.visible()
	if v.follow {
		v.top = max(0, len(ls)-height)
	}
	c := v.run
	title := "LOG · " + c.Agent
	if v.title != "" {
		title = v.title
	}
	titleCol := agentColor(c.Agent)
	if c.Agent == "" {
		titleCol = cBlue
	}
	head := lipgloss.NewStyle().Bold(true).Foreground(titleCol).Render(" " + title)
	if c.Status != "" {
		res, resColor := resultBadge(c.Status)
		head += lipgloss.NewStyle().Foreground(resColor).Render("  " + res)
	}
	sub := c.Log
	if v.diff {
		sub = c.Dir
	}
	if sub != "" {
		head += lipgloss.NewStyle().Foreground(cDim).Render("  " + truncateMiddle(sub, max(10, width-40)))
	}
	var b strings.Builder
	b.WriteString(head + "\n")
	statusStr := fmt.Sprintf(" %d–%d of %d", min(v.top+1, len(ls)), min(v.top+height, len(ls)), len(ls))
	if v.follow {
		statusStr += " · following"
	}
	if !v.noise && len(ls) < len(v.lines) {
		statusStr += fmt.Sprintf(" · %d noise lines hidden", len(v.lines)-len(ls))
	}
	if v.err != "" {
		statusStr = " " + v.err
	}
	b.WriteString(lipgloss.NewStyle().Foreground(cDim).Render(truncate(statusStr, width)) + "\n")
	end := min(len(ls), v.top+height)
	for _, l := range ls[min(v.top, end):end] {
		txt := truncate(strings.ReplaceAll(l, "\t", "    "), width)
		if v.diff {
			txt = colorizeDiff(txt)
		}
		b.WriteString(txt + "\n")
	}
	for i := end - v.top; i < height; i++ {
		b.WriteString("\n")
	}
	if v.diff {
		b.WriteString(Footer(width, "↑↓", "scroll", "PgUp/PgDn", "page", "g/G", "top/end", "esc", "back", "?", "help"))
	} else {
		b.WriteString(Footer(width, "↑↓", "scroll", "PgUp/PgDn", "page", "g/G", "top/end", "r", "noise", "esc", "back", "?", "help"))
	}
	return clipHeight(b.String(), m.screenHeight()-1) + ""
}
