package priority

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

// Tier re-checks cards worth at least Min every Every.
type Tier struct {
	Min   float64
	Every time.Duration
}

// Policy is a tier table, highest Min first, plus slack: a card counts as due
// this much early, so an hourly schedule does not drift later each day.
type Policy struct {
	Tiers []Tier
	Slack time.Duration
}

const day = 24 * time.Hour

// Default is DD-12's schedule: four tiers, nothing waits longer than a week.
// About 915 requests a day for the author's collection.
var Default = Policy{
	Tiers: []Tier{
		{Min: 100, Every: day},
		{Min: 20, Every: 2 * day},
		{Min: 5, Every: 4 * day},
		{Min: 0, Every: 7 * day},
	},
	Slack: time.Hour,
}

// Interval is how often a card of this value is re-checked. A policy with no
// tiers returns 0: everything is always due.
func (p Policy) Interval(value float64) time.Duration {
	for _, t := range p.Tiers {
		if value >= t.Min {
			return t.Every
		}
	}
	if len(p.Tiers) == 0 {
		return 0
	}
	return p.Tiers[len(p.Tiers)-1].Every
}

// Due returns the candidates due at now, most urgent first, at most n of them,
// and how many were due in total. Cards that are not due are left out: a run
// stops early rather than spend budget on them (DD-12).
func Due(cands []card.Candidate, now time.Time, n int, p Policy) ([]card.Candidate, int) {
	type scored struct {
		c       card.Candidate
		overdue float64 // elapsed / interval; +Inf when never priced or no interval
	}
	var due []scored
	for _, c := range cands {
		every := p.Interval(c.Value)
		if c.NeverSeen || every <= 0 {
			due = append(due, scored{c, math.Inf(1)})
			continue
		}
		if elapsed := now.Sub(c.LastChecked); elapsed+p.Slack >= every {
			due = append(due, scored{c, float64(elapsed) / float64(every)})
		}
	}
	slices.SortFunc(due, func(a, b scored) int {
		if o := cmp.Compare(b.overdue, a.overdue); o != 0 {
			return o
		}
		if v := cmp.Compare(b.c.Value, a.c.Value); v != 0 {
			return v
		}
		return cmp.Compare(a.c.SourceCardID, b.c.SourceCardID)
	})
	picked := make([]card.Candidate, 0, min(n, len(due)))
	for _, s := range due[:min(n, len(due))] {
		picked = append(picked, s.c)
	}
	return picked, len(due)
}
