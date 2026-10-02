package readers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/live"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

// Codex implements Reader for the "codex" agent, wrapping Delegate for the
// overall state and reading quotas from Codex's local sessions.
type Codex struct {
	Delegate    *Delegate
	Live        interface{ Latest() live.Reading } // nil = sessions only
	SessionsDir string
	Now         func() time.Time
	FileReads   int

	InitialBalance    float64
	HasInitialBalance bool

	cachePath    string
	cacheModTime time.Time
	cacheSize    int64
	cacheQuota   state.Quota
	cacheDetail  string
	cacheCtx     float64 // context % of the last turn; <0 if unknown
	cacheBalance float64
	cacheOk      bool
}

// NewCodex creates the reader with the real system paths.
func NewCodex() *Codex {
	sessionsDir := os.Getenv("CODEX_HOME")
	if sessionsDir != "" {
		sessionsDir = filepath.Join(sessionsDir, "sessions")
	} else {
		u := os.Getenv("USERPROFILE")
		if u == "" {
			u, _ = os.UserHomeDir()
		}
		sessionsDir = filepath.Join(u, ".codex", "sessions")
	}
	return &Codex{
		Delegate:    NewDelegate("codex"),
		SessionsDir: sessionsDir,
		Now:         time.Now,
	}
}

func (c *Codex) Agent() string {
	if c.Delegate != nil {
		return c.Delegate.Agent()
	}
	return "codex"
}

type sessionMetaJSON struct {
	Type    string `json:"type"`
	Payload struct {
		Cwd        string `json:"cwd"`
		Originator string `json:"originator"`
	} `json:"payload"`
}

type eventMsgJSON struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type string `json:"type"`
		Info struct {
			TotalTokenUsage struct {
				InputTokens           int `json:"input_tokens"`
				CachedInputTokens     int `json:"cached_input_tokens"`
				OutputTokens          int `json:"output_tokens"`
				ReasoningOutputTokens int `json:"reasoning_output_tokens"`
				TotalTokens           int `json:"total_tokens"`
			} `json:"total_token_usage"`
			LastTokenUsage struct {
				InputTokens int `json:"input_tokens"`
			} `json:"last_token_usage"`
			ModelContextWindow int `json:"model_context_window"`
		} `json:"info"`
		RateLimits *struct {
			LimitID string `json:"limit_id"`
			Primary *struct {
				UsedPercent   float64 `json:"used_percent"`
				WindowMinutes int     `json:"window_minutes"`
				ResetsAt      int64   `json:"resets_at"`
			} `json:"primary"`
			Credits struct {
				HasCredits bool   `json:"has_credits"`
				Unlimited  bool   `json:"unlimited"`
				Balance    string `json:"balance"`
			} `json:"credits"`
			PlanType string `json:"plan_type"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

// Read is what the sessions say and, if there is a newer live reading
// (codex app-server), that one wins: it is the quota right now, including
// whatever was spent from the IDE, the web or another machine and any resets.
func (c *Codex) Read() state.Row {
	row := c.readSessions()
	if c.Live == nil {
		return row
	}
	l := c.Live.Latest()
	if !l.HasData || !l.At.After(row.Quota.SeenAt) {
		return row
	}
	q := l.Quota
	q.Live = true
	if l.Resets == 1 {
		q.Extra = "1 saved reset"
	} else if l.Resets > 1 {
		q.Extra = fmt.Sprintf("%d saved resets", l.Resets)
	}
	row.Quota = q
	if row.Status == state.OutOfQuota && q.UsedPct < 100 {
		row.Status = state.Idle
	}
	note := fmt.Sprintf("live quota: from codex app-server · plan %s", l.Plan)
	if l.Resets >= 0 {
		note += fmt.Sprintf(" · saved resets: %d", l.Resets)
	}
	if row.Detail != "" {
		row.Detail = note + "\n" + row.Detail
	} else {
		row.Detail = note
	}
	return row
}

func (c *Codex) readSessions() state.Row {
	var row state.Row
	if c.Delegate != nil {
		if c.Now != nil && c.Delegate.Now == nil {
			c.Delegate.Now = c.Now
		}
		row = c.Delegate.Read()
	} else {
		row = state.Row{
			Agent:  "codex",
			Status: state.NoData,
		}
	}

	path, info, err := c.newestFile()
	if err != nil {
		row.Quota = state.Quota{Summary: "no sessions"}
		return row
	}

	// Check the cache
	if c.cacheOk && path == c.cachePath && info.ModTime().Equal(c.cacheModTime) && info.Size() == c.cacheSize {
		row.Quota = c.cacheQuota
		if row.Status == state.OutOfQuota && c.cacheQuota.UsedPct < 100 {
			row.Status = state.Idle
		}
		if c.HasInitialBalance && c.cacheBalance < c.InitialBalance {
			n := int(math.Round(c.InitialBalance - c.cacheBalance))
			if n > 0 {
				row.Quota.Summary = fmt.Sprintf("-%d cr this session", n)
			}
		}
		setContext(&row, c.cacheCtx)
		if c.cacheDetail != "" {
			if row.Detail != "" {
				row.Detail = row.Detail + "\n\n" + c.cacheDetail
			} else {
				row.Detail = c.cacheDetail
			}
		}
		return row
	}

	c.FileReads++

	file, err := os.Open(path)
	if err != nil {
		row.Quota = state.Quota{Summary: "no sessions"}
		return row
	}
	defer file.Close()

	meta, _ := readMeta(file)
	ev, err := findLastRateLimit(file, info.Size())
	if err != nil || ev == nil || ev.Payload.RateLimits == nil || ev.Payload.RateLimits.Primary == nil {
		row.Quota = state.Quota{Summary: "no sessions"}
		return row
	}

	primary := ev.Payload.RateLimits.Primary
	credits := ev.Payload.RateLimits.Credits

	seen, err := time.Parse(time.RFC3339Nano, ev.Timestamp)
	if err != nil {
		seen, _ = time.Parse(time.RFC3339, ev.Timestamp)
	}

	quota := state.Quota{
		Exact:    true,
		UsedPct:  primary.UsedPercent,
		ResetsAt: time.Unix(primary.ResetsAt, 0),
		SeenAt:   seen,
	}
	// "sem" is the weekly bar's key across packages (state, ui, forecast).
	windowName := "sem"
	if primary.WindowMinutes > 0 && primary.WindowMinutes < 1440 {
		windowName = fmt.Sprintf("%dh", primary.WindowMinutes/60)
	}
	quota.Bars = []state.Bar{{Name: windowName, UsedPct: primary.UsedPercent, ResetsAt: quota.ResetsAt}}

	var balance float64
	if credits.Balance != "" {
		if f, err := strconv.ParseFloat(credits.Balance, 64); err == nil {
			balance = f
			quota.Credits = fmt.Sprintf("%d", int(math.Round(f)))
		}
	}

	if !c.HasInitialBalance {
		c.InitialBalance = balance
		c.HasInitialBalance = true
	} else if balance < c.InitialBalance {
		n := int(math.Round(c.InitialBalance - balance))
		if n > 0 {
			quota.Summary = fmt.Sprintf("-%d cr this session", n)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "plan: %s · window: %d d", ev.Payload.RateLimits.PlanType, primary.WindowMinutes/1440)
	fmt.Fprintf(&b, "\nsession: %s", path)
	fmt.Fprintf(&b, "\ncwd: %s · originator: %s", meta.Payload.Cwd, meta.Payload.Originator)
	fmt.Fprintf(&b, "\ntotal tokens: %d", ev.Payload.Info.TotalTokenUsage.TotalTokens)
	if primary.UsedPercent >= 100 && credits.HasCredits {
		b.WriteString("\n⚠ weekly quota used up: each use costs credits")
	}
	codexDetail := b.String()

	// The state comes from the last delegated attempt; if that one bounced on
	// quota but the exact quota is already below 100 (it reset), it is not
	// out of quota: it is available.
	if row.Status == state.OutOfQuota && quota.UsedPct < 100 {
		row.Status = state.Idle
	}

	c.cachePath = path
	c.cacheModTime = info.ModTime()
	c.cacheSize = info.Size()
	c.cacheQuota = quota
	c.cacheDetail = codexDetail
	c.cacheCtx = -1
	if w := ev.Payload.Info.ModelContextWindow; w > 0 {
		c.cacheCtx = float64(ev.Payload.Info.LastTokenUsage.InputTokens) * 100 / float64(w)
	}
	setContext(&row, c.cacheCtx)
	c.cacheBalance = balance
	c.cacheOk = true

	row.Quota = quota
	if row.Detail != "" {
		row.Detail = row.Detail + "\n\n" + codexDetail
	} else {
		row.Detail = codexDetail
	}

	return row
}

func (c *Codex) newestFile() (string, os.FileInfo, error) {
	dir := c.SessionsDir
	if dir == "" {
		return "", nil, os.ErrNotExist
	}
	dirInfo, err := os.Stat(dir)
	if err != nil || !dirInfo.IsDir() {
		return "", nil, os.ErrNotExist
	}

	var newest string
	var newestInfo os.FileInfo

	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".jsonl") {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if newestInfo == nil || info.ModTime().After(newestInfo.ModTime()) ||
				(info.ModTime().Equal(newestInfo.ModTime()) && path > newest) {
				newest = path
				newestInfo = info
			}
		}
		return nil
	})
	if err != nil || newest == "" {
		return "", nil, os.ErrNotExist
	}
	return newest, newestInfo, nil
}

func readMeta(f *os.File) (sessionMetaJSON, error) {
	var meta sessionMetaJSON
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return meta, err
	}
	reader := bufio.NewReader(f)
	firstLine, err := reader.ReadBytes('\n')
	if err != nil && len(firstLine) == 0 {
		return meta, err
	}
	firstLine = bytes.TrimRight(firstLine, "\r\n")
	_ = json.Unmarshal(firstLine, &meta)
	return meta, nil
}

func findLastRateLimit(f *os.File, fileSize int64) (*eventMsgJSON, error) {
	const maxTailBytes = 512 * 1024

	if fileSize <= maxTailBytes {
		return searchFrom(f, 0)
	}

	// Large file: read only the last 512 KB
	offset := fileSize - maxTailBytes
	ev, err := searchTail(f, offset, maxTailBytes)
	if err == nil && ev != nil {
		return ev, nil
	}

	// Nothing there: read the whole file once
	return searchFrom(f, 0)
}

func searchTail(f *os.File, offset int64, length int) (*eventMsgJSON, error) {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, length)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	buf = buf[:n]

	// Since we start mid-file, the first line may be cut off.
	// Skip to the first newline.
	idx := bytes.IndexByte(buf, '\n')
	if idx >= 0 {
		buf = buf[idx+1:]
	}

	return searchBytes(buf), nil
}

func searchBytes(buf []byte) *eventMsgJSON {
	lines := bytes.Split(buf, []byte("\n"))
	var last *eventMsgJSON
	for _, line := range lines {
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 || !bytes.Contains(line, []byte(`"rate_limits"`)) {
			continue
		}
		var ev eventMsgJSON
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Payload.RateLimits != nil && ev.Payload.RateLimits.Primary != nil {
			last = &ev
		}
	}
	return last
}

func searchFrom(f *os.File, offset int64) (*eventMsgJSON, error) {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(f)
	var last *eventMsgJSON
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			if len(line) > 0 && bytes.Contains(line, []byte(`"rate_limits"`)) {
				var ev eventMsgJSON
				if errJson := json.Unmarshal(line, &ev); errJson == nil {
					if ev.Payload.RateLimits != nil && ev.Payload.RateLimits.Primary != nil {
						last = &ev
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	return last, nil
}

// setContext: while codex works, how much of its context window the last
// turn filled (input_tokens of the last token_count over
// model_context_window), to watch it approach compaction.
func setContext(f *state.Row, pct float64) {
	if pct < 0 || f.Status != state.Working {
		return
	}
	f.HasContext, f.ContextPct = true, min(pct, 100)
}
