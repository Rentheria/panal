package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/history"
)

// Formats accepted by -report -format.
var Formats = []string{"text", "md", "csv", "json"}

// Export builds the report of the last days days in the requested format:
// text (the usual table), md (to paste in an issue, a chat or a doc), csv
// (one row per agent, for a spreadsheet) or json (everything, for another
// program).
// price: dollars per million Claude tokens (0 = not estimated).
// rs: the router's decisions in the period (nil = no Router section; csv
// never has one).
func Export(cs []history.Run, days int, now time.Time, violated func(history.Run) bool, format string, price float64, rs *RouterSummary) (string, error) {
	since := now.AddDate(0, 0, -days)
	ps := Compute(cs, since, violated)
	mx := ComputeMatrix(cs, since, violated)
	sv := ComputeSavings(cs, since, price)
	switch format {
	case "", "text":
		txt := Text(ps, days)
		if rs != nil {
			txt += "\n" + RouterText(*rs) + "\n"
		}
		return txt, nil
	case "md":
		return exportMD(ps, mx, sv, days, now, rs), nil
	case "csv":
		return exportCSV(ps)
	case "json":
		return exportJSON(ps, mx, sv, days, since, now, rs)
	}
	return "", fmt.Errorf("unknown format %q: %s", format, strings.Join(Formats, ", "))
}

func pct(a, b int) int {
	if b == 0 {
		return 0
	}
	return a * 100 / b
}

func exportMD(ps []AgentStats, mx Matrix, sv Savings, days int, now time.Time, rs *RouterSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Agents · last %d %s (as of %s)\n\n", days, plural(days, "day", "days"), now.Format("2006-01-02 15:04"))
	if len(ps) == 0 {
		b.WriteString("No runs in this period.\n")
		return b.String()
	}
	b.WriteString("| agent | runs | ok | failed | out of quota | broke | median | credits | tokens |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, p := range ps {
		fmt.Fprintf(&b, "| %s | %d | %d%% | %d | %d | %d | %s | %d | %d |\n",
			p.Agent, p.Runs, pct(p.Finished, p.Runs), p.Failed, p.OutOfQuota, p.Violations,
			shortDuration(p.Median), p.Credits, p.Tokens)
	}
	if len(mx.Types) > 0 {
		b.WriteString("\n### By task type (clean / runs)\n\n| agent | " + strings.Join(mx.Types, " | ") + " |\n|---|")
		b.WriteString(strings.Repeat("---:|", len(mx.Types)) + "\n")
		for _, a := range mx.Agents {
			b.WriteString("| " + a + " |")
			for _, t := range mx.Types {
				if ce := mx.Cells[a][t]; ce != nil {
					fmt.Fprintf(&b, " %d/%d |", ce.Clean, ce.Runs)
				} else {
					b.WriteString(" · |")
				}
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "\n**Delegated:** %d finished %s", sv.Runs, plural(sv.Runs, "run", "runs"))
	if sv.Tokens > 0 {
		fmt.Fprintf(&b, " · %d tokens Claude did not spend", sv.Tokens)
	}
	if sv.Dollars > 0 {
		fmt.Fprintf(&b, " · ≈ $%.2f at Claude's price", sv.Dollars)
	}
	b.WriteString(".\n")
	if rs != nil {
		b.WriteString("\n### Router\n\n" + RouterText(*rs) + ".\n")
	}
	b.WriteString("\n_«broke» only counts runs that were already checked._\n")
	return b.String()
}

func shortDuration(d time.Duration) string {
	if d == 0 {
		return "—"
	}
	return d.Round(time.Second).String()
}

func exportCSV(ps []AgentStats) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write([]string{"agent", "runs", "finished", "failed", "out_of_quota", "violations", "median_s", "total_s", "credits", "tokens"})
	for _, p := range ps {
		w.Write([]string{p.Agent, strconv.Itoa(p.Runs), strconv.Itoa(p.Finished), strconv.Itoa(p.Failed),
			strconv.Itoa(p.OutOfQuota), strconv.Itoa(p.Violations), strconv.Itoa(int(p.Median.Seconds())),
			strconv.Itoa(int(p.Total.Seconds())), strconv.Itoa(p.Credits), strconv.FormatInt(p.Tokens, 10)})
	}
	w.Flush()
	return buf.String(), w.Error()
}

func exportJSON(ps []AgentStats, mx Matrix, sv Savings, days int, since, now time.Time, rs *RouterSummary) (string, error) {
	type agentJSON struct {
		Agent      string  `json:"agent"`
		Runs       int     `json:"runs"`
		Finished   int     `json:"finished"`
		Failed     int     `json:"failed"`
		OutOfQuota int     `json:"out_of_quota"`
		Violations int     `json:"violations"`
		MedianS    float64 `json:"median_s"`
		TotalS     float64 `json:"total_s"`
		Credits    int     `json:"credits"`
		Tokens     int64   `json:"tokens"`
	}
	out := struct {
		Days    int                         `json:"days"`
		Since   time.Time                   `json:"since"`
		Until   time.Time                   `json:"until"`
		Agents  []agentJSON                 `json:"agents"`
		ByType  map[string]map[string]*Cell `json:"by_type"`
		Savings map[string]any              `json:"delegated"`
		Router  *RouterSummary              `json:"router,omitempty"`
	}{Days: days, Since: since, Until: now, ByType: mx.Cells, Router: rs,
		Savings: map[string]any{"runs": sv.Runs, "tokens": sv.Tokens, "estimated_dollars": sv.Dollars}}
	for _, p := range ps {
		out.Agents = append(out.Agents, agentJSON{p.Agent, p.Runs, p.Finished, p.Failed, p.OutOfQuota,
			p.Violations, p.Median.Seconds(), p.Total.Seconds(), p.Credits, p.Tokens})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	return string(b) + "\n", err
}

// RouterSummary is what the router (panal delegate -c auto) decided in the
// period. It is computed by internal/router, which knows what a success is.
type RouterSummary struct {
	Decisions       int `json:"decisions"`           // auto delegations
	FirstChoiceRuns int `json:"first_choice_signal"` // whose first choice ended in a success or a failure
	FirstChoiceOK   int `json:"first_choice_ok"`     // … in a success
	FellBack        int `json:"fell_back"`           // whose first choice was out of quota or not installed
	Explored        int `json:"explored"`            // the first choice was exploration
	Exploited       int `json:"exploited"`           // the first choice was the favorite
}

// RouterText is the Router section in one line.
func RouterText(s RouterSummary) string {
	if s.Decisions == 0 {
		return "router: no auto decisions yet (panal delegate -c auto)"
	}
	txt := fmt.Sprintf("router: %d auto %s", s.Decisions, plural(s.Decisions, "decision", "decisions"))
	if s.FirstChoiceRuns > 0 {
		txt += fmt.Sprintf(" · first choice ok %d/%d (%d%%)", s.FirstChoiceOK, s.FirstChoiceRuns, pct(s.FirstChoiceOK, s.FirstChoiceRuns))
	}
	if s.FellBack > 0 {
		txt += fmt.Sprintf(" · %d fell back (out of quota)", s.FellBack)
	}
	txt += fmt.Sprintf(" · explored %d, exploited %d", s.Explored, s.Exploited)
	return txt
}
