package state

import (
	"testing"
	"time"
)

func TestExpireResetsPastWindows(t *testing.T) {
	now := time.Date(2026, 9, 29, 7, 0, 0, 0, time.Local)
	orig := Quota{
		Exact: true, UsedPct: 100, ResetsAt: now.Add(-72 * time.Hour),
		Bars: []Bar{
			{Name: "sem", UsedPct: 100, ResetsAt: now.Add(-72 * time.Hour), ExhaustsAt: now.Add(-80 * time.Hour)},
			{Name: "5h", UsedPct: 40, ResetsAt: now.Add(time.Hour)},
		},
	}
	c, freed := orig.Expire(now)
	if !freed {
		t.Fatal("the week was at 100 % and has already reset: it was freed")
	}
	if c.UsedPct != 0 || c.Bars[0].UsedPct != 0 || !c.Bars[0].AlreadyReset || !c.Bars[0].ResetsAt.IsZero() || !c.Bars[0].ExhaustsAt.IsZero() {
		t.Fatalf("the expired window must be at 0 %%: %+v", c)
	}
	if c.Bars[1].UsedPct != 40 || c.Bars[1].AlreadyReset {
		t.Fatalf("the window that has not expired is left alone: %+v", c.Bars[1])
	}
	if orig.Bars[0].UsedPct != 100 {
		t.Fatal("Expire must not touch the original bars (readers cache them)")
	}
	if _, fr := (Quota{Bars: []Bar{{UsedPct: 30, ResetsAt: now.Add(-time.Hour)}}}).Expire(now); fr {
		t.Fatal("a window at 30 % that resets does not «free» anything")
	}
}

func TestParseStatus(t *testing.T) {
	for st := NoData; st <= Orchestrating; st++ {
		if got, ok := ParseStatus(st.String()); !ok || got != st {
			t.Errorf("ParseStatus(%q) = %v, %v", st.String(), got, ok)
		}
	}
	if _, ok := ParseStatus("whatever"); ok {
		t.Error("an unknown word must give ok=false")
	}
}
