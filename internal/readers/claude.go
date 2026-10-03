package readers

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/activity"
	"github.com/AlbertoVasquezR/panal/internal/live"
	"github.com/AlbertoVasquezR/panal/internal/runs"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

// Claude implements Reader for the "claude" agent by reading Claude Code's
// status line JSON (it is not run by panal delegate).
type Claude struct {
	Path      string
	Live      interface{ LatestClaude() live.ClaudeReading } // nil = status line only
	Now       func() time.Time
	FileReads int

	cacheModTime time.Time
	cacheSize    int64
	cacheData    *claudeStatuslineJSON
	lastGood     *claudeStatuslineJSON // last file that parsed, for reads that land mid-write
	cacheErr     error
	cacheOk      bool
}

type claudeStatuslineJSON struct {
	SessionName    string `json:"session_name"`
	TranscriptPath string `json:"transcript_path"`
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	Version        string `json:"version"`
	Model          *struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Effort *struct {
		Level string `json:"level"`
	} `json:"effort"`
	Cost *struct {
		TotalCostUSD    *float64 `json:"total_cost_usd"`
		TotalDurationMS *int64   `json:"total_duration_ms"`
	} `json:"cost"`
	ContextWindow *struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
	PromptCache *struct {
		HitRatio *float64 `json:"hit_ratio"`
	} `json:"prompt_cache"`
	RateLimits *struct {
		FiveHour *struct {
			UsedPercentage *float64 `json:"used_percentage"`
			ResetsAt       *int64   `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay *struct {
			UsedPercentage *float64 `json:"used_percentage"`
			ResetsAt       *int64   `json:"resets_at"`
		} `json:"seven_day"`
	} `json:"rate_limits"`
}

func defaultClaudePath() string {
	if p := os.Getenv("PANAL_CLAUDE"); p != "" {
		return p
	}
	p := filepath.Join(runs.Home(), "claude", "statusline.json")
	if fileExists(p) {
		return p
	}
	// Fallback: the status line script used to write to ~/.ct-delegar
	// (delegar.sh's directory). Read it there only while the new file doesn't
	// exist, so a status line set up before the move keeps working.
	u := os.Getenv("USERPROFILE")
	if u == "" {
		u, _ = os.UserHomeDir()
	}
	if old := filepath.Join(u, ".ct-delegar", "claude", "statusline.json"); fileExists(old) {
		return old
	}
	return p
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// NewClaude creates a Reader for Claude Code pointing at the real path.
func NewClaude() *Claude {
	return &Claude{
		Path: defaultClaudePath(),
		Now:  time.Now,
	}
}

func (c *Claude) Agent() string {
	return "claude"
}

// Read is what the status line says and, if there is a newer live reading
// (get_usage), that one wins: it also counts usage from the web, other
// machines and resets.
func (c *Claude) Read() state.Row {
	row := c.readStatusLine()
	if c.Live == nil {
		return row
	}
	l := c.Live.LatestClaude()
	if !l.HasData || !l.At.After(row.Quota.SeenAt) {
		return row
	}
	row.Quota = l.Quota
	note := "live quota: from Claude Code (get_usage, same as /usage) · plan " + l.Plan
	if row.Detail != "" {
		row.Detail = note + "\n" + row.Detail
	} else {
		row.Detail = note
	}
	return row
}

func (c *Claude) readStatusLine() state.Row {
	row := state.Row{
		Agent:  "claude",
		Status: state.NoData,
	}

	if c.Path == "" {
		return row
	}

	info, err := os.Stat(c.Path)
	if err != nil {
		c.cacheOk = false
		return row
	}

	nowFn := c.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()

	var data *claudeStatuslineJSON
	if c.cacheOk && info.ModTime().Equal(c.cacheModTime) && info.Size() == c.cacheSize {
		if c.cacheErr != nil {
			row.Error = c.cacheErr.Error()
			return row
		}
		data = c.cacheData
	} else {
		c.FileReads++
		b, readErr := os.ReadFile(c.Path)
		var parsed claudeStatuslineJSON
		parseErr := readErr
		if readErr == nil {
			parseErr = json.Unmarshal(b, &parsed)
		}
		// Claude Code rewrites the file on every status-line refresh, so a
		// read can land in the middle of a write: try once more, and if it is
		// still half-written keep the last good read (without caching the
		// error, so the next refresh reads it again).
		if parseErr != nil {
			time.Sleep(30 * time.Millisecond)
			c.FileReads++
			if b, readErr = os.ReadFile(c.Path); readErr == nil {
				parsed = claudeStatuslineJSON{}
				parseErr = json.Unmarshal(b, &parsed)
			} else {
				parseErr = readErr
			}
		}
		if parseErr != nil && c.lastGood != nil {
			c.cacheOk = false
			data = c.lastGood
		} else if readErr != nil {
			c.cacheOk = true
			c.cacheModTime = info.ModTime()
			c.cacheSize = info.Size()
			c.cacheErr = readErr
			c.cacheData = nil
			row.Error = readErr.Error()
			return row
		} else if parseErr != nil {
			c.cacheOk = true
			c.cacheModTime = info.ModTime()
			c.cacheSize = info.Size()
			c.cacheErr = parseErr
			c.cacheData = nil
			row.Error = parseErr.Error()
			return row
		} else {
			data = &parsed
			c.lastGood = data
			c.cacheOk = true
			c.cacheModTime = info.ModTime()
			c.cacheSize = info.Size()
			c.cacheErr = nil
			c.cacheData = data
		}
	}

	age := now.Sub(info.ModTime())
	if age < 3*time.Minute {
		row.Status = state.Orchestrating
	} else {
		row.Status = state.Idle
	}

	var model string
	if data.Model != nil {
		model = data.Model.DisplayName
	}
	if data.Effort != nil && data.Effort.Level != "" {
		if model != "" {
			model += " · " + data.Effort.Level
		} else {
			model = data.Effort.Level
		}
	}
	row.Model = model

	row.Task = data.SessionName
	row.Dir = data.Cwd

	if data.Cost != nil && data.Cost.TotalDurationMS != nil && *data.Cost.TotalDurationMS > 0 {
		dur := time.Duration(*data.Cost.TotalDurationMS) * time.Millisecond
		row.Since = info.ModTime().Add(-dur)
	}

	if data.RateLimits == nil || (data.RateLimits.FiveHour == nil && data.RateLimits.SevenDay == nil) {
		row.Quota = state.Quota{
			Exact:   false,
			Summary: "no quota data",
			SeenAt:  info.ModTime(),
		}
	} else {
		quota := state.Quota{
			Exact:  true,
			SeenAt: info.ModTime(),
		}
		if data.RateLimits.FiveHour != nil {
			if data.RateLimits.FiveHour.UsedPercentage != nil {
				quota.UsedPct = *data.RateLimits.FiveHour.UsedPercentage
			}
			if data.RateLimits.FiveHour.ResetsAt != nil && *data.RateLimits.FiveHour.ResetsAt > 0 {
				quota.ResetsAt = time.Unix(*data.RateLimits.FiveHour.ResetsAt, 0)
			}
		}
		if data.RateLimits.SevenDay != nil {
			var weekPct string
			if data.RateLimits.SevenDay.UsedPercentage != nil {
				weekPct = fmt.Sprintf("week %d%%", int(math.Round(*data.RateLimits.SevenDay.UsedPercentage)))
			}
			if data.RateLimits.SevenDay.ResetsAt != nil && *data.RateLimits.SevenDay.ResetsAt > 0 {
				reset := time.Unix(*data.RateLimits.SevenDay.ResetsAt, 0)
				resetStr := reset.Format("02-Jan 15:04")
				if weekPct != "" {
					quota.Summary = fmt.Sprintf("%s · resets %s", weekPct, resetStr)
				} else {
					quota.Summary = fmt.Sprintf("resets %s", resetStr)
				}
			} else if weekPct != "" {
				quota.Summary = weekPct
			}
		}
		if fh := data.RateLimits.FiveHour; fh != nil && fh.UsedPercentage != nil {
			quota.Bars = append(quota.Bars, state.Bar{Name: "5h", UsedPct: *fh.UsedPercentage, ResetsAt: quota.ResetsAt})
		}
		if sd := data.RateLimits.SevenDay; sd != nil && sd.UsedPercentage != nil {
			// "sem" is the weekly bar's key across packages (state, ui, forecast).
			b := state.Bar{Name: "sem", UsedPct: *sd.UsedPercentage}
			if sd.ResetsAt != nil && *sd.ResetsAt > 0 {
				b.ResetsAt = time.Unix(*sd.ResetsAt, 0)
			}
			quota.Bars = append(quota.Bars, b)
		}
		row.Quota = quota
	}

	if data.ContextWindow != nil && data.ContextWindow.UsedPercentage != nil {
		row.HasContext, row.ContextPct = true, *data.ContextWindow.UsedPercentage
	}
	if data.Cost != nil && data.Cost.TotalCostUSD != nil {
		row.SessionCost = fmt.Sprintf("$%.2f", *data.Cost.TotalCostUSD)
	}
	if row.Status == state.Orchestrating {
		row.Activity, row.ActivityAt = activity.FromClaudeFile(data.TranscriptPath)
	}
	row.Detail = formatClaudeDetail(data, info.ModTime(), now)

	return row
}

func formatClaudeDetail(data *claudeStatuslineJSON, mtime, now time.Time) string {
	var lines []string

	var session []string
	if data.Cost != nil && data.Cost.TotalCostUSD != nil {
		session = append(session, fmt.Sprintf("cost: $%.2f", *data.Cost.TotalCostUSD))
	}
	if data.ContextWindow != nil && data.ContextWindow.UsedPercentage != nil {
		session = append(session, fmt.Sprintf("context: %d%%", int(math.Round(*data.ContextWindow.UsedPercentage))))
	}
	if data.PromptCache != nil && data.PromptCache.HitRatio != nil {
		session = append(session, fmt.Sprintf("cache hits: %d%%", int(math.Round(*data.PromptCache.HitRatio*100))))
	}
	if len(session) > 0 {
		lines = append(lines, strings.Join(session, " · "))
	}

	var info []string
	if data.Cost != nil && data.Cost.TotalDurationMS != nil && *data.Cost.TotalDurationMS > 0 {
		dur := time.Duration(*data.Cost.TotalDurationMS) * time.Millisecond
		info = append(info, fmt.Sprintf("duration: %s", Ago(dur)))
	}
	if data.Version != "" {
		info = append(info, fmt.Sprintf("version: %s", data.Version))
	}
	if len(info) > 0 {
		lines = append(lines, strings.Join(info, " · "))
	}

	if data.SessionID != "" {
		lines = append(lines, fmt.Sprintf("session_id: %s", data.SessionID))
	}

	d := now.Sub(mtime)
	if d < 0 {
		d = 0
	}
	lines = append(lines, fmt.Sprintf("seen %s ago", Ago(d)))

	return strings.Join(lines, "\n")
}
