package router

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/AlbertoVasquezR/panal/internal/changes"
	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/report"
)

// Tiers: how big a task looks. The router learns separately per tier, and
// its priors expect cheap arms to do well at simple tasks and strong arms at
// complex ones.
const (
	Simple  = "simple"
	Medium  = "medium"
	Complex = "complex"
)

// Tiers in order.
var Tiers = []string{Simple, Medium, Complex}

// Features are what the router looks at in a task. Nothing here runs a
// model: it is the task text, the read-only flag and the task's own rules.
type Features struct {
	Type        string // report.TaskType: review, fix, tests, refactor, docs, feature, other
	Length      int    // characters of the task's goal (report.Goal)
	Files       int    // files the task allows to modify (changes.Allowed)
	Dirs        int    // directories it allows to modify
	Steps       int    // numbered or bulleted lines
	ReadOnly    bool
	lower       string // the task in lower case, for the keyword rules
	fileAllowed bool   // the task has a "you may only modify" list
}

// Extract computes the features of a task.
func Extract(task string, readOnly bool) Features {
	f := Features{
		Type:     report.TaskType(history.Run{Task: firstLine(task), FullTask: task, ReadOnly: readOnly}),
		ReadOnly: readOnly,
	}
	// Size and keywords come from the goal only: the rule phrases an
	// orchestrator repeats in every task ("do not commit", "you may only
	// modify…") say nothing about how hard it is.
	goal := report.Goal(task)
	if goal == "" {
		goal = task
	}
	f.Length = utf8.RuneCountInString(strings.TrimSpace(goal))
	f.lower = strings.ToLower(goal)
	allowed, _ := changes.Allowed(task)
	f.fileAllowed = len(allowed) > 0
	for _, a := range allowed {
		if strings.HasSuffix(a, "/") {
			f.Dirs++
		} else {
			f.Files++
		}
	}
	f.Steps = len(reStep.FindAllString(task, -1))
	return f
}

var reStep = regexp.MustCompile(`(?m)^\s*(?:\d+[.)]|[-*•])\s+\S`)

// rule is one row of the tier table: a name (shown by `panal route`), how
// many points it adds and when it applies.
type rule struct {
	name   string
	points int
	when   func(f Features) bool
}

// kw matches a keyword pattern against the lower-case task.
func kw(pattern string) func(Features) bool {
	re := regexp.MustCompile(pattern)
	return func(f Features) bool { return re.MatchString(f.lower) }
}

// tierRules is the whole tier classifier. The points of the rules that apply
// are added up: −2 or less is simple, +2 or more is complex, the rest is
// medium. Keyword rules match tasks written in English or in Spanish (the
// Spanish words match tasks written in Spanish); each rule counts once,
// however many of its words appear.
var tierRules = []rule{
	// Size of the text. A long task usually means many requirements.
	{"short task", -1, func(f Features) bool { return f.Length < 200 }},
	{"long task", +1, func(f Features) bool { return f.Length >= 1500 && f.Length < 4000 }},
	{"very long task", +2, func(f Features) bool { return f.Length >= 4000 }},
	{"many steps", +1, func(f Features) bool { return f.Steps >= 4 }},

	// What the agent may do.
	{"read-only", -1, func(f Features) bool { return f.ReadOnly }},
	{"one allowed file", -1, func(f Features) bool { return f.fileAllowed && f.Files == 1 && f.Dirs == 0 }},
	{"many allowed files", +1, func(f Features) bool { return f.Files+f.Dirs >= 5 }},
	{"allowed directories", +1, func(f Features) bool { return f.Dirs > 0 }},

	// Task type (report.TaskType).
	{"docs", -1, func(f Features) bool { return f.Type == "docs" }},
	{"refactor", +1, func(f Features) bool { return f.Type == "refactor" }},
	{"feature", +1, func(f Features) bool { return f.Type == "feature" }},

	// Size words. One alone is enough to make even a short task complex.
	{"migration", +3, kw(`\bmigrat|\bmigra(r|ción|cion)?\b|\bport (it |the \w+ )?to\b`)},
	{"redesign or rewrite", +3, kw(`\bredesign|\bre-?architect|\brewrite|\boverhaul|from scratch|\brediseñ|\breescrib|desde cero`)},
	{"whole codebase", +3, kw(`(across|throughout) the (whole |entire )?(repo|codebase|project)|\b(whole|entire) (repo|codebase|project|app)|\bevery (package|module|file)\b|\btodo el (repo|repositorio|proyecto|código|codigo)\b|\btoda la (app|aplicación|aplicacion|base de código)|\ben todo el\b|\bcada (paquete|módulo|modulo|archivo)\b`)},
	{"architecture", +3, kw(`\barchitecture|\barchitectural|\barquitectura`)},
	{"small change", -2, kw(`\btypos?\b|\berrata|error tipográfico|error de dedo|\bone[- ]liner|\bone[- ]line\b|\ba single line\b|\buna (sola )?l[ií]nea\b|\brenam|\brenombr|\btiny\b|\btrivial\b|\bsmall\b|\bminor\b|\bquick fix|\bpequeñ|\bcambio menor|\bbump the version|\bsube la versión`)},
}

// Hit is a rule that applied to a task.
type Hit struct {
	Rule   string
	Points int
}

// Classify gives the tier of a task, with the rules that applied.
func Classify(f Features) (tier string, score int, hits []Hit) {
	for _, r := range tierRules {
		if r.when(f) {
			score += r.points
			hits = append(hits, Hit{r.name, r.points})
		}
	}
	switch {
	case score <= -2:
		return Simple, score, hits
	case score >= 2:
		return Complex, score, hits
	}
	return Medium, score, hits
}

// validTier: tier is one of Tiers.
func validTier(t string) bool {
	for _, x := range Tiers {
		if t == x {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")), "\n")
	return strings.TrimSpace(s)
}
