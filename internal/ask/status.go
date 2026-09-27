package ask

import (
	"context"
	"time"

	"github.com/jkhaynes/pricewatch/internal/site"
)

const statusDoc = `How the pricing pipeline is doing: how many cards are priced, how cards resolved
at the price source (resolved, ambiguous, unmatched), why unresolved cards failed grouped by
reason, requests spent today against the daily budget, and runs in the last 24 hours.`

type StatusOut struct {
	Keys       int            `json:"keys" jsonschema:"distinct cards (collection keys) in the collection"`
	Priced     int            `json:"priced" jsonschema:"cards with at least one market price"`
	Mappings   map[string]int `json:"mappings" jsonschema:"cards by resolution status; cards missing from every status are not yet resolved"`
	Unresolved []site.Group   `json:"unresolved" jsonschema:"why cards did not resolve, largest group first"`
	QuotaToday int            `json:"quota_used_today" jsonschema:"requests spent today, UTC"`
	Runs       int            `json:"runs_last_24h" jsonschema:"runs that finished in the last 24 hours"`
	Requests   int            `json:"requests_last_24h"`
}

func (t tools) pipelineStatus(ctx context.Context, st Store, _ struct{}) (StatusOut, error) {
	cards, _, err := t.cards(ctx, st)
	if err != nil {
		return StatusOut{}, err
	}
	counts, err := st.MappingCounts(ctx, t.source)
	if err != nil {
		return StatusOut{}, err
	}
	unresolved, err := st.Unresolved(ctx, t.source)
	if err != nil {
		return StatusOut{}, err
	}
	now := t.now().UTC()
	used, err := st.QuotaUsed(ctx, t.source, now.Format(time.DateOnly))
	if err != nil {
		return StatusOut{}, err
	}
	runs, err := st.RunsSince(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return StatusOut{}, err
	}
	out := StatusOut{Keys: len(cards), Mappings: map[string]int{}, Unresolved: site.Groups(unresolved), QuotaToday: used}
	for _, c := range cards {
		if c.Price != nil {
			out.Priced++
		}
	}
	for status, n := range counts {
		out.Mappings[string(status)] = n
	}
	for _, r := range runs {
		out.Requests += r.Requests
		if r.FinishedAt != nil {
			out.Runs++
		}
	}
	return out, nil
}
