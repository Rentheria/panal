// Package changes tells what an agent really touched in its dir, so we do not
// depend on what it reports. Run files do not store a base commit, so the
// attribution goes by time: uncommitted files whose modification time falls
// within the run, and commits made during it. Then it is compared with what
// the task allowed («You may only modify a.py and b.py», «Do not commit»,
// read-only); the task may be written in Spanish or English.
//
// Read-only: git runs with --no-optional-locks so not even the index is touched.
package changes

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// File changed during the run.
type File struct {
	Path           string // relative to the repo root, with /
	Added, Removed int    // lines; -1 if unknown (binary or new and not counted)
	New            bool   // untracked in git
	Deleted        bool
	Forbidden      bool // outside of what the task allowed
	OutsideWindow  bool // deleted: there is no date to attribute it for sure
}

// Commit made during the run (or, for Next, right after it ended).
type Commit struct {
	Hash, Subject string
	Time          time.Time
	Files         []string
	Outside       int // how many of its files are outside of what was allowed
}

// Result of checking a run.
type Result struct {
	Files     []File
	Commits   []Commit // during the run
	Next      *Commit  // first commit shortly after: the work may have gone there
	Allowed   []string // what the task allowed to modify; empty = no limit given
	Truncated bool     // the stored task is cut: there may be more allowed paths
	Warnings  []string // violations in words
	Err       string
}

// HasViolations: there is something the task did not allow.
func (r Result) HasViolations() bool { return len(r.Warnings) > 0 }

// Run is what we need to know about a run.
type Run struct {
	Key      string // for the cache (stamp+agent)
	Dir      string
	Start    time.Time
	End      time.Time // zero if still running
	Task     string
	Complete bool // Task is the full text
	ReadOnly bool
}

// Margin after the end during which changes are still attributed (writes
// that finish flushing) and window to look for the next commit.
const (
	Margin     = 15 * time.Second
	NextWindow = 30 * time.Minute
)

var git = func(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--no-optional-locks", "-C", dir}, args...)...)
	hideWindow(cmd)
	out, err := cmd.Output()
	return string(out), err
}

// Check computes the result for a run.
func Check(c Run, now time.Time) Result {
	var r Result
	r.Allowed, r.Truncated = Allowed(c.Task)
	if c.Complete {
		r.Truncated = false
	}
	if c.Dir == "" {
		r.Err = "no dir"
		return r
	}
	root, err := git(c.Dir, "rev-parse", "--show-toplevel")
	if err != nil {
		r.Err = "not a git repository"
		return r
	}
	root = strings.TrimSpace(root)
	end := c.End
	if end.IsZero() {
		end = now
	}
	since, until := c.Start.Add(-2*time.Second), end.Add(Margin)

	// Lines per file against HEAD (only tracked ones).
	lines := map[string][2]int{}
	if out, err := git(root, "diff", "--numstat", "HEAD"); err == nil {
		for _, l := range strings.Split(out, "\n") {
			p := strings.SplitN(l, "\t", 3)
			if len(p) != 3 {
				continue
			}
			plus, e1 := strconv.Atoi(p[0])
			minus, e2 := strconv.Atoi(p[1])
			if e1 != nil || e2 != nil {
				plus, minus = -1, -1
			}
			lines[p[2]] = [2]int{plus, minus}
		}
	}
	out, err := git(root, "status", "--porcelain=v1", "-uall", "-z")
	if err != nil {
		r.Err = "git status failed"
		return r
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		e := fields[i]
		if len(e) < 4 {
			continue
		}
		xy, path := e[:2], e[3:]
		if xy[0] == 'R' || xy[0] == 'C' {
			i++ // the source name comes in the next field
		}
		a := File{Path: path, Added: -1, Removed: -1, New: xy == "??", Deleted: strings.Contains(xy, "D")}
		if n, ok := lines[path]; ok {
			a.Added, a.Removed = n[0], n[1]
		}
		if a.Deleted {
			// Without the file there is no date: only shown while running.
			if !c.End.IsZero() {
				continue
			}
			a.OutsideWindow = true
		} else {
			st, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil || st.ModTime().Before(since) || st.ModTime().After(until) {
				continue
			}
		}
		a.Forbidden = c.ReadOnly || !isAllowed(path, r.Allowed)
		r.Files = append(r.Files, a)
	}

	// Commits during the run and the first one after. Only HEAD's: worktrees
	// share the repo, and with --all the commits of a parallel run, on
	// another branch, were attributed to this one.
	format := "--format=\x1e%H\x1f%cI\x1f%s"
	if out, err := git(root, "log", "HEAD", "--name-only", format,
		"--since="+since.Format(time.RFC3339), "--until="+end.Add(NextWindow).Format(time.RFC3339)); err == nil {
		for _, block := range strings.Split(out, "\x1e") {
			blockLines := strings.Split(strings.TrimSpace(block), "\n")
			p := strings.Split(blockLines[0], "\x1f")
			if len(p) != 3 {
				continue
			}
			at, _ := time.Parse(time.RFC3339, p[1])
			// The 2 s margin before the start is for file dates; a commit from
			// before the start is not part of the run (the orchestrator often
			// commits and launches the next run right away).
			if at.Before(c.Start.Truncate(time.Second)) {
				continue
			}
			cm := Commit{Hash: p[0][:min(8, len(p[0]))], Time: at, Subject: p[2]}
			for _, f := range blockLines[1:] {
				if f = strings.TrimSpace(f); f != "" {
					cm.Files = append(cm.Files, f)
					if c.ReadOnly || !isAllowed(f, r.Allowed) {
						cm.Outside++
					}
				}
			}
			switch {
			case !at.After(until):
				r.Commits = append(r.Commits, cm)
			case r.Next == nil || at.Before(r.Next.Time):
				next := cm
				r.Next = &next
			}
		}
	}

	// Violations.
	var outside []string
	for _, a := range r.Files {
		if a.Forbidden {
			outside = append(outside, a.Path)
		}
	}
	switch {
	case c.ReadOnly && len(r.Files) > 0:
		r.Warnings = append(r.Warnings, "was read-only and modified "+count(len(r.Files), "file"))
	case len(outside) > 0:
		r.Warnings = append(r.Warnings, "modified "+count(len(outside), "file")+" outside what was allowed")
	}
	if len(r.Commits) > 0 && (c.ReadOnly || forbidsCommit(c.Task)) {
		r.Warnings = append(r.Warnings, "made "+count(len(r.Commits), "commit")+" and the task forbade it")
	}
	return r
}

// Count: "1 file", "3 files".
func Count(n int, word string) string { return count(n, word) }

func count(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// Task phrases are matched in English and in Spanish (they match tasks
// written in Spanish).
var (
	reAllowed  = regexp.MustCompile(`(?i)(?:solo\s+(?:puedes|podrás|debes)\s+(?:modificar|tocar|editar|cambiar|crear|escribir)|\bonly\s+(?:modify|touch|edit|change|create|write)|\b(?:may|can)\s+(?:modify|touch|edit|change|create|write)\s+only)\b(.*)`)
	reFile     = regexp.MustCompile(`[\w\-./\\]*\w\.[A-Za-z0-9]+|[\w\-./\\]+[/\\]`)
	reNoCommit = regexp.MustCompile(`(?i)no\s+hagas\s+(?:git\s+)?commit|\bdo\s+not\s+(?:git\s+)?commit|\bdon'?t\s+(?:git\s+)?commit|\bnever\s+(?:git\s+)?commit`)
)

// StoredLength is how much of the task a run file stores (its first line).
const StoredLength = 200

// Allowed extracts from the task the «You may only modify …» list: file
// names and dirs (ending in /) up to the end of the sentence or a
// parenthesis. truncated: the task reached the stored limit, there may be more.
func Allowed(task string) (list []string, truncated bool) {
	truncated = len([]rune(task)) >= StoredLength-1
	lines := strings.Split(strings.ReplaceAll(task, "\r\n", "\n"), "\n")
	for idx, l := range lines {
		m := reAllowed.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		rest := m[1]
		filesInLine := reFile.FindAllString(rest, -1)
		restTrim := strings.TrimSpace(rest)
		if len(filesInLine) == 0 && strings.HasSuffix(restTrim, ":") {
			// Bullet list in the following lines.
			for i := idx + 1; i < len(lines); i++ {
				next := strings.TrimLeft(lines[i], " \t")
				var content string
				if strings.HasPrefix(next, "-") {
					content = strings.TrimPrefix(next, "-")
				} else if strings.HasPrefix(next, "*") {
					content = strings.TrimPrefix(next, "*")
				} else if strings.HasPrefix(next, "•") {
					content = strings.TrimPrefix(next, "•")
				} else {
					break
				}
				if j := strings.IndexAny(content, "("); j >= 0 {
					content = content[:j]
				}
				if j := strings.Index(content, ". "); j >= 0 {
					content = content[:j]
				}
				for _, f := range reFile.FindAllString(content, -1) {
					f = strings.TrimSuffix(strings.ReplaceAll(f, `\`, "/"), ".")
					list = append(list, strings.TrimPrefix(f, "./"))
				}
			}
			return list, truncated
		}

		// A one-line sentence is still cut at «(», at «. » and at the end of the line.
		sentence := rest
		if j := strings.IndexAny(sentence, "\r\n"); j >= 0 {
			sentence = sentence[:j]
		}
		if j := strings.IndexAny(sentence, "("); j >= 0 {
			sentence = sentence[:j]
		}
		if j := strings.Index(sentence, ". "); j >= 0 {
			sentence = sentence[:j]
		}
		for _, f := range reFile.FindAllString(sentence, -1) {
			f = strings.TrimSuffix(strings.ReplaceAll(f, `\`, "/"), ".")
			list = append(list, strings.TrimPrefix(f, "./"))
		}
		return list, truncated
	}
	return nil, truncated
}

// isAllowed: with no list, everything; otherwise the path is one of them,
// ends in one of them (the task usually names paths relative to the dir) or
// is inside an allowed dir.
func isAllowed(path string, list []string) bool {
	if len(list) == 0 {
		return true
	}
	for _, p := range list {
		if strings.HasSuffix(p, "/") {
			if strings.HasPrefix(path, p) || strings.Contains(path, "/"+p) {
				return true
			}
			continue
		}
		if path == p || strings.HasSuffix(path, "/"+p) {
			return true
		}
	}
	return false
}

func forbidsCommit(task string) bool { return reNoCommit.MatchString(task) }

// Cache keeps results for a while: the view is drawn every 450 ms and git is slow.
type Cache struct {
	mu sync.Mutex
	m  map[string]cacheEntry
}

type cacheEntry struct {
	r  Result
	at time.Time
}

// Result lifetime: short while running, longer once done.
const (
	TTLRunning = 3 * time.Second
	TTLDone    = 20 * time.Second
)

// Peek returns the last result computed for the key, without running git.
func (k *Cache) Peek(key string) (Result, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	e, ok := k.m[key]
	return e.r, ok
}

// Check returns the cached result if it is fresh; otherwise it computes it.
func (k *Cache) Check(c Run, now time.Time) Result {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]cacheEntry{}
	}
	e, ok := k.m[c.Key]
	k.mu.Unlock()
	ttl := TTLDone
	if c.End.IsZero() {
		ttl = TTLRunning
	}
	if ok && now.Sub(e.at) < ttl {
		return e.r
	}
	r := Check(c, now)
	k.mu.Lock()
	k.m[c.Key] = cacheEntry{r, now}
	k.mu.Unlock()
	return r
}
