package resolve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jkhaynes/pricewatch/internal/card"
)

type fakeCatalog struct {
	sets      []card.SourceSet
	cards     map[string][]card.SourceCard
	cardCalls map[string]int
	setsErr   error
}

func (f *fakeCatalog) Sets(context.Context) ([]card.SourceSet, error) { return f.sets, f.setsErr }

func (f *fakeCatalog) Cards(_ context.Context, id string) ([]card.SourceCard, error) {
	f.cardCalls[id]++
	return f.cards[id], nil
}

func newFake() *fakeCatalog {
	return &fakeCatalog{
		sets: []card.SourceSet{
			{ID: "1393", Names: []string{"Ruby and Sapphire"}},
			{ID: "23473", Names: []string{"SV06: Twilight Masquerade", "Twilight Masquerade"}},
			{ID: "604", Names: []string{"Base Set"}},
			{ID: "dupA", Names: []string{"Promos"}},
			{ID: "dupB", Names: []string{"Promos"}},
			// PokeWallet spells these without the accent (checked live, 2026-09-18).
			{ID: "3064", Names: []string{"Pokemon GO"}},
			{ID: "17688", Names: []string{"Crown Zenith"}},
		},
		cards: map[string][]card.SourceCard{
			"1393":  {{ID: "pk_59", Number: "59", Name: "Mudkip"}},
			"23473": {{ID: "pk_t1", Number: "001", Name: "Tangela"}},
			"3064":  {{ID: "pk_go1", Number: "001", Name: "Bulbasaur"}},
			"17688": {{ID: "pk_catch", Number: "138", Name: "Pokemon Catcher"}},
			"604": {
				{ID: "pk_zard", Number: "004", Name: "Charizard"},
				{ID: "pk_dot", Number: "004", Name: "Charizard (Black Dot Error)"},
				{ID: "pk_x1", Number: "010", Name: "Twin"},
				{ID: "pk_x2", Number: "010", Name: "Twin"},
			},
		},
		cardCalls: map[string]int{},
	}
}

func r(name, exp, num, variant, lang string) card.Row {
	return card.Row{Region: "International", Name: name, Expansion: exp, Number: num, Variant: variant, Language: lang}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name        string
		row         card.Row
		overrides   map[string]string
		wantStatus  card.Status
		wantID      string
		wantVariant card.Variant
		wantReason  string // substring
	}{
		{"ampersand matches and", r("Mudkip", "Ruby & Sapphire", "59/109", "Reverse Holo", "English"), nil,
			card.StatusResolved, "pk_59", card.VariantReverseHolo, ""},
		{"code-prefix alias and padded number", r("Tangela", "Twilight Masquerade", "1/167", "Normal", "English"), nil,
			card.StatusResolved, "pk_t1", card.VariantNormal, ""},
		{"name disambiguates a shared number", r("Charizard", "Base Set", "4/102", "Holo", "English"), nil,
			card.StatusResolved, "pk_zard", card.VariantHolo, ""},
		{"override wins", r("Mudkip", "EX Ruby & Sapphire", "59/109", "Normal", "English"), map[string]string{"EX Ruby & Sapphire": "1393"},
			card.StatusResolved, "pk_59", card.VariantNormal, ""},
		{"unknown expansion suggests, does not guess", r("Mudkip", "EX Ruby & Sapphire", "59/109", "Normal", "English"), nil,
			card.StatusUnmatched, "", "", `unknown expansion "EX Ruby & Sapphire" (candidates: 1393`},
		{"unsupported variant", r("Mudkip", "Ruby & Sapphire", "59/109", "Jumbo", "English"), nil,
			card.StatusUnmatched, "", "", "unsupported variant"},
		{"unsupported language", r("Mudkip", "Ruby & Sapphire", "59/109", "Normal", "Japanese"), nil,
			card.StatusUnmatched, "", "", "unsupported language"},
		{"ambiguous expansion", r("Pikachu", "Promos", "1", "Normal", "English"), nil,
			card.StatusAmbiguous, "", "", "matches sets [dupA dupB]"},
		{"number not in set", r("Mudkip", "Ruby & Sapphire", "999/109", "Normal", "English"), nil,
			card.StatusUnmatched, "", "", "not in set"},
		{"name mismatch", r("Torchic", "Ruby & Sapphire", "59/109", "Normal", "English"), nil,
			card.StatusUnmatched, "", "", "name mismatch"},
		{"two cards share number and name", r("Twin", "Base Set", "10/102", "Normal", "English"), nil,
			card.StatusAmbiguous, "", "", "match"},
		{"accent in expansion name", r("Bulbasaur", "Pokémon GO", "1/78", "Normal", "English"), nil,
			card.StatusResolved, "pk_go1", card.VariantNormal, ""},
		{"accent in card name", r("Pokémon Catcher", "Crown Zenith", "138/159", "Reverse Holo", "English"), nil,
			card.StatusResolved, "pk_catch", card.VariantReverseHolo, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(newFake(), "pw", tt.overrides).Resolve(t.Context(), tt.row)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if m.Status != tt.wantStatus || m.SourceCardID != tt.wantID || m.Variant != tt.wantVariant ||
				!strings.Contains(m.Reason, tt.wantReason) {
				t.Errorf("got %+v", m)
			}
			if m.Key != tt.row.Key() || m.Source != "pw" {
				t.Errorf("key/source = %q/%q", m.Key, m.Source)
			}
		})
	}
}

func TestResolveCachesSetCards(t *testing.T) {
	cat := newFake()
	res := New(cat, "pw", nil)
	for _, v := range []string{"Normal", "Reverse Holo"} {
		if _, err := res.Resolve(t.Context(), r("Mudkip", "Ruby & Sapphire", "59/109", v, "English")); err != nil {
			t.Fatal(err)
		}
	}
	if cat.cardCalls["1393"] != 1 {
		t.Errorf("Cards(1393) called %d times, want 1", cat.cardCalls["1393"])
	}
}

func TestResolveCatalogErrorIsReturnedNotDecided(t *testing.T) {
	cat := newFake()
	cat.setsErr = card.ErrQuotaExhausted
	_, err := New(cat, "pw", nil).Resolve(t.Context(), r("Mudkip", "Ruby & Sapphire", "59/109", "Normal", "English"))
	if !errors.Is(err, card.ErrQuotaExhausted) {
		t.Fatalf("err = %v, want ErrQuotaExhausted to pass through", err)
	}
}

func TestLoadOverrides(t *testing.T) {
	m, err := LoadOverrides(strings.NewReader("expansion,set_id\nEX Ruby & Sapphire,1393\n\"Black Star Promos, Wizards\",1418\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["EX Ruby & Sapphire"] != "1393" || m["Black Star Promos, Wizards"] != "1418" || len(m) != 2 {
		t.Errorf("overrides = %v", m)
	}
}
