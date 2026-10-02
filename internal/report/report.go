// Package report builds summaries and tables of the agents' activity in
// plain text and markdown.
package report

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/runs"
)

// AgentStats summarizes the activity and performance of an agent over a period.
type AgentStats struct {
	Agent      string
	Runs       int
	Finished   int
	Failed     int           // failed, timeout, interrupted
	OutOfQuota int           // out_of_quota, no_permission, skipped
	Violations int           // how many broke the task rules
	Median     time.Duration // only the finished ones
	Total      time.Duration // only the finished ones
	Tokens     int64
	Credits    int // sum of the Costs that end in " cr"
}

// Compute groups and summarizes the runs per agent starting at since.
// It only includes runs with Start >= since and leaves out the running ones.
// violated may be nil (then Violations stays at 0).
// Order: most runs first, ties by name.
func Compute(cs []history.Run, since time.Time, violated func(history.Run) bool) []AgentStats {
	type agentData struct {
		stats     AgentStats
		durations []time.Duration
	}

	order := []string{}
	agents := map[string]*agentData{}

	for _, c := range cs {
		if c.Status == runs.Running {
			continue
		}
		if !since.IsZero() && c.Start.Before(since) {
			continue
		}

		d, ok := agents[c.Agent]
		if !ok {
			d = &agentData{
				stats: AgentStats{Agent: c.Agent},
			}
			agents[c.Agent] = d
			order = append(order, c.Agent)
		}

		d.stats.Runs++

		switch c.Status {
		case runs.Done:
			d.stats.Finished++
			if !c.End.IsZero() && !c.End.Before(c.Start) {
				dur := c.End.Sub(c.Start)
				d.stats.Total += dur
				d.durations = append(d.durations, dur)
			}
		case runs.Failed, runs.Timeout, runs.Interrupted:
			d.stats.Failed++
		case runs.OutOfQuota, runs.NoPermission, runs.Skipped:
			d.stats.OutOfQuota++
		}

		if violated != nil && violated(c) {
			d.stats.Violations++
		}

		d.stats.Tokens += c.Tokens
		d.stats.Credits += parseCredits(c.Cost)
	}

	res := make([]AgentStats, 0, len(agents))
	for _, name := range order {
		d := agents[name]
		if len(d.durations) > 0 {
			sort.Slice(d.durations, func(i, j int) bool {
				return d.durations[i] < d.durations[j]
			})
			n := len(d.durations)
			if n%2 == 1 {
				d.stats.Median = d.durations[n/2]
			} else {
				d.stats.Median = (d.durations[n/2-1] + d.durations[n/2]) / 2
			}
		}
		res = append(res, d.stats)
	}

	sort.Slice(res, func(i, j int) bool {
		if res[i].Runs != res[j].Runs {
			return res[i].Runs > res[j].Runs
		}
		return res[i].Agent < res[j].Agent
	})

	return res
}

func parseCredits(cost string) int {
	if strings.HasSuffix(cost, " cr") {
		crStr := strings.TrimSpace(strings.TrimSuffix(cost, " cr"))
		if val, err := strconv.Atoi(crStr); err == nil && val > 0 {
			return val
		}
	}
	return 0
}

// Text builds an aligned plain-text table with the summary per agent.
// Columns: agent, runs, % ok, failed, out of quota, broke, median, credits, tokens.
// Title «last N days». With no data: «no runs in the last N days».
func Text(ps []AgentStats, days int) string {
	if len(ps) == 0 {
		return fmt.Sprintf("no runs in the last %d %s\n", days, plural(days, "day", "days"))
	}

	headers := []string{
		"agent",
		"runs",
		"% ok",
		"failed",
		"out of quota",
		"broke",
		"median",
		"credits",
		"tokens",
	}

	rows := make([][]string, len(ps))
	for i, p := range ps {
		pct := 0
		if p.Runs > 0 {
			pct = p.Finished * 100 / p.Runs
		}
		med := "—"
		if p.Finished > 0 {
			med = readers.Ago(p.Median)
		}
		rows[i] = []string{
			p.Agent,
			strconv.Itoa(p.Runs),
			fmt.Sprintf("%d%%", pct),
			strconv.Itoa(p.Failed),
			strconv.Itoa(p.OutOfQuota),
			strconv.Itoa(p.Violations),
			med,
			strconv.Itoa(p.Credits),
			strconv.FormatInt(p.Tokens, 10),
		}
	}

	colWidth := make([]int, len(headers))
	for i, h := range headers {
		colWidth[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if n := utf8.RuneCountInString(c); n > colWidth[i] {
				colWidth[i] = n
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "last %d %s\n", days, plural(days, "day", "days"))

	// Headers
	for i, h := range headers {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(h)
		if i < len(headers)-1 {
			padding := colWidth[i] - utf8.RuneCountInString(h)
			b.WriteString(strings.Repeat(" ", padding))
		}
	}
	b.WriteByte('\n')

	// Rows
	for _, r := range rows {
		for i, val := range r {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(val)
			if i < len(r)-1 {
				padding := colWidth[i] - utf8.RuneCountInString(val)
				b.WriteString(strings.Repeat(" ", padding))
			}
		}
		b.WriteByte('\n')
	}

	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Markdown returns the summary of the runs of the given day in markdown:
// title «# Agents — YYYY-MM-DD», a line with history.Summary and the table
// | time | agent | model | result | duration | cost | task |.
func Markdown(cs []history.Run, day time.Time, violated func(history.Run) bool) string {
	if day.IsZero() {
		day = time.Now()
	}

	loc := day.Location()
	y2, m2, d2 := day.Date()
	var ofDay []history.Run
	for _, c := range cs {
		y1, m1, d1 := c.Start.In(loc).Date()
		if y1 == y2 && m1 == m2 && d1 == d2 {
			ofDay = append(ofDay, c)
		}
	}

	// Oldest first
	sort.Slice(ofDay, func(i, j int) bool {
		return ofDay[i].Start.Before(ofDay[j].Start)
	})

	var b strings.Builder
	dateStr := day.Local().Format("2006-01-02")
	fmt.Fprintf(&b, "# Agents — %s\n\n", dateStr)
	b.WriteString(history.Summary(cs, day))
	b.WriteString("\n\n")

	b.WriteString("| time | agent | model | result | duration | cost | task |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")

	for _, c := range ofDay {
		hour := c.Start.Local().Format("15:04")
		model := c.Model
		if model == "" {
			model = "—"
		}
		res := c.Status
		if violated != nil && violated(c) {
			res = "⚠ " + res
		}
		dur := "—"
		if !c.End.IsZero() && !c.End.Before(c.Start) {
			dur = readers.Ago(c.End.Sub(c.Start))
		}
		cost := c.Cost
		if cost == "" {
			cost = "—"
		}
		task := c.Task
		task = strings.ReplaceAll(task, "\r", "")
		task = strings.ReplaceAll(task, "\n", " ")
		runes := []rune(task)
		if len(runes) > 80 {
			task = string(runes[:80])
		}
		task = strings.ReplaceAll(task, "|", `\|`)

		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			hour, c.Agent, model, res, dur, cost, task)
	}

	return b.String()
}
