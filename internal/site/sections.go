package site

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/priority"
)

// Bands puts every priceable source card on the freshness map: one band per
// DD-12 tier, each tile coloured by the share of its tier's interval used,
// most urgent first.
func Bands(cands []card.Candidate, info map[string]card.Listing, p priority.Policy, now time.Time) []Band {
	if len(p.Tiers) == 0 {
		return nil
	}
	bands := make([]Band, len(p.Tiers))
	for i, t := range p.Tiers {
		bands[i] = Band{Label: tierLabel(p, i), EveryDays: int(t.Every / day)}
	}
	for _, c := range cands {
		i := tierOf(p, c.Value)
		every := p.Tiers[i].Every
		l := info[c.Keys[0].Key]
		tile := Tile{Name: l.Name, Set: l.Expansion, Number: l.Number}
		if !c.NeverSeen {
			r := float64(now.Sub(c.LastChecked)) / float64(every)
			tile.Ratio = &r
		}
		if c.NeverSeen || now.Sub(c.LastChecked)+p.Slack >= every { // priority.Due's rule
			bands[i].Due++
		}
		bands[i].Tiles = append(bands[i].Tiles, tile)
	}
	for _, b := range bands {
		slices.SortStableFunc(b.Tiles, func(x, y Tile) int {
			return cmp.Compare(urgency(y), urgency(x))
		})
	}
	return bands
}

// urgency sorts never-priced tiles first, then by share of interval used.
func urgency(t Tile) float64 {
	if t.Ratio == nil {
		return 1e9
	}
	return *t.Ratio
}

func tierOf(p priority.Policy, value float64) int {
	for i, t := range p.Tiers {
		if value >= t.Min {
			return i
		}
	}
	return len(p.Tiers) - 1
}

func tierLabel(p priority.Policy, i int) string {
	t := p.Tiers[i]
	switch {
	case i == 0:
		return fmt.Sprintf("$%g+", t.Min)
	case t.Min == 0:
		return fmt.Sprintf("under $%g", p.Tiers[i-1].Min)
	default:
		return fmt.Sprintf("$%g to $%g", t.Min, p.Tiers[i-1].Min)
	}
}

// Cover counts keys with at least one market price, and the share of the
// collection's export value they hold. Export prices weigh the share only;
// they are never published.
func Cover(listings []card.Listing, hist map[string]series) Coverage {
	c := Coverage{Total: len(listings)}
	var all, priced float64
	for _, l := range listings {
		v := 0.0
		if l.Export != nil {
			v = *l.Export * float64(max(l.Quantity, 1))
		}
		all += v
		if len(hist[l.Key]) > 0 {
			c.Priced++
			priced += v
		}
	}
	if all > 0 {
		c.ValueShare = priced / all
	}
	return c
}

// reasonGroups sorts unresolved reasons into groups a reader understands. The
// first matching substring wins; the substrings come from the resolver's
// messages and card's error sentinels.
var reasonGroups = []struct{ match, label, hint string }{
	{card.ErrUnsupportedVariant.Error(), "Special prints not priced yet", "Poké Ball and Energy reverse holos, Cosmos, Prize Pack, stamps"},
	{"name mismatch", "Name differs at the source", "Mostly cards the source lists only as a pattern print"},
	{"not in set", "Number not in the set", "The set exists; the card number doesn't"},
	{card.ErrNotFound.Error(), "Gone from the source", "Found once, now returns not found"},
	{card.ErrVariantUnavailable.Error(), "Print not priced at the source", "The card exists, but not in this print"},
	{"unknown expansion", "Unknown expansion", "No set with a matching name"},
	{"matches sets", "Matches more than one set", "The expansion name fits several sets"},
	{card.ErrVariantAmbiguous.Error(), "More than one price for the print", "The source lists this print twice"},
	{"cards in set", "More than one card matches", "Several cards share the number and name"},
	{"unsupported language", "Not English", "Only English cards are priced"},
}

// Groups counts unresolved keys by reason group, largest first.
func Groups(unresolved []card.Mapping) []Group {
	counts := map[string]*Group{}
	var out []*Group
	for _, m := range unresolved {
		label, hint := "Other", "A reason pricewatch doesn't group yet"
		for _, g := range reasonGroups {
			if strings.Contains(m.Reason, g.match) {
				label, hint = g.label, g.hint
				break
			}
		}
		g, ok := counts[label]
		if !ok {
			g = &Group{Label: label, Hint: hint}
			counts[label] = g
			out = append(out, g)
		}
		g.Count++
	}
	slices.SortStableFunc(out, func(a, b *Group) int { return cmp.Compare(b.Count, a.Count) })
	groups := make([]Group, len(out))
	for i, g := range out {
		groups[i] = *g
	}
	return groups
}

// Hourly splits the last 24 hours into hourly slots, the current hour last,
// and reports whether a run finished in each and how many requests it made.
// A run that never finished made its requests but saved nothing, so it counts
// as no run. A failed job never saves the database at all.
func Hourly(runs []card.Run, now time.Time) (ran []bool, requests []int) {
	start := now.UTC().Truncate(time.Hour).Add(-23 * time.Hour)
	ran, requests = make([]bool, 24), make([]int, 24)
	for _, r := range runs {
		if r.FinishedAt == nil {
			continue
		}
		d := r.StartedAt.Sub(start)
		if d < 0 {
			continue
		}
		i := int(d / time.Hour)
		if i >= 24 {
			continue
		}
		ran[i] = true
		requests[i] += r.Requests
	}
	return ran, requests
}
