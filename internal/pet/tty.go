package pet

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/state"
	"github.com/AlbertoVasquezR/panal/internal/ui"
)

// The terminal view: the mascot centered, its status line under it and one
// line with the others. Made for a split pane of ~24 columns.

type (
	dataTick  time.Time
	frameTick time.Time
)

type model struct {
	pet           *Pet
	readers       []readers.Reader
	rows          []state.Row
	every         time.Duration
	now           func() time.Time
	noAnim        bool
	width, height int
}

func newModel(p *Pet, ls []readers.Reader, every time.Duration, now func() time.Time) model {
	m := model{pet: p, readers: ls, every: every, now: now, noAnim: p.opts.NoAnimation, width: 24, height: 10}
	m.read()
	return m
}

// read re-reads every agent (the cheap readers: files only).
func (m *model) read() {
	m.rows = readAll(m.readers)
	m.pet.Observe(m.rows, m.now())
}

func readAll(ls []readers.Reader) []state.Row {
	rows := make([]state.Row, 0, len(ls))
	for _, l := range ls {
		rows = append(rows, l.Read())
	}
	return rows
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.SetWindowTitle("Panal pet"), waitData(m.every)}
	if !m.noAnim {
		cmds = append(cmds, waitFrame())
	}
	return tea.Batch(cmds...)
}

func waitData(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return dataTick(t) })
}

func waitFrame() tea.Cmd {
	return tea.Tick(FrameEvery, func(t time.Time) tea.Msg { return frameTick(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dataTick:
		m.read()
		return m, waitData(m.every)
	case frameTick:
		return m, waitFrame()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Sequence(tea.SetWindowTitle(""), tea.Quit)
		}
	}
	return m, nil
}

func (m model) View() string {
	return View(m.pet.Frame(m.rows, m.now()), m.width, m.height)
}

// View draws a frame in a width×height terminal. What goes first when it
// does not fit: the blank line, then the mascot (below 12×7), then the
// others' line; the pet's status line stays.
func View(f Frame, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	status := statusLine(f, width)
	others := othersLine(f, width)
	var lines []string
	textLines := 1
	if others != "" && height >= 2 {
		textLines = 2
	}
	mascotRows := len(f.cells)
	if mascotRows > 0 && !ui.NoColor() && width >= len(f.cells[0]) && height >= mascotRows+textLines {
		lines = append(lines, strings.Split(renderCells(f), "\n")...)
		if height >= mascotRows+textLines+1 {
			lines = append(lines, "")
		}
	}
	lines = append(lines, status)
	if textLines == 2 {
		lines = append(lines, others)
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, strings.Join(lines, "\n"))
}

// renderCells draws the raster with colors, like the dashboard's mascots.
func renderCells(f Frame) string {
	var b strings.Builder
	for i, line := range f.cells {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, c := range line {
			if c.FG == "" && c.BG == "" {
				b.WriteRune(c.Char)
				continue
			}
			st := lipgloss.NewStyle().Foreground(lipgloss.Color(c.FG))
			if c.BG != "" {
				st = st.Background(lipgloss.Color(c.BG))
			}
			b.WriteString(st.Render(string(c.Char)))
		}
	}
	return b.String()
}

// statusLine: "claude ● orchestrating · 12 min" with colors; shorter
// versions when it does not fit (without the time, the word cut with "…",
// without the word), and at last the name cut with "…".
func statusLine(f Frame, width int) string {
	name := lipgloss.NewStyle().Bold(true).Foreground(ui.AgentColor(f.row.Agent))
	stc := lipgloss.NewStyle().Foreground(ui.StatusColor(f.row.Status))
	dim := lipgloss.NewStyle().Foreground(ui.DimColor())
	word, suffix := f.row.Status.String(), f.suffix
	glyph := ui.Icon(f.row.Status)
	variants := [][3]string{
		{f.row.Agent, glyph + " " + word, suffix},
		{f.row.Agent, glyph + " " + word, ""},
	}
	// The word cut short still says more than the glyph alone.
	if room := width - lipgloss.Width(f.row.Agent+" "+glyph+" "); room >= 5 {
		variants = append(variants, [3]string{f.row.Agent, glyph + " " + cut(word, room), ""})
	}
	variants = append(variants, [3]string{f.row.Agent, glyph, ""})
	for _, v := range variants {
		plain := v[0] + " " + v[1]
		if v[2] != "" {
			plain += " " + v[2]
		}
		if lipgloss.Width(plain) <= width {
			out := name.Render(v[0]) + " " + stc.Render(v[1])
			if v[2] != "" {
				out += " " + dim.Render(v[2])
			}
			return out
		}
	}
	return name.Render(cut(f.row.Agent+" "+glyph, width))
}

// othersLine: "agy ✔ · codex ◐ · opencode ○"; without the dots if it does
// not fit, then the last agents give way to "…".
func othersLine(f Frame, width int) string {
	if len(f.others) == 0 {
		return ""
	}
	type item struct{ plain, styled string }
	items := make([]item, len(f.others))
	for i, o := range f.others {
		g := ui.Icon(o.Status)
		items[i] = item{o.Agent + " " + g, o.Agent + " " + lipgloss.NewStyle().Foreground(ui.StatusColor(o.Status)).Render(g)}
	}
	dim := lipgloss.NewStyle().Foreground(ui.DimColor())
	join := func(n int, sep string, more bool) (string, string) {
		var ps, ss []string
		for _, it := range items[:n] {
			ps, ss = append(ps, it.plain), append(ss, it.styled)
		}
		plain, styled := strings.Join(ps, sep), strings.Join(ss, dim.Render(sep))
		if more {
			plain, styled = plain+" …", styled+dim.Render(" …")
		}
		return plain, styled
	}
	for _, sep := range []string{" · ", " "} {
		if p, s := join(len(items), sep, false); lipgloss.Width(p) <= width {
			return s
		}
	}
	for n := len(items) - 1; n >= 1; n-- {
		if p, s := join(n, " ", true); lipgloss.Width(p) <= width {
			return s
		}
	}
	return dim.Render(cut(items[0].plain, width))
}

// cut trims s to width columns, ending in "…".
func cut(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return strings.Repeat("…", max(width, 0))
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > width {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
