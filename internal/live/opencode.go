package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

// Opencode asks for the OpenCode Go plan quota at its official usage endpoint
// (GET https://opencode.ai/zen/go/v1/usage, in opencode's public code,
// packages/console/app/src/routes/zen/go/v1/usage.ts), the same one
// opencode's quota plugins use. It gives three windows: the 5 h one, the
// weekly one and the monthly one. The key is the Go API key opencode keeps in
// auth.json (or OPENCODE_API_KEY): it is only read, and only sent to
// opencode.ai.
type Opencode struct {
	URL      string
	Interval time.Duration
	ReadKey  func() (string, error)
	Client   *http.Client

	mu      sync.Mutex
	latest  OpencodeReading
	events  []string
	request chan struct{}
}

// OpencodeReading: the Go plan windows.
type OpencodeReading struct {
	Quota     state.Quota
	Exhausted bool // some window says "rate-limited"
	At        time.Time
	Error     string
	HasData   bool
}

// NewOpencode prepares the reader; it asks every 5 minutes.
func NewOpencode() *Opencode {
	return &Opencode{
		URL:      "https://opencode.ai/zen/go/v1/usage",
		Interval: 5 * time.Minute,
		ReadKey:  opencodeKey,
		Client:   &http.Client{Timeout: 20 * time.Second},
		request:  make(chan struct{}, 1),
	}
}

// opencodeKey: OPENCODE_API_KEY or the "opencode-go" entry (otherwise
// "opencode") of ~/.local/share/opencode/auth.json.
func opencodeKey() (string, error) {
	if k := os.Getenv("OPENCODE_API_KEY"); k != "" {
		return k, nil
	}
	u := os.Getenv("USERPROFILE")
	if u == "" {
		u, _ = os.UserHomeDir()
	}
	b, err := os.ReadFile(filepath.Join(u, ".local", "share", "opencode", "auth.json"))
	if err != nil {
		return "", errors.New("opencode key not found (auth.json)")
	}
	var a map[string]struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(b, &a) != nil {
		return "", errors.New("opencode auth.json is unreadable")
	}
	for _, p := range []string{"opencode-go", "opencode"} {
		if k := a[p].Key; k != "" {
			return k, nil
		}
	}
	return "", errors.New("opencode auth.json has no Go key")
}

// LatestOpencode is the latest known reading.
func (o *Opencode) LatestOpencode() OpencodeReading {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.latest
}

// TakeEvents returns (and forgets) the early resets that were detected.
func (o *Opencode) TakeEvents() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	ev := o.events
	o.events = nil
	return ev
}

// Refresh asks for a reading now.
func (o *Opencode) Refresh() {
	select {
	case o.request <- struct{}{}:
	default:
	}
}

// Start asks now and then every Interval; if it fails, it waits longer.
func (o *Opencode) Start(ctx context.Context) {
	wait := o.Interval
	for ctx.Err() == nil {
		l, err := o.query(ctx)
		o.mu.Lock()
		if err != nil {
			o.latest.Error = err.Error()
			wait = min(wait*2, time.Hour)
		} else {
			if o.latest.HasData {
				o.events = append(o.events, compareBars("opencode", o.latest.Quota, l.Quota, l.At)...)
			}
			o.latest = l
			wait = o.Interval
		}
		o.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-o.request:
		}
	}
}

func (o *Opencode) query(ctx context.Context) (OpencodeReading, error) {
	key, err := o.ReadKey()
	if err != nil {
		return OpencodeReading{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.URL, nil)
	if err != nil {
		return OpencodeReading{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "opencode") // without this Cloudflare answers 403
	res, err := o.Client.Do(req)
	if err != nil {
		return OpencodeReading{}, fmt.Errorf("opencode.ai did not respond: %v", errors.Unwrap(err))
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return OpencodeReading{}, errors.New("opencode rejected the key (401)")
	case http.StatusForbidden:
		if strings.Contains(string(b), "subscription required") {
			return OpencodeReading{}, errors.New("the account has no OpenCode Go")
		}
		return OpencodeReading{}, errors.New("opencode.ai denied the request (403)")
	default:
		return OpencodeReading{}, fmt.Errorf("opencode.ai returned %d", res.StatusCode)
	}
	return parseOpencode(b, time.Now())
}

type opencodeWindow struct {
	Status   string   `json:"status"`
	Percent  *float64 `json:"percent"`
	ResetsAt string   `json:"resetsAt"`
}

func parseOpencode(b []byte, at time.Time) (OpencodeReading, error) {
	var r struct {
		Usage map[string]*opencodeWindow `json:"usage"`
	}
	if err := json.Unmarshal(b, &r); err != nil || r.Usage == nil {
		return OpencodeReading{}, errors.New("opencode.ai returned unexpected usage data")
	}
	l := OpencodeReading{Quota: state.Quota{Exact: true, SeenAt: at, Live: true}, At: at, HasData: true}
	// "sem" and "mes" are the weekly and monthly bar keys across packages
	// (state, ui, forecast).
	for _, v := range []struct{ key, name string }{{"rolling", "5h"}, {"weekly", "sem"}, {"monthly", "mes"}} {
		w := r.Usage[v.key]
		if w == nil || w.Percent == nil {
			continue
		}
		br := state.Bar{Name: v.name, UsedPct: *w.Percent}
		if w.Status == "rate-limited" {
			br.UsedPct = max(br.UsedPct, 100)
			l.Exhausted = true
		}
		if t, err := time.Parse(time.RFC3339Nano, w.ResetsAt); err == nil {
			br.ResetsAt = t.Local()
		}
		l.Quota.Bars = append(l.Quota.Bars, br)
	}
	if len(l.Quota.Bars) == 0 {
		return OpencodeReading{}, errors.New("opencode.ai returned no usage windows")
	}
	l.Quota.UsedPct, l.Quota.ResetsAt = l.Quota.Bars[0].UsedPct, l.Quota.Bars[0].ResetsAt
	return l, nil
}
