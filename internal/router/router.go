// Package router picks which agent, model and effort to delegate a task to,
// and learns from how past runs went. It is pure Go: no model runs here.
//
//   - Arms are chain links ("cli:model[:effort]") from a pool listed
//     cheapest → strongest; an arm's cost rank is its place in the pool.
//   - A task gets a type (report.TaskType) and a tier (simple, medium,
//     complex: tier.go, one table of rules).
//   - Every past run is a success, a failure or no signal (evidence.go), and
//     each (arm, type, tier) keeps a recency-weighted Beta posterior with a
//     fallback to coarser levels when it has few runs (posterior.go).
//   - The choice is Thompson sampling with a cost penalty: draw a success
//     rate from each arm's posterior and take the cheapest arm whose draw is
//     within Margin of the best one. Uncertain arms sometimes draw high, so
//     they get tried (exploration); as runs pile up the posteriors narrow
//     and exploration fades on its own.
//   - Optionally an external command classifies the task (external.go).
package router

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
)

// Margin: an arm whose sampled success is within this of the best one is
// as good, and the cheapest of those wins.
const Margin = 0.08

// explorationDraws: how many extra Thompson draws estimate how often the
// router explores for this task.
const explorationDraws = 200

// Request is a task to route.
type Request struct {
	Task     string
	Dir      string
	ReadOnly bool
	// Pool is the arms, cheapest first, as chain links ("cli:model[:effort]").
	Pool []string
	// Evidence is what the past runs say (Gather).
	Evidence []Evidence
	// OutOfQuota: CLIs known to be out of quota right now. Their arms go to
	// the end of the chain.
	OutOfQuota map[string]bool
	Now        time.Time
	Rand       *rand.Rand // nil = seeded from the clock
	External   *External  // nil = only the built-in router
}

// Decision is the router's answer.
type Decision struct {
	Type, Tier string
	Score      int   // the tier rules' points
	Hits       []Hit // the tier rules that applied
	Estimates  []Estimate
	Draws      map[string]float64 // the Thompson draw of each arm
	Chain      []string           // best first; the rest is the fallback
	Favorite   string             // the first arm by posterior mean (no exploration)
	Explored   bool               // Chain[0] is not the favorite: this pick explores
	ExploreP   float64            // how often a draw picks something other than the favorite
	External   string             // what the external router did ("" = not configured)
	Picked     bool               // the external router picked Chain[0]
	Why        string             // one line, in English
}

// Decide routes a task.
func Decide(q Request) (Decision, error) {
	if len(q.Pool) == 0 {
		return Decision{}, fmt.Errorf("the pool is empty")
	}
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	rng := q.Rand
	if rng == nil {
		seed := uint64(time.Now().UnixNano())
		rng = rand.New(rand.NewPCG(seed, seed>>1|1))
	}

	f := Extract(q.Task, q.ReadOnly)
	var d Decision
	d.Type = f.Type
	d.Tier, d.Score, d.Hits = Classify(f)

	extArm := ""
	if q.External != nil {
		ans, err := q.External.Ask(q, d)
		switch {
		case err != nil:
			d.External = "external router failed (" + err.Error() + "), used the built-in one"
		default:
			var notes []string
			if ans.Tier != "" && ans.Tier != d.Tier {
				notes = append(notes, "tier "+ans.Tier)
				d.Tier = ans.Tier
			}
			if ans.Arm != "" {
				extArm = ans.Arm
				notes = append(notes, "arm "+ans.Arm)
			}
			if len(notes) == 0 {
				d.External = "external router agreed"
			} else {
				d.External = "external router: " + strings.Join(notes, ", ")
			}
		}
	}

	rankOf := map[string]int{}
	for i, a := range q.Pool {
		if _, dup := rankOf[a]; !dup {
			rankOf[a] = i
		}
	}
	n := len(q.Pool)
	rank := func(arm string) (int, int) {
		if r, ok := rankOf[arm]; ok {
			return r, n
		}
		return (n - 1) / 2, n
	}
	t := learn(ResolveArms(q.Evidence, q.Pool), q.Now, rank)
	seen := map[string]bool{}
	for i, a := range q.Pool {
		if seen[a] {
			continue
		}
		seen[a] = true
		d.Estimates = append(d.Estimates, t.estimate(a, i, n, d.Type, d.Tier))
	}

	out := func(arm string) bool { return q.OutOfQuota[cliOf(arm)] }
	d.Draws = map[string]float64{}
	for _, e := range d.Estimates {
		d.Draws[e.Arm] = sampleBeta(rng, e.Post)
	}
	d.Chain = order(d.Estimates, func(e Estimate) float64 { return d.Draws[e.Arm] }, out)
	fav := order(d.Estimates, func(e Estimate) float64 { return e.Post.Mean() }, out)
	d.Favorite = fav[0]
	d.Explored = d.Chain[0] != d.Favorite
	differ := 0
	for i := 0; i < explorationDraws; i++ {
		draws := map[string]float64{}
		for _, e := range d.Estimates {
			draws[e.Arm] = sampleBeta(rng, e.Post)
		}
		if order(d.Estimates, func(e Estimate) float64 { return draws[e.Arm] }, out)[0] != d.Favorite {
			differ++
		}
	}
	d.ExploreP = float64(differ) / explorationDraws

	if extArm != "" {
		if _, ok := rankOf[extArm]; ok {
			d.Chain = moveFirst(d.Chain, extArm)
			d.Explored, d.Picked = false, true
		} else {
			d.External += " (not in the pool, ignored)"
		}
	}
	d.Why = explain(d, q.OutOfQuota)
	return d, nil
}

// order ranks the arms: repeatedly, among those left, the cheapest one
// whose value is within Margin of the best. Arms whose CLI is out of quota
// go last, in the same order.
func order(es []Estimate, value func(Estimate) float64, out func(string) bool) []string {
	left := append([]Estimate(nil), es...)
	sort.SliceStable(left, func(i, j int) bool { return left[i].Rank < left[j].Rank })
	var ranked []string
	for len(left) > 0 {
		best := math.Inf(-1)
		for _, e := range left {
			best = math.Max(best, value(e))
		}
		for i, e := range left {
			if value(e) >= best-Margin {
				ranked = append(ranked, e.Arm)
				left = append(left[:i], left[i+1:]...)
				break
			}
		}
	}
	var ok, later []string
	for _, a := range ranked {
		if out(a) {
			later = append(later, a)
		} else {
			ok = append(ok, a)
		}
	}
	return append(ok, later...)
}

// ResolveArms maps each run to the pool arm it is evidence for: its own arm
// when that is in the pool; otherwise, when the pool has its CLI bare
// ("codex": the CLI's own default model and effort), that bare arm, since a
// run with a model the pool doesn't name is the closest thing to it. Other
// runs keep their arm and only show in the stats.
func ResolveArms(ev []Evidence, pool []string) []Evidence {
	in := map[string]bool{}
	for _, a := range pool {
		in[a] = true
	}
	out := make([]Evidence, len(ev))
	for i, e := range ev {
		if !in[e.Arm] && in[e.CLI] {
			e.Arm = e.CLI
		}
		out[i] = e
	}
	return out
}

func moveFirst(chain []string, arm string) []string {
	out := []string{arm}
	for _, a := range chain {
		if a != arm {
			out = append(out, a)
		}
	}
	return out
}

func cliOf(arm string) string {
	cli, _, _ := strings.Cut(arm, ":")
	return cli
}

// Estimate returns the estimate of arm, if it is in the decision.
func (d Decision) Estimate(arm string) (Estimate, bool) {
	for _, e := range d.Estimates {
		if e.Arm == arm {
			return e, true
		}
	}
	return Estimate{}, false
}

// explain is the one-line reason, e.g.
//
//	auto: codex:gpt-6-luna:low — fix · medium · 9/10 ok in the last 30 days (exploring 1 in 10)
func explain(d Decision, outOfQuota map[string]bool) string {
	first := d.Chain[0]
	e, _ := d.Estimate(first)
	s := fmt.Sprintf("auto: %s — %s · %s · %s", first, d.Type, d.Tier, evidenceText(e, d.Tier))
	switch {
	case d.Picked:
		// The external router picked it; that is said at the end.
	case d.Explored:
		s += " (exploring; the favorite is " + d.Favorite + ")"
	case d.ExploreP >= 0.005:
		s += " (exploring " + oneIn(d.ExploreP) + ")"
	}
	if outOfQuota[cliOf(first)] {
		s += " · every arm is out of quota now"
	} else {
		var skipped []string
		seen := map[string]bool{}
		for _, e := range d.Estimates {
			if cli := cliOf(e.Arm); outOfQuota[cli] && !seen[cli] {
				seen[cli] = true
				skipped = append(skipped, cli)
			}
		}
		if len(skipped) > 0 {
			sort.Strings(skipped)
			s += " · out of quota now: " + strings.Join(skipped, ", ")
		}
	}
	if d.External != "" {
		s += " · " + d.External
	}
	return s
}

// evidenceText: the record behind an estimate, at the most specific level
// that has runs.
func evidenceText(e Estimate, tier string) string {
	switch {
	case e.Cell.N > 0:
		return countsText(e.Cell, "")
	case e.ArmTier.N > 0:
		return countsText(e.ArmTier, " on "+tier+" tasks")
	case e.Arm1.N > 0:
		return countsText(e.Arm1, " on any task")
	}
	return fmt.Sprintf("no runs yet, prior %.0f%%", 100*e.Base)
}

func countsText(c Counts, where string) string {
	if c.Recent[1] > 0 {
		return fmt.Sprintf("%d/%d ok%s in the last 30 days", c.Recent[0], c.Recent[1], where)
	}
	return fmt.Sprintf("%d/%d ok%s, older than 30 days", c.OK, c.N, where)
}

// oneIn: 0.1 → "1 in 10".
func oneIn(p float64) string {
	if p >= 0.5 {
		return "often"
	}
	return fmt.Sprintf("1 in %d", int(math.Round(1/p)))
}
