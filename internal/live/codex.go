// Package live asks the CLIs themselves for the quota while the dashboard is
// open, instead of waiting for an agent to run and leave it in its log. That
// way you see resets (the platform's or the ones you redeem), usage from
// elsewhere (IDE, web, another machine) and credits right away.
//
// It only uses queries that do not start a model turn or spend quota. It
// never redeems resets or writes credentials: the CLI manages the session.
package live

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

// Codex keeps a `codex app-server` alive (JSON-RPC over stdio, one message
// per line) and asks it for account/rateLimits/read every minute (more often
// when the quota is high, like codex's TUI does), or when asked to.
type Codex struct {
	Command []string // default: codex app-server

	mu      sync.Mutex
	latest  Reading
	events  []string
	request chan struct{}
}

// Reading is the latest thing the server said.
type Reading struct {
	Quota       state.Quota
	Resets      int    // saved resets you have left (-1 if unknown)
	Plan        string // prolite, plus, pro…
	At          time.Time
	Error       string // why the last query failed (empty if it went fine)
	HasData     bool   // there was at least one good reading
	LastAttempt time.Time
}

// NewCodex prepares the reader; Start puts it to work.
func NewCodex() *Codex {
	return &Codex{Command: []string{"codex", "app-server"}, request: make(chan struct{}, 1)}
}

// Latest is the latest known reading.
func (c *Codex) Latest() Reading {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

// TakeEvents returns (and forgets) what was detected between two readings:
// early resets, a saved reset redeemed, credits bought.
func (c *Codex) TakeEvents() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ev := c.events
	c.events = nil
	return ev
}

// Refresh asks for a reading now (the r key).
func (c *Codex) Refresh() {
	select {
	case c.request <- struct{}{}:
	default:
	}
}

// Start runs until ctx is cancelled: it starts the server, queries it and,
// if it goes down, starts it again, waiting longer each time.
func (c *Codex) Start(ctx context.Context) {
	wait := 15 * time.Second
	for ctx.Err() == nil {
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		c.latest.Error = err.Error()
		c.latest.LastAttempt = time.Now()
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-c.request:
		}
		wait = min(wait*2, 5*time.Minute)
	}
}

// pollInterval: like codex's TUI, more often the closer to the limit.
func pollInterval(used float64) time.Duration {
	switch {
	case used >= 99:
		return 5 * time.Second
	case used >= 90:
		return 15 * time.Second
	case used >= 75:
		return 30 * time.Second
	}
	return 60 * time.Second
}

// session: one live server, queried until it fails.
func (c *Codex) session(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, c.Command[0], c.Command[1:]...)
	hideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start codex app-server: %w", err)
	}
	defer func() {
		stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	cli := &rpc{w: stdin, resp: map[int]chan json.RawMessage{}}
	go cli.read(stdout)

	if _, err := cli.call("initialize", map[string]any{
		"clientInfo": map[string]string{"name": "panal", "title": "panal", "version": "1"},
	}, 20*time.Second); err != nil {
		return fmt.Errorf("codex app-server did not respond: %w", err)
	}
	if err := cli.notify("initialized", map[string]any{}); err != nil {
		return err
	}
	for {
		res, err := cli.call("account/rateLimits/read", map[string]any{"excludeResetCreditDetails": true}, 15*time.Second)
		if err != nil {
			return err
		}
		l, err := parse(res, time.Now())
		if err != nil {
			return err
		}
		c.store(l)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval(l.Quota.UsedPct)):
		case <-c.request:
		}
	}
}

func (c *Codex) store(l Reading) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latest.HasData {
		c.events = append(c.events, Compare(c.latest, l)...)
	}
	l.LastAttempt = l.At
	c.latest = l
}

// Compare says what changed between two readings that is not explained by
// time passing: an early reset, a saved reset redeemed, new credits.
func Compare(before, after Reading) []string {
	out := compareBars("codex", before.Quota, after.Quota, after.At)
	if before.Resets >= 0 && after.Resets >= 0 && after.Resets < before.Resets {
		out = append(out, fmt.Sprintf("codex: a saved reset was redeemed (%d left)", after.Resets))
	}
	ca, _ := strconv.ParseFloat(before.Quota.Credits, 64)
	cb, _ := strconv.ParseFloat(after.Quota.Credits, 64)
	if cb-ca >= 1 {
		out = append(out, fmt.Sprintf("codex: +%.0f credits (now %.0f)", cb-ca, cb))
	}
	return out
}

// compareBars: a window that dropped more than 5 points before its reset
// time came was reset early (by the platform or by you).
func compareBars(who string, before, after state.Quota, at time.Time) []string {
	var out []string
	byName := map[string]state.Bar{}
	for _, b := range before.Bars {
		byName[b.Name] = b
	}
	for _, b := range after.Bars {
		a, ok := byName[b.Name]
		if ok && a.UsedPct-b.UsedPct > 5 && !a.ResetsAt.IsZero() && at.Before(a.ResetsAt) {
			out = append(out, fmt.Sprintf("%s: %s quota reset early (%.0f%% → %.0f%%)", who, windowName(b.Name), a.UsedPct, b.UsedPct))
		}
	}
	return out
}

// windowName turns a bar key ("5h", "sem", "mes") into words.
func windowName(n string) string {
	switch n {
	case "sem":
		return "weekly"
	case "mes":
		return "monthly"
	}
	return n
}

// ------------------------------------------------------------------ JSON --

type rpcWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *int     `json:"windowDurationMins"`
	ResetsAt           *int64   `json:"resetsAt"`
}

type rpcSnapshot struct {
	Primary   *rpcWindow `json:"primary"`
	Secondary *rpcWindow `json:"secondary"`
	Credits   *struct {
		HasCredits bool   `json:"hasCredits"`
		Unlimited  bool   `json:"unlimited"`
		Balance    string `json:"balance"`
	} `json:"credits"`
	PlanType string `json:"planType"`
}

type rpcResponse struct {
	RateLimits          *rpcSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID map[string]*rpcSnapshot `json:"rateLimitsByLimitId"`
	ResetCredits        *struct {
		AvailableCount *int `json:"availableCount"`
	} `json:"rateLimitResetCredits"`
}

// parse turns the account/rateLimits/read answer into the dashboard's quota.
func parse(raw json.RawMessage, at time.Time) (Reading, error) {
	var r rpcResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return Reading{}, err
	}
	s := r.RateLimitsByLimitID["codex"]
	if s == nil {
		s = r.RateLimits
	}
	if s == nil {
		return Reading{}, errors.New("codex reported no rate limits (API key session?)")
	}
	l := Reading{Quota: state.Quota{Exact: true, SeenAt: at}, Resets: -1, Plan: s.PlanType, At: at, HasData: true}
	for i, v := range []*rpcWindow{s.Primary, s.Secondary} {
		if v == nil || v.UsedPercent == nil {
			continue
		}
		// "sem" is the weekly bar's key across packages (state, ui, forecast).
		b := state.Bar{Name: "sem", UsedPct: *v.UsedPercent}
		if v.WindowDurationMins != nil && *v.WindowDurationMins > 0 && *v.WindowDurationMins < 1440 {
			b.Name = fmt.Sprintf("%dh", *v.WindowDurationMins/60)
		}
		if v.ResetsAt != nil && *v.ResetsAt > 0 {
			b.ResetsAt = time.Unix(*v.ResetsAt, 0)
		}
		l.Quota.Bars = append(l.Quota.Bars, b)
		if i == 0 {
			l.Quota.UsedPct, l.Quota.ResetsAt = b.UsedPct, b.ResetsAt
		}
	}
	if s.Credits != nil && s.Credits.Balance != "" {
		if f, err := strconv.ParseFloat(s.Credits.Balance, 64); err == nil {
			l.Quota.Credits = strconv.Itoa(int(f + 0.5))
		}
	}
	if r.ResetCredits != nil && r.ResetCredits.AvailableCount != nil {
		l.Resets = *r.ResetCredits.AvailableCount
	}
	return l, nil
}

// ------------------------------------------------------------------- rpc --

// rpc: codex app-server's JSON-RPC 2.0 (without the "jsonrpc" field), one
// message per line.
type rpc struct {
	mu   sync.Mutex
	w    io.Writer
	id   int
	resp map[int]chan json.RawMessage
	err  error
}

type message struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params any             `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (r *rpc) read(rd io.Reader) {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil || m.Method != "" {
			continue // notifications and requests from the server: not our business
		}
		r.mu.Lock()
		ch := r.resp[*m.ID]
		delete(r.resp, *m.ID)
		r.mu.Unlock()
		if ch == nil {
			continue
		}
		if m.Error != nil {
			ch <- json.RawMessage(`{"__error":` + strconv.Quote(m.Error.Message) + `}`)
		} else {
			ch <- m.Result
		}
	}
	r.mu.Lock()
	r.err = errors.New("codex app-server exited")
	for id, ch := range r.resp {
		close(ch)
		delete(r.resp, id)
	}
	r.mu.Unlock()
}

func (r *rpc) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	r.mu.Lock()
	if r.err != nil {
		r.mu.Unlock()
		return nil, r.err
	}
	r.id++
	id := r.id
	ch := make(chan json.RawMessage, 1)
	r.resp[id] = ch
	r.mu.Unlock()
	if err := r.write(message{ID: &id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	select {
	case res, ok := <-ch:
		if !ok {
			return nil, errors.New("codex app-server exited")
		}
		var e struct {
			Err *string `json:"__error"`
		}
		if json.Unmarshal(res, &e) == nil && e.Err != nil {
			return nil, fmt.Errorf("codex: %s", *e.Err)
		}
		return res, nil
	case <-time.After(timeout):
		r.mu.Lock()
		delete(r.resp, id)
		r.mu.Unlock()
		return nil, fmt.Errorf("codex did not answer %s within %s", method, timeout)
	}
}

func (r *rpc) notify(method string, params any) error {
	return r.write(message{Method: method, Params: params})
}

func (r *rpc) write(m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err = r.w.Write(append(b, '\n'))
	return err
}
