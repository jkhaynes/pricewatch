package store

import (
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

func fp(v float64) *float64 { return &v }

type spec struct{ name, number, variant, sourceID string }

// seed puts each spec into the collection and card_map (resolved at "pw") and returns keys by spec order.
func seed(t *testing.T, s *SQLite, specs ...spec) []string {
	t.Helper()
	ctx := t.Context()
	var rows []card.Row
	for _, sp := range specs {
		rows = append(rows, row(sp.name, sp.number, sp.variant))
	}
	if err := s.ReplaceCollection(ctx, rows); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for i, r := range rows {
		v, _ := card.ParseVariant(specs[i].variant)
		m := card.Mapping{Key: r.Key(), Source: "pw", SourceCardID: specs[i].sourceID, Variant: v, Status: card.StatusResolved}
		if err := s.PutMapping(ctx, m); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, r.Key())
	}
	return keys
}

func observe(t *testing.T, s *SQLite, runID int64, key string, market *float64) {
	t.Helper()
	o := card.Observation{CardID: key, Source: "pw", Price: card.Price{Market: market}, ObservedAt: time.Now()}
	if err := s.Save(t.Context(), runID, o); err != nil {
		t.Fatal(err)
	}
}

func sourceIDs(ms []card.Mapping) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.SourceCardID)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStalestSelectsWholeSourceCardsAndContinues(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	k := seed(t, s,
		spec{"A", "1/109", "Normal", "pk_A"},
		spec{"A", "1/109", "Reverse Holo", "pk_A"},
		spec{"B", "2/109", "Normal", "pk_B"},
		spec{"C", "3/109", "Normal", "pk_C"},
	)

	// Budget of 2 source cards: pk_A (both variants) and pk_B. That is 3 keys for 2 requests.
	first, err := s.Stalest(ctx, "pw", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceIDs(first); !equal(got, []string{"pk_A", "pk_A", "pk_B"}) {
		t.Fatalf("run 1 = %v", got)
	}
	r1, _ := s.StartRun(ctx)
	for _, key := range k[:3] {
		observe(t, s, r1, key, fp(1))
	}

	// Next run continues: pk_C was never seen, then pk_A is the oldest.
	second, err := s.Stalest(ctx, "pw", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceIDs(second); !equal(got, []string{"pk_C", "pk_A", "pk_A"}) {
		t.Fatalf("run 2 = %v, want [pk_C pk_A pk_A]", got)
	}
}

func TestStalestTreatsPartlySeenCardAsNeverSeen(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	k := seed(t, s,
		spec{"A", "1/109", "Normal", "pk_A"},
		spec{"B", "2/109", "Normal", "pk_B"},
	)
	r1, _ := s.StartRun(ctx)
	observe(t, s, r1, k[0], fp(1))
	observe(t, s, r1, k[1], fp(1))

	// A Reverse Holo copy of A is added to the collection later and has never been priced.
	rows := []card.Row{row("A", "1/109", "Normal"), row("B", "2/109", "Normal"), row("A", "1/109", "Reverse Holo")}
	if err := s.ReplaceCollection(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMapping(ctx, card.Mapping{Key: rows[2].Key(), Source: "pw", SourceCardID: "pk_A", Variant: card.VariantReverseHolo, Status: card.StatusResolved}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stalest(ctx, "pw", 1)
	if err != nil {
		t.Fatal(err)
	}
	if ids := sourceIDs(got); !equal(ids, []string{"pk_A", "pk_A"}) {
		t.Errorf("Stalest = %v, want pk_A first because its new variant was never seen", ids)
	}
}

// DD-8: export price breaks staleness ties; it never outranks staleness.
func TestStalestBreaksTiesByExportPrice(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	priced := func(name, number, variant string, price *float64) card.Row {
		r := row(name, number, variant)
		r.Price = price
		return r
	}
	rows := []card.Row{
		priced("Cheap", "1/109", "Normal", fp(0.06)),
		priced("Rich", "2/109", "Normal", fp(1.00)),
		priced("Rich", "2/109", "Reverse Holo", fp(400.00)), // a card is worth its most valuable variant
		priced("Mid", "3/109", "Normal", fp(5.00)),
		priced("Unpriced", "4/109", "Normal", nil), // counts as 0
	}
	if err := s.ReplaceCollection(ctx, rows); err != nil {
		t.Fatal(err)
	}
	ids := []string{"pk_cheap", "pk_rich", "pk_rich", "pk_mid", "pk_none"}
	for i, r := range rows {
		v, _ := card.ParseVariant(r.Variant)
		if err := s.PutMapping(ctx, card.Mapping{Key: r.Key(), Source: "pw", SourceCardID: ids[i], Variant: v, Status: card.StatusResolved}); err != nil {
			t.Fatal(err)
		}
	}

	// First pass: everything is tied at "never seen", so value decides.
	got, err := s.Stalest(ctx, "pw", 10)
	if err != nil {
		t.Fatal(err)
	}
	if order := sourceIDs(got); !equal(order, []string{"pk_rich", "pk_rich", "pk_mid", "pk_cheap", "pk_none"}) {
		t.Fatalf("first pass = %v, want most valuable first", order)
	}

	// Once observed, staleness wins: the cheap card priced first is due before the rich one.
	r1, _ := s.StartRun(ctx)
	for _, r := range rows { // Cheap is observed first, so it becomes the stalest
		observe(t, s, r1, r.Key(), fp(1))
	}
	next, err := s.Stalest(ctx, "pw", 1)
	if err != nil {
		t.Fatal(err)
	}
	if order := sourceIDs(next); !equal(order, []string{"pk_cheap"}) {
		t.Errorf("after observing = %v, want pk_cheap (stalest) despite its low value", order)
	}
}

func TestStalestSkipsUnresolvedAndOtherSources(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	k := seed(t, s, spec{"A", "1/109", "Normal", "pk_A"}, spec{"B", "2/109", "Normal", "pk_B"})
	if err := s.PutMapping(ctx, card.Mapping{Key: k[1], Source: "pw", Status: card.StatusAmbiguous}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMapping(ctx, card.Mapping{Key: k[1], Source: "other", SourceCardID: "x", Status: card.StatusResolved}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stalest(ctx, "pw", 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := sourceIDs(got); !equal(ids, []string{"pk_A"}) {
		t.Errorf("Stalest = %v, want only pk_A", ids)
	}
}

func TestSaveRejectsSecondObservationInSameRun(t *testing.T) {
	s := openTest(t)
	k := seed(t, s, spec{"A", "1/109", "Normal", "pk_A"})
	r, _ := s.StartRun(t.Context())
	observe(t, s, r, k[0], fp(1))
	o := card.Observation{CardID: k[0], Source: "pw", Price: card.Price{Market: fp(2)}, ObservedAt: time.Now()}
	if err := s.Save(t.Context(), r, o); err == nil {
		t.Error("want error for duplicate (run, card)")
	}
}

func TestChangesCompareAgainstCardsOwnPreviousObservation(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	k := seed(t, s,
		spec{"A", "1/109", "Normal", "pk_A"}, spec{"B", "2/109", "Normal", "pk_B"},
		spec{"C", "3/109", "Normal", "pk_C"}, spec{"D", "4/109", "Normal", "pk_D"})

	r1, _ := s.StartRun(ctx)
	observe(t, s, r1, k[0], fp(1.00)) // A: seen in run 1, skipped in run 2
	observe(t, s, r1, k[1], fp(5.00))

	r2, _ := s.StartRun(ctx)
	observe(t, s, r2, k[2], fp(0.06)) // C: first seen in run 2

	r3, _ := s.StartRun(ctx)
	observe(t, s, r3, k[0], fp(1.50)) // A: previous is run 1 (+50%), not "the previous run"
	observe(t, s, r3, k[1], fp(5.00)) // B: unchanged
	observe(t, s, r3, k[2], fp(0.20)) // C: previous is run 2 (+233%)
	observe(t, s, r3, k[3], fp(9.99)) // D: first sighting, so no change

	changes, err := s.Changes(ctx, r3)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes: %+v", len(changes), changes)
	}
	if changes[0].CardID != k[2] || *changes[0].Previous.Market != 0.06 {
		t.Errorf("first change = %+v", changes[0])
	}
	if changes[1].CardID != k[0] || *changes[1].Previous.Market != 1.00 {
		t.Errorf("second change = %+v", changes[1])
	}
}

func TestFinishRunRecordsCounts(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	r, _ := s.StartRun(ctx)
	if err := s.FinishRun(ctx, r, 7, 2); err != nil {
		t.Fatal(err)
	}
	var ok, failed int
	var finished *time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT ok_count, error_count, finished_at FROM runs WHERE id=?`, r).Scan(&ok, &failed, &finished); err != nil {
		t.Fatal(err)
	}
	if ok != 7 || failed != 2 || finished == nil {
		t.Errorf("ok=%d failed=%d finished=%v", ok, failed, finished)
	}
}
