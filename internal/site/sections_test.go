package site

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/priority"
)

func cand(id string, value float64, lastAgo time.Duration) card.Candidate {
	c := card.Candidate{SourceCardID: id, Keys: []card.Mapping{{Key: "k-" + id}}, Value: value}
	if lastAgo < 0 {
		c.NeverSeen = true
	} else {
		c.LastChecked = now.Add(-lastAgo)
	}
	return c
}

func TestBands(t *testing.T) {
	cands := []card.Candidate{
		cand("rich", 400, 12*time.Hour),
		cand("cheap", 0.2, day),
		cand("late", 1, 9*day),
		cand("new", 3, -1),
	}
	names := map[string]card.Listing{"k-rich": {Name: "Umbreon VMAX", Expansion: "Evolving Skies", Number: "215/203"}}
	bands := Bands(cands, names, priority.Default, now)

	var labels []string
	var every []int
	for _, b := range bands {
		labels = append(labels, b.Label)
		every = append(every, b.EveryDays)
	}
	if !slices.Equal(labels, []string{"$100+", "$20 to $100", "$5 to $20", "under $5"}) || !slices.Equal(every, []int{1, 2, 4, 7}) {
		t.Fatalf("labels = %v, every = %v", labels, every)
	}
	rich := bands[0]
	if len(rich.Tiles) != 1 || rich.Due != 0 || *rich.Tiles[0].Ratio != 0.5 ||
		rich.Tiles[0].Name != "Umbreon VMAX" || rich.Tiles[0].Set != "Evolving Skies" || rich.Tiles[0].Number != "215/203" {
		t.Errorf("$100+ band = %+v", rich)
	}
	cheap := bands[3]
	if len(cheap.Tiles) != 3 || cheap.Due != 2 {
		t.Fatalf("under-$5 band = %+v, want 3 tiles with 2 due", cheap)
	}
	if cheap.Tiles[0].Ratio != nil || math.Abs(*cheap.Tiles[1].Ratio-9.0/7) > 1e-9 || math.Abs(*cheap.Tiles[2].Ratio-1.0/7) > 1e-9 {
		t.Errorf("under-$5 order: want never priced, then overdue, then fresh")
	}
}

func TestCover(t *testing.T) {
	listings := []card.Listing{
		{Key: "a", Export: ptr(10), Quantity: 2},
		{Key: "b", Export: ptr(5), Quantity: 1},
		{Key: "c", Export: ptr(5), Quantity: 1}, // never priced
		{Key: "d", Quantity: 1},                 // no export price
	}
	h := hist(step{"a", 12, day}, step{"b", 4, day})
	got := Cover(listings, h)
	if got.Priced != 2 || got.Total != 4 || math.Abs(got.ValueShare-25.0/30) > 1e-9 {
		t.Errorf("Cover = %+v, want 2 of 4 priced, value share (20+5)/30", got)
	}
}

func TestGroups(t *testing.T) {
	reasons := []string{
		`unsupported variant: "Poké Ball Reverse Holo"`,
		`unsupported variant: "Cosmos Holo"`,
		`name mismatch: collection "Pikachu", set 3020 #TG05 has ["Pikachu (Pattern)"]`,
		`number 999 not in set 1393`,
		`GET /cards/pk_x: card not found at source`,
		`holo: variant not priced at source`,
		`unknown expansion "Mystery Set"`,
		`expansion "Promos" matches sets [1 2]`,
		`holo: variant matches more than one price at source`,
		`2 cards in set 1393 match #59 "Mudkip"`,
		`unsupported language "Japanese"`,
		`something new`,
	}
	var ms []card.Mapping
	for _, r := range reasons {
		ms = append(ms, card.Mapping{Reason: r})
	}
	got := map[string]int{}
	var order []string
	for _, g := range Groups(ms) {
		got[g.Label] = g.Count
		order = append(order, g.Label)
	}
	want := map[string]int{
		"Special prints not priced yet": 2, "Name differs at the source": 1, "Number not in the set": 1,
		"Gone from the source": 1, "Print not priced at the source": 1, "Unknown expansion": 1,
		"Matches more than one set": 1, "More than one price for the print": 1,
		"More than one card matches": 1, "Not English": 1, "Other": 1,
	}
	for label, n := range want {
		if got[label] != n {
			t.Errorf("group %q = %d, want %d", label, got[label], n)
		}
	}
	if order[0] != "Special prints not priced yet" {
		t.Errorf("groups start with %q, want the largest first", order[0])
	}
}

func TestHourly(t *testing.T) {
	at := func(ago time.Duration) time.Time { return now.Add(30*time.Minute - ago) } // now is 12:00; runs start at :07-ish
	done := func(start time.Time) *time.Time { f := start.Add(3 * time.Minute); return &f }
	runs := []card.Run{
		{StartedAt: at(23 * time.Minute), Requests: 38},                  // 12:07 today: the current slot
		{StartedAt: at(83 * time.Minute), Requests: 40},                  // 11:07
		{StartedAt: at(143 * time.Minute), Requests: 9},                  // 10:07, never finished
		{StartedAt: now.Add(-23*time.Hour + 7*time.Minute), Requests: 5}, // 13:07 yesterday: the oldest slot
		{StartedAt: now.Add(-24*time.Hour + 7*time.Minute), Requests: 7}, // 12:07 yesterday: outside the window
	}
	for i := range runs {
		if i != 2 {
			runs[i].FinishedAt = done(runs[i].StartedAt)
		}
	}
	ran, requests := Hourly(runs, now.Add(30*time.Minute))
	if len(ran) != 24 || len(requests) != 24 {
		t.Fatalf("got %d and %d slots, want 24", len(ran), len(requests))
	}
	if !ran[23] || requests[23] != 38 || !ran[22] || requests[22] != 40 || ran[21] || requests[21] != 0 || !ran[0] || requests[0] != 5 {
		t.Errorf("ran = %v, requests = %v", ran, requests)
	}
}
