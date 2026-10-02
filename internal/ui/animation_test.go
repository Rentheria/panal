package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/mascots"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

func TestReactionOnChange(t *testing.T) {
	cases := []struct {
		before, now state.Status
		want        mascots.Mode
		present     bool
	}{
		{state.Idle, state.Working, mascots.WakeUp, true},
		{state.Idle, state.Orchestrating, mascots.WakeUp, true},
		{state.Working, state.Done, mascots.Celebrate, true},
		{state.Working, state.Failed, mascots.Scared, true},
		{state.Working, state.OutOfQuota, mascots.Scared, true},
		{state.Working, state.Stuck, mascots.Scared, true},
		{state.Working, state.Working, 0, false},
		{state.Working, state.Orchestrating, 0, false},
		{state.Done, state.Idle, 0, false},
	}
	for _, c := range cases {
		m, ok := reactionForChange(c.before, c.now)
		if ok != c.present || (ok && m != c.want) {
			t.Errorf("%s → %s: got (%d, %v), want (%d, %v)", c.before, c.now, m, ok, c.want, c.present)
		}
	}
}

func TestReactionLastsAndEnds(t *testing.T) {
	f := state.Row{Agent: "codex", Status: state.Done}
	a := Animation{Frame: 10, Reactions: map[string]Reaction{"codex": {Mode: mascots.Celebrate, Start: 10}}}
	if m, n, _ := a.mascot(f, time.Time{}); m != mascots.Celebrate || n != 0 {
		t.Fatalf("at the start: (%d, %d), want Celebrate on frame 0", m, n)
	}
	if a.bubble("codex") == "" {
		t.Fatal("there should be a bubble during the reaction")
	}
	a.Frame = 10 + mascots.Duration(mascots.Celebrate)
	if m, _, _ := a.mascot(f, time.Time{}); m != mascots.Idle {
		t.Fatalf("when the reaction ends it should go back to the status mode, got %d", m)
	}
	if g := a.bubble("codex"); g != "" {
		t.Fatalf("no reaction, no bubble, got %q", g)
	}
}

func TestCardShowsBubbleWithoutChangingHeight(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	f := state.Row{Agent: "agy", Status: state.Done, Model: "gemini", End: now.Add(-time.Minute)}
	still := Card(f, Animation{}, 40, false, now, 0)
	a := Animation{Frame: 3, Reactions: map[string]Reaction{"agy": {Mode: mascots.Celebrate, Start: 3}}}
	celebrates := Card(f, a, 40, false, now, 0)
	if !strings.Contains(celebrates, a.bubble("agy")) {
		t.Fatalf("the card doesn't say %q:\n%s", a.bubble("agy"), celebrates)
	}
	if len(strings.Split(still, "\n")) != len(strings.Split(celebrates, "\n")) {
		t.Fatal("the bubble should not change the card height")
	}
}

func TestNapAndSweat(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	f := state.Row{Agent: "claude", Status: state.Idle, End: now.Add(-3 * time.Hour)}
	if m, _, _ := (Animation{}).mascot(f, now); m != mascots.Nap {
		t.Fatalf("after 3 h at rest it should nap, got %d", m)
	}
	f.End = now.Add(-10 * time.Minute)
	if m, _, _ := (Animation{}).mascot(f, now); m != mascots.Idle {
		t.Fatalf("after 10 min it is still awake, got %d", m)
	}
	f.Quota.Bars = []state.Bar{{Name: "5h", UsedPct: 85}}
	if _, _, fx := (Animation{}).mascot(f, now); !fx.Sweat {
		t.Fatal("with the quota at 85 % it should sweat")
	}
}

func TestKeysPAndArrowsAnimateSelected(t *testing.T) {
	fixedTime := time.Date(2026, 9, 26, 15, 4, 5, 0, time.Local)
	oldNow := nowFn
	nowFn = func() time.Time { return fixedTime }
	defer func() { nowFn = oldNow }()

	var mod tea.Model = New(setupTestEnv(t, fixedTime), 2*time.Second)
	mod, _ = mod.Update(tea.WindowSizeMsg{Width: 132, Height: 40})
	mod, _ = mod.Update(keyMsg("p"))
	m := mod.(Model)
	sel := m.rows[m.table.Cursor()].Agent
	if r := m.reactions[sel]; r.Mode != mascots.Pet {
		t.Fatalf("p should pet %s: %+v", sel, m.reactions)
	}
	mod, _ = mod.Update(keyMsg("right"))
	m = mod.(Model)
	other := m.rows[m.table.Cursor()].Agent
	if other == sel {
		t.Fatal("→ did not change the selection")
	}
	if r := m.reactions[other]; r.Mode != mascots.Greet {
		t.Fatalf("the newly selected one (%s) should greet: %+v", other, m.reactions)
	}
}

func TestCardTellsWhatItDoes(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	f := state.Row{Agent: "codex", Status: state.Working, Since: now.Add(-5 * time.Minute),
		Activity: "$ go test ./...", ActivityAt: now.Add(-10 * time.Second)}
	if v := Card(f, Animation{}, 40, false, now, 0); !strings.Contains(v, "› $ go test ./...") {
		t.Fatalf("the card should say what it is doing:\n%s", v)
	}
	f.ActivityAt = now.Add(-3 * time.Minute)
	if got := activityText(f, now); got != "$ go test ./... · 3 min ago" {
		t.Fatalf("after more than a minute it should say how long ago: %q", got)
	}
	a := Animation{Reactions: map[string]Reaction{"codex": {Mode: mascots.WakeUp}}}
	if v := Card(f, a, 40, false, now, 0); strings.Contains(v, "go test") {
		t.Fatal("during a reaction the reaction phrase wins")
	}
	f.Status = state.Done
	if activityText(f, now) != "" {
		t.Fatal("once it stops working, it no longer says what it is doing")
	}
}

func TestSocialReactions(t *testing.T) {
	m := New(nil, time.Second)
	m.rows = []state.Row{
		{Agent: "claude", Status: state.Orchestrating},
		{Agent: "agy", Status: state.Idle},
		{Agent: "codex", Status: state.Working},
		{Agent: "opencode", Status: state.Idle},
	}
	m.react("codex", mascots.WakeUp)
	m.socialReactions([]mascotChange{{"codex", mascots.WakeUp}})
	if r := m.reactions["claude"]; r.Mode != mascots.Conduct || m.animation().bubble("claude") != "your turn, codex!" {
		t.Fatalf("claude should conduct codex: %+v", r)
	}

	m.frame += 10 // let the reactions end
	m.rows[2].Status = state.Done
	m.react("codex", mascots.Celebrate)
	m.socialReactions([]mascotChange{{"codex", mascots.Celebrate}})
	if r := m.reactions["agy"]; r.Mode != mascots.LookRight {
		t.Fatalf("agy (left of codex) should look right: %+v", r)
	}
	if r := m.reactions["opencode"]; r.Mode != mascots.LookLeft {
		t.Fatalf("opencode (on the right) should look left: %+v", r)
	}
	if r := m.reactions["claude"]; r.Mode == mascots.LookRight || r.Mode == mascots.LookLeft {
		t.Fatal("claude is orchestrating (moving): it doesn't turn")
	}
}

func TestStreak(t *testing.T) {
	cs := []history.Run{ // newest to oldest
		{Agent: "codex", Status: runRunning},
		{Agent: "codex", Status: runDone},
		{Agent: "agy", Status: runFailed},
		{Agent: "codex", Status: runSkipped},
		{Agent: "codex", Status: runDone},
		{Agent: "codex", Status: runOutOfQuota},
		{Agent: "codex", Status: runDone},
	}
	if n := streak(cs, "codex"); n != 2 {
		t.Fatalf("codex: %d in a row, want 2 (broken by out of quota)", n)
	}
	if n := streak(cs, "agy"); n != 0 {
		t.Fatalf("agy: %d, want 0", n)
	}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	f := state.Row{Agent: "agy", Status: state.Done, Streak: 7, End: now.Add(-time.Minute)}
	if v := stripANSI(Card(f, Animation{}, 40, false, now, 0)); !strings.Contains(v, "★ 7 in a row, no failures") {
		t.Fatalf("the card should show off the streak:\n%s", v)
	}
}

func TestStuckIsMarkedAndAlertedOnce(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	f := state.Row{Agent: "codex", Status: state.Working, Activity: "$ go test ./...", Repeating: "$ go test ./...", Times: 4}
	if v := stripANSI(Card(f, Animation{}, 40, false, now, 0)); !strings.Contains(v, "⟳ ×4 $ go test") {
		t.Fatalf("the card should mark the repetition:\n%s", v)
	}
	m := New(nil, time.Second)
	m.now = now
	f2 := f
	f2.Times = 2
	m.rows = []state.Row{f2}
	m.detectAlerts() // on start it wasn't repeating yet
	m.rows = []state.Row{f}
	a := m.detectAlerts()
	n := 0
	for _, x := range a {
		if strings.Contains(x.title, "repeated the same thing 4 times") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("it should alert once: %+v", a)
	}
	for _, x := range m.detectAlerts() {
		if strings.Contains(x.title, "repeated") {
			t.Fatal("the alert should not repeat for the same action")
		}
	}
}

func TestCardSaysIfTestsPassed(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	f := state.Row{Agent: "codex", Status: state.Done, End: now.Add(-time.Minute), Tests: "✗ 2 failures · go test ./...", Streak: 9}
	if v := stripANSI(Card(f, Animation{}, 40, false, now, 0)); !strings.Contains(v, "✗ 2 failures · go test") {
		t.Fatalf("tests win over the streak:\n%s", v)
	}
	f.Status = state.Working
	f.Activity = "$ go build ./..."
	if v := stripANSI(Card(f, Animation{}, 40, false, now, 0)); !strings.Contains(v, "› $ go build") {
		t.Fatalf("while working, what it does wins:\n%s", v)
	}
}
