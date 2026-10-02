package changes

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAllowed(t *testing.T) {
	cases := []struct {
		task string
		want []string
	}{
		// Tasks written in Spanish.
		{"Trabajas en esta carpeta (repo). No hagas commit ni push. Solo puedes modificar app/models.py y app/routes.py.", []string{"app/models.py", "app/routes.py"}},
		{"Solo puedes modificar app/blueprints/vr200.py (y, si una prueba lo pide, tests/x.py).", []string{"app/blueprints/vr200.py"}},
		{"Solo puedes modificar la carpeta docs/ y README.md. Luego corre las pruebas.", []string{"docs/", "README.md"}},
		{"Crea el archivo b.txt con el texto adios.", nil},
		// Tasks written in English.
		{"You work in this dir (repo). Do not commit or push. You may only modify app/models.py and app/routes.py.", []string{"app/models.py", "app/routes.py"}},
		{"You can only modify app/blueprints/vr200.py (and, if a test needs it, tests/x.py).", []string{"app/blueprints/vr200.py"}},
		{"You may modify only the docs/ dir and README.md. Then run the tests.", []string{"docs/", "README.md"}},
		{"Only create tests/test_no_id.py.", []string{"tests/test_no_id.py"}},
		{"Create the file b.txt with the text bye.", nil},
	}
	for _, c := range cases {
		if got, _ := Allowed(c.task); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Allowed(%q) = %q, want %q", c.task, got, c.want)
		}
	}
	if _, truncated := Allowed(strings.Repeat("x", 200)); !truncated {
		t.Error("a 200-character task comes truncated")
	}
}

func TestForbidsCommit(t *testing.T) {
	for task, want := range map[string]bool{
		"No hagas commit.":             true,
		"no hagas git commit ni push.": true,
		"Do not commit.":               true,
		"Don't commit or push.":        true,
		"dont git commit":              true,
		"Never commit anything.":       true,
		"Commit when you are done.":    false,
		"Haz commit al terminar.":      false,
	} {
		if forbidsCommit(task) != want {
			t.Errorf("forbidsCommit(%q) = %v", task, !want)
		}
	}
}

func TestIsAllowed(t *testing.T) {
	l := []string{"app/models.py", "docs/"}
	for path, want := range map[string]bool{
		"app/models.py": true, "my-api/app/models.py": true, "docs/a.md": true,
		"app/routes.py": false, "other/docs.md": false,
	} {
		if isAllowed(path, l) != want {
			t.Errorf("isAllowed(%q) = %v", path, !want)
		}
	}
	if !isAllowed("any/thing", nil) {
		t.Error("with no list everything is allowed")
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	runEnv(t, dir, nil, args...)
}

func runEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t"), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, path, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(path))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckInRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	d := t.TempDir()
	run(t, d, "init", "-q")
	write(t, d, "app/models.py", "a\n")
	write(t, d, "app/routes.py", "a\n")
	write(t, d, "old.txt", "a\n")
	run(t, d, "add", ".")
	// The base commit, from long ago: it does not fall within the run.
	runEnv(t, d, []string{"GIT_COMMITTER_DATE=2020-01-01T00:00:00Z", "GIT_AUTHOR_DATE=2020-01-01T00:00:00Z"}, "commit", "-q", "-m", "base")

	start := time.Now()
	write(t, d, "app/models.py", "a\nb\nc\n") // allowed
	write(t, d, "app/routes.py", "z\n")       // not allowed
	write(t, d, "new.py", "x\n")              // new, not allowed
	write(t, d, "old.txt", "changed before\n")
	before := start.Add(-time.Hour)
	os.Chtimes(filepath.Join(d, "old.txt"), before, before) // outside the run

	c := Run{Key: "x", Dir: d, Start: start, End: time.Now().Add(time.Second),
		Task: "No hagas commit. Solo puedes modificar app/models.py."}
	r := Check(c, time.Now())
	if r.Err != "" {
		t.Fatal(r.Err)
	}
	got := map[string]File{}
	for _, a := range r.Files {
		got[a.Path] = a
	}
	if _, ok := got["old.txt"]; ok {
		t.Error("old.txt was modified before the run and must not count")
	}
	if a := got["app/models.py"]; a.Forbidden || a.Added != 2 {
		t.Errorf("models.py: %+v (want allowed, +2)", a)
	}
	if !got["app/routes.py"].Forbidden || !got["new.py"].Forbidden || !got["new.py"].New {
		t.Errorf("routes.py and new.py must come out forbidden: %+v", r.Files)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "2 files outside") {
		t.Errorf("warnings: %q", r.Warnings)
	}

	// A commit during the run, with the task forbidding it.
	run(t, d, "add", ".")
	run(t, d, "commit", "-q", "-m", "the agent committed")
	c.End = time.Now().Add(time.Second)
	r = Check(c, time.Now())
	if len(r.Commits) != 1 || !r.HasViolations() || !strings.Contains(strings.Join(r.Warnings, "|"), "commit") {
		t.Errorf("forbidden commit not detected: commits=%d warnings=%q", len(r.Commits), r.Warnings)
	}

	// Read-only: any change is a violation.
	write(t, d, "app/models.py", "once more\n")
	r = Check(Run{Key: "y", Dir: d, Start: time.Now().Add(-time.Minute), ReadOnly: true}, time.Now())
	if !r.HasViolations() || !strings.Contains(r.Warnings[0], "read-only") {
		t.Errorf("read-only: %q", r.Warnings)
	}
}

func TestCheckWithoutRepo(t *testing.T) {
	if r := Check(Run{Dir: t.TempDir()}, time.Now()); r.Err == "" {
		t.Error("a dir without git must give an error")
	}
}

func TestAllowedCreate(t *testing.T) {
	got, _ := Allowed("Solo puedes CREAR el archivo tests/test_sin_id.py y nada más.")
	if !reflect.DeepEqual(got, []string{"tests/test_sin_id.py"}) {
		t.Fatalf("create: %q", got)
	}
}

func TestAllowedBullets(t *testing.T) {
	task := `Trabajas en este repositorio.
Solo puedes modificar:
  - internal/history/history.go
  - internal/changes/changes.go (solo lectura)
	* internal/ui/views.go
  • docs/readme.txt. Luego corre las pruebas.
No hagas commit ni push.
- ignored.txt because the bullet list is over.`

	want := []string{
		"internal/history/history.go",
		"internal/changes/changes.go",
		"internal/ui/views.go",
		"docs/readme.txt",
	}

	got, _ := Allowed(task)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Allowed with bullets = %q, want %q", got, want)
	}

	// Same list with the task written in English.
	taskEN := strings.NewReplacer(
		"Trabajas en este repositorio.", "You work in this repository.",
		"Solo puedes modificar:", "You may only modify:",
		"(solo lectura)", "(read-only)",
		"Luego corre las pruebas.", "Then run the tests.",
		"No hagas commit ni push.", "Do not commit or push.",
	).Replace(task)
	if got, _ := Allowed(taskEN); !reflect.DeepEqual(got, want) {
		t.Fatalf("Allowed with bullets (English) = %q, want %q", got, want)
	}

	// One-line sentences are cut at the end of the line
	oneLine := "Solo puedes modificar app/models.py y app/routes.py\nEste otro renglón tiene extra.go que no debe entrar."
	gotOneLine, _ := Allowed(oneLine)
	wantOneLine := []string{"app/models.py", "app/routes.py"}
	if !reflect.DeepEqual(gotOneLine, wantOneLine) {
		t.Fatalf("Allowed one line with end of line = %q, want %q", gotOneLine, wantOneLine)
	}
}

func TestCheck_TruncatedWithComplete(t *testing.T) {
	d := t.TempDir()
	run(t, d, "init", "-q")
	start := time.Now()

	// With Complete=true, Truncated must be false even if the task is long
	cComplete := Run{
		Key:      "c1",
		Dir:      d,
		Start:    start,
		End:      start.Add(time.Second),
		Task:     strings.Repeat("x", 250),
		Complete: true,
	}
	rComplete := Check(cComplete, time.Now())
	if rComplete.Truncated {
		t.Error("with Complete=true, Result.Truncated must be false")
	}

	// With Complete=false and a task >= 199 runes, Truncated must be true
	cTruncated := Run{
		Key:      "c2",
		Dir:      d,
		Start:    start,
		End:      start.Add(time.Second),
		Task:     strings.Repeat("x", 250),
		Complete: false,
	}
	rTruncated := Check(cTruncated, time.Now())
	if !rTruncated.Truncated {
		t.Error("with Complete=false and a long task, Result.Truncated must be true")
	}
}

func TestCheck_ForbidsCommitFullText(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	d := t.TempDir()
	run(t, d, "init", "-q")
	write(t, d, "file.txt", "base\n")
	run(t, d, "add", ".")
	runEnv(t, d, []string{"GIT_COMMITTER_DATE=2020-01-01T00:00:00Z", "GIT_AUTHOR_DATE=2020-01-01T00:00:00Z"}, "commit", "-q", "-m", "base")

	start := time.Now()
	write(t, d, "file.txt", "change\n")
	run(t, d, "add", ".")
	run(t, d, "commit", "-q", "-m", "commit during the run")

	for _, task := range []string{
		"Trabajas en el repositorio.\nSolo puedes modificar file.txt.\nNo hagas commit.",
		"You work in the repository.\nYou may only modify file.txt.\nDo not commit.",
	} {
		c := Run{
			Key:      "c3",
			Dir:      d,
			Start:    start,
			End:      time.Now().Add(time.Second),
			Task:     task,
			Complete: true,
		}
		r := Check(c, time.Now())
		if !r.HasViolations() || !strings.Contains(strings.Join(r.Warnings, "|"), "commit") {
			t.Fatalf("expected a forbidden commit to be detected in multi-line text %q: warnings=%q", task, r.Warnings)
		}
	}
}

// Worktrees share the repo: a commit on another branch, made by a parallel
// run, is not part of this run.
func TestCommitFromOtherWorktreeDoesNotCount(t *testing.T) {
	d := t.TempDir()
	repo, other := filepath.Join(d, "repo"), filepath.Join(d, "other")
	os.Mkdir(repo, 0o755)
	run(t, repo, "init", "-q", "-b", "master")
	old := time.Now().Add(-time.Hour).Format(time.RFC3339)
	runEnv(t, repo, []string{"GIT_AUTHOR_DATE=" + old, "GIT_COMMITTER_DATE=" + old}, "commit", "-q", "--allow-empty", "-m", "base")
	run(t, repo, "worktree", "add", "-q", "-b", "parallel", other)

	start := time.Now().Add(-time.Minute)
	run(t, other, "commit", "-q", "--allow-empty", "-m", "from the other run")
	r := Check(Run{Dir: repo, Start: start, End: time.Now(), Task: "No hagas commit."}, time.Now())
	if len(r.Commits) != 0 || r.Next != nil || r.HasViolations() {
		t.Fatalf("the commit from another worktree was attributed: %+v", r)
	}
}

// A commit made seconds before the start (the orchestrator commits and
// launches the next run) is not part of the run.
func TestCommitBeforeStartDoesNotCount(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "master")
	start := time.Now().Truncate(time.Second)
	before := start.Add(-time.Second).Format(time.RFC3339)
	runEnv(t, repo, []string{"GIT_AUTHOR_DATE=" + before, "GIT_COMMITTER_DATE=" + before}, "commit", "-q", "--allow-empty", "-m", "from the orchestrator")
	r := Check(Run{Dir: repo, Start: start, End: start.Add(time.Minute), Task: "No hagas commit."}, start.Add(time.Minute))
	if len(r.Commits) != 0 || r.HasViolations() {
		t.Fatalf("a commit from before the start was counted: %+v", r)
	}
}
