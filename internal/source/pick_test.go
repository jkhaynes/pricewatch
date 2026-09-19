package source

import (
	"errors"
	"testing"

	"github.com/jkhaynes/pricewatch/internal/card"
)

func f(v float64) *float64 { return &v }

// PokéWallet sub-type names, from the 2026-09-18 spike.
var pw = map[card.Variant][]string{
	card.VariantNormal:           {"Normal", "Unlimited"},
	card.VariantHolo:             {"Holofoil", "Unlimited Holofoil"},
	card.VariantReverseHolo:      {"Reverse Holofoil"},
	card.VariantFirstEdition:     {"1st Edition"},
	card.VariantFirstEditionHolo: {"1st Edition Holofoil"},
}

func TestQuotes(t *testing.T) {
	mudkip := map[string]card.Price{"Normal": {Market: f(8.22)}, "Reverse Holofoil": {Market: f(50.47)}}
	lugia := map[string]card.Price{"1st Edition Holofoil": {Market: f(1079.79)}, "Unlimited Holofoil": {Market: f(518.99)}}
	sunflora := map[string]card.Price{"1st Edition": {Market: f(3.93)}, "Unlimited": {Market: f(2.23)}}
	charizard := map[string]card.Price{"Holofoil": {Market: f(882.02)}}
	both := map[string]card.Price{"Normal": {Market: f(1)}, "Unlimited": {Market: f(2)}}
	nullMarket := map[string]card.Price{"Holofoil": {Market: nil}}

	tests := []struct {
		name       string
		available  map[string]card.Price
		variant    card.Variant
		wantMarket *float64
		wantErr    error
	}{
		{"normal vs reverse: normal", mudkip, card.VariantNormal, f(8.22), nil},
		{"normal vs reverse: reverse", mudkip, card.VariantReverseHolo, f(50.47), nil},
		{"1st edition holo", lugia, card.VariantFirstEditionHolo, f(1079.79), nil},
		{"unlimited holo is Holo", lugia, card.VariantHolo, f(518.99), nil},
		{"1st edition non-holo", sunflora, card.VariantFirstEdition, f(3.93), nil},
		{"unlimited non-holo is Normal", sunflora, card.VariantNormal, f(2.23), nil},
		{"variant not offered", charizard, card.VariantReverseHolo, nil, card.ErrVariantUnavailable},
		{"two names match one variant", both, card.VariantNormal, nil, card.ErrVariantAmbiguous},
		{"variant with no mapping", mudkip, card.Variant("jumbo"), nil, card.ErrVariantUnavailable},
		{"null market is a price, not an error", nullMarket, card.VariantHolo, nil, nil},
		{"no prices at all", nil, card.VariantNormal, nil, card.ErrVariantUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs := Quotes(tt.available, pw, []card.Variant{tt.variant})
			if len(qs) != 1 || qs[0].Variant != tt.variant {
				t.Fatalf("quotes = %+v", qs)
			}
			q := qs[0]
			if !errors.Is(q.Err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", q.Err, tt.wantErr)
			}
			if !eqPtr(q.Price.Market, tt.wantMarket) {
				t.Errorf("Market = %v, want %v", q.Price.Market, tt.wantMarket)
			}
		})
	}
}

func TestQuotesKeepsRequestOrder(t *testing.T) {
	mudkip := map[string]card.Price{"Normal": {Market: f(8.22)}, "Reverse Holofoil": {Market: f(50.47)}}
	qs := Quotes(mudkip, pw, []card.Variant{card.VariantReverseHolo, card.VariantHolo, card.VariantNormal})
	if len(qs) != 3 || qs[0].Variant != card.VariantReverseHolo || qs[1].Err == nil || *qs[2].Price.Market != 8.22 {
		t.Errorf("quotes = %+v", qs)
	}
}

func eqPtr(a, b *float64) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
