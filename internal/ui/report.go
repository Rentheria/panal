package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/report"
)

// reportDaysOrDefault returns the report period: 30 if 30 was asked for, 7 by default.
func (m Model) reportDaysOrDefault() int {
	if m.reportDays == 30 {
		return 30
	}
	return 7
}

// reportBarColor gives the color of the small success bar: green from 80 %,
// amber from 50 %, red below.
func reportBarColor(pct int) lipgloss.TerminalColor {
	switch {
	case pct >= 80:
		return cGreen
	case pct >= 50:
		return cAmber
	default:
		return cRed
	}
}

// barCell draws a small 10-cell bar with the percentage and the figure.
func barCell(pct, width int) string {
	if width <= 0 {
		return ""
	}
	col := reportBarColor(pct)
	if width < 16 {
		return cell(fmt.Sprintf("%d%%", pct), width, lipgloss.NewStyle().Foreground(col))
	}
	filled := int(math.Round(float64(pct) / 10.0))
	if filled > 10 {
		filled = 10
	} else if filled < 0 {
		filled = 0
	}
	bar := lipgloss.NewStyle().Foreground(col).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(cEmpty).Render(strings.Repeat("░", 10-filled))
	figure := lipgloss.NewStyle().Foreground(col).Render(fmt.Sprintf("%3d%%", pct))
	content := bar + " " + figure
	pad := max(0, width-1-15)
	return content + strings.Repeat(" ", pad) + " "
}

// reportColumns splits the width between the report columns. Tokens and
// credits only show if some agent has them. In narrow terminals columns are
// dropped right to left (tokens, credits, median, broke, out of quota, failed).
func reportColumns(ps []report.AgentStats, width int) []int {
	tokens, credits := 0, 0
	for _, p := range ps {
		if p.Tokens > 0 {
			tokens = 11
		}
		if p.Credits > 0 {
			credits = 10
		}
	}
	// Columns: agent, runs, %, failed, out of quota, broke, median, credits, tokens.
	w := []int{10, 10, 16, 8, 11, 8, 12, credits, tokens}
	dropOrder := []int{8, 7, 6, 5, 4, 3}
	sum := func() int {
		s := 2 // left margin "  "
		for _, x := range w {
			s += x
		}
		return s
	}
	for _, col := range dropOrder {
		if sum() > width {
			w[col] = 0
		}
	}
	if sum() > width && w[1] > 8 {
		w[1] = max(8, w[1]-(sum()-width))
	}
	if sum() > width && w[0] > 8 {
		w[0] = max(8, w[0]-(sum()-width))
	}
	return w
}

func reportHeaderRow(w []int) string {
	t := lipgloss.NewStyle().Foreground(cDim)
	return "  " + cell("AGENT", w[0], t) +
		cell("RUNS", w[1], t) +
		cell("% DONE OK", w[2], t) +
		cell("FAILED", w[3], t) +
		cell("NO QUOTA", w[4], t) +
		cell("BROKE", w[5], t) +
		cell("MEDIAN", w[6], t) +
		cell("CREDITS", w[7], t) +
		cell("TOKENS", w[8], t)
}

func reportAgentRow(p report.AgentStats, w []int) string {
	normal := lipgloss.NewStyle()
	pct := 0
	if p.Runs > 0 {
		pct = p.Finished * 100 / p.Runs
	}
	stBroke := normal
	if p.Violations > 0 {
		stBroke = lipgloss.NewStyle().Foreground(cRed)
	}
	mid := "—"
	if p.Finished > 0 {
		mid = readers.Ago(p.Median)
	}
	return "  " +
		cell(p.Agent, w[0], lipgloss.NewStyle().Foreground(agentColor(p.Agent)).Bold(true)) +
		cell(strconv.Itoa(p.Runs), w[1], normal) +
		barCell(pct, w[2]) +
		cell(strconv.Itoa(p.Failed), w[3], normal) +
		cell(strconv.Itoa(p.OutOfQuota), w[4], normal) +
		cell(strconv.Itoa(p.Violations), w[5], stBroke) +
		cell(mid, w[6], normal) +
		cell(strconv.Itoa(p.Credits), w[7], normal) +
		cell(formatTokens(p.Tokens), w[8], normal)
}

// reportView draws the per-agent summary full screen.
func (m Model) reportView() string {
	width := max(m.width, 40)
	days := m.reportDaysOrDefault()
	now := m.now
	if now.IsZero() {
		now = nowFn()
	}
	from := now.AddDate(0, 0, -days)
	ps := report.Compute(m.runs, from, m.violation)

	head := Header(m.rows, now, width, "report") + "\n" +
		tabBar("report", width) + "\n" +
		m.alertBand(width)
	footer := Footer(width, "←→", "period", "tab", "toggle", "esc", "back", "?", "help", "q", "quit")

	var b strings.Builder
	b.WriteString(head)
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("  REPORT · last %d days", days)) + "\n\n")

	dimS := lipgloss.NewStyle().Foreground(cDim)
	if len(ps) == 0 {
		b.WriteString(dimS.Render(fmt.Sprintf("  no runs in the last %d days", days)) + "\n")
	} else {
		w := reportColumns(ps, width)
		b.WriteString(reportHeaderRow(w) + "\n")
		for _, p := range ps {
			b.WriteString(reportAgentRow(p, w) + "\n")
		}
		b.WriteString("\n" + dimS.Render(truncate("  broke only counts runs already checked (opened with tab or d)", width)) + "\n")
		b.WriteString("\n" + reportMatrix(report.ComputeMatrix(m.runs, from, m.violation), width))
		b.WriteString("\n" + savingsLine(report.ComputeSavings(m.runs, from, ClaudePrice), width) + "\n")
	}

	return clipHeight(b.String(), m.screenHeight()-lipgloss.Height(footer)) + footer
}

// ClaudePrice: dollars per million tokens had Claude done that work
// ("claude_price" in panal.conf). 0 = no dollar estimate.
var ClaudePrice float64

// reportMatrix: agent × task type, "ok/runs" in each cell, green from 80 %,
// amber from 50 %, red below. It shows which agent is best for what. Columns
// that don't fit are counted at the end.
func reportMatrix(mx report.Matrix, width int) string {
	if len(mx.Agents) == 0 {
		return ""
	}
	dimS := lipgloss.NewStyle().Foreground(cDim)
	const wAg, wCol = 12, 11
	fit := max((width-2-wAg)/wCol, 1)
	kinds := mx.Types
	rest := 0
	if len(kinds) > fit {
		kinds, rest = kinds[:fit], len(kinds)-fit
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("  BY TASK TYPE") + dimS.Render(" · no failure / runs") + "\n")
	row := "  " + cell("", wAg, dimS)
	for _, t := range kinds {
		row += cell(t, wCol, dimS)
	}
	b.WriteString(row + "\n")
	for _, a := range mx.Agents {
		row := "  " + cell(a, wAg, lipgloss.NewStyle().Foreground(agentColor(a)).Bold(true))
		for _, t := range kinds {
			ce := mx.Cells[a][t]
			if ce == nil {
				row += cell("·", wCol, lipgloss.NewStyle().Foreground(cEmpty))
				continue
			}
			c := cRed
			switch r := float64(ce.Clean) / float64(ce.Runs); {
			case r >= 0.8:
				c = cGreen
			case r >= 0.5:
				c = cAmber
			}
			row += cell(fmt.Sprintf("%d/%d", ce.Clean, ce.Runs), wCol, lipgloss.NewStyle().Foreground(c))
		}
		b.WriteString(row + "\n")
	}
	if rest > 0 {
		b.WriteString(dimS.Render(fmt.Sprintf("  +%d more types (widen the terminal or use -report)", rest)) + "\n")
	}
	return b.String()
}

// savingsLine: how much work the cheaper agents did instead of Claude.
func savingsLine(a report.Savings, width int) string {
	if a.Runs == 0 {
		return ""
	}
	t := "  delegated: " + plural(a.Runs, "run") + " done"
	if a.Tokens > 0 {
		t += fmt.Sprintf(" · %s tokens Claude didn't spend", formatTokens(a.Tokens))
	}
	if a.Dollars > 0 {
		t += fmt.Sprintf(" · ≈ $%.2f at Claude prices", a.Dollars)
	}
	return lipgloss.NewStyle().Foreground(cDim).Render(truncate(t, width))
}
