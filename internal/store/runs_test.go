package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/pipeline"
)

// Compile-time proof that the store fits the runner's consumer-side interface.
var _ pipeline.Store = (*SQLite)(nil)

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

func byID(cs []card.Candidate) map[string]card.Candidate {
	out := map[string]card.Candidate{}
	for _, c := range cs {
		out[c.SourceCardID] = c
	}
	return out
}

func TestCandidatesGroupRowsAndSummariseThem(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	priced := func(name, number, variant string, price *float64) card.Row {
		r := row(name, number, variant)
		r.Price = price
		return r
	}
	rows := []card.Row{
		priced("A", "1/109", "Normal", fp(1.00)),
		priced("A", "1/109", "Reverse Holo", fp(3.00)), // same source card as the Normal
		priced("B", "2/109", "Normal", fp(9.00)),
		priced("C", "3/109", "Normal", nil),
	}
	if err := s.ReplaceCollection(ctx, rows); err != nil {
		t.Fatal(err)
	}
	ids := []string{"pk_A", "pk_A", "pk_B", "pk_C"}
	for i, r := range rows {
		v, _ := card.ParseVariant(r.Variant)
		if err := s.PutMapping(ctx, card.Mapping{Key: r.Key(), Source: "pw", SourceCardID: ids[i], Variant: v, Status: card.StatusResolved}); err != nil {
			t.Fatal(err)
		}
	}
	t1 := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(24 * time.Hour)
	r1, _ := s.StartRun(ctx)
	save := func(runID int64, key string, market *float64, at time.Time) {
		if err := s.Save(ctx, runID, card.Observation{CardID: key, Source: "pw", Price: card.Price{Market: market}, ObservedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	save(r1, rows[0].Key(), fp(50), t1) // A Normal: market 50 beats its $1 export price
	save(r1, rows[1].Key(), fp(2), t1)
	save(r1, rows[2].Key(), nil, t1) // B: priced, but no market price: keep the export price
	r2, _ := s.StartRun(ctx)
	save(r2, rows[0].Key(), fp(60), t2) // A Normal again; A Reverse is still at t1

	got, err := s.Candidates(ctx, "pw")
	if err != nil {
		t.Fatal(err)
	}
	c := byID(got)
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3: %+v", len(got), got)
	}
	a := c["pk_A"]
	if len(a.Keys) != 2 || a.NeverSeen || !a.LastChecked.Equal(t1) || a.Value != 60 {
		t.Errorf("A = keys %d, neverSeen %v, lastChecked %v, value %v; want 2, false, %v (its oldest row), 60 (latest market)",
			len(a.Keys), a.NeverSeen, a.LastChecked, a.Value, t1)
	}
	if b := c["pk_B"]; b.NeverSeen || b.Value != 9 {
		t.Errorf("B = neverSeen %v, value %v; want false, 9 (export price, since no market price)", b.NeverSeen, b.Value)
	}
	if cc := c["pk_C"]; !cc.NeverSeen || cc.Value != 0 {
		t.Errorf("C = neverSeen %v, value %v; want true, 0", cc.NeverSeen, cc.Value)
	}
}

func TestCandidatesAPartlyPricedCardIsNeverSeen(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	k := seed(t, s, spec{"A", "1/109", "Normal", "pk_A"}, spec{"A", "1/109", "Reverse Holo", "pk_A"})
	r1, _ := s.StartRun(ctx)
	observe(t, s, r1, k[0], fp(1)) // only the Normal
	got, err := s.Candidates(ctx, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].NeverSeen {
		t.Errorf("a card with an unpriced row must count as never seen: %+v", got)
	}
}

func TestCandidatesSkipUnresolvedOtherSourcesAndRemovedRows(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	k := seed(t, s, spec{"A", "1/109", "Normal", "pk_A"}, spec{"B", "2/109", "Normal", "pk_B"})
	if err := s.PutMapping(ctx, card.Mapping{Key: k[1], Source: "pw", Status: card.StatusAmbiguous}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMapping(ctx, card.Mapping{Key: k[1], Source: "other", SourceCardID: "x", Status: card.StatusResolved}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMapping(ctx, card.Mapping{Key: "gone|from|the|collection", Source: "pw", SourceCardID: "pk_gone", Status: card.StatusResolved}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Candidates(ctx, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceCardID != "pk_A" {
		t.Errorf("Candidates = %+v, want only pk_A", got)
	}
}

// At the real collection's scale (about 8,800 rows) selection must stay fast:
// phase 1's Stalest once took 8 minutes when SQLite mis-planned a CTE.
func TestCandidatesAreFastAtCollectionScale(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	const n = 8000
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		key := fmt.Sprintf("international|set %d|%d/200|normal|english", i/200, i%200)
		if _, err := tx.ExecContext(ctx, `INSERT INTO collection (collection_key, tcg_region, card_name, card_number, expansion, variant, tcgc_price)
			VALUES (?, 'International', 'Card', '1/1', 'Set', 'Normal', ?)`, key, float64(i%97)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO card_map (collection_key, source, source_card_id, variant, status)
			VALUES (?, 'pw', ?, 'normal', 'resolved')`, key, fmt.Sprintf("pk_%05d", i/2)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	got, err := s.Candidates(ctx, "pw")
	if err != nil || len(got) != n/2 {
		t.Fatalf("Candidates = %d, %v; want %d candidates within 3 s", len(got), err, n/2)
	}
}
