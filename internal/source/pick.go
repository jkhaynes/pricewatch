package source

import (
	"fmt"
	"maps"
	"slices"

	"github.com/jkhaynes/pricewatch/internal/card"
)

// Pick returns the single available price whose name is one of candidates.
func Pick(available map[string]card.Price, candidates []string) (card.Price, error) {
	var found []string
	for _, c := range candidates {
		if _, ok := available[c]; ok {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return card.Price{}, fmt.Errorf("%w: want one of %q, source has %q",
			card.ErrVariantUnavailable, candidates, slices.Sorted(maps.Keys(available)))
	case 1:
		return available[found[0]], nil
	default:
		return card.Price{}, fmt.Errorf("%w: %q all present", card.ErrVariantAmbiguous, found)
	}
}

// Quotes answers each requested variant from one response's prices.
func Quotes(available map[string]card.Price, subtypes map[card.Variant][]string, variants []card.Variant) []card.Quote {
	out := make([]card.Quote, len(variants))
	for i, v := range variants {
		p, err := Pick(available, subtypes[v])
		if err != nil {
			err = fmt.Errorf("%s: %w", v, err)
		}
		out[i] = card.Quote{Variant: v, Price: p, Err: err}
	}
	return out
}
