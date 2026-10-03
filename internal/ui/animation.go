package ui

import (
	"fmt"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/mascots"
	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

// Reaction is a short mascot animation that started on pulse Start.
type Reaction struct {
	Mode  mascots.Mode
	Start int
	Text  string // what it says; empty = one of the mode's phrases
}

// Animation is what the cards need to animate the mascots: the current pulse,
// the card's offset (so they don't all move at once) and the reactions in
// progress by agent.
type Animation struct {
	Frame     int
	Offset    int
	Reactions map[string]Reaction
}

// After how long at rest a mascot takes a nap.
const napAfter = 2 * time.Hour

// From which quota percentage it sweats.
const sweatFrom = 80

func (a Animation) offsetBy(d int) Animation {
	a.Offset = d
	return a
}

// reaction returns the agent's reaction in progress and its current frame.
func (a Animation) reaction(agent string) (Reaction, int, bool) {
	r, ok := a.Reactions[agent]
	if !ok {
		return r, 0, false
	}
	n := a.Frame - r.Start
	if n < 0 || n >= mascots.Duration(r.Mode) {
		return r, 0, false
	}
	return r, n, true
}

// mascot says which animation to play, at which frame and with which effects:
// the reaction if one is in progress; otherwise the status one (a nap if it has
// been at rest for hours).
func (a Animation) mascot(f state.Row, now time.Time) (mascots.Mode, int, mascots.Effects) {
	fx := MascotEffects(f)
	if r, n, ok := a.reaction(f.Agent); ok {
		return r.Mode, n, fx
	}
	return StatusMode(f, now), a.Frame + a.Offset, fx
}

// StatusMode is the mascot's animation for the agent's status when no
// reaction is in progress: MascotMode, or a nap if it has been at rest for
// hours. The dashboard and panal pet share it.
func StatusMode(f state.Row, now time.Time) mascots.Mode {
	mode := MascotMode(f.Status)
	if mode == mascots.Idle && (f.Status == state.Idle || f.Status == state.Done) &&
		!f.End.IsZero() && !now.IsZero() && now.Sub(f.End) > napAfter {
		mode = mascots.Nap
	}
	return mode
}

// MascotEffects are the status effects that are not a mode (sweating when a
// quota or the context is above 80 %).
func MascotEffects(f state.Row) mascots.Effects {
	return mascots.Effects{Sweat: highQuota(f)}
}

func highQuota(f state.Row) bool {
	if f.HasContext && f.ContextPct >= sweatFrom {
		return true
	}
	for _, b := range f.Quota.Bars {
		if b.UsedPct >= sweatFrom {
			return true
		}
	}
	return false
}

// What the mascot says during a reaction, on the free line below it. It
// varies with the pulse it started on, so it doesn't always say the same thing.
var phrases = map[mascots.Mode][]string{
	mascots.Celebrate: {"done!", "nailed it!", "all set!"},
	mascots.Scared:    {"oh no!", "something failed", "oops…"},
	mascots.WakeUp:    {"on it!", "let's go!", "to work"},
	mascots.Pet:       {"♥", "thanks!", "♥ ♥"},
}

func (a Animation) bubble(agent string) string {
	r, _, ok := a.reaction(agent)
	if !ok {
		return ""
	}
	if r.Text != "" {
		return r.Text
	}
	fs := phrases[r.Mode]
	if len(fs) == 0 {
		return ""
	}
	i := r.Start % len(fs)
	if i < 0 {
		i = -i
	}
	return fs[i]
}

// ReactionForChange: which reaction a change from one status to another causes.
func ReactionForChange(before, now state.Status) (mascots.Mode, bool) {
	works := func(e state.Status) bool { return e == state.Working || e == state.Orchestrating }
	switch {
	case before == now:
		return 0, false
	case works(now) && !works(before):
		return mascots.WakeUp, true
	case now == state.Done:
		return mascots.Celebrate, true
	case now == state.Failed || now == state.Stuck || now == state.OutOfQuota || now == state.NoPermission:
		return mascots.Scared, true
	}
	return 0, false
}

// justFinished: the status change comes from a run that just ended (it was
// working, or its last run ended recently), not from a reader re-reading the
// quota.
func (m *Model) justFinished(agent string, before state.Status) bool {
	if before == state.Working || before == state.Orchestrating || before == state.Stuck {
		return true
	}
	c, ok := m.lastRun(agent)
	return ok && !c.End.IsZero() && m.now.Sub(c.End) < 3*time.Minute
}

// react starts a reaction of the agent's mascot on this pulse.
func (m *Model) react(agent string, mode mascots.Mode) {
	if NoAnimation { // without a pulse the reaction would never end
		return
	}
	if m.reactions == nil {
		m.reactions = map[string]Reaction{}
	}
	m.reactions[agent] = Reaction{Mode: mode, Start: m.frame}
}

// mascotChange: a reaction caused by a status change.
type mascotChange struct {
	agent string
	mode  mascots.Mode
}

// socialReactions: the mascots see each other. If an agent starts working
// while claude orchestrates, claude conducts it ("your turn, codex!"); if one
// finishes or fails, the idle ones turn to look at it.
func (m *Model) socialReactions(cs []mascotChange) {
	pos := map[string]int{}
	for i, f := range m.rows {
		pos[f.Agent] = i
	}
	free := func(ag string) bool {
		_, _, busy := m.animation().reaction(ag)
		return !busy
	}
	for _, c := range cs {
		i, ok := pos[c.agent]
		if !ok {
			continue
		}
		switch c.mode {
		case mascots.WakeUp:
			if j, ok := pos["claude"]; ok && c.agent != "claude" && m.rows[j].Status == state.Orchestrating && free("claude") {
				m.react("claude", mascots.Conduct)
				if r, ok := m.reactions["claude"]; ok {
					r.Text = "your turn, " + c.agent + "!"
					m.reactions["claude"] = r
				}
			}
		case mascots.Celebrate, mascots.Scared:
			for j, f := range m.rows {
				if j == i || !free(f.Agent) || MascotMode(f.Status) != mascots.Idle {
					continue
				}
				mode := mascots.LookRight
				if j > i {
					mode = mascots.LookLeft
				}
				m.react(f.Agent, mode)
			}
		}
	}
}

// greet: the newly selected mascot hops (unless it is already in the middle
// of another reaction, which matters more).
func (m *Model) greet() {
	sel, ok := m.selected()
	if !ok {
		return
	}
	ag := m.rows[sel].Agent
	if _, _, ok := m.animation().reaction(ag); ok {
		return
	}
	m.react(ag, mascots.Greet)
}

// selected: index of the selected row, as the view draws it (the table
// reports -1 while it has no rows).
func (m Model) selected() (int, bool) {
	if len(m.rows) == 0 {
		return 0, false
	}
	return min(max(0, m.table.Cursor()), len(m.rows)-1), true
}

func (m Model) animation() Animation {
	return Animation{Frame: m.frame, Reactions: m.reactions}
}

// After how many repetitions in a row of the same thing it is marked, and
// after how many it is alerted.
const (
	repeatFrom = 3
	alertFrom  = 4
)

// repeatText: "⟳ ×4 $ go test ./..." if the working agent is doing the same
// thing over and over (it may be stuck).
func repeatText(f state.Row) string {
	if f.Times < repeatFrom || (f.Status != state.Working && f.Status != state.Stuck) {
		return ""
	}
	return fmt.Sprintf("⟳ ×%d %s", f.Times, f.Repeating)
}

// activityText: the last thing a working agent did (or a stuck one, to see
// where), with how long ago if it was more than a minute.
func activityText(f state.Row, now time.Time) string {
	if f.Activity == "" || (f.Status != state.Working && f.Status != state.Orchestrating && f.Status != state.Stuck) {
		return ""
	}
	t := f.Activity
	if !f.ActivityAt.IsZero() && !now.IsZero() {
		if d := now.Sub(f.ActivityAt); d >= time.Minute {
			t += " · " + readers.Ago(d) + " ago"
		}
	}
	return t
}

// From how many runs in a row without failure the streak is shown.
const minStreak = 3

// streak: the agent's consecutive runs that ended without failure, from the
// most recent backwards (runs go from newest to oldest). Running and skipped
// runs neither count nor break it.
func streak(runs []history.Run, agent string) int {
	n := 0
	for _, c := range runs {
		if c.Agent != agent {
			continue
		}
		switch c.Status {
		case runDone:
			n++
		case runRunning, runSkipped:
		default:
			return n
		}
	}
	return n
}
