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

func TestTopCards(t *testing.T) {
	st := collection(t)
	tests := []struct {
		name          string
		in            TopIn
		want          []string // card names with variant, most valuable unit price first
		wantTruncated bool
		wantErr       bool
	}{
		{name: "default: every priced card", in: TopIn{},
			want: []string{"Mudkip Reverse Holo", "Mudkip Normal", "Tropius Normal"}},
		{name: "limit", in: TopIn{Limit: 1}, want: []string{"Mudkip Reverse Holo"}, wantTruncated: true},
		{name: "one expansion", in: TopIn{Expansion: "Unseen Forces"}, want: []string{"Tropius Normal"}},
		{name: "limit over 100", in: TopIn{Limit: 101}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testTools().topCards(t.Context(), st, tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error: %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if names := labels(got.Cards); !slices.Equal(names, tt.want) || got.Truncated != tt.wantTruncated {
				t.Errorf("got %v truncated=%v, want %v truncated=%v", names, got.Truncated, tt.want, tt.wantTruncated)
			}
		})
	}
}

func labels(cards []Card) []string {
	var out []string
	for _, c := range cards {
		out = append(out, c.Name+" "+c.Variant)
	}
	return out
}
