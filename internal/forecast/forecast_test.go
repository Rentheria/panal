package forecast

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConstantRate(t *testing.T) {
	m := New()
	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	key := Key("claude", "5h")

	// 50% at t=0, 60% at t=10m, 70% at t=20m -> 1% per minute
	m.Record(key, t0, 50)
	m.Record(key, t0.Add(10*time.Minute), 60)
	m.Record(key, t0.Add(20*time.Minute), 70)

	now := t0.Add(20 * time.Minute)
	runsOut, ok := m.Project(key, now, 70)
	if !ok {
		t.Fatal("expected a valid projection with a constant rate")
	}

	// It should reach 100% at t=50m (10:50:00)
	want := t0.Add(50 * time.Minute)
	if !runsOut.Equal(want) {
		t.Fatalf("wrong projected time: got %v, want %v", runsOut, want)
	}
}

func TestFewSamples(t *testing.T) {
	m := New()
	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	key := "agy/5h"

	// 0 samples
	if _, ok := m.Project(key, t0, 50); ok {
		t.Fatal("must not project with 0 samples")
	}

	// 1 sample
	m.Record(key, t0, 50)
	if _, ok := m.Project(key, t0, 50); ok {
		t.Fatal("must not project with 1 sample")
	}

	// 2 samples
	m.Record(key, t0.Add(10*time.Minute), 60)
	if _, ok := m.Project(key, t0.Add(10*time.Minute), 60); ok {
		t.Fatal("must not project with 2 samples")
	}

	// 3 samples spanning less than 10 min (e.g. 8 min)
	m2 := New()
	m2.Record(key, t0, 50)
	m2.Record(key, t0.Add(4*time.Minute), 55)
	m2.Record(key, t0.Add(8*time.Minute), 60)
	if _, ok := m2.Project(key, t0.Add(8*time.Minute), 60); ok {
		t.Fatal("3 samples spanning 8 min (< 10 min) must not be enough")
	}

	// Samples spanning exactly 10 min are enough
	m2.Record(key, t0.Add(10*time.Minute), 65)
	if _, ok := m2.Project(key, t0.Add(10*time.Minute), 65); !ok {
		t.Fatal("samples spanning 10 min must be enough")
	}
}

func TestResetClearsSamples(t *testing.T) {
	m := New()
	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	key := "codex/5h"

	m.Record(key, t0, 80)
	m.Record(key, t0.Add(10*time.Minute), 85)

	// Drops from 85% to 70% (15 points > 5 points): window reset
	m.Record(key, t0.Add(20*time.Minute), 70)

	// The earlier ones must be cleared, leaving only 1 sample
	if _, ok := m.Project(key, t0.Add(20*time.Minute), 70); ok {
		t.Fatal("after a reset only 1 sample is left, must not project")
	}

	// With new samples from the new cycle it projects again
	m.Record(key, t0.Add(30*time.Minute), 75)
	m.Record(key, t0.Add(40*time.Minute), 80)
	if _, ok := m.Project(key, t0.Add(40*time.Minute), 80); !ok {
		t.Fatal("should project with the samples of the new cycle")
	}
}

func TestSlopeZeroOrNegative(t *testing.T) {
	m := New()
	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	keyZero := "opencode/5h"

	// Zero slope (10 min apart so they are not skipped for being < 1 min)
	m.Record(keyZero, t0, 50)
	m.Record(keyZero, t0.Add(10*time.Minute), 50)
	m.Record(keyZero, t0.Add(20*time.Minute), 50)
	if _, ok := m.Project(keyZero, t0.Add(20*time.Minute), 50); ok {
		t.Fatal("must not project with a zero slope")
	}

	// Negative slope (drops of <= 5 points so they do not count as a reset)
	keyNeg := "opencode/sem"
	m.Record(keyNeg, t0, 50)
	m.Record(keyNeg, t0.Add(10*time.Minute), 48)
	m.Record(keyNeg, t0.Add(20*time.Minute), 46)
	if _, ok := m.Project(keyNeg, t0.Add(20*time.Minute), 46); ok {
		t.Fatal("must not project with a negative slope")
	}
}

func TestFilterLessThanOneMinute(t *testing.T) {
	m := New()
	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	key := "claude/5h"

	m.Record(key, t0, 50)
	// Repeated at 2 s and 30 s: not stored
	m.Record(key, t0.Add(2*time.Second), 50)
	m.Record(key, t0.Add(30*time.Second), 50)

	if len(m.samples[key]) != 1 {
		t.Fatalf("repeated samples within < 1 min were stored: len=%d", len(m.samples[key]))
	}

	// Same sample after 1 minute: stored
	m.Record(key, t0.Add(61*time.Second), 50)
	if len(m.samples[key]) != 2 {
		t.Fatalf("sample after 1 min was not stored: len=%d", len(m.samples[key]))
	}

	// Different sample within 1 minute: stored
	m.Record(key, t0.Add(70*time.Second), 51)
	if len(m.samples[key]) != 3 {
		t.Fatalf("changed sample was not stored: len=%d", len(m.samples[key]))
	}
}

func TestOldSamples(t *testing.T) {
	m := New()
	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	key := "claude/5h"

	// Samples more than 2 hours apart
	m.Record(key, t0, 10)
	m.Record(key, t0.Add(2*time.Hour+1*time.Minute), 20)

	// The one at t0 must have been pruned for being older than 2 h
	if len(m.samples[key]) != 1 {
		t.Fatalf("must prune samples older than 2 h: len=%d", len(m.samples[key]))
	}

	// Samples outside Project's 60 min
	m.Record(key, t0.Add(2*time.Hour+15*time.Minute), 30)
	m.Record(key, t0.Add(2*time.Hour+25*time.Minute), 40)
	// Evaluate at t0 + 3h30m (the samples are more than 60m old)
	now := t0.Add(3*time.Hour + 30*time.Minute)
	if _, ok := m.Project(key, now, 40); ok {
		t.Fatal("samples older than 60 min must not be used in the projection")
	}
}

func TestAlreadyExhausted(t *testing.T) {
	m := New()
	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	key := "claude/5h"

	m.Record(key, t0, 90)
	m.Record(key, t0.Add(10*time.Minute), 95)
	m.Record(key, t0.Add(20*time.Minute), 99)

	if _, ok := m.Project(key, t0.Add(20*time.Minute), 100); ok {
		t.Fatal("with current >= 100 it must return ok=false")
	}
	if _, ok := m.Project(key, t0.Add(20*time.Minute), 105); ok {
		t.Fatal("with current > 100 it must return ok=false")
	}
}

func TestPath(t *testing.T) {
	t.Setenv("PANAL_DATA", `C:\test\data`)
	if got := Path(); got != filepath.Join(`C:\test\data`, "samples.json") {
		t.Fatalf("unexpected path with PANAL_DATA: got %q", got)
	}

	// No variable at all.
	os.Unsetenv("PANAL_DATA")
	got := Path()
	if !strings.HasSuffix(got, filepath.Join(".panal", "samples.json")) {
		t.Fatalf("path without PANAL_DATA must end in .panal/samples.json: got %q", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "samples.json")

	m := New()
	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	key1 := Key("claude", "5h")
	key2 := Key("codex", "sem")

	m.Record(key1, t0, 50)
	m.Record(key1, t0.Add(10*time.Minute), 60)
	m.Record(key2, t0.Add(5*time.Minute), 30)

	if !m.Dirty() {
		t.Fatal("must be dirty after recording samples")
	}

	if err := m.Save(path); err != nil {
		t.Fatalf("error saving: %v", err)
	}

	if m.Dirty() {
		t.Fatal("must not be dirty after a successful save")
	}

	// Check the file exists and is valid JSON with version: 1.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read the saved file: %v", err)
	}
	if !strings.Contains(string(b), `"version":1`) {
		t.Fatalf("the saved JSON must contain version 1: %s", string(b))
	}

	// Load into a new Sampler.
	m2 := New()
	now := t0.Add(15 * time.Minute)
	if err := m2.Load(path, now); err != nil {
		t.Fatalf("error loading: %v", err)
	}

	if m2.Dirty() {
		t.Fatal("must not be dirty after loading")
	}

	// Check the right samples were loaded.
	m2.mu.Lock()
	ms1 := m2.samples[key1]
	ms2 := m2.samples[key2]
	m2.mu.Unlock()

	if len(ms1) != 2 {
		t.Fatalf("expected 2 samples for claude/5h, got %d", len(ms1))
	}
	if ms1[0].pct != 50 || ms1[1].pct != 60 {
		t.Fatalf("claude/5h values do not match: %+v", ms1)
	}
	if len(ms2) != 1 || ms2[0].pct != 30 {
		t.Fatalf("codex/sem values do not match: %+v", ms2)
	}
}

func TestLoadDiscardsOlderThan2hAndFuture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "samples.json")

	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	key := Key("agy", "5h")

	// JSON file with samples at different times:
	// - 3 h ago (must be dropped for being older than 2 h)
	// - exactly 2 h ago (within the limit)
	// - 1 h ago (valid)
	// - now (valid)
	// - 10 min in the future (must be dropped)
	content := fmt.Sprintf(`{
		"version": 1,
		"samples": {
			"%s": [
				{"t": %q, "pct": 10},
				{"t": %q, "pct": 20},
				{"t": %q, "pct": 30},
				{"t": %q, "pct": 40},
				{"t": %q, "pct": 50}
			]
		}
	}`,
		key,
		now.Add(-3*time.Hour).Format(time.RFC3339),
		now.Add(-2*time.Hour).Format(time.RFC3339),
		now.Add(-1*time.Hour).Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Add(10*time.Minute).Format(time.RFC3339),
	)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("error writing the test file: %v", err)
	}

	m := New()
	if err := m.Load(path, now); err != nil {
		t.Fatalf("error loading: %v", err)
	}

	m.mu.Lock()
	ms := m.samples[key]
	m.mu.Unlock()

	if len(ms) != 3 {
		t.Fatalf("expected 3 valid samples (2h, 1h, now), got %d: %+v", len(ms), ms)
	}
	if ms[0].pct != 20 || ms[1].pct != 30 || ms[2].pct != 40 {
		t.Fatalf("the kept samples are not the expected ones: %+v", ms)
	}
}

func TestLoadMissingFile(t *testing.T) {
	m := New()
	path := filepath.Join(t.TempDir(), "does_not_exist.json")

	if err := m.Load(path, time.Now()); err != nil {
		t.Fatalf("a missing file must not return an error: %v", err)
	}

	m.mu.Lock()
	n := len(m.samples)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("with a missing file it must start empty, it has %d keys", n)
	}
	if m.Dirty() {
		t.Fatal("must not be dirty after loading a missing file")
	}
}

func TestLoadBrokenFileOrOtherVersion(t *testing.T) {
	dir := t.TempDir()

	// File with broken content / invalid JSON.
	brokenPath := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(brokenPath, []byte("{this is not json"), 0o644); err != nil {
		t.Fatalf("error preparing the broken file: %v", err)
	}

	m := New()
	if err := m.Load(brokenPath, time.Now()); err != nil {
		t.Fatalf("a broken file must not return an error: %v", err)
	}
	m.mu.Lock()
	nBroken := len(m.samples)
	m.mu.Unlock()
	if nBroken != 0 {
		t.Fatalf("with a broken file it must start empty: %d", nBroken)
	}

	// File with another version.
	verPath := filepath.Join(dir, "other_version.json")
	if err := os.WriteFile(verPath, []byte(`{"version":99,"samples":{"claude/5h":[{"t":"2026-09-26T10:00:00Z","pct":50}]}}`), 0o644); err != nil {
		t.Fatalf("error preparing the other-version file: %v", err)
	}

	m2 := New()
	if err := m2.Load(verPath, time.Now()); err != nil {
		t.Fatalf("a file of another version must not return an error: %v", err)
	}
	m2.mu.Lock()
	nVer := len(m2.samples)
	m2.mu.Unlock()
	if nVer != 0 {
		t.Fatalf("with another version it must start empty: %d", nVer)
	}
}

func TestDirtyClearedOnSave(t *testing.T) {
	m := New()
	if m.Dirty() {
		t.Fatal("a new sampler must not be dirty")
	}

	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	key := Key("codex", "5h")

	m.Record(key, t0, 40)
	if !m.Dirty() {
		t.Fatal("must be dirty after Record")
	}

	path := filepath.Join(t.TempDir(), "m.json")
	if err := m.Save(path); err != nil {
		t.Fatalf("error saving: %v", err)
	}
	if m.Dirty() {
		t.Fatal("Save must clear the dirty state")
	}

	// An identical sample within a minute adds nothing and must not dirty it.
	m.Record(key, t0.Add(10*time.Second), 40)
	if m.Dirty() {
		t.Fatal("a skipped sample must not set dirty to true")
	}

	// A new sample dirties it again.
	m.Record(key, t0.Add(2*time.Minute), 45)
	if !m.Dirty() {
		t.Fatal("a new sample must set dirty to true")
	}
}
