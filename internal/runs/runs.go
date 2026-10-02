// Package runs defines the run file: one JSON file per delegated attempt,
// written by `panal delegate` when the attempt starts (status "running") and
// rewritten when it ends. Everything else in panal (readers, history, report)
// reads runs through this package, so the format lives in one place.
//
// Files written by the older delegar.sh helper use Spanish keys and status
// values; legacy.go translates them on read, so callers only ever see the
// English format.
package runs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Version of the format written by WriteFile.
const Version = 2

// Status of a run.
type Status string

const (
	Running      Status = "running"
	Done         Status = "done"
	OutOfQuota   Status = "out_of_quota"
	NoPermission Status = "no_permission"
	Skipped      Status = "skipped"
	Timeout      Status = "timeout"
	Failed       Status = "failed"
	Interrupted  Status = "interrupted"
)

// Exit codes of `panal delegate`, also stored in Run.RC.
const (
	ExitDone         = 0
	ExitFailed       = 1
	ExitTimeout      = 124
	ExitNoPermission = 126
	ExitInterrupted  = 130
	ExitOutOfQuota   = 75
)

// Run is one attempt of one agent at one task.
type Run struct {
	Version  int    `json:"version"`
	ID       string `json:"id"` // e.g. "20261002-101500"; the file is <ID>-<agent>.json
	Agent    string `json:"agent"`
	Model    string `json:"model"`
	Effort   string `json:"effort,omitempty"`
	Task     string `json:"task"`                // first line of the task
	TaskFile string `json:"task_file,omitempty"` // full task text, when it has more than one line
	Dir      string `json:"dir"`
	ReadOnly bool   `json:"read_only"`
	PID      int    `json:"pid"`
	Log      string `json:"log"`
	Start    string `json:"start"` // RFC 3339
	Status   Status `json:"status"`
	End      string `json:"end,omitempty"`
	RC       *int   `json:"rc,omitempty"`

	// What the router (internal/router) knew when the run was delegated.
	// TaskType and Tier are recorded for every run; the rest only when the
	// chain was picked by the router (panal delegate -c auto).
	TaskType string `json:"task_type,omitempty"` // review, fix, tests, refactor, docs, feature, other
	Tier     string `json:"tier,omitempty"`      // simple, medium, complex
	Auto     bool   `json:"auto,omitempty"`      // the chain came from the router
	Route    string `json:"route,omitempty"`     // the router's one-line explanation
	Choice   int    `json:"choice,omitempty"`    // auto: this link's place in the chain (1 = first choice)
	Explored bool   `json:"explored,omitempty"`  // auto: the first choice was exploration, not the favorite
}

// StartTime parses Start; zero if it is empty or invalid.
func (r Run) StartTime() time.Time { return parseTime(r.Start) }

// EndTime parses End; zero if it is empty or invalid.
func (r Run) EndTime() time.Time { return parseTime(r.End) }

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Parse decodes a run file in either format (current English or legacy
// delegar.sh Spanish) and returns it in the current format.
func Parse(data []byte) (Run, error) {
	r, _, err := parse(data)
	return r, err
}

func parse(data []byte) (r Run, legacy bool, err error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return Run{}, false, err
	}
	if isLegacy(probe) {
		r, err := parseLegacy(data)
		return r, true, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return Run{}, false, err
	}
	return r, false, nil
}

// ReadFile reads and parses one run file.
func ReadFile(path string) (Run, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Run{}, err
	}
	r, err := Parse(data)
	if err != nil {
		return Run{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return r, nil
}

// WriteFile writes r (in the current format) atomically to dir/<ID>-<agent>.json
// and returns the path.
func WriteFile(dir string, r Run) (string, error) {
	r.Version = Version
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, r.ID+"-"+r.Agent+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

// Home is panal's own directory: PANAL_DATA, else ~/.panal.
func Home() string {
	if d := os.Getenv("PANAL_DATA"); d != "" {
		return d
	}
	return filepath.Join(userHome(), ".panal")
}

// Dir is where `panal delegate` writes run files: PANAL_RUNS, else ~/.panal/runs.
func Dir() string {
	if d := os.Getenv("PANAL_RUNS"); d != "" {
		return d
	}
	return filepath.Join(Home(), "runs")
}

// LogDir is where `panal delegate` writes each attempt's output: PANAL_LOGS,
// else ~/.panal/logs.
func LogDir() string {
	if d := os.Getenv("PANAL_LOGS"); d != "" {
		return d
	}
	return filepath.Join(Home(), "logs")
}

// Dirs are all the directories run files are read from: Dir first, then the
// legacy delegar.sh directory when it exists (or is set explicitly).
func Dirs() []string { return withLegacy(Dir(), legacyDir()) }

// LogDirs are all the directories attempt logs are read from.
func LogDirs() []string { return withLegacy(LogDir(), legacyLogDir()) }

func withLegacy(dir, legacy string) []string {
	out := []string{dir}
	if legacy != "" && !strings.EqualFold(filepath.Clean(legacy), filepath.Clean(dir)) {
		out = append(out, legacy)
	}
	return out
}

func userHome() string {
	if u := os.Getenv("USERPROFILE"); u != "" {
		return u
	}
	u, _ := os.UserHomeDir()
	return u
}

// File is one run file found by List.
type File struct {
	Path   string
	Run    Run   // zero if Err is set
	Legacy bool  // written by delegar.sh
	Err    error // could not be read or parsed
}

// List reads every *.json run file in dirs, newest Start first (files whose
// Start can't be parsed go last). When the same ID and agent appear in more
// than one file, the current-format one wins, else the one in the earlier dir.
// Missing dirs are skipped.
func List(dirs []string) []File {
	var out []File
	index := map[string]int{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			f := File{Path: filepath.Join(dir, e.Name())}
			data, err := os.ReadFile(f.Path)
			if err != nil {
				continue
			}
			f.Run, f.Legacy, f.Err = parse(data)
			if f.Err != nil || f.Run.ID == "" || f.Run.Agent == "" {
				out = append(out, f)
				continue
			}
			key := f.Run.ID + "\x00" + f.Run.Agent
			if i, ok := index[key]; ok {
				if out[i].Legacy && !f.Legacy {
					out[i] = f
				}
				continue
			}
			index[key] = len(out)
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := out[i].Run.StartTime(), out[j].Run.StartTime()
		if ti.IsZero() != tj.IsZero() {
			return tj.IsZero()
		}
		return ti.After(tj)
	})
	return out
}
