package alerts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSendOutbound(t *testing.T) {
	var ntfyTitle, ntfyTags, ntfyBody string
	var hook map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/topic" {
			ntfyTitle, ntfyTags, ntfyBody = r.URL.Query().Get("title"), r.URL.Query().Get("tags"), string(b)
			return
		}
		json.Unmarshal(b, &hook)
	}))
	defer srv.Close()
	recent = nil
	SendOutbound(Outbound{Ntfy: srv.URL + "/topic", Webhook: srv.URL + "/hook"}, "Panal · codex ✔ done", "tui · fix the table")
	if ntfyTitle != "Panal · codex ✔ done" || ntfyTags != "white_check_mark" || ntfyBody != "tui · fix the table" {
		t.Fatalf("ntfy: title %q, tags %q, body %q", ntfyTitle, ntfyTags, ntfyBody)
	}
	if hook["text"] != "Panal · codex ✔ done\ntui · fix the table" || hook["content"] != hook["text"] {
		t.Fatalf("webhook (Slack uses text, Discord content): %v", hook)
	}
}

func TestSendOutboundHasLimit(t *testing.T) {
	recent = nil
	now := time.Now()
	n := 0
	for i := 0; i < 10; i++ {
		if fits(now) {
			n++
		}
	}
	if n != maxPerMinute {
		t.Fatalf("%d alerts fit in a minute, %d went through", maxPerMinute, n)
	}
	if !fits(now.Add(61 * time.Second)) {
		t.Fatal("after the minute it fits again")
	}
}

func TestTag(t *testing.T) {
	for title, want := range map[string]string{
		"Panal · codex failed":    "warning",
		"Panal · agy ⟳ repeating": "repeat",
		"Panal · codex ✔ done":    "white_check_mark",
	} {
		if got := tag(title); got != want {
			t.Errorf("tag(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{
		"all": All, "": None, "bell": Bell, "Bell": Bell,
		"none": None, "no": None, "off": None, "anything": All,
	} {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", in, got, want)
		}
	}
}
