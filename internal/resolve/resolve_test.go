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
			{ID: "24451", Names: []string{"ME: Mega Evolution Promo", "Mega Evolution Promo"}},
			{ID: "22872", Names: []string{"SV: Scarlet & Violet Promo Cards", "Scarlet & Violet Promo Cards"}},
			{ID: "24269", Names: []string{"SV10: Destined Rivals", "Destined Rivals"}},
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
			// White Flare #014 as PokeWallet lists it: the plain card and two
			// pattern products (live, 2026-09-27). #015 has only a pattern
			// product; #016 only the plain card.
			"24326": {
				{ID: "pk_pansear", Number: "014", Name: "Pansear"},
				{ID: "pk_pb", Number: "014", Name: "Pansear (Poke Ball Pattern)", Print: card.PrintPokeBall, Aliases: []string{"Pansear"}},
				{ID: "pk_mb", Number: "014", Name: "Pansear (Master Ball Pattern)", Print: card.PrintMasterBall, Aliases: []string{"Pansear"}},
				{ID: "pk_simi_pb", Number: "015", Name: "Simisear (Poke Ball Pattern)", Print: card.PrintPokeBall, Aliases: []string{"Simisear"}},
				{ID: "pk_panpour", Number: "016", Name: "Panpour"},
				{ID: "pk_tw1", Number: "017", Name: "Twin (Poke Ball Pattern)", Print: card.PrintPokeBall, Aliases: []string{"Twin"}},
				{ID: "pk_tw2", Number: "017", Name: "Twin (Poke Ball)", Print: card.PrintPokeBall, Aliases: []string{"Twin"}},
			},
			"2754": {{ID: "pk_sf12", Number: "012", Name: "Rillaboom V"}},
			"2781": {{ID: "pk_sv86", Number: "SV086", Name: "Galarian Meowth"}},
			// Mega Evolution Promos as PokeWallet lists them (probe, 2026-09-29).
			// The plain Ceruledge is added to prove a labelled product wins.
			"24451": {
				{ID: "pk_cer", Number: "14", Name: "Ceruledge"},
				{ID: "pk_cer_pre", Number: "14", Name: "Ceruledge (Prerelease)", Print: card.PrintPrerelease, Aliases: []string{"Ceruledge"}},
				{ID: "pk_cer_staff", Number: "14", Name: "Ceruledge (Prerelease) [Staff]", Print: card.PrintPrereleaseStaff, Aliases: []string{"Ceruledge"}},
				{ID: "pk_amph", Number: "75", Name: "Ampharos"},
				{ID: "pk_amph_staff", Number: "75", Name: "Ampharos - 075 [Staff]", Print: card.PrintPrereleaseStaff, Aliases: []string{"Ampharos"}},
				{ID: "pk_bulba", Number: "37", Name: "Bulbasaur"},
				{ID: "pk_luna", Number: "4", Name: "Lunatone"},
				{ID: "pk_slow", Number: "83", Name: "Slowbro"},
				// An unreadable label: pokewallet gives this PrintStandard with no
				// alias, so it must block the fallback rather than look empty.
				{ID: "pk_slow_pb", Number: "83", Name: "Slowbro - 083 (Pitch Black Stamped)"},
				{ID: "pk_meg", Number: "1", Name: "Meganium"},
			},
			"22872": {
				{ID: "pk_pr150", Number: "150", Name: "Paradise Resort - 150 (World Championships 2024)", Print: card.PrintWorlds, Aliases: []string{"Paradise Resort"}},
				{ID: "pk_pr150_staff", Number: "150", Name: "Paradise Resort - 150 (World Championships 2024) [Staff]", Print: card.PrintWorldsStaff, Aliases: []string{"Paradise Resort"}},
			},
			// A main set: the unlabelled product is the ordinary card.
			"24269": {
				{ID: "pk_trmimikyu", Number: "087", Name: "Team Rocket's Mimikyu"},
				{ID: "pk_trmeowth", Number: "088", Name: "Team Rocket's Meowth"},
			},
		},
		cardCalls: map[string]int{},
	}
}

func r(name, exp, num, variant, lang string) card.Row {
	return card.Row{Region: "International", Name: name, Expansion: exp, Number: num, Variant: variant, Language: lang}
}

func TestResolve(t *testing.T) {
	me := []Override{{Expansion: "Mega Evolution Promos", SetID: "24451"}}
	sv := []Override{{Expansion: "Scarlet & Violet Promos", SetID: "22872"}}
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
		// Pattern prints: a separate product at the same number, matched only by print.
		{"plain row takes the plain product", r("Pansear", "White Flare", "014/086", "Normal", "English"), nil,
			card.StatusResolved, "pk_pansear", card.VariantNormal, ""},
		{"reverse holo takes the plain product", r("Pansear", "White Flare", "014/086", "Reverse Holo", "English"), nil,
			card.StatusResolved, "pk_pansear", card.VariantReverseHolo, ""},
		{"poke ball takes its own product", r("Pansear", "White Flare", "014/086", "Poké Ball Reverse Holo", "English"), nil,
			card.StatusResolved, "pk_pb", card.VariantPattern, ""},
		{"master ball takes its own product", r("Pansear", "White Flare", "014/086", "Master Ball Reverse Holo", "English"), nil,
			card.StatusResolved, "pk_mb", card.VariantPattern, ""},
		{"plain row never takes a pattern product", r("Simisear", "White Flare", "015/086", "Normal", "English"), nil,
			card.StatusUnmatched, "", "", `no standard print at set 24326 #015; has ["Simisear (Poke Ball Pattern)"]`},
		{"pattern row never takes the plain product", r("Panpour", "White Flare", "016/086", "Poké Ball Reverse Holo", "English"), nil,
			card.StatusUnmatched, "", "", `no pokeball print at set 24326 #016; has ["Panpour"]`},
		{"missing pattern lists every product at the number", r("Pansear", "White Flare", "014/086", "Energy Reverse Holo", "English"), nil,
			card.StatusUnmatched, "", "", `no energy print at set 24326 #014; has ["Pansear" "Pansear (Poke Ball Pattern)" "Pansear (Master Ball Pattern)"]`},
		{"two products of one print are ambiguous", r("Twin", "White Flare", "017/086", "Poké Ball Reverse Holo", "English"), nil,
			card.StatusAmbiguous, "", "", "match"},

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

		// Stamped promo prints.
		{"labelled prerelease beats the plain product", r("Ceruledge", "Mega Evolution Promos", "014", "Prerelease", "English"), me,
			card.StatusResolved, "pk_cer_pre", card.VariantStamped, ""},
		{"labelled prerelease staff", r("Ceruledge", "Mega Evolution Promos", "014", "Prerelease (Staff)", "English"), me,
			card.StatusResolved, "pk_cer_staff", card.VariantStamped, ""},
		{"promo set: unlabelled prerelease is the plain product", r("Ampharos", "Mega Evolution Promos", "075", "Prerelease", "English"), me,
			card.StatusResolved, "pk_amph", card.VariantStamped, ""},
		{"promo set: staff by its marker", r("Ampharos", "Mega Evolution Promos", "075", "Prerelease (Staff)", "English"), me,
			card.StatusResolved, "pk_amph_staff", card.VariantStamped, ""},
		{"promo set: 30th anniversary is the plain product", r("Bulbasaur", "Mega Evolution Promos", "037", "30th Anniversary", "English"), me,
			card.StatusResolved, "pk_bulba", card.VariantStamped, ""},
		{"worlds staff", r("Paradise Resort", "Scarlet & Violet Promos", "150", "World Championships (Staff)", "English"), sv,
			card.StatusResolved, "pk_pr150_staff", card.VariantStamped, ""},
		{"worlds", r("Paradise Resort", "Scarlet & Violet Promos", "150", "World Championships", "English"), sv,
			card.StatusResolved, "pk_pr150", card.VariantStamped, ""},
		{"staff never falls back to the plain product", r("Lunatone", "Mega Evolution Promos", "004", "Prerelease (Staff)", "English"), me,
			card.StatusUnmatched, "", "", `no prerelease-staff print at set 24451 #004; has ["Lunatone"]`},
		{"main set: prerelease never falls back", r("Team Rocket's Mimikyu", "Destined Rivals", "087/182", "Prerelease", "English"), nil,
			card.StatusUnmatched, "", "", `no prerelease print at set 24269 #087; has ["Team Rocket's Mimikyu"]`},
		{"main set: 30th never falls back", r("Team Rocket's Meowth", "Destined Rivals", "088/182", "30th Anniversary", "English"), nil,
			card.StatusUnmatched, "", "", "no 30th print at set 24269 #088"},
		{"promo set: worlds never falls back", r("Bulbasaur", "Mega Evolution Promos", "037", "World Championships", "English"), me,
			card.StatusUnmatched, "", "", "no worlds print at set 24451 #037"},
		{"promo set: an unreadable label at the number blocks the fallback", r("Slowbro", "Mega Evolution Promos", "083", "Prerelease", "English"), me,
			card.StatusUnmatched, "", "", "no prerelease print at set 24451 #083"},
		{"promo set: fallback still requires the name", r("Chikorita", "Mega Evolution Promos", "001", "Prerelease", "English"), me,
			card.StatusUnmatched, "", "", "name mismatch"},
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
