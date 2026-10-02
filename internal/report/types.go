package report

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/runs"
)

// Task types, in the order they are drawn.
var TypeOrder = []string{"review", "fix", "tests", "refactor", "docs", "feature", "other"}

// Words that give away each type, in priority order: a review that talks
// about «errors» is still a review. Tasks may be written in Spanish or
// English, so each rule matches both (the Spanish stems match tasks written
// in Spanish).
var typeRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"review", regexp.MustCompile(`\b(revis[aáo]|revisión|review|audit|analiza|analy[sz]e|busca errores|look for (bugs|errors)|find (bugs|errors)|solo lectura|read[- ]only)`)},
	{"fix", regexp.MustCompile(`\b(arregl|corrig|repar|bug|fix|falla|no funciona|broken|crash|does not work|doesn't work)`)},
	{"tests", regexp.MustCompile(`\b(pruebas?\b|tests?\b|testing\b|cobertura|coverage)`)},
	{"refactor", regexp.MustCompile(`\b(refactor|limpi|clean ?up|reorganiz|simplif|renombr|renam|código muerto|dead code)`)},
	{"docs", regexp.MustCompile(`\b(document|readme|comentari|comment)`)},
	{"feature", regexp.MustCompile(`\b(agreg|añad|implement|crea|nuev|haz que|escrib|que .* muestre|permit|add\b|adds\b|adding\b|new\b|make it|write|allow|support)`)},
}

// Rule phrases that do not say what the task is about (Spanish and English:
// tasks may be written in Spanish).
var reRulePhrase = regexp.MustCompile(`(?i)^\s*(solo puedes|no hagas|no modifiques|trabajas en|repositorio|programa go|es un programa|primero:|lee |si la herramienta|you may only|you can only|only modify|do not commit|don't commit|do not modify|don't modify|you work in|you are working in|repository|go program|is a program|first:|read |if the tool)`)

// Sentences are split at «. » or at a newline, not at every period (file
// names have periods).
var reSentences = regexp.MustCompile(`(\.\s+|\n+)`)

// Markers that introduce the actual goal after the rules ("objetivo:" matches
// tasks written in Spanish).
var goalMarkers = []string{"objetivo:", "objective:", "goal:"}

// TaskType classifies a run by what its task asks for (without the rules).
// Read-only runs, and those whose first sentence asks for a review, are
// reviews; otherwise the type with the most mentions wins (on a tie, the one
// with higher priority).
func TaskType(c history.Run) string {
	if c.ReadOnly {
		return "review"
	}
	txt := c.FullTask
	if txt == "" {
		txt = c.Task
	}
	parts := goalParts(txt)
	task := strings.ToLower(strings.Join(parts, ". "))
	if len(parts) > 0 && typeRules[0].re.MatchString(strings.ToLower(parts[0])) {
		return "review" // the task starts by asking for a review
	}
	best, n := "other", 0
	for _, r := range typeRules[1:] {
		if k := len(r.re.FindAllString(task, -1)); k > n {
			best, n = r.name, k
		}
	}
	return best
}

// Goal is what a task asks for, without its rule phrases ("you may only
// modify…", "do not commit…") and starting at its goal marker if it has one.
// It is "" when the task is only rules.
func Goal(task string) string { return strings.Join(goalParts(task), ". ") }

func goalParts(txt string) []string {
	lower := strings.ToLower(txt)
	for _, m := range goalMarkers {
		if i := strings.Index(lower, m); i >= 0 {
			txt = txt[i+len(m):]
			break
		}
	}
	var parts []string
	for _, f := range reSentences.Split(txt, -1) {
		if strings.TrimSpace(f) != "" && !reRulePhrase.MatchString(f) {
			parts = append(parts, strings.TrimSpace(f))
		}
	}
	return parts
}

// Cell of the agent × type matrix.
type Cell struct {
	Runs  int `json:"runs"`
	Clean int `json:"clean"` // done without breaking the task rules
}

// Matrix: per agent and type, how many runs and how many went well, since a
// date. Types are only the ones that appear, in TypeOrder.
type Matrix struct {
	Agents []string
	Types  []string
	Cells  map[string]map[string]*Cell
}

// ComputeMatrix builds the matrix; violated may be nil.
func ComputeMatrix(cs []history.Run, since time.Time, violated func(history.Run) bool) Matrix {
	m := Matrix{Cells: map[string]map[string]*Cell{}}
	present := map[string]bool{}
	for _, c := range cs {
		if c.Status == runs.Running || c.Status == runs.Skipped || (!since.IsZero() && c.Start.Before(since)) {
			continue
		}
		t := TaskType(c)
		present[t] = true
		if m.Cells[c.Agent] == nil {
			m.Cells[c.Agent] = map[string]*Cell{}
			m.Agents = append(m.Agents, c.Agent)
		}
		ce := m.Cells[c.Agent][t]
		if ce == nil {
			ce = &Cell{}
			m.Cells[c.Agent][t] = ce
		}
		ce.Runs++
		if c.Status == runs.Done && (violated == nil || !violated(c)) {
			ce.Clean++
		}
	}
	for _, t := range TypeOrder {
		if present[t] {
			m.Types = append(m.Types, t)
		}
	}
	sort.Strings(m.Agents)
	return m
}

// Savings: the work done by the cheap agents instead of Claude.
type Savings struct {
	Runs    int     // finished runs of agents other than claude
	Tokens  int64   // tokens they processed (the known ones)
	Dollars float64 // Tokens × Claude's price per million, if given (0 otherwise)
}

// ComputeSavings: pricePerMillion is what it would cost on Claude (dollars per
// million tokens, «claude_price» in panal.conf); 0 = not estimated.
func ComputeSavings(cs []history.Run, since time.Time, pricePerMillion float64) Savings {
	var a Savings
	for _, c := range cs {
		if c.Agent == "claude" || c.Status != runs.Done || (!since.IsZero() && c.Start.Before(since)) {
			continue
		}
		a.Runs++
		a.Tokens += c.Tokens
	}
	if pricePerMillion > 0 {
		a.Dollars = float64(a.Tokens) / 1e6 * pricePerMillion
	}
	return a
}
