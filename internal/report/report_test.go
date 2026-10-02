package report

import (
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/history"
)

func TestCompute_Counts(t *testing.T) {
	base := time.Date(2026, 9, 26, 10, 0, 0, 0, time.Local)

	cs := []history.Run{
		{
			Agent:  "codex",
			Status: "done",
			Start:  base,
			End:    base.Add(10 * time.Second),
			Tokens: 1000,
			Cost:   "4 cr",
			Task:   "task 1",
		},
		{
			Agent:  "codex",
			Status: "done",
			Start:  base.Add(1 * time.Minute),
			End:    base.Add(1*time.Minute + 20*time.Second),
			Tokens: 2500,
			Cost:   "6 cr",
			Task:   "task 2",
		},
		{
			Agent:  "codex",
			Status: "failed",
			Start:  base.Add(2 * time.Minute),
			End:    base.Add(2*time.Minute + 5*time.Second),
			Tokens: 300,
			Task:   "task 3",
		},
		{
			Agent:  "codex",
			Status: "timeout",
			Start:  base.Add(3 * time.Minute),
			End:    base.Add(4 * time.Minute),
			Task:   "task 4",
		},
		{
			Agent:  "codex",
			Status: "interrupted",
			Start:  base.Add(5 * time.Minute),
			End:    base.Add(5*time.Minute + 15*time.Second),
			Task:   "task 5",
		},
		{
			Agent:  "codex",
			Status: "out_of_quota",
			Start:  base.Add(6 * time.Minute),
			Task:   "task 6",
		},
		{
			Agent:  "codex",
			Status: "no_permission",
			Start:  base.Add(7 * time.Minute),
			Task:   "task 7",
		},
		{
			Agent:  "codex",
			Status: "skipped",
			Start:  base.Add(8 * time.Minute),
			Task:   "task 8",
		},
		{
			// Running ("running") runs must be left out
			Agent:  "codex",
			Status: "running",
			Start:  base.Add(9 * time.Minute),
			Task:   "task 9",
		},
	}

	violated := func(c history.Run) bool {
		return c.Task == "task 2" || c.Task == "task 3"
	}

	ps := Compute(cs, base, violated)
	if len(ps) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(ps))
	}

	p := ps[0]
	if p.Agent != "codex" {
		t.Errorf("agent: expected codex, got %s", p.Agent)
	}
	if p.Runs != 8 {
		t.Errorf("runs: expected 8 (leaving out running ones), got %d", p.Runs)
	}
	if p.Finished != 2 {
		t.Errorf("finished: expected 2, got %d", p.Finished)
	}
	if p.Failed != 3 {
		t.Errorf("failed: expected 3, got %d", p.Failed)
	}
	if p.OutOfQuota != 3 {
		t.Errorf("out of quota: expected 3, got %d", p.OutOfQuota)
	}
	if p.Violations != 2 {
		t.Errorf("violations: expected 2, got %d", p.Violations)
	}
	if p.Tokens != 3800 {
		t.Errorf("tokens: expected 3800, got %d", p.Tokens)
	}
	if p.Credits != 10 {
		t.Errorf("credits: expected 10, got %d", p.Credits)
	}
	if p.Total != 30*time.Second {
		t.Errorf("total duration: expected 30s, got %v", p.Total)
	}
	if p.Median != 15*time.Second {
		t.Errorf("median: expected 15s, got %v", p.Median)
	}
}

func TestCompute_Median(t *testing.T) {
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)

	// Odd: 3 durations (10s, 50s, 30s) -> median 30s
	csOdd := []history.Run{
		{Agent: "agy", Status: "done", Start: base, End: base.Add(10 * time.Second)},
		{Agent: "agy", Status: "done", Start: base.Add(time.Minute), End: base.Add(time.Minute + 50*time.Second)},
		{Agent: "agy", Status: "done", Start: base.Add(2 * time.Minute), End: base.Add(2*time.Minute + 30*time.Second)},
	}
	ps := Compute(csOdd, base, nil)
	if len(ps) != 1 || ps[0].Median != 30*time.Second {
		t.Fatalf("odd median: expected 30s, got %v", ps[0].Median)
	}

	// Even: 4 durations
	csEven := append(csOdd, history.Run{
		Agent: "agy", Status: "done", Start: base.Add(3 * time.Minute), End: base.Add(3*time.Minute + 20*time.Second),
	})
	ps = Compute(csEven, base, nil)
	if len(ps) != 1 || ps[0].Median != 25*time.Second {
		// order: 10s, 20s, 30s, 50s -> (20+30)/2 = 25s
		t.Fatalf("even median: expected 25s, got %v", ps[0].Median)
	}

	// None finished: median 0
	csZero := []history.Run{
		{Agent: "agy", Status: "failed", Start: base, End: base.Add(10 * time.Second)},
	}
	ps = Compute(csZero, base, nil)
	if len(ps) != 1 || ps[0].Median != 0 {
		t.Fatalf("median with none finished: expected 0, got %v", ps[0].Median)
	}
}

func TestCompute_DateFilter(t *testing.T) {
	since := time.Date(2026, 9, 20, 0, 0, 0, 0, time.Local)

	cs := []history.Run{
		// Before since: must be ignored
		{Agent: "codex", Status: "done", Start: since.Add(-24 * time.Hour)},
		// Equal to since: must be included
		{Agent: "codex", Status: "done", Start: since},
		// After since: must be included
		{Agent: "codex", Status: "done", Start: since.Add(2 * time.Hour)},
		// After but still running: must be ignored
		{Agent: "codex", Status: "running", Start: since.Add(3 * time.Hour)},
	}

	ps := Compute(cs, since, nil)
	if len(ps) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(ps))
	}
	if ps[0].Runs != 2 {
		t.Fatalf("expected 2 runs in range, got %d", ps[0].Runs)
	}
}

func TestCompute_Order(t *testing.T) {
	base := time.Date(2026, 9, 26, 8, 0, 0, 0, time.Local)

	cs := []history.Run{
		{Agent: "opencode", Status: "done", Start: base},
		{Agent: "opencode", Status: "done", Start: base.Add(time.Minute)},
		{Agent: "codex", Status: "done", Start: base},
		{Agent: "codex", Status: "done", Start: base.Add(time.Minute)},
		{Agent: "codex", Status: "done", Start: base.Add(2 * time.Minute)},
		{Agent: "agy", Status: "done", Start: base},
		{Agent: "agy", Status: "done", Start: base.Add(time.Minute)},
	}

	ps := Compute(cs, base, nil)
	if len(ps) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(ps))
	}
	// codex has 3 runs
	if ps[0].Agent != "codex" || ps[0].Runs != 3 {
		t.Errorf("first must be codex with 3, got %s with %d", ps[0].Agent, ps[0].Runs)
	}
	// agy and opencode tie at 2; agy goes first by name
	if ps[1].Agent != "agy" || ps[1].Runs != 2 {
		t.Errorf("second must be agy by alphabetical tie-break, got %s", ps[1].Agent)
	}
	if ps[2].Agent != "opencode" || ps[2].Runs != 2 {
		t.Errorf("third must be opencode, got %s", ps[2].Agent)
	}
}

func TestCompute_NilViolated(t *testing.T) {
	base := time.Date(2026, 9, 26, 8, 0, 0, 0, time.Local)
	cs := []history.Run{
		{Agent: "claude", Status: "done", Start: base},
	}
	// Must not panic with violated == nil
	ps := Compute(cs, base, nil)
	if len(ps) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(ps))
	}
	if ps[0].Violations != 0 {
		t.Fatalf("with violated nil, Violations must be 0, got %d", ps[0].Violations)
	}
}

func TestText_Format(t *testing.T) {
	// No data
	empty := Text(nil, 7)
	wantEmpty := "no runs in the last 7 days\n"
	if empty != wantEmpty {
		t.Fatalf("expected %q, got %q", wantEmpty, empty)
	}

	// With data
	ps := []AgentStats{
		{
			Agent:      "codex",
			Runs:       10,
			Finished:   8,
			Failed:     1,
			OutOfQuota: 1,
			Violations: 2,
			Median:     45 * time.Second,
			Credits:    15,
			Tokens:     50000,
		},
		{
			Agent:      "agy",
			Runs:       5,
			Finished:   5,
			Failed:     0,
			OutOfQuota: 0,
			Violations: 0,
			Median:     2 * time.Minute,
			Credits:    0,
			Tokens:     0,
		},
	}

	txt := Text(ps, 7)
	if !strings.HasPrefix(txt, "last 7 days\n") {
		t.Errorf("missing expected title, got:\n%s", txt)
	}
	// Check the columns are there
	columns := []string{"agent", "runs", "% ok", "failed", "out of quota", "broke", "median", "credits", "tokens"}
	for _, col := range columns {
		if !strings.Contains(txt, col) {
			t.Errorf("table does not contain column %q", col)
		}
	}
	// Check the formatted data shows up
	if !strings.Contains(txt, "80%") {
		t.Errorf("expected 80%% finished ok for codex")
	}
	if !strings.Contains(txt, "100%") {
		t.Errorf("expected 100%% finished ok for agy")
	}
	if !strings.Contains(txt, "45 s") || !strings.Contains(txt, "2 min") {
		t.Errorf("expected readable median durations (45 s, 2 min)")
	}
	if !strings.Contains(txt, "50000") {
		t.Errorf("expected tokens in the table")
	}
}

func TestMarkdown_Format(t *testing.T) {
	day := time.Date(2026, 9, 26, 15, 30, 0, 0, time.Local)
	otherDay := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)

	cs := []history.Run{
		{
			Agent:  "codex",
			Model:  "gpt-5",
			Status: "done",
			Start:  day.Add(10 * time.Minute),
			End:    day.Add(10*time.Minute + 30*time.Second),
			Cost:   "3 cr",
			Task:   "fix bug",
		},
		{
			Agent:  "agy",
			Model:  "gemini-2.5",
			Status: "failed",
			Start:  day,
			End:    day.Add(15 * time.Second),
			Task:   "review tests",
		},
		{
			// From the day before: must not show up in the table
			Agent:  "opencode",
			Model:  "claude",
			Status: "done",
			Start:  otherDay,
			End:    otherDay.Add(5 * time.Minute),
			Task:   "old task",
		},
	}

	violated := func(c history.Run) bool {
		return c.Agent == "codex"
	}

	md := Markdown(cs, day, violated)

	// Title
	wantTitle := "# Agents — 2026-09-26"
	if !strings.Contains(md, wantTitle) {
		t.Errorf("expected title %q in markdown", wantTitle)
	}

	// History summary line present
	if !strings.Contains(md, "today:") {
		t.Errorf("expected a line with history.Summary")
	}

	// Table header
	if !strings.Contains(md, "| time | agent | model | result | duration | cost | task |") {
		t.Errorf("expected the right table header")
	}

	// Order: oldest first (agy was at 15:30, codex at 15:40)
	posAgy := strings.Index(md, "agy")
	posCodex := strings.Index(md, "codex")
	if posAgy < 0 || posCodex < 0 || posAgy > posCodex {
		t.Errorf("wrong chronological order: agy (oldest) must come before codex")
	}

	// A run from another day must not show up
	if strings.Contains(md, "old task") {
		t.Errorf("must not include runs from another day")
	}

	// A run that broke the rules must carry the ⚠ warning in its result
	if !strings.Contains(md, "⚠ done") {
		t.Errorf("the result of a run that broke the rules must carry ⚠ done")
	}
	if strings.Contains(md, "⚠ failed") {
		t.Errorf("agy did not break the rules: must not carry ⚠")
	}
}

func TestMarkdown_EscapeAndTruncate(t *testing.T) {
	day := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)

	// Task with '|' and longer than 80 characters
	longTask := "This is an extremely detailed task with a pipe | in the middle that goes well beyond the eighty characters allowed, to test the cut"
	if len([]rune(longTask)) <= 80 {
		t.Fatalf("the test needs a task longer than 80 characters")
	}

	cs := []history.Run{
		{
			Agent:  "opencode",
			Model:  "custom",
			Status: "done",
			Start:  day,
			End:    day.Add(10 * time.Second),
			Task:   longTask,
		},
	}

	md := Markdown(cs, day, nil)

	// The pipe must be escaped as \|
	if strings.Contains(md, "pipe |") {
		t.Errorf("the '|' character was not escaped: %s", md)
	}
	if !strings.Contains(md, `pipe \|`) {
		t.Errorf("expected to see `pipe \\|` escaped")
	}

	// Check the task was cut at 80 characters
	origRunes := []rune(longTask)
	first80 := string(origRunes[:80])
	escaped80 := strings.ReplaceAll(first80, "|", `\|`)

	if !strings.Contains(md, escaped80) {
		t.Errorf("expected the task cut at 80 characters: %q", escaped80)
	}

	beyond80 := string(origRunes[80:])
	if strings.Contains(md, beyond80) {
		t.Errorf("the content after 80 characters must not be included: %q", beyond80)
	}
}

func TestText_ZeroRunsInPeriod(t *testing.T) {
	base := time.Date(2026, 9, 26, 8, 0, 0, 0, time.Local)
	// All of them out of range
	cs := []history.Run{
		{Agent: "codex", Status: "done", Start: base.Add(-48 * time.Hour)},
	}
	ps := Compute(cs, base, nil)
	txt := Text(ps, 1)
	if txt != "no runs in the last 1 day\n" {
		t.Errorf("unexpected output: %q", txt)
	}
}
