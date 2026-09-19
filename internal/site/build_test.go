package site

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/priority"
)

type fakeStore struct {
	listings []card.Listing
	history  []card.Observation
	cands    []card.Candidate
	unres    []card.Mapping
	runs     []card.Run
	used     int
	day      string // the day QuotaUsed was asked about
}

func (f *fakeStore) Listings(ctx context.Context, _ string) ([]card.Listing, error) {
	return f.listings, ctx.Err()
}
func (f *fakeStore) History(ctx context.Context, _ string) ([]card.Observation, error) {
	return f.history, ctx.Err()
}
func (f *fakeStore) Candidates(ctx context.Context, _ string) ([]card.Candidate, error) {
	return f.cands, ctx.Err()
}
func (f *fakeStore) Unresolved(ctx context.Context, _ string) ([]card.Mapping, error) {
	return f.unres, ctx.Err()
}
func (f *fakeStore) RunsSince(ctx context.Context, _ time.Time) ([]card.Run, error) {
	return f.runs, ctx.Err()
}
func (f *fakeStore) QuotaUsed(ctx context.Context, _, day string) (int, error) {
	f.day = day
	return f.used, ctx.Err()
}

func TestBuildWiresEverySection(t *testing.T) {
	p10, p12 := 10.0, 12.0
	st := &fakeStore{
		listings: []card.Listing{{Key: "a", Name: "Mudkip", Quantity: 1, Export: &p10}, {Key: "u", Name: "Tropius"}},
		history: []card.Observation{
			{CardID: "a", Price: card.Price{Market: &p10}, ObservedAt: now.Add(-8 * day)},
			{CardID: "a", Price: card.Price{Market: &p12}, ObservedAt: now.Add(-day)},
		},
		cands: []card.Candidate{{SourceCardID: "pk_a", Keys: []card.Mapping{{Key: "a"}}, LastChecked: now.Add(-day), Value: 12}},
		unres: []card.Mapping{{Key: "u", Reason: `unsupported variant: "Cosmos Holo"`}},
		used:  612,
	}
	d, err := Build(t.Context(), st, Options{Source: "pw", Now: now, DailyLimit: 1000, RunMinute: 7, Policy: priority.Default})
	if err != nil {
		t.Fatal(err)
	}
	if d.Spotlight == nil || d.Spotlight.Name != "Mudkip" || d.Index.Week == nil || d.Coverage.Priced != 1 || d.Coverage.Total != 2 {
		t.Errorf("movers/index/coverage not wired: %+v", d)
	}
	if len(d.Bands) != 4 || d.UnresolvedTotal != 1 || len(d.Unresolved) != 1 || len(d.Runs) != 24 {
		t.Errorf("bands/unresolved/runs not wired: bands=%d unresolved=%d/%d runs=%d", len(d.Bands), d.UnresolvedTotal, len(d.Unresolved), len(d.Runs))
	}
	if d.Budget.Used != 612 || d.Budget.Limit != 1000 || st.day != "2026-09-20" || d.RunMinute != 7 {
		t.Errorf("budget = %+v for day %q, runMinute %d", d.Budget, st.day, d.RunMinute)
	}
}

func TestRenderEmbedsDataSafely(t *testing.T) {
	d := Data{RunMinute: 7, Spotlight: &Mover{Name: `</script><script>alert(1)</script>`}}
	var buf bytes.Buffer
	if err := Render(&buf, d); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	if strings.Contains(page, placeholder) {
		t.Error("placeholder left in the page")
	}
	if !strings.Contains(page, `"runMinute":7`) {
		t.Error("data not embedded")
	}
	if strings.Contains(page, "<script>alert(1)") {
		t.Error("a card name closed the data script: must be escaped")
	}
}
