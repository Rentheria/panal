package report

import (
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/history"
)

func TestTaskType(t *testing.T) {
	cases := []struct {
		c    history.Run
		want string
	}{
		{history.Run{ReadOnly: true, Task: "Crea un archivo"}, "review"},
		// Tasks written in Spanish.
		{history.Run{Task: "Solo puedes modificar app/x.py. No hagas commit. Revisa el diff de la rama y dime los errores."}, "review"},
		{history.Run{Task: "Solo puedes modificar a.go. Corrige el bug que hace fallar la tabla."}, "fix"},
		{history.Run{Task: "Solo puedes crear a_test.go. Escribe pruebas para el lector. Las pruebas deben cubrir los errores."}, "tests"},
		{history.Run{Task: "Renombra las funciones y limpia el código muerto."}, "refactor"},
		{history.Run{Task: "Documenta el paquete en el README."}, "docs"},
		{history.Run{Task: "Agrega una pestaña nueva que muestre el informe."}, "feature"},
		{history.Run{Task: "Trabajas en C:/x (rama f1). Programa Go: lee README.md."}, "other"},
		// A passing «revisión» in the middle of a long task does not make it a review.
		{history.Run{FullTask: "Solo puedes modificar internal/ui/. No hagas commit.\n\nLa navegación ya quedó bien.\n1. Agrega la paleta nueva.\n2. La marca ⚠ de revisión se queda.\n3. Crea la insignia."}, "feature"},
		// The same tasks written in English.
		{history.Run{Task: "You may only modify app/x.py. Do not commit. Review the branch diff and tell me the errors."}, "review"},
		{history.Run{Task: "You may only modify a.go. Fix the bug that breaks the table."}, "fix"},
		{history.Run{Task: "You may only create a_test.go. Write tests for the reader. The tests must cover the errors."}, "tests"},
		{history.Run{Task: "Rename the functions and remove the dead code."}, "refactor"},
		{history.Run{Task: "Document the package in the README."}, "docs"},
		{history.Run{Task: "Add a new tab that shows the report."}, "feature"},
		{history.Run{Task: "You work in C:/x (branch f1). Go program: read README.md."}, "other"},
		{history.Run{Task: "Goal: add a new view."}, "feature"},
	}
	for _, c := range cases {
		if got := TaskType(c.c); got != c.want {
			t.Errorf("%q: %s, want %s", c.c.Task+c.c.FullTask, got, c.want)
		}
	}
}

func TestMatrixAndSavings(t *testing.T) {
	d := time.Date(2026, 9, 26, 10, 0, 0, 0, time.Local)
	cs := []history.Run{
		{Agent: "agy", Status: "done", Task: "Fix the bug", Start: d, Tokens: 1000},
		{Agent: "agy", Status: "failed", Task: "Fix the bug", Start: d},
		{Agent: "codex", Status: "done", ReadOnly: true, Start: d, Tokens: 500},
		{Agent: "claude", Status: "done", Task: "Fix", Start: d, Tokens: 99999},
		{Agent: "codex", Status: "running", Start: d},
	}
	m := ComputeMatrix(cs, time.Time{}, nil)
	if ce := m.Cells["agy"]["fix"]; ce == nil || ce.Runs != 2 || ce.Clean != 1 {
		t.Fatalf("agy fix: %+v", ce)
	}
	if len(m.Types) != 2 || m.Types[0] != "review" || m.Types[1] != "fix" {
		t.Fatalf("present types, in order: %v", m.Types)
	}
	a := ComputeSavings(cs, time.Time{}, 15)
	if a.Runs != 2 || a.Tokens != 1500 || a.Dollars < 0.0224 || a.Dollars > 0.0226 {
		t.Fatalf("savings (without claude or unfinished runs): %+v", a)
	}
}

func TestExport(t *testing.T) {
	d := time.Date(2026, 9, 26, 10, 0, 0, 0, time.Local)
	cs := []history.Run{
		{Agent: "agy", Status: "done", Task: "Fix the bug", Start: d, End: d.Add(5 * time.Minute), Tokens: 1000},
		{Agent: "codex", Status: "failed", Task: "Add a view", Start: d},
	}
	now := d.Add(time.Hour)
	for f, want := range map[string]string{
		"md":   "| agy | 1 | 100% |",
		"csv":  "agy,1,1,0,0,0,300,300,0,1000",
		"json": `"clean": 1`,
	} {
		txt, err := Export(cs, 7, now, nil, f, 0, nil)
		if err != nil || !strings.Contains(txt, want) {
			t.Errorf("%s: missing %q (%v):\n%s", f, want, err, txt)
		}
	}
	csv, _ := Export(cs, 7, now, nil, "csv", 0, nil)
	if !strings.HasPrefix(csv, "agent,runs,finished,failed,out_of_quota,violations,median_s,total_s,credits,tokens\n") {
		t.Errorf("csv header:\n%s", csv)
	}
	for _, f := range []string{"", "text"} {
		if txt, err := Export(cs, 7, now, nil, f, 0, nil); err != nil || !strings.HasPrefix(txt, "last 7 days\n") {
			t.Errorf("format %q must give the text table (%v):\n%s", f, err, txt)
		}
	}
	if _, err := Export(cs, 7, now, nil, "xls", 0, nil); err == nil {
		t.Error("an unknown format must return an error")
	}
}

func TestExportRouterSection(t *testing.T) {
	d := time.Date(2026, 9, 26, 10, 0, 0, 0, time.Local)
	cs := []history.Run{{Agent: "agy", Status: "done", Task: "Fix the bug", Start: d, End: d.Add(time.Minute)}}
	rs := &RouterSummary{Decisions: 4, FirstChoiceRuns: 3, FirstChoiceOK: 2, FellBack: 1, Explored: 1, Exploited: 3}
	want := "router: 4 auto decisions · first choice ok 2/3 (66%) · 1 fell back (out of quota) · explored 1, exploited 3"
	for _, f := range []string{"text", "md"} {
		txt, _ := Export(cs, 7, d.Add(time.Hour), nil, f, 0, rs)
		if !strings.Contains(txt, want) {
			t.Errorf("%s: missing the router line:\n%s", f, txt)
		}
	}
	if txt, _ := Export(cs, 7, d.Add(time.Hour), nil, "md", 0, rs); !strings.Contains(txt, "### Router") {
		t.Errorf("md: no Router heading:\n%s", txt)
	}
	if txt, _ := Export(cs, 7, d.Add(time.Hour), nil, "json", 0, rs); !strings.Contains(txt, `"first_choice_ok": 2`) {
		t.Errorf("json: no router object:\n%s", txt)
	}
	if txt, _ := Export(cs, 7, d.Add(time.Hour), nil, "text", 0, nil); strings.Contains(txt, "router:") {
		t.Errorf("no summary, no section:\n%s", txt)
	}
	if got := RouterText(RouterSummary{}); !strings.Contains(got, "no auto decisions yet") {
		t.Errorf("empty: %q", got)
	}
}
