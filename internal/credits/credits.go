// Package credits gets codex's credit spending from its own sessions
// (~/.codex/sessions/**/*.jsonl): every token_count event carries the time and
// the balance. Spending is the sum of the balance drops; rises (top-ups) do not
// count. The files only grow, so they are read incrementally.
package credits

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Point: the balance at a given time.
type Point struct {
	T       time.Time
	Balance float64
}

// Thresholds used by Summarize.
const (
	Refill = 2.0           // minimum rise that counts as a top-up
	Gap    = 2 * time.Hour // drops after a silence this long are not spending
)

// Usage summarized for drawing.
type Usage struct {
	Balance  float64 // last balance seen
	Today    float64 // spent today (local time)
	PerDay   float64 // average over the last Days days with data, today included
	Days     int     // days that go into the average
	DaysLeft float64 // balance / PerDay; 0 if there is no spending
	HasData  bool    // there were at least two points
}

type file struct {
	offset int64
	points []Point
}

// Reader reads the jsonl files of the sessions dir, with a per-file cache.
type Reader struct {
	Dir    string
	Window time.Duration // how far back to look (by file modification time)

	mu       sync.Mutex
	files    map[string]*file
	lastScan time.Time // last time the dir was walked
	list     []string
}

// New: CODEX_SESSIONS or ~/.codex/sessions, 8 days back.
func New() *Reader {
	d := os.Getenv("CODEX_SESSIONS")
	if d == "" {
		u := os.Getenv("USERPROFILE")
		if u == "" {
			u, _ = os.UserHomeDir()
		}
		d = filepath.Join(u, ".codex", "sessions")
	}
	return &Reader{Dir: d, Window: 8 * 24 * time.Hour}
}

type event struct {
	Timestamp string `json:"timestamp"`
	Payload   struct {
		RateLimits *struct {
			Credits *struct {
				Balance string `json:"balance"`
			} `json:"credits"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

// Points returns every point in the window, sorted by time.
func (l *Reader) Points(now time.Time) []Point {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.files == nil {
		l.files = map[string]*file{}
	}
	// Walking the dir is costly: once a minute is enough to see new sessions.
	if now.Sub(l.lastScan) > time.Minute || l.list == nil {
		l.lastScan = now
		l.list = l.list[:0]
		filepath.WalkDir(l.Dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
				return nil
			}
			if info, err := d.Info(); err == nil && now.Sub(info.ModTime()) <= l.Window {
				l.list = append(l.list, p)
			}
			return nil
		})
	}
	var all []Point
	for _, p := range l.list {
		a := l.files[p]
		if a == nil {
			a = &file{}
			l.files[p] = a
		}
		readFrom(p, a)
		all = append(all, a.points...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].T.Before(all[j].T) })
	return all
}

// readFrom reads what is new in the file since the last complete line.
func readFrom(p string, a *file) {
	f, err := os.Open(p)
	if err != nil {
		return
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() <= a.offset {
		return
	}
	if _, err := f.Seek(a.offset, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			// Half-written line: it is read again next time.
			return
		}
		a.offset += int64(len(line))
		if !strings.Contains(string(line), `"balance"`) {
			continue
		}
		var e event
		if json.Unmarshal(line, &e) != nil || e.Payload.RateLimits == nil || e.Payload.RateLimits.Credits == nil {
			continue
		}
		s, err1 := strconv.ParseFloat(e.Payload.RateLimits.Credits.Balance, 64)
		t, err2 := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err1 == nil && err2 == nil {
			a.points = append(a.points, Point{T: t, Balance: s})
		}
	}
}

// Summarize computes usage: spending per local day, the average of the last
// 7 days with data and how many days the balance lasts at that pace.
func Summarize(ps []Point, now time.Time) Usage {
	var c Usage
	if len(ps) < 2 {
		if len(ps) == 1 {
			c.Balance = ps[0].Balance
		}
		return c
	}
	c.HasData = true
	c.Balance = ps[len(ps)-1].Balance
	perDay := map[string]float64{}
	days := map[string]bool{}
	// With several sessions at once, one of them reports a slightly stale
	// balance and the series goes up and down like a saw. So the minimum is
	// followed: only what drops below it counts. A rise of more than Refill is
	// a top-up (new starting point). A drop after a gap of more than Gap with
	// no events is not counted: it happened outside any session (an
	// adjustment, an expiry), and counting it as that day's spending misleads.
	base := ps[0].Balance
	for i := 1; i < len(ps); i++ {
		d := ps[i].T.Local().Format("2006-01-02")
		days[d] = true
		s := ps[i].Balance
		switch {
		case s < base:
			if ps[i].T.Sub(ps[i-1].T) <= Gap {
				perDay[d] += base - s
			}
			base = s
		case s > base+Refill:
			base = s
		}
	}
	c.Balance = base
	today := now.Local().Format("2006-01-02")
	c.Today = perDay[today]
	var keys []string
	for d := range days {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	if len(keys) > 7 {
		keys = keys[len(keys)-7:]
	}
	total := 0.0
	for _, d := range keys {
		total += perDay[d]
	}
	c.Days = len(keys)
	if c.Days > 0 {
		c.PerDay = total / float64(c.Days)
	}
	if c.PerDay > 0 {
		c.DaysLeft = c.Balance / c.PerDay
	}
	return c
}
