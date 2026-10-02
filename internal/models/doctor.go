package models

import (
	"fmt"
	"time"
)

// DoctorLines are `panal -doctor`'s lines about the catalog, one per CLI:
//
//	✔ codex: 10 models (cached 3 h ago, codex debug models)
//	○ opencode: no models · opencode api model.list listed no models; …
func DoctorLines(cat Catalog, now time.Time) []string {
	var out []string
	for _, cli := range CLIs {
		e := cat.Get(cli)
		switch {
		case e == nil:
			out = append(out, fmt.Sprintf("○ %s: not asked yet (panal models -refresh)", cli))
		case len(e.Models) > 0:
			line := fmt.Sprintf("✔ %s: %d %s (cached %s ago, %s)", cli, len(e.Models), plural(len(e.Models), "model", "models"), ago(now.Sub(e.At)), e.Command)
			if e.Error != "" {
				line += " · last refresh failed: " + e.Error
			}
			out = append(out, line)
		default:
			out = append(out, fmt.Sprintf("○ %s: no models · %s", cli, e.Error))
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ago: "40 s", "12 min", "3 h", "2 d" (like the dashboard's).
func ago(d time.Duration) string {
	switch {
	case d < 0:
		return "0 s"
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d d", int(d.Hours()/24))
}

// Ago is how long ago the CLI's catalog was cached ("3 h"), or "".
func (c Catalog) Ago(cli string, now time.Time) string {
	e := c.Get(cli)
	if e == nil || e.At.IsZero() {
		return ""
	}
	return ago(now.Sub(e.At))
}
