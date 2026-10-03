// Package pet is `panal pet`: one mascot you keep next to an agent CLI, in a
// small terminal pane or drawn by another program from its JSON frames
// (-json, -stream). It reads the same cheap readers as `panal -once` (never
// the live quota, never an agent process) and reacts like the dashboard's
// mascots: working, celebrating, scared, asleep.
package pet

import (
	"strings"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/mascots"
	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/state"
	"github.com/AlbertoVasquezR/panal/internal/ui"
)

// FrameEvery is the animation pace: about 4 frames per second.
const FrameEvery = 250 * time.Millisecond

// dashboardPulse is the dashboard's animation tick: a reaction lasts as long
// in wall time as it does there (mascots.Duration counts those ticks).
const dashboardPulse = 450 * time.Millisecond

// JustNow: a run that ended less than this ago still makes the pet celebrate
// (or get scared), even in a single -json call that saw no change. Long
// enough for a consumer that polls every 2 s to catch it.
const JustNow = 5 * time.Second

// finishedWithin: a status change counts as a run that just finished (and
// not a reader re-reading an old run) if it was working or its run ended
// within this time. Same rule as the dashboard.
const finishedWithin = 3 * time.Minute

// Moods, as the JSON contract names them.
const (
	MoodWork      = "work"
	MoodCelebrate = "celebrate"
	MoodScared    = "scared"
	MoodSleep     = "sleep"
	MoodIdle      = "idle"
	MoodGreet     = "greet"
)

// MoodOf maps a mascot mode to the contract's mood.
func MoodOf(m mascots.Mode) string {
	switch m {
	case mascots.Working, mascots.Conduct:
		return MoodWork
	case mascots.Celebrate:
		return MoodCelebrate
	case mascots.Scared, mascots.Stuck:
		return MoodScared
	case mascots.Sleeping, mascots.Nap:
		return MoodSleep
	case mascots.WakeUp, mascots.Greet:
		return MoodGreet
	}
	return MoodIdle
}

// Options for a pet.
type Options struct {
	Agent       string // forced pet agent; "" = chosen by SelectAgent
	Cols, Rows  int    // maximum raster size in cells; 0 = the sprite's
	NoAnimation bool   // still frame, no reactions
	All         bool   // also every shown agent's own mascot in Frame.Pets
}

// reaction is a short animation that started at a moment.
type reaction struct {
	mode mascots.Mode
	at   time.Time
}

// Pet remembers what it saw between refreshes, to react to changes.
type Pet struct {
	opts      Options
	statuses  map[string]state.Status
	reactions map[string]reaction
	current   string // the pet's agent on the last refresh
}

// New returns a pet that has not seen anything yet.
func New(opts Options) *Pet {
	return &Pet{opts: opts, statuses: map[string]state.Status{}, reactions: map[string]reaction{}}
}

// Visible: the rows the pet talks about, in reader order. Agents turned off
// in panal.conf (or -off) are left out while at rest, like the status line.
func Visible(rows []state.Row) []state.Row {
	offs := ui.DisabledAgents()
	out := make([]state.Row, 0, len(rows))
	for _, f := range rows {
		if offs[f.Agent] && ui.AtRest(f.Status) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// SelectAgent picks the pet's agent: the forced one; else claude while it
// orchestrates; else the working (or stuck) agent that started last; else the
// one whose last run ended last; else claude, or the first row.
func SelectAgent(rows []state.Row, forced string) string {
	if forced != "" {
		return forced
	}
	for _, f := range rows {
		if f.Agent == "claude" && f.Status == state.Orchestrating {
			return f.Agent
		}
	}
	best, bestAt := "", time.Time{}
	for _, f := range rows {
		if f.Status != state.Working && f.Status != state.Stuck {
			continue
		}
		at := firstSet(f.Since, f.Start, f.ActivityAt)
		if best == "" || at.After(bestAt) {
			best, bestAt = f.Agent, at
		}
	}
	if best != "" {
		return best
	}
	for _, f := range rows {
		if !f.End.IsZero() && f.End.After(bestAt) {
			best, bestAt = f.Agent, f.End
		}
	}
	if best != "" {
		return best
	}
	for _, f := range rows {
		if f.Agent == "claude" {
			return f.Agent
		}
	}
	if len(rows) > 0 {
		return rows[0].Agent
	}
	return ""
}

func firstSet(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// Observe records a refresh: status changes start reactions (the
// dashboard's table: working → wake up, done → celebrate, failed, stuck or
// out of quota → scared) and a new pet agent greets.
func (p *Pet) Observe(rows []state.Row, now time.Time) {
	vis := Visible(rows)
	first := len(p.statuses) == 0
	for _, f := range vis {
		before, seen := p.statuses[f.Agent]
		p.statuses[f.Agent] = f.Status
		if first || !seen || p.opts.NoAnimation {
			continue
		}
		r, ok := ui.ReactionForChange(before, f.Status)
		if !ok {
			continue
		}
		if r != mascots.WakeUp && !justFinished(f, before, now) {
			continue
		}
		p.reactions[f.Agent] = reaction{r, now}
	}
	agent := SelectAgent(vis, p.opts.Agent)
	if p.current != "" && agent != p.current && !p.opts.NoAnimation {
		if _, busy := p.active(agent, now); !busy {
			p.reactions[agent] = reaction{mascots.Greet, now}
		}
	}
	p.current = agent
}

func justFinished(f state.Row, before state.Status, now time.Time) bool {
	if before == state.Working || before == state.Orchestrating || before == state.Stuck {
		return true
	}
	return !f.End.IsZero() && now.Sub(f.End) < finishedWithin
}

// active returns the agent's observed reaction if it is still running.
func (p *Pet) active(agent string, now time.Time) (reaction, bool) {
	r, ok := p.reactions[agent]
	if !ok {
		return r, false
	}
	d := now.Sub(r.at)
	return r, d >= 0 && d < time.Duration(mascots.Duration(r.mode))*dashboardPulse
}

// event is a reaction that may show on the pet.
type event struct {
	mode mascots.Mode
	at   time.Time
}

// latestEvent: the newest reaction that should show on the pet: any of its
// own agent's, or another agent's run that finished or failed (the pet is
// the user's: it cheers for all of them). Observed reactions and runs that
// ended within JustNow both count.
func (p *Pet) latestEvent(vis []state.Row, agent string, now time.Time) (event, bool) {
	var best event
	found := false
	consider := func(e event) {
		if !found || e.at.After(best.at) {
			best, found = e, true
		}
	}
	for _, f := range vis {
		own := f.Agent == agent
		if r, ok := p.active(f.Agent, now); ok && (own || r.mode == mascots.Celebrate || r.mode == mascots.Scared) {
			consider(event(r))
		}
		if f.End.IsZero() || now.Before(f.End) || now.Sub(f.End) >= JustNow {
			continue
		}
		switch f.Status {
		case state.Done:
			consider(event{mascots.Celebrate, f.End})
		case state.Failed:
			consider(event{mascots.Scared, f.End})
		}
	}
	return best, found
}

// AgentInfo is one agent in the contract's "agents" list.
type AgentInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Glyph  string `json:"glyph"`
	Color  string `json:"color"`
}

// PetInfo is one agent's own mascot, in the frame's "pets" list (-all).
type PetInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Glyph  string `json:"glyph"`
	Color  string `json:"color"`
	Mood   string `json:"mood"`
	Line   string `json:"line"`
	Raster Raster `json:"raster"`
}

// Raster is the mascot as cells; see EncodeCells.
type Raster struct {
	Columns int    `json:"columns"`
	Rows    int    `json:"rows"`
	Cells   string `json:"cells"`
}

// Frame is one JSON frame of `panal pet -json` / `-stream` (contract v1)
// plus what the terminal view needs to draw it.
type Frame struct {
	V      int         `json:"v"`
	Agent  string      `json:"agent"`
	Status string      `json:"status"`
	Glyph  string      `json:"glyph"`
	Color  string      `json:"color"`
	Mood   string      `json:"mood"`
	Line   string      `json:"line"`
	Others string      `json:"others"`
	Agents []AgentInfo `json:"agents"`
	Raster Raster      `json:"raster"`
	Pets   []PetInfo   `json:"pets,omitempty"` // with Options.All: every shown agent

	row    state.Row
	suffix string // "· 12 min", the status line's time
	others []state.Row
	cells  [][]mascots.Cell
}

// Frame builds what the pet shows at now from the last rows read.
func (p *Pet) Frame(rows []state.Row, now time.Time) Frame {
	vis := Visible(rows)
	agent := SelectAgent(vis, p.opts.Agent)
	var row state.Row
	found := false
	for _, f := range rows { // a forced agent shows even if it is off
		if f.Agent == agent {
			row, found = f, true
			break
		}
	}
	if !found {
		row = state.Row{Agent: agent}
	}

	mode, n := ui.StatusMode(row, now), 0
	if !p.opts.NoAnimation {
		n = int(now.UnixMilli() / FrameEvery.Milliseconds())
		if e, ok := p.latestEvent(vis, agent, now); ok {
			mode, n = e.mode, int(now.Sub(e.at)/FrameEvery)
		}
	}

	f := Frame{
		V:      1,
		Agent:  agent,
		Status: row.Status.String(),
		Glyph:  ui.Icon(row.Status),
		Color:  Hex(ui.StatusColor(row.Status)),
		Mood:   MoodOf(mode),
		Line:   Line(row, now),
		Agents: []AgentInfo{},
		row:    row,
	}
	if lp := lineParts(row, now); len(lp) > 3 {
		f.suffix = lp[3]
	}
	var parts []string
	for _, o := range vis {
		f.Agents = append(f.Agents, AgentInfo{o.Agent, o.Status.String(), ui.Icon(o.Status), Hex(ui.StatusColor(o.Status))})
		if o.Agent != agent {
			f.others = append(f.others, o)
			parts = append(parts, o.Agent+" "+ui.Icon(o.Status))
		}
	}
	f.Others = strings.Join(parts, " · ")
	if s, ok := mascots.All[agent]; ok {
		f.cells = crop(s.Cells(mode, n, ui.MascotEffects(row)), p.opts.Cols, p.opts.Rows)
	}
	f.Raster = EncodeRaster(f.cells)
	if p.opts.All {
		for _, o := range vis {
			f.Pets = append(f.Pets, p.own(o, now))
		}
	}
	return f
}

// own is one agent's mascot as itself: its status pose and only its own
// reactions (the pet in Frame cheers for everyone; these do not).
func (p *Pet) own(o state.Row, now time.Time) PetInfo {
	mode, n := ui.StatusMode(o, now), 0
	if !p.opts.NoAnimation {
		n = int(now.UnixMilli() / FrameEvery.Milliseconds())
		if r, ok := p.active(o.Agent, now); ok {
			mode, n = r.mode, int(now.Sub(r.at)/FrameEvery)
		} else if !o.End.IsZero() && !now.Before(o.End) && now.Sub(o.End) < JustNow {
			switch o.Status {
			case state.Done:
				mode, n = mascots.Celebrate, int(now.Sub(o.End)/FrameEvery)
			case state.Failed:
				mode, n = mascots.Scared, int(now.Sub(o.End)/FrameEvery)
			}
		}
	}
	pi := PetInfo{
		Name:   o.Agent,
		Status: o.Status.String(),
		Glyph:  ui.Icon(o.Status),
		Color:  Hex(ui.StatusColor(o.Status)),
		Mood:   MoodOf(mode),
		Line:   Line(o, now),
	}
	if s, ok := mascots.All[o.Agent]; ok {
		pi.Raster = EncodeRaster(crop(s.Cells(mode, n, ui.MascotEffects(o)), p.opts.Cols, p.opts.Rows))
	}
	return pi
}

// Line: "claude ● orchestrating · 12 min" while it works (time since it
// started), "codex ✔ done · 3 min ago" after a run, "agy ○ idle" otherwise.
func Line(f state.Row, now time.Time) string {
	return strings.Join(lineParts(f, now), " ")
}

// lineParts: name, glyph, status and, if known, "· time".
func lineParts(f state.Row, now time.Time) []string {
	parts := []string{f.Agent, ui.Icon(f.Status), f.Status.String()}
	ago := func(t time.Time) string { return readers.Ago(max(0, now.Sub(t))) }
	switch {
	case (f.Status == state.Working || f.Status == state.Orchestrating || f.Status == state.Stuck) && !f.Since.IsZero():
		parts = append(parts, "· "+ago(f.Since))
	case !f.End.IsZero():
		parts = append(parts, "· "+ago(f.End)+" ago")
	}
	return parts
}

// crop keeps at most cols×rows cells: centered horizontally (the sprites
// have empty sides) and from the top (the effects float above the head;
// the feet go first). 0 = no limit.
func crop(cells [][]mascots.Cell, cols, rows int) [][]mascots.Cell {
	if rows > 0 && rows < len(cells) {
		cells = cells[:rows]
	}
	if len(cells) == 0 {
		return cells
	}
	w := len(cells[0])
	if cols <= 0 || cols >= w {
		return cells
	}
	x0 := (w - cols) / 2
	out := make([][]mascots.Cell, len(cells))
	for i, line := range cells {
		out[i] = line[x0 : x0+cols]
	}
	return out
}
