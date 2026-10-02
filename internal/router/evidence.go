package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/activity"
	"github.com/AlbertoVasquezR/panal/internal/changes"
	"github.com/AlbertoVasquezR/panal/internal/feedback"
	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/runs"
)

// Outcome of a past run, as the router learns from it.
type Outcome int

const (
	NoSignal Outcome = iota // out of quota, skipped, interrupted, still running: says nothing about the arm
	Success
	Failure
)

func (o Outcome) String() string {
	return [...]string{"no signal", "success", "failure"}[o]
}

// Observation is everything known about how a run went.
type Observation struct {
	Status      runs.Status
	Violated    bool   // it broke the task's rules (internal/changes)
	Rating      string // the user's rating (internal/feedback)
	TestsSeen   bool   // tests ran in its activity (internal/activity)
	TestsPassed bool   // and the last ones passed
}

// OutcomeOf maps an observation to an outcome.
//
// The user's rating wins: good is a success and bad a failure, whatever the
// status said (they looked at the work), except for runs where the arm never
// worked on the task (running, out of quota, skipped). Without a rating:
//
// Success: status done, no rule broken and, if tests were seen in its
// activity, the last ones passed.
//
// Failure: failed, timeout, no permission, a broken rule, or done with its
// last tests failing.
//
// No signal: out of quota, skipped, interrupted, running, or anything else.
func OutcomeOf(o Observation) Outcome {
	switch o.Status {
	case runs.Running, runs.OutOfQuota, runs.Skipped:
		return NoSignal // the arm never really worked on it
	}
	switch o.Rating {
	case feedback.Bad:
		return Failure
	case feedback.Good:
		return Success
	}
	switch o.Status {
	case runs.Failed, runs.Timeout, runs.NoPermission:
		return Failure
	case runs.Done:
		if o.Violated || (o.TestsSeen && !o.TestsPassed) {
			return Failure
		}
		return Success
	}
	return NoSignal
}

// Evidence is one past run, reduced to what the router learns from.
type Evidence struct {
	Key     string // run key (<ID>-<agent>)
	Arm     string
	CLI     string
	Type    string
	Tier    string
	At      time.Time // when it ended (else started)
	Outcome Outcome
}

// ArmKey is the arm of a run: "cli", "cli:model", "cli:model:effort" or
// "cli::effort", the way panal delegate writes a chain link.
func ArmKey(cli, model, effort string) string {
	s := cli
	if model != "" {
		s += ":" + model
	}
	if effort != "" {
		if model == "" {
			s += ":"
		}
		s += ":" + effort
	}
	return s
}

// ---------------------------------------------------------------- checks --

// Check is the part of an observation that costs git or log reads: whether
// the run broke its task and how its tests went. It is final once the run
// has ended, so it is cached in ~/.panal/router-checks.json.
type Check struct {
	Violated    bool      `json:"violated,omitempty"`
	TestsSeen   bool      `json:"tests_seen,omitempty"`
	TestsPassed bool      `json:"tests_passed,omitempty"`
	At          time.Time `json:"at"`
}

// ChecksFile is the cache's name inside runs.Home().
const ChecksFile = "router-checks.json"

// Checks caches the checks of finished runs, by run key.
type Checks struct {
	Path string // "" = do not persist
	mu   sync.Mutex
	m    map[string]Check
	// dirty: something new to save.
	dirty bool
	// Run checks a run (tests replace it); default CheckRun.
	Run func(history.Run, time.Time) Check
}

// LoadChecks reads the cache (a missing or broken file is an empty cache).
func LoadChecks(path string) *Checks {
	c := &Checks{Path: path, m: map[string]Check{}}
	if path == "" {
		return c
	}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c.m)
		if c.m == nil {
			c.m = map[string]Check{}
		}
	}
	return c
}

// Get returns the check of a finished run, computing it if needed. Only
// done runs need one: every other status decides the outcome on its own.
func (c *Checks) Get(r history.Run, now time.Time) Check {
	if r.Status != runs.Done {
		return Check{}
	}
	c.mu.Lock()
	if v, ok := c.m[r.Key()]; ok {
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()
	fn := c.Run
	if fn == nil {
		fn = CheckRun
	}
	v := fn(r, now)
	c.Put(r.Key(), v)
	return v
}

// Put stores a check.
func (c *Checks) Put(key string, v Check) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]Check{}
	}
	c.m[key] = v
	c.dirty = true
}

// Save writes the cache if it changed and has a path.
func (c *Checks) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Path == "" || !c.dirty {
		return nil
	}
	b, err := json.MarshalIndent(c.m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	c.dirty = false
	return os.Rename(tmp, c.Path)
}

// CheckRun checks a run for real: its changes against the task's rules
// (git, read-only) and the last tests in its log.
func CheckRun(r history.Run, now time.Time) Check {
	var c Check
	c.At = now
	if r.Dir != "" && r.Agent != "claude" {
		task, full := r.Task, false
		if r.FullTask != "" {
			task, full = r.FullTask, true
		}
		res := changes.Check(changes.Run{
			Key: r.Key(), Dir: r.Dir, Start: r.Start, End: r.End,
			Task: task, Complete: full, ReadOnly: r.ReadOnly,
		}, now)
		c.Violated = res.HasViolations()
	}
	_, c.TestsPassed, c.TestsSeen = activity.LatestTests(r.Agent, r.Log)
	return c
}

// Observe builds the observation of a run.
func Observe(r history.Run, checks *Checks, now time.Time) Observation {
	o := Observation{Status: r.Status, Rating: r.Rating}
	if checks != nil {
		c := checks.Get(r, now)
		o.Violated, o.TestsSeen, o.TestsPassed = c.Violated, c.TestsSeen, c.TestsPassed
	}
	return o
}

// Gather turns past runs into evidence. Runs without a recorded type or tier
// (older than the router, or not delegated by panal) are classified now
// from their task.
func Gather(rs []history.Run, checks *Checks, now time.Time) []Evidence {
	var out []Evidence
	for _, r := range rs {
		if r.Agent == "" || r.Agent == "claude" {
			continue
		}
		o := OutcomeOf(Observe(r, checks, now))
		typ, tier := r.TaskType, r.Tier
		if typ == "" || !validTier(tier) {
			task := r.FullTask
			if task == "" {
				task = r.Task
			}
			f := Extract(task, r.ReadOnly)
			if typ == "" {
				typ = f.Type
			}
			if !validTier(tier) {
				tier, _, _ = Classify(f)
			}
		}
		at := r.End
		if at.IsZero() {
			at = r.Start
		}
		out = append(out, Evidence{
			Key: r.Key(), Arm: ArmKey(r.Agent, r.ModelID, r.Effort), CLI: r.Agent,
			Type: typ, Tier: tier, At: at, Outcome: o,
		})
	}
	return out
}
