package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/mascots"
	"github.com/AlbertoVasquezR/panal/internal/readers"
	"github.com/AlbertoVasquezR/panal/internal/runs"
	"github.com/AlbertoVasquezR/panal/internal/state"
)

func TestUnseenIsMarkedAndCleared(t *testing.T) {
	m := New(nil, time.Second)
	m.rows = []state.Row{{Agent: "codex", Status: state.Done}, {Agent: "claude"}}
	m.noteUnseen("codex", mascots.Celebrate)
	m.syncUnseen()
	if !m.rows[0].Unseen || m.rows[1].Unseen {
		t.Fatalf("only codex should be unseen: %+v", m.rows)
	}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	if !strings.Contains(Card(m.rows[0], Animation{}, 40, false, now, 0), "✦ CODEX") {
		t.Fatal("the card should have \"✦\" next to the name")
	}
	m.noteUnseen("codex", mascots.WakeUp)
	m.syncUnseen()
	if m.rows[0].Unseen {
		t.Fatal("once it works again it is no longer unseen")
	}
	m.noteUnseen("codex", mascots.Scared)
	m.syncUnseen()
	m.markSelSeen() // the selection is on the first one, codex
	if m.rows[0].Unseen || m.unseen["codex"] {
		t.Fatal("opening the detail should clear the mark")
	}
}

// isolateSources points every source panal reads at an empty home, so the
// machine's own data and variables don't leak into a test. It returns the home.
func isolateSources(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	vars := []string{"PANAL_DATA", "PANAL_RUNS", "PANAL_LOGS", "PANAL_CLAUDE", "PANAL_CONF",
		"CODEX_HOME", "CODEX_SESSIONS", "OPENCODE_LOG", "OPENCODE_DB"}
	for _, v := range append(vars, runs.LegacyEnv...) {
		t.Setenv(v, "")
	}
	return home
}

func TestFirstRunSaysWhatIsMissing(t *testing.T) {
	isolateSources(t)
	// The label of the delegated runs source comes from internal/readers.
	runsLabel := ""
	for _, s := range readers.Sources(readers.All()) {
		if s.Env == "PANAL_RUNS" {
			runsLabel = s.What
		}
	}
	if runsLabel == "" {
		t.Fatal("no source for the delegated runs")
	}
	for _, width := range []int{60, 100} {
		txt := stripANSI(CaptureView(readers.All(), width, ""))
		for _, want := range []string{"Nothing to show yet", runsLabel, "Getting started"} {
			if !strings.Contains(txt, want) {
				t.Errorf("width %d: missing %q:\n%s", width, want, txt)
			}
		}
		if width >= 100 && !strings.Contains(txt, "missing · PANAL_RUNS") {
			t.Errorf("it should say which variable moves the runs dir:\n%s", txt)
		}
		if strings.Contains(txt, "legacy") {
			t.Errorf("without a legacy dir it must not be listed:\n%s", txt)
		}
	}
}
