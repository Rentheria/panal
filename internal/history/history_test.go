package history

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/AlbertoVasquezR/panal/internal/runs"
)

// runFile is a run file as panal writes it.
type runFile = runs.Run

func writeStatus(t *testing.T, dir, filename string, st runFile) {
	t.Helper()
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0644); err != nil {
		t.Fatal(err)
	}
}

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
	`)
	if err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func insertMessage(t *testing.T, dbPath string, id string, role string, timeCreated int64, inTokens, outTokens int64, cost float64) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s", filepath.ToSlash(dbPath)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	data := fmt.Sprintf(`{"role":%q,"tokens":{"input":%d,"output":%d},"cost":%f}`, role, inTokens, outTokens, cost)
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		id, "ses_1", timeCreated, timeCreated, data)
	if err != nil {
		t.Fatal(err)
	}
}

func TestHistory_OrderAndLimit(t *testing.T) {
	dir := t.TempDir()

	writeStatus(t, dir, "c1.json", runFile{
		ID:     "stamp-1",
		Agent:  "agy",
		Start:  "2026-09-24T09:00:00Z",
		Status: "done",
		Task:   "first",
	})
	writeStatus(t, dir, "c2.json", runFile{
		ID:     "stamp-2",
		Agent:  "agy",
		Start:  "2026-09-24T12:00:00Z",
		Status: "done",
		Task:   "fourth",
	})
	writeStatus(t, dir, "c3.json", runFile{
		ID:     "stamp-3",
		Agent:  "agy",
		Start:  "2026-09-24T10:00:00Z",
		Status: "done",
		Task:   "second",
	})
	writeStatus(t, dir, "c4.json", runFile{
		ID:     "stamp-4",
		Agent:  "agy",
		Start:  "2026-09-24T11:00:00Z",
		Status: "done",
		Task:   "third",
	})

	h := New()

	// Limit 2: must return the 2 most recent (12:00 and 11:00)
	limited := h.Read(dir, 2)
	if len(limited) != 2 {
		t.Fatalf("expected 2 runs with limit 2, got %d", len(limited))
	}
	if limited[0].Stamp != "stamp-2" || limited[1].Stamp != "stamp-4" {
		t.Fatalf("wrong order with limit: [0]=%s, [1]=%s", limited[0].Stamp, limited[1].Stamp)
	}

	// All of them, newest to oldest
	all := h.Read(dir, 10)
	if len(all) != 4 {
		t.Fatalf("expected 4 runs, got %d", len(all))
	}
	want := []string{"stamp-2", "stamp-4", "stamp-3", "stamp-1"}
	for i, exp := range want {
		if all[i].Stamp != exp {
			t.Fatalf("position %d: expected %s, got %s", i, exp, all[i].Stamp)
		}
	}
}

func TestHistory_BrokenJSON(t *testing.T) {
	dir := t.TempDir()

	writeStatus(t, dir, "valid.json", runFile{
		ID:     "stamp-valid",
		Agent:  "codex",
		Start:  "2026-09-24T10:00:00Z",
		Status: "done",
		Task:   "ok",
	})

	// JSON with broken syntax
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{bad json..."), 0644); err != nil {
		t.Fatal(err)
	}

	// JSON with an invalid start
	if err := os.WriteFile(filepath.Join(dir, "invalid_start.json"), []byte(`{"id":"bad","start":"not-a-date"}`), 0644); err != nil {
		t.Fatal(err)
	}

	h := New()
	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 valid run ignoring broken files, got %d", len(runs))
	}
	if runs[0].Stamp != "stamp-valid" {
		t.Fatalf("expected stamp-valid, got %s", runs[0].Stamp)
	}
}

func TestHistory_CodexEnrichment(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "20260924-100000-codex")

	jsonlContent := strings.Join([]string{
		`{"timestamp":"2026-09-24T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":100}},"rate_limits":{"credits":{"balance":"1000"}}}}`,
		`{"timestamp":"2026-09-24T10:01:00Z","type":"event_msg","payload":{"type":"custom"}}`,
		`{"timestamp":"2026-09-24T10:05:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":550}},"rate_limits":{"credits":{"balance":"925"}}}}`,
	}, "\n") + "\n"

	if err := os.WriteFile(regPath+".jsonl", []byte(jsonlContent), 0644); err != nil {
		t.Fatal(err)
	}

	writeStatus(t, dir, "codex.json", runFile{
		ID:     "stamp-codex",
		Agent:  "codex",
		Model:  "gpt-6-luna",
		Start:  "2026-09-24T10:00:00Z",
		End:    "2026-09-24T10:05:00Z",
		Status: "done",
		Log:    regPath,
	})

	h := New()
	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	c := runs[0]
	if c.Tokens != 550 {
		t.Fatalf("expected 550 tokens from the last line, got %d", c.Tokens)
	}
	if c.Cost != "75 cr" {
		t.Fatalf("expected cost '75 cr' (1000 - 925), got %q", c.Cost)
	}
}

func TestHistory_OpencodeEnrichment(t *testing.T) {
	dir := t.TempDir()
	dbPath := createSyntheticDB(t, dir)

	tStart := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	tEnd := time.Date(2026, 9, 24, 10, 10, 0, 0, time.UTC)

	// Message 1: BEFORE the window (must not count)
	insertMessage(t, dbPath, "m1", "assistant", tStart.Add(-1*time.Minute).UnixMilli(), 100, 100, 1.00)

	// Message 2: INSIDE the window (must count)
	insertMessage(t, dbPath, "m2", "assistant", tStart.Add(2*time.Minute).UnixMilli(), 150, 50, 0.04)

	// Message 3: INSIDE the window (must count)
	insertMessage(t, dbPath, "m3", "assistant", tStart.Add(5*time.Minute).UnixMilli(), 25, 75, 0.03)

	// Message 4: AFTER the window (must not count)
	insertMessage(t, dbPath, "m4", "assistant", tEnd.Add(1*time.Minute).UnixMilli(), 200, 200, 2.00)

	// Message 5: INSIDE the window but role='user' (must not count)
	insertMessage(t, dbPath, "m5", "user", tStart.Add(3*time.Minute).UnixMilli(), 500, 0, 0.00)

	writeStatus(t, dir, "opencode.json", runFile{
		ID:     "stamp-opencode",
		Agent:  "opencode",
		Model:  "deepseek-v4",
		Start:  tStart.Format(time.RFC3339),
		End:    tEnd.Format(time.RFC3339),
		Status: "done",
		Dir:    "C:\\Codigo\\wt",
	})

	h := New()
	h.OpencodeDBPath = dbPath

	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	c := runs[0]
	// Expected tokens: (150+50) + (25+75) = 200 + 100 = 300
	if c.Tokens != 300 {
		t.Fatalf("expected 300 tokens (only those inside the window), got %d", c.Tokens)
	}
	// Expected cost: 0.04 + 0.03 = 0.07 -> "$0.07"
	if c.Cost != "$0.07" {
		t.Fatalf("expected cost '$0.07' (only those inside the window), got %q", c.Cost)
	}
}

func TestHistory_RunningNotEnriched(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "20260924-100000-codex")

	jsonlContent := `{"timestamp":"2026-09-24T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":800}},"rate_limits":{"credits":{"balance":"900"}}}}` + "\n"
	if err := os.WriteFile(regPath+".jsonl", []byte(jsonlContent), 0644); err != nil {
		t.Fatal(err)
	}

	writeStatus(t, dir, "codex-running.json", runFile{
		ID:     "stamp-running",
		Agent:  "codex",
		Model:  "gpt-6-luna",
		Start:  "2026-09-24T10:00:00Z",
		Status: "running",
		Log:    regPath,
	})

	h := New()
	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	c := runs[0]
	if c.Tokens != 0 {
		t.Fatalf("a running run must not have computed tokens, got %d", c.Tokens)
	}
	if c.Cost != "" {
		t.Fatalf("a running run must not have an enriched cost, got %q", c.Cost)
	}
}

func TestHistory_CacheDoesNotRecompute(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "20260924-100000-codex")

	jsonlContent := `{"timestamp":"2026-09-24T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":200}},"rate_limits":{"credits":{"balance":"950"}}}}` + "\n"
	if err := os.WriteFile(regPath+".jsonl", []byte(jsonlContent), 0644); err != nil {
		t.Fatal(err)
	}

	writeStatus(t, dir, "codex.json", runFile{
		ID:     "stamp-cache",
		Agent:  "codex",
		Start:  "2026-09-24T10:00:00Z",
		End:    "2026-09-24T10:05:00Z",
		Status: "done",
		Log:    regPath,
	})

	h := New()

	// First read: must compute and increment the counter
	c1 := h.Read(dir, 10)
	if len(c1) != 1 {
		t.Fatalf("expected 1 run, got %d", len(c1))
	}
	if h.Computations != 1 {
		t.Fatalf("expected Computations == 1 after the first read, got %d", h.Computations)
	}

	// Second read: must come from the cache, without recomputing
	c2 := h.Read(dir, 10)
	if len(c2) != 1 {
		t.Fatalf("expected 1 run, got %d", len(c2))
	}
	if h.Computations != 1 {
		t.Fatalf("expected Computations == 1 after the second read (using the cache), got %d", h.Computations)
	}
	if c2[0].Tokens != 200 {
		t.Fatalf("expected 200 tokens from the cache, got %d", c2[0].Tokens)
	}
}

func TestHistory_Summary(t *testing.T) {
	day := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	t.Run("with credits", func(t *testing.T) {
		runs := []Run{
			{
				Start:  time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC),
				Status: "done",
				Cost:   "30 cr",
			},
			{
				Start:  time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC),
				Status: "out_of_quota",
				Cost:   "20 cr",
			},
			{
				Start:  time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC),
				Status: "failed",
				Cost:   "",
			},
			{
				// Yesterday's run (must not count today)
				Start:  time.Date(2026, 9, 23, 20, 0, 0, 0, time.UTC),
				Status: "done",
				Cost:   "100 cr",
			},
		}

		res := Summary(runs, day)
		want := "today: 3 runs · 1 done · 1 out of quota · 1 failed · 50 cr spent"
		if res != want {
			t.Fatalf("expected %q, got %q", want, res)
		}
	})

	t.Run("without credits", func(t *testing.T) {
		runs := []Run{
			{
				Start:  time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC),
				Status: "done",
				Cost:   "$0.05",
			},
			{
				Start:  time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC),
				Status: "done",
				Cost:   "",
			},
		}

		res := Summary(runs, day)
		want := "today: 2 runs · 2 done"
		if res != want {
			t.Fatalf("expected %q, got %q", want, res)
		}
		if strings.Contains(res, "cr spent") {
			t.Fatalf("must not contain 'cr spent': %q", res)
		}
		if strings.Contains(res, "out of quota") {
			t.Fatalf("must not contain 'out of quota': %q", res)
		}
		if strings.Contains(res, "failed") {
			t.Fatalf("must not contain 'failed': %q", res)
		}
	})

	t.Run("empty", func(t *testing.T) {
		res := Summary(nil, day)
		if res != "today: 0 runs" {
			t.Fatalf("expected 'today: 0 runs', got %q", res)
		}
	})
}

func TestHistory_JSONV1MissingField(t *testing.T) {
	dir := t.TempDir()

	writeStatus(t, dir, "v1.json", runFile{
		Version: 1,
		ID:      "stamp-v1",
		Agent:   "agy",
		Start:   "2026-09-24T10:00:00Z",
		Status:  "done",
		Task:    "first line of v1 task",
	})

	h := New()
	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	c := runs[0]
	if c.Task != "first line of v1 task" {
		t.Fatalf("wrong task: %q", c.Task)
	}
	if c.FullTask != "" {
		t.Fatalf("FullTask must be empty in v1, got %q", c.FullTask)
	}
}

func TestHistory_JSONV2WithFileAndCache(t *testing.T) {
	dir := t.TempDir()
	txtPath := filepath.Join(dir, "task.txt")
	content := "first line\nsecond line\nthird line\n"
	if err := os.WriteFile(txtPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	writeStatus(t, dir, "v2.json", runFile{
		Version:  2,
		ID:       "stamp-v2",
		Agent:    "codex",
		Start:    "2026-09-24T10:00:00Z",
		Status:   "done",
		Task:     "first line",
		TaskFile: txtPath,
	})

	h := New()

	// First read: reads from disk and fills the cache.
	runs1 := h.Read(dir, 10)
	if len(runs1) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs1))
	}
	want := "first line\nsecond line\nthird line"
	if runs1[0].FullTask != want {
		t.Fatalf("expected %q without trailing newline, got %q", want, runs1[0].FullTask)
	}
	if h.FileReads != 1 {
		t.Fatalf("expected 1 file read, got %d", h.FileReads)
	}

	// Delete the file on disk to check that the next read uses the cache.
	if err := os.Remove(txtPath); err != nil {
		t.Fatal(err)
	}

	runs2 := h.Read(dir, 10)
	if len(runs2) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs2))
	}
	if runs2[0].FullTask != want {
		t.Fatalf("expected %q from the cache after deleting the file, got %q", want, runs2[0].FullTask)
	}
	if h.FileReads != 1 {
		t.Fatalf("the cache must not read the disk again: FileReads = %d", h.FileReads)
	}
}

func TestHistory_FileMissing(t *testing.T) {
	dir := t.TempDir()
	writeStatus(t, dir, "no-file.json", runFile{
		Version:  2,
		ID:       "stamp-ghost",
		Agent:    "opencode",
		Start:    "2026-09-24T10:00:00Z",
		Status:   "done",
		Task:     "something",
		TaskFile: filepath.Join(dir, "does-not-exist.txt"),
	})

	h := New()
	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].FullTask != "" {
		t.Fatalf("a missing file must leave FullTask empty, got %q", runs[0].FullTask)
	}
}

func TestHistory_FileMax64KiB(t *testing.T) {
	dir := t.TempDir()
	txtPath := filepath.Join(dir, "big.txt")
	// 70 KiB
	size := 70 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = 'a'
	}
	if err := os.WriteFile(txtPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	writeStatus(t, dir, "big.json", runFile{
		Version:  2,
		ID:       "stamp-big",
		Agent:    "claude",
		Start:    "2026-09-24T10:00:00Z",
		Status:   "done",
		Task:     "big",
		TaskFile: txtPath,
	})

	h := New()
	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if len(runs[0].FullTask) != 64*1024 {
		t.Fatalf("expected size 65536 bytes (64 KiB), got %d", len(runs[0].FullTask))
	}
}

func TestHistory_CodexWithSession(t *testing.T) {
	dir := t.TempDir()
	td, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}

	regPath := filepath.Join(td, "codex_exec_with_session")
	writeStatus(t, dir, "codex_session.json", runFile{
		ID:     "stamp-codex-session",
		Agent:  "codex",
		Start:  "2026-09-25T20:26:50Z",
		End:    "2026-09-25T20:26:55Z",
		Status: "done",
		Task:   "You may only modify/create a.go. Do not commit.",
		Log:    regPath,
	})

	h := New()
	h.CodexSessions = filepath.Join(td, "sessions")

	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	c := runs[0]
	if c.Tokens != 177628 {
		t.Fatalf("expected 177628 tokens from the session, got %d", c.Tokens)
	}
	if c.Cost != "50 cr" {
		t.Fatalf("expected cost '50 cr' (1000 - 950), got %q", c.Cost)
	}
	wantTask := "You may only modify/create a.go. Do not commit.\nSecond line codex with session."
	if c.FullTask != wantTask {
		t.Fatalf("expected full task %q, got %q", wantTask, c.FullTask)
	}
}

func TestHistory_CodexWithoutSession(t *testing.T) {
	dir := t.TempDir()
	td, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}

	regPath := filepath.Join(td, "codex_exec_without_session")
	writeStatus(t, dir, "codex_without_session.json", runFile{
		ID:     "stamp-codex-without-session",
		Agent:  "codex",
		Start:  "2026-09-25T20:26:50Z",
		End:    "2026-09-25T20:26:55Z",
		Status: "done",
		Task:   "You may only modify something",
		Log:    regPath,
	})

	h := New()
	h.CodexSessions = filepath.Join(td, "sessions")

	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	c := runs[0]
	// Sum of turn.completed: (1200+300) + (800+200) = 2500
	if c.Tokens != 2500 {
		t.Fatalf("expected 2500 tokens from turn.completed, got %d", c.Tokens)
	}
	if c.Cost != "" {
		t.Fatalf("without a session the cost must be empty, got %q", c.Cost)
	}
	if c.FullTask != "" {
		t.Fatalf("without a session the full task must be empty, got %q", c.FullTask)
	}
}

func TestHistory_FullTaskAgy(t *testing.T) {
	dir := t.TempDir()
	td, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}

	regPath := filepath.Join(td, "agy_multiline")
	writeStatus(t, dir, "agy.json", runFile{
		ID:     "stamp-agy-task",
		Agent:  "agy",
		Start:  "2026-09-26T08:19:00Z",
		End:    "2026-09-26T08:19:10Z",
		Status: "done",
		Task:   "You may only modify/create a.go. Do not commit.",
		Log:    regPath,
	})

	h := New()
	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	c := runs[0]
	want := "You may only modify/create a.go. Do not commit.\nThis is the second line of the agy task.\nAnd this is the third line."
	if c.FullTask != want {
		t.Fatalf("expected the task cut before the next log line:\n%q\ngot:\n%q", want, c.FullTask)
	}
}

func TestHistory_TaskCodexMultipleMessages(t *testing.T) {
	dir := t.TempDir()
	td, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}

	regPath := filepath.Join(td, "codex_exec_multiple")
	writeStatus(t, dir, "codex_multiple.json", runFile{
		ID:     "stamp-codex-multiple",
		Agent:  "codex",
		Start:  "2026-09-25T20:20:00Z",
		End:    "2026-09-25T20:20:05Z",
		Status: "done",
		Task:   "You may only modify/create codex.go. Do not commit.",
		Log:    regPath,
	})

	h := New()
	h.CodexSessions = filepath.Join(td, "sessions")

	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	c := runs[0]
	want := "You may only modify/create codex.go. Do not commit.\nSecond line with detailed instructions.\nThird line."
	if c.FullTask != want {
		t.Fatalf("expected to pick the message that matches the task:\n%q\ngot:\n%q", want, c.FullTask)
	}
}

func TestHistory_TaskMismatch(t *testing.T) {
	dir := t.TempDir()
	td, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}

	regPath := filepath.Join(td, "agy_mismatch")
	writeStatus(t, dir, "agy_mismatch.json", runFile{
		ID:     "stamp-agy-mismatch",
		Agent:  "agy",
		Start:  "2026-09-26T08:19:00Z",
		End:    "2026-09-26T08:19:10Z",
		Status: "done",
		Task:   "You may only modify/create original.go",
		Log:    regPath,
	})

	h := New()
	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].FullTask != "" {
		t.Fatalf("on mismatch it must stay empty, got %q", runs[0].FullTask)
	}
}

func TestHistory_TaskFilePriority(t *testing.T) {
	dir := t.TempDir()
	td, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}

	txtFile := filepath.Join(td, "task_priority.txt")
	regPath := filepath.Join(td, "agy_multiline")
	writeStatus(t, dir, "priority.json", runFile{
		ID:       "stamp-priority",
		Agent:    "agy",
		Start:    "2026-09-26T08:19:00Z",
		End:      "2026-09-26T08:19:10Z",
		Status:   "done",
		Task:     "You may only modify/create a.go. Do not commit.",
		TaskFile: txtFile,
		Log:      regPath,
	})

	h := New()
	runs := h.Read(dir, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	c := runs[0]
	want := "Priority task from encargo_archivo\nSecond priority line"
	if c.FullTask != want {
		t.Fatalf("encargo_archivo must take priority over the log: expected %q, got %q", want, c.FullTask)
	}
}

func TestHistory_TaskCacheRunningAndDone(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	regPath := filepath.Join(logDir, "agy_dynamic")

	// Run in running status whose log does not have the message yet
	logContentInitial := "I0926 08:19:00.100000       1 startup.go:10] Starting up\n"
	if err := os.WriteFile(regPath+".log", []byte(logContentInitial), 0644); err != nil {
		t.Fatal(err)
	}

	writeStatus(t, dir, "agy_running.json", runFile{
		ID:     "stamp-dynamic",
		Agent:  "agy",
		Start:  "2026-09-26T08:19:00Z",
		Status: "running",
		Task:   "You may only modify a.go",
		Log:    regPath,
	})

	h := New()

	// 1. While running and before the message is written: not found and not cached
	c1 := h.Read(dir, 10)
	if len(c1) != 1 {
		t.Fatalf("expected 1 run, got %d", len(c1))
	}
	if c1[0].FullTask != "" {
		t.Fatalf("the message does not exist yet, it should be empty: %q", c1[0].FullTask)
	}

	// 2. The log grows and the message is written
	logContentUpdated := logContentInitial + "I0926 08:19:08.231597       1 convertinputs.go:353] Received cascade user message with 0 mentions and 0 media: You may only modify a.go\nLine two\nI0926 08:19:09.500000       1 convertinputs.go:400] End\n"
	if err := os.WriteFile(regPath+".log", []byte(logContentUpdated), 0644); err != nil {
		t.Fatal(err)
	}

	// Since the empty result was not cached, the next read finds it
	c2 := h.Read(dir, 10)
	if len(c2) != 1 {
		t.Fatalf("expected 1 run, got %d", len(c2))
	}
	want := "You may only modify a.go\nLine two"
	if c2[0].FullTask != want {
		t.Fatalf("expected to find the new message in the log: %q, got %q", want, c2[0].FullTask)
	}

	// 3. Now that it was found while running, it must be cached.
	// Delete the log file to check that it comes from the cache.
	if err := os.Remove(regPath + ".log"); err != nil {
		t.Fatal(err)
	}

	c3 := h.Read(dir, 10)
	if len(c3) != 1 {
		t.Fatalf("expected 1 run, got %d", len(c3))
	}
	if c3[0].FullTask != want {
		t.Fatalf("expected the task from the cache after deleting the log: %q, got %q", want, c3[0].FullTask)
	}
}
