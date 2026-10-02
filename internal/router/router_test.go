package router

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/feedback"
	"github.com/AlbertoVasquezR/panal/internal/history"
	"github.com/AlbertoVasquezR/panal/internal/runs"
)

// The test binary doubles as a fake external router: with
// PANAL_FAKE_ROUTER set it answers the way that variable says and exits.
func TestMain(m *testing.M) {
	switch os.Getenv("PANAL_FAKE_ROUTER") {
	case "":
		os.Exit(m.Run())
	case "simple":
		fmt.Println(`{"tier": "simple"}`)
	case "arm":
		fmt.Println(`{"tier": "complex", "arm": "agy"}`)
	case "garbage":
		fmt.Println(`I think it is a simple task`)
	case "badtier":
		fmt.Println(`{"tier": "huge"}`)
	case "fail":
		fmt.Fprintln(os.Stderr, "model not found")
		os.Exit(3)
	case "slow":
		time.Sleep(10 * time.Second)
		fmt.Println(`{"tier": "simple"}`)
	}
	os.Exit(0)
}

func seeded(n uint64) *rand.Rand { return rand.New(rand.NewPCG(n, n+1)) }

var now = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// ------------------------------------------------------------- tiers --

func TestTiersEnglishAndSpanish(t *testing.T) {
	long := strings.Repeat("Keep the existing behaviour and add tests for every case you touch. ", 30)
	cases := []struct {
		task     string
		readOnly bool
		want     string
	}{
		// simple
		{"Fix the typo in README", false, Simple},
		{"Corrige la errata del README", false, Simple},
		{"Rename pager.Next to pager.Forward", false, Simple},
		{"Renombra la función Siguiente a Avanzar", false, Simple},
		{"Change one line in config.go: the default timeout is 30s", false, Simple},
		{"Cambia una línea en config.go: el tiempo por defecto es 30 s", false, Simple},
		{"Review internal/ui and list the bugs", true, Simple},
		{"Revisa internal/ui y lista los errores", true, Simple},
		// medium
		{"Fix the off-by-one in the pager; add a test", false, Medium},
		{"Arregla el error de paginación y agrega una prueba", false, Medium},
		{"Add a -json flag to panal -once", false, Medium},
		{"Agrega una opción -json a panal -once", false, Medium},
		// complex
		{"Migrate the whole repo from Bubble Tea v1 to v2", false, Complex},
		{"Migra todo el repo de Bubble Tea v1 a v2", false, Complex},
		{"Redesign the architecture of internal/ui", false, Complex},
		{"Rediseña la arquitectura de internal/ui", false, Complex},
		{"Rewrite the readers from scratch", false, Complex},
		{"Reescribe los lectores desde cero", false, Complex},
		{"Implement the new history view. " + long, false, Complex},
		{"You may only modify internal/ui/, internal/state/ and cmd/. Implement live reload of the config across the whole project", false, Complex},
	}
	for _, c := range cases {
		f := Extract(c.task, c.readOnly)
		got, score, hits := Classify(f)
		if got != c.want {
			t.Errorf("%q: %s (score %d, %v), want %s", c.task, got, score, hits, c.want)
		}
	}
}

func TestFeatures(t *testing.T) {
	f := Extract("You may only modify a.go, b.go and internal/ui/.\n1. one\n2. two\n3. three\n4. four", false)
	if f.Files != 2 || f.Dirs != 1 || f.Steps != 4 {
		t.Errorf("features: %+v", f)
	}
	if f := Extract("Solo puedes modificar README.md. Corrige el texto.", false); f.Files != 1 || f.Dirs != 0 {
		t.Errorf("one allowed file (es): %+v", f)
	}
}

// --------------------------------------------------------- posterior --

func TestPrior(t *testing.T) {
	if p := Prior(Simple, 0, 3); math.Abs(p-0.80) > 1e-9 {
		t.Errorf("cheapest simple: %v", p)
	}
	if p := Prior(Complex, 2, 3); math.Abs(p-0.75) > 1e-9 {
		t.Errorf("strongest complex: %v", p)
	}
	if Prior(Complex, 0, 3) >= Prior(Complex, 2, 3) {
		t.Error("strong arms must be expected to do better at complex tasks")
	}
	if Prior(Simple, 0, 1) != Prior(Simple, 0, 1) || Prior(Medium, 0, 1) != 0.70 {
		t.Errorf("a lone arm is a middle one: %v", Prior(Medium, 0, 1))
	}
}

func TestWeightHalfLife(t *testing.T) {
	if w := Weight(now, now); w != 1 {
		t.Errorf("today: %v", w)
	}
	if w := Weight(now.Add(-HalfLife), now); math.Abs(w-0.5) > 1e-9 {
		t.Errorf("one half-life ago: %v", w)
	}
	if w := Weight(now.Add(time.Hour), now); w != 1 {
		t.Errorf("the future counts as now: %v", w)
	}
}

func ev(arm, typ, tier string, o Outcome, ago time.Duration) Evidence {
	return Evidence{Arm: arm, CLI: cliOf(arm), Type: typ, Tier: tier, Outcome: o, At: now.Add(-ago)}
}

func rankIn(pool []string) func(string) (int, int) {
	return func(a string) (int, int) {
		for i, p := range pool {
			if p == a {
				return i, len(pool)
			}
		}
		return 0, len(pool)
	}
}

func TestPosteriorMath(t *testing.T) {
	pool := []string{"cheap", "strong"}
	// No runs: the cell is the prior, PriorStrength runs' worth.
	e := learn(nil, now, rankIn(pool)).estimate("cheap", 0, 2, "fix", Medium)
	if e.Lift != 1 || math.Abs(e.Post.Mean()-0.60) > 1e-9 || math.Abs(e.Post.Strength()-PriorStrength) > 1e-9 {
		t.Errorf("cold start: %+v", e)
	}
	// Three fresh successes in the cell: Beta(k·m + 3, k·(1−m)).
	var es []Evidence
	for i := 0; i < 3; i++ {
		es = append(es, ev("cheap", "fix", Medium, Success, 0))
	}
	e = learn(es, now, rankIn(pool)).estimate("cheap", 0, 2, "fix", Medium)
	lift := 4.0 / (3*0.60 + 1) // (S+1)/(E+1)
	odds := 0.60 / 0.40 * lift
	m0 := odds / (1 + odds)
	tierPost := Beta{PriorStrength*m0 + 3, PriorStrength * (1 - m0)}
	k := math.Min(TierStrength, tierPost.Strength())
	want := Beta{k*tierPost.Mean() + 3, k * (1 - tierPost.Mean())}
	if math.Abs(e.Lift-lift) > 1e-9 || math.Abs(e.Post.A-want.A) > 1e-9 || math.Abs(e.Post.B-want.B) > 1e-9 {
		t.Errorf("got lift %v post %+v, want lift %v post %+v", e.Lift, e.Post, lift, want)
	}
	// An old run weighs less: a failure one half-life ago counts half.
	e = learn([]Evidence{ev("cheap", "fix", Medium, Failure, HalfLife)}, now, rankIn(pool)).estimate("cheap", 0, 2, "fix", Medium)
	if math.Abs(e.Cell.F-0.5) > 1e-9 || e.Cell.N != 1 {
		t.Errorf("recency: %+v", e.Cell)
	}
	// No signal is ignored.
	e = learn([]Evidence{ev("cheap", "fix", Medium, NoSignal, 0)}, now, rankIn(pool)).estimate("cheap", 0, 2, "fix", Medium)
	if e.Arm1.N != 0 {
		t.Errorf("no signal counted: %+v", e.Arm1)
	}
}

func TestHierarchicalFallback(t *testing.T) {
	pool := []string{"cheap", "strong"}
	var es []Evidence
	// The cheap arm failed four medium fixes: a docs task at medium (no
	// runs of its own) inherits that from (arm, tier).
	for i := 0; i < 4; i++ {
		es = append(es, ev("cheap", "fix", Medium, Failure, 0))
	}
	tb := learn(es, now, rankIn(pool))
	docs := tb.estimate("cheap", 0, 2, "docs", Medium)
	cold := learn(nil, now, rankIn(pool)).estimate("cheap", 0, 2, "docs", Medium)
	if docs.Cell.N != 0 || docs.ArmTier.N != 4 || docs.Post.Mean() >= cold.Post.Mean()-0.2 {
		t.Errorf("(arm, tier) fallback: %.2f vs cold %.2f", docs.Post.Mean(), cold.Post.Mean())
	}
	// At another tier only the lift carries over, and only a little.
	simple := tb.estimate("cheap", 0, 2, "fix", Simple)
	if simple.ArmTier.N != 0 || simple.Lift >= 1 || simple.Post.Mean() >= Prior(Simple, 0, 2) || simple.Post.Mean() < 0.5 {
		t.Errorf("(arm) lift: lift %.2f mean %.2f", simple.Lift, simple.Post.Mean())
	}
	// Lots of simple successes don't make the cheap arm look good at complex tasks.
	es = nil
	for i := 0; i < 30; i++ {
		es = append(es, ev("cheap", "fix", Simple, Success, 0))
	}
	cx := learn(es, now, rankIn(pool)).estimate("cheap", 0, 2, "fix", Complex)
	if cx.Post.Mean() > 0.5 {
		t.Errorf("cheap arm at complex after simple successes: %.2f", cx.Post.Mean())
	}
}

func TestSampleBeta(t *testing.T) {
	r := seeded(1)
	for _, b := range []Beta{{2, 8}, {0.4, 0.6}, {30, 3}} {
		sum := 0.0
		const n = 20000
		for i := 0; i < n; i++ {
			x := sampleBeta(r, b)
			if x < 0 || x > 1 {
				t.Fatalf("draw out of [0,1]: %v", x)
			}
			sum += x
		}
		if mean := sum / n; math.Abs(mean-b.Mean()) > 0.01 {
			t.Errorf("Beta(%v,%v): sample mean %.3f, want %.3f", b.A, b.B, mean, b.Mean())
		}
	}
}

// ------------------------------------------------------------ choice --

func TestDecideIsDeterministicWithASeed(t *testing.T) {
	q := Request{Task: "Fix the off-by-one in the pager; add a test", Pool: []string{"codex::low", "agy", "codex::high"}, Now: now}
	q.Rand = seeded(42)
	a, err := Decide(q)
	if err != nil {
		t.Fatal(err)
	}
	q.Rand = seeded(42)
	b, _ := Decide(q)
	if strings.Join(a.Chain, " ") != strings.Join(b.Chain, " ") || a.Why != b.Why {
		t.Errorf("same seed, different answer:\n%v %s\n%v %s", a.Chain, a.Why, b.Chain, b.Why)
	}
	if len(a.Chain) != 3 || a.Type != "fix" || a.Tier != Medium {
		t.Errorf("decision: %+v", a)
	}
	if !strings.HasPrefix(a.Why, "auto: "+a.Chain[0]+" — fix · medium · no runs yet, prior ") {
		t.Errorf("explanation: %q", a.Why)
	}
}

func TestCostPenaltyPrefersTheCheapest(t *testing.T) {
	// Two arms with the same strong record: the cheaper one wins.
	var es []Evidence
	for i := 0; i < 40; i++ {
		es = append(es, ev("cheap", "fix", Simple, Success, 0), ev("strong", "fix", Simple, Success, 0))
	}
	for s := uint64(1); s <= 20; s++ {
		d, _ := Decide(Request{Task: "Fix the typo in README", Pool: []string{"cheap", "strong"}, Evidence: es, Now: now, Rand: seeded(s)})
		if d.Chain[0] != "cheap" {
			t.Fatalf("seed %d: %v (%s)", s, d.Chain, d.Why)
		}
	}
}

func TestOutOfQuotaGoesLast(t *testing.T) {
	d, _ := Decide(Request{Task: "Fix the typo in README", Pool: []string{"codex", "agy", "opencode"},
		OutOfQuota: map[string]bool{"codex": true}, Now: now, Rand: seeded(3)})
	if d.Chain[len(d.Chain)-1] != "codex" || d.Chain[0] == "codex" {
		t.Errorf("chain %v", d.Chain)
	}
	if !strings.Contains(d.Why, "out of quota now: codex") {
		t.Errorf("explanation: %q", d.Why)
	}
}

func TestEmptyPool(t *testing.T) {
	if _, err := Decide(Request{Task: "x"}); err == nil {
		t.Error("an empty pool must be an error")
	}
}

// ---------------------------------------------------------- learning --

// simulate delegates n tasks of a tier through Decide, with a world where
// each arm succeeds with the given rate, and returns how often each arm was
// picked in the last quarter.
func simulate(t *testing.T, task string, pool []string, rate map[string]float64, n int, seed uint64) map[string]int {
	t.Helper()
	r := seeded(seed)
	world := seeded(seed + 1000)
	var es []Evidence
	picks := map[string]int{}
	for i := 0; i < n; i++ {
		at := now.Add(time.Duration(i-n) * time.Hour)
		d, err := Decide(Request{Task: task, Pool: pool, Evidence: es, Now: at, Rand: r})
		if err != nil {
			t.Fatal(err)
		}
		arm := d.Chain[0]
		o := Failure
		if world.Float64() < rate[arm] {
			o = Success
		}
		es = append(es, Evidence{Arm: arm, CLI: arm, Type: d.Type, Tier: d.Tier, Outcome: o, At: at})
		if i >= n*3/4 {
			picks[arm]++
		}
	}
	return picks
}

// Thompson sampling is random: a cheap arm with two unlucky failures early
// can take a while to win its tasks back. So these look at ten simulated
// histories (seeded, deterministic) and ask for most of them to go right.

func TestCheapArmThatFailsComplexTasksLosesThem(t *testing.T) {
	task := "Migrate the whole repo from Bubble Tea v1 to v2"
	good := 0
	for s := uint64(1); s <= 10; s++ {
		picks := simulate(t, task, []string{"cheap", "strong"}, map[string]float64{"cheap": 0.15, "strong": 0.85}, 80, s)
		if picks["strong"] >= 18 {
			good++
		}
	}
	if good < 9 {
		t.Errorf("the strong arm kept the last complex tasks in %d of 10 histories", good)
	}
}

func TestCheapArmThatSucceedsAtSimpleTasksKeepsThem(t *testing.T) {
	task := "Fix the typo in README"
	good := 0
	for s := uint64(1); s <= 10; s++ {
		picks := simulate(t, task, []string{"cheap", "strong"}, map[string]float64{"cheap": 0.95, "strong": 0.97}, 80, s)
		if picks["cheap"] >= 13 {
			good++
		}
	}
	if good < 7 {
		t.Errorf("the cheap arm kept most of the last simple tasks in %d of 10 histories", good)
	}
}

func TestExplorationFades(t *testing.T) {
	pool := []string{"cheap", "strong"}
	task := "Fix the typo in README"
	cold, _ := Decide(Request{Task: task, Pool: pool, Now: now, Rand: seeded(5)})
	var es []Evidence
	for i := 0; i < 60; i++ {
		es = append(es, ev("cheap", "fix", Simple, Success, 0), ev("strong", "fix", Simple, Success, 0))
	}
	warm, _ := Decide(Request{Task: task, Pool: pool, Evidence: es, Now: now, Rand: seeded(5)})
	if warm.ExploreP >= cold.ExploreP || warm.ExploreP > 0.05 {
		t.Errorf("exploration: cold %.2f, warm %.2f", cold.ExploreP, warm.ExploreP)
	}
}

// ----------------------------------------------------------- outcomes --

func TestOutcomeOf(t *testing.T) {
	cases := []struct {
		o    Observation
		want Outcome
	}{
		{Observation{Status: runs.Done}, Success},
		{Observation{Status: runs.Done, TestsSeen: true, TestsPassed: true}, Success},
		{Observation{Status: runs.Done, TestsSeen: true}, Failure},
		{Observation{Status: runs.Done, Violated: true}, Failure},
		{Observation{Status: runs.Done, Rating: feedback.Bad}, Failure},
		{Observation{Status: runs.Done, Violated: true, Rating: feedback.Good}, Success},
		{Observation{Status: runs.Failed}, Failure},
		{Observation{Status: runs.Timeout}, Failure},
		{Observation{Status: runs.NoPermission}, Failure},
		{Observation{Status: runs.Failed, Rating: feedback.Good}, Success},
		{Observation{Status: runs.OutOfQuota}, NoSignal},
		{Observation{Status: runs.OutOfQuota, Rating: feedback.Bad}, NoSignal},
		{Observation{Status: runs.Skipped}, NoSignal},
		{Observation{Status: runs.Interrupted}, NoSignal},
		{Observation{Status: runs.Running}, NoSignal},
		{Observation{Status: runs.Running, Rating: feedback.Bad}, NoSignal},
	}
	for _, c := range cases {
		if got := OutcomeOf(c.o); got != c.want {
			t.Errorf("%+v: %v, want %v", c.o, got, c.want)
		}
	}
}

func TestGatherAndChecksCache(t *testing.T) {
	end := now.Add(-time.Hour)
	rs := []history.Run{
		{Stamp: "A", Agent: "codex", ModelID: "gpt-6-luna", Effort: "low", Task: "Fix the typo in README", Status: runs.Done, Start: end.Add(-time.Minute), End: end},
		{Stamp: "B", Agent: "agy", Task: "Migrate the whole repo to v2", Status: runs.Done, Start: end, End: end, Rating: feedback.Bad},
		{Stamp: "C", Agent: "agy", Task: "x", TaskType: "docs", Tier: Complex, Status: runs.OutOfQuota, Start: end},
		{Stamp: "D", Agent: "claude", Task: "orchestrate", Status: runs.Done, Start: end},
	}
	calls := 0
	path := t.TempDir() + "/checks.json"
	checks := LoadChecks(path)
	checks.Run = func(r history.Run, at time.Time) Check {
		calls++
		return Check{Violated: r.Stamp == "A", At: at}
	}
	got := Gather(rs, checks, now)
	if len(got) != 3 {
		t.Fatalf("claude's runs are not evidence: %+v", got)
	}
	want := []Evidence{
		{Key: "A-codex", Arm: "codex:gpt-6-luna:low", CLI: "codex", Type: "fix", Tier: Simple, At: end, Outcome: Failure},
		{Key: "B-agy", Arm: "agy", CLI: "agy", Type: "other", Tier: Complex, At: end, Outcome: Failure},
		{Key: "C-agy", Arm: "agy", CLI: "agy", Type: "docs", Tier: Complex, At: end, Outcome: NoSignal},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("evidence %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
	if calls != 2 {
		t.Errorf("only done runs are checked: %d calls", calls)
	}
	if err := checks.Save(); err != nil {
		t.Fatal(err)
	}
	again := LoadChecks(path)
	again.Run = func(history.Run, time.Time) Check { t.Fatal("checked again"); return Check{} }
	if c := again.Get(rs[0], now); !c.Violated {
		t.Errorf("cached check lost: %+v", c)
	}
}

func TestResolveArms(t *testing.T) {
	es := []Evidence{{Arm: "codex:gpt-6-luna:low", CLI: "codex"}, {Arm: "agy:gemini", CLI: "agy"}, {Arm: "opencode:x", CLI: "opencode"}}
	got := ResolveArms(es, []string{"codex", "agy:gemini", "opencode:y"})
	if got[0].Arm != "codex" || got[1].Arm != "agy:gemini" || got[2].Arm != "opencode:x" {
		t.Errorf("resolved: %+v", got)
	}
}

func TestArmKey(t *testing.T) {
	for _, c := range [][4]string{
		{"codex", "", "", "codex"},
		{"codex", "gpt-6-luna", "", "codex:gpt-6-luna"},
		{"codex", "gpt-6-luna", "low", "codex:gpt-6-luna:low"},
		{"codex", "", "high", "codex::high"},
	} {
		if got := ArmKey(c[0], c[1], c[2]); got != c[3] {
			t.Errorf("%v: %q", c, got)
		}
	}
}

func TestSummarize(t *testing.T) {
	at := now.Add(-time.Hour)
	rs := []history.Run{
		{Stamp: "1", Agent: "codex", Auto: true, Choice: 1, Status: runs.Done, Start: at, End: at},
		{Stamp: "2", Agent: "codex", Auto: true, Choice: 1, Status: runs.Failed, Start: at, End: at, Explored: true},
		{Stamp: "3", Agent: "codex", Auto: true, Choice: 1, Status: runs.OutOfQuota, Start: at, End: at},
		{Stamp: "3", Agent: "agy", Auto: true, Choice: 2, Status: runs.Done, Start: at, End: at},
		{Stamp: "4", Agent: "agy", Status: runs.Done, Start: at, End: at},                                   // not auto
		{Stamp: "5", Agent: "agy", Auto: true, Choice: 1, Status: runs.Done, Start: now.AddDate(0, 0, -40)}, // too old
	}
	checks := &Checks{Run: func(history.Run, time.Time) Check { return Check{} }}
	s := Summarize(rs, now.AddDate(0, 0, -7), checks, now)
	if s.Decisions != 3 || s.FirstChoiceRuns != 2 || s.FirstChoiceOK != 1 || s.FellBack != 1 || s.Explored != 1 || s.Exploited != 2 {
		t.Errorf("summary: %+v", s)
	}
}

// ---------------------------------------------------------- external --

func external(mode string, timeout time.Duration) *External {
	self, _ := os.Executable()
	return &External{Command: self, Timeout: timeout, Env: []string{"PANAL_FAKE_ROUTER=" + mode}}
}

func TestExternalRouter(t *testing.T) {
	q := Request{Task: "Migrate the whole repo to v2", Pool: []string{"codex", "agy"}, Now: now, Rand: seeded(1)}

	q.External = external("simple", 0)
	d, _ := Decide(q)
	if d.Tier != Simple || !strings.Contains(d.Why, "external router: tier simple") {
		t.Errorf("tier from the external router: %s / %s", d.Tier, d.Why)
	}

	q.External = external("arm", 0)
	d, _ = Decide(q)
	if d.Chain[0] != "agy" || !d.Picked || strings.Contains(d.Why, "exploring") {
		t.Errorf("arm from the external router: %v / %s", d.Chain, d.Why)
	}

	for _, mode := range []string{"garbage", "badtier", "fail"} {
		q.External = external(mode, 0)
		d, err := Decide(q)
		if err != nil || d.Tier != Complex || !strings.Contains(d.Why, "external router failed") || !strings.Contains(d.Why, "used the built-in one") {
			t.Errorf("%s: %v %s / %s", mode, err, d.Tier, d.Why)
		}
	}

	q.External = external("slow", 300*time.Millisecond)
	start := time.Now()
	d, _ = Decide(q)
	if time.Since(start) > 5*time.Second || !strings.Contains(d.Why, "no answer in 300ms") {
		t.Errorf("timeout: took %s / %s", time.Since(start), d.Why)
	}

	q.External = &External{Command: "panal-no-such-router-command"}
	if d, _ := Decide(q); !strings.Contains(d.Why, "external router failed") {
		t.Errorf("missing command: %s", d.Why)
	}
}

func TestOutOfQuotaNoteOnlyNamesThePool(t *testing.T) {
	d, _ := Decide(Request{Task: "Fix the typo in README", Pool: []string{"codex", "agy"},
		OutOfQuota: map[string]bool{"opencode": true}, Now: now, Rand: seeded(3)})
	if strings.Contains(d.Why, "out of quota") {
		t.Errorf("opencode is not in the pool: %q", d.Why)
	}
}
