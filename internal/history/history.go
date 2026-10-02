package history

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/AlbertoVasquezR/panal/internal/feedback"
	"github.com/AlbertoVasquezR/panal/internal/runs"
)

// Run is a past delegated run (`panal delegate`, or the older delegar.sh).
type Run struct {
	Stamp    string
	Agent    string
	Model    string
	Task     string
	FullTask string // full task read from TaskFile or from logs/sessions
	Dir      string
	Status   runs.Status
	Start    time.Time
	End      time.Time
	RC       *int
	Log      string
	ReadOnly bool   // read-only run
	Tokens   int64  // 0 = unknown
	Cost     string // already formatted text, empty = unknown

	// As stored in the run file: the model and effort apart (Model joins
	// them for display), and what the router recorded (internal/router).
	ModelID  string
	Effort   string
	TaskType string // "" for runs from before the router
	Tier     string
	Auto     bool
	Route    string
	Choice   int
	Explored bool

	// The user's rating (internal/feedback): "good", "bad" or "".
	Rating     string
	RatingNote string
}

// Key identifies the run (its run file's name without .json); feedback is
// stored by it.
func (r Run) Key() string { return feedback.Key(r.Stamp, r.Agent) }

type cacheEntry struct {
	tokens int64
	cost   string
}

// History keeps the enrichment cache for finished runs.
type History struct {
	OpencodeDBPath string
	CodexSessions  string // codex sessions dir (empty = CODEX_SESSIONS or %USERPROFILE%\.codex\sessions)
	Computations   int    // number of enrichment computations (for tests)
	FileReads      int    // number of disk reads of task files (for tests)
	cache          map[string]cacheEntry
	tasks          map[string]string // cache by TaskFile path
	runTasks       map[string]string // cache by stamp+agent for tasks read from logs
	mu             sync.Mutex
}

// New creates a History with an empty cache.
func New() *History {
	return &History{
		cache:    make(map[string]cacheEntry),
		tasks:    make(map[string]string),
		runTasks: make(map[string]string),
	}
}

var defaultHistory = New()

// Read reads the runs using the default global instance ("" = runs.Dirs()).
func Read(dir string, limit int) []Run {
	return defaultHistory.Read(dir, limit)
}

// readTask reads up to 64 KiB from the given path, without the trailing newline.
// It uses an internal cache so unchanged files are not read again.
func (h *History) readTask(path string) string {
	if path == "" {
		return ""
	}
	h.mu.Lock()
	if h.tasks == nil {
		h.tasks = make(map[string]string)
	}
	if txt, ok := h.tasks[path]; ok {
		h.mu.Unlock()
		return txt
	}
	h.FileReads++
	h.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	const maxBytes = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return ""
	}

	txt := strings.TrimRight(string(data), "\r\n")

	h.mu.Lock()
	h.tasks[path] = txt
	h.mu.Unlock()

	return txt
}

// codexSessionsDir returns the codex sessions directory.
func (h *History) codexSessionsDir() string {
	if h.CodexSessions != "" {
		return h.CodexSessions
	}
	d := os.Getenv("CODEX_SESSIONS")
	if d == "" {
		u := os.Getenv("USERPROFILE")
		if u == "" {
			u, _ = os.UserHomeDir()
		}
		d = filepath.Join(u, ".codex", "sessions")
	}
	return d
}

// findCodexSession finds the session file for a thread_id.
func (h *History) findCodexSession(threadID string) string {
	if threadID == "" {
		return ""
	}
	dir := h.codexSessionsDir()
	pattern := filepath.Join(dir, "*", "*", "*", "rollout-*-"+threadID+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

// validateTask checks that the text found starts with the base task
// (ignoring surrounding whitespace and comparing up to 200 characters if the
// base has that many). It returns the text cut to at most 64 KiB, without
// trailing newlines.
func validateTask(baseTask, text string) string {
	pref := strings.TrimSpace(baseTask)
	if pref == "" {
		return ""
	}
	txtTrim := strings.TrimSpace(text)
	runesPref := []rune(pref)
	if len(runesPref) > 200 {
		pref = string(runesPref[:200])
	}
	if !strings.HasPrefix(txtTrim, pref) {
		return ""
	}
	const maxBytes = 64 * 1024
	if len(text) > maxBytes {
		text = strings.ToValidUTF8(text[:maxBytes], "")
	}
	return strings.TrimRight(text, "\r\n")
}

// Go log line format: level, MMDD date, time with microseconds and a space.
var reGoLog = regexp.MustCompile(`^[IWEF]\d{4} \d\d:\d\d:\d\d\.\d+ `)

// extractAgyTask finds the full task in the agy log.
func extractAgyTask(logPath, baseTask string) string {
	if logPath == "" {
		return ""
	}
	f, err := os.Open(logPath + ".log")
	if err != nil {
		return ""
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64*1024)
	var lines []string
	capturing := false

	for {
		line, err := r.ReadString('\n')
		lineTrim := strings.TrimRight(line, "\r\n")

		if capturing {
			if reGoLog.MatchString(lineTrim) {
				text := strings.Join(lines, "\n")
				if enc := validateTask(baseTask, text); enc != "" {
					return enc
				}
				lines = nil
				capturing = false
			} else {
				lines = append(lines, lineTrim)
			}
		}

		if !capturing {
			if idx := strings.Index(lineTrim, "Received cascade user message with "); idx != -1 {
				rest := lineTrim[idx:]
				if mediaIdx := strings.Index(rest, "media"); mediaIdx != -1 {
					afterMedia := rest[mediaIdx+len("media"):]
					if colonIdx := strings.Index(afterMedia, ": "); colonIdx != -1 {
						firstText := afterMedia[colonIdx+2:]
						lines = append(lines, firstText)
						capturing = true
					}
				}
			}
		}

		if err != nil {
			break
		}
	}

	if capturing && len(lines) > 0 {
		text := strings.Join(lines, "\n")
		return validateTask(baseTask, text)
	}

	return ""
}

// extractCodexTask finds the user's full task in the codex session.
func (h *History) extractCodexTask(c *Run) string {
	if c.Log == "" {
		return ""
	}
	jsonlPath := c.Log + ".jsonl"
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		return ""
	}

	var threadID string
	type codexExecLine struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
	}

	lines := bytes.Split(data, []byte("\n"))
	for _, lineBytes := range lines {
		lineBytes = bytes.TrimRight(lineBytes, "\r\n")
		if len(lineBytes) == 0 {
			continue
		}
		var l codexExecLine
		if err := json.Unmarshal(lineBytes, &l); err == nil {
			if l.Type == "thread.started" && l.ThreadID != "" {
				threadID = l.ThreadID
				break
			}
		}
	}

	if threadID == "" {
		return ""
	}

	sessionPath := h.findCodexSession(threadID)
	if sessionPath == "" {
		return ""
	}

	f, err := os.Open(sessionPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	type codexResponseItem struct {
		Type    string `json:"type"`
		Payload struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"payload"`
	}

	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 && bytes.Contains(line, []byte(`"response_item"`)) && bytes.Contains(line, []byte(`"input_text"`)) {
			var item codexResponseItem
			if json.Unmarshal(line, &item) == nil {
				if item.Type == "response_item" && item.Payload.Type == "message" && item.Payload.Role == "user" {
					var sb strings.Builder
					for _, ct := range item.Payload.Content {
						if ct.Type == "input_text" {
							sb.WriteString(ct.Text)
						}
					}
					candidate := sb.String()
					if enc := validateTask(c.Task, candidate); enc != "" {
						return enc
					}
				}
			}
		}
		if err != nil {
			break
		}
	}

	return ""
}

// lookupTask finds the full task in logs or sessions depending on the agent,
// caching by stamp+agent so finished runs are not read again.
func (h *History) lookupTask(c *Run) string {
	if c.Task == "" || (c.Agent != "agy" && c.Agent != "codex") {
		return ""
	}

	key := c.Stamp + "+" + c.Agent
	h.mu.Lock()
	if h.runTasks == nil {
		h.runTasks = make(map[string]string)
	}
	if enc, ok := h.runTasks[key]; ok {
		h.mu.Unlock()
		return enc
	}
	h.mu.Unlock()

	var enc string
	switch c.Agent {
	case "agy":
		enc = extractAgyTask(c.Log, c.Task)
	case "codex":
		enc = h.extractCodexTask(c)
	}

	// Cache it if the run is over, or if it is still running and was found.
	if c.Status != runs.Running || enc != "" {
		h.mu.Lock()
		h.runTasks[key] = enc
		h.mu.Unlock()
	}

	return enc
}

// Read returns the runs in the given run dir ("" = every dir in
// runs.Dirs()), sorted newest to oldest by Start, up to limit runs. Files
// with broken JSON or without a valid start are skipped.
func (h *History) Read(dir string, limit int) []Run {
	dirs := runs.Dirs()
	if dir != "" {
		dirs = []string{dir}
	}

	type item struct {
		run  Run
		file string
	}
	var items []item
	for _, f := range runs.List(dirs) {
		if f.Err != nil {
			continue
		}
		st := f.Run
		tStart := st.StartTime()
		if tStart.IsZero() {
			continue
		}

		agent := st.Agent
		if agent == "" {
			agent = guessAgent(filepath.Base(f.Path))
		}

		model := st.Model
		if st.Effort != "" {
			model = fmt.Sprintf("%s (%s)", st.Model, st.Effort)
		}

		items = append(items, item{
			run: Run{
				Stamp:    st.ID,
				Agent:    agent,
				Model:    model,
				Task:     st.Task,
				Dir:      st.Dir,
				Status:   st.Status,
				Start:    tStart,
				End:      st.EndTime(),
				RC:       st.RC,
				Log:      st.Log,
				ReadOnly: st.ReadOnly,
				ModelID:  st.Model,
				Effort:   st.Effort,
				TaskType: st.TaskType,
				Tier:     st.Tier,
				Auto:     st.Auto,
				Route:    st.Route,
				Choice:   st.Choice,
				Explored: st.Explored,
			},
			file: st.TaskFile,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].run.Start.After(items[j].run.Start)
	})

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	ratings := feedback.Cached(feedback.Path())
	runs := make([]Run, len(items))
	for i, el := range items {
		c := el.run
		if e := ratings.Get(c.Stamp, c.Agent); e.Rating != "" {
			c.Rating, c.RatingNote = e.Rating, e.Note
		}
		if el.file != "" {
			c.FullTask = h.readTask(el.file)
		}
		if c.FullTask == "" {
			c.FullTask = h.lookupTask(&c)
		}
		h.enrich(&c)
		runs[i] = c
	}

	return runs
}

func guessAgent(name string) string {
	base := strings.TrimSuffix(name, ".json")
	for _, a := range []string{"codex", "opencode", "agy", "claude"} {
		if strings.HasSuffix(base, "-"+a) || strings.HasPrefix(base, a+"-") || base == a {
			return a
		}
	}
	return ""
}

func (h *History) enrich(c *Run) {
	if c.Status == runs.Running {
		return
	}

	key := c.Stamp + "+" + c.Agent
	h.mu.Lock()
	if entry, ok := h.cache[key]; ok {
		h.mu.Unlock()
		c.Tokens = entry.tokens
		c.Cost = entry.cost
		return
	}
	h.Computations++
	h.mu.Unlock()

	var tokens int64
	var cost string

	switch c.Agent {
	case "codex":
		tokens, cost = h.enrichCodex(c)
	case "opencode":
		tokens, cost = h.enrichOpencode(c)
	case "agy":
		// agy: nothing (Tokens 0, empty Cost)
	}

	c.Tokens = tokens
	c.Cost = cost

	h.mu.Lock()
	h.cache[key] = cacheEntry{tokens: tokens, cost: cost}
	h.mu.Unlock()
}

func (h *History) enrichCodex(c *Run) (int64, string) {
	if c.Log == "" {
		return 0, ""
	}
	jsonlPath := c.Log + ".jsonl"
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		return 0, ""
	}

	var threadID string
	var fallbackTokens int64

	type codexExecLine struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Usage    *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}

	lines := bytes.Split(data, []byte("\n"))
	for _, lineBytes := range lines {
		lineBytes = bytes.TrimRight(lineBytes, "\r\n")
		if len(lineBytes) == 0 {
			continue
		}
		var l codexExecLine
		if err := json.Unmarshal(lineBytes, &l); err == nil {
			if threadID == "" && l.Type == "thread.started" && l.ThreadID != "" {
				threadID = l.ThreadID
			}
			if l.Type == "turn.completed" && l.Usage != nil {
				fallbackTokens += l.Usage.InputTokens + l.Usage.OutputTokens
			}
		}
	}

	if threadID != "" {
		sessionPath := h.findCodexSession(threadID)
		if sessionPath != "" {
			tokens, cost, ok := parseCodexSession(sessionPath)
			if ok {
				return tokens, cost
			}
		}
	}

	// If it did not come from a delegated run (no thread.started nor turn.completed), try
	// to parse it directly, for compatibility with files in session format.
	if threadID == "" && fallbackTokens == 0 {
		if tokens, cost, ok := parseCodexSession(jsonlPath); ok && (tokens > 0 || cost != "") {
			return tokens, cost
		}
	}

	return fallbackTokens, ""
}

func parseCodexSession(path string) (int64, string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, "", false
	}

	var lastTokens int64
	var firstBalance, lastBalance float64
	var hasFirstBalance, hasLastBalance bool

	type codexLine struct {
		Payload struct {
			Info struct {
				TotalTokenUsage *struct {
					TotalTokens int64 `json:"total_tokens"`
				} `json:"total_token_usage"`
			} `json:"info"`
			RateLimits *struct {
				Credits *struct {
					Balance any `json:"balance"`
				} `json:"credits"`
			} `json:"rate_limits"`
		} `json:"payload"`
	}

	lines := bytes.Split(data, []byte("\n"))
	for _, lineBytes := range lines {
		lineBytes = bytes.TrimRight(lineBytes, "\r\n")
		if len(lineBytes) == 0 {
			continue
		}

		hasTokens := bytes.Contains(lineBytes, []byte(`"total_token_usage"`))
		hasRateLimits := bytes.Contains(lineBytes, []byte(`"rate_limits"`))
		if !hasTokens && !hasRateLimits {
			continue
		}

		var l codexLine
		if err := json.Unmarshal(lineBytes, &l); err != nil {
			continue
		}

		if l.Payload.Info.TotalTokenUsage != nil {
			lastTokens = l.Payload.Info.TotalTokenUsage.TotalTokens
		}

		if l.Payload.RateLimits != nil && l.Payload.RateLimits.Credits != nil && l.Payload.RateLimits.Credits.Balance != nil {
			if val, ok := parseBalance(l.Payload.RateLimits.Credits.Balance); ok {
				if !hasFirstBalance {
					firstBalance = val
					hasFirstBalance = true
				}
				lastBalance = val
				hasLastBalance = true
			}
		}
	}

	var cost string
	if hasFirstBalance && hasLastBalance {
		n := int(math.Round(firstBalance - lastBalance))
		if n > 0 {
			cost = fmt.Sprintf("%d cr", n)
		}
	}

	return lastTokens, cost, true
}

func parseBalance(b any) (float64, bool) {
	switch v := b.(type) {
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func (h *History) enrichOpencode(c *Run) (int64, string) {
	dbPath := h.OpencodeDBPath
	if dbPath == "" {
		dbPath = os.Getenv("OPENCODE_DB")
		if dbPath == "" {
			u := os.Getenv("USERPROFILE")
			if u == "" {
				u, _ = os.UserHomeDir()
			}
			dbPath = filepath.Join(u, ".local", "share", "opencode", "opencode.db")
		}
	}

	if _, err := os.Stat(dbPath); err != nil {
		return 0, ""
	}

	slashPath := filepath.ToSlash(dbPath)
	dsn := fmt.Sprintf("file:%s?mode=ro", slashPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, ""
	}
	defer db.Close()

	startMs := c.Start.UnixMilli()
	endMs := c.End.UnixMilli()

	query := `SELECT 
		COALESCE(SUM(json_extract(data, '$.tokens.input')), 0) + COALESCE(SUM(json_extract(data, '$.tokens.output')), 0),
		COALESCE(SUM(json_extract(data, '$.cost')), 0.0)
	FROM message
	WHERE json_extract(data, '$.role') = 'assistant'
	  AND time_created >= ? AND time_created <= ?`

	var tokens int64
	var sum float64
	if err := db.QueryRow(query, startMs, endMs).Scan(&tokens, &sum); err != nil {
		return 0, ""
	}

	var cost string
	if sum > 0 {
		cost = fmt.Sprintf("$%.2f", sum)
	}

	return tokens, cost
}

// Summary returns a one-line summary of the runs for the given day:
// "today: N runs · X done · Y out of quota · Z failed · W cr spent",
// leaving out the parts that are zero.
func Summary(cs []Run, day time.Time) string {
	if day.IsZero() {
		day = time.Now()
	}
	y2, m2, d2 := day.Date()
	loc := day.Location()

	var n, done, outOfQuota, failed, credits int

	for _, c := range cs {
		y1, m1, d1 := c.Start.In(loc).Date()
		if y1 != y2 || m1 != m2 || d1 != d2 {
			continue
		}
		n++
		switch c.Status {
		case runs.Done:
			done++
		case runs.OutOfQuota:
			outOfQuota++
		case runs.Failed, runs.Timeout, runs.Interrupted, runs.NoPermission:
			failed++
		}

		if strings.HasSuffix(c.Cost, " cr") {
			crStr := strings.TrimSpace(strings.TrimSuffix(c.Cost, " cr"))
			if val, err := strconv.Atoi(crStr); err == nil && val > 0 {
				credits += val
			}
		}
	}

	parts := []string{fmt.Sprintf("today: %d %s", n, plural(n, "run", "runs"))}
	if done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", done))
	}
	if outOfQuota > 0 {
		parts = append(parts, fmt.Sprintf("%d out of quota", outOfQuota))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if credits > 0 {
		parts = append(parts, fmt.Sprintf("%d cr spent", credits))
	}

	return strings.Join(parts, " · ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
