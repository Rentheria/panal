package models

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The user's rules over the discovered models, from panal.conf:
//
//	models           = codex:gpt-6-luna* agy:gemini-3.8-flash-* agy:gemini-3.1-pro-*
//	exclude          = agy:claude-*
//	pool_max_per_cli = 4
//
// A pattern is "cli[:model[:effort]]" with * and ? as wildcards, case
// insensitive; * also matches "/" (opencode's provider/model). Without an
// effort part, a codex or opencode model counts with the efforts low,
// medium and high (DefaultEfforts); "codex:gpt-6-luna:*" allows every
// effort the model has, "codex:gpt-6-luna:xhigh" just that one. agy's
// effort is part of its model id, so its patterns match the id.
//
//   - models (allow): only arms matching one of these. Unset = every
//     discovered model.
//   - exclude (deny): never an arm matching one of these. Without an effort
//     part the whole model goes ("codex:gpt-6.1-*"); with one, only those
//     efforts ("codex:*:high").
//   - Models the catalog hides or retires stay out unless an allow pattern
//     names them exactly (no wildcard in the model part).
//   - pool_max_per_cli caps how many arms of each CLI an auto pool keeps
//     (default 4): the cheapest, the strongest and evenly spaced ones in
//     between, so exploring does not spend on dozens of models.

// DefaultMaxPerCLI is the cap when pool_max_per_cli is not set.
const DefaultMaxPerCLI = 4

// DefaultEfforts are the efforts a codex or opencode model gets in an auto
// pool when the pattern that allows it says none.
var DefaultEfforts = []string{"low", "medium", "high"}

// ChainEfforts are the effort words a chain link accepts after the model
// (panal delegate parses "cli:model:effort" with this same list).
var ChainEfforts = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

// Rules are the user's allow and deny patterns and the per-CLI cap.
type Rules struct {
	Allow     []string
	Exclude   []string
	MaxPerCLI int // 0 = DefaultMaxPerCLI
}

// ParseRules reads the config values (space- or comma-separated patterns;
// the cap as a number). A bad cap is reported and the default kept.
func ParseRules(allow, exclude, maxPerCLI string) (Rules, error) {
	r := Rules{Allow: fields(allow), Exclude: fields(exclude)}
	if v := strings.TrimSpace(maxPerCLI); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return r, fmt.Errorf("pool_max_per_cli = %s is not a number of 1 or more; using %d", v, DefaultMaxPerCLI)
		}
		r.MaxPerCLI = n
	}
	return r, nil
}

func fields(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
}

// Cap is the effective per-CLI cap.
func (r Rules) Cap() int {
	if r.MaxPerCLI > 0 {
		return r.MaxPerCLI
	}
	return DefaultMaxPerCLI
}

// Arm is a chain link built from the catalog, with its heuristic cost.
type Arm struct {
	CLI, Model, Effort string
	Score              float64
	Why                string // how the score was made
	Priority           int    // the catalog's own order, for ties
}

// String is the chain link: "cli:model[:effort]".
func (a Arm) String() string {
	s := a.CLI
	if a.Model != "" {
		s += ":" + a.Model
	}
	if a.Effort != "" {
		if a.Model == "" {
			s += ":"
		}
		s += ":" + a.Effort
	}
	return s
}

// ParseArm splits a chain link the way panal delegate does: the last
// ":word" is the effort only when it is one of ChainEfforts.
func ParseArm(link string) Arm {
	cli, rest, _ := strings.Cut(strings.TrimSpace(link), ":")
	a := Arm{CLI: strings.ToLower(cli), Model: rest}
	if i := strings.LastIndex(rest, ":"); i >= 0 && isChainEffort(rest[i+1:]) {
		a.Model, a.Effort = rest[:i], strings.ToLower(rest[i+1:])
	} else if isChainEffort(rest) && !strings.Contains(rest, "/") {
		a.Model, a.Effort = "", strings.ToLower(rest)
	}
	return a
}

func isChainEffort(s string) bool {
	for _, e := range ChainEfforts {
		if strings.EqualFold(s, e) {
			return true
		}
	}
	return false
}

// pattern is a parsed allow or deny pattern.
type pattern struct {
	raw, cli, model, effort string
	hasEffort               bool
}

func parsePattern(s string) pattern {
	s = strings.ToLower(strings.TrimSpace(s))
	p := pattern{raw: s, model: "*"}
	cli, rest, ok := strings.Cut(s, ":")
	p.cli = cli
	if !ok {
		return p
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 && effortGlob(rest[i+1:]) {
		p.model, p.effort, p.hasEffort = rest[:i], rest[i+1:], true
	} else {
		p.model = rest
	}
	if p.model == "" {
		p.model = "*"
	}
	return p
}

// effortGlob: s reads as an effort or a wildcard over efforts.
func effortGlob(s string) bool {
	if s == "" {
		return false
	}
	if _, ok := effortCost[s]; ok {
		return true
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r == '*' || r == '?') {
			return false
		}
	}
	return strings.ContainsAny(s, "*?")
}

// literal: the pattern names one model exactly.
func (p pattern) literal() bool { return !strings.ContainsAny(p.model, "*?") }

func (p pattern) matchModel(cli, model string) bool {
	return glob(p.cli, strings.ToLower(cli)) && glob(p.model, strings.ToLower(model))
}

// matchEffort: for an allow pattern without an effort part, the default
// efforts (and no effort at all).
func (p pattern) matchEffort(effort string, deny bool) bool {
	if p.hasEffort {
		return glob(p.effort, strings.ToLower(effort))
	}
	if deny || effort == "" {
		return true
	}
	for _, e := range DefaultEfforts {
		if e == effort {
			return true
		}
	}
	return false
}

// glob matches s against a pattern with * (any run, "/" included) and ?.
func glob(p, s string) bool {
	if p == "" {
		return s == ""
	}
	// Iterative wildcard match with backtracking to the last *.
	pi, si, star, mark := 0, 0, -1, 0
	pr, sr := []rune(p), []rune(s)
	for si < len(sr) {
		switch {
		case pi < len(pr) && (pr[pi] == '?' || pr[pi] == sr[si]):
			pi++
			si++
		case pi < len(pr) && pr[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pr) && pr[pi] == '*' {
		pi++
	}
	return pi == len(pr)
}

// Verdict is what the rules say about one model of the catalog.
type Verdict struct {
	Model Model
	Arms  []Arm  // the arms it gives (empty when it gives none)
	Why   string // why it gives none: "not in models", "exclude: agy:claude-*", "hidden", "retiring: …"
}

// Judge applies the rules to every model of one CLI.
func (r Rules) Judge(cat Catalog, cli string) []Verdict {
	e := cat.Get(cli)
	if e == nil {
		return nil
	}
	allow := make([]pattern, 0, len(r.Allow))
	for _, s := range r.Allow {
		allow = append(allow, parsePattern(s))
	}
	deny := make([]pattern, 0, len(r.Exclude))
	for _, s := range r.Exclude {
		deny = append(deny, parsePattern(s))
	}
	var out []Verdict
	for _, m := range e.Models {
		v := Verdict{Model: m}
		var hits []pattern
		if len(allow) == 0 {
			hits = []pattern{{cli: "*", model: "*"}}
		}
		for _, p := range allow {
			if p.matchModel(cli, m.ID) {
				hits = append(hits, p)
			}
		}
		if len(hits) == 0 {
			v.Why = "not in models"
			out = append(out, v)
			continue
		}
		if m.Hidden || m.Retiring != "" {
			named := false
			for _, p := range hits {
				named = named || p.literal()
			}
			if !named {
				if m.Hidden {
					v.Why = "hidden in " + cli + "'s catalog"
				} else {
					v.Why = "retiring: " + m.Retiring
				}
				out = append(out, v)
				continue
			}
		}
		denied := func(effort string) string {
			for _, p := range deny {
				if p.matchModel(cli, m.ID) && p.matchEffort(effort, true) {
					return p.raw
				}
			}
			return ""
		}
		allowed := func(effort string) bool {
			for _, p := range hits {
				if p.matchEffort(effort, false) {
					return true
				}
			}
			return false
		}
		efforts := chainEffortsOf(m)
		var why string
		for _, ef := range efforts {
			if !allowed(ef) {
				continue
			}
			if d := denied(ef); d != "" {
				why = "exclude: " + d
				continue
			}
			v.Arms = append(v.Arms, r.arm(cli, m, ef))
		}
		// No effort of its own (agy), or none of its efforts allowed by a
		// pattern without an effort part: the model at the CLI's default.
		if len(v.Arms) == 0 && why == "" && allowed("") {
			if d := denied(""); d != "" {
				why = "exclude: " + d
			} else if len(efforts) == 0 || !anyHasEffort(hits) {
				v.Arms = append(v.Arms, r.arm(cli, m, ""))
			}
		}
		if len(v.Arms) == 0 {
			if why == "" {
				why = "no allowed effort"
			}
			v.Why = why
		}
		out = append(out, v)
	}
	return out
}

func anyHasEffort(ps []pattern) bool {
	for _, p := range ps {
		if p.hasEffort {
			return true
		}
	}
	return false
}

func (r Rules) arm(cli string, m Model, effort string) Arm {
	s, why := Cost(cli, m, effort)
	return Arm{CLI: cli, Model: m.ID, Effort: effort, Score: s, Why: why, Priority: m.Priority}
}

// chainEffortsOf: the model's efforts that a chain link can carry.
func chainEffortsOf(m Model) []string {
	var out []string
	for _, e := range m.Efforts {
		if isChainEffort(e) {
			out = append(out, strings.ToLower(e))
		}
	}
	return out
}

// Allowed is every arm the rules allow, over every CLI, cheapest first.
func (r Rules) Allowed(cat Catalog) []Arm {
	var out []Arm
	for _, cli := range cat.Names() {
		for _, v := range r.Judge(cat, cli) {
			out = append(out, v.Arms...)
		}
	}
	SortArms(out)
	return out
}

// AutoPool is `pool = auto`: the allowed arms, without older versions of
// the same model line (Newest), at most Cap() per CLI (Spread), cheapest
// first.
func (r Rules) AutoPool(cat Catalog) []Arm {
	var out []Arm
	for _, cli := range cat.Names() {
		var arms []Arm
		for _, v := range r.Judge(cat, cli) {
			arms = append(arms, v.Arms...)
		}
		arms = Newest(arms)
		SortArms(arms)
		out = append(out, Spread(arms, r.Cap())...)
	}
	SortArms(out)
	return out
}

// Newest drops an arm when the same model line in a newer version, at the
// same effort, is also there: gemini-3.6-flash-low and gemini-3.7-flash-low
// go when gemini-3.8-flash-low is allowed, codex:gpt-5.6-luna:low when
// codex:gpt-6-luna:low is. Arms whose id has no version are kept.
func Newest(arms []Arm) []Arm {
	type best struct {
		v float64
		i int
	}
	line := func(a Arm) (string, float64, bool) {
		v := version(a.Model)
		if v == "" {
			return "", 0, false
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "", 0, false
		}
		i := strings.Index(a.Model, v)
		return strings.ToLower(a.CLI + ":" + a.Model[:i] + "#" + a.Model[i+len(v):] + ":" + a.Effort), f, true
	}
	top := map[string]best{}
	for i, a := range arms {
		if k, v, ok := line(a); ok {
			if b, seen := top[k]; !seen || v > b.v {
				top[k] = best{v, i}
			}
		}
	}
	var out []Arm
	for i, a := range arms {
		if k, _, ok := line(a); ok && top[k].i != i {
			continue
		}
		out = append(out, a)
	}
	return out
}

// SortArms orders arms cheapest first: by score, then the catalog's
// priority, then the link.
func SortArms(a []Arm) {
	sort.SliceStable(a, func(i, j int) bool {
		if a[i].Score != a[j].Score {
			return a[i].Score < a[j].Score
		}
		if a[i].Priority != a[j].Priority {
			return a[i].Priority > a[j].Priority
		}
		return a[i].String() < a[j].String()
	})
}

// Spread keeps at most n of arms (sorted cheapest first): the cheapest, the
// strongest and evenly spaced ones in between.
func Spread(arms []Arm, n int) []Arm {
	if n <= 0 || len(arms) <= n {
		return arms
	}
	if n == 1 {
		return arms[:1]
	}
	var out []Arm
	last := -1
	for i := 0; i < n; i++ {
		k := (i*(len(arms)-1) + (n-1)/2) / (n - 1)
		if k <= last {
			k = last + 1
		}
		out = append(out, arms[k])
		last = k
	}
	return out
}

// Check looks at an explicit pool against the catalog and the rules and
// returns warnings (never errors: the pool is used as written): models the
// catalog doesn't have, efforts the model doesn't take, models it hides or
// retires, and arms the user's own models/exclude rules leave out.
func (r Rules) Check(cat Catalog, pool []string) []string {
	var out []string
	inRules := map[string]bool{}
	for _, a := range r.Allowed(cat) {
		inRules[a.String()] = true
	}
	for _, link := range pool {
		a := ParseArm(link)
		if a.Model == "" || !cat.Known(a.CLI) {
			continue
		}
		m, ok := cat.Find(a.CLI, a.Model)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s: %s's catalog has no model %q", link, a.CLI, a.Model))
			continue
		case a.Effort != "" && len(m.Efforts) > 0 && !contains(m.Efforts, a.Effort):
			out = append(out, fmt.Sprintf("%s: %s takes the efforts %s", link, m.ID, strings.Join(m.Efforts, ", ")))
		case m.Retiring != "":
			out = append(out, fmt.Sprintf("%s: %s", link, m.Retiring))
		case m.Hidden:
			out = append(out, fmt.Sprintf("%s: hidden in %s's catalog", link, a.CLI))
		}
		if (len(r.Allow) > 0 || len(r.Exclude) > 0) && !inRules[a.String()] {
			out = append(out, fmt.Sprintf("%s: in the pool, but models/exclude leave it out", link))
		}
	}
	return out
}

// Suggest is what an auto pool would add to an explicit one: its arms that
// the pool doesn't have.
func (r Rules) Suggest(cat Catalog, pool []string) []Arm {
	in := map[string]bool{}
	for _, l := range pool {
		in[strings.ToLower(ParseArm(l).String())] = true
	}
	var out []Arm
	for _, a := range r.AutoPool(cat) {
		if !in[strings.ToLower(a.String())] {
			out = append(out, a)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
