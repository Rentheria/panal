package readers

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"

	_ "modernc.org/sqlite"
)

func createSyntheticDB(t *testing.T, dir string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s", filepath.ToSlash(dbPath)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE message (
			id text PRIMARY KEY,
			session_id text NOT NULL,
			time_created integer NOT NULL,
			time_updated integer NOT NULL,
			data text NOT NULL
		);
		CREATE TABLE account (
			id text PRIMARY KEY,
			secret_token text
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func insertSyntheticMessage(t *testing.T, dbPath string, id string, timeCreated int64, providerID string, outTokens, inTokens int64, cost float64) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s", filepath.ToSlash(dbPath)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	data := fmt.Sprintf(`{"role":"assistant","providerID":%q,"tokens":{"input":%d,"output":%d},"cost":%f}`,
		providerID, inTokens, outTokens, cost)
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		id, "ses_1", timeCreated, timeCreated, data)
	if err != nil {
		t.Fatal(err)
	}
}

func logLine(ts string, level, provider, errText string) string {
	return fmt.Sprintf(`timestamp=%s level=%s run=abc message="stream error" providerID=%s modelID=deepseek-v4 error.error=%q`+"\n",
		ts, level, provider, errText)
}

func TestOpencode_ErrorKinds(t *testing.T) {
	t.Run("5 h error", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "opencode.log")
		dbPath := createSyntheticDB(t, dir)

		errText := "AI_APICallError: 5-hour usage limit reached. Resets in 20min. To continue using..."
		logContent := logLine("2026-09-24T10:00:00Z", "ERROR", "opencode-go", errText)
		if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
			t.Fatal(err)
		}

		now := time.Date(2026, 9, 24, 10, 5, 0, 0, time.UTC)
		reader := &Opencode{
			LogPath: logPath,
			DBPath:  dbPath,
			Now:     func() time.Time { return now },
		}

		f := reader.Read()
		if f.Quota.Exact {
			t.Fatalf("expected Exact == false")
		}
		want := "Go 5 h used up · resets 10:20"
		if f.Quota.Summary != want {
			t.Fatalf("expected Summary %q, got %q", want, f.Quota.Summary)
		}
		if f.Quota.SeenAt.Format(time.RFC3339) != "2026-09-24T10:00:00Z" {
			t.Fatalf("expected SeenAt 2026-09-24T10:00:00Z, got %v", f.Quota.SeenAt)
		}
	})

	t.Run("exceeded error", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "opencode.log")
		dbPath := createSyntheticDB(t, dir)

		logContent := logLine("2026-09-24T10:00:00Z", "ERROR", "opencode-go", "AI_APICallError: Go usage limit exceeded")
		if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
			t.Fatal(err)
		}

		now := time.Date(2026, 9, 24, 10, 12, 0, 0, time.UTC)
		reader := &Opencode{
			LogPath: logPath,
			DBPath:  dbPath,
			Now:     func() time.Time { return now },
		}

		f := reader.Read()
		want := "Go used up (12 min ago)"
		if f.Quota.Summary != want {
			t.Fatalf("expected Summary %q, got %q", want, f.Quota.Summary)
		}
	})

	t.Run("paused error", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "opencode.log")
		dbPath := createSyntheticDB(t, dir)

		logContent := logLine("2026-09-24T10:00:00Z", "ERROR", "opencode-go", "Upstream request failed: [permission_error] API access paused for this organization")
		if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
			t.Fatal(err)
		}

		now := time.Date(2026, 9, 24, 10, 30, 0, 0, time.UTC)
		reader := &Opencode{
			LogPath: logPath,
			DBPath:  dbPath,
			Now:     func() time.Time { return now },
		}

		f := reader.Read()
		want := "Go paused (30 min ago)"
		if f.Quota.Summary != want {
			t.Fatalf("expected Summary %q, got %q", want, f.Quota.Summary)
		}
	})
}

func TestOpencode_OldErrorNewUsage(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "opencode.log")
	dbPath := createSyntheticDB(t, dir)

	// Old error at 09:00:00
	logContent := logLine("2026-09-24T09:00:00Z", "ERROR", "opencode-go", "AI_APICallError: Go usage limit exceeded")
	if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
		t.Fatal(err)
	}

	// New message at 10:00:00 with tokens.output = 100
	tMsg := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	insertSyntheticMessage(t, dbPath, "m1", tMsg.UnixMilli(), "opencode-go", 100, 50, 0.05)

	now := time.Date(2026, 9, 24, 10, 20, 0, 0, time.UTC)
	reader := &Opencode{
		LogPath: logPath,
		DBPath:  dbPath,
		Now:     func() time.Time { return now },
	}

	f := reader.Read()
	want := "Go ok (last used 20 min ago)"
	if f.Quota.Summary != want {
		t.Fatalf("expected Summary %q, got %q", want, f.Quota.Summary)
	}
	if f.Quota.SeenAt.UnixMilli() != tMsg.UnixMilli() {
		t.Fatalf("expected SeenAt %v, got %v", tMsg, f.Quota.SeenAt)
	}
}

func TestOpencode_IgnoreOtherProvider(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "opencode.log")
	dbPath := createSyntheticDB(t, dir)

	// Errors with a providerID other than opencode-go
	logContent := logLine("2026-09-24T10:00:00Z", "ERROR", "opencode", "AI_APICallError: Go usage limit exceeded") +
		logLine("2026-09-24T10:01:00Z", "ERROR", "other-go", "AI_APICallError: 5-hour usage limit reached. Resets in 10min")
	if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 24, 10, 10, 0, 0, time.UTC)
	reader := &Opencode{
		LogPath: logPath,
		DBPath:  dbPath,
		Now:     func() time.Time { return now },
	}

	f := reader.Read()
	if f.Quota.Summary != "no data" {
		t.Fatalf("other providers' errors must be ignored; expected 'no data', got %q", f.Quota.Summary)
	}
}

func TestOpencode_Reset5hFutureAndPast(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "opencode.log")
	dbPath := createSyntheticDB(t, dir)

	// Error at 10:00 that resets in 30 min (10:30)
	errText := "5-hour usage limit reached. Resets in 30min"
	logContent := logLine("2026-09-24T10:00:00Z", "ERROR", "opencode-go", errText)
	if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Case 1: in the future (10:15)
	nowFuture := time.Date(2026, 9, 24, 10, 15, 0, 0, time.UTC)
	readerFuture := &Opencode{
		LogPath: logPath,
		DBPath:  dbPath,
		Now:     func() time.Time { return nowFuture },
	}
	f1 := readerFuture.Read()
	wantFuture := "Go 5 h used up · resets 10:30"
	if f1.Quota.Summary != wantFuture {
		t.Fatalf("future: expected %q, got %q", wantFuture, f1.Quota.Summary)
	}

	// Case 2: in the past (10:45)
	nowPast := time.Date(2026, 9, 24, 10, 45, 0, 0, time.UTC)
	readerPast := &Opencode{
		LogPath: logPath,
		DBPath:  dbPath,
		Now:     func() time.Time { return nowPast },
	}
	f2 := readerPast.Read()
	wantPast := "Go 5 h: should have reset at 10:30"
	if f2.Quota.Summary != wantPast {
		t.Fatalf("past: expected %q, got %q", wantPast, f2.Quota.Summary)
	}
}

func TestOpencode_LargeLogTail(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "opencode-large.log")
	dbPath := createSyntheticDB(t, dir)

	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}

	// Write more than 1.1 MB of filler
	padding := "timestamp=2026-09-24T09:00:00.000Z level=INFO message=\"some stream info\" providerID=opencode-go\n"
	for i := 0; i < 12000; i++ {
		if _, err := f.WriteString(padding); err != nil {
			t.Fatal(err)
		}
	}

	// Error at the end of the log
	errLine := logLine("2026-09-24T10:00:00Z", "ERROR", "opencode-go", "AI_APICallError: Go usage limit exceeded")
	if _, err := f.WriteString(errLine); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() <= 1024*1024 {
		t.Fatalf("the test file must exceed 1 MB, size: %d", fi.Size())
	}

	now := time.Date(2026, 9, 24, 10, 5, 0, 0, time.UTC)
	reader := &Opencode{
		LogPath: logPath,
		DBPath:  dbPath,
		Now:     func() time.Time { return now },
	}

	res := reader.Read()
	want := "Go used up (5 min ago)"
	if res.Quota.Summary != want {
		t.Fatalf("expected to find the error in the 1 MB tail, got %q", res.Quota.Summary)
	}
}

func TestOpencode_NoLogNorDB(t *testing.T) {
	dir := t.TempDir()
	reader := &Opencode{
		LogPath: filepath.Join(dir, "no_log.log"),
		DBPath:  filepath.Join(dir, "no_db.db"),
		Now:     time.Now,
	}

	f := reader.Read()
	if f.Quota.Exact {
		t.Fatalf("expected Exact == false")
	}
	if f.Quota.Summary != "no data" {
		t.Fatalf("expected Summary 'no data', got %q", f.Quota.Summary)
	}
	if f.Error != "" {
		t.Fatalf("no log and no database must not produce a reader error, got %q", f.Error)
	}
}

func TestOpencode_WorkingWithLaterError(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "opencode.log")
	dbPath := createSyntheticDB(t, dir)
	delegateDir := filepath.Join(dir, "delegate")
	if err := os.MkdirAll(delegateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Process running since 10:00:00
	tStart := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	stJSON := fmt.Sprintf(`{
		"version": 1,
		"agent": "opencode",
		"model": "deepseek-v4",
		"start": %q,
		"status": "running",
		"pid": 9999
	}`, tStart.Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(delegateDir, "20260924-100000-opencode.json"), []byte(stJSON), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Delegate{
		Name:     "opencode",
		Dirs:     []string{delegateDir},
		PIDAlive: func(int) bool { return true },
	}

	t.Run("error after the start switches to OutOfQuota", func(t *testing.T) {
		// Error at 10:02:00 (after the 10:00:00 start)
		logContent := logLine("2026-09-24T10:02:00Z", "ERROR", "opencode-go", "AI_APICallError: Go usage limit exceeded")
		if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
			t.Fatal(err)
		}

		reader := &Opencode{
			Delegate: d,
			LogPath:  logPath,
			DBPath:   dbPath,
			Now:      func() time.Time { return time.Date(2026, 9, 24, 10, 3, 0, 0, time.UTC) },
		}

		f := reader.Read()
		if f.Status != state.OutOfQuota {
			t.Fatalf("expected OutOfQuota status, got %v (%s)", f.Status, f.Status)
		}
	})

	t.Run("error before the start keeps Working", func(t *testing.T) {
		// Error at 09:55:00 (before the 10:00:00 start)
		logContent := logLine("2026-09-24T09:55:00Z", "ERROR", "opencode-go", "AI_APICallError: Go usage limit exceeded")
		if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
			t.Fatal(err)
		}

		reader := &Opencode{
			Delegate: d,
			LogPath:  logPath,
			DBPath:   dbPath,
			Now:      func() time.Time { return time.Date(2026, 9, 24, 10, 3, 0, 0, time.UTC) },
		}

		f := reader.Read()
		if f.Status != state.Working {
			t.Fatalf("an error before the start must not change the status; expected Working, got %v", f.Status)
		}
	})
}

func TestOpencode_OutOfQuotaWithRecentUseGoesIdle(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "opencode.log")
	dbPath := createSyntheticDB(t, dir)
	delegateDir := filepath.Join(dir, "delegate")
	if err := os.MkdirAll(delegateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Delegate finished with out_of_quota at 09:00:00
	stJSON := `{
		"version": 1,
		"agent": "opencode",
		"model": "deepseek-v4",
		"start": "2026-09-24T09:00:00Z",
		"status": "out_of_quota",
		"end": "2026-09-24T09:00:05Z",
		"rc": 1
	}`
	if err := os.WriteFile(filepath.Join(delegateDir, "20260924-090000-opencode.json"), []byte(stJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// But the database has a successful opencode-go message at 10:00:00
	tMsg := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	insertSyntheticMessage(t, dbPath, "m1", tMsg.UnixMilli(), "opencode-go", 200, 100, 0.02)

	d := &Delegate{
		Name:     "opencode",
		Dirs:     []string{delegateDir},
		PIDAlive: func(int) bool { return false },
	}

	reader := &Opencode{
		Delegate: d,
		LogPath:  logPath,
		DBPath:   dbPath,
		Now:      func() time.Time { return time.Date(2026, 9, 24, 10, 5, 0, 0, time.UTC) },
	}

	f := reader.Read()
	if !strings.HasPrefix(f.Quota.Summary, "Go ok") {
		t.Fatalf("expected a Summary starting with 'Go ok', got %q", f.Quota.Summary)
	}
	if f.Status != state.Idle {
		t.Fatalf("if Delegate says OutOfQuota but Summary is Go ok, expected Idle, got %v", f.Status)
	}
}

func TestOpencode_Cache(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "opencode.log")
	dbPath := createSyntheticDB(t, dir)

	logContent := logLine("2026-09-24T10:00:00Z", "ERROR", "opencode-go", "AI_APICallError: Go usage limit exceeded")
	if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
		t.Fatal(err)
	}
	insertSyntheticMessage(t, dbPath, "m1", time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC).UnixMilli(), "opencode-go", 100, 50, 0.01)

	reader := &Opencode{
		LogPath: logPath,
		DBPath:  dbPath,
		Now:     func() time.Time { return time.Date(2026, 9, 24, 10, 5, 0, 0, time.UTC) },
	}

	// 1. First read
	f1 := reader.Read()
	if reader.LogReads != 1 || reader.DBQueries != 1 {
		t.Fatalf("read 1: expected LogReads=1, DBQueries=1; got %d, %d", reader.LogReads, reader.DBQueries)
	}
	if f1.Quota.Summary != "Go used up (5 min ago)" {
		t.Fatalf("read 1: unexpected summary %q", f1.Quota.Summary)
	}

	// 2. Second read without changes: must use the cache
	f2 := reader.Read()
	if reader.LogReads != 1 || reader.DBQueries != 1 {
		t.Fatalf("read 2 (no changes): expected the cache to be used (1, 1); got %d, %d", reader.LogReads, reader.DBQueries)
	}
	if f2.Quota.Summary != f1.Quota.Summary {
		t.Fatalf("read 2: summary changed unexpectedly")
	}

	// 3. Modify the log: must re-read only the log
	newLog := logContent + logLine("2026-09-24T10:10:00Z", "ERROR", "opencode-go", "AI_APICallError: Go usage limit exceeded")
	if err := os.WriteFile(logPath, []byte(newLog), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(5 * time.Minute)
	_ = os.Chtimes(logPath, future, future)

	reader.Now = func() time.Time { return time.Date(2026, 9, 24, 10, 15, 0, 0, time.UTC) }
	f3 := reader.Read()
	if reader.LogReads != 2 || reader.DBQueries != 1 {
		t.Fatalf("read 3 (log modified): expected LogReads=2, DBQueries=1; got %d, %d", reader.LogReads, reader.DBQueries)
	}
	if f3.Quota.Summary != "Go used up (5 min ago)" {
		t.Fatalf("read 3: unexpected summary %q", f3.Quota.Summary)
	}

	// 4. Modify the database: must query the database again
	insertSyntheticMessage(t, dbPath, "m2", time.Date(2026, 9, 24, 10, 20, 0, 0, time.UTC).UnixMilli(), "opencode-go", 500, 200, 0.05)
	_ = os.Chtimes(dbPath, future.Add(time.Minute), future.Add(time.Minute))

	reader.Now = func() time.Time { return time.Date(2026, 9, 24, 10, 25, 0, 0, time.UTC) }
	f4 := reader.Read()
	if reader.LogReads != 2 || reader.DBQueries != 2 {
		t.Fatalf("read 4 (DB modified): expected LogReads=2, DBQueries=2; got %d, %d", reader.LogReads, reader.DBQueries)
	}
	if f4.Quota.Summary != "Go ok (last used 5 min ago)" {
		t.Fatalf("read 4: unexpected summary after updating the DB: %q", f4.Quota.Summary)
	}
}

func TestOpencode_DBIsolationAndForbiddenWords(t *testing.T) {
	dir := t.TempDir()
	dbPath := createSyntheticDB(t, dir)

	// Fill a secondary, off-limits table with data
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s", filepath.ToSlash(dbPath)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO account (id, secret_token) VALUES ('acc_1', 'secret_token_xyz')`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Insert a legitimate message into message
	insertSyntheticMessage(t, dbPath, "m1", time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC).UnixMilli(), "opencode-go", 100, 50, 0.02)

	reader := &Opencode{
		DBPath: dbPath,
		Now:    func() time.Time { return time.Date(2026, 9, 24, 10, 10, 0, 0, time.UTC) },
	}

	f := reader.Read()
	if !strings.HasPrefix(f.Quota.Summary, "Go ok") {
		t.Fatalf("expected a successful read of message even with other tables present, got %q", f.Quota.Summary)
	}

	// Check that opencode.go's source does NOT contain the forbidden words
	srcBytes, err := os.ReadFile("opencode.go")
	if err != nil {
		t.Fatal(err)
	}
	srcLower := strings.ToLower(string(srcBytes))
	forbidden := []string{"account", "credential", "control_account"}
	for _, w := range forbidden {
		if strings.Contains(srcLower, w) {
			t.Fatalf("opencode.go contains the forbidden word: %q", w)
		}
	}
}

func TestOpencode_Detail(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "opencode.log")
	dbPath := createSyntheticDB(t, dir)
	delegateDir := filepath.Join(dir, "delegate")
	if err := os.MkdirAll(delegateDir, 0755); err != nil {
		t.Fatal(err)
	}

	stJSON := `{
		"version": 1,
		"agent": "opencode",
		"model": "deepseek-v4",
		"start": "2026-09-24T09:00:00Z",
		"status": "done",
		"end": "2026-09-24T09:10:00Z",
		"rc": 0
	}`
	if err := os.WriteFile(filepath.Join(delegateDir, "20260924-090000-opencode.json"), []byte(stJSON), 0644); err != nil {
		t.Fatal(err)
	}

	logContent := logLine("2026-09-24T09:05:00Z", "ERROR", "opencode-go", "AI_APICallError: Go usage limit exceeded")
	if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	// Message within the last 24 h for opencode-go
	insertSyntheticMessage(t, dbPath, "m1", now.Add(-2*time.Hour).UnixMilli(), "opencode-go", 250, 1000, 1.25)
	// Message within the last 24 h for opencode
	insertSyntheticMessage(t, dbPath, "m2", now.Add(-3*time.Hour).UnixMilli(), "opencode", 100, 500, 0.00)
	// Message OUTSIDE the last 24 h (26 h ago)
	insertSyntheticMessage(t, dbPath, "m3", now.Add(-26*time.Hour).UnixMilli(), "opencode-go", 9999, 9999, 50.00)

	d := &Delegate{
		Name:     "opencode",
		Dirs:     []string{delegateDir},
		PIDAlive: func(int) bool { return false },
	}

	reader := &Opencode{
		Delegate: d,
		LogPath:  logPath,
		DBPath:   dbPath,
		Now:      func() time.Time { return now },
	}

	f := reader.Read()

	// 1. It must include Delegate's and Opencode's details separated by \n\n
	parts := strings.Split(f.Detail, "\n\n")
	if len(parts) < 2 {
		t.Fatalf("expected the details separated by a blank line (\\n\\n), detail: %q", f.Detail)
	}
	if !strings.Contains(parts[0], localEnd("2026-09-24T09:10:00Z")) {
		t.Fatalf("part 1 must contain Delegate's detail, got: %s", parts[0])
	}

	// 2. It must include the last quota error with its time
	if !strings.Contains(f.Detail, "last quota error: 09:05") {
		t.Fatalf("detail must contain the last error at 09:05, got: %s", f.Detail)
	}

	// 3. It must include the last 24 h, formatted by providerID
	if !strings.Contains(f.Detail, "opencode-go: 1 messages · 1000 in · 250 out · $1.25") {
		t.Fatalf("detail has no 24 h stats for opencode-go: %s", f.Detail)
	}
	if !strings.Contains(f.Detail, "opencode: 1 messages · 500 in · 100 out · $0.00") {
		t.Fatalf("detail has no 24 h stats for opencode: %s", f.Detail)
	}
	// The 26 h old message must NOT be counted (the cost must not be $51.25)
	if strings.Contains(f.Detail, "51.25") || strings.Contains(f.Detail, "9999") {
		t.Fatalf("messages older than 24 h must not be included in the stats: %s", f.Detail)
	}
}
