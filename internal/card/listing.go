package card

import "time"

// Listing describes one collection key for display: what the collector calls
// it, what it was worth at export, and how it maps to one source.
type Listing struct {
	Key          string
	Name         string
	Expansion    string
	Number       string
	VariantLabel string   // as TCG Collector writes it, e.g. "Reverse Holo"
	Export       *float64 // the highest export price among its rows (DD-6)
	Quantity     int      // copies across its rows
	Status       Status   // "" while the key has no mapping at this source
	SourceCardID string
	Image        string // card art URL; "" until the card has been priced
}

// Run is one pricing run as recorded.
type Run struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt *time.Time // nil: the run never finished
	Requests   int
}
