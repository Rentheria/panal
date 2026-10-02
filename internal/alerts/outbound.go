package alerts

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Outbound: where else alerts are sent, to see them on the phone or in a chat.
// Empty = nowhere (it is the only thing the dashboard sends off the machine,
// and only if configured).
//
//   - Ntfy: the URL of an ntfy topic («https://ntfy.sh/my-secret-topic»):
//     POST with the text; title and tag go in the URL (?title=&tags=).
//   - Webhook: a URL that receives JSON. Works for Slack and Discord (we send
//     «text» and «content» with the same thing) or for anything else.
type Outbound struct {
	Ntfy    string
	Webhook string
}

var (
	muOutbound     sync.Mutex
	recent         []time.Time
	outboundClient = &http.Client{Timeout: 10 * time.Second}
)

// Maximum outbound alerts per minute: if something goes haywire, it should not
// flood the phone.
const maxPerMinute = 6

// SendOutbound sends the alert to the configured ntfy and webhook. It blocks
// while the request goes out: call it from a tea.Cmd. Errors are ignored (a
// lost alert must not bring the dashboard down).
func SendOutbound(f Outbound, title, text string) {
	if f.Ntfy == "" && f.Webhook == "" || !fits(time.Now()) {
		return
	}
	if f.Ntfy != "" {
		// Title and tag in the URL: in a header, accents and symbols (✔, ⟳)
		// arrive garbled.
		if u, err := url.Parse(f.Ntfy); err == nil {
			q := u.Query()
			q.Set("title", title)
			q.Set("tags", tag(title))
			u.RawQuery = q.Encode()
			if text == "" {
				text = title
			}
			if res, err := outboundClient.Post(u.String(), "text/plain; charset=utf-8", strings.NewReader(text)); err == nil {
				res.Body.Close()
			}
		}
	}
	if f.Webhook != "" {
		msg := title
		if text != "" {
			msg += "\n" + text
		}
		b, _ := json.Marshal(map[string]string{"text": msg, "content": msg, "title": title, "body": text})
		if res, err := outboundClient.Post(f.Webhook, "application/json", bytes.NewReader(b)); err == nil {
			res.Body.Close()
		}
	}
}

// fits keeps count of the alerts of the last minute.
func fits(now time.Time) bool {
	muOutbound.Lock()
	defer muOutbound.Unlock()
	alive := recent[:0]
	for _, t := range recent {
		if now.Sub(t) < time.Minute {
			alive = append(alive, t)
		}
	}
	recent = alive
	if len(recent) >= maxPerMinute {
		return false
	}
	recent = append(recent, now)
	return true
}

// tag: the ntfy emoji for the alert.
func tag(title string) string {
	switch {
	case strings.Contains(title, "⚠"), strings.Contains(title, "✖"), strings.Contains(title, "failed"):
		return "warning"
	case strings.Contains(title, "⟳"):
		return "repeat"
	case strings.Contains(title, "↻"):
		return "arrows_counterclockwise"
	}
	return "white_check_mark"
}
