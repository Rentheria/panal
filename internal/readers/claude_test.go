package readers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

func TestClaude_RealSample(t *testing.T) {
	path := filepath.Join("testdata", "claude", "statusline.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("could not read sample: %v", err)
	}

	now := info.ModTime().Add(30 * time.Second)
	c := &Claude{
		Path: path,
		Now: func() time.Time {
			return now
		},
	}

	f := c.Read()

	if f.Agent != "claude" {
		t.Errorf("expected agent 'claude', got %q", f.Agent)
	}
	if f.Status != state.Orchestrating {
		t.Errorf("expected Orchestrating status, got %v", f.Status)
	}
	if f.Model != "Opus 5.5 (1M context) · medium" {
		t.Errorf("expected model 'Opus 5.5 (1M context) · medium', got %q", f.Model)
	}
	if f.Task != "Pendientes del proyecto" {
		t.Errorf("expected task 'Pendientes del proyecto', got %q", f.Task)
	}
	if f.Dir != `C:\Codigo\mi-proyecto` {
		t.Errorf("expected dir 'C:\\Codigo\\mi-proyecto', got %q", f.Dir)
	}

	wantDur := time.Duration(66471173) * time.Millisecond
	wantSince := info.ModTime().Add(-wantDur)
	if !f.Since.Equal(wantSince) {
		t.Errorf("expected Since %v, got %v", wantSince, f.Since)
	}

	if !f.Quota.Exact {
		t.Errorf("expected Quota.Exact == true")
	}
	if f.Quota.UsedPct != 38.0 {
		t.Errorf("expected Quota.UsedPct == 38.0, got %f", f.Quota.UsedPct)
	}
	wantResets := time.Unix(1790275200, 0)
	if !f.Quota.ResetsAt.Equal(wantResets) {
		t.Errorf("expected Quota.ResetsAt %v, got %v", wantResets, f.Quota.ResetsAt)
	}
	wantSummary := "week 38% · resets " + time.Unix(1790560800, 0).Format("02-Jan 15:04")
	if f.Quota.Summary != wantSummary {
		t.Errorf("expected Quota.Summary %q, got %q", wantSummary, f.Quota.Summary)
	}
	if !f.Quota.SeenAt.Equal(info.ModTime()) {
		t.Errorf("expected Quota.SeenAt %v, got %v", info.ModTime(), f.Quota.SeenAt)
	}

	if f.Error != "" {
		t.Errorf("expected empty Error, got %q", f.Error)
	}

	// Detail
	for _, want := range []string{
		"cost: $47.36",
		"context: 59%",
		"cache hits: 99%",
		"duration: 18 h 27 min",
		"version: 2.1.281",
		"session_id: 0fde5825-8585-4838-8d1a-e04672097ae5",
		"seen 30 s ago",
	} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail must contain %q, got: %s", want, f.Detail)
		}
	}
}

func TestClaude_RecentAndStaleStates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	content := `{"session_name": "test", "cwd": "C:\\test"}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("error writing file: %v", err)
	}

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.Local)

	// Case 1: modified less than 3 minutes ago (1 min) -> Orchestrating
	recent := now.Add(-1 * time.Minute)
	if err := os.Chtimes(path, recent, recent); err != nil {
		t.Fatalf("chtimes error: %v", err)
	}

	c := &Claude{
		Path: path,
		Now:  func() time.Time { return now },
	}
	f1 := c.Read()
	if f1.Status != state.Orchestrating {
		t.Fatalf("recent file (< 3 min): expected Orchestrating, got %v", f1.Status)
	}

	// Case 2: modified more than 3 minutes ago (5 min) -> Idle
	old := now.Add(-5 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes error: %v", err)
	}

	c = &Claude{
		Path: path,
		Now:  func() time.Time { return now },
	}
	f2 := c.Read()
	if f2.Status != state.Idle {
		t.Fatalf("old file (> 3 min): expected Idle, got %v", f2.Status)
	}
}

func TestClaude_NoFile(t *testing.T) {
	c := &Claude{
		Path: filepath.Join(t.TempDir(), "missing.json"),
	}
	f := c.Read()
	if f.Agent != "claude" {
		t.Errorf("expected agent 'claude', got %q", f.Agent)
	}
	if f.Status != state.NoData {
		t.Fatalf("no file: expected NoData, got %v", f.Status)
	}
	if f.Error != "" {
		t.Fatalf("no file must not report an Error, got %q", f.Error)
	}
}

func TestClaude_BrokenJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	if err := os.WriteFile(path, []byte(`{ broken json...`), 0644); err != nil {
		t.Fatalf("error writing file: %v", err)
	}

	c := &Claude{Path: path}
	f := c.Read()
	if f.Status != state.NoData {
		t.Fatalf("broken JSON: expected NoData, got %v", f.Status)
	}
	if f.Error == "" {
		t.Fatalf("broken JSON: expected a non-empty Row.Error")
	}
}

func TestClaude_NoRateLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	content := `{"session_name": "no quota", "cwd": "C:\\test"}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("error writing file: %v", err)
	}

	c := &Claude{Path: path}
	f := c.Read()
	if f.Quota.Exact {
		t.Fatalf("no rate_limits: expected Exact == false")
	}
	if f.Quota.Summary != "no quota data" {
		t.Fatalf("no rate_limits: expected Summary 'no quota data', got %q", f.Quota.Summary)
	}
}

func TestClaude_NoEffortNorDuration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	content := `{
		"session_name": "simple session",
		"cwd": "C:\\simple",
		"model": {"display_name": "Claude 3.7 Sonnet"},
		"cost": {"total_cost_usd": 1.25}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("error writing file: %v", err)
	}

	c := &Claude{Path: path}
	f := c.Read()
	if f.Model != "Claude 3.7 Sonnet" {
		t.Fatalf("no effort: expected 'Claude 3.7 Sonnet', got %q", f.Model)
	}
	if !f.Since.IsZero() {
		t.Fatalf("no total_duration_ms: expected zero Since, got %v", f.Since)
	}
	if strings.Contains(f.Detail, "duration:") {
		t.Fatalf("no total_duration_ms: detail must not have 'duration:', got %s", f.Detail)
	}
	if !strings.Contains(f.Detail, "cost: $1.25") {
		t.Fatalf("detail must have cost: $1.25, got %s", f.Detail)
	}
}

func TestClaude_Cache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	content1 := `{"session_name": "v1", "cwd": "C:\\v1"}`
	if err := os.WriteFile(path, []byte(content1), 0644); err != nil {
		t.Fatalf("error writing file: %v", err)
	}

	c := &Claude{Path: path, Now: time.Now}

	// 1. First read
	f1 := c.Read()
	if f1.Task != "v1" {
		t.Fatalf("expected task 'v1', got %q", f1.Task)
	}
	if c.FileReads != 1 {
		t.Fatalf("first read: expected FileReads == 1, got %d", c.FileReads)
	}

	// 2. Second read without changes: must use the cache
	f2 := c.Read()
	if f2.Task != "v1" {
		t.Fatalf("expected task 'v1', got %q", f2.Task)
	}
	if c.FileReads != 1 {
		t.Fatalf("second read (no changes): expected FileReads == 1 (cache), got %d", c.FileReads)
	}

	// 3. File modified
	time.Sleep(10 * time.Millisecond)
	content2 := `{"session_name": "v2_modified", "cwd": "C:\\v2"}`
	if err := os.WriteFile(path, []byte(content2), 0644); err != nil {
		t.Fatalf("error overwriting file: %v", err)
	}
	newMtime := time.Now().Add(1 * time.Second)
	_ = os.Chtimes(path, newMtime, newMtime)

	f3 := c.Read()
	if f3.Task != "v2_modified" {
		t.Fatalf("expected task 'v2_modified', got %q", f3.Task)
	}
	if c.FileReads != 2 {
		t.Fatalf("third read after modifying: expected FileReads == 2, got %d", c.FileReads)
	}
}

func TestClaude_QuotaFiveHourVsSevenDay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	// five_hour: 25%, seven_day: 75%
	content := `{
		"rate_limits": {
			"five_hour": {"used_percentage": 25.0, "resets_at": 1790275200},
			"seven_day": {"used_percentage": 75.0, "resets_at": 1790560800}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("error writing file: %v", err)
	}

	c := &Claude{Path: path}
	f := c.Read()

	// Catches mutation (a): UsedPct must be five_hour's (25.0), NOT seven_day's (75.0)
	if f.Quota.UsedPct != 25.0 {
		t.Fatalf("UsedPct must be five_hour's (25.0), but got %f (maybe it took seven_day)", f.Quota.UsedPct)
	}
	if !strings.Contains(f.Quota.Summary, "week 75%") {
		t.Fatalf("Summary must contain 'week 75%%', got %q", f.Quota.Summary)
	}
}

func TestClaude_SinceCalculation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	// 60000 ms = 1 minute
	content := `{
		"cost": {"total_duration_ms": 60000}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("error writing file: %v", err)
	}

	mtime := time.Date(2026, 9, 24, 10, 0, 0, 0, time.Local)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes error: %v", err)
	}

	c := &Claude{
		Path: path,
		Now:  func() time.Time { return mtime },
	}
	f := c.Read()

	// Catches mutation (c): Since must be mtime minus the duration (10:00 - 1m = 09:59), NOT plus (10:01)
	want := mtime.Add(-1 * time.Minute)
	wrongAdded := mtime.Add(1 * time.Minute)

	if !f.Since.Equal(want) {
		if f.Since.Equal(wrongAdded) {
			t.Fatalf("Since added the duration (%v) instead of subtracting it (%v)", f.Since, want)
		}
		t.Fatalf("expected Since %v, got %v", want, f.Since)
	}
}

func TestClaude_EnvVarAndDefaultPath(t *testing.T) {
	tempPath := filepath.Join(t.TempDir(), "my-statusline.json")
	t.Setenv("PANAL_CLAUDE", tempPath)
	c := NewClaude()
	if c.Path != tempPath {
		t.Fatalf("expected path from PANAL_CLAUDE %q, got %q", tempPath, c.Path)
	}

	// Without the variable: ~/.panal/claude/statusline.json.
	home := t.TempDir()
	t.Setenv("PANAL_CLAUDE", "")
	t.Setenv("PANAL_DATA", "")
	t.Setenv("USERPROFILE", home)
	want := filepath.Join(home, ".panal", "claude", "statusline.json")
	if c := NewClaude(); c.Path != want {
		t.Fatalf("expected default path %q, got %q", want, c.Path)
	}

	// Only the file the status line script used to write exists: read that one.
	old := filepath.Join(home, ".ct-delegar", "claude", "statusline.json")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := NewClaude(); c.Path != old {
		t.Fatalf("expected the old path %q as a fallback, got %q", old, c.Path)
	}

	// Once the new one exists, it wins.
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := NewClaude(); c.Path != want {
		t.Fatalf("expected the new path %q, got %q", want, c.Path)
	}
}

// A read that lands while Claude Code is rewriting the status line (the file
// is half-written) keeps the last good read instead of flashing "no data".
func TestClaudeHalfWrittenFileKeepsLastGood(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	good := `{"session_id":"s","model":{"display_name":"Opus"},"workspace":{"current_dir":"/w"}}`
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &Claude{Path: path}
	if r := c.Read(); r.Status != state.Orchestrating {
		t.Fatalf("first read: %v (%s)", r.Status, r.Error)
	}
	if err := os.WriteFile(path, []byte(`{"session_id":"s","mod`), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := c.Read(); r.Status != state.Orchestrating || r.Error != "" {
		t.Fatalf("half-written: %v (%s), want the last good read", r.Status, r.Error)
	}
	// Once it is whole again it is read again (the error was not cached).
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := c.Read(); r.Status != state.Orchestrating {
		t.Fatalf("whole again: %v", r.Status)
	}
}
