package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/changes"
	"github.com/AlbertoVasquezR/panal/internal/credits"
	"github.com/AlbertoVasquezR/panal/internal/forecast"
	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

// A run that goes from running to done alerts once; the first refresh alerts
// nothing (on start, old news isn't news).
func TestDetectAlerts(t *testing.T) {
	now := time.Now()
	m := Model{now: now}
	m.runs = []history.Run{
		{Stamp: "1", Agent: "agy", Status: runRunning, Start: now.Add(-time.Minute)},
		{Stamp: "0", Agent: "codex", Status: runDone, Start: now.Add(-time.Hour), End: now.Add(-50 * time.Minute)},
	}
	m.rows = []state.Row{{Agent: "agy", Status: state.Working}}
	if a := m.detectAlerts(); len(a) != 0 {
		t.Fatalf("the first refresh should not alert: %v", a)
	}
	m.runs[0].Status, m.runs[0].End = runDone, now
	m.runs[0].Task = "You work in x. Create hello.txt."
	m.rows[0].Status = state.Stuck
	a := m.detectAlerts()
	if len(a) != 2 || !strings.Contains(a[0].title, "agy ✔ done") || a[0].text != "Create hello.txt." ||
		!strings.Contains(a[1].title, "got stuck") {
		t.Fatalf("alerts: %+v", a)
	}
	if a := m.detectAlerts(); len(a) != 0 {
		t.Fatalf("should not repeat: %v", a)
	}
	// A new run that started and ended between two refreshes alerts too.
	m.runs = append(m.runs, history.Run{Stamp: "2", Agent: "codex", Status: runOutOfQuota, Start: now, End: now})
	if a := m.detectAlerts(); len(a) != 1 || !strings.Contains(a[0].title, "codex") {
		t.Fatalf("quick run: %v", a)
	}
}

func TestHistoryFilters(t *testing.T) {
	m := Model{width: 120, height: 40, now: time.Now(), inHistory: true}
	m.runs = []history.Run{
		{Agent: "agy", Status: runFailed, Task: "Fix the Login"},
		{Agent: "agy", Status: runDone, Task: "poem"},
		{Agent: "codex", Status: runOutOfQuota, Task: "login again"},
		{Agent: "codex", Status: runTimedOut, Task: "x", Dir: `C:\wt\login`},
	}
	m.historyKey("e") // failures
	if n := len(m.filteredRuns()); n != 2 {
		t.Fatalf("failures: %d, want 2", n)
	}
	m.historyKey("e") // out of quota/permission
	if cs := m.filteredRuns(); len(cs) != 1 || cs[0].Status != runOutOfQuota {
		t.Fatalf("quota: %v", cs)
	}
	m.historyKey("esc")
	m.historyKey("/")
	for _, r := range "LOGIN" {
		m.searchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if n := len(m.filteredRuns()); n != 3 {
		t.Fatalf("search 'LOGIN' (task and dir, case-insensitive): %d, want 3", n)
	}
	if !strings.Contains(m.View(), "/ LOGIN") {
		t.Fatal("the search should be visible while typing")
	}
	m.searchKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.historyKey("f") // claude: none
	if n := len(m.filteredRuns()); n != 0 {
		t.Fatalf("agent filter + search: %d", n)
	}
	m.historyKey("esc")
	if m.query != "" || m.historyFilter != "" || len(m.filteredRuns()) != 4 {
		t.Fatal("esc should clear all filters")
	}
}

func TestLogViewer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log.txt")
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("I0925 14:17:25.522742       1 resolver.go:85] noise\n")
		b.WriteString("useful line \x1b[32mwith color\x1b[0m\n")
	}
	os.WriteFile(p, []byte(b.String()), 0o644)
	v := newViewer(history.Run{Agent: "agy", Log: p, Status: runRunning})
	if n := len(v.visible()); n != 50 {
		t.Fatalf("without noise: %d lines, want 50", n)
	}
	if strings.Contains(v.visible()[0], "\x1b") {
		t.Fatal("the log's colors should be stripped")
	}
	v.key("r", 10)
	if n := len(v.visible()); n != 100 {
		t.Fatalf("with noise: %d, want 100", n)
	}
	v.key("r", 10)
	// Going up stops following; going to the end follows again.
	v.key("up", 10)
	if v.follow {
		t.Fatal("going up should stop following")
	}
	v.key("G", 10)
	if !v.follow {
		t.Fatal("going to the end should follow again")
	}
	// The file grows: reloading sees it.
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("new at the end\n")
	f.Close()
	v.reload()
	if ls := v.visible(); ls[len(ls)-1] != "new at the end" {
		t.Fatalf("did not re-read: %q", ls[len(ls)-1])
	}
	if !v.key("down", 10) || v.key("esc", 10) {
		t.Fatal("esc closes, nothing else does")
	}
	m := Model{width: 80, height: 20, reg: v}
	if h := strings.Count(m.View(), "\n") + 1; h > 20 {
		t.Fatalf("the viewer is %d lines tall in 20", h)
	}
}

func TestSpendText(t *testing.T) {
	txt, bad := spendText(credits.Usage{HasData: true, Today: 2.36, PerDay: 14.02, DaysLeft: 71, Balance: 996})
	if txt != "today 2.4 cr · ~14/day · ~71 days" || bad {
		t.Fatalf("%q bad=%v", txt, bad)
	}
	if _, bad := spendText(credits.Usage{HasData: true, PerDay: 100, DaysLeft: 3}); !bad {
		t.Fatal("3 days of balance should be flagged")
	}
}

func TestColorizeDiff(t *testing.T) {
	// + should be green
	plus := colorizeDiff("+new line")
	if plus != lipgloss.NewStyle().Foreground(cGreen).Render("+new line") {
		t.Errorf("'+' line is not green: %q", plus)
	}

	// - should be red
	minus := colorizeDiff("-old line")
	if minus != lipgloss.NewStyle().Foreground(cRed).Render("-old line") {
		t.Errorf("'-' line is not red: %q", minus)
	}

	// +++ and --- should be dim (neither green nor red)
	plusPlus := colorizeDiff("+++ b/file.go")
	if plusPlus != lipgloss.NewStyle().Foreground(cDim).Render("+++ b/file.go") {
		t.Errorf("'+++' line is not dim: %q", plusPlus)
	}
	minusMinus := colorizeDiff("--- a/file.go")
	if minusMinus != lipgloss.NewStyle().Foreground(cDim).Render("--- a/file.go") {
		t.Errorf("'---' line is not dim: %q", minusMinus)
	}

	// @@ should be cyan
	hunk := colorizeDiff("@@ -1,3 +1,4 @@")
	if hunk != lipgloss.NewStyle().Foreground(cCyan).Render("@@ -1,3 +1,4 @@") {
		t.Errorf("'@@' line is not cyan: %q", hunk)
	}

	// diff --git and commit should be bold
	gitDiff := colorizeDiff("diff --git a/x b/x")
	if gitDiff != lipgloss.NewStyle().Bold(true).Render("diff --git a/x b/x") {
		t.Errorf("'diff --git' line is not bold: %q", gitDiff)
	}
	commitLine := colorizeDiff("commit 1234567890")
	if commitLine != lipgloss.NewStyle().Bold(true).Render("commit 1234567890") {
		t.Errorf("'commit ' line is not bold: %q", commitLine)
	}

	// a normal line stays unchanged
	normal := colorizeDiff("a normal line")
	if normal != "a normal line" {
		t.Errorf("a normal line should have no styles: %q", normal)
	}
}

func TestKeyDOpensAndCloses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	d := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = d
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(d, "a.txt")
	os.WriteFile(f, []byte("base line\n"), 0o644)
	cmd = exec.Command("git", "-C", d, "add", ".")
	cmd.Run()
	cmd = exec.Command("git", "-C", d, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base")
	cmd.Run()

	// Change the file during the run
	os.WriteFile(f, []byte("base line changed\n"), 0o644)

	now := time.Now()
	c := history.Run{
		Agent:  "agy",
		Dir:    d,
		Start:  now.Add(-time.Minute),
		End:    now.Add(time.Minute),
		Status: runDone,
	}

	m := Model{
		width:     100,
		height:    30,
		now:       now,
		changes:   &changes.Cache{},
		runs:      []history.Run{c},
		inHistory: true,
	}

	// Open with 'd'
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mod2 := m2.(Model)
	if mod2.reg == nil {
		t.Fatalf("the 'd' key should open the diff, alert: %q", mod2.alert)
	}
	if !mod2.reg.diff {
		t.Fatal("the viewer should be in diff mode")
	}
	view := mod2.View()
	if !strings.Contains(view, "DIFF · agy") {
		t.Fatalf("the view should have the title DIFF · agy: %s", view)
	}
	if !strings.Contains(view, "esc") || !strings.Contains(view, "back") {
		t.Fatal("the diff footer should contain 'esc back'")
	}
	if strings.Contains(view, "noise") {
		t.Fatal("the diff footer should not contain 'noise'")
	}

	// In diff mode 'r' should do nothing
	if !mod2.reg.key("r", 10) {
		t.Fatal("'r' should not close the diff viewer")
	}

	// Close with 'd'
	m3, _ := mod2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	mod3 := m3.(Model)
	if mod3.reg != nil {
		t.Fatal("the 'd' key should close the diff viewer")
	}

	// No run or no changes: it should show a short alert
	mWithout := Model{now: time.Now()}
	mWithout2, _ := mWithout.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	modWithout := mWithout2.(Model)
	if modWithout.reg != nil || modWithout.alert == "" {
		t.Fatal("with no run it should show a short alert and not open the viewer")
	}
}

func TestQuotaAlertOnlyOnce(t *testing.T) {
	now := time.Date(2026, 9, 26, 15, 0, 0, 0, time.Local)
	m := Model{now: now}
	m.rows = []state.Row{
		{
			Agent:  "agy",
			Status: state.Working,
			Quota: state.Quota{
				Exact: true,
				Bars: []state.Bar{
					{
						Name:       "5h",
						UsedPct:    80,
						ResetsAt:   time.Date(2026, 9, 26, 17, 0, 0, 0, time.Local),
						ExhaustsAt: time.Date(2026, 9, 26, 15, 40, 0, 0, time.Local),
					},
					{
						Name:       "sem",
						UsedPct:    85,
						ExhaustsAt: time.Date(2026, 9, 26, 15, 40, 0, 0, time.Local),
					},
				},
			},
		},
	}

	alertList := m.detectAlerts()
	if len(alertList) != 2 {
		t.Fatalf("expected 2 quota alerts, got %d (%+v)", len(alertList), alertList)
	}

	// With a reset
	if alertList[0].title != "agy: quota running out" || alertList[0].text != "5h runs out ~15:40, resets 17:00" {
		t.Fatalf("wrong alert with a reset: %+v", alertList[0])
	}
	// Without a reset
	if alertList[1].title != "agy: quota running out" || alertList[1].text != "sem runs out ~15:40" {
		t.Fatalf("wrong alert without a reset: %+v", alertList[1])
	}

	// Second refresh: should not repeat
	if a2 := m.detectAlerts(); len(a2) != 0 {
		t.Fatalf("the alert should not repeat: %+v", a2)
	}

	// New reset cycle: it should alert again
	m.rows[0].Quota.Bars[0].ResetsAt = time.Date(2026, 9, 26, 22, 0, 0, 0, time.Local)
	m.rows[0].Quota.Bars[0].ExhaustsAt = time.Date(2026, 9, 26, 15, 50, 0, 0, time.Local)
	a3 := m.detectAlerts()
	if len(a3) != 1 || a3[0].text != "5h runs out ~15:50, resets 22:00" {
		t.Fatalf("a changed reset should alert again: %+v", a3)
	}
}

type fakeReader struct {
	row state.Row
}

func (l *fakeReader) Agent() string { return l.row.Agent }

func (l *fakeReader) Read() state.Row { return l.row }

func TestProjectionNotAfterReset(t *testing.T) {
	now := time.Now()
	t0 := now.Add(-20 * time.Minute)
	t1 := now.Add(-10 * time.Minute)

	// Bar "before": rises 1%/min, at t=now (70%) it runs out at +30m (now + 30m).
	// It resets at now + 45m -> runs out before the reset, ExhaustsAt is set.
	// Bar "after": rises 1%/min, runs out at +30m (now + 30m).
	// It resets at now + 15m -> runs out after the reset, ExhaustsAt is not set.
	// Bar "noreset": rises 1%/min, ResetsAt is zero -> ExhaustsAt is set.
	lf := &fakeReader{
		row: state.Row{
			Agent:  "agy",
			Status: state.Working,
			Quota: state.Quota{
				Exact: true,
				Bars: []state.Bar{
					{Name: "before", UsedPct: 70, ResetsAt: now.Add(45 * time.Minute)},
					{Name: "after", UsedPct: 70, ResetsAt: now.Add(15 * time.Minute)},
					{Name: "noreset", UsedPct: 70},
				},
			},
		},
	}

	m := Model{
		readers: []readers.Reader{lf},
		sampler: forecast.New(),
	}

	// Earlier samples in the sampler
	m.sampler.Record("agy/before", t0, 50)
	m.sampler.Record("agy/after", t0, 50)
	m.sampler.Record("agy/noreset", t0, 50)

	m.sampler.Record("agy/before", t1, 60)
	m.sampler.Record("agy/after", t1, 60)
	m.sampler.Record("agy/noreset", t1, 60)

	m.refresh()

	if len(m.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(m.rows))
	}
	bars := m.rows[0].Quota.Bars
	if len(bars) != 3 {
		t.Fatalf("expected 3 bars, got %d", len(bars))
	}

	if bars[0].ExhaustsAt.IsZero() {
		t.Fatal("bar 'before' should have ExhaustsAt set")
	}
	if !bars[1].ExhaustsAt.IsZero() {
		t.Fatalf("bar 'after' should not have ExhaustsAt: it runs out (%v) after the reset (%v)",
			bars[1].ExhaustsAt, bars[1].ResetsAt)
	}
	if bars[2].ExhaustsAt.IsZero() {
		t.Fatal("bar 'noreset' should have ExhaustsAt set with a zero ResetsAt")
	}
}
