package changes

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDiffEmpty(t *testing.T) {
	got, err := Diff(t.TempDir(), Result{})
	if err != nil {
		t.Fatalf("unexpected error with an empty result: %v", err)
	}
	if got != "" {
		t.Fatalf("expected an empty string, got %q", got)
	}
}

func TestDiffRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	d := t.TempDir()
	run(t, d, "init", "-q")
	write(t, d, "base.txt", "line 1\nline 2\n")
	run(t, d, "add", ".")
	run(t, d, "commit", "-q", "-m", "base commit")

	// Modified file
	write(t, d, "base.txt", "line 1 changed\nline 2\n")
	// New file
	write(t, d, "new.txt", "first new line\nsecond new line\n")
	// New binary file
	write(t, d, "binary.bin", "before\x00after")

	// Commit of the run
	write(t, d, "in_commit.txt", "commit content\n")
	run(t, d, "add", "in_commit.txt")
	run(t, d, "commit", "-q", "-m", "test commit")
	outHash, err := git(d, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	hash1 := strings.TrimSpace(outHash)

	// Next commit
	run(t, d, "commit", "-q", "--allow-empty", "-m", "next test commit")
	outNext, err := git(d, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	hashNext := strings.TrimSpace(outNext)

	r := Result{
		Files: []File{
			{Path: "base.txt", New: false},
			{Path: "new.txt", New: true},
			{Path: "binary.bin", New: true},
		},
		Commits: []Commit{
			{Hash: hash1, Subject: "test commit"},
		},
		Next: &Commit{
			Hash:    hashNext,
			Subject: "next test commit",
		},
	}

	diff, err := Diff(d, r)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}

	// Modified file checks
	if !strings.Contains(diff, "diff --git a/base.txt b/base.txt") {
		t.Error("missing diff header for base.txt")
	}
	if !strings.Contains(diff, "-line 1") || !strings.Contains(diff, "+line 1 changed") {
		t.Error("missing diff content for base.txt")
	}

	// New file checks
	if !strings.Contains(diff, "diff --git a/new.txt b/new.txt") {
		t.Error("missing diff header for new.txt")
	}
	if !strings.Contains(diff, "new file") {
		t.Error("missing 'new file' mark for new.txt")
	}
	if !strings.Contains(diff, "+first new line") || !strings.Contains(diff, "+second new line") {
		t.Error("missing '+' content for new.txt")
	}

	// Binary file checks
	if !strings.Contains(diff, "diff --git a/binary.bin b/binary.bin") {
		t.Error("missing diff header for binary.bin")
	}
	if !strings.Contains(diff, "(binary)") {
		t.Error("missing (binary) marker for binary.bin")
	}

	// Run commit checks
	if !strings.Contains(diff, "run commit") {
		t.Error("missing 'run commit' separator")
	}
	if !strings.Contains(diff, "test commit") {
		t.Error("missing subject of the run commit")
	}

	// Next commit checks
	if !strings.Contains(diff, "next commit (may contain the work)") {
		t.Error("missing 'next commit (may contain the work)' separator")
	}
	if !strings.Contains(diff, "next test commit") {
		t.Error("missing subject of the next commit")
	}
}

func TestDiffNoRealChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	d := t.TempDir()
	run(t, d, "init", "-q")
	write(t, d, "base.txt", "hello\n")
	run(t, d, "add", ".")
	run(t, d, "commit", "-q", "-m", "base")

	// The file is in Files but has no real differences against HEAD
	r := Result{
		Files: []File{
			{Path: "base.txt", New: false},
		},
	}
	diff, err := Diff(d, r)
	if err != nil {
		t.Fatal(err)
	}
	if diff != "" {
		t.Errorf("expected an empty diff when there are no changes against HEAD, got %q", diff)
	}
}
