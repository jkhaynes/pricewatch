package site

import (
	"context"
	"fmt"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/priority"
)

// Store is what the page needs from the database, declared here where it is
// consumed.
type Store interface {
	Listings(ctx context.Context, source string) ([]card.Listing, error)
	History(ctx context.Context, source string) ([]card.Observation, error)
	Candidates(ctx context.Context, source string) ([]card.Candidate, error)
	Unresolved(ctx context.Context, source string) ([]card.Mapping, error)
	RunsSince(ctx context.Context, since time.Time) ([]card.Run, error)
	QuotaUsed(ctx context.Context, source, day string) (int, error)
}

type Options struct {
	Source     string
	Now        time.Time
	DailyLimit int
	RunMinute  int // the schedule's minute past each hour
	Policy     priority.Policy
}

// Build reads everything once and derives every section of the page.
func Build(ctx context.Context, st Store, o Options) (Data, error) {
	listings, err := st.Listings(ctx, o.Source)
	if err != nil {
		return Data{}, fmt.Errorf("load listings: %w", err)
	}
	obs, err := st.History(ctx, o.Source)
	if err != nil {
		return Data{}, fmt.Errorf("load history: %w", err)
	}
	cands, err := st.Candidates(ctx, o.Source)
	if err != nil {
		return Data{}, fmt.Errorf("load candidates: %w", err)
	}
	unresolved, err := st.Unresolved(ctx, o.Source)
	if err != nil {
		return Data{}, fmt.Errorf("load unresolved: %w", err)
	}
	runs, err := st.RunsSince(ctx, o.Now.Add(-25*time.Hour))
	if err != nil {
		return Data{}, fmt.Errorf("load runs: %w", err)
	}
	used, err := st.QuotaUsed(ctx, o.Source, o.Now.UTC().Format(time.DateOnly))
	if err != nil {
		return Data{}, fmt.Errorf("load quota: %w", err)
	}

	info := make(map[string]card.Listing, len(listings))
	qty := make(map[string]int, len(listings))
	for _, l := range listings {
		info[l.Key], qty[l.Key] = l, l.Quantity
	}
	hist := byKey(obs)

	d := Data{
		GeneratedAt:     o.Now.UTC(),
		RunMinute:       o.RunMinute,
		Coverage:        Cover(listings, hist),
		Today:           Today(hist, info, o.Now),
		Bands:           Bands(cands, info, o.Policy, o.Now),
		Unresolved:      Groups(unresolved),
		UnresolvedTotal: len(unresolved),
		Index:           Index{Week: PriceIndex(hist, qty, o.Now, 7*day), Month: PriceIndex(hist, qty, o.Now, 30*day)},
		Budget:          Budget{Used: used, Limit: o.DailyLimit},
	}
	d.Spotlight, d.Rising, d.Falling = Movers(hist, info, o.Now)
	d.Runs, d.Budget.PerHour = Hourly(runs, o.Now)
	return d, nil
}
