package pokewallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

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
	// SourceCard holds a slice (Aliases), so it is not comparable with ==.
	if !reflect.DeepEqual(cards, want) {
		t.Errorf("Cards = %+v", cards)
	}
}

// Qualifiers seen in the real import (2026-09-19). Only those naming the card's
// own print get a bare-name alias. Pattern prints, error prints and special
// releases share a number with a different card, so aliasing them would price
// the wrong print.
func TestCardsQualifierAliases(t *testing.T) {
	tests := []struct {
		name        string
		wantAliases []string
	}{
		{"Whismur (117)", []string{"Whismur"}},
		{"Gardenia (Full Art)", []string{"Gardenia"}},
		{"Pikachu (Secret)", []string{"Pikachu"}},
		{"Pheromosa GX (Secret Rare)", []string{"Pheromosa GX"}},
		{"Mewtwo V (Alternate Full Art)", []string{"Mewtwo V"}},
		{"Fire Energy (Texture Full Art)", []string{"Fire Energy"}},
		{"Bulbasaur (Holo Common)", []string{"Bulbasaur"}},
		{"Zacian V (Shiny)", []string{"Zacian V"}},
		{"Mewtwo GX (Secret Shining)", []string{"Mewtwo GX"}},
		{"Sylveon VMAX (Alternate Art Secret)", []string{"Sylveon VMAX"}},
		{"Pansear (Poke Ball Pattern)", nil},
		{"Sewaddle (Master Ball Pattern)", nil},
		{"Charizard (Black Dot Error)", nil},
		{"N's Zekrom - 031 (Pokemon Center Exclusive)", nil},
		{"Feraligatr - 213 (Illustration Contest 2024)", nil},
		{"Glaceon ex - 026/131 (Holiday Calendar)", nil},
		{"Magmar ex", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"cards":      []any{map[string]any{"id": "pk_x", "card_info": map[string]any{"name": tt.name, "card_number": "117/168"}}},
				"pagination": map[string]any{"page": 1, "total_pages": 1},
			})
			cards, err := New(fakeGetter{"/sets/s?page=1&limit=50": string(body)}).Cards(t.Context(), "s")
			if err != nil {
				t.Fatal(err)
			}
			if got := cards[0].Aliases; !slices.Equal(got, tt.wantAliases) {
				t.Errorf("Aliases = %q, want %q", got, tt.wantAliases)
			}
			if cards[0].Name != tt.name {
				t.Errorf("Name = %q; the full name must be kept for exact matching and reports", cards[0].Name)
			}
		})
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

func TestConfigPacesAgainstTheServersHourlyCount(t *testing.T) {
	cfg := Config(DefaultBaseURL, "key", nil, time.Second)
	h := http.Header{}
	h.Set("X-RateLimit-Limit-Hour", "100")
	h.Set("X-RateLimit-Remaining-Hour", "37")
	if cfg.HourCount == nil {
		t.Fatal("HourCount not set")
	}
	if limit, remaining, ok := cfg.HourCount(h); !ok || limit != 100 || remaining != 37 {
		t.Errorf("HourCount = %d, %d, %v", limit, remaining, ok)
	}
	if cfg.Limits.PerSecond != 2 {
		t.Errorf("politeness cap = %v per second, want 2", cfg.Limits.PerSecond)
	}
}
