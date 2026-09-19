package site

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

var now = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// hist builds history from "key: price@hoursAgo" steps, oldest first per key.
func hist(steps ...step) map[string]series {
	var obs []card.Observation
	for _, s := range steps {
		p := s.price
		obs = append(obs, card.Observation{CardID: s.key, Price: card.Price{Market: &p}, ObservedAt: now.Add(-s.ago)})
	}
	return byKey(obs)
}

type step struct {
	key   string
	price float64
	ago   time.Duration
}

func info(keys ...string) map[string]card.Listing {
	out := map[string]card.Listing{}
	for _, k := range keys {
		out[k] = card.Listing{Key: k, Name: "Card " + k, Expansion: "Set", Number: "1/100", VariantLabel: "Holo"}
	}
	return out
}

func keys(ms []Mover) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.key)
	}
	return out
}

func TestMovers(t *testing.T) {
	h := hist(
		step{"up", 10, 8 * day}, step{"up", 12, day}, // +20%
		step{"down", 300, 10 * day}, step{"down", 270, 2 * day}, // -10%
		step{"big", 100, 9 * day}, step{"big", 150, time.Hour}, // +50%: the spotlight
		step{"new", 50, 2 * day},                             // first price: never a move
		step{"recent", 20, 5 * day}, step{"recent", 30, day}, // no price at least 7 days old
		step{"cheap", 0.20, 8 * day}, step{"cheap", 0.35, day}, // +75%, but under $1
		step{"tiny", 50, 8 * day}, step{"tiny", 50.30, day}, // moved less than $0.50
		step{"stale", 40, 20 * day}, step{"stale", 44, 12 * day}, // latest is itself older than 7 days
	)
	spot, rising, falling := Movers(h, info("up", "down", "big", "new", "recent", "cheap", "tiny", "stale"), now)
	if spot == nil || spot.key != "big" || spot.Was != 100 || spot.Now != 150 || spot.Percent != 50 {
		t.Fatalf("spotlight = %+v, want big at +50%%", spot)
	}
	if len(spot.History) != 2 || spot.Name != "Card big" || spot.Variant != "Holo" {
		t.Errorf("spotlight history/description = %+v", spot)
	}
	if !slices.Equal(keys(rising), []string{"up"}) || !slices.Equal(keys(falling), []string{"down"}) {
		t.Errorf("rising = %v, falling = %v; want [up] and [down]", keys(rising), keys(falling))
	}
}

func TestMoversCapsAndNeverRepeatTheSpotlight(t *testing.T) {
	var steps []step
	for i, k := range []string{"u1", "u2", "u3", "u4", "u5", "u6"} {
		steps = append(steps, step{k, 100, 8 * day}, step{k, 110 + float64(i), day})
	}
	for i, k := range []string{"d1", "d2", "d3", "d4", "d5", "d6"} {
		steps = append(steps, step{k, 100, 8 * day}, step{k, 95 - float64(i), day})
	}
	spot, rising, falling := Movers(hist(steps...), info("u1", "u2", "u3", "u4", "u5", "u6", "d1", "d2", "d3", "d4", "d5", "d6"), now)
	if spot.key != "u6" {
		t.Fatalf("spotlight = %s, want u6 (+15%%)", spot.key)
	}
	if !slices.Equal(keys(rising), []string{"u5", "u4", "u3"}) {
		t.Errorf("rising = %v, want the next three risers", keys(rising))
	}
	if !slices.Equal(keys(falling), []string{"d6", "d5", "d4", "d3"}) {
		t.Errorf("falling = %v, want the top four fallers", keys(falling))
	}
}

func TestMoversEmptyUntilThereIsAWeekOfHistory(t *testing.T) {
	spot, rising, falling := Movers(hist(step{"a", 10, day}, step{"a", 20, time.Hour}), info("a"), now)
	if spot != nil || len(rising) != 0 || len(falling) != 0 {
		t.Errorf("got %v %v %v; want nothing without a 7-day baseline", spot, rising, falling)
	}
}

func TestToday(t *testing.T) {
	h := hist(
		step{"rich", 140, 25 * time.Hour}, step{"rich", 150, 2 * time.Hour}, // +7.1%
		step{"rich-down", 400, 26 * time.Hour}, step{"rich-down", 380, 3 * time.Hour}, // -5%
		step{"stale", 200, 72 * time.Hour}, step{"stale", 210, 48 * time.Hour}, // not checked today
		step{"mid", 40, 25 * time.Hour}, step{"mid", 50, 2 * time.Hour}, // under $100
		step{"once", 300, time.Hour},                                       // no previous check
		step{"flat", 500, 25 * time.Hour}, step{"flat", 500.20, time.Hour}, // under $0.50
	)
	got := Today(h, info("rich", "rich-down", "stale", "mid", "once", "flat"), now)
	if !slices.Equal(keys(got), []string{"rich", "rich-down"}) {
		t.Errorf("Today = %v, want [rich rich-down] by absolute %%", keys(got))
	}
}

func TestPriceIndex(t *testing.T) {
	h := hist(
		step{"a", 100, 8 * day}, step{"a", 110, day},
		step{"b", 50, 8 * day}, step{"b", 60, day},
		step{"new", 1000, day}, // no baseline: must not count
	)
	qty := map[string]int{"a": 1, "b": 2, "new": 1}
	tests := []struct {
		name   string
		window time.Duration
		want   *float64
	}{
		{"week: (110 + 2x60) / (100 + 2x50)", 7 * day, ptr(15.0)},
		{"month: no card has a 30-day baseline", 30 * day, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PriceIndex(h, qty, now, tt.window)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("PriceIndex = %v, want nil", *got)
			case tt.want != nil && (got == nil || math.Abs(*got-*tt.want) > 1e-9):
				t.Errorf("PriceIndex = %v, want %v", got, *tt.want)
			}
		})
	}
}

func ptr(f float64) *float64 { return &f }
