package site

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

const (
	moveWindow = 7 * day  // DD-14: long enough that every tier has a fresh check inside it
	chartSpan  = 30 * day // the spotlight's price history
	minPrice   = 1.00     // cheaper cards are never movers
	minDelta   = 0.50     // smaller moves are noise
	todayMin   = 100.0    // the Today strip is the daily-checked tier only
	maxRising  = 3        // after the spotlight
	maxFalling = 4
	maxToday   = 5
)

// series is one key's market observations, oldest first.
type series []card.Observation

func byKey(obs []card.Observation) map[string]series {
	out := map[string]series{}
	for _, o := range obs {
		out[o.CardID] = append(out[o.CardID], o)
	}
	for _, s := range out {
		slices.SortStableFunc(s, func(a, b card.Observation) int { return a.ObservedAt.Compare(b.ObservedAt) })
	}
	return out
}

// move compares a series' latest price with its last price at or before cut.
// A card's first price is never a move: without a baseline, ok is false.
func move(s series, cut time.Time) (was, latest card.Observation, ok bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if !s[i].ObservedAt.After(cut) {
			if i == len(s)-1 {
				return was, latest, false // nothing newer than the baseline
			}
			return s[i], s[len(s)-1], true
		}
	}
	return was, latest, false
}

func significant(was, now float64) bool {
	return max(was, now) >= minPrice && math.Abs(now-was) >= minDelta
}

func newMover(key string, l card.Listing, was, now float64) Mover {
	return Mover{key: key, Name: l.Name, Set: l.Expansion, Number: l.Number, Variant: l.VariantLabel,
		Image: l.Image, Was: was, Now: now, Percent: (now - was) / was * 100}
}

// byMagnitude orders movers by the size of the move, largest first; the key
// breaks ties so the page is stable from one run to the next.
func byMagnitude(a, b Mover) int {
	if c := cmp.Compare(math.Abs(b.Percent), math.Abs(a.Percent)); c != 0 {
		return c
	}
	return cmp.Compare(a.key, b.key)
}

// Movers returns the week's biggest move as the spotlight, then the next
// risers and the top fallers (DD-14).
func Movers(hist map[string]series, info map[string]card.Listing, now time.Time) (spot *Mover, rising, falling []Mover) {
	var all []Mover
	for key, s := range hist {
		l, ok := info[key]
		if !ok {
			continue
		}
		was, latest, ok := move(s, now.Add(-moveWindow))
		if !ok || !significant(*was.Market, *latest.Market) {
			continue
		}
		all = append(all, newMover(key, l, *was.Market, *latest.Market))
	}
	if len(all) == 0 {
		return nil, nil, nil
	}
	slices.SortFunc(all, byMagnitude)
	top := all[0]
	for _, o := range hist[top.key] {
		if o.ObservedAt.After(now.Add(-chartSpan)) {
			top.History = append(top.History, Point{At: o.ObservedAt, Price: *o.Market})
		}
	}
	for _, m := range all[1:] {
		switch {
		case m.Percent > 0 && len(rising) < maxRising:
			rising = append(rising, m)
		case m.Percent < 0 && len(falling) < maxFalling:
			falling = append(falling, m)
		}
	}
	return &top, rising, falling
}

// Today compares each $100+ card checked in the last 24 hours with its
// previous check. Every card in that tier runs on the same daily clock, so
// the comparison is fair (DD-14).
func Today(hist map[string]series, info map[string]card.Listing, now time.Time) []Mover {
	var out []Mover
	for key, s := range hist {
		l, ok := info[key]
		if !ok || len(s) < 2 {
			continue
		}
		prev, latest := s[len(s)-2], s[len(s)-1]
		if now.Sub(latest.ObservedAt) > day || *latest.Market < todayMin || !significant(*prev.Market, *latest.Market) {
			continue
		}
		out = append(out, newMover(key, l, *prev.Market, *latest.Market))
	}
	slices.SortFunc(out, byMagnitude)
	return out[:min(len(out), maxToday)]
}

// PriceIndex is the value-weighted change in percent over window, counting
// only cards priced at both ends, so coverage growing during the first pass
// cannot look like the market rising. nil means no card has a baseline yet.
func PriceIndex(hist map[string]series, qty map[string]int, now time.Time, window time.Duration) *float64 {
	var then, latestSum float64
	for key, s := range hist {
		was, latest, ok := move(s, now.Add(-window))
		if !ok {
			continue
		}
		q := float64(max(qty[key], 1))
		then += *was.Market * q
		latestSum += *latest.Market * q
	}
	if then == 0 {
		return nil
	}
	p := (latestSum/then - 1) * 100
	return &p
}
