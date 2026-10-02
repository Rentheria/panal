package router

import (
	"math"
	"math/rand/v2"
	"time"
)

// The learning, in plain words: every arm has, for each task type and tier,
// a count of successes and failures (recent runs weigh more). Those counts
// make a Beta distribution: "what fraction of such tasks this arm gets
// right", wide when there are few runs and narrow when there are many.
//
// When a cell has few runs, it borrows from coarser levels:
//
//	(arm, type, tier) ← (arm, tier) ← prior(tier, cost rank), its odds × lift(arm)
//
// lift is how the arm did over all its runs compared with what the prior
// expected for them, so an arm that beats expectations at simple tasks is
// trusted a bit more at medium ones, without its simple-task record
// pretending to be evidence about complex tasks.

// Beta is a Beta(Alpha, Beta) posterior over an arm's success rate.
type Beta struct{ A, B float64 }

// Mean of the distribution.
func (b Beta) Mean() float64 { return b.A / (b.A + b.B) }

// Strength is A+B: how many runs' worth of evidence it holds.
func (b Beta) Strength() float64 { return b.A + b.B }

// HalfLife: a run this old weighs half as much as one from today.
const HalfLife = 30 * 24 * time.Hour

// Weight of a run that ended at t, seen at now: 0.5^(age/HalfLife).
func Weight(t, now time.Time) float64 {
	age := now.Sub(t)
	if age < 0 {
		age = 0
	}
	return math.Pow(0.5, float64(age)/float64(HalfLife))
}

// Strengths of each level, in runs' worth of evidence: how much a cell with
// no runs of its own leans on the level above it.
const (
	PriorStrength = 3.0 // prior(tier, rank) × lift → (arm, tier)
	TierStrength  = 5.0 // (arm, tier) → (arm, type, tier), at most
	minLift       = 0.5
	maxLift       = 2.0
)

// Prior is the success rate expected of an arm before any run, by tier and
// cost rank (rank 0 = cheapest of n arms, n-1 = strongest). Cheap arms are
// expected to do well at simple tasks; complex tasks are expected to need
// the strong ones:
//
//	simple   0.80 → 0.85
//	medium   0.60 → 0.80
//	complex  0.35 → 0.75
//
// linearly from the cheapest arm to the strongest.
func Prior(tier string, rank, n int) float64 {
	r := 0.5
	if n > 1 {
		r = float64(rank) / float64(n-1)
	}
	switch tier {
	case Simple:
		return 0.80 + 0.05*r
	case Complex:
		return 0.35 + 0.40*r
	}
	return 0.60 + 0.20*r
}

// Counts are recency-weighted successes and failures, plus the raw number
// of runs behind them (for the explanation).
type Counts struct {
	S, F   float64 // weighted
	OK, N  int     // raw: successes and runs with a signal
	Recent [2]int  // raw, last 30 days: successes, runs
}

func (c *Counts) add(o Outcome, w float64, recent bool) {
	switch o {
	case Success:
		c.S += w
		c.OK++
		if recent {
			c.Recent[0]++
		}
	case Failure:
		c.F += w
	default:
		return
	}
	c.N++
	if recent {
		c.Recent[1]++
	}
}

// Estimate is the posterior of one arm for one task, with how it was built.
type Estimate struct {
	Arm      string
	Rank     int
	Prior    float64 // prior(tier, rank)
	Lift     float64 // the arm's record against the prior over all its runs
	Base     float64 // the prior with its odds multiplied by Lift
	Arm1     Counts  // the arm, every type and tier
	ArmTier  Counts  // the arm, this tier
	Cell     Counts  // the arm, this type and tier
	TierPost Beta    // (arm, tier)
	Post     Beta    // (arm, type, tier): what is sampled
}

// table is the learned evidence: counts per arm, per (arm, tier) and per
// (arm, type, tier), and for the lift, what the prior expected of each arm.
type table struct {
	arm      map[string]*Counts
	armTier  map[[2]string]*Counts
	cell     map[[3]string]*Counts
	expected map[string]float64 // Σ weight × prior over the arm's runs with a signal
}

// Table is the learned evidence (see Learn).
type Table = table

// Learn builds the table from evidence (already resolved to pool arms with
// ResolveArms); rank gives an arm's cost rank and the pool size.
func Learn(ev []Evidence, now time.Time, rank func(arm string) (int, int)) *Table {
	return learn(ev, now, rank)
}

// Estimate is the posterior of arm (cost rank of n) for a task of type and tier.
func (t *Table) Estimate(arm string, rank, n int, typ, tier string) Estimate {
	return t.estimate(arm, rank, n, typ, tier)
}

// learn builds the table from evidence. rank gives each arm's cost rank in
// the pool (arms outside the pool get the middle).
func learn(ev []Evidence, now time.Time, rank func(arm string) (int, int)) *table {
	t := &table{
		arm: map[string]*Counts{}, armTier: map[[2]string]*Counts{},
		cell: map[[3]string]*Counts{}, expected: map[string]float64{},
	}
	get := func(m map[string]*Counts, k string) *Counts {
		if m[k] == nil {
			m[k] = &Counts{}
		}
		return m[k]
	}
	for _, e := range ev {
		if e.Outcome == NoSignal {
			continue
		}
		w := Weight(e.At, now)
		recent := now.Sub(e.At) <= 30*24*time.Hour
		get(t.arm, e.Arm).add(e.Outcome, w, recent)
		kt := [2]string{e.Arm, e.Tier}
		if t.armTier[kt] == nil {
			t.armTier[kt] = &Counts{}
		}
		t.armTier[kt].add(e.Outcome, w, recent)
		kc := [3]string{e.Arm, e.Type, e.Tier}
		if t.cell[kc] == nil {
			t.cell[kc] = &Counts{}
		}
		t.cell[kc].add(e.Outcome, w, recent)
		r, n := rank(e.Arm)
		t.expected[e.Arm] += w * Prior(e.Tier, r, n)
	}
	return t
}

func val(c *Counts) Counts {
	if c == nil {
		return Counts{}
	}
	return *c
}

// estimate is the posterior of arm (cost rank of n) for a task of type and tier.
func (t *table) estimate(arm string, rank, n int, typ, tier string) Estimate {
	e := Estimate{Arm: arm, Rank: rank, Prior: Prior(tier, rank, n)}
	e.Arm1 = val(t.arm[arm])
	e.ArmTier = val(t.armTier[[2]string{arm, tier}])
	e.Cell = val(t.cell[[3]string{arm, typ, tier}])

	// lift = (successes + 1) / (expected successes + 1), clamped to
	// [0.5, 2]: with no runs it is 1, and it moves slowly. It multiplies the
	// prior's odds, so the result stays a probability.
	e.Lift = (e.Arm1.S + 1) / (t.expected[arm] + 1)
	e.Lift = math.Min(maxLift, math.Max(minLift, e.Lift))
	odds := e.Prior / (1 - e.Prior) * e.Lift
	e.Base = odds / (1 + odds)
	m0 := e.Base

	// (arm, tier): the prior as PriorStrength pseudo-runs plus its own runs.
	e.TierPost = Beta{PriorStrength*m0 + e.ArmTier.S, PriorStrength*(1-m0) + e.ArmTier.F}
	// (arm, type, tier): (arm, tier)'s mean as up to TierStrength pseudo-runs
	// (never more than the evidence it has) plus the cell's own runs.
	k := math.Min(TierStrength, e.TierPost.Strength())
	m1 := e.TierPost.Mean()
	e.Post = Beta{k*m1 + e.Cell.S, k*(1-m1) + e.Cell.F}
	return e
}

// ------------------------------------------------------------ sampling --

// sampleBeta draws from Beta(a, b) as X/(X+Y) with X~Gamma(a), Y~Gamma(b).
func sampleBeta(r *rand.Rand, b Beta) float64 {
	x := sampleGamma(r, b.A)
	y := sampleGamma(r, b.B)
	if x+y == 0 {
		return b.Mean()
	}
	return x / (x + y)
}

// sampleGamma draws from Gamma(shape, 1) (Marsaglia and Tsang, 2000).
func sampleGamma(r *rand.Rand, shape float64) float64 {
	if shape <= 0 {
		return 0
	}
	if shape < 1 {
		// Gamma(a) = Gamma(a+1) · U^(1/a)
		return sampleGamma(r, shape+1) * math.Pow(r.Float64(), 1/shape)
	}
	d := shape - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := r.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := r.Float64()
		if u < 1-0.0331*x*x*x*x || math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}
