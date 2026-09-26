package ask

import (
	"slices"
	"testing"
)

func TestCollectionValue(t *testing.T) {
	st := collection(t)
	tests := []struct {
		name         string
		expansion    string
		wantTotal    float64
		wantPriced   int
		wantUnpriced int
		wantExps     []string // largest value first
		wantErr      bool
	}{
		{name: "whole collection", wantTotal: 7.18, wantPriced: 3, wantUnpriced: 1,
			wantExps: []string{"EX Ruby & Sapphire", "Unseen Forces"}},
		{name: "one expansion, any case", expansion: "unseen forces", wantTotal: 0.18, wantPriced: 1, wantUnpriced: 1,
			wantExps: []string{"Unseen Forces"}},
		{name: "unknown expansion is an error, not $0", expansion: "Nope", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testTools().collectionValue(t.Context(), st, ValueIn{Expansion: tt.expansion})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error: %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Total != tt.wantTotal || got.Priced != tt.wantPriced || got.Unpriced != tt.wantUnpriced {
				t.Errorf("got total %v, %d priced, %d unpriced; want %v, %d, %d",
					got.Total, got.Priced, got.Unpriced, tt.wantTotal, tt.wantPriced, tt.wantUnpriced)
			}
			var exps []string
			for _, e := range got.Expansions {
				exps = append(exps, e.Expansion)
			}
			if !slices.Equal(exps, tt.wantExps) {
				t.Errorf("expansions = %v, want %v", exps, tt.wantExps)
			}
		})
	}
}
