package readers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

func metaLine(cwd, originator string) string {
	return fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q,"originator":%q}}`+"\n", cwd, originator)
}

func rateLimitLine(ts string, usedPct float64, windowMin int, resetsAt int64, hasCredits bool, balance, plan string, totalTokens int) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d}},"rate_limits":{"limit_id":"codex","primary":{"used_percent":%f,"window_minutes":%d,"resets_at":%d},"credits":{"has_credits":%t,"balance":%q},"plan_type":%q}}}`+"\n",
		ts, totalTokens, usedPct, windowMin, resetsAt, hasCredits, balance, plan)
}

func TestCodex_Newest(t *testing.T) {
	dir := t.TempDir()

	sub1 := filepath.Join(dir, "2026", "09", "20")
	if err := os.MkdirAll(sub1, 0755); err != nil {
		t.Fatal(err)
	}
	sub2 := filepath.Join(dir, "2026", "09", "24")
	if err := os.MkdirAll(sub2, 0755); err != nil {
		t.Fatal(err)
	}

	file1 := filepath.Join(sub1, "rollout-1.jsonl")
	file2 := filepath.Join(sub2, "rollout-2.jsonl")

	c1 := metaLine("C:\\wt1", "codex-tui") + rateLimitLine("2026-09-20T10:00:00Z", 40.0, 10080, 1790000000, true, "1000", "prolite", 100)
	c2 := metaLine("C:\\wt2", "codex-tui") + rateLimitLine("2026-09-24T10:00:00Z", 75.0, 10080, 1790000000, true, "900", "prolite", 500)

	if err := os.WriteFile(file1, []byte(c1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte(c2), 0644); err != nil {
		t.Fatal(err)
	}

	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(file1, t0, t0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file2, t1, t1); err != nil {
		t.Fatal(err)
	}

	c := &Codex{SessionsDir: dir}
	f := c.Read()

	if !f.Quota.Exact {
		t.Fatalf("expected Quota.Exact == true, got false")
	}
	if f.Quota.UsedPct != 75.0 {
		t.Fatalf("expected UsedPct 75.0 from the newest file, got %f", f.Quota.UsedPct)
	}
	if f.Quota.Credits != "900" {
		t.Fatalf("expected Credits '900', got %s", f.Quota.Credits)
	}
	if !strings.Contains(f.Detail, "rollout-2.jsonl") {
		t.Fatalf("Detail does not contain the newest file's name: %s", f.Detail)
	}
}

func TestCodex_LastLineRateLimits(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "rollout.jsonl")

	c := metaLine("C:\\wt", "codex_exec") +
		rateLimitLine("2026-09-24T10:00:00Z", 25.0, 10080, 1790000000, true, "1000", "prolite", 100) +
		`{"timestamp":"2026-09-24T10:01:00Z","type":"event_msg","payload":{"type":"custom"}}` + "\n" +
		rateLimitLine("2026-09-24T10:05:00Z", 80.0, 10080, 1790000000, true, "850", "prolite", 450)

	if err := os.WriteFile(filePath, []byte(c), 0644); err != nil {
		t.Fatal(err)
	}

	reader := &Codex{SessionsDir: dir}
	f := reader.Read()

	if f.Quota.UsedPct != 80.0 {
		t.Fatalf("expected the LAST line with rate_limits (80.0%%), got %f", f.Quota.UsedPct)
	}
	if f.Quota.Credits != "850" {
		t.Fatalf("expected balance '850', got %s", f.Quota.Credits)
	}
	if !strings.Contains(f.Detail, "total tokens: 450") {
		t.Fatalf("Detail must contain the last line's total tokens: %s", f.Detail)
	}
}

func TestCodex_PrimaryNullIgnored(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "rollout.jsonl")

	c := metaLine("C:\\wt", "codex-tui") +
		rateLimitLine("2026-09-24T10:00:00Z", 55.0, 10080, 1790000000, true, "800", "prolite", 200) +
		`{"timestamp":"2026-09-24T10:02:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":null,"credits":{"has_credits":true,"balance":"800"}}}}` + "\n"

	if err := os.WriteFile(filePath, []byte(c), 0644); err != nil {
		t.Fatal(err)
	}

	reader := &Codex{SessionsDir: dir}
	f := reader.Read()

	if f.Quota.UsedPct != 55.0 {
		t.Fatalf("must ignore a null primary and keep the earlier valid line, got %f", f.Quota.UsedPct)
	}
}

func TestCodex_BrokenLineSkipped(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "rollout.jsonl")

	c := metaLine("C:\\wt", "codex-tui") +
		`{this json is completely broken` + "\n" +
		rateLimitLine("2026-09-24T10:00:00Z", 62.0, 10080, 1790000000, true, "750", "prolite", 300) +
		`another broken line that does not parse!` + "\n"

	if err := os.WriteFile(filePath, []byte(c), 0644); err != nil {
		t.Fatal(err)
	}

	reader := &Codex{SessionsDir: dir}
	f := reader.Read()

	if !f.Quota.Exact {
		t.Fatalf("must skip broken lines and process the valid one: Quota.Exact == false")
	}
	if f.Quota.UsedPct != 62.0 {
		t.Fatalf("expected 62.0%%, got %f", f.Quota.UsedPct)
	}
}

func TestCodex_NoSessions(t *testing.T) {
	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		reader := &Codex{SessionsDir: dir}
		f := reader.Read()
		if f.Quota.Exact {
			t.Fatalf("expected Exact == false")
		}
		if f.Quota.Summary != "no sessions" {
			t.Fatalf("expected Summary 'no sessions', got %q", f.Quota.Summary)
		}
		if f.Error != "" {
			t.Fatalf("no sessions must not flag an error, got %q", f.Error)
		}
	})

	t.Run("missing dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "does-not-exist")
		reader := &Codex{SessionsDir: dir}
		f := reader.Read()
		if f.Quota.Exact {
			t.Fatalf("expected Exact == false")
		}
		if f.Quota.Summary != "no sessions" {
			t.Fatalf("expected Summary 'no sessions', got %q", f.Quota.Summary)
		}
		if f.Error != "" {
			t.Fatalf("no sessions must not flag an error, got %q", f.Error)
		}
	})

	t.Run("file without rate_limits", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "rollout.jsonl")
		content := metaLine("C:\\wt", "codex-tui") +
			`{"timestamp":"2026-09-24T10:00:00Z","type":"event_msg","payload":{"type":"hello"}}` + "\n"
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		reader := &Codex{SessionsDir: dir}
		f := reader.Read()
		if f.Quota.Exact {
			t.Fatalf("expected Exact == false")
		}
		if f.Quota.Summary != "no sessions" {
			t.Fatalf("expected Summary 'no sessions', got %q", f.Quota.Summary)
		}
	})
}

func TestCodex_Cache(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "rollout.jsonl")

	c := metaLine("C:\\wt", "codex-tui") +
		rateLimitLine("2026-09-24T10:00:00Z", 50.0, 10080, 1790000000, true, "1000", "prolite", 100)
	if err := os.WriteFile(filePath, []byte(c), 0644); err != nil {
		t.Fatal(err)
	}

	reader := &Codex{SessionsDir: dir}

	// First read: reads the file
	f1 := reader.Read()
	if reader.FileReads != 1 {
		t.Fatalf("first read: expected FileReads == 1, got %d", reader.FileReads)
	}
	if f1.Quota.UsedPct != 50.0 {
		t.Fatalf("expected 50.0%%, got %f", f1.Quota.UsedPct)
	}

	// Second read without changes: must use the cache
	f2 := reader.Read()
	if reader.FileReads != 1 {
		t.Fatalf("second read (no changes): expected FileReads == 1 (cache), got %d", reader.FileReads)
	}
	if f2.Quota.UsedPct != 50.0 {
		t.Fatalf("expected 50.0%% from the cache, got %f", f2.Quota.UsedPct)
	}

	// Third read after changing mtime/size: must read again
	cNew := c + rateLimitLine("2026-09-24T10:05:00Z", 60.0, 10080, 1790000000, true, "950", "prolite", 200)
	if err := os.WriteFile(filePath, []byte(cNew), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(5 * time.Minute)
	if err := os.Chtimes(filePath, future, future); err != nil {
		t.Fatal(err)
	}

	f3 := reader.Read()
	if reader.FileReads != 2 {
		t.Fatalf("after modifying: expected FileReads == 2, got %d", reader.FileReads)
	}
	if f3.Quota.UsedPct != 60.0 {
		t.Fatalf("expected 60.0%% after re-reading, got %f", f3.Quota.UsedPct)
	}
}

func TestCodex_CreditsWarning(t *testing.T) {
	const warning = "⚠ weekly quota used up: each use costs credits"

	t.Run("used_percent >= 100 and has_credits true", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "rollout.jsonl")
		content := metaLine("C:\\wt", "codex-tui") +
			rateLimitLine("2026-09-24T10:00:00Z", 100.0, 10080, 1790000000, true, "1000", "prolite", 100)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		f := (&Codex{SessionsDir: dir}).Read()
		if !strings.Contains(f.Detail, warning) {
			t.Fatalf("expected a credits warning in the detail when used_percent >= 100 and has_credits: %s", f.Detail)
		}
	})

	t.Run("used_percent >= 100 but has_credits false", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "rollout.jsonl")
		content := metaLine("C:\\wt", "codex-tui") +
			rateLimitLine("2026-09-24T10:00:00Z", 100.0, 10080, 1790000000, false, "0", "prolite", 100)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		f := (&Codex{SessionsDir: dir}).Read()
		if strings.Contains(f.Detail, warning) {
			t.Fatalf("must NOT show the warning when has_credits is false: %s", f.Detail)
		}
	})

	t.Run("used_percent < 100 and has_credits true", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "rollout.jsonl")
		content := metaLine("C:\\wt", "codex-tui") +
			rateLimitLine("2026-09-24T10:00:00Z", 99.0, 10080, 1790000000, true, "1000", "prolite", 100)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		f := (&Codex{SessionsDir: dir}).Read()
		if strings.Contains(f.Detail, warning) {
			t.Fatalf("must NOT show the warning when used_percent < 100: %s", f.Detail)
		}
	})
}

func TestCodex_BalanceDecrease(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "rollout.jsonl")

	c1 := metaLine("C:\\wt", "codex-tui") +
		rateLimitLine("2026-09-24T10:00:00Z", 50.0, 10080, 1790000000, true, "1008.0", "prolite", 100)
	if err := os.WriteFile(filePath, []byte(c1), 0644); err != nil {
		t.Fatal(err)
	}

	reader := &Codex{SessionsDir: dir}

	// First read
	f1 := reader.Read()
	if f1.Quota.Credits != "1008" {
		t.Fatalf("expected Credits '1008', got %s", f1.Quota.Credits)
	}
	if f1.Quota.Summary != "" {
		t.Fatalf("on the first read Summary must be empty, got %q", f1.Quota.Summary)
	}

	// Second read: the balance drops to 1003.0 (-5 cr)
	c2 := metaLine("C:\\wt", "codex-tui") +
		rateLimitLine("2026-09-24T10:10:00Z", 55.0, 10080, 1790000000, true, "1003.0", "prolite", 200)
	if err := os.WriteFile(filePath, []byte(c2), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(10 * time.Minute)
	if err := os.Chtimes(filePath, future, future); err != nil {
		t.Fatal(err)
	}

	f2 := reader.Read()
	if f2.Quota.Credits != "1003" {
		t.Fatalf("expected Credits '1003', got %s", f2.Quota.Credits)
	}
	wantSummary := "-5 cr this session"
	if f2.Quota.Summary != wantSummary {
		t.Fatalf("expected Summary %q, got %q", wantSummary, f2.Quota.Summary)
	}
	if !f2.Quota.Exact {
		t.Fatalf("Quota.Exact must still be true")
	}
}

func TestCodex_LargeFile512KB(t *testing.T) {
	t.Run("rate_limits in the tail (last 512 KB)", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "rollout-large.jsonl")

		f, err := os.Create(filePath)
		if err != nil {
			t.Fatal(err)
		}

		// First line: session metadata
		if _, err := f.WriteString(metaLine("C:\\my\\project", "codex-tui")); err != nil {
			t.Fatal(err)
		}

		// More than 550 KB of filler lines without rate_limits
		paddingLine := `{"timestamp":"2026-09-24T10:00:00Z","type":"event_msg","payload":{"type":"heartbeat_ping"}}` + "\n"
		for i := 0; i < 7500; i++ {
			if _, err := f.WriteString(paddingLine); err != nil {
				t.Fatal(err)
			}
		}

		// Last line: rate_limits at the end
		finalLine := rateLimitLine("2026-09-24T10:30:00Z", 88.0, 10080, 1790447529, true, "777.4", "prolite", 12345)
		if _, err := f.WriteString(finalLine); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		info, err := os.Stat(filePath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() <= 512*1024 {
			t.Fatalf("the file must exceed 512 KB, size: %d", info.Size())
		}

		reader := &Codex{SessionsDir: dir}
		res := reader.Read()

		if !res.Quota.Exact {
			t.Fatalf("expected Exact == true")
		}
		if res.Quota.UsedPct != 88.0 {
			t.Fatalf("expected UsedPct 88.0, got %f", res.Quota.UsedPct)
		}
		if res.Quota.Credits != "777" {
			t.Fatalf("expected Credits '777', got %s", res.Quota.Credits)
		}
		if !strings.Contains(res.Detail, "plan: prolite · window: 7 d") {
			t.Fatalf("detail has no plan or window: %s", res.Detail)
		}
		if !strings.Contains(res.Detail, "cwd: C:\\my\\project · originator: codex-tui") {
			t.Fatalf("detail has no cwd and originator from the first line: %s", res.Detail)
		}
		if !strings.Contains(res.Detail, "total tokens: 12345") {
			t.Fatalf("detail has no total tokens: %s", res.Detail)
		}
	})

	t.Run("rate_limits only at the start (fallback reads the whole file)", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "rollout-large-start.jsonl")

		f, err := os.Create(filePath)
		if err != nil {
			t.Fatal(err)
		}

		// First line: metadata
		if _, err := f.WriteString(metaLine("C:\\my\\repo", "codex_exec")); err != nil {
			t.Fatal(err)
		}

		// Second line: rate_limits at the start of the file
		if _, err := f.WriteString(rateLimitLine("2026-09-24T10:00:00Z", 42.0, 10080, 1790447529, true, "999", "prolite", 500)); err != nil {
			t.Fatal(err)
		}

		// More than 550 KB of filler without rate_limits until the end
		paddingLine := `{"timestamp":"2026-09-24T10:00:00Z","type":"event_msg","payload":{"type":"noop"}}` + "\n"
		for i := 0; i < 7500; i++ {
			if _, err := f.WriteString(paddingLine); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		info, err := os.Stat(filePath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() <= 512*1024 {
			t.Fatalf("the file must exceed 512 KB, size: %d", info.Size())
		}

		reader := &Codex{SessionsDir: dir}
		res := reader.Read()

		if !res.Quota.Exact {
			t.Fatalf("expected Exact == true through the fallback")
		}
		if res.Quota.UsedPct != 42.0 {
			t.Fatalf("expected UsedPct 42.0 through the fallback, got %f", res.Quota.UsedPct)
		}
	})
}

func TestCodex_DelegateWrapping(t *testing.T) {
	dir := t.TempDir()
	sesDir := t.TempDir()

	startStr := "2026-09-24T10:00:00Z"
	jsonContent := fmt.Sprintf(`{
		"version": 1,
		"agent": "codex",
		"model": "gpt-6",
		"task": "delegated task",
		"dir": "C:\\wt",
		"pid": 9999,
		"start": %q,
		"status": "done",
		"end": "2026-09-24T10:05:00Z",
		"rc": 0
	}`, startStr)
	if err := os.WriteFile(filepath.Join(dir, "20260924-100000-codex.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	sesFile := filepath.Join(sesDir, "rollout.jsonl")
	sesContent := metaLine("C:\\wt", "codex-tui") +
		rateLimitLine("2026-09-24T10:05:00Z", 10.0, 10080, 1790447529, true, "1000", "prolite", 100)
	if err := os.WriteFile(sesFile, []byte(sesContent), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Delegate{Name: "codex", Dirs: []string{dir}, PIDAlive: func(int) bool { return true }}
	c := &Codex{
		Delegate:    d,
		SessionsDir: sesDir,
	}

	f := c.Read()
	if f.Agent != "codex" {
		t.Fatalf("expected Agent 'codex', got %s", f.Agent)
	}
	if f.Status != state.Done {
		t.Fatalf("expected Done status from Delegate, got %v", f.Status)
	}
	if f.Model != "gpt-6" {
		t.Fatalf("expected Model 'gpt-6', got %s", f.Model)
	}
	if f.Task != "delegated task" {
		t.Fatalf("expected Task 'delegated task', got %s", f.Task)
	}
	if f.Dir != "C:\\wt" {
		t.Fatalf("expected Dir 'C:\\wt', got %s", f.Dir)
	}
	if !f.Quota.Exact || f.Quota.UsedPct != 10.0 {
		t.Fatalf("wrong quota: %+v", f.Quota)
	}
	// It must contain Delegate's detail AND codex's, separated by a blank line
	if !strings.Contains(f.Detail, localEnd("2026-09-24T10:05:00Z")) {
		t.Fatalf("Detail has no end from Delegate: %s", f.Detail)
	}
	if !strings.Contains(f.Detail, "plan: prolite") {
		t.Fatalf("Detail has no plan from Codex: %s", f.Detail)
	}
	parts := strings.Split(f.Detail, "\n\n")
	if len(parts) < 2 {
		t.Fatalf("the details must be separated by a blank line (\\n\\n): %q", f.Detail)
	}
}

// The last delegated attempt bounced on quota, but the exact quota has already
// reset: the row must not keep saying "out of quota".
func TestCodex_ResetQuotaNotStillOutOfQuota(t *testing.T) {
	states := t.TempDir()
	js := `{"version":1,"agent":"codex","model":"gpt-6-luna","pid":1,"log":"","start":"2026-09-24T08:00:00-06:00","status":"out_of_quota","end":"2026-09-24T08:00:05-06:00","rc":75}`
	if err := os.WriteFile(filepath.Join(states, "20260924-080000-codex.json"), []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions := t.TempDir()
	write := func(pct float64) {
		c := metaLine(`C:\wt`, "codex-tui") + rateLimitLine("2026-09-26T19:00:00Z", pct, 10080, 1791000000, true, "900", "prolite", 10)
		if err := os.WriteFile(filepath.Join(sessions, "rollout-x.jsonl"), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	newCodex := func() *Codex {
		return &Codex{SessionsDir: sessions, Delegate: &Delegate{Name: "codex", Dirs: []string{states}, PIDAlive: func(int) bool { return false }}}
	}

	write(100)
	if f := newCodex().Read(); f.Status != state.OutOfQuota {
		t.Fatalf("quota at 100: expected out of quota, got %v", f.Status)
	}
	write(3)
	if f := newCodex().Read(); f.Status != state.Idle {
		t.Fatalf("quota reset (3%%): expected idle, got %v", f.Status)
	}
}

func TestCodex_ContextWhileWorking(t *testing.T) {
	f := state.Row{Status: state.Working}
	setContext(&f, 19.8)
	if !f.HasContext || f.ContextPct != 19.8 {
		t.Fatalf("while working it must carry the context: %+v", f)
	}
	g := state.Row{Status: state.Done}
	setContext(&g, 50)
	if g.HasContext {
		t.Fatal("once it stops working, its session's context says nothing")
	}
	h := state.Row{Status: state.Working}
	setContext(&h, -1)
	if h.HasContext {
		t.Fatal("without model_context_window it is unknown")
	}

	// The figure comes from the last token_count: input_tokens / model_context_window.
	dir := t.TempDir()
	line := strings.Replace(rateLimitLine("2026-09-24T10:05:00Z", 30, 10080, 1790000000, true, "900", "prolite", 450),
		`"info":{`, `"info":{"model_context_window":258400,"last_token_usage":{"input_tokens":51680},`, 1)
	if !strings.Contains(line, "model_context_window") {
		t.Skip("rateLimitLine changed shape")
	}
	os.WriteFile(filepath.Join(dir, "rollout.jsonl"), []byte(metaLine(`C:\wt`, "codex_exec")+line), 0644)
	r := &Codex{SessionsDir: dir}
	r.Read()
	if r.cacheCtx < 19.9 || r.cacheCtx > 20.1 {
		t.Fatalf("51680/258400 = 20 %%, got %.2f", r.cacheCtx)
	}
}
