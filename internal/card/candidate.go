package card

import "time"

// Candidate is one source card a run might price: every resolved collection
// row it covers, and what due-date scheduling needs to know about it (DD-12).
type Candidate struct {
	SourceCardID string
	Keys         []Mapping
	NeverSeen    bool      // at least one row has never been priced
	LastChecked  time.Time // the oldest of its rows' latest observations; zero if none
	Value        float64   // its most valuable row: latest market price, else export price
}
