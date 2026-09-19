package store

import (
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/site"
)

// Compile-time proof that the store fits the page builder's consumer-side interface.
var _ site.Store = (*SQLite)(nil)

func TestListingsDescribeEveryCollectionKey(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	normal := row("Mudkip", "59/109", "Normal")
	normal.Price, normal.Quantity = fp(8), 2
	second := normal // a second copy of the same key
	second.Price, second.Quantity = fp(6), 1
	special := row("Mudkip", "59/109", "Poké Ball Reverse Holo")
	special.Price = fp(3)
	if err := s.ReplaceCollection(ctx, []card.Row{normal, second, special}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMapping(ctx, card.Mapping{Key: normal.Key(), Source: "pw", SourceCardID: "pk_59", Variant: card.VariantNormal, Status: card.StatusResolved}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutArt(ctx, "pw", "pk_59", "https://img/59.jpg"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Listings(ctx, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d listings, want one per distinct key: %+v", len(got), got)
	}
	byKey := map[string]card.Listing{}
	for _, l := range got {
		byKey[l.Key] = l
	}
	n := byKey[normal.Key()]
	if n.Name != "Mudkip" || n.Number != "59/109" || n.VariantLabel != "Normal" || n.Expansion != "EX Ruby & Sapphire" ||
		n.Export == nil || *n.Export != 8 || n.Quantity != 3 || n.Status != card.StatusResolved ||
		n.SourceCardID != "pk_59" || n.Image != "https://img/59.jpg" {
		t.Errorf("resolved listing = %+v", n)
	}
	if sp := byKey[special.Key()]; sp.Status != "" || sp.Image != "" || sp.VariantLabel != "Poké Ball Reverse Holo" {
		t.Errorf("unmapped listing = %+v; want no status and no art", sp)
	}
}

func TestHistoryHasMarketPricesInOrder(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	k := seed(t, s, spec{"A", "1/109", "Normal", "pk_A"}, spec{"B", "2/109", "Normal", "pk_B"})
	t1 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	save := func(key string, market *float64, at time.Time) {
		r, _ := s.StartRun(ctx)
		if err := s.Save(ctx, r, card.Observation{CardID: key, Source: "pw", Price: card.Price{Market: market}, ObservedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	save(k[1], fp(5), t1.Add(time.Hour))
	save(k[0], fp(2), t1.Add(48*time.Hour))
	save(k[0], fp(1), t1)
	save(k[0], nil, t1.Add(24*time.Hour)) // priced, but no market price: not history
	save("gone|from|collection", fp(9), t1)

	got, err := s.History(ctx, "pw")
	if err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, o := range got {
		seq = append(seq, o.CardID+"@"+o.ObservedAt.UTC().Format("02T15"))
	}
	want := []string{k[0] + "@10T10", k[0] + "@12T10", k[1] + "@10T11"}
	if len(seq) != len(want) {
		t.Fatalf("History = %v, want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Errorf("History[%d] = %s, want %s", i, seq[i], want[i])
		}
	}
	if *got[1].Market != 2 {
		t.Errorf("market = %v, want 2", *got[1].Market)
	}
}

func TestRunsSinceIncludesUnfinishedRuns(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	before := time.Now().UTC().Add(-time.Minute)
	done, _ := s.StartRun(ctx)
	if err := s.FinishRun(ctx, done, 1, 0, 38); err != nil {
		t.Fatal(err)
	}
	s.StartRun(ctx) // never finished, as after a crash

	got, err := s.RunsSince(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Requests != 38 || got[0].FinishedAt == nil || got[1].FinishedAt != nil {
		t.Errorf("RunsSince = %+v", got)
	}
	if later, _ := s.RunsSince(ctx, time.Now().UTC().Add(time.Hour)); len(later) != 0 {
		t.Errorf("RunsSince(future) = %+v, want none", later)
	}
}
