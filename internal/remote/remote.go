// Package remote brings what happens on other machines into one dashboard: one
// machine serves its status (panal -serve) and the others read it (machine = …
// in panal.conf). Read-only: the only thing exposed is GET /status, and only to
// whoever brings the token.
package remote

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

// StatusPath is the path served by Handler.
const StatusPath = "/status"

// Bar is a quota window, as it travels on the wire.
type Bar struct {
	Name     string    `json:"name"`
	Used     float64   `json:"used"`
	ResetsAt time.Time `json:"resets_at,omitempty"`
}

// Agent is what is told about each agent of the other machine.
type Agent struct {
	Agent    string    `json:"agent"`
	Status   string    `json:"status"` // state.Status.String() of the serving machine
	Glyph    string    `json:"glyph"`
	Model    string    `json:"model,omitempty"`
	Task     string    `json:"task,omitempty"` // first line, trimmed
	Activity string    `json:"activity,omitempty"`
	Since    time.Time `json:"since,omitempty"`
	Bars     []Bar     `json:"bars,omitempty"`
}

// Status is the response of GET /status.
type Status struct {
	Machine string    `json:"machine"`
	Time    time.Time `json:"time"`
	Agents  []Agent   `json:"agents"`
}

// Glyph of each status (the same as on the dashboard).
func Glyph(e state.Status) string {
	switch e {
	case state.Working, state.Orchestrating:
		return "●"
	case state.OutOfQuota:
		return "◐"
	case state.NoPermission:
		return "⊘"
	case state.Failed, state.Stuck:
		return "✖"
	case state.Done:
		return "✔"
	}
	return "○"
}

// Collect reads the local agents.
func Collect(ls []readers.Reader, now time.Time) Status {
	name, _ := os.Hostname()
	e := Status{Machine: name, Time: now}
	for _, l := range ls {
		f := l.Read()
		task, _, _ := strings.Cut(f.Task, "\n")
		if r := []rune(task); len(r) > 160 {
			task = string(r[:160]) + "…"
		}
		a := Agent{Agent: f.Agent, Status: f.Status.String(), Glyph: Glyph(f.Status), Model: f.Model,
			Task: task, Activity: f.Activity, Since: f.Since}
		for _, b := range f.Quota.Bars {
			a.Bars = append(a.Bars, Bar{b.Name, b.UsedPct, b.ResetsAt})
		}
		e.Agents = append(e.Agents, a)
	}
	return e
}

// Serve answers GET /status on addr until ctx is cancelled. Without a token it
// does not start: the status includes tasks and commands.
func Serve(ctx context.Context, addr, token string, ls []readers.Reader) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("missing token: set serve_token = … in panal.conf (or PANAL_TOKEN)")
	}
	srv := &http.Server{Addr: addr, Handler: Handler(token, ls), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Handler: GET /status with the token; anything else is an error.
func Handler(token string, ls []readers.Reader) http.Handler {
	var mu sync.Mutex // readers are not meant to be used from several requests at once
	serve := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(given), []byte(token)) != 1 {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		e := Collect(ls, time.Now())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(e)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(StatusPath, serve)
	return mux
}

// -------------------------------------------------------------- client --

// Machine is another machine to read: «machine = name URL token».
type Machine struct {
	Name, URL, Token string
}

// ParseMachine reads «pc2 http://10.0.0.5:8765 secret».
func ParseMachine(s string) (Machine, error) {
	c := strings.Fields(s)
	if len(c) != 3 {
		return Machine{}, fmt.Errorf("«machine = %s»: expected «name URL token»", s)
	}
	u := strings.TrimRight(c[1], "/")
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "http://" + u
	}
	return Machine{Name: c[0], URL: u, Token: c[2]}, nil
}

// Reading of one machine.
type Reading struct {
	Machine Machine
	Status  Status
	At      time.Time // when it last answered
	Error   string
}

// NoAnswerYet is the Error of a machine that has not answered yet.
const NoAnswerYet = "no answer yet"

// Client reads the machines every so often.
type Client struct {
	Machines []Machine
	Interval time.Duration
	HTTP     *http.Client

	mu     sync.Mutex
	latest map[string]Reading
}

// NewClient reads every 10 s.
func NewClient(ms []Machine) *Client {
	return &Client{Machines: ms, Interval: 10 * time.Second, HTTP: &http.Client{Timeout: 8 * time.Second},
		latest: map[string]Reading{}}
}

// Readings: the latest of each machine, in configuration order.
func (c *Client) Readings() []Reading {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Reading
	for _, m := range c.Machines {
		l, ok := c.latest[m.Name]
		if !ok {
			l = Reading{Machine: m, Error: NoAnswerYet}
		}
		out = append(out, l)
	}
	return out
}

// Start reads every machine now and then every Interval.
func (c *Client) Start(ctx context.Context) {
	for ctx.Err() == nil {
		var wg sync.WaitGroup
		for _, m := range c.Machines {
			wg.Add(1)
			go func(m Machine) {
				defer wg.Done()
				e, err := c.read(ctx, m)
				c.mu.Lock()
				l := c.latest[m.Name]
				l.Machine = m
				if err != nil {
					l.Error = err.Error()
				} else {
					l.Status, l.At, l.Error = e, time.Now(), ""
				}
				c.latest[m.Name] = l
				c.mu.Unlock()
			}(m)
		}
		wg.Wait()
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.Interval):
		}
	}
}

// read asks for /status.
func (c *Client) read(ctx context.Context, m Machine) (Status, error) {
	e, _, err := c.get(ctx, m, StatusPath)
	return e, err
}

func (c *Client) get(ctx context.Context, m Machine, path string) (Status, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL+path, nil)
	if err != nil {
		return Status{}, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return Status{}, 0, errors.New("no answer")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return Status{}, res.StatusCode, errors.New("token rejected")
	}
	if res.StatusCode != http.StatusOK {
		return Status{}, res.StatusCode, fmt.Errorf("answered %d", res.StatusCode)
	}
	var e Status
	if err := json.NewDecoder(res.Body).Decode(&e); err != nil {
		return Status{}, res.StatusCode, errors.New("unreadable answer")
	}
	return e, res.StatusCode, nil
}
