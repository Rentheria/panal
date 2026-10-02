package feedback

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/runs"
)

const usage = `Usage: panal feedback RUN good|bad [note]
       panal feedback RUN clear

Rates a delegated run, so the router (panal delegate -c auto) learns from
it: a run rated bad counts as a failure even if the agent finished its turn.

RUN is the run's ID as the dashboard shows it (e.g. 20261002-101500), its
run file's name without .json (20261002-101500-codex), or "last" for the
newest finished run. When an ID has several attempts (a fallback chain),
the one that ended the chain is rated.

Ratings are saved in %s.
`

// Main runs `panal feedback` with args (without "feedback") and returns the
// exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, runs.Dirs(), Path(), time.Now())
}

func run(args []string, stdout, stderr io.Writer, dirs []string, path string, now time.Time) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help") {
		fmt.Fprintf(stdout, usage, path)
		return 0
	}
	if len(args) < 2 {
		fmt.Fprintf(stderr, usage, path)
		return 2
	}
	rating := strings.ToLower(args[1])
	switch rating {
	case Good, Bad:
	case "clear", "none":
		rating = ""
	default:
		fmt.Fprintf(stderr, "panal feedback: the rating is good, bad or clear, not %q\n", args[1])
		return 2
	}
	r, ok := Find(runs.List(dirs), args[0])
	if !ok {
		fmt.Fprintf(stderr, "panal feedback: no run %q in %s\n", args[0], strings.Join(dirs, ", "))
		return 1
	}
	key := Key(r.ID, r.Agent)
	if err := Set(path, key, rating, strings.Join(args[2:], " "), now); err != nil {
		fmt.Fprintln(stderr, "panal feedback:", err)
		return 1
	}
	if rating == "" {
		fmt.Fprintf(stdout, "cleared the rating of %s (%s)\n", key, oneLine(r.Task))
	} else {
		fmt.Fprintf(stdout, "rated %s %s (%s)\n", key, rating, oneLine(r.Task))
	}
	return 0
}

// Find picks the run that id names among files (as runs.List returns them,
// newest first): "last" is the newest finished run; an exact <ID>-<agent>
// is that run; a bare ID with several attempts is the one that ended the
// chain (the last one that is not skipped or out of quota, else the newest).
func Find(files []runs.File, id string) (runs.Run, bool) {
	id = strings.TrimSuffix(strings.TrimSpace(id), ".json")
	if id == "last" {
		for _, f := range files {
			if f.Err == nil && f.Run.Status != runs.Running && f.Run.Status != runs.Skipped && f.Run.Status != runs.OutOfQuota {
				return f.Run, true
			}
		}
		return runs.Run{}, false
	}
	var same []runs.Run
	for _, f := range files {
		if f.Err != nil {
			continue
		}
		if Key(f.Run.ID, f.Run.Agent) == id {
			return f.Run, true
		}
		if f.Run.ID == id {
			same = append(same, f.Run)
		}
	}
	if len(same) == 0 {
		return runs.Run{}, false
	}
	// Attempts of one delegation share their start second; the one that
	// ended the chain started last.
	best := same[0]
	for _, r := range same {
		if signal(r.Status) && (!signal(best.Status) || r.StartTime().After(best.StartTime())) {
			best = r
		}
	}
	return best, true
}

func signal(s runs.Status) bool {
	return s != runs.Skipped && s != runs.OutOfQuota
}

func oneLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > 60 {
		s = string(r[:59]) + "…"
	}
	return s
}
