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
		label   string
		want    Variant
		wantErr error
	}{
		{"Normal", VariantNormal, nil},
		{"Non-holo", VariantNormal, nil},
		{"Holo", VariantHolo, nil},
		{"Normal Holo", VariantHolo, nil},
		{"Reverse Holo", VariantReverseHolo, nil},
		{" reverse holo ", VariantReverseHolo, nil},
		{"1st Edition", VariantFirstEdition, nil},
		{"1st Edition Holo", VariantFirstEditionHolo, nil},
		// Real labels from the export that phase 1 excludes on purpose.
		{"Poké Ball Reverse Holo", "", ErrUnsupportedVariant},
		{"Master Ball Reverse Holo", "", ErrUnsupportedVariant},
		{"Energy Reverse Holo", "", ErrUnsupportedVariant},
		{"Cosmos Holo", "", ErrUnsupportedVariant},
		{"Play! Pokémon Prize Pack, Non-holo", "", ErrUnsupportedVariant},
		{"Jumbo Size", "", ErrUnsupportedVariant},
		{"", "", ErrUnsupportedVariant},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got, err := ParseVariant(tt.label)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
