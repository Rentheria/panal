package readers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/live"
	"github.com/AlbertoVasquezR/panal/internal/runs"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

const quotaHeaderSuffix = "Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:"

// Agy implements Reader for the "agy" agent, wrapping Delegate for the
// overall state and reading quotas from the delegated runs' logs.
type Agy struct {
	Delegate  *Delegate
	Live      interface{ LatestAgy() live.AgyReading } // nil = logs only
	LogDirs   []string                                 // log directories (where *-agy-*.txt.log live)
	Now       func() time.Time
	FileReads int

	fileCache map[string]fileCacheItem
}

type fileCacheItem struct {
	modTime time.Time
	size    int64
	capture *quotaCapture
}

type quotaCapture struct {
	summary agyQuotaSummaryJSON
	seenAt  time.Time
}

type agyBucketJSON struct {
	BucketID          string   `json:"bucketId"`
	DisplayName       string   `json:"displayName"`
	Window            string   `json:"window"`
	ResetTime         string   `json:"resetTime"`
	Description       string   `json:"description"`
	RemainingFraction *float64 `json:"remainingFraction"`
}

type agyGroupJSON struct {
	DisplayName string          `json:"displayName"`
	Description string          `json:"description"`
	Buckets     []agyBucketJSON `json:"buckets"`
}

type agyQuotaSummaryJSON struct {
	Groups      []agyGroupJSON `json:"groups"`
	Description string         `json:"description"`
}

// NewAgy creates the reader with the real system paths.
func NewAgy() *Agy {
	return &Agy{
		Delegate: NewDelegate("agy"),
		LogDirs:  runs.LogDirs(),
		Now:      time.Now,
	}
}

func (a *Agy) Agent() string {
	if a.Delegate != nil {
		return a.Delegate.Agent()
	}
	return "agy"
}

// Read is what its logs say and, if there is a newer live reading
// (agy -p /usage), that one wins for its model's group.
func (a *Agy) Read() state.Row {
	row := a.readLogs()
	if a.Live == nil {
		return row
	}
	l := a.Live.LatestAgy()
	if !l.HasData || !l.At.After(row.Quota.SeenAt) {
		return row
	}
	g, ok := l.GroupFor(row.Model)
	if !ok {
		return row
	}
	q := g.Quota
	q.Summary = g.Name
	row.Quota = q
	free := true
	for _, b := range q.Bars {
		free = free && b.UsedPct < 100
	}
	if row.Status == state.OutOfQuota && free {
		row.Status = state.Idle
	}
	note := "live quota: from agy -p /usage · group " + g.Name
	if row.Detail != "" {
		row.Detail = note + "\n" + row.Detail
	} else {
		row.Detail = note
	}
	return row
}

func (a *Agy) readLogs() state.Row {
	var row state.Row
	if a.Delegate != nil {
		if a.Now != nil {
			a.Delegate.Now = a.Now
		}
		row = a.Delegate.Read()
	} else {
		row = state.Row{
			Agent:  "agy",
			Status: state.NoData,
		}
	}

	nowFn := a.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()

	runEnd := row.Quota.SeenAt

	capture, err := a.findLatestCapture()
	if err != nil || capture == nil {
		row.Quota = state.Quota{Summary: "no data (run agy with panal delegate)"}
		return row
	}

	group := pickGroup(row.Model, capture.summary.Groups)
	if group == nil {
		row.Quota = state.Quota{Summary: "no data (run agy with panal delegate)"}
		return row
	}

	var b5h, bWeekly *agyBucketJSON
	for i := range group.Buckets {
		b := &group.Buckets[i]
		if b.Window == "5h" || strings.Contains(b.BucketID, "5h") {
			b5h = b
		}
		if b.Window == "weekly" || strings.Contains(b.BucketID, "weekly") {
			bWeekly = b
		}
	}

	rem5h := 1.0
	if b5h != nil && b5h.RemainingFraction != nil {
		rem5h = *b5h.RemainingFraction
	}
	remWk := 1.0
	if bWeekly != nil && bWeekly.RemainingFraction != nil {
		remWk = *bWeekly.RemainingFraction
	}

	quota := state.Quota{
		Exact:   true,
		UsedPct: (1.0 - rem5h) * 100.0,
		SeenAt:  capture.seenAt,
	}

	if b5h != nil && b5h.ResetTime != "" {
		if t5h, err := time.Parse(time.RFC3339, b5h.ResetTime); err == nil {
			quota.ResetsAt = t5h.Local()
		}
	}

	quota.Bars = append(quota.Bars, state.Bar{Name: "5h", UsedPct: quota.UsedPct, ResetsAt: quota.ResetsAt})
	// "sem" is the weekly bar's key across packages (state, ui, forecast).
	weekBar := state.Bar{Name: "sem", UsedPct: (1.0 - remWk) * 100.0}
	if bWeekly != nil && bWeekly.ResetTime != "" {
		if t, err := time.Parse(time.RFC3339, bWeekly.ResetTime); err == nil {
			weekBar.ResetsAt = t.Local()
		}
	}
	quota.Bars = append(quota.Bars, weekBar)

	weeklyPct := int(math.Round((1.0 - remWk) * 100.0))
	if bWeekly != nil && bWeekly.ResetTime != "" {
		if tWk, err := time.Parse(time.RFC3339, bWeekly.ResetTime); err == nil {
			quota.Summary = fmt.Sprintf("week %d%% · resets %s", weeklyPct, tWk.Local().Format("02-Jan 15:04"))
		} else {
			quota.Summary = fmt.Sprintf("week %d%%", weeklyPct)
		}
	} else {
		quota.Summary = fmt.Sprintf("week %d%%", weeklyPct)
	}

	// State: if Delegate says OutOfQuota but the group's 5 h and weekly
	// buckets have remainingFraction > 0 and the data is newer than the end of
	// that attempt, Idle.
	if row.Status == state.OutOfQuota && rem5h > 0 && remWk > 0 && (runEnd.IsZero() || capture.seenAt.After(runEnd)) {
		row.Status = state.Idle
	}

	agyDetail := formatDetail(capture.summary, capture.seenAt, now)
	row.Quota = quota
	if row.Detail != "" {
		row.Detail = row.Detail + "\n\n" + agyDetail
	} else {
		row.Detail = agyDetail
	}

	return row
}

type fileCandidate struct {
	path    string
	modTime time.Time
	size    int64
}

func (a *Agy) findLatestCapture() (*quotaCapture, error) {
	var candidates []fileCandidate
	for _, dir := range a.LogDirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			matched, _ := filepath.Match("*-agy-*.txt.log", name)
			if !matched {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			candidates = append(candidates, fileCandidate{
				path:    filepath.Join(dir, name),
				modTime: info.ModTime(),
				size:    info.Size(),
			})
		}
	}

	if len(candidates) == 0 {
		return nil, os.ErrNotExist
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].path > candidates[j].path
		}
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	if a.fileCache == nil {
		a.fileCache = make(map[string]fileCacheItem)
	}

	for _, cand := range candidates {
		item, ok := a.fileCache[cand.path]
		if ok && item.modTime.Equal(cand.modTime) && item.size == cand.size {
			if item.capture != nil {
				return item.capture, nil
			}
			continue
		}

		a.FileReads++
		capture, _ := parseLogFile(cand.path, cand.modTime.Year())
		a.fileCache[cand.path] = fileCacheItem{
			modTime: cand.modTime,
			size:    cand.size,
			capture: capture,
		}

		if capture != nil {
			return capture, nil
		}
	}

	return nil, nil
}

func parseLogFile(path string, fileYear int) (*quotaCapture, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var (
		lastCapture *quotaCapture
		inJSON      bool
		currentSeen time.Time
		braceCount  int
		jsonBuf     strings.Builder
	)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if inJSON {
			if isGlogLine(line) {
				inJSON = false
				var summary agyQuotaSummaryJSON
				if err := json.Unmarshal([]byte(jsonBuf.String()), &summary); err == nil && len(summary.Groups) > 0 {
					lastCapture = &quotaCapture{summary: summary, seenAt: currentSeen}
				}
				jsonBuf.Reset()
				braceCount = 0
			} else {
				jsonBuf.WriteString(line)
				jsonBuf.WriteByte('\n')
				for _, r := range line {
					if r == '{' {
						braceCount++
					} else if r == '}' {
						braceCount--
					}
				}
				if braceCount == 0 && jsonBuf.Len() > 0 {
					inJSON = false
					var summary agyQuotaSummaryJSON
					if err := json.Unmarshal([]byte(jsonBuf.String()), &summary); err == nil && len(summary.Groups) > 0 {
						lastCapture = &quotaCapture{summary: summary, seenAt: currentSeen}
					}
					jsonBuf.Reset()
				}
				continue
			}
		}

		if strings.HasSuffix(trimmed, quotaHeaderSuffix) {
			currentSeen = parseGlogTime(line, fileYear)
			inJSON = true
			braceCount = 0
			jsonBuf.Reset()
		}
	}

	if inJSON && jsonBuf.Len() > 0 {
		var summary agyQuotaSummaryJSON
		if err := json.Unmarshal([]byte(jsonBuf.String()), &summary); err == nil && len(summary.Groups) > 0 {
			lastCapture = &quotaCapture{summary: summary, seenAt: currentSeen}
		}
	}

	return lastCapture, scanner.Err()
}

func isGlogLine(line string) bool {
	if len(line) < 6 {
		return false
	}
	c := line[0]
	if c != 'I' && c != 'W' && c != 'E' && c != 'F' {
		return false
	}
	for i := 1; i <= 4; i++ {
		if line[i] < '0' || line[i] > '9' {
			return false
		}
	}
	return line[5] == ' '
}

func parseGlogTime(line string, year int) time.Time {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return time.Time{}
	}
	f0 := fields[0]
	if len(f0) < 5 {
		return time.Time{}
	}
	f0 = f0[1:5]
	f1 := fields[1]
	if year <= 0 {
		year = time.Now().Year()
	}
	raw := fmt.Sprintf("%04d%s %s", year, f0, f1)
	t, err := time.ParseInLocation("20060102 15:04:05.999999", raw, time.Local)
	if err != nil {
		t, err = time.ParseInLocation("20060102 15:04:05", raw, time.Local)
	}
	if err != nil {
		return time.Time{}
	}
	return t
}

func pickGroup(model string, groups []agyGroupJSON) *agyGroupJSON {
	wanted := "Gemini Models"
	modLower := strings.ToLower(strings.TrimSpace(model))
	if modLower != "" && !strings.HasPrefix(modLower, "gemini") {
		wanted = "Claude and GPT models"
	}
	for i := range groups {
		if strings.EqualFold(groups[i].DisplayName, wanted) {
			return &groups[i]
		}
	}
	for i := range groups {
		if strings.Contains(strings.ToLower(groups[i].DisplayName), strings.ToLower(wanted)) {
			return &groups[i]
		}
	}
	if len(groups) > 0 {
		return &groups[0]
	}
	return nil
}

func formatDetail(summary agyQuotaSummaryJSON, seenAt, now time.Time) string {
	var b strings.Builder
	for _, g := range summary.Groups {
		for _, bucket := range g.Buckets {
			rem := 1.0
			if bucket.RemainingFraction != nil {
				rem = *bucket.RemainingFraction
			}
			pct := int(math.Round((1.0 - rem) * 100.0))
			resetStr := ""
			if bucket.ResetTime != "" {
				if tReset, err := time.Parse(time.RFC3339, bucket.ResetTime); err == nil {
					resetStr = " · resets " + tReset.Local().Format("02-Jan 15:04")
				}
			}
			fmt.Fprintf(&b, "%s · %s: %d%% used%s\n", g.DisplayName, bucket.Window, pct, resetStr)
		}
	}
	d := now.Sub(seenAt)
	if d < 0 {
		d = 0
	}
	fmt.Fprintf(&b, "seen %s ago", Ago(d))
	if d > 6*time.Hour {
		b.WriteString("\nstale data: updates on the next agy run")
	}
	return b.String()
}
