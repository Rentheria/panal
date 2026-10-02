package credits

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func line(t time.Time, balance string) string {
	return `{"timestamp":"` + t.UTC().Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"token_count","rate_limits":{"credits":{"has_credits":true,"balance":"` + balance + `"}}}}` + "\n"
}

func TestReadIncrementalAndSummarize(t *testing.T) {
	d := t.TempDir()
	sub := filepath.Join(d, "2026", "09", "25")
	os.MkdirAll(sub, 0o755)
	p := filepath.Join(sub, "rollout.jsonl")
	now := time.Date(2026, 9, 25, 15, 0, 0, 0, time.Local)
	yesterday := now.AddDate(0, 0, -1)
	text := line(yesterday, "1000") + line(yesterday.Add(time.Minute), "996") +
		`{"timestamp":"x","payload":{"type":"other"}}` + "\n" +
		line(now.Add(-2*time.Hour), "996") + line(now.Add(-time.Hour), "995")
	os.WriteFile(p, []byte(text), 0o644)

	l := &Reader{Dir: d, Window: 30 * 24 * time.Hour}
	ps := l.Points(now)
	if len(ps) != 4 {
		t.Fatalf("points = %d, want 4", len(ps))
	}
	// A half-written line does not count until it is finished.
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(line(now.Add(-30*time.Minute), "993")[:40])
	f.Close()
	if n := len(l.Points(now)); n != 4 {
		t.Fatalf("with an incomplete line: %d points, want 4", n)
	}
	f, _ = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(line(now.Add(-30*time.Minute), "993")[40:] + line(now.Add(-20*time.Minute), "1993"))
	f.Close()
	ps = l.Points(now)
	if len(ps) != 6 {
		t.Fatalf("after completing it: %d points, want 6", len(ps))
	}

	c := Summarize(ps, now)
	// Today: 996→995 (1) and 995→993 (2); the top-up to 1993 does not count. Yesterday: 4.
	if c.Today != 3 || c.Balance != 1993 || c.Days != 2 || c.PerDay != 3.5 {
		t.Fatalf("summary: %+v", c)
	}
	if math.Abs(c.DaysLeft-1993/3.5) > 1e-9 {
		t.Fatalf("days left: %v", c.DaysLeft)
	}
}

// Two sessions at once: one reports a stale balance. The sawtooth must not be
// counted twice, and a drop after hours without events is not spending.
func TestSummarizeSawtoothAndGap(t *testing.T) {
	h := time.Date(2026, 9, 25, 12, 0, 0, 0, time.Local)
	ps := []Point{
		{h.Add(-20 * time.Hour), 3200},
		{h.Add(-2 * time.Minute), 1000}, // after 20 h without events: not spending
		{h, 999.0},
		{h.Add(1 * time.Second), 999.5}, // stale session
		{h.Add(2 * time.Second), 998.8},
		{h.Add(3 * time.Second), 999.4}, // stale session
		{h.Add(4 * time.Second), 998.5},
	}
	c := Summarize(ps, h)
	if math.Abs(c.Today-1.5) > 1e-9 || c.Balance != 998.5 {
		t.Fatalf("today = %v (want 1.5), balance = %v (want 998.5)", c.Today, c.Balance)
	}
}

func TestSummarizeNoData(t *testing.T) {
	if c := Summarize(nil, time.Now()); c.HasData || c.DaysLeft != 0 {
		t.Fatalf("without points: %+v", c)
	}
}
