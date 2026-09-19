package resolve

import (
	"context"
	"errors"
	"slices"
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
			// Shapes seen in the real import (2026-09-19).
			{ID: "1914", Names: []string{"SM - Celestial Storm", "Celestial Storm"}},
			{ID: "24326", Names: []string{"SV: White Flare", "White Flare"}},
			{ID: "2754", Names: []string{"Shining Fates"}},
			{ID: "2781", Names: []string{"Shining Fates: Shiny Vault"}},
		},
		cards: map[string][]card.SourceCard{
			"1393":  {{ID: "pk_59", Number: "59", Name: "Mudkip"}},
			"23473": {{ID: "pk_t1", Number: "001", Name: "Tangela"}},
			"3064":  {{ID: "pk_go1", Number: "001", Name: "Bulbasaur"}},
			"17688": {{ID: "pk_catch", Number: "138", Name: "Pokemon Catcher"}},
			"604": {
				{ID: "pk_zard", Number: "004", Name: "Charizard"},
				{ID: "pk_dot", Number: "004", Name: "Charizard (Black Dot Error)"},
				{ID: "pk_zfa", Number: "004", Name: "Charizard (Full Art)", Aliases: []string{"Charizard"}},
				{ID: "pk_x1", Number: "010", Name: "Twin"},
				{ID: "pk_x2", Number: "010", Name: "Twin"},
			},
			"1914": {
				{ID: "pk_wh117", Number: "117", Name: "Whismur (117)", Aliases: []string{"Whismur"}},
				{ID: "pk_wh116", Number: "116", Name: "Whismur (116)", Aliases: []string{"Whismur"}},
				{ID: "pk_fa", Number: "150", Name: "Guzma (Full Art)", Aliases: []string{"Guzma"}},
				{ID: "pk_sr", Number: "150", Name: "Guzma (Secret)", Aliases: []string{"Guzma"}},
			},
			"24326": {{ID: "pk_pat", Number: "014", Name: "Pansear (Poke Ball Pattern)"}},
			"2754":  {{ID: "pk_sf12", Number: "012", Name: "Rillaboom V"}},
			"2781":  {{ID: "pk_sv86", Number: "SV086", Name: "Galarian Meowth"}},
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
		overrides   []Override
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
		{"override wins", r("Mudkip", "EX Ruby & Sapphire", "59/109", "Normal", "English"), []Override{{Expansion: "EX Ruby & Sapphire", SetID: "1393"}},
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

		// Aliases: used only when nothing at the number matches the name exactly.
		{"alias matches a (number) suffix", r("Whismur", "Celestial Storm", "117/168", "Normal", "English"), nil,
			card.StatusResolved, "pk_wh117", card.VariantNormal, ""},
		{"exact name beats an alias", r("Charizard", "Base Set", "4/102", "Holo", "English"), nil,
			card.StatusResolved, "pk_zard", card.VariantHolo, ""},
		{"two alias matches are ambiguous", r("Guzma", "Celestial Storm", "150/168", "Normal Holo", "English"), nil,
			card.StatusAmbiguous, "", "", "match"},
		{"a pattern print gets no alias", r("Pansear", "White Flare", "014/086", "Normal", "English"), nil,
			card.StatusUnmatched, "", "", "name mismatch"},

		// Subsets: an override keyed on expansion plus number prefix routes to the subset's set.
		{"prefix override routes a subset card", r("Galarian Meowth", "Shining Fates", "SV086/SV122", "Normal Holo", "English"),
			[]Override{{Expansion: "Shining Fates", NumberPrefix: "SV", SetID: "2781"}},
			card.StatusResolved, "pk_sv86", card.VariantHolo, ""},
		{"prefixed numbers ignore leading zeros too", r("Galarian Meowth", "Shining Fates", "SV86/SV122", "Normal Holo", "English"),
			[]Override{{Expansion: "Shining Fates", NumberPrefix: "SV", SetID: "2781"}},
			card.StatusResolved, "pk_sv86", card.VariantHolo, ""},
		{"prefix override leaves plain numbers alone", r("Rillaboom V", "Shining Fates", "012/072", "Normal Holo", "English"),
			[]Override{{Expansion: "Shining Fates", NumberPrefix: "SV", SetID: "2781"}},
			card.StatusResolved, "pk_sf12", card.VariantHolo, ""},
		{"subset card without a prefix override is reported", r("Galarian Meowth", "Shining Fates", "SV086/SV122", "Normal Holo", "English"), nil,
			card.StatusUnmatched, "", "", "number SV086 not in set 2754"},
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
	// The third column, number_prefix, is optional per line.
	in := "expansion,set_id,number_prefix\n" +
		"EX Ruby & Sapphire,1393\n" +
		"\"Black Star Promos, Wizards\",1418\n" +
		"Shining Fates,2781,SV\n"
	got, err := LoadOverrides(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []Override{
		{Expansion: "EX Ruby & Sapphire", SetID: "1393"},
		{Expansion: "Black Star Promos, Wizards", SetID: "1418"},
		{Expansion: "Shining Fates", SetID: "2781", NumberPrefix: "SV"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("overrides = %+v", got)
	}
}

func TestLoadOverridesRejectsBadLines(t *testing.T) {
	for _, in := range []string{"Base Set\n", "Base Set,604,SV,extra\n", "Base Set,,SV\n"} {
		if _, err := LoadOverrides(strings.NewReader(in)); err == nil {
			t.Errorf("LoadOverrides(%q) returned nil error", in)
		}
	}
}
