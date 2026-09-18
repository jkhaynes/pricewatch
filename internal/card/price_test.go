package card

import (
	"math"
	"testing"
)

func f(v float64) *float64 { return &v }

func obs(market *float64) Observation { return Observation{CardID: "k", Price: Price{Market: market}} }

func TestCompare(t *testing.T) {
	tests := []struct {
		name        string
		prev, curr  *float64
		wantChanged bool
		wantDelta   float64
	}{
		{"up", f(0.06), f(0.20), true, 0.14},
		{"down", f(882.02), f(850.00), true, -32.02},
		{"unchanged", f(8.22), f(8.22), false, 0},
		{"float noise below a cent", f(0.1 + 0.2), f(0.3), false, 0},
		{"sub-cent difference rounds to same cent", f(1.001), f(1.004), false, 0},
		{"previous missing market price", nil, f(1.00), false, 0},
		{"current missing market price", f(1.00), nil, false, 0},
		{"both missing", nil, nil, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, changed := Compare(obs(tt.prev), obs(tt.curr))
			if changed != tt.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if changed && math.Abs(ch.Delta()-tt.wantDelta) > 1e-9 {
				t.Errorf("Delta() = %v, want %v", ch.Delta(), tt.wantDelta)
			}
		})
	}
}

func TestChangePercent(t *testing.T) {
	tests := []struct{ prev, curr, want float64 }{
		{0.10, 0.20, 100},
		{200, 150, -25},
	}
	for _, tt := range tests {
		ch := Change{Previous: obs(f(tt.prev)), Current: obs(f(tt.curr))}
		if got := ch.Percent(); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("Percent(%v -> %v) = %v, want %v", tt.prev, tt.curr, got, tt.want)
		}
	}
}
