package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

func testRows() []state.Row {
	return []state.Row{
		{Agent: "claude", Status: state.Orchestrating, Model: "Opus", Task: "orchestrate",
			Quota: state.Quota{Exact: true, Bars: []state.Bar{{Name: "5h", UsedPct: 17}, {Name: "sem", UsedPct: 42}}}},
		{Agent: "agy", Status: state.Working, Quota: state.Quota{Exact: true, Bars: []state.Bar{{Name: "5h", UsedPct: 90}}}},
		{Agent: "codex", Status: state.OutOfQuota, Quota: state.Quota{Exact: true, Credits: "999",
			Bars: []state.Bar{{Name: "sem", UsedPct: 100}}}},
		{Agent: "opencode", Status: state.NoData, Quota: state.Quota{Summary: "Go used up"}},
	}
}

// With different numbers of bars, the cards must be the same height:
// otherwise the borders don't line up.
func TestCardsSameHeight(t *testing.T) {
	now := time.Now()
	fs := testRows()
	heights := map[int]bool{}
	for i, f := range fs {
		heights[lipgloss.Height(Card(f, Animation{}, 30, i == 0, now, 20))] = true
	}
	if len(heights) != 1 {
		t.Fatalf("with a fixed height, the cards have different heights: %v", heights)
	}
	// And Cards works that height out by itself: in a row of 4, the last line
	// must have the 4 bottom corners. If a card were shorter, its bottom border
	// would sit higher and a corner would be missing.
	lines := strings.Split(Cards(fs, Animation{}, 0, 132, now), "\n")
	last := lines[len(lines)-1]
	if n := strings.Count(last, "╯") + strings.Count(last, "┛"); n != len(fs) {
		t.Fatalf("the last line has %d bottom corners, want %d: the borders don't line up\n%s", n, len(fs), last)
	}
}

func TestCardsFitWidth(t *testing.T) {
	now := time.Now()
	fs := testRows()
	wide := Cards(fs, Animation{}, 0, 132, now)
	narrow := Cards(fs, Animation{}, 0, 80, now)
	if lipgloss.Height(narrow) <= lipgloss.Height(wide) {
		t.Fatalf("at 80 columns there should be two rows of cards: height %d vs %d",
			lipgloss.Height(narrow), lipgloss.Height(wide))
	}
	if lipgloss.Width(wide) > 132 || lipgloss.Width(narrow) > 80 {
		t.Fatalf("the cards overflow the width: %d/132, %d/80", lipgloss.Width(wide), lipgloss.Width(narrow))
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("short truncate: %q", got)
	}
	got := truncate("You work in C:\\Codigo\\wt\\panal", 10)
	if lipgloss.Width(got) > 10 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long truncate: %q (width %d)", got, lipgloss.Width(got))
	}
}

func TestBarFitsAndShowsPercent(t *testing.T) {
	for _, pct := range []float64{0, 10, 70, 95, 100, 130} {
		b := Bar(state.Bar{Name: "5h", UsedPct: pct}, 30, time.Time{})
		if lipgloss.Width(b) > 30 {
			t.Fatalf("%v%%: the bar overflows its width: %d", pct, lipgloss.Width(b))
		}
	}
	if b := Bar(state.Bar{Name: "5h", UsedPct: 130}, 30, time.Time{}); !strings.Contains(b, "100%") {
		t.Fatalf("usage above 100 should show as 100%%: %q", b)
	}
}

func TestBarShowsRunsOut(t *testing.T) {
	runsOut := time.Date(2026, 9, 26, 15, 40, 0, 0, time.Local)
	resets := time.Date(2026, 9, 26, 17, 0, 0, 0, time.Local)
	b := state.Bar{
		Name:       "5h",
		UsedPct:    40, // normally green
		ResetsAt:   resets,
		ExhaustsAt: runsOut,
	}
	render := Bar(b, 35, resets.Add(-time.Hour))
	if !strings.Contains(render, "⚠ ~15:40") {
		t.Fatalf("the bar should include \"⚠ ~15:40\": %q", render)
	}
	if lipgloss.Width(render) > 35 {
		t.Fatalf("the bar overflows the width: %d/35", lipgloss.Width(render))
	}
}

func TestBarRunsOutDoesNotFit(t *testing.T) {
	runsOut := time.Date(2026, 9, 26, 15, 40, 0, 0, time.Local)
	resets := time.Date(2026, 9, 26, 17, 0, 0, 0, time.Local)
	b := state.Bar{
		Name:       "5h",
		UsedPct:    40,
		ResetsAt:   resets,
		ExhaustsAt: runsOut,
	}
	// At a narrow width (24 columns) "⚠ ~15:40" doesn't fit with a bar >= 4; only "⚠" should stay
	render := Bar(b, 24, resets.Add(-time.Hour))
	if !strings.Contains(render, "⚠") {
		t.Fatalf("the narrow bar should contain \"⚠\": %q", render)
	}
	if strings.Contains(render, "~15:40") {
		t.Fatalf("the full time doesn't fit, only \"⚠\" should stay: %q", render)
	}
	if lipgloss.Width(render) > 24 {
		t.Fatalf("the narrow bar overflows the width: %d/24", lipgloss.Width(render))
	}
}

func TestCard_EndAndTime(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)

	// Working: uses Since
	fWorking := state.Row{
		Agent:  "agy",
		Status: state.Working,
		Since:  now.Add(-15 * time.Minute),
	}
	v := Card(fWorking, Animation{}, 40, false, now, 0)
	if !strings.Contains(v, "time  15 min") {
		t.Fatalf("working should show 15 min, got:\n%s", v)
	}

	// Done with End and Start: "2 h ago · took 8 min"
	fDone := state.Row{
		Agent:  "codex",
		Status: state.Done,
		Start:  now.Add(-2*time.Hour - 8*time.Minute),
		End:    now.Add(-2 * time.Hour),
	}
	vDone := Card(fDone, Animation{}, 45, false, now, 0)
	if !strings.Contains(vDone, "2 h ago · took 8 min") {
		t.Fatalf("done should show '2 h ago · took 8 min', got:\n%s", vDone)
	}

	// OutOfQuota with End: "out of quota 5 min ago"
	fNoQuota := state.Row{
		Agent:  "codex",
		Status: state.OutOfQuota,
		End:    now.Add(-5 * time.Minute),
	}
	vQuota := Card(fNoQuota, Animation{}, 40, false, now, 0)
	if !strings.Contains(vQuota, "out of quota 5 min ago") {
		t.Fatalf("out of quota should show 'out of quota 5 min ago', got:\n%s", vQuota)
	}

	// Failed with End: "failed 2 h ago"
	fFailed := state.Row{
		Agent:  "opencode",
		Status: state.Failed,
		End:    now.Add(-2 * time.Hour),
	}
	vFailed := Card(fFailed, Animation{}, 40, false, now, 0)
	if !strings.Contains(vFailed, "failed 2 h ago") {
		t.Fatalf("failed should show 'failed 2 h ago', got:\n%s", vFailed)
	}

	// At rest with no End: "time  —"
	fIdle := state.Row{
		Agent:  "claude",
		Status: state.Idle,
	}
	vIdle := Card(fIdle, Animation{}, 40, false, now, 0)
	if !strings.Contains(vIdle, "time  —") {
		t.Fatalf("at rest with no end it should show 'time  —', got:\n%s", vIdle)
	}
}

func TestCard_Review(t *testing.T) {
	now := time.Now()
	fWithReview := state.Row{
		Agent:  "codex",
		Status: state.Done,
		Review: "changed 1 file outside what was allowed",
	}
	vWith := Card(fWithReview, Animation{}, 50, false, now, 0)
	if !strings.Contains(vWith, "⚠ changed 1 file outside what was allowed") {
		t.Fatalf("a card with a review should show '⚠ changed 1 file outside what was allowed', got:\n%s", vWith)
	}

	fNoReview := state.Row{
		Agent:  "codex",
		Status: state.Done,
	}
	vWithout := Card(fNoReview, Animation{}, 50, false, now, 0)
	if strings.Contains(vWithout, "⚠") {
		t.Fatalf("a card without a review should not show '⚠', got:\n%s", vWithout)
	}
}

func TestBarFullAndEmpty(t *testing.T) {
	// 0% -> all ░, no █
	b0 := Bar(state.Bar{Name: "5h", UsedPct: 0}, 30, time.Time{})
	filled0 := strings.Count(b0, "█")
	empty0 := strings.Count(b0, "░")
	if filled0 != 0 || empty0 == 0 {
		t.Fatalf("at 0%% expected 0 █ and >0 ░, got %d █ and %d ░ in %q", filled0, empty0, b0)
	}

	// 100% -> all █, no ░
	b100 := Bar(state.Bar{Name: "5h", UsedPct: 100}, 30, time.Time{})
	filled100 := strings.Count(b100, "█")
	empty100 := strings.Count(b100, "░")
	if empty100 != 0 || filled100 == 0 {
		t.Fatalf("at 100%% expected 0 ░ and >0 █, got %d █ and %d ░ in %q", filled100, empty100, b100)
	}

	// 50% -> half █, half ░ (at most 1 apart)
	b50 := Bar(state.Bar{Name: "5h", UsedPct: 50}, 30, time.Time{})
	filled50 := strings.Count(b50, "█")
	empty50 := strings.Count(b50, "░")
	if filled50 == 0 || empty50 == 0 || math.Abs(float64(filled50-empty50)) > 1 {
		t.Fatalf("at 50%% expected a balanced split, got %d █ and %d ░ in %q", filled50, empty50, b50)
	}
}

func TestLabelsAndReset(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	// Check that "5h" -> "5 h   " and "sem" -> "week  "
	b5h := Bar(state.Bar{Name: "5h", UsedPct: 20}, 40, now)
	bWeek := Bar(state.Bar{Name: "sem", UsedPct: 20}, 40, now)
	if !strings.Contains(b5h, "5 h   ") {
		t.Fatalf("expected the label '5 h   ' padded to 6 columns: %q", b5h)
	}
	if !strings.Contains(bWeek, "week  ") {
		t.Fatalf("expected the label 'week  ' padded to 6 columns: %q", bWeek)
	}

	// Reset soon (<24h): if it fits -> "resets 14:30"
	bNear := state.Bar{
		Name:     "5h",
		UsedPct:  20,
		ResetsAt: now.Add(2*time.Hour + 30*time.Minute),
	}
	rLong := Bar(bNear, 40, now)
	if !strings.Contains(rLong, "resets 14:30") {
		t.Fatalf("with enough width it should say 'resets 14:30': %q", rLong)
	}

	// Reset soon when the long form doesn't fit -> "↻ 14:30"
	rShort := Bar(bNear, 28, now)
	if !strings.Contains(rShort, "↻ 14:30") {
		t.Fatalf("at a narrow width it should say '↻ 14:30': %q", rShort)
	}

	// Reset later (>=24h): if it fits -> "resets 03 Oct"
	bFar := state.Bar{
		Name:     "sem",
		UsedPct:  20,
		ResetsAt: time.Date(2026, 10, 3, 10, 0, 0, 0, time.Local),
	}
	rLongDate := Bar(bFar, 40, now)
	if !strings.Contains(rLongDate, "resets 03 Oct") {
		t.Fatalf("with enough width it should say 'resets 03 Oct': %q", rLongDate)
	}

	// Reset later when the long form doesn't fit -> "↻ 03 Oct"
	rShortDate := Bar(bFar, 28, now)
	if !strings.Contains(rShortDate, "↻ 03 Oct") {
		t.Fatalf("at a narrow width it should say '↻ 03 Oct': %q", rShortDate)
	}
}

func TestCardNoData(t *testing.T) {
	now := time.Now()
	f := state.Row{
		Agent:  "opencode",
		Status: state.NoData,
	}
	v := Card(f, Animation{}, 40, false, now, 0)
	if !strings.Contains(v, "no runs from opencode yet") {
		t.Fatalf("a card with status NoData should say 'no runs from opencode yet':\n%s", v)
	}
}

func TestContextLine(t *testing.T) {
	f := state.Row{Agent: "claude", HasContext: true, ContextPct: 34, SessionCost: "$15.51"}
	if got := stripANSI(contextLine(f, 40)); got != "context 34% · $15.51 this session" {
		t.Fatalf("wide: %q", got)
	}
	if got := stripANSI(contextLine(f, 24)); got != "context 34% · $15.51" {
		t.Fatalf("narrow: %q", got)
	}
	if got := stripANSI(contextLine(f, 14)); got != "context 34%" {
		t.Fatalf("very narrow: %q", got)
	}
	if contextLine(state.Row{}, 40) != "" {
		t.Fatal("no context data means no line")
	}
	f.ContextPct = 90
	if _, _, fx := (Animation{}).mascot(f, time.Time{}); !fx.Sweat {
		t.Fatal("with the context at 90 % it should sweat")
	}
}

func TestBarAlreadyReset(t *testing.T) {
	b := state.Bar{Name: "5h", AlreadyReset: true}
	if got := stripANSI(Bar(b, 40, time.Time{})); !strings.Contains(got, "0% already reset") {
		t.Fatalf("wide: %q", got)
	}
	if got := stripANSI(Bar(b, 22, time.Time{})); !strings.Contains(got, "↻ now") {
		t.Fatalf("narrow: %q", got)
	}
}

// With quota bars, the summary only shows when it is the credits spent this
// session (written by the codex reader as "-N cr this session").
func TestCardShowsSessionSpend(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	f := state.Row{Agent: "codex", Status: state.Working, Quota: state.Quota{Exact: true,
		Bars: []state.Bar{{Name: "5h", UsedPct: 20}}, Summary: "-12 cr this session"}}
	if v := stripANSI(Card(f, Animation{}, 40, false, now, 0)); !strings.Contains(v, "-12 cr this session") {
		t.Fatalf("the card should show the session spend:\n%s", v)
	}
	f.Quota.Summary = "something else"
	if v := stripANSI(Card(f, Animation{}, 40, false, now, 0)); strings.Contains(v, "something else") {
		t.Fatalf("with bars, other summaries are not shown:\n%s", v)
	}
}
