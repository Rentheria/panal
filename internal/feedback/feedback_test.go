package feedback

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/runs"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", FileName)
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	if s, err := Load(path); err != nil || len(s) != 0 {
		t.Fatalf("missing file: %v %v", s, err)
	}
	if err := Set(path, Key("20261002-101500", "codex"), Good, "  clean diff ", now); err != nil {
		t.Fatal(err)
	}
	if err := Set(path, Key("20261002-111500", "agy"), Bad, "", now); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if e := s.Get("20261002-101500", "codex"); e.Rating != Good || e.Note != "clean diff" || !e.At.Equal(now) {
		t.Errorf("good entry: %+v", e)
	}
	if e := s.Get("20261002-111500", "agy"); e.Rating != Bad {
		t.Errorf("bad entry: %+v", e)
	}
	// Clearing removes it; the cache notices the change.
	if c := Cached(path); len(c) != 2 {
		t.Fatalf("cached: %v", c)
	}
	if err := Set(path, Key("20261002-111500", "agy"), "", "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if s, _ := Load(path); len(s) != 1 || s.Get("20261002-111500", "agy").Rating != "" {
		t.Errorf("after clearing: %v", s)
	}
	if err := Set(path, "x", "meh", "", now); err == nil {
		t.Error("an unknown rating must be refused")
	}
}

func TestFind(t *testing.T) {
	at := func(m int) string { return time.Date(2026, 10, 2, 10, m, 0, 0, time.UTC).Format(time.RFC3339) }
	files := []runs.File{
		{Run: runs.Run{ID: "B", Agent: "agy", Status: runs.Running, Start: at(9)}},
		{Run: runs.Run{ID: "A", Agent: "agy", Status: runs.Done, Start: at(2)}},
		{Run: runs.Run{ID: "A", Agent: "codex", Status: runs.OutOfQuota, Start: at(1)}},
	}
	if r, ok := Find(files, "A"); !ok || r.Agent != "agy" {
		t.Errorf("bare ID: %+v", r)
	}
	if r, ok := Find(files, "A-codex.json"); !ok || r.Agent != "codex" {
		t.Errorf("exact key: %+v", r)
	}
	if r, ok := Find(files, "last"); !ok || r.ID != "A" || r.Agent != "agy" {
		t.Errorf("last: %+v", r)
	}
	if _, ok := Find(files, "nope"); ok {
		t.Error("unknown ID found")
	}
}

func TestCLI(t *testing.T) {
	dir := t.TempDir()
	if _, err := runs.WriteFile(dir, runs.Run{ID: "20261002-101500", Agent: "codex", Task: "Fix the pager", Status: runs.Done,
		Start: "2026-10-02T10:15:00Z"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), FileName)
	var out, errb bytes.Buffer
	now := time.Now()
	if code := run([]string{"20261002-101500", "bad", "wrong", "file"}, &out, &errb, []string{dir}, path, now); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "rated 20261002-101500-codex bad") {
		t.Errorf("output: %q", out.String())
	}
	if e := Cached(path).Get("20261002-101500", "codex"); e.Rating != Bad || e.Note != "wrong file" {
		t.Errorf("stored: %+v", e)
	}
	if code := run([]string{"20261002-101500", "meh"}, &out, &errb, []string{dir}, path, now); code != 2 {
		t.Errorf("bad rating: code %d", code)
	}
	if code := run([]string{"nope", "good"}, &out, &errb, []string{dir}, path, now); code != 1 {
		t.Errorf("unknown run: code %d", code)
	}
}
