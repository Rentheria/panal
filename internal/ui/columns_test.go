package ui

import (
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

func TestDistribute(t *testing.T) {
	base := []int{10, 20, 10, 0}
	minimum := []int{10, 8, 0, 0}
	// Room to spare: the flexible column takes the rest.
	if w := distribute(base, minimum, 3, 10, 100, []int{1, 2}); w[3] != 60 {
		t.Fatalf("with room to spare, flex = %d, want 60 (%v)", w[3], w)
	}
	// A little short: column 1 shrinks before column 2 is touched.
	if w := distribute(base, minimum, 3, 10, 45, []int{1, 2}); w[1] != 15 || w[2] != 10 || w[3] != 10 {
		t.Fatalf("shrink: %v", w)
	}
	// Very short: column 2 (minimum 0) goes and column 0 never drops below its minimum.
	w := distribute(base, minimum, 3, 10, 20, []int{1, 2})
	if w[0] != 10 || w[2] != 0 {
		t.Fatalf("drop: %v", w)
	}
	sum := 0
	for _, x := range w {
		sum += x
	}
	if sum > 20 && w[3] > 0 {
		t.Fatalf("exceeds the total: %v", w)
	}
}

func TestSummarizeTask(t *testing.T) {
	// Tasks are written by the user, in Spanish or English: both must work.
	cases := map[string]string{
		"You work in this dir (Flask repo my-api). Do not commit or push. You can only modify app/models.py.":                "You can only modify app/models.py.",
		"Read-only: don't change any file. Git repo ct-front.":                                                               "Git repo ct-front.",
		"Create the file b.txt with the text bye.":                                                                           "Create the file b.txt with the text bye.",
		"Trabajas en esta carpeta (repositorio Flask mi-api). No hagas commit ni push. Solo puedes modificar app/models.py.": "Solo puedes modificar app/models.py.",
		"Solo lectura: no modifiques ningún archivo. Repositorio git ct-front.":                                              "Repositorio git ct-front.",
		"Trabajas en C:\\Codigo\\wt\\x (worktree de git, rama f1). Programa Go: lee README.md.":                              "Programa Go: lee README.md.",
		"Crea el archivo b.txt con el texto adios.":                                                                          "Crea el archivo b.txt con el texto adios.",
		"No hagas commit.": "No hagas commit.", // if nothing is left, the task as is
		"":                 "",
	}
	for in, want := range cases {
		if got := summarizeTask(in); got != want {
			t.Errorf("summarizeTask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateMiddle(t *testing.T) {
	r := `C:\Users\someone\AppData\Local\Temp\claude\session\scratchpad`
	got := truncateMiddle(r, 30)
	if w := len([]rune(got)); w != 30 {
		t.Fatalf("width %d, want 30: %q", w, got)
	}
	if got[:3] != `C:\` || got[len(got)-10:] != "scratchpad" {
		t.Fatalf("should keep the start and the end: %q", got)
	}
	if !looksLikePath(r) || looksLikePath("You can only modify app/models.py and app/routes.py.") {
		t.Fatal("looksLikePath: a path yes, text with spaces no")
	}
	if truncateMiddle("short", 30) != "short" {
		t.Fatal("text that fits is left alone")
	}
}

func TestTaskTitle(t *testing.T) {
	cases := []struct {
		full    string
		leading string
		want    string
	}{
		{
			full:    "Trabajas en C:\\Codigo\\wt\\tui-ux. Lee README.md.\nObjetivo: que las pantallas se entiendan de un vistazo.\nEsta ronda es sobre QUÉ dicen las pantallas.",
			leading: "Trabajas en C:\\Codigo\\wt\\tui-ux. Lee README.md.",
			want:    "Que las pantallas se entiendan de un vistazo.",
		},
		{
			full:    "Solo puedes modificar/crear a.go, b.go. No hagas commit.\nObjetivo general: unificar vistas del tablero.\nOtras instrucciones.",
			leading: "Solo puedes modificar/crear a.go, b.go. No hagas commit.",
			want:    "Unificar vistas del tablero.",
		},
		{
			full:    "Trabajas en repo. No hagas commit.\nImplementar pruebas de pantalla fijas. Verificar anchos.",
			leading: "Trabajas en repo. No hagas commit.",
			want:    "Implementar pruebas de pantalla fijas.",
		},
		{
			full:    "",
			leading: "Trabajas en repo. Solo lectura: no toques nada. Repositorio git ct-front.",
			want:    "Repositorio git ct-front.",
		},
		{
			full:    "",
			leading: "Solo puedes modificar/crear internal/ui/columnas.go. No hagas commit.",
			want:    "Solo puedes modificar/crear internal/ui/columnas.go. No hagas commit.",
		},
		{
			full:    "No hagas commit ni push.\nNo modifiques nada.\nSolo lectura.",
			leading: "No hagas commit ni push.",
			want:    "No hagas commit ni push.",
		},
		{
			full:    "SOLO PUEDES MODIFICAR a.go. NO HAGAS COMMIT.\nOBJETIVO: tarea en mayúsculas.",
			leading: "SOLO PUEDES MODIFICAR a.go.",
			want:    "Tarea en mayúsculas.",
		},
		{
			full:    "Es un programa Go. Lee README.md.\nCrear el archivo b.txt con el texto adios.",
			leading: "Es un programa Go. Lee README.md.",
			want:    "Crear el archivo b.txt con el texto adios.",
		},
		{
			full:    "You work in C:\\Codigo\\wt\\tui-ux. Read README.md.\nGoal: make the screens clear at a glance.\nThis round is about WHAT the screens say.",
			leading: "You work in C:\\Codigo\\wt\\tui-ux. Read README.md.",
			want:    "Make the screens clear at a glance.",
		},
		{
			full:    "You can only modify a.go. Do not commit.\nProblem: the samples are lost on quit.",
			leading: "You can only modify a.go. Do not commit.",
			want:    "The samples are lost on quit.",
		},
		{
			full:    "",
			leading: "",
			want:    "",
		},
	}
	for _, c := range cases {
		got := taskTitle(c.full, c.leading)
		if got != c.want {
			t.Errorf("taskTitle(%q, %q) = %q, want %q", c.full, c.leading, got, c.want)
		}
	}
}

func TestShortProject(t *testing.T) {
	cases := map[string]string{
		`C:\Codigo\wt\tui-panel`:  "tui-panel",
		`C:\Codigo\wt\tui-panel\`: "tui-panel",
		"C:/Codigo/wt/tui-panel":  "tui-panel",
		"C:/Codigo/wt/tui-panel/": "tui-panel",
		"tui-panel":               "tui-panel",
		"":                        "",
		"/":                       "",
		`\`:                       "",
	}
	for in, want := range cases {
		if got := shortProject(in); got != want {
			t.Errorf("shortProject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShortModel(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"gemini-3.8-flash-medium", "gemini-3.8-flash"},
		{"gemini-3.8-flash-low", "gemini-3.8-flash"},
		{"gpt-6-luna (low)", "luna (low)"},
		{"Opus 5.5 (1M context) · medium", "opus 5.5"},
		{"opencode/nemotron-4-340b", "nemotron-4-340b"},
		{"opencode/qwen-2.5-coder", "qwen-2.5-coder"},
		{"claude-3-7-sonnet", "claude-3-7-sonnet"},
		{"o3-mini", "o3-mini"},
		{"gemini-2.5-pro", "gemini-2.5-pro"},
		{"", ""},
	}
	for _, c := range cases {
		if got := shortModel(c.in); got != c.want {
			t.Errorf("shortModel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShortAgoAndRowTime(t *testing.T) {
	for d, want := range map[time.Duration]string{
		46 * time.Second:              "46 s",
		21*time.Hour + 46*time.Minute: "21 h",
	} {
		if got := shortAgo(d); got != want {
			t.Errorf("shortAgo(%v) = %q, want %q", d, got, want)
		}
	}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	cases := []struct {
		f    state.Row
		want string
	}{
		{state.Row{Since: now.Add(-5 * time.Minute)}, "5 min"},
		{state.Row{End: now.Add(-3*time.Hour - 10*time.Minute)}, "3 h ago"},
		{state.Row{}, "—"},
	}
	for _, c := range cases {
		if got := rowTime(c.f, now); got != c.want {
			t.Errorf("rowTime(%+v) = %q, want %q", c.f, got, c.want)
		}
	}
}
