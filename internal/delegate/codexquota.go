package delegate

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/live"
	"github.com/AlbertoVasquezR/panal/internal/readers"
)

// When codex's weekly quota is used up, codex does not stop: it keeps going
// on paid credits. So before running codex, panal checks the quota the same
// way the dashboard does, without spending anything:
//
//  1. the rate_limits codex left in its newest session file
//     (~/.codex/sessions, read by internal/readers);
//  2. only if that says "used up", `codex app-server`'s
//     account/rateLimits/read (internal/live), which does not start a turn,
//     in case the quota reset early since that session.
//
// PANAL_CODEX_CREDITS=1 skips the check (spending credits is fine).

// codexWeeklyUsedUp says whether codex would bill credits now, and why.
func codexWeeklyUsedUp(now time.Time) (bool, string) {
	if os.Getenv("PANAL_CODEX_CREDITS") == "1" {
		return false, ""
	}
	r := readers.NewCodex()
	r.Delegate = nil // only the sessions, not the run files
	q := r.Read().Quota
	if !q.Exact || q.UsedPct < 100 || (!q.ResetsAt.IsZero() && !q.ResetsAt.After(now)) {
		return false, ""
	}
	why := "codex's weekly quota is used up (its last session says " + pct(q.UsedPct) + ")"
	if !q.ResetsAt.IsZero() {
		why += ", it resets " + q.ResetsAt.Local().Format("Mon 02-Jan 15:04")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cv := live.NewCodex()
	go cv.Start(ctx)
	for ctx.Err() == nil {
		if l := cv.Latest(); l.HasData {
			if l.Quota.UsedPct < 100 {
				return false, ""
			}
			break
		} else if l.Error != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true, why + "; running it would spend paid credits (PANAL_CODEX_CREDITS=1 allows it)"
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f) }
