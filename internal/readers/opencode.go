package readers

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/live"
	"github.com/AlbertoVasquezR/panal/internal/state"

	_ "modernc.org/sqlite"
)

type quotaErrorKind int

const (
	errNone quotaErrorKind = iota
	err5h
	errExceeded
	errPaused
)

type quotaErrorInfo struct {
	kind      quotaErrorKind
	at        time.Time
	resetsMin int
	text      string
}

type providerStats struct {
	Provider     string
	Messages     int
	InputTokens  int64
	OutputTokens int64
	Cost         float64
}

// Opencode implements Reader for the "opencode" agent, wrapping Delegate and
// querying the local log and database for quota, tokens and cost.
type Opencode struct {
	Delegate *Delegate
	Live     interface{ LatestOpencode() live.OpencodeReading } // nil = local data only
	LogPath  string
	DBPath   string
	Now      func() time.Time

	// Counters for cache tests
	LogReads  int
	DBQueries int

	// Log cache
	cacheLogModTime time.Time
	cacheLogSize    int64
	cacheLogErr     *quotaErrorInfo
	cacheLogOk      bool

	// Database cache
	cacheDBModTime  time.Time
	cacheWALModTime time.Time
	cacheLastUse    time.Time
	cacheStats24h   []providerStats
	cacheDBOk       bool
}

// NewOpencode creates the reader with the real system paths.
func NewOpencode() *Opencode {
	logPath := os.Getenv("OPENCODE_LOG")
	if logPath == "" {
		u := os.Getenv("USERPROFILE")
		if u == "" {
			u, _ = os.UserHomeDir()
		}
		logPath = filepath.Join(u, ".local", "share", "opencode", "log", "opencode.log")
	}

	dbPath := os.Getenv("OPENCODE_DB")
	if dbPath == "" {
		u := os.Getenv("USERPROFILE")
		if u == "" {
			u, _ = os.UserHomeDir()
		}
		dbPath = filepath.Join(u, ".local", "share", "opencode", "opencode.db")
	}

	return &Opencode{
		Delegate: NewDelegate("opencode"),
		LogPath:  logPath,
		DBPath:   dbPath,
		Now:      time.Now,
	}
}

func (o *Opencode) Agent() string {
	if o.Delegate != nil {
		return o.Delegate.Agent()
	}
	return "opencode"
}

// Read is what its log and database say and, if there is a newer live
// reading (the OpenCode Go usage endpoint), that one wins: the plan's three
// windows with their resets. If any of them is used up, it is out of quota.
func (o *Opencode) Read() state.Row {
	row := o.readLocal()
	if o.Live == nil {
		return row
	}
	l := o.Live.LatestOpencode()
	if !l.HasData || !l.At.After(row.Quota.SeenAt) {
		return row
	}
	row.Quota = l.Quota
	switch {
	case l.Exhausted && row.Status != state.Working && row.Status != state.Stuck:
		row.Status = state.OutOfQuota
	case !l.Exhausted && row.Status == state.OutOfQuota:
		row.Status = state.Idle
	}
	note := "live quota: from opencode.ai/zen/go/v1/usage (Go plan)"
	if row.Detail != "" {
		row.Detail = note + "\n" + row.Detail
	} else {
		row.Detail = note
	}
	return row
}

func (o *Opencode) readLocal() state.Row {
	var row state.Row
	if o.Delegate != nil {
		if o.Now != nil {
			o.Delegate.Now = o.Now
		}
		row = o.Delegate.Read()
	} else {
		row = state.Row{
			Agent:  "opencode",
			Status: state.NoData,
		}
	}

	nowFn := o.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()

	lastErr := o.lastLogError()
	lastUse, stats24h := o.dbData(now)

	// Work out the quota
	quota := state.Quota{
		Exact: false,
	}

	loc := now.Location()
	if loc == nil {
		loc = time.Local
	}

	hasError := (lastErr != nil)
	hasUse := !lastUse.IsZero()

	// Is the last opencode-go error NEWER than the last successful use with tokens.output > 0?
	errorIsNewer := hasError && (!hasUse || lastErr.at.After(lastUse))

	if errorIsNewer {
		quota.SeenAt = lastErr.at
		switch lastErr.kind {
		case err5h:
			resetAt := lastErr.at.Add(time.Duration(lastErr.resetsMin) * time.Minute)
			quota.ResetsAt = resetAt
			hourStr := resetAt.In(loc).Format("15:04")
			if now.After(resetAt) {
				quota.Summary = "Go 5 h: should have reset at " + hourStr
			} else {
				quota.Summary = "Go 5 h used up · resets " + hourStr
			}
		case errExceeded:
			d := now.Sub(lastErr.at)
			if d < 0 {
				d = 0
			}
			quota.Summary = fmt.Sprintf("Go used up (%s ago)", Ago(d))
		case errPaused:
			d := now.Sub(lastErr.at)
			if d < 0 {
				d = 0
			}
			quota.Summary = fmt.Sprintf("Go paused (%s ago)", Ago(d))
		}
	} else if hasUse {
		quota.SeenAt = lastUse
		d := now.Sub(lastUse)
		if d < 0 {
			d = 0
		}
		quota.Summary = fmt.Sprintf("Go ok (last used %s ago)", Ago(d))
	} else {
		quota.Summary = "no data"
	}

	row.Quota = quota

	// State rules
	if row.Status == state.OutOfQuota && strings.HasPrefix(quota.Summary, "Go ok") {
		row.Status = state.Idle
	}
	if row.Status == state.Working && hasError && lastErr.at.After(row.Since) {
		row.Status = state.OutOfQuota
	}

	// Detail
	var b strings.Builder
	if hasError {
		errHour := lastErr.at.In(loc).Format("15:04")
		fmt.Fprintf(&b, "last quota error: %s · %s", errHour, lastErr.text)
	}
	if len(stats24h) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("last 24 h:")
		for _, s := range stats24h {
			fmt.Fprintf(&b, "\n  %s: %d messages · %d in · %d out · $%.2f",
				s.Provider, s.Messages, s.InputTokens, s.OutputTokens, s.Cost)
		}
	}

	opencodeDetail := b.String()
	if opencodeDetail != "" {
		if row.Detail != "" {
			row.Detail = row.Detail + "\n\n" + opencodeDetail
		} else {
			row.Detail = opencodeDetail
		}
	}

	return row
}

// Patterns of opencode's own log lines and error messages (external text).
var (
	reProviderGo = regexp.MustCompile(`(?:^|\s)providerID="?opencode-go"?(\s|$)`)
	reLevelErr   = regexp.MustCompile(`(?:^|\s)level="?ERROR"?(\s|$)`)
	reTimestamp  = regexp.MustCompile(`(?:^|\s)timestamp="?([^"\s]+)"?`)
	re5hLimit    = regexp.MustCompile(`5-hour usage limit reached\. Resets in (\d+)min`)
)

func (o *Opencode) lastLogError() *quotaErrorInfo {
	if o.LogPath == "" {
		return nil
	}
	info, err := os.Stat(o.LogPath)
	if err != nil {
		o.cacheLogOk = false
		o.cacheLogErr = nil
		return nil
	}

	if o.cacheLogOk && info.Size() == o.cacheLogSize && info.ModTime().Equal(o.cacheLogModTime) {
		return o.cacheLogErr
	}

	o.LogReads++

	qErr := findLastLogError(o.LogPath, info.Size())
	o.cacheLogSize = info.Size()
	o.cacheLogModTime = info.ModTime()
	o.cacheLogErr = qErr
	o.cacheLogOk = true

	return qErr
}

func findLastLogError(path string, size int64) *quotaErrorInfo {
	const maxTail = 1024 * 1024 // 1 MB
	var data []byte

	if size <= maxTail {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return nil
		}
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		offset := size - maxTail
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil
		}
		buf := make([]byte, maxTail)
		n, err := io.ReadFull(f, buf)
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return nil
		}
		buf = buf[:n]
		if idx := bytes.IndexByte(buf, '\n'); idx >= 0 {
			buf = buf[idx+1:]
		}
		data = buf
	}

	lines := bytes.Split(data, []byte("\n"))
	var newest *quotaErrorInfo

	for _, lineBytes := range lines {
		lineBytes = bytes.TrimRight(lineBytes, "\r\n")
		if len(lineBytes) == 0 {
			continue
		}
		line := string(lineBytes)

		if !reProviderGo.MatchString(line) || !reLevelErr.MatchString(line) {
			continue
		}

		tsMatch := reTimestamp.FindStringSubmatch(line)
		if len(tsMatch) < 2 {
			continue
		}
		tEv, err := time.Parse(time.RFC3339Nano, tsMatch[1])
		if err != nil {
			tEv, err = time.Parse(time.RFC3339, tsMatch[1])
			if err != nil {
				continue
			}
		}

		var kind quotaErrorKind
		var resetsMin int
		var text string

		if m := re5hLimit.FindStringSubmatch(line); len(m) > 1 {
			kind = err5h
			resetsMin, _ = strconv.Atoi(m[1])
			text = m[0]
		} else if strings.Contains(line, "Go usage limit exceeded") {
			kind = errExceeded
			text = "Go usage limit exceeded"
		} else if strings.Contains(line, "API access paused for this organization") {
			kind = errPaused
			text = "API access paused for this organization"
		} else {
			continue
		}

		info := &quotaErrorInfo{
			kind:      kind,
			at:        tEv,
			resetsMin: resetsMin,
			text:      text,
		}

		if newest == nil || info.at.After(newest.at) || info.at.Equal(newest.at) {
			newest = info
		}
	}

	return newest
}

func (o *Opencode) dbData(now time.Time) (time.Time, []providerStats) {
	if o.DBPath == "" {
		return time.Time{}, nil
	}
	dbInfo, err := os.Stat(o.DBPath)
	if err != nil {
		o.cacheDBOk = false
		o.cacheLastUse = time.Time{}
		o.cacheStats24h = nil
		return time.Time{}, nil
	}

	dbModTime := dbInfo.ModTime()
	var walModTime time.Time
	if walInfo, errWal := os.Stat(o.DBPath + "-wal"); errWal == nil {
		walModTime = walInfo.ModTime()
	}

	if o.cacheDBOk && dbModTime.Equal(o.cacheDBModTime) && walModTime.Equal(o.cacheWALModTime) {
		return o.cacheLastUse, o.cacheStats24h
	}

	o.DBQueries++

	lastUse, stats := queryDB(o.DBPath, now)
	o.cacheDBModTime = dbModTime
	o.cacheWALModTime = walModTime
	o.cacheLastUse = lastUse
	o.cacheStats24h = stats
	o.cacheDBOk = true

	return lastUse, stats
}

func queryDB(dbPath string, now time.Time) (time.Time, []providerStats) {
	slashPath := filepath.ToSlash(dbPath)
	dsn := fmt.Sprintf("file:%s?mode=ro", slashPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return time.Time{}, nil
	}
	defer db.Close()

	var lastUse time.Time
	var timeCreatedMs sql.NullInt64
	queryUse := `SELECT time_created FROM message WHERE json_extract(data, '$.role') = 'assistant' AND json_extract(data, '$.providerID') = 'opencode-go' AND json_extract(data, '$.tokens.output') > 0 ORDER BY time_created DESC LIMIT 1`
	if err := db.QueryRow(queryUse).Scan(&timeCreatedMs); err == nil && timeCreatedMs.Valid {
		lastUse = time.UnixMilli(timeCreatedMs.Int64)
	}

	since24hMs := now.Add(-24 * time.Hour).UnixMilli()
	queryStats := `SELECT COALESCE(json_extract(data, '$.providerID'), 'unknown') AS prov, COUNT(*), COALESCE(SUM(json_extract(data, '$.tokens.input')), 0), COALESCE(SUM(json_extract(data, '$.tokens.output')), 0), COALESCE(SUM(json_extract(data, '$.cost')), 0.0) FROM message WHERE time_created >= ? AND json_extract(data, '$.role') = 'assistant' GROUP BY prov ORDER BY prov`
	rows, err := db.Query(queryStats, since24hMs)
	if err != nil {
		return lastUse, nil
	}
	defer rows.Close()

	var stats []providerStats
	for rows.Next() {
		var s providerStats
		if err := rows.Scan(&s.Provider, &s.Messages, &s.InputTokens, &s.OutputTokens, &s.Cost); err == nil {
			stats = append(stats, s)
		}
	}

	return lastUse, stats
}
