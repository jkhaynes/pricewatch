package card

import (
	"math"
	"time"
)

type Price struct {
	Market *float64
	Low    *float64
	High   *float64
}

// Quote is a provider's answer for one requested variant of one source card.
type Quote struct {
	Variant Variant
	Price   Price
	Err     error  // ErrVariantUnavailable or ErrVariantAmbiguous, wrapped with detail
	Image   string // the card's art URL; the same on every quote of one card, "" if unknown
}

// Observation is one price reading. CardID is the collection key, not the
// source's ID, so history survives a change of provider.
type Observation struct {
	CardID string
	Source string
	Price
	ObservedAt time.Time
}

// Change is a card's current observation against its own previous one (DD-7).
type Change struct {
	CardID   string
	Previous Observation
	Current  Observation
}

func (c Change) Delta() float64   { return *c.Current.Market - *c.Previous.Market }
func (c Change) Percent() float64 { return c.Delta() / *c.Previous.Market * 100 }

// Compare reports whether the market price moved by at least one cent.
func Compare(prev, curr Observation) (Change, bool) {
	if prev.Market == nil || curr.Market == nil {
		return Change{}, false
	}
	if cents(*prev.Market) == cents(*curr.Market) {
		return Change{}, false
	}
	return Change{CardID: curr.CardID, Previous: prev, Current: curr}, true
}

func cents(p float64) int64 { return int64(math.Round(p * 100)) }
