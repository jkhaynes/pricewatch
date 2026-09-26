package ask

import (
	"slices"
	"strings"
	"testing"
)

func TestFindCardsNeverMergesVariants(t *testing.T) {
	st := collection(t)
	tests := []struct {
		name string
		in   FindIn
		want []string
	}{
		{"both prints of a name, any case", FindIn{Name: "MUDKIP"}, []string{"Mudkip Normal", "Mudkip Reverse Holo"}},
		{"part of a name", FindIn{Name: "tro"}, []string{"Tropius Normal"}},
		{"number narrows", FindIn{Name: "mudkip", Number: "59/109"}, []string{"Mudkip Normal", "Mudkip Reverse Holo"}},
		{"wrong number finds nothing", FindIn{Name: "mudkip", Number: "59"}, nil},
		{"expansion narrows", FindIn{Name: "t", Expansion: "unseen forces"}, []string{"Treecko Normal", "Tropius Normal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testTools().findCards(t.Context(), st, tt.in)
			if err != nil {
				t.Fatal(err)
			}
			names := labels(got.Cards)
			slices.Sort(names)
			if !slices.Equal(names, tt.want) {
				t.Errorf("found %v, want %v", names, tt.want)
			}
		})
	}

	got, err := testTools().findCards(t.Context(), st, FindIn{Name: "mudkip"})
	if err != nil {
		t.Fatal(err)
	}
	prices := map[string]float64{}
	for _, c := range got.Cards {
		prices[c.Variant] = *c.Price
	}
	if prices["Normal"] != 1.50 || prices["Reverse Holo"] != 4.00 || got.Cards[0].Key == got.Cards[1].Key {
		t.Errorf("each print must keep its own key and price: %+v", got.Cards)
	}
}

func TestFindCardsNeedsAName(t *testing.T) {
	if _, err := testTools().findCards(t.Context(), collection(t), FindIn{Name: "  "}); err == nil {
		t.Error("a blank name matched every card")
	}
}

func TestUnpricedCardSaysSo(t *testing.T) {
	got, err := testTools().findCards(t.Context(), collection(t), FindIn{Name: "treecko"})
	if err != nil {
		t.Fatal(err)
	}
	if c := got.Cards[0]; c.Price != nil || c.Value != nil || c.Status != "resolved" {
		t.Errorf("treecko = %+v, want resolved with no price", c)
	}
}

func TestPriceHistory(t *testing.T) {
	st := collection(t)
	got, err := testTools().priceHistory(t.Context(), st, HistoryIn{Key: mudkip.Key()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Card.Variant != "Normal" || len(got.Prices) != 2 || got.Prices[0].Price != 1.00 || got.Prices[1].Price != 1.50 {
		t.Errorf("history = %+v, want Normal's two prices, oldest first", got)
	}
	_, err = testTools().priceHistory(t.Context(), st, HistoryIn{Key: "Mudkip"})
	if err == nil || !strings.Contains(err.Error(), "find_cards") {
		t.Errorf("unknown key: err = %v, want a pointer to find_cards", err)
	}
}
