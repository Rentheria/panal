package router

import (
	"time"

	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/report"
	"github.com/AlbertoVasquezR/panal/internal/runs"
)

// Summarize is the report's Router section: the auto decisions since a date
// (one per `panal delegate -c auto`, found by its first link), how often the
// first choice succeeded, and how many picks explored.
func Summarize(rs []history.Run, since time.Time, checks *Checks, now time.Time) report.RouterSummary {
	var s report.RouterSummary
	for _, r := range rs {
		if !r.Auto || r.Choice != 1 || r.Status == runs.Running || (!since.IsZero() && r.Start.Before(since)) {
			continue
		}
		s.Decisions++
		if r.Explored {
			s.Explored++
		} else {
			s.Exploited++
		}
		switch OutcomeOf(Observe(r, checks, now)) {
		case Success:
			s.FirstChoiceOK++
			s.FirstChoiceRuns++
		case Failure:
			s.FirstChoiceRuns++
		default:
			if r.Status == runs.OutOfQuota || r.Status == runs.Skipped {
				s.FellBack++
			}
		}
	}
	return s
}
