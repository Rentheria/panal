package router

import "testing"

// The rule phrases an orchestrator puts in every task must not make an easy
// task look harder: only the goal counts.
func TestTierIgnoresRulePhrases(t *testing.T) {
	for _, task := range []string{
		"You work in ../wt-x, a Go repo. Do not commit or push. You may only modify README.md. Read AGENTS.md before you start. If the tool does not let you write, use the shell. Goal: fix a typo in the README.",
		"Trabajas en ../wt-x, un repo Go. No hagas commit ni push. Solo puedes modificar README.md. Lee AGENTS.md antes de empezar. Si la herramienta de escribir no te deja, usa el shell. Objetivo: corrige un error de dedo en el README.",
	} {
		if tier, score, hits := Classify(Extract(task, false)); tier != Simple {
			t.Errorf("%q: tier %s (score %d, %v), want simple", task[:40], tier, score, hits)
		}
	}
}
