package ui

import (
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

// distribute fits a table's column widths to the available width. base is
// each column's desired width and minimum the narrowest it can shrink to (0:
// it can be dropped). The flex column (the task) gets what is left, aiming for
// at least minFlex. If it doesn't fit, columns shrink in the given order and,
// if it still doesn't fit, they are dropped in that order.
func distribute(base, minimum []int, flex, minFlex, total int, order []int) []int {
	w := append([]int(nil), base...)
	w[flex] = 0
	sum := func() int {
		s := 0
		for _, x := range w {
			s += x
		}
		return s
	}
	for _, i := range order {
		if missing := sum() + minFlex - total; missing > 0 {
			w[i] -= min(missing, w[i]-minimum[i])
		}
	}
	for _, i := range order {
		if sum()+minFlex > total && minimum[i] == 0 {
			w[i] = 0
		}
	}
	w[flex] = max(0, total-sum())
	return w
}

// cell truncates plain text to the column, pads it and then colors it:
// truncating colored text can split a color code. Width 0 = dropped column.
// It leaves one space before the next column.
func cell(text string, width int, st lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	avail := width - 1
	if lipgloss.Width(text) <= avail {
		return st.Render(text) + strings.Repeat(" ", max(0, avail-lipgloss.Width(text))) + " "
	}
	t := truncate(text, avail)
	if strings.Contains(text, "\x1b") && !strings.HasSuffix(t, "\x1b[0m") {
		t += "\x1b[0m"
	}
	return st.Render(t) + " "
}

// The phrases below are matched against task text written by the user, which
// may be in Spanish or English, so both languages are listed.

// Phrases almost every task starts with: they say where and under which rules
// to work, not what to do. Lists drop them so the actual task shows.
// "Solo puedes modificar x.py" / "You can only modify x.py" is not here: it
// says which files it touches, and that is useful.
// The Spanish entries match tasks written in Spanish.
var openers = []string{
	"trabajas en ", "solo lectura", "no hagas ", "no modifiques ", "es un programa ",
	"you work in ", "you are working in ", "read-only", "read only", "do not ", "don't ", "it is a program ",
}

// Rule prefixes stripped from tasks in lists (the Spanish ones match tasks
// written in Spanish).
var prefixRules = []string{
	"solo puedes",
	"solo lectura",
	"no hagas",
	"no modifiques",
	"no toques",
	"trabajas en",
	"lee ",
	"reglas:",
	"al terminar",
	"termina con",
	"es un programa",
	"you can only",
	"you may only",
	"read-only",
	"read only",
	"do not",
	"don't",
	"you work in",
	"you are working in",
	"read ",
	"rules:",
	"when done",
	"when you finish",
	"finish with",
	"it is a program",
}

// Labels that open the goal line without saying anything by themselves
// ("Problem: the samples…"): they are dropped and the sentence stays.
// The Spanish ones match tasks written in Spanish.
var titleLabels = []string{
	"problema:", "contexto:", "objetivo general:", "objetivo:", "tarea:",
	"problem:", "context:", "overall goal:", "goal:", "objective:", "task:",
}

// goalLabels introduce the line that holds the task's goal ("objetivo" matches
// tasks written in Spanish).
var goalLabels = []string{"objetivo general:", "objetivo:", "overall goal:", "goal:", "objective:"}

// taskTitle extracts the real task from the task text, dropping rules and
// openers. It uses the full text if there is one (otherwise the first line).
// If a line with "Goal:" (or similar) remains, the title is what follows.
// Otherwise the first remaining sentence. If nothing is left, summarizeTask(leading).
func taskTitle(full, leading string) string {
	text := full
	if strings.TrimSpace(text) == "" {
		text = leading
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	var remaining []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		for {
			lower := strings.ToLower(l)
			drop := false
			for _, p := range prefixRules {
				if strings.HasPrefix(lower, p) {
					drop = true
					break
				}
			}
			if !drop {
				break
			}
			i := sentenceEnd(l)
			if i < 0 {
				l = ""
				break
			}
			l = strings.TrimSpace(l[i:])
		}
		if l != "" {
			remaining = append(remaining, l)
		}
	}

	for _, l := range remaining {
		lower := strings.ToLower(l)
		var nxt string
		for _, g := range goalLabels {
			if strings.HasPrefix(lower, g) {
				nxt = strings.TrimSpace(l[len(g):])
				break
			}
		}
		if nxt != "" {
			return capitalize(noBreaks(nxt))
		}
	}

	if len(remaining) > 0 {
		l := remaining[0]
		for _, e := range titleLabels {
			if strings.HasPrefix(strings.ToLower(l), e) {
				l = strings.TrimSpace(l[len(e):])
				break
			}
		}
		if phrase := firstSentence(l); phrase != "" {
			return capitalize(noBreaks(phrase))
		}
	}

	return noBreaks(summarizeTask(leading))
}

// capitalize: the title starts with a capital letter even when it comes from
// mid-sentence ("Overall goal: that the screens…").
func capitalize(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(unicode.ToUpper(r[0])) + string(r[1:])
}

// shortAgo: "21 h" instead of "21 h 46 min"; past the hour, the minutes don't
// help place the run and make the line wrap.
func shortAgo(d time.Duration) string {
	s := readers.Ago(d)
	if i := strings.Index(s, " h "); i >= 0 {
		return s[:i+2]
	}
	return s
}

func noBreaks(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

// firstSentence returns the first sentence of s (up to ". ", a trailing ": " or the end of the line).
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	depth := 0
	for i := 0; i+1 < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '.':
			if depth <= 0 && s[i+1] == ' ' {
				if i == 1 && s[0] >= '0' && s[0] <= '9' {
					continue
				}
				if i > 1 && s[i-1] >= '0' && s[i-1] <= '9' && (s[i-2] == ' ' || s[i-2] == '\t') {
					continue
				}
				return strings.TrimSpace(s[:i+1])
			}
		case ':':
			if depth <= 0 && (i+2 == len(s) || strings.TrimSpace(s[i+1:]) == "") {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return s
}

// shortProject returns the last component of the path (C:\Codigo\wt\tui-panel → "tui-panel");
// empty if there is no dir.
func shortProject(dir string) string {
	c := strings.TrimRight(dir, `/\`)
	if c == "" {
		return ""
	}
	if i := strings.LastIndexAny(c, `/\`); i >= 0 {
		return c[i+1:]
	}
	return c
}

// shortModel simplifies long model names for lists (cards, table, history).
func shortModel(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "opencode/") {
		s = strings.TrimPrefix(s, "opencode/")
	}
	if strings.HasPrefix(s, "gpt-6-") {
		s = strings.TrimPrefix(s, "gpt-6-")
	}
	if strings.HasPrefix(s, "gemini-") {
		for _, suffix := range []string{"-medium", "-low", "-high"} {
			if strings.HasSuffix(s, suffix) {
				s = strings.TrimSuffix(s, suffix)
				break
			}
		}
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "opus ") || strings.HasPrefix(lower, "sonnet ") ||
		strings.HasPrefix(lower, "haiku ") || strings.Contains(lower, "context)") {
		base := strings.Split(s, " · ")[0]
		if i := strings.Index(base, " ("); i >= 0 {
			base = base[:i]
		}
		s = strings.ToLower(strings.TrimSpace(base))
	}
	return s
}

// taskCell formats the task cell for lists: "<project> · <title>" with the
// project dimmed, keeping the column width without breaking ANSI sequences.
func taskCell(project, title string, width int) string {
	if width <= 0 {
		return ""
	}
	avail := width - 1
	if avail <= 0 {
		return strings.Repeat(" ", width)
	}
	dimS := lipgloss.NewStyle().Foreground(cDim)
	if project == "" {
		t := truncate(title, avail)
		pad := strings.Repeat(" ", max(0, avail-lipgloss.Width(t)))
		return t + pad + " "
	}
	sep := " · "
	fixedWidth := lipgloss.Width(project) + lipgloss.Width(sep)
	if fixedWidth+1 <= avail {
		shortTitle := truncate(title, avail-fixedWidth)
		used := fixedWidth + lipgloss.Width(shortTitle)
		pad := strings.Repeat(" ", max(0, avail-used))
		return dimS.Render(project+sep) + shortTitle + pad + " "
	}
	pShort := truncate(project, avail)
	pad := strings.Repeat(" ", max(0, avail-lipgloss.Width(pShort)))
	return dimS.Render(pShort) + pad + " "
}

// summarizeTask drops those phrases from the start. If nothing is left, it
// returns the whole task.
func summarizeTask(s string) string {
	rest := strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	for {
		drop := false
		for _, a := range openers {
			if strings.HasPrefix(strings.ToLower(rest), a) {
				drop = true
				break
			}
		}
		if !drop {
			break
		}
		i := sentenceEnd(rest)
		if i < 0 {
			return s
		}
		rest = strings.TrimSpace(rest[i:])
	}
	if rest == "" {
		return s
	}
	return rest
}

// sentenceEnd: index right after the ". " (or ": ") that closes the first
// sentence; parentheses don't count, so "(repo x. y)" is not cut.
func sentenceEnd(s string) int {
	depth := 0
	for i := 0; i+1 < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '.', ':':
			if depth <= 0 && s[i+1] == ' ' {
				return i + 2
			}
		}
	}
	return -1
}

// truncateMiddle cuts in the middle: in a path what matters is at the start
// (the drive) and the end (the file), not the dirs in between.
func truncateMiddle(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	if n < 5 {
		return truncate(s, n)
	}
	r := []rune(s)
	left := (n - 1) / 3
	right := n - 1 - left
	return string(r[:left]) + "…" + string(r[len(r)-right:])
}

// looksLikePath: it has dir separators and no spaces. With spaces it is text
// (a task that mentions "app/a.py and app/b.py"), and that is cut at the end.
func looksLikePath(s string) bool {
	return !strings.ContainsAny(s, " \x1b") && (strings.Count(s, `\`) >= 2 || strings.Count(s, "/") >= 2)
}

// rowTime: in tables, how long it has been working; otherwise how long ago
// its last run ended ("—" only if there is none).
func rowTime(f state.Row, now time.Time) string {
	if !f.Since.IsZero() {
		return readers.Ago(now.Sub(f.Since))
	}
	if !f.End.IsZero() {
		return shortAgo(now.Sub(f.End)) + " ago"
	}
	return "—"
}
