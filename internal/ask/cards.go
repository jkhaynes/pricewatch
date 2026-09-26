package ask

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

const findDoc = `Look up cards in the collection by name, optionally narrowed by exact card number
(as printed, e.g. 59/109) and expansion. Returns every matching print as its own row with its
status, latest price and collection_key. One card number often exists as several prints
(Normal, Reverse Holo, 1st Edition) with very different prices: if more than one row matches,
show them all or ask which print is meant. Never combine their prices.`

const historyDoc = `Every recorded market price for one exact print, oldest first. Takes a
collection_key from find_cards or top_cards, never a card name, so the history is always for
one print.`

// maxFound caps find_cards, so a one-letter search can't flood the context.
const maxFound = 100

type FindIn struct {
	Name      string `json:"name" jsonschema:"part of the card name, any case, e.g. tropius"`
	Number    string `json:"number,omitempty" jsonschema:"the exact card number as printed, e.g. 59/109"`
	Expansion string `json:"expansion,omitempty" jsonschema:"only this expansion, by its TCG Collector name"`
}

type HistoryIn struct {
	Key string `json:"collection_key" jsonschema:"from find_cards or top_cards; names one exact print"`
}

type HistoryOut struct {
	Card   Card    `json:"card"`
	Prices []Point `json:"prices" jsonschema:"oldest first"`
}

type Point struct {
	At    time.Time `json:"at"`
	Price float64   `json:"price"`
}

func (t tools) findCards(ctx context.Context, st Store, in FindIn) (CardsOut, error) {
	name := card.Normalize(in.Name)
	if name == "" {
		return CardsOut{}, errors.New("name is required")
	}
	cards, _, err := t.cards(ctx, st)
	if err != nil {
		return CardsOut{}, err
	}
	var out []Card
	for _, c := range cards {
		if !strings.Contains(card.Normalize(c.Name), name) ||
			(in.Number != "" && c.Number != in.Number) ||
			(in.Expansion != "" && !strings.EqualFold(c.Expansion, in.Expansion)) {
			continue
		}
		out = append(out, c)
	}
	return capped(out, maxFound), nil
}

func (t tools) priceHistory(ctx context.Context, st Store, in HistoryIn) (HistoryOut, error) {
	cards, hist, err := t.cards(ctx, st)
	if err != nil {
		return HistoryOut{}, err
	}
	for _, c := range cards {
		if c.Key != in.Key {
			continue
		}
		out := HistoryOut{Card: c}
		for _, o := range hist[c.Key] {
			out.Prices = append(out.Prices, Point{At: o.ObservedAt, Price: *o.Market})
		}
		return out, nil
	}
	return HistoryOut{}, fmt.Errorf("no card with collection_key %q: look it up with find_cards first", in.Key)
}
