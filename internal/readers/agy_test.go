package readers

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

func TestAgy_LastResponseInFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "20260924-101558-agy-test.txt.log")

	// Two responses in the same file: the first at 50% used, the second at ~14%
	resp1 := `I0924 10:10:00.000000     100 http_helpers.go:333] Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:
{
  "groups": [
    {
      "displayName": "Gemini Models",
      "buckets": [
        {
          "bucketId": "gemini-weekly",
          "window": "weekly",
          "resetTime": "2026-09-25T19:00:00Z",
          "remainingFraction": 0.50
        },
        {
          "bucketId": "gemini-5h",
          "window": "5h",
          "resetTime": "2026-09-24T18:00:00Z",
          "remainingFraction": 0.50
        }
      ]
    }
  ]
}
`
	resp2 := `I0924 10:16:06.329307     619 http_helpers.go:333] Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:
{
  "groups": [
    {
      "displayName": "Gemini Models",
      "buckets": [
        {
          "bucketId": "gemini-weekly",
          "window": "weekly",
          "resetTime": "2026-09-25T19:34:57Z",
          "remainingFraction": 0.93066454
        },
        {
          "bucketId": "gemini-5h",
          "window": "5h",
          "resetTime": "2026-09-24T19:12:21Z",
          "remainingFraction": 0.8576482
        }
      ]
    }
  ]
}
`
	if err := os.WriteFile(logPath, []byte(resp1+resp2), 0644); err != nil {
		t.Fatal(err)
	}

	agy := &Agy{LogDirs: []string{dir}}
	f := agy.Read()

	if !f.Quota.Exact {
		t.Fatalf("expected Quota.Exact == true, got false")
	}
	// It must take the last response (0.8576482 remaining -> 14.235% used), not the first (50%)
	wantUsed := (1.0 - 0.8576482) * 100.0
	if math.Abs(f.Quota.UsedPct-wantUsed) > 0.001 {
		t.Fatalf("expected UsedPct from the LAST response (~%.2f%%), got %.2f%%", wantUsed, f.Quota.UsedPct)
	}
	if f.Quota.UsedPct == 50.0 {
		t.Fatalf("took the first response instead of the last")
	}
}

func TestAgy_TakesNewestFile(t *testing.T) {
	dir := t.TempDir()
	log1 := filepath.Join(dir, "20260924-000001-agy-old.txt.log")
	log2 := filepath.Join(dir, "20260924-000002-agy-new.txt.log")

	c1 := `I0924 10:00:00.000000     100 http_helpers.go:333] Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:
{
  "groups": [
    {
      "displayName": "Gemini Models",
      "buckets": [
        { "bucketId": "gemini-5h", "window": "5h", "remainingFraction": 0.40 }
      ]
    }
  ]
}
`
	c2 := `I0924 10:05:00.000000     100 http_helpers.go:333] Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:
{
  "groups": [
    {
      "displayName": "Gemini Models",
      "buckets": [
        { "bucketId": "gemini-5h", "window": "5h", "remainingFraction": 0.80 }
      ]
    }
  ]
}
`
	if err := os.WriteFile(log1, []byte(c1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log2, []byte(c2), 0644); err != nil {
		t.Fatal(err)
	}

	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(log1, t0, t0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(log2, t1, t1); err != nil {
		t.Fatal(err)
	}

	agy := &Agy{LogDirs: []string{dir}}
	f := agy.Read()

	// 0.80 remaining -> 20% used, from the newest file
	if math.Abs(f.Quota.UsedPct-20.0) > 0.001 {
		t.Fatalf("expected 20.0%% from the newest file, got %f", f.Quota.UsedPct)
	}
}

func TestAgy_IgnoresLogsWithoutResponse(t *testing.T) {
	dir := t.TempDir()
	validLog := filepath.Join(dir, "20260924-000001-agy-valid.txt.log")
	emptyLog := filepath.Join(dir, "20260924-000002-agy-empty.txt.log")

	// Read the real testdata
	data, err := os.ReadFile("testdata/agy/quota.log")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(validLog, data, 0644); err != nil {
		t.Fatal(err)
	}
	// Newer file without any quota response
	if err := os.WriteFile(emptyLog, []byte("I0924 11:00:00.000000 1 main.go:1] just ordinary lines\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(validLog, t0, t0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(emptyLog, t1, t1); err != nil {
		t.Fatal(err)
	}

	agy := &Agy{LogDirs: []string{dir}}
	f := agy.Read()

	if !f.Quota.Exact {
		t.Fatalf("expected Quota.Exact == true when skipping the log without a response, got false")
	}
	wantUsed := (1.0 - 0.8576482) * 100.0
	if math.Abs(f.Quota.UsedPct-wantUsed) > 0.001 {
		t.Fatalf("expected UsedPct from validLog (~%.2f%%), got %.2f%%", wantUsed, f.Quota.UsedPct)
	}

	// If no log has a response
	emptyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyDir, "20260924-000003-agy-empty.txt.log"), []byte("filler line\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fEmpty := (&Agy{LogDirs: []string{emptyDir}}).Read()
	if fEmpty.Quota.Summary != "no data (run agy with panal delegate)" {
		t.Fatalf("expected 'no data (run agy with panal delegate)', got %q", fEmpty.Quota.Summary)
	}
}

func TestAgy_ModelGroup(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile("testdata/agy/quota.log")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "20260924-101606-agy-quota.txt.log"), data, 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("gemini model picks Gemini Models", func(t *testing.T) {
		statesDir := t.TempDir()
		stJSON := `{"version":1,"agent":"agy","model":"gemini-2.5-flash","start":"2026-09-24T10:00:00Z","status":"running","pid":1234}`
		if err := os.WriteFile(filepath.Join(statesDir, "20260924-100000-agy.json"), []byte(stJSON), 0644); err != nil {
			t.Fatal(err)
		}

		agy := &Agy{
			LogDirs:  []string{dir},
			Delegate: &Delegate{Name: "agy", Dirs: []string{statesDir}, PIDAlive: func(int) bool { return true }},
		}
		f := agy.Read()

		wantUsed := (1.0 - 0.8576482) * 100.0
		if math.Abs(f.Quota.UsedPct-wantUsed) > 0.001 {
			t.Fatalf("gemini model: expected the Gemini Models quota (~%.2f%%), got %.2f%%", wantUsed, f.Quota.UsedPct)
		}
		if !strings.HasPrefix(f.Quota.Summary, "week 7%") {
			t.Fatalf("gemini model: expected 'week 7%%...', got %q", f.Quota.Summary)
		}
	})

	t.Run("claude model picks Claude and GPT models", func(t *testing.T) {
		statesDir := t.TempDir()
		stJSON := `{"version":1,"agent":"agy","model":"claude-3-5-sonnet","start":"2026-09-24T10:00:00Z","status":"running","pid":1234}`
		if err := os.WriteFile(filepath.Join(statesDir, "20260924-100000-agy.json"), []byte(stJSON), 0644); err != nil {
			t.Fatal(err)
		}

		agy := &Agy{
			LogDirs:  []string{dir},
			Delegate: &Delegate{Name: "agy", Dirs: []string{statesDir}, PIDAlive: func(int) bool { return true }},
		}
		f := agy.Read()

		// In quota.log, Claude and GPT models has remainingFraction: 1 for 5h (0% used)
		if f.Quota.UsedPct != 0.0 {
			t.Fatalf("claude model: expected the Claude and GPT models quota (0%%), got %f", f.Quota.UsedPct)
		}
		if !strings.HasPrefix(f.Quota.Summary, "week 0%") {
			t.Fatalf("claude model: expected 'week 0%%...', got %q", f.Quota.Summary)
		}
	})

	t.Run("unknown model picks Gemini", func(t *testing.T) {
		agy := &Agy{LogDirs: []string{dir}}
		f := agy.Read()

		wantUsed := (1.0 - 0.8576482) * 100.0
		if math.Abs(f.Quota.UsedPct-wantUsed) > 0.001 {
			t.Fatalf("no model: expected Gemini Models by default (~%.2f%%), got %.2f%%", wantUsed, f.Quota.UsedPct)
		}
	})
}

func TestAgy_RemainingFractionMissing(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "20260924-100000-agy-missing.txt.log")

	// Bucket without the remainingFraction key
	content := `I0924 10:00:00.000000     100 http_helpers.go:333] Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:
{
  "groups": [
    {
      "displayName": "Gemini Models",
      "buckets": [
        {
          "bucketId": "gemini-5h",
          "window": "5h",
          "resetTime": "2026-09-24T20:00:00Z"
        },
        {
          "bucketId": "gemini-weekly",
          "window": "weekly",
          "resetTime": "2026-09-25T20:00:00Z"
        }
      ]
    }
  ]
}
`
	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	agy := &Agy{LogDirs: []string{dir}}
	f := agy.Read()

	if !f.Quota.Exact {
		t.Fatalf("expected Exact == true")
	}
	if f.Quota.UsedPct != 0.0 {
		t.Fatalf("a missing remainingFraction counts as 1.0 (0%% used), got %f", f.Quota.UsedPct)
	}
	if !strings.HasPrefix(f.Quota.Summary, "week 0%") {
		t.Fatalf("expected Summary week 0%%, got %q", f.Quota.Summary)
	}
}

func TestAgy_BrokenJSONSkipped(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "20260924-100000-agy-broken.txt.log")

	valid := `I0924 10:00:00.000000     100 http_helpers.go:333] Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:
{
  "groups": [
    {
      "displayName": "Gemini Models",
      "buckets": [
        { "bucketId": "gemini-5h", "window": "5h", "remainingFraction": 0.75 }
      ]
    }
  ]
}
`
	broken := `I0924 10:05:00.000000     100 http_helpers.go:333] Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:
{
  "groups": [ { this json never closes and is completely broken
}
`
	if err := os.WriteFile(logPath, []byte(valid+broken), 0644); err != nil {
		t.Fatal(err)
	}

	agy := &Agy{LogDirs: []string{dir}}
	f := agy.Read()

	if !f.Quota.Exact {
		t.Fatalf("must skip the broken json and keep the valid response")
	}
	// 0.75 remaining -> 25% used
	if math.Abs(f.Quota.UsedPct-25.0) > 0.001 {
		t.Fatalf("expected 25%% used from the earlier valid response, got %f", f.Quota.UsedPct)
	}
}

func TestAgy_StaleDataWarns(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile("testdata/agy/quota.log")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "20260924-101606-agy-quota.txt.log")
	if err := os.WriteFile(logPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	warning := "stale data: updates on the next agy run"

	t.Run("more than 6 hours warns", func(t *testing.T) {
		agy := &Agy{
			LogDirs: []string{dir},
			Now: func() time.Time {
				// seenAt in quota.log is 10:16:06.329307 on September 24 of the file's year
				// Simulate 7 hours later
				return time.Date(2026, time.September, 24, 17, 30, 0, 0, time.Local)
			},
		}
		f := agy.Read()
		if !strings.Contains(f.Detail, warning) {
			t.Fatalf("expected a stale data warning after > 6 hours, detail: %s", f.Detail)
		}
	})

	t.Run("less than 6 hours does not warn", func(t *testing.T) {
		agy := &Agy{
			LogDirs: []string{dir},
			Now: func() time.Time {
				// Simulate 2 hours later
				return time.Date(2026, time.September, 24, 12, 16, 0, 0, time.Local)
			},
		}
		f := agy.Read()
		if strings.Contains(f.Detail, warning) {
			t.Fatalf("must NOT warn about stale data after < 6 hours, detail: %s", f.Detail)
		}
	})
}

func TestAgy_OutOfQuotaToIdle(t *testing.T) {
	statesDir := t.TempDir()
	logsDir := t.TempDir()

	// Delegate finished with out_of_quota at 08:00:00
	runEnd := "2026-09-24T08:00:00-06:00"
	stJSON := fmt.Sprintf(`{
		"version": 1,
		"agent": "agy",
		"model": "gemini-pro",
		"pid": 1234,
		"start": "2026-09-24T07:55:00-06:00",
		"status": "out_of_quota",
		"end": %q,
		"rc": 1
	}`, runEnd)
	if err := os.WriteFile(filepath.Join(statesDir, "20260924-080000-agy.json"), []byte(stJSON), 0644); err != nil {
		t.Fatal(err)
	}

	writeLog := func(glogTime string, rem5h, remWk float64) {
		content := fmt.Sprintf(`%s     100 http_helpers.go:333] Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:
{
  "groups": [
    {
      "displayName": "Gemini Models",
      "buckets": [
        { "bucketId": "gemini-5h", "window": "5h", "remainingFraction": %f },
        { "bucketId": "gemini-weekly", "window": "weekly", "remainingFraction": %f }
      ]
    }
  ]
}
`, glogTime, rem5h, remWk)
		p := filepath.Join(logsDir, "20260924-agy-test.txt.log")
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	newAgy := func() *Agy {
		return &Agy{
			LogDirs: []string{logsDir},
			Delegate: &Delegate{
				Name:     "agy",
				Dirs:     []string{statesDir},
				PIDAlive: func(int) bool { return false },
			},
		}
	}

	// Case 1: quota newer than the end (09:00:00 > 08:00:00) and remainingFraction > 0 -> Idle
	writeLog("I0924 09:00:00.000000", 0.50, 0.80)
	f1 := newAgy().Read()
	if f1.Status != state.Idle {
		t.Fatalf("newer data with quota > 0: expected Idle, got %v", f1.Status)
	}

	// Case 2: quota older than the end (07:50:00 < 08:00:00) -> still OutOfQuota
	writeLog("I0924 07:50:00.000000", 0.50, 0.80)
	f2 := newAgy().Read()
	if f2.Status != state.OutOfQuota {
		t.Fatalf("data older than the end: expected OutOfQuota, got %v", f2.Status)
	}

	// Case 3: newer data but remainingFraction is 0 -> still OutOfQuota
	writeLog("I0924 09:00:00.000000", 0.0, 0.80)
	f3 := newAgy().Read()
	if f3.Status != state.OutOfQuota {
		t.Fatalf("data with remainingFraction == 0: expected OutOfQuota, got %v", f3.Status)
	}
}

func TestAgy_Cache(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "20260924-100000-agy-cache.txt.log")

	c1 := `I0924 10:00:00.000000     100 http_helpers.go:333] Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:
{
  "groups": [
    {
      "displayName": "Gemini Models",
      "buckets": [
        { "bucketId": "gemini-5h", "window": "5h", "remainingFraction": 0.60 }
      ]
    }
  ]
}
`
	if err := os.WriteFile(logPath, []byte(c1), 0644); err != nil {
		t.Fatal(err)
	}

	agy := &Agy{LogDirs: []string{dir}}

	// First read: reads the file
	f1 := agy.Read()
	if agy.FileReads != 1 {
		t.Fatalf("first read: expected FileReads == 1, got %d", agy.FileReads)
	}
	if math.Abs(f1.Quota.UsedPct-40.0) > 0.001 {
		t.Fatalf("expected 40.0%%, got %f", f1.Quota.UsedPct)
	}

	// Second read without changes: must use the cache
	f2 := agy.Read()
	if agy.FileReads != 1 {
		t.Fatalf("second read (no changes): expected FileReads == 1 (cache), got %d", agy.FileReads)
	}
	if math.Abs(f2.Quota.UsedPct-40.0) > 0.001 {
		t.Fatalf("expected 40.0%% from the cache, got %f", f2.Quota.UsedPct)
	}

	// File modified: must read again
	c2 := `I0924 10:10:00.000000     100 http_helpers.go:333] Response from https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary:
{
  "groups": [
    {
      "displayName": "Gemini Models",
      "buckets": [
        { "bucketId": "gemini-5h", "window": "5h", "remainingFraction": 0.85 }
      ]
    }
  ]
}
`
	if err := os.WriteFile(logPath, []byte(c2), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(5 * time.Minute)
	if err := os.Chtimes(logPath, future, future); err != nil {
		t.Fatal(err)
	}

	f3 := agy.Read()
	if agy.FileReads != 2 {
		t.Fatalf("after modifying: expected FileReads == 2, got %d", agy.FileReads)
	}
	if math.Abs(f3.Quota.UsedPct-15.0) > 0.001 {
		t.Fatalf("expected 15.0%% after re-reading, got %f", f3.Quota.UsedPct)
	}
}

func TestAgy_UsedPctCalculation(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile("testdata/agy/quota.log")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "20260924-101606-agy-quota.txt.log"), data, 0644); err != nil {
		t.Fatal(err)
	}

	agy := &Agy{LogDirs: []string{dir}}
	f := agy.Read()

	// In quota.log for Gemini Models 5h: remainingFraction = 0.8576482
	// UsedPct MUST be (1 - 0.8576482)*100 = 14.23518...
	// It must NOT be 0.8576482 * 100 = 85.76482...
	want := (1.0 - 0.8576482) * 100.0
	if math.Abs(f.Quota.UsedPct-want) > 0.001 {
		t.Fatalf("wrong UsedPct: expected %f, got %f", want, f.Quota.UsedPct)
	}
	if math.Abs(f.Quota.UsedPct-(0.8576482*100.0)) < 0.001 {
		t.Fatalf("UsedPct was wrongly computed as remainingFraction*100 (%f)", f.Quota.UsedPct)
	}
}
