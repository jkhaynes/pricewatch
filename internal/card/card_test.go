package card

import (
	"errors"
	"testing"
)

func TestRowKey(t *testing.T) {
	tests := []struct {
		name string
		row  Row
		want string
	}{
		{"basic", Row{Region: "International", Expansion: "EX Ruby & Sapphire", Number: "59/109", Variant: "Reverse Holo", Language: "English"},
			"international|ex ruby & sapphire|59/109|reverse holo|english"},
		{"whitespace and case are normalised", Row{Region: " International ", Expansion: "EX  Ruby &  Sapphire", Number: "59/109 ", Variant: "reverse holo", Language: "ENGLISH"},
			"international|ex ruby & sapphire|59/109|reverse holo|english"},
		{"variant is part of the key", Row{Region: "International", Expansion: "EX Ruby & Sapphire", Number: "59/109", Variant: "Normal", Language: "English"},
			"international|ex ruby & sapphire|59/109|normal|english"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.row.Key(); got != tt.want {
				t.Errorf("Key() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseVariant(t *testing.T) {
	tests := []struct {
		label     string
		want      Variant
		wantPrint Print
		wantErr   error
	}{
		{"Normal", VariantNormal, PrintStandard, nil},
		{"Non-holo", VariantNormal, PrintStandard, nil},
		{"Holo", VariantHolo, PrintStandard, nil},
		{"Normal Holo", VariantHolo, PrintStandard, nil},
		{"Reverse Holo", VariantReverseHolo, PrintStandard, nil},
		{" reverse holo ", VariantReverseHolo, PrintStandard, nil},
		{"1st Edition", VariantFirstEdition, PrintStandard, nil},
		{"1st Edition Holo", VariantFirstEditionHolo, PrintStandard, nil},
		// Pattern reverse holos: a separate product at the source, priced as its own foil.
		{"Poké Ball Reverse Holo", VariantPattern, PrintPokeBall, nil},
		{"Master Ball Reverse Holo", VariantPattern, PrintMasterBall, nil},
		{"Friend Ball Reverse Holo", VariantPattern, PrintFriendBall, nil},
		{"Quick Ball Reverse Holo", VariantPattern, PrintQuickBall, nil},
		{"Love Ball Reverse Holo", VariantPattern, PrintLoveBall, nil},
		{"Dusk Ball Reverse Holo", VariantPattern, PrintDuskBall, nil},
		{"Rocket Reverse Holo", VariantPattern, PrintRocket, nil},
		{"Energy Reverse Holo", VariantPattern, PrintEnergy, nil},
		// Stamped promo prints: a separate product at the source, priced at its only price.
		{"Prerelease", VariantStamped, PrintPrerelease, nil},
		{"Prerelease (Staff)", VariantStamped, PrintPrereleaseStaff, nil},
		{"World Championships", VariantStamped, PrintWorlds, nil},
		{"World Championships (Staff)", VariantStamped, PrintWorldsStaff, nil},
		{"30th Anniversary", VariantStamped, PrintAnniversary, nil},
		// Real labels from the export that are still excluded on purpose.
		{"Cosmos Holo", "", PrintStandard, ErrUnsupportedVariant},
		{"Play! Pokémon Prize Pack, Non-holo", "", PrintStandard, ErrUnsupportedVariant},
		{"Jumbo Size", "", PrintStandard, ErrUnsupportedVariant},
		{"", "", PrintStandard, ErrUnsupportedVariant},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got, gotPrint, err := ParseVariant(tt.label)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want || gotPrint != tt.wantPrint {
				t.Errorf("got (%q, %q), want (%q, %q)", got, gotPrint, tt.want, tt.wantPrint)
			}
		})
	}
}

// Print's zero value is the plain card, which reads badly in a reason ("no  print").
func TestPrintString(t *testing.T) {
	tests := []struct {
		p    Print
		want string
	}{
		{PrintStandard, "standard"},
		{PrintPokeBall, "pokeball"},
		{PrintEnergy, "energy"},
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("%q.String() = %q, want %q", string(tt.p), got, tt.want)
		}
	}
}
