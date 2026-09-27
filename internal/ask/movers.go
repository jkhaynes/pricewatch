package ask

import (
	"context"
	"fmt"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/site"
)

const moversDoc = `Biggest price rises and falls over the last N days (default 7): each card's
latest market price against its last price at least N days old. A card's first price is never
a move. Only cards worth $1 or more that moved at least $0.50 count. Cheap cards are checked
about weekly, so a window under 7 days only sees the cards checked inside it.`

const maxMoves = 10

type MoversIn struct {
	Days int `json:"days,omitempty" jsonschema:"look-back window in days, 1 to 90; default 7"`
}

type MoversOut struct {
	Rising  []Move `json:"rising" jsonschema:"largest rise first, at most 10"`
	Falling []Move `json:"falling" jsonschema:"largest fall first, at most 10"`
}

type Move struct {
	Key       string  `json:"collection_key"`
	Name      string  `json:"name"`
	Expansion string  `json:"expansion"`
	Number    string  `json:"number"`
	Variant   string  `json:"variant"`
	Was       float64 `json:"was" jsonschema:"market price at the start of the window, USD"`
	Now       float64 `json:"now" jsonschema:"latest market price, USD"`
	Percent   float64 `json:"percent"`
}

func (t tools) movers(ctx context.Context, st Store, in MoversIn) (MoversOut, error) {
	days := in.Days
	switch {
	case days == 0:
		days = 7
	case days < 1 || days > 90:
		return MoversOut{}, fmt.Errorf("days %d: want 1 to 90", days)
	}
	listings, err := st.Listings(ctx, t.source)
	if err != nil {
		return MoversOut{}, err
	}
	obs, err := st.History(ctx, t.source)
	if err != nil {
		return MoversOut{}, err
	}
	info := make(map[string]card.Listing, len(listings))
	for _, l := range listings {
		info[l.Key] = l
	}
	var out MoversOut
	for _, m := range site.Moves(site.ByKey(obs), info, t.now(), time.Duration(days)*24*time.Hour) {
		mv := Move{Key: m.Key(), Name: m.Name, Expansion: m.Set, Number: m.Number, Variant: m.Variant,
			Was: m.Was, Now: m.Now, Percent: cents(m.Percent)}
		switch {
		case m.Percent > 0 && len(out.Rising) < maxMoves:
			out.Rising = append(out.Rising, mv)
		case m.Percent < 0 && len(out.Falling) < maxMoves:
			out.Falling = append(out.Falling, mv)
		}
	}
	return out, nil
}
