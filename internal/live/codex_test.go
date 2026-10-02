package live

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

// The test binary plays "codex app-server" when the test launches it: it
// answers initialize and, to each account/rateLimits/read, the next of its
// answers (the second one simulates a redeemed saved reset).
func TestMain(m *testing.M) {
	if f := os.Getenv("LIVE_FAKE_AGY"); f != "" {
		b, _ := os.ReadFile(f)
		os.Stdout.Write(b)
		os.Exit(0)
	}
	if os.Getenv("LIVE_FAKE_SERVER") == "1" {
		fakeServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeServer() {
	answers := []string{
		`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":60,"windowDurationMins":10080,"resetsAt":%d},"secondary":null,"credits":{"hasCredits":true,"unlimited":false,"balance":"979.2489"},"planType":"prolite"},"rateLimitResetCredits":{"availableCount":1,"credits":null}}`,
		`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":0,"windowDurationMins":10080,"resetsAt":%d},"secondary":null,"credits":{"hasCredits":true,"unlimited":false,"balance":"979.2489"},"planType":"prolite"},"rateLimitResetCredits":{"availableCount":0,"credits":null}}`,
	}
	reset := time.Now().Add(72 * time.Hour).Unix()
	sc := bufio.NewScanner(os.Stdin)
	n := 0
	out := bufio.NewWriter(os.Stdout)
	for sc.Scan() {
		var m struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil {
			continue
		}
		fmt.Fprintln(out, `{"method":"remoteControl/status/changed","params":{}}`) // noise
		switch m.Method {
		case "initialize":
			fmt.Fprintf(out, `{"id":%d,"result":{"userAgent":"fake"}}`+"\n", *m.ID)
		case "account/rateLimits/read":
			r := answers[min(n, len(answers)-1)]
			n++
			fmt.Fprintf(out, `{"id":%d,"result":`+r+`}`+"\n", *m.ID, reset)
		default:
			fmt.Fprintf(out, `{"id":%d,"error":{"code":-32601,"message":"no"}}`+"\n", *m.ID)
		}
		out.Flush()
	}
}

func TestCodexLiveReadsAndDetectsReset(t *testing.T) {
	t.Setenv("LIVE_FAKE_SERVER", "1")
	c := NewCodex()
	c.Command = []string{os.Args[0], "-test.run=^$"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Start(ctx)

	waitFor := func(cond func(Reading) bool) Reading {
		t.Helper()
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) {
			if l := c.Latest(); cond(l) {
				return l
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("the expected reading never arrived: %+v", c.Latest())
		return Reading{}
	}
	l := waitFor(func(l Reading) bool { return l.HasData })
	if len(l.Quota.Bars) != 1 || l.Quota.Bars[0].Name != "sem" || l.Quota.Bars[0].UsedPct != 60 {
		t.Fatalf("bars misread: %+v", l.Quota.Bars)
	}
	if l.Quota.Credits != "979" || l.Resets != 1 || l.Plan != "prolite" {
		t.Fatalf("credits, resets or plan misread: %+v", l)
	}

	c.Refresh() // the r key: the second answer, with the redeemed reset
	waitFor(func(l Reading) bool { return l.HasData && l.Quota.UsedPct == 0 })
	ev := strings.Join(c.TakeEvents(), "\n")
	if !strings.Contains(ev, "reset early") || !strings.Contains(ev, "saved reset") {
		t.Fatalf("should detect the early reset and the redemption: %q", ev)
	}
	if len(c.TakeEvents()) != 0 {
		t.Fatal("TakeEvents must empty the list")
	}
}

func TestCodexWithoutServerReportsError(t *testing.T) {
	c := NewCodex()
	c.Command = []string{"no-such-codex-xyz"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go c.Start(ctx)
	end := time.Now().Add(2 * time.Second)
	for time.Now().Before(end) && c.Latest().Error == "" {
		time.Sleep(20 * time.Millisecond)
	}
	if l := c.Latest(); l.HasData || !strings.Contains(l.Error, "could not start") {
		t.Fatalf("without codex it must say why: %+v", l)
	}
}

func TestCompare(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.Local)
	bar := func(p float64, r time.Time) state.Quota {
		return state.Quota{Bars: []state.Bar{{Name: "5h", UsedPct: p, ResetsAt: r}}}
	}
	before := Reading{Quota: bar(40, now.Add(time.Hour)), Resets: -1, At: now.Add(-time.Minute)}
	// It dropped because its window already expired: not an early reset.
	if ev := Compare(before, Reading{Quota: bar(0, now.Add(5*time.Hour)), Resets: -1, At: now.Add(2 * time.Hour)}); len(ev) != 0 {
		t.Fatalf("a reset on schedule is not news: %v", ev)
	}
	if ev := Compare(before, Reading{Quota: bar(38, now.Add(time.Hour)), Resets: -1, At: now}); len(ev) != 0 {
		t.Fatalf("a 2-point difference is noise: %v", ev)
	}
	if pollInterval(95) != 15*time.Second || pollInterval(10) != time.Minute {
		t.Fatal("the polling pace does not follow codex's TUI")
	}
}

func TestParseAgy(t *testing.T) {
	b, _ := os.ReadFile("testdata/agy_usage.json")
	l, err := parseAgy(b, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	g, ok := l.GroupFor("gemini-3.8-flash")
	if !ok || g.Name != "Gemini Models" || len(g.Quota.Bars) != 2 {
		t.Fatalf("gemini group misread: %+v", g)
	}
	if b := g.Quota.Bars[0]; b.Name != "5h" || b.UsedPct > 1 || b.ResetsAt.IsZero() {
		t.Fatalf("the first bar is the 5 h one: %+v", b)
	}
	if b := g.Quota.Bars[1]; b.Name != "sem" || b.UsedPct < 17 || b.UsedPct > 17.1 {
		t.Fatalf("week at 17 %%: %+v", b)
	}
	if g, _ := l.GroupFor("claude-opus"); g.Name != "Claude and GPT models" {
		t.Fatalf("a non-gemini model goes to the other group: %q", g.Name)
	}
}

func TestAgyThatSpendsStops(t *testing.T) {
	b, _ := os.ReadFile("testdata/agy_usage_with_turn.json")
	if _, err := parseAgy(b, time.Now()); !errors.Is(err, errSpent) {
		t.Fatalf("an answer with a turn and tokens must turn the query off: %v", err)
	}
	t.Setenv("LIVE_FAKE_AGY", "testdata/agy_usage_with_turn.json")
	a := NewAgy()
	a.Command = []string{os.Args[0], "-test.run=^$"}
	a.Interval = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.Start(ctx) // must return on its own: it does not keep asking
	if l := a.LatestAgy(); !l.Stopped || l.HasData {
		t.Fatalf("must end up stopped: %+v", l)
	}
	if ev := a.TakeEvents(); len(ev) != 1 || !strings.Contains(ev[0], "spent quota") {
		t.Fatalf("must warn once: %v", ev)
	}
}

func TestParseClaude(t *testing.T) {
	b, _ := os.ReadFile("testdata/claude_get_usage.json")
	l, err := parseClaude(b, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if l.Plan != "team" || len(l.Quota.Bars) != 2 || l.Quota.Bars[0].Name != "5h" || l.Quota.Bars[0].UsedPct != 31 || l.Quota.Bars[1].UsedPct != 16 {
		t.Fatalf("misread: %+v", l)
	}
	if l.Quota.Bars[1].ResetsAt.UTC().Format("2006-01-02 15:04") != "2026-10-05 01:59" {
		t.Fatalf("weekly reset misread: %v", l.Quota.Bars[1].ResetsAt)
	}
	spent := []byte(strings.Replace(string(b), `"total_cost_usd": 0`, `"total_cost_usd": 0.02`, 1))
	if _, err := parseClaude(spent, time.Now()); !errors.Is(err, errSpentClaude) {
		t.Fatalf("with a cost it must turn off: %v", err)
	}
}
