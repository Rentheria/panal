package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/remote"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

// NoAnimation (-no-animation or PANAL_NO_ANIMATION): the mascots stay still,
// with no reactions and no animation pulse. For SSH, slow terminals or anyone
// who doesn't want motion.
var NoAnimation = os.Getenv("PANAL_NO_ANIMATION") != ""

// noColor: NO_COLOR (no-color.org). Without color the mascots, made of
// colored half blocks, turn into blobs, so they are not drawn. The status is
// still shown with glyph and word.
func noColor() bool { return os.Getenv("NO_COLOR") != "" }

// Themes: -theme or PANAL_THEME.
var Themes = []string{"auto", "dark", "light", "contrast"}

// ApplyTheme sets the palette before the UI opens. "auto" lets the terminal
// say whether its background is dark; "dark" and "light" force it (when the
// detection is wrong); "contrast" raises the contrast of dim text, borders and
// empty bars.
func ApplyTheme(t string) error {
	switch t {
	case "", "auto":
	case "dark":
		lipgloss.SetHasDarkBackground(true)
	case "light":
		lipgloss.SetHasDarkBackground(false)
	case "contrast":
		cDim = lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
		cBorder = lipgloss.AdaptiveColor{Light: "238", Dark: "250"}
		cEmpty = lipgloss.AdaptiveColor{Light: "244", Dark: "243"}
		cSelBg = lipgloss.AdaptiveColor{Light: "251", Dark: "239"}
		dim = lipgloss.NewStyle().Foreground(cDim)
	default:
		return fmt.Errorf("unknown theme %q: %s", t, strings.Join(Themes, ", "))
	}
	return nil
}

// StatusLine is the status on one line, for Claude Code's status line, tmux
// or the terminal bar: "claude ● · agy ✔ 15% · codex ✔ 100% · opencode ◐".
// Each agent with its status glyph and, if known, its highest quota. Disabled
// agents at rest are left out.
func StatusLine(ls []readers.Reader) string {
	offs := currentDisabled()
	var parts []string
	for _, l := range ls {
		f := l.Read()
		if offs[f.Agent] && atRest(f.Status) {
			continue
		}
		p := f.Agent + " " + icon(f.Status)
		highest := -1.0
		for _, b := range f.Quota.Bars {
			highest = max(highest, b.UsedPct)
		}
		if highest >= 0 {
			p += fmt.Sprintf(" %.0f%%", min(highest, 100))
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " · ")
}

// atRest: a disabled agent in this state goes on the "⏻ off" line instead of
// getting a card.
func atRest(e state.Status) bool {
	return e != state.Working && e != state.Stuck
}

// windowTitle sums up the status for the tab or the taskbar, so it shows even
// when the window is in the background: what needs attention first.
func windowTitle(rows []state.Row) string {
	var nWorking, problems, unseen int
	for _, f := range rows {
		switch f.Status {
		case state.Working:
			nWorking++
		case state.Failed, state.Stuck, state.OutOfQuota, state.NoPermission:
			problems++
		}
		if f.Unseen {
			unseen++
		}
	}
	var parts []string
	if unseen > 0 {
		parts = append(parts, fmt.Sprintf("✦ %d unseen", unseen))
	}
	if nWorking > 0 {
		parts = append(parts, fmt.Sprintf("● %d working", nWorking))
	}
	if problems > 0 {
		parts = append(parts, fmt.Sprintf("✖ %d with problems", problems))
	}
	if len(parts) == 0 {
		return "Panal"
	}
	return strings.Join(parts, " · ") + " — Panal"
}

// titleCmd sets the window title if it changed (nil otherwise).
func (m *Model) titleCmd() tea.Cmd {
	t := windowTitle(m.rows)
	if t == m.title {
		return nil
	}
	m.title = t
	return tea.SetWindowTitle(t)
}

// quit clears the window title before exiting.
func quit() tea.Cmd { return tea.Sequence(tea.SetWindowTitle(""), tea.Quit) }

// Diagnostics says, as text, what the dashboard found: the config file, each
// source (whether it exists and how many files it has), the status it read for
// each agent, the models each CLI listed (lines from models.DoctorLines)
// and what affects how it looks. For setting it up on another
// machine or understanding why a card is empty.
func Diagnostics(ls []readers.Reader, confPath string, live, models []string) string {
	var b strings.Builder
	exists := func(r string) (string, bool) {
		info, err := os.Stat(r)
		if err != nil {
			return "missing", false
		}
		if info.IsDir() {
			entries, _ := os.ReadDir(r)
			if len(entries) == 1 {
				return "1 item", true
			}
			return fmt.Sprintf("%d items", len(entries)), true
		}
		return readers.Ago(time.Since(info.ModTime())) + " ago", true
	}
	mark := func(ok bool) string {
		if ok {
			return "✔"
		}
		return "○"
	}

	b.WriteString("panal · doctor\n\n")
	txt, ok := exists(confPath)
	fmt.Fprintf(&b, "%s config  %s (%s; PANAL_CONF moves it)\n\n", mark(ok), confPath, txt)

	b.WriteString("sources\n")
	fs := readers.Sources(ls)
	width := 0
	for _, f := range fs {
		width = max(width, len([]rune(f.What)))
	}
	for _, f := range fs {
		txt, ok := exists(f.Path)
		if f.Env != "" {
			txt += "; " + f.Env + " moves it"
		}
		fmt.Fprintf(&b, "  %s %-*s  %s (%s)\n", mark(ok), width, f.What, f.Path, txt)
	}

	if len(live) > 0 {
		b.WriteString("\nlive quota (free: each agent is asked)\n")
		for _, l := range live {
			b.WriteString("  " + l + "\n")
		}
	}

	if len(models) > 0 {
		b.WriteString("\nmodels (panal models; what the router may pick)\n")
		for _, l := range models {
			b.WriteString("  " + l + "\n")
		}
	}

	b.WriteString("\nwhat it read\n")
	for _, l := range ls {
		f := l.Read()
		fmt.Fprintf(&b, "  %-9s %s %s", f.Agent, icon(f.Status), f.Status)
		if f.Model != "" {
			fmt.Fprintf(&b, " · %s", f.Model)
		}
		for _, br := range f.Quota.Bars {
			fmt.Fprintf(&b, " · %s %.0f%%", barLabel(br.Name), br.UsedPct)
		}
		if f.Quota.Credits != "" {
			fmt.Fprintf(&b, " · %s credits", f.Quota.Credits)
		}
		if f.Error != "" {
			fmt.Fprintf(&b, " · error: %s", f.Error)
		}
		b.WriteString("\n")
	}

	b.WriteString("\nscreen\n")
	fmt.Fprintf(&b, "  colors: %s", map[bool]string{true: "no (NO_COLOR)", false: "yes"}[noColor()])
	fmt.Fprintf(&b, " · animation: %s", map[bool]string{true: "no", false: "yes"}[NoAnimation])
	fmt.Fprintf(&b, " · dark background: %s\n", map[bool]string{true: "yes", false: "no"}[lipgloss.HasDarkBackground()])
	return b.String()
}

// Remotes: the other machines being read (machine = … in panal.conf).
var Remotes interface{ Readings() []remote.Reading }

// remoteLines: one line per machine with its agents, below the cards:
// "⇄ pc2 · codex ● 12 min › $ go test · agy ✔ · 5 s ago".
func remoteLines(now time.Time, width int) string {
	if Remotes == nil {
		return ""
	}
	dimS := lipgloss.NewStyle().Foreground(cDim)
	var b strings.Builder
	for _, l := range Remotes.Readings() {
		head := lipgloss.NewStyle().Bold(true).Render("⇄ " + l.Machine.Name)
		if l.At.IsZero() {
			b.WriteString(" " + head + dimS.Render(" · "+l.Error) + "\n")
			continue
		}
		var parts []string
		for _, a := range l.Status.Agents {
			if a.Status == state.NoData.String() {
				continue
			}
			p := a.Agent + " " + a.Glyph
			if a.Status == state.Working.String() && !a.Since.IsZero() {
				p += " " + readers.Ago(now.Sub(a.Since))
				if a.Activity != "" {
					p += " › " + a.Activity
				}
			}
			parts = append(parts, p)
		}
		tail := " · " + readers.Ago(now.Sub(l.At)) + " ago"
		if l.Error != "" {
			tail += " (" + l.Error + ")"
		}
		line := strings.Join(parts, " · ")
		avail := width - 2 - lipgloss.Width(head) - 3 - lipgloss.Width(tail)
		b.WriteString(" " + head + " " + truncate(line, max(avail, 8)) + dimS.Render(tail) + "\n")
	}
	return b.String()
}
