package pokewallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
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
		{"Charizard (Black Dot Error)", nil},
		{"N's Zekrom - 031 (Pokemon Center Exclusive)", nil},
		{"Feraligatr - 213 (Illustration Contest 2024)", nil},
		{"Glaceon ex - 026/131 (Holiday Calendar)", nil},
		{"Magmar ex", nil},
		// Several trailing qualifiers: strip them all, but only if every one is allowed.
		{"Gardevoir & Sylveon GX (205) (Alternate Full Art)", []string{"Gardevoir & Sylveon GX"}},
		{"Pikachu (Poke Ball Pattern) (Secret)", nil},
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

// Pattern reverse holos are separate products at the same number as the plain
// card. Names from the live listings (probe, 2026-09-27): SV sets say
// "(Poke Ball Pattern)", ME sets say "(Poke Ball)", and ME names may put the
// number suffix before the qualifier.
func TestCardsPatternPrints(t *testing.T) {
	tests := []struct {
		name, number string
		wantPrint    card.Print
		wantAliases  []string
	}{
		{"Exeggcute", "001/131", card.PrintStandard, nil},
		{"Exeggcute (Poke Ball Pattern)", "001/131", card.PrintPokeBall, []string{"Exeggcute"}},
		{"Exeggcute (Master Ball Pattern)", "001/131", card.PrintMasterBall, []string{"Exeggcute"}},
		{"Pansage (Master Ball Pattern)", "004/086", card.PrintMasterBall, []string{"Pansage"}},
		{"Erika's Oddish (Poke Ball)", "001/217", card.PrintPokeBall, []string{"Erika's Oddish"}},
		{"Erika's Tangela - 007/217 (Poke Ball)", "007/217", card.PrintPokeBall, []string{"Erika's Tangela"}},
		{"Chikorita (Friend Ball)", "008/217", card.PrintFriendBall, []string{"Chikorita"}},
		{"Chikorita (Quick Ball)", "008/217", card.PrintQuickBall, []string{"Chikorita"}},
		{"Chikorita (Love Ball)", "008/217", card.PrintLoveBall, []string{"Chikorita"}},
		{"Chikorita (Dusk Ball)", "008/217", card.PrintDuskBall, []string{"Chikorita"}},
		{"Chikorita (Energy Symbol Pattern)", "008/217", card.PrintEnergy, []string{"Chikorita"}},
		{"Team Rocket's Ekans (Team Rocket)", "050/217", card.PrintRocket, []string{"Team Rocket's Ekans"}},
		// A pattern over an own-print qualifier: both are stripped.
		{"Pikachu (Secret) (Poke Ball Pattern)", "117/168", card.PrintPokeBall, []string{"Pikachu"}},
		// A pattern over a different print: the pattern is known, the base is not
		// this card, so no alias and it can never match.
		{"Charizard (Black Dot Error) (Poke Ball Pattern)", "004/102", card.PrintPokeBall, nil},
		// Not a pattern: an unknown qualifier stays a standard, unmatchable product.
		{"Charizard (Black Dot Error)", "004/102", card.PrintStandard, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"cards":      []any{map[string]any{"id": "pk_x", "card_info": map[string]any{"name": tt.name, "card_number": tt.number}}},
				"pagination": map[string]any{"page": 1, "total_pages": 1},
			})
			cards, err := New(fakeGetter{"/sets/s?page=1&limit=50": string(body)}).Cards(t.Context(), "s")
			if err != nil {
				t.Fatal(err)
			}
			c := cards[0]
			if c.Print != tt.wantPrint || !slices.Equal(c.Aliases, tt.wantAliases) {
				t.Errorf("Print, Aliases = %q, %q; want %q, %q", c.Print, c.Aliases, tt.wantPrint, tt.wantAliases)
			}
			if c.Print != card.PrintStandard && c.Name != tt.name {
				t.Errorf("Name = %q; a pattern product keeps its full name for reports", c.Name)
			}
		})
	}
}

// Stamped promo prints (probe, 2026-09-29): the staff marker is a trailing
// "[Staff]" or "(Staff)", World Championships carry a year, and promo names pad
// the number ("Ampharos - 075" at card number 75).
func TestCardsStampedPrints(t *testing.T) {
	tests := []struct {
		name, number string
		wantPrint    card.Print
		wantAliases  []string
	}{
		{"Ceruledge (Prerelease)", "14", card.PrintPrerelease, []string{"Ceruledge"}},
		{"Ceruledge (Prerelease) [Staff]", "14", card.PrintPrereleaseStaff, []string{"Ceruledge"}},
		{"Chi-Yu - 057 (Prerelease)", "057", card.PrintPrerelease, []string{"Chi-Yu"}},
		{"Ledian - 133 (Prerelease) [Staff]", "133", card.PrintPrereleaseStaff, []string{"Ledian"}},
		{"Ampharos - 075 [Staff]", "75", card.PrintPrereleaseStaff, []string{"Ampharos"}},
		{"Alakazam - 003 (Staff)", "3", card.PrintPrereleaseStaff, []string{"Alakazam"}},
		{"Paradise Resort - 150 (World Championships 2024)", "150", card.PrintWorlds, []string{"Paradise Resort"}},
		{"Paradise Resort - 150 (World Championships 2024) [Staff]", "150", card.PrintWorldsStaff, []string{"Paradise Resort"}},
		{"Paradise Resort - 224 (World Championship 2025)", "224", card.PrintWorlds, []string{"Paradise Resort"}},
		{"Sylveon ex - 100 (30th Celebration)", "100", card.PrintAnniversary, []string{"Sylveon ex"}},
		// Unknown combinations stay standard and unmatchable.
		{"Nidorina - 101 (30th Celebration) (Pokemon Center Exclusive)", "101", card.PrintStandard, nil},
		{"Mew - 105 (30th Celebration) [Staff]", "105", card.PrintStandard, nil},
		{"Pikachu (Poke Ball Pattern) [Staff]", "117", card.PrintStandard, nil},
		// A staff print of a different print: staff is known, the base is not this card.
		{"Slowbro - 083 (Pitch Black Stamped) [Staff]", "83", card.PrintPrereleaseStaff, nil},
		// Other brackets are not staff markers.
		{"Professor's Research [Professor Oak]", "122/131", card.PrintStandard, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"cards":      []any{map[string]any{"id": "pk_x", "card_info": map[string]any{"name": tt.name, "card_number": tt.number}}},
				"pagination": map[string]any{"page": 1, "total_pages": 1},
			})
			cards, err := New(fakeGetter{"/sets/s?page=1&limit=50": string(body)}).Cards(t.Context(), "s")
			if err != nil {
				t.Fatal(err)
			}
			c := cards[0]
			if c.Print != tt.wantPrint || !slices.Equal(c.Aliases, tt.wantAliases) {
				t.Errorf("Print, Aliases = %q, %q; want %q, %q", c.Print, c.Aliases, tt.wantPrint, tt.wantAliases)
			}
			if c.Print != card.PrintStandard && c.Name != tt.name {
				t.Errorf("Name = %q; a stamped product keeps its full name for reports", c.Name)
			}
		})
	}
}

// A trailing " - <number>" is the card's own number however it is padded or
// spaced, and is removed; any other number is part of the name.
func TestCardsNumberSuffix(t *testing.T) {
	tests := []struct{ name, number, want string }{
		{"Mudkip - 59/109", "59/109", "Mudkip"},
		{"Ampharos - 075", "75", "Ampharos"},
		{"Quaxly -  063", "63", "Quaxly"},
		{"Haunter  - 027", "27", "Haunter"},
		{"Mimikyu -160/091", "160/091", "Mimikyu"},
		{"Mega Charizard X ex - 023", "23", "Mega Charizard X ex"},
		{"Porygon - 2", "150", "Porygon - 2"},
		{"Destined Rivals Booster Box", "", "Destined Rivals Booster Box"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"cards":      []any{map[string]any{"id": "pk_x", "card_info": map[string]any{"name": tt.name, "card_number": tt.number}}},
				"pagination": map[string]any{"page": 1, "total_pages": 1},
			})
			cards, err := New(fakeGetter{"/sets/s?page=1&limit=50": string(body)}).Cards(t.Context(), "s")
			if err != nil {
				t.Fatal(err)
			}
			if cards[0].Name != tt.want {
				t.Errorf("Name = %q, want %q", cards[0].Name, tt.want)
			}
		})
	}
}

// A stamped product carries one price: Holofoil for most, Normal for World
// Championships, Reverse Holofoil for a few staff prints. Two is a guess.
func TestQuoteStamped(t *testing.T) {
	tests := []struct {
		name, body string
		want       any // float64 market, or error sentinel
	}{
		{"Normal", `{"tcgplayer":{"prices":[{"sub_type_name":"Normal","market_price":641.76}]}}`, 641.76},
		{"Holofoil", `{"tcgplayer":{"prices":[{"sub_type_name":"Holofoil","market_price":84.02}]}}`, 84.02},
		{"Reverse Holofoil", `{"tcgplayer":{"prices":[{"sub_type_name":"Reverse Holofoil","market_price":3.5}]}}`, 3.5},
		{"two prices is ambiguous", `{"tcgplayer":{"prices":[
			{"sub_type_name":"Holofoil","market_price":2},{"sub_type_name":"Normal","market_price":1}]}}`, card.ErrVariantAmbiguous},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs, err := New(fakeGetter{"/cards/pk_x": tt.body}).Quote(t.Context(), "pk_x", []card.Variant{card.VariantStamped})
			if err != nil {
				t.Fatal(err)
			}
			switch w := tt.want.(type) {
			case float64:
				if qs[0].Err != nil || qs[0].Price.Market == nil || *qs[0].Price.Market != w {
					t.Errorf("quote = %+v, want market %v", qs[0], w)
				}
			case error:
				if !errors.Is(qs[0].Err, w) {
					t.Errorf("err = %v, want %v", qs[0].Err, w)
				}
			}
		})
	}
}

// A pattern product carries one price, under Holofoil in SV sets and Reverse
// Holofoil in ME sets (probe, 2026-09-27). Both at once would be a guess.
func TestQuotePattern(t *testing.T) {
	tests := []struct {
		name, body string
		want       any // float64 market, or error sentinel
	}{
		{"SV pattern: Holofoil", `{"tcgplayer":{"prices":[{"sub_type_name":"Holofoil","market_price":0.32}]}}`, 0.32},
		{"ME pattern: Reverse Holofoil", `{"tcgplayer":{"prices":[{"sub_type_name":"Reverse Holofoil","market_price":0.24}]}}`, 0.24},
		{"both is ambiguous", `{"tcgplayer":{"prices":[
			{"sub_type_name":"Holofoil","market_price":0.3},{"sub_type_name":"Reverse Holofoil","market_price":0.2}]}}`, card.ErrVariantAmbiguous},
		{"neither is unavailable", `{"tcgplayer":{"prices":[{"sub_type_name":"Normal","market_price":0.05}]}}`, card.ErrVariantUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs, err := New(fakeGetter{"/cards/pk_x": tt.body}).Quote(t.Context(), "pk_x", []card.Variant{card.VariantPattern})
			if err != nil {
				t.Fatal(err)
			}
			switch w := tt.want.(type) {
			case float64:
				if qs[0].Err != nil || qs[0].Price.Market == nil || *qs[0].Price.Market != w {
					t.Errorf("quote = %+v, want market %v", qs[0], w)
				}
			case error:
				if !errors.Is(qs[0].Err, w) {
					t.Errorf("err = %v, want %v", qs[0].Err, w)
				}
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

// PokeWallet's X-RateLimit-Remaining-* headers report the count from before the
// request carrying them was counted: a brand-new key's first response said 100
// of 100 per hour and 1000 of 1000 per day (spike, 2026-09-18). Reading them as
// "after" let the client send one request too many and get a 429 (2026-09-18,
// "used 100, remaining 0"). Config converts them to "after this request".
// Spent at 23:29 local on 2026-09-18, full again at 00:06: a rolling window
// would still have been spent until about 00:28. PokeWallet resets on the hour.
func TestConfigResetsOnTheClockHour(t *testing.T) {
	if cfg := Config(DefaultBaseURL, "key", nil, time.Second); cfg.HourWindow != source.ClockHour {
		t.Errorf("HourWindow = %v, want source.ClockHour", cfg.HourWindow)
	}
}

func TestConfigCountsTheRequestCarryingTheHeaders(t *testing.T) {
	cfg := Config(DefaultBaseURL, "key", nil, time.Second)
	if cfg.HourCount == nil || cfg.DayUsed == nil {
		t.Fatal("HourCount and DayUsed must be set")
	}
	tests := []struct {
		hourRemaining, dayRemaining string
		wantHourLeft, wantDayUsed   int
	}{
		{"100", "1000", 99, 1}, // a fresh window and day: this request is the first
		{"37", "617", 36, 384},
		{"1", "1", 0, 1000}, // the last request allowed: nothing left after it
		{"0", "0", 0, 1000}, // a 429 still reports 0, never a negative count
	}
	for _, tt := range tests {
		h := http.Header{}
		h.Set("X-RateLimit-Limit-Hour", "100")
		h.Set("X-RateLimit-Remaining-Hour", tt.hourRemaining)
		h.Set("X-RateLimit-Limit-Day", "1000")
		h.Set("X-RateLimit-Remaining-Day", tt.dayRemaining)
		if limit, left, ok := cfg.HourCount(h); !ok || limit != 100 || left != tt.wantHourLeft {
			t.Errorf("hour header %s: HourCount = %d, %d, %v; want 100, %d", tt.hourRemaining, limit, left, ok, tt.wantHourLeft)
		}
		if used, ok := cfg.DayUsed(h); !ok || used != tt.wantDayUsed {
			t.Errorf("day header %s: DayUsed = %d, %v; want %d", tt.dayRemaining, used, ok, tt.wantDayUsed)
		}
	}
	if cfg.Limits.PerSecond != 2 {
		t.Errorf("politeness cap = %v per second, want 2", cfg.Limits.PerSecond)
	}
}

// The real client against a server that counts like PokeWallet and enforces a
// hard hourly limit of 3: the fourth request must wait for the window, never
// be sent and rejected with a 429.
func TestClientNeverOverspendsPokeWalletsHourlyLimit(t *testing.T) {
	var used atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		before := 3 - int(used.Load()) // PokeWallet reports the count before this request
		w.Header().Set("X-RateLimit-Limit-Hour", "3")
		w.Header().Set("X-RateLimit-Remaining-Hour", strconv.Itoa(max(before, 0)))
		if used.Add(1) > 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":"Rate limit exceeded","message":"Hourly limit exceeded"}`)
			return
		}
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	c := source.New(Config(srv.URL, "key", nil, time.Second))
	for i := range 3 {
		if err := c.GetJSON(t.Context(), "/x", &struct{}{}); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	// Longer than the 2-per-second politeness gap, far shorter than the hour.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err := c.GetJSON(ctx, "/x", &struct{}{})
	if errors.Is(err, card.ErrRateLimited) || used.Load() > 3 {
		t.Fatalf("a fourth request was sent and rejected (%v); the client must wait for the window instead", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "hourly window") {
		t.Fatalf("err = %v, want the wait for the hourly window to be cut short by the deadline", err)
	}
}

// A bracket holding the card's own number, prefix and all, names the card's own
// print: Hidden Fates Shiny Vault lists "Lycanroc GX (SV66)" at SV66/SV94.
func TestCardsQualifierMatchingTheCardsOwnNumber(t *testing.T) {
	tests := []struct {
		name, number string
		wantAliases  []string
	}{
		{"Lycanroc GX (SV66)", "SV66/SV94", []string{"Lycanroc GX"}},
		{"Lycanroc GX (sv67)", "SV67/SV94", []string{"Lycanroc GX"}},
		{"Lycanroc GX (SV67)", "SV66/SV94", nil}, // a different number: not this card's own print
		{"Charizard (GG)", "GG01/GG70", nil},     // a prefix alone is not a number
	}
	for _, tt := range tests {
		t.Run(tt.name+" at "+tt.number, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"cards":      []any{map[string]any{"id": "pk_x", "card_info": map[string]any{"name": tt.name, "card_number": tt.number}}},
				"pagination": map[string]any{"page": 1, "total_pages": 1},
			})
			cards, err := New(fakeGetter{"/sets/s?page=1&limit=50": string(body)}).Cards(t.Context(), "s")
			if err != nil {
				t.Fatal(err)
			}
			if got := cards[0].Aliases; !slices.Equal(got, tt.wantAliases) {
				t.Errorf("Aliases = %q, want %q", got, tt.wantAliases)
			}
		})
	}
}

// The card's art comes from the TCGplayer product in the same response, so it
// costs no extra requests (DD-14).
func TestQuoteCarriesTCGplayerArt(t *testing.T) {
	const want = "https://tcgplayer-cdn.tcgplayer.com/product/83475_in_1000x1000.jpg"
	tests := []struct {
		name, body, want string
	}{
		{"product URL", `{"tcgplayer":{"url":"https://www.tcgplayer.com/product/83475","prices":[{"sub_type_name":"Normal","market_price":1}]}}`, want},
		{"product URL with a slug", `{"tcgplayer":{"url":"https://www.tcgplayer.com/product/83475/pokemon-ruby-and-sapphire-aggron","prices":[{"sub_type_name":"Normal","market_price":1}]}}`, want},
		{"no url", `{"tcgplayer":{"prices":[{"sub_type_name":"Normal","market_price":1}]}}`, ""},
		{"no tcgplayer block", `{"tcgplayer":null}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs, err := New(fakeGetter{"/cards/pk_x": tt.body}).Quote(t.Context(), "pk_x",
				[]card.Variant{card.VariantNormal, card.VariantReverseHolo})
			if err != nil {
				t.Fatal(err)
			}
			for i, q := range qs {
				if q.Image != tt.want {
					t.Errorf("quote %d Image = %q, want %q", i, q.Image, tt.want)
				}
			}
		})
	}
}
