package ask

import (
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

func TestPipelineStatus(t *testing.T) {
	st := collection(t)
	ctx := t.Context()
	now := time.Now().UTC() // the fixture's runs are stamped with the real clock
	if err := st.PutMapping(ctx, card.Mapping{Key: treecko.Key(), Source: "pw", Status: card.StatusUnmatched,
		Reason: "unknown expansion"}); err != nil {
		t.Fatal(err)
	}
	if err := st.QuotaAdd(ctx, "pw", now.Format(time.DateOnly), 7); err != nil {
		t.Fatal(err)
	}
	tt := testTools()
	tt.now = func() time.Time { return now }

	got, err := tt.pipelineStatus(ctx, st, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Keys != 4 || got.Priced != 3 {
		t.Errorf("keys %d priced %d, want 4 and 3", got.Keys, got.Priced)
	}
	if got.Mappings["resolved"] != 3 || got.Mappings["unmatched"] != 1 {
		t.Errorf("mappings = %v", got.Mappings)
	}
	if len(got.Unresolved) != 1 || got.Unresolved[0].Label != "Unknown expansion" || got.Unresolved[0].Count != 1 {
		t.Errorf("unresolved = %+v", got.Unresolved)
	}
	if got.QuotaToday != 7 || got.Runs != 4 || got.Requests != 4 {
		t.Errorf("quota %d, runs %d, requests %d; want 7, 4, 4", got.QuotaToday, got.Runs, got.Requests)
	}
}
