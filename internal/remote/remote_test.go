package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

type fixed struct{ f state.Row }

func (l fixed) Agent() string   { return l.f.Agent }
func (l fixed) Read() state.Row { return l.f }

func waitReadings(t *testing.T, c *Client) []Reading {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		ok := true
		for _, l := range c.Readings() {
			if l.At.IsZero() && l.Error == NoAnswerYet {
				ok = false
			}
		}
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return c.Readings()
}

func TestServeAndRead(t *testing.T) {
	ls := []readers.Reader{
		fixed{state.Row{Agent: "codex", Status: state.Working, Task: "Fix the table\nsecond line", Activity: "$ go test ./...",
			Quota: state.Quota{Bars: []state.Bar{{Name: "sem", UsedPct: 32}}}}},
		fixed{state.Row{Agent: "agy", Status: state.Done}},
	}
	srv := httptest.NewServer(Handler("secret", ls))
	defer srv.Close()

	good := Machine{Name: "pc2", URL: srv.URL, Token: "secret"}
	bad := Machine{Name: "pc3", URL: srv.URL, Token: "other"}
	c := NewClient([]Machine{good, bad})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Start(ctx)
	l := waitReadings(t, c)
	if len(l[0].Status.Agents) != 2 {
		t.Fatalf("pc2 must bring both agents: %+v", l[0])
	}
	a := l[0].Status.Agents[0]
	if a.Agent != "codex" || a.Status != "working" || a.Glyph != "●" || a.Task != "Fix the table" || a.Activity != "$ go test ./..." || len(a.Bars) != 1 || a.Bars[0].Used != 32 {
		t.Fatalf("codex served wrong: %+v", a)
	}
	if !l[1].At.IsZero() || l[1].Error != "token rejected" {
		t.Fatalf("pc3 with another token must not read anything: %+v", l[1])
	}
}

// The payload keys are English.
func TestWireKeys(t *testing.T) {
	ls := []readers.Reader{fixed{state.Row{Agent: "codex", Status: state.Done}}}
	srv := httptest.NewServer(Handler("secret", ls))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+StatusPath, nil)
	req.Header.Set("Authorization", "Bearer secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("/status must answer: %v %v", res, err)
	}
	defer res.Body.Close()
	var raw map[string]any
	json.NewDecoder(res.Body).Decode(&raw)
	for _, k := range []string{"machine", "time", "agents"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("payload key %q missing: %v", k, raw)
		}
	}
}

func TestNoTokenNoServe(t *testing.T) {
	if err := Serve(context.Background(), "127.0.0.1:0", " ", nil); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("without a token nothing is served: %v", err)
	}
}

func TestParseMachine(t *testing.T) {
	m, err := ParseMachine("pc2 10.0.0.5:8765 secret")
	if err != nil || m.Name != "pc2" || m.URL != "http://10.0.0.5:8765" || m.Token != "secret" {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := ParseMachine("pc2 http://x"); err == nil {
		t.Fatal("without a token it must be an error")
	}
}
