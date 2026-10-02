package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// External is an optional classifier the user plugs in with
// `router = <command>` in panal.conf (or PANAL_ROUTER): a program (and its
// arguments, split on spaces; no shell) that gets the task as JSON on stdin
// and answers JSON on stdout within Timeout:
//
//	in:  {"task": "...", "dir": "...", "read_only": false,
//	      "type": "fix", "tier": "medium", "score": 0,
//	      "pool": ["codex:gpt-6-luna:low", "agy"]}
//	out: {"tier": "simple|medium|complex", "arm": "optional, one of pool"}
//
// "tier" replaces the built-in tier; "arm", if given and in the pool, goes
// first in the chain. On any error (it can't start, exits non-zero, takes
// too long, prints something that isn't that JSON) panal uses the built-in
// router and says so in the explanation. It can be anything: a script with
// your own rules, a small decision model, a local Ollama model.
type External struct {
	Command string
	Timeout time.Duration // 0 = DefaultExternalTimeout
	Env     []string      // added to the environment (tests)
}

// DefaultExternalTimeout is how long the external router may take.
const DefaultExternalTimeout = 5 * time.Second

// ExternalInput is what the external router reads on stdin.
type ExternalInput struct {
	Task     string   `json:"task"`
	Dir      string   `json:"dir,omitempty"`
	ReadOnly bool     `json:"read_only"`
	Type     string   `json:"type"`
	Tier     string   `json:"tier"`
	Score    int      `json:"score"`
	Pool     []string `json:"pool"`
}

// ExternalAnswer is what it prints on stdout.
type ExternalAnswer struct {
	Tier string `json:"tier"`
	Arm  string `json:"arm,omitempty"`
}

// Ask runs the external router for a request and the built-in decision so far.
func (x *External) Ask(q Request, d Decision) (ExternalAnswer, error) {
	args := strings.Fields(x.Command)
	if len(args) == 0 {
		return ExternalAnswer{}, errors.New("no command")
	}
	in, err := json.Marshal(ExternalInput{
		Task: q.Task, Dir: q.Dir, ReadOnly: q.ReadOnly,
		Type: d.Type, Tier: d.Tier, Score: d.Score, Pool: q.Pool,
	})
	if err != nil {
		return ExternalAnswer{}, err
	}
	timeout := x.Timeout
	if timeout <= 0 {
		timeout = DefaultExternalTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if len(x.Env) > 0 {
		cmd.Env = append(cmd.Environ(), x.Env...)
	}
	cmd.Stdin = bytes.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return ExternalAnswer{}, fmt.Errorf("no answer in %s", timeout)
	}
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg != "" {
			return ExternalAnswer{}, fmt.Errorf("%v: %s", err, msg)
		}
		return ExternalAnswer{}, err
	}
	var ans ExternalAnswer
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &ans); err != nil {
		return ExternalAnswer{}, errors.New("its answer is not JSON")
	}
	ans.Tier = strings.ToLower(strings.TrimSpace(ans.Tier))
	ans.Arm = strings.TrimSpace(ans.Arm)
	if ans.Tier != "" && !validTier(ans.Tier) {
		return ExternalAnswer{}, fmt.Errorf("unknown tier %q", ans.Tier)
	}
	if ans.Tier == "" && ans.Arm == "" {
		return ExternalAnswer{}, errors.New("its answer has neither tier nor arm")
	}
	return ans, nil
}
