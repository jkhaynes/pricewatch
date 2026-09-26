package ask

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
)

const valueDoc = `Total market value of the collection, or of one expansion: each card's latest
market price times the copies owned. Cards never priced are counted as unpriced and left
out of the total, not valued at $0. Includes a per-expansion breakdown, most valuable first.`

type ValueIn struct {
	Expansion string `json:"expansion,omitempty" jsonschema:"only this expansion, by its TCG Collector name, e.g. EX Ruby & Sapphire"`
}

type ValueOut struct {
	Total      float64          `json:"total" jsonschema:"USD, over priced cards only"`
	Priced     int              `json:"priced" jsonschema:"cards (collection keys) with a price"`
	Unpriced   int              `json:"unpriced" jsonschema:"cards never priced, not in the total"`
	Expansions []ExpansionValue `json:"expansions" jsonschema:"most valuable first"`
}

type ExpansionValue struct {
	Expansion string  `json:"expansion"`
	Total     float64 `json:"total"`
	Priced    int     `json:"priced"`
	Unpriced  int     `json:"unpriced"`
}

func (t tools) collectionValue(ctx context.Context, st Store, in ValueIn) (ValueOut, error) {
	cards, _, err := t.cards(ctx, st)
	if err != nil {
		return ValueOut{}, err
	}
	var out ValueOut
	byExp := map[string]*ExpansionValue{}
	for _, c := range cards {
		if in.Expansion != "" && !strings.EqualFold(c.Expansion, in.Expansion) {
			continue
		}
		e := byExp[c.Expansion]
		if e == nil {
			e = &ExpansionValue{Expansion: c.Expansion}
			byExp[c.Expansion] = e
		}
		if c.Value == nil {
			out.Unpriced++
			e.Unpriced++
			continue
		}
		out.Total += *c.Value
		out.Priced++
		e.Total += *c.Value
		e.Priced++
	}
	if in.Expansion != "" && len(byExp) == 0 {
		return ValueOut{}, fmt.Errorf("no cards in expansion %q; expansion names are TCG Collector's, e.g. EX Ruby & Sapphire", in.Expansion)
	}
	out.Total = cents(out.Total)
	for _, e := range byExp {
		e.Total = cents(e.Total)
		out.Expansions = append(out.Expansions, *e)
	}
	slices.SortFunc(out.Expansions, func(a, b ExpansionValue) int {
		if c := cmp.Compare(b.Total, a.Total); c != 0 {
			return c
		}
		return cmp.Compare(a.Expansion, b.Expansion)
	})
	return out, nil
}
