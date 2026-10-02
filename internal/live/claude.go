package live

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

// Claude asks for the subscription usage with Claude Code's get_usage control
// request (the same account /usage shows): it opens `claude -p` in
// stream-json, sends only control requests (never a user message, so there
// is no turn) and closes it. It runs with hooks off (disableAllHooks) so no
// one is notified of a "new session", without saving the session, and from a
// temporary directory. If any answer says it cost something, it stops asking.
type Claude struct {
	Command  []string
	Interval time.Duration

	mu      sync.Mutex
	latest  ClaudeReading
	events  []string
	request chan struct{}
}

// ClaudeReading: the subscription windows.
type ClaudeReading struct {
	Quota   state.Quota
	Plan    string // team, max, pro…
	At      time.Time
	Error   string
	HasData bool
	Stopped bool
}

// NewClaude prepares the reader; it asks every 5 minutes (the status line
// already counts usage while Claude Code works; this covers usage from the
// web, other machines and resets).
func NewClaude() *Claude {
	return &Claude{
		Command: []string{"claude", "-p", "--input-format", "stream-json", "--output-format", "stream-json",
			"--verbose", "--no-session-persistence", "--settings", `{"disableAllHooks":true}`},
		Interval: 5 * time.Minute,
		request:  make(chan struct{}, 1),
	}
}

// LatestClaude is the latest known reading.
func (c *Claude) LatestClaude() ClaudeReading {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

// TakeEvents returns (and forgets) the early resets that were detected.
func (c *Claude) TakeEvents() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ev := c.events
	c.events = nil
	return ev
}

// Refresh asks for a reading now.
func (c *Claude) Refresh() {
	select {
	case c.request <- struct{}{}:
	default:
	}
}

// Start asks now and then every Interval. If it fails (e.g. the server
// rate-limits the queries), it waits longer before trying again.
func (c *Claude) Start(ctx context.Context) {
	wait := c.Interval
	for ctx.Err() == nil {
		l, err := c.query(ctx)
		c.mu.Lock()
		switch {
		case errors.Is(err, errSpentClaude):
			c.latest.Error, c.latest.Stopped = err.Error(), true
			c.events = append(c.events, "claude: "+err.Error())
			c.mu.Unlock()
			return
		case err != nil:
			c.latest.Error = err.Error()
			wait = min(wait*2, time.Hour)
		default:
			if c.latest.HasData {
				c.events = append(c.events, compareBars("claude", c.latest.Quota, l.Quota, l.At)...)
			}
			c.latest = l
			wait = c.Interval
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-c.request:
		}
	}
}

var errSpentClaude = errors.New("get_usage reported cost or API time: stopped asking for this session")

func (c *Claude) query(ctx context.Context) (ClaudeReading, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Command[0], c.Command[1:]...)
	cmd.Dir = os.TempDir() // no CLAUDE.md or project settings
	hideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ClaudeReading{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ClaudeReading{}, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return ClaudeReading{}, fmt.Errorf("could not start claude: %w", err)
	}
	defer func() {
		stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	for _, m := range []string{
		`{"type":"control_request","request_id":"init","request":{"subtype":"initialize"}}`,
		`{"type":"control_request","request_id":"usage","request":{"subtype":"get_usage","skip_behaviors":true}}`,
	} {
		if _, err := io.WriteString(stdin, m+"\n"); err != nil {
			return ClaudeReading{}, err
		}
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var r struct {
			Type     string `json:"type"`
			Response struct {
				RequestID string          `json:"request_id"`
				Subtype   string          `json:"subtype"`
				Error     string          `json:"error"`
				Response  json.RawMessage `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Type != "control_response" || r.Response.RequestID != "usage" {
			continue
		}
		if r.Response.Subtype != "success" {
			return ClaudeReading{}, fmt.Errorf("claude get_usage: %s", r.Response.Error)
		}
		return parseClaude(r.Response.Response, time.Now())
	}
	if ctx.Err() != nil {
		return ClaudeReading{}, errors.New("claude did not answer get_usage within 40 s")
	}
	return ClaudeReading{}, errors.New("claude exited without answering get_usage")
}

type claudeWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

func parseClaude(raw json.RawMessage, at time.Time) (ClaudeReading, error) {
	var r struct {
		Session struct {
			TotalCostUSD       float64 `json:"total_cost_usd"`
			TotalAPIDurationMS float64 `json:"total_api_duration_ms"`
		} `json:"session"`
		SubscriptionType    string `json:"subscription_type"`
		RateLimitsAvailable bool   `json:"rate_limits_available"`
		RateLimits          struct {
			FiveHour *claudeWindow `json:"five_hour"`
			SevenDay *claudeWindow `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return ClaudeReading{}, fmt.Errorf("claude get_usage returned something unexpected: %w", err)
	}
	if r.Session.TotalCostUSD > 0 || r.Session.TotalAPIDurationMS > 0 {
		return ClaudeReading{}, errSpentClaude
	}
	if !r.RateLimitsAvailable {
		return ClaudeReading{}, errors.New("claude reports no subscription limits (API key session?)")
	}
	l := ClaudeReading{Quota: state.Quota{Exact: true, SeenAt: at, Live: true}, Plan: r.SubscriptionType, At: at, HasData: true}
	// "sem" is the weekly bar's key across packages (state, ui, forecast).
	for _, v := range []struct {
		name string
		w    *claudeWindow
	}{{"5h", r.RateLimits.FiveHour}, {"sem", r.RateLimits.SevenDay}} {
		if v.w == nil || v.w.Utilization == nil {
			continue
		}
		b := state.Bar{Name: v.name, UsedPct: *v.w.Utilization}
		if v.w.ResetsAt != nil {
			if t, err := time.Parse(time.RFC3339Nano, *v.w.ResetsAt); err == nil {
				b.ResetsAt = t.Local()
			}
		}
		l.Quota.Bars = append(l.Quota.Bars, b)
	}
	if len(l.Quota.Bars) == 0 {
		return ClaudeReading{}, errors.New("claude get_usage returned no windows")
	}
	l.Quota.UsedPct, l.Quota.ResetsAt = l.Quota.Bars[0].UsedPct, l.Quota.Bars[0].ResetsAt
	return l, nil
}
