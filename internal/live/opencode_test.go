package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const opencodeUsage = `{"usage":{"rolling":{"status":"ok","percent":0,"resetsAt":"2026-09-29T20:35:02.212Z"},"weekly":{"status":"ok","percent":12,"resetsAt":"2026-10-05T00:00:00.000Z"},"monthly":{"status":"rate-limited","percent":100,"resetsAt":"2026-10-17T21:11:51.000Z"}}}`

func TestOpencodeLive(t *testing.T) {
	var ua, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua, auth = r.Header.Get("User-Agent"), r.Header.Get("Authorization")
		switch auth {
		case "Bearer good":
			w.Write([]byte(opencodeUsage))
		case "Bearer no-go":
			w.WriteHeader(403)
			w.Write([]byte(`{"type":"error","error":{"type":"EntitlementError","message":"OpenCode Go subscription required."}}`))
		default:
			w.WriteHeader(401)
		}
	}))
	defer srv.Close()

	read := func(key string) OpencodeReading {
		o := NewOpencode()
		o.URL = srv.URL
		o.ReadKey = func() (string, error) { return key, nil }
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		go o.Start(ctx)
		for ctx.Err() == nil {
			if l := o.LatestOpencode(); l.HasData || l.Error != "" {
				return l
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("no answer")
		return OpencodeReading{}
	}

	l := read("good")
	if ua != "opencode" {
		t.Fatalf("must send User-Agent opencode (otherwise Cloudflare answers 403): %q", ua)
	}
	if !l.Exhausted || len(l.Quota.Bars) != 3 {
		t.Fatalf("three windows and the monthly one used up: %+v", l)
	}
	b := l.Quota.Bars
	if b[0].Name != "5h" || b[1].Name != "sem" || b[1].UsedPct != 12 || b[2].Name != "mes" || b[2].UsedPct != 100 {
		t.Fatalf("windows misread: %+v", b)
	}
	if b[2].ResetsAt.UTC().Format("2006-01-02") != "2026-10-17" {
		t.Fatalf("monthly reset misread: %v", b[2].ResetsAt)
	}
	if l := read("bad"); !strings.Contains(l.Error, "401") {
		t.Fatalf("rejected key: %q", l.Error)
	}
	if l := read("no-go"); !strings.Contains(l.Error, "has no OpenCode Go") {
		t.Fatalf("no Go subscription: %q", l.Error)
	}
}
