package ask

import (
	"slices"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

func TestMovers(t *testing.T) {
	day := 24 * time.Hour
	up := row("S", "Riser", "1/100", "Holo", 1)
	down := row("S", "Faller", "2/100", "Holo", 1)
	recent := row("S", "Recent", "3/100", "Holo", 1)
	st := newStore(t, []card.Row{up, down, recent}, []obs{
		{up.Key(), 10, at.Add(-10 * day)}, {up.Key(), 20, at}, // +100% over 10 days
		{down.Key(), 50, at.Add(-8 * day)}, {down.Key(), 40, at}, // -20% over 8 days
		{recent.Key(), 5, at.Add(-2 * day)}, {recent.Key(), 10, at}, // +100% over 2 days
	})
	tests := []struct {
		name        string
		days        int
		wantRising  []string
		wantFalling []string
		wantErr     bool
	}{
		{name: "default week", wantRising: []string{"Riser"}, wantFalling: []string{"Faller"}},
		{name: "two days sees the recent move too", days: 2, wantRising: []string{"Riser", "Recent"}, wantFalling: []string{"Faller"}},
		{name: "nine days: only the riser has a baseline that old", days: 9, wantRising: []string{"Riser"}},
		{name: "out of range", days: 91, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testTools().movers(t.Context(), st, MoversIn{Days: tt.days})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error: %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			// slices.Equal treats nil and empty as equal.
			if r, f := moveNames(got.Rising), moveNames(got.Falling); !slices.Equal(r, tt.wantRising) || !slices.Equal(f, tt.wantFalling) {
				t.Errorf("rising %v falling %v; want %v and %v", r, f, tt.wantRising, tt.wantFalling)
			}
			for _, m := range append(got.Rising, got.Falling...) {
				if m.Key == "" {
					t.Errorf("%s has no collection_key", m.Name)
				}
			}
		})
	}
}

func moveNames(ms []Move) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}
