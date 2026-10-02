package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

// Agy asks for the quota with `agy -p /usage --output-format json`: since
// 1.1.11 agy answers read-only commands in print mode without starting a
// turn, without spending quota and without leaving a conversation behind. In
// case that ever changes, every answer is checked: if it carries a turn or
// spent tokens, it stops asking and says so.
//
// Note: the argument is "/usage" as is. Launched from Git Bash it would be
// turned into a Windows path and agy would take it as a question for the
// model; from Go that does not happen, because exec does not go through the
// shell.
type Agy struct {
	Command  []string
	Interval time.Duration

	mu      sync.Mutex
	latest  AgyReading
	events  []string
	request chan struct{}
}

// AgyReading: the quota of each model group (Gemini; Claude and GPT).
type AgyReading struct {
	Groups  []Group
	At      time.Time
	Error   string
	HasData bool
	Stopped bool // stopped asking because an answer spent something
}

// Group of models that share a quota.
type Group struct {
	Name  string
	Quota state.Quota
}

// NewAgy prepares the reader; it asks every 5 minutes.
func NewAgy() *Agy {
	return &Agy{
		Command:  []string{"agy", "-p", "/usage", "--output-format", "json"},
		Interval: 5 * time.Minute,
		request:  make(chan struct{}, 1),
	}
}

// LatestAgy is the latest known reading.
func (a *Agy) LatestAgy() AgyReading {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.latest
}

// TakeEvents returns (and forgets) the early resets that were detected.
func (a *Agy) TakeEvents() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	ev := a.events
	a.events = nil
	return ev
}

// Refresh asks for a reading now.
func (a *Agy) Refresh() {
	select {
	case a.request <- struct{}{}:
	default:
	}
}

// Start asks now and then every Interval (or when asked to), until ctx is
// cancelled or an answer spent something.
func (a *Agy) Start(ctx context.Context) {
	for ctx.Err() == nil {
		l, err := a.query(ctx)
		a.mu.Lock()
		switch {
		case errors.Is(err, errSpent):
			a.latest.Error, a.latest.Stopped = err.Error(), true
			a.events = append(a.events, "agy: "+err.Error())
			a.mu.Unlock()
			return
		case err != nil:
			a.latest.Error = err.Error()
		default:
			if a.latest.HasData {
				for i, g := range l.Groups {
					if i < len(a.latest.Groups) && a.latest.Groups[i].Name == g.Name {
						a.events = append(a.events, compareBars("agy ("+g.Name+")", a.latest.Groups[i].Quota, g.Quota, l.At)...)
					}
				}
			}
			a.latest = l
		}
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.Interval):
		case <-a.request:
		}
	}
}

var errSpent = errors.New(`"/usage" started a turn and spent quota: stopped asking for this session`)

func (a *Agy) query(ctx context.Context) (AgyReading, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.Command[0], a.Command[1:]...)
	hideWindow(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return AgyReading{}, errors.New(`agy did not answer "/usage" within 45 s`)
		}
		return AgyReading{}, fmt.Errorf("agy /usage: %w", err)
	}
	return parseAgy(out.Bytes(), time.Now())
}

type agyResponse struct {
	ConversationID string `json:"conversation_id"`
	NumTurns       int    `json:"num_turns"`
	Usage          struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	Command *struct {
		Name string `json:"name"`
		Data struct {
			Groups []struct {
				Name    string `json:"name"`
				Buckets []struct {
					ID                string   `json:"id"`
					Window            string   `json:"window"`
					RemainingFraction *float64 `json:"remaining_fraction"`
					ResetTime         string   `json:"reset_time"`
				} `json:"buckets"`
			} `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

func parseAgy(b []byte, at time.Time) (AgyReading, error) {
	var r agyResponse
	if err := json.Unmarshal(bytes.TrimSpace(b), &r); err != nil {
		return AgyReading{}, fmt.Errorf("agy /usage did not return JSON: %w", err)
	}
	if r.NumTurns > 0 || r.Usage.TotalTokens > 0 || r.ConversationID != "" {
		return AgyReading{}, errSpent
	}
	if r.Command == nil || r.Command.Name != "usage" {
		return AgyReading{}, errors.New(`agy did not answer "/usage" as a command (version older than 1.1.11?)`)
	}
	l := AgyReading{At: at, HasData: true}
	for _, g := range r.Command.Data.Groups {
		q := state.Quota{Exact: true, SeenAt: at, Live: true}
		for _, bk := range g.Buckets {
			if bk.RemainingFraction == nil {
				continue
			}
			// "sem" is the weekly bar's key across packages (state, ui, forecast).
			br := state.Bar{Name: "sem", UsedPct: (1 - *bk.RemainingFraction) * 100}
			if bk.Window == "5h" || strings.Contains(bk.ID, "5h") {
				br.Name = "5h"
			}
			if t, err := time.Parse(time.RFC3339, bk.ResetTime); err == nil {
				br.ResetsAt = t.Local()
			}
			q.Bars = append(q.Bars, br)
		}
		// The 5 h one first, as in the rest of the dashboard.
		if len(q.Bars) == 2 && q.Bars[1].Name == "5h" {
			q.Bars[0], q.Bars[1] = q.Bars[1], q.Bars[0]
		}
		if len(q.Bars) > 0 {
			q.UsedPct, q.ResetsAt = q.Bars[0].UsedPct, q.Bars[0].ResetsAt
		}
		l.Groups = append(l.Groups, Group{Name: g.Name, Quota: q})
	}
	if len(l.Groups) == 0 {
		return AgyReading{}, errors.New("agy /usage returned no quota groups")
	}
	return l, nil
}

// GroupFor picks the model's group: Gemini for gemini-*, the "Claude and
// GPT" one for the rest.
func (l AgyReading) GroupFor(model string) (Group, bool) {
	want := "gemini"
	if m := strings.ToLower(strings.TrimSpace(model)); m != "" && !strings.HasPrefix(m, "gemini") {
		want = "claude"
	}
	for _, g := range l.Groups {
		if strings.Contains(strings.ToLower(g.Name), want) {
			return g, true
		}
	}
	if len(l.Groups) > 0 {
		return l.Groups[0], true
	}
	return Group{}, false
}

// Combine joins several quota askers into one, for the r key and the alerts.
func Combine(vs ...interface {
	Refresh()
	TakeEvents() []string
}) Multi {
	return Multi(vs)
}

// Multi: see Combine.
type Multi []interface {
	Refresh()
	TakeEvents() []string
}

func (v Multi) Refresh() {
	for _, x := range v {
		x.Refresh()
	}
}

func (v Multi) TakeEvents() []string {
	var out []string
	for _, x := range v {
		out = append(out, x.TakeEvents()...)
	}
	return out
}
