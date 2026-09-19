package priority

import (
	"slices"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

var now = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func TestDefaultIntervals(t *testing.T) {
	tests := []struct {
		value float64
		want  time.Duration
	}{
		{400, day}, {100, day},
		{99.99, 2 * day}, {20, 2 * day},
		{19.99, 4 * day}, {5, 4 * day},
		{4.99, 7 * day}, {1, 7 * day}, {0.06, 7 * day}, {0, 7 * day}, // nothing waits longer than a week
	}
	for _, tt := range tests {
		if got := Default.Interval(tt.value); got != tt.want {
			t.Errorf("Interval(%v) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func seen(id string, value float64, ago time.Duration) card.Candidate {
	return card.Candidate{SourceCardID: id, Value: value, LastChecked: now.Add(-ago)}
}

func never(id string, value float64) card.Candidate {
	return card.Candidate{SourceCardID: id, Value: value, NeverSeen: true}
}

func ids(cs []card.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.SourceCardID)
	}
	return out
}

func TestDue(t *testing.T) {
	tests := []struct {
		name      string
		cands     []card.Candidate
		n         int
		wantIDs   []string
		wantTotal int
	}{
		{"never-priced cards first, most valuable first",
			[]card.Candidate{never("cheap", 0.06), seen("rich-late", 400, 3*day), never("rich", 400)},
			10, []string{"rich", "cheap", "rich-late"}, 3},
		{"not due yet is left out: stop early",
			[]card.Candidate{seen("rich-fresh", 400, 12*time.Hour), seen("cheap-fresh", 0.06, 5*day)},
			10, nil, 0},
		{"each card on its own tier's clock",
			[]card.Candidate{seen("rich", 400, 25*time.Hour), seen("mid", 10, 3*day), seen("mid-due", 10, 4*day)},
			10, []string{"rich", "mid-due"}, 2},
		{"one hour of slack keeps an hourly schedule from drifting",
			[]card.Candidate{seen("rich", 400, 23*time.Hour+30*time.Minute), seen("rich-early", 400, 22*time.Hour)},
			10, []string{"rich"}, 1},
		{"most overdue relative to its own interval comes first",
			// cheap: 14 days of a 7-day interval (2.0); rich: 30 hours of a 1-day interval (1.25)
			[]card.Candidate{seen("rich", 400, 30*time.Hour), seen("cheap", 0.06, 14*day)},
			10, []string{"cheap", "rich"}, 2},
		{"equally overdue: value breaks the tie (DD-8), then ID",
			[]card.Candidate{seen("b", 5, 8*day), seen("a", 5, 8*day), seen("rich", 10, 8*day)},
			10, []string{"rich", "a", "b"}, 3},
		{"budget caps the pick but the due count is complete",
			[]card.Candidate{never("a", 1), never("b", 2), never("c", 3)},
			2, []string{"c", "b"}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, total := Due(tt.cands, now, tt.n, Default)
			if !slices.Equal(ids(got), tt.wantIDs) || total != tt.wantTotal {
				t.Errorf("Due = %v (%d due), want %v (%d due)", ids(got), total, tt.wantIDs, tt.wantTotal)
			}
		})
	}
}

func TestZeroPolicyTreatsEverythingAsDue(t *testing.T) {
	got, total := Due([]card.Candidate{seen("a", 1, time.Minute)}, now, 10, Policy{})
	if total != 1 || len(got) != 1 {
		t.Errorf("zero policy: %v (%d due), want everything due", ids(got), total)
	}
}
