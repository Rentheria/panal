package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

func TestKeysCAndWOpenDir(t *testing.T) {
	var launched [][]string
	old := launch
	launch = func(a []string) error { launched = append(launched, a); return nil }
	defer func() { launch = old }()

	dir := t.TempDir()
	m := New(nil, time.Second)
	m.rows = []state.Row{{Agent: "codex", Dir: dir}}
	for _, k := range []string{"c", "w"} {
		mod, _ := m.Update(keyMsg(k))
		m = mod.(Model)
	}
	if len(launched) != 2 || launched[0][0] != "code" || launched[0][len(launched[0])-1] != dir || launched[1][0] != "wt" {
		t.Fatalf("c opens VS Code and w the terminal in the dir: %v", launched)
	}
	m.rows[0].Dir = dir + `\missing`
	m.openDir("c")
	if !strings.Contains(m.alert, "no longer exists") || len(launched) != 2 {
		t.Fatalf("a deleted dir is reported and nothing is launched: %q", m.alert)
	}
}
