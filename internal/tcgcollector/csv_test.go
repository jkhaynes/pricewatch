package tcgcollector

import (
	"strings"
	"testing"

	"github.com/jkhaynes/pricewatch/internal/card"
)

type cardRow = card.Row // alias only to keep the table short

// The real export's header (Task 0), with the UTF-8 BOM that spreadsheet-style exports often carry.
const header = "\uFEFFTCG region,Card name,Card number,Card number sorting order,Expansion,Rarity,Card variant,Card language,Card condition,Quantity,Price,Total price,Note\n"

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantRows    int
		wantRowErrs int
		check       func(t *testing.T, rows []cardRow)
	}{
		{
			name:     "one full row",
			body:     `International,Tropius,001/084,1,Pitch Black,Common,Reverse Holo,English,Near Mint,2,$0.20,$0.40,trade bait` + "\n",
			wantRows: 1,
			check: func(t *testing.T, rows []cardRow) {
				r := rows[0]
				if r.Name != "Tropius" || r.Number != "001/084" || r.Variant != "Reverse Holo" || r.Quantity != 2 ||
					r.Condition != "Near Mint" || r.Note != "trade bait" {
					t.Errorf("unexpected row %+v", r)
				}
				if r.Price == nil || *r.Price != 0.20 {
					t.Errorf("Price = %v, want 0.20", r.Price)
				}
				if r.SortNumber == nil || *r.SortNumber != 1 {
					t.Errorf("SortNumber = %v, want 1", r.SortNumber)
				}
			},
		},
		{
			name:     "quoted expansion with comma and ampersand",
			body:     `International,Mudkip,59/109,,"EX Ruby & Sapphire, Promo",Common,Normal,English,,,,` + "\n",
			wantRows: 1,
			check: func(t *testing.T, rows []cardRow) {
				r := rows[0]
				if r.Expansion != "EX Ruby & Sapphire, Promo" {
					t.Errorf("Expansion = %q", r.Expansion)
				}
				if r.Quantity != 1 {
					t.Errorf("empty quantity should default to 1, got %d", r.Quantity)
				}
				if r.Price != nil || r.SortNumber != nil {
					t.Errorf("empty price/sort should be nil, got %v %v", r.Price, r.SortNumber)
				}
			},
		},
		{
			name:        "bad quantity is a row error, not a fatal error",
			body:        "International,A,1/1,,S,,Normal,English,,two,,\nInternational,B,2/2,,S,,Normal,English,,1,,\n",
			wantRows:    1,
			wantRowErrs: 1,
		},
		{
			name:        "missing required field is a row error",
			body:        "International,,1/1,,S,,Normal,English,,1,,\n",
			wantRows:    0,
			wantRowErrs: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, rowErrs, err := Parse(strings.NewReader(header + tt.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(rows) != tt.wantRows || len(rowErrs) != tt.wantRowErrs {
				t.Fatalf("rows=%d rowErrs=%d (%v), want %d/%d", len(rows), len(rowErrs), rowErrs, tt.wantRows, tt.wantRowErrs)
			}
			if tt.check != nil {
				tt.check(t, rows)
			}
		})
	}
}

func TestParseMissingRequiredHeader(t *testing.T) {
	_, _, err := Parse(strings.NewReader("TCG region,Card name\nInternational,A\n"))
	if err == nil {
		t.Fatal("want error for missing required headers")
	}
}
