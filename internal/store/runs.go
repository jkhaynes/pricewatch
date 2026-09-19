package store

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

func (s *SQLite) StartRun(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO runs (started_at) VALUES (?)`, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("start run: %w", err)
	}
	return res.LastInsertId()
}

func (s *SQLite) FinishRun(ctx context.Context, runID int64, ok, failed, requests int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET finished_at=?, ok_count=?, error_count=?, requests=? WHERE id=?`,
		time.Now().UTC(), ok, failed, requests, runID)
	if err != nil {
		return fmt.Errorf("finish run %d: %w", runID, err)
	}
	return nil
}

func (s *SQLite) Save(ctx context.Context, runID int64, o card.Observation) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO observations
		(run_id, card_id, source, market_price, low_price, high_price, observed_at)
		VALUES (?,?,?,?,?,?,?)`,
		runID, o.CardID, o.Source, o.Market, o.Low, o.High, o.ObservedAt.UTC())
	if err != nil {
		return fmt.Errorf("save %s in run %d: %w", o.CardID, runID, err)
	}
	return nil
}

// Candidates returns every source card a run might price at source, one per
// source card, with what due-date scheduling needs (DD-12). Choosing among
// them is priority.Due's job, not the database's.
func (s *SQLite) Candidates(ctx context.Context, source string) ([]card.Candidate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+mappingCols+`, o.observed_at, o.market_price,
		       (SELECT MAX(c.tcgc_price) FROM collection c WHERE c.collection_key = m.collection_key)
		FROM card_map m
		LEFT JOIN observations o ON o.id = (
		    SELECT MAX(p.id) FROM observations p WHERE p.card_id = m.collection_key)
		WHERE m.source = ? AND m.status = 'resolved'
		  AND EXISTS (SELECT 1 FROM collection c WHERE c.collection_key = m.collection_key)
		ORDER BY m.source_card_id, m.collection_key`, source)
	if err != nil {
		return nil, fmt.Errorf("query candidates: %w", err)
	}
	defer rows.Close()

	var out []card.Candidate
	for rows.Next() {
		var m card.Mapping
		var variant, status string
		var observed *time.Time
		var market, export *float64
		if err := rows.Scan(&m.Key, &m.Source, &m.SourceCardID, &variant, &status, &m.Reason,
			&observed, &market, &export); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		m.Variant, m.Status = card.Variant(variant), card.Status(status)

		if n := len(out); n == 0 || out[n-1].SourceCardID != m.SourceCardID {
			out = append(out, card.Candidate{SourceCardID: m.SourceCardID})
		}
		c := &out[len(out)-1]
		c.Keys = append(c.Keys, m)

		value := 0.0
		if export != nil {
			value = *export
		}
		if observed == nil {
			c.NeverSeen = true
		} else {
			if c.LastChecked.IsZero() || observed.Before(c.LastChecked) {
				c.LastChecked = *observed
			}
			if market != nil {
				value = *market
			}
		}
		c.Value = max(c.Value, value)
	}
	return out, rows.Err()
}

func (s *SQLite) Changes(ctx context.Context, runID int64) ([]card.Change, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT cur.card_id,
		       cur.source, cur.market_price, cur.low_price, cur.high_price, cur.observed_at,
		       prev.source, prev.market_price, prev.low_price, prev.high_price, prev.observed_at
		FROM observations cur
		JOIN observations prev ON prev.id = (
		    SELECT p.id FROM observations p
		    WHERE p.card_id = cur.card_id AND p.id < cur.id
		    ORDER BY p.id DESC LIMIT 1)
		WHERE cur.run_id = ?`, runID)
	if err != nil {
		return nil, fmt.Errorf("query changes for run %d: %w", runID, err)
	}
	defer rows.Close()

	var out []card.Change
	for rows.Next() {
		var cur, prev card.Observation
		if err := rows.Scan(&cur.CardID,
			&cur.Source, &cur.Market, &cur.Low, &cur.High, &cur.ObservedAt,
			&prev.Source, &prev.Market, &prev.Low, &prev.High, &prev.ObservedAt); err != nil {
			return nil, fmt.Errorf("scan change: %w", err)
		}
		prev.CardID = cur.CardID
		if ch, ok := card.Compare(prev, cur); ok {
			out = append(out, ch)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b card.Change) int {
		return cmp.Compare(math.Abs(b.Percent()), math.Abs(a.Percent()))
	})
	return out, nil
}
