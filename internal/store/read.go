package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

// Listings returns every distinct collection key with what the status page
// shows about it. A key's rows share a name, set, number and variant, so MIN
// just picks that shared value.
func (s *SQLite) Listings(ctx context.Context, source string) ([]card.Listing, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.collection_key, MIN(c.card_name), MIN(c.expansion), MIN(c.card_number), MIN(c.variant),
		       MAX(c.tcgc_price), SUM(c.quantity),
		       COALESCE(MIN(m.status), ''), COALESCE(MIN(m.source_card_id), ''), COALESCE(MIN(a.image_url), '')
		FROM collection c
		LEFT JOIN card_map m ON m.collection_key = c.collection_key AND m.source = ?1
		LEFT JOIN card_art a ON a.source = ?1 AND a.source_card_id = m.source_card_id
		GROUP BY c.collection_key
		ORDER BY c.collection_key`, source)
	if err != nil {
		return nil, fmt.Errorf("query listings: %w", err)
	}
	defer rows.Close()
	var out []card.Listing
	for rows.Next() {
		var l card.Listing
		var status string
		if err := rows.Scan(&l.Key, &l.Name, &l.Expansion, &l.Number, &l.VariantLabel,
			&l.Export, &l.Quantity, &status, &l.SourceCardID, &l.Image); err != nil {
			return nil, fmt.Errorf("scan listing: %w", err)
		}
		l.Status = card.Status(status)
		out = append(out, l)
	}
	return out, rows.Err()
}

// History returns every observation with a market price, for keys still in
// the collection, ordered by key and then time. Observations without a market
// price can never be a baseline or a current price (DD-14).
func (s *SQLite) History(ctx context.Context, source string) ([]card.Observation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.card_id, o.market_price, o.observed_at
		FROM observations o
		WHERE o.source = ? AND o.market_price IS NOT NULL
		  AND EXISTS (SELECT 1 FROM collection c WHERE c.collection_key = o.card_id)
		ORDER BY o.card_id, o.observed_at, o.id`, source)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer rows.Close()
	var out []card.Observation
	for rows.Next() {
		o := card.Observation{Source: source}
		var market float64
		if err := rows.Scan(&o.CardID, &market, &o.ObservedAt); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		o.Market = &market
		out = append(out, o)
	}
	return out, rows.Err()
}

// RunsSince returns the runs started at or after since, oldest first.
func (s *SQLite) RunsSince(ctx context.Context, since time.Time) ([]card.Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, started_at, finished_at, requests
		FROM runs WHERE started_at >= ? ORDER BY id`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()
	var out []card.Run
	for rows.Next() {
		var r card.Run
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.FinishedAt, &r.Requests); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
