package pokewallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/pipeline"
	"github.com/jkhaynes/pricewatch/internal/resolve"
	"github.com/jkhaynes/pricewatch/internal/source"
)

// Compile-time proof that the provider and the shared client fit the
// consumer-side interfaces they are wired into.
var (
	_ resolve.Catalog      = (*Provider)(nil)
	_ pipeline.PriceSource = (*Provider)(nil)
	_ Getter               = (*source.Client)(nil)
)

// fakeGetter answers from canned JSON keyed by path; trimmed from the 2026-09-18 spike.
type fakeGetter map[string]string

func (f fakeGetter) GetJSON(_ context.Context, path string, v any) error {
	body, ok := f[path]
	if !ok {
		return fmt.Errorf("GET %s: %w", path, card.ErrNotFound)
	}
	return json.Unmarshal([]byte(body), v)
}

func TestSets(t *testing.T) {
	g := fakeGetter{"/sets": `{"success":true,"data":[
		{"name":"Ruby and Sapphire","set_code":"RS","set_id":"1393","language":"eng"},
		{"name":"SV06: Twilight Masquerade","set_code":"TWM","set_id":"23473","language":"eng"},
		{"name":"SM - Guardians Rising","set_code":"SM02","set_id":"1919","language":"eng"},
		{"name":"Unbroken Bonds","set_code":"UNB","set_id":"-185","language":"eng"},
		{"name":"Vaporeon VMAX Promo","set_code":null,"set_id":"24073","language":"jap"}]}`}
	sets, err := New(g).Sets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Both prefix styles PokeWallet uses get a bare alias. Negative set IDs are
	// CardMarket-only sets with no TCGplayer prices, so phase 1 never matches them.
	want := []card.SourceSet{
		{ID: "1393", Names: []string{"Ruby and Sapphire"}},
		{ID: "23473", Names: []string{"SV06: Twilight Masquerade", "Twilight Masquerade"}},
		{ID: "1919", Names: []string{"SM - Guardians Rising", "Guardians Rising"}},
	}
	if !slices.EqualFunc(sets, want, func(a, b card.SourceSet) bool { return a.ID == b.ID && slices.Equal(a.Names, b.Names) }) {
		t.Errorf("Sets = %+v", sets)
	}
}

func TestCardsPaginatesAndCleans(t *testing.T) {
	g := fakeGetter{
		"/sets/1393?page=1&limit=50": `{"cards":[
			{"id":"pk_59","card_info":{"name":"Mudkip - 59/109","card_number":"59/109"}},
			{"id":"pk_3","card_info":{"name":"Blaziken","card_number":"3/109"}}],
			"pagination":{"page":1,"total_pages":2}}`,
		"/sets/1393?page=2&limit=50": `{"cards":[
			{"id":"pk_100","card_info":{"name":"Magmar ex","card_number":"100/109"}}],
			"pagination":{"page":2,"total_pages":2}}`,
	}
	cards, err := New(g).Cards(t.Context(), "1393")
	if err != nil {
		t.Fatal(err)
	}
	want := []card.SourceCard{
		{ID: "pk_59", Number: "59", Name: "Mudkip"},
		{ID: "pk_3", Number: "3", Name: "Blaziken"},
		{ID: "pk_100", Number: "100", Name: "Magmar ex"},
	}
	if !slices.Equal(cards, want) {
		t.Errorf("Cards = %+v", cards)
	}
}

func TestQuote(t *testing.T) {
	g := fakeGetter{
		"/cards/pk_59": `{"id":"pk_59","tcgplayer":{"prices":[
			{"sub_type_name":"Normal","low_price":3,"high_price":38.58,"market_price":8.22},
			{"sub_type_name":"Reverse Holofoil","low_price":74.99,"high_price":579.88,"market_price":50.47}]}}`,
		"/cards/pk_bss4": `{"id":"pk_bss4","tcgplayer":{"prices":[
			{"sub_type_name":"1st Edition Holofoil","market_price":10000},
			{"sub_type_name":"Unlimited Holofoil","market_price":2257.87}]}}`,
		"/cards/pk_cm": `{"id":"pk_cm","tcgplayer":null}`,
	}
	p := New(g)
	tests := []struct {
		name     string
		id       string
		variants []card.Variant
		want     []any // *float64 market, or error sentinel
	}{
		{"one request, both variants", "pk_59", []card.Variant{card.VariantNormal, card.VariantReverseHolo},
			[]any{8.22, 50.47}},
		{"shadowless 1st edition and unlimited", "pk_bss4", []card.Variant{card.VariantFirstEditionHolo, card.VariantHolo},
			[]any{10000.0, 2257.87}},
		{"missing variant is per-variant, not per-request", "pk_59", []card.Variant{card.VariantHolo, card.VariantNormal},
			[]any{card.ErrVariantUnavailable, 8.22}},
		{"no tcgplayer block", "pk_cm", []card.Variant{card.VariantNormal}, []any{card.ErrVariantUnavailable}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs, err := p.Quote(t.Context(), tt.id, tt.variants)
			if err != nil {
				t.Fatal(err)
			}
			if len(qs) != len(tt.want) {
				t.Fatalf("got %d quotes", len(qs))
			}
			for i, w := range tt.want {
				switch w := w.(type) {
				case float64:
					if qs[i].Err != nil || qs[i].Price.Market == nil || *qs[i].Price.Market != w {
						t.Errorf("quote %d = %+v, want market %v", i, qs[i], w)
					}
				case error:
					if !errors.Is(qs[i].Err, w) {
						t.Errorf("quote %d err = %v, want %v", i, qs[i].Err, w)
					}
				}
			}
		})
	}
}

func TestQuoteUnknownCardIsRequestError(t *testing.T) {
	_, err := New(fakeGetter{}).Quote(t.Context(), "pk_nope", []card.Variant{card.VariantNormal})
	if !errors.Is(err, card.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
