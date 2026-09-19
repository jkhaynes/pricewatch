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

func (s *SQLite) FinishRun(ctx context.Context, runID int64, ok, failed int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET finished_at=?, ok_count=?, error_count=? WHERE id=?`,
		time.Now().UTC(), ok, failed, runID)
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

func (s *SQLite) Stalest(ctx context.Context, source string, cards int) ([]card.Mapping, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH live AS (
		    SELECT m.collection_key, m.source_card_id, o.last_id,
		           (SELECT MAX(c.tcgc_price) FROM collection c
		             WHERE c.collection_key = m.collection_key) AS value
		    FROM card_map m
		    LEFT JOIN (SELECT card_id, MAX(id) AS last_id FROM observations GROUP BY card_id) o
		           ON o.card_id = m.collection_key
		    WHERE m.source = ?1 AND m.status = 'resolved'
		      AND EXISTS (SELECT 1 FROM collection c WHERE c.collection_key = m.collection_key)
		),
		-- MATERIALIZED: compute the pick once. Left to itself, SQLite sometimes
		-- re-ran it for every card_map row, which at ~8,800 rows never finished.
		picked AS MATERIALIZED (
		    SELECT source_card_id,
		           MAX(last_id IS NULL)      AS never_seen,
		           MIN(COALESCE(last_id, 0)) AS oldest,
		           MAX(COALESCE(value, 0))   AS value  -- DD-8: a card is worth its most valuable variant
		    FROM live
		    GROUP BY source_card_id
		    ORDER BY never_seen DESC, oldest, value DESC, source_card_id
		    LIMIT ?2
		)
		SELECT `+mappingCols+`
		FROM card_map m
		JOIN picked p ON p.source_card_id = m.source_card_id
		WHERE m.source = ?1 AND m.status = 'resolved'
		  AND EXISTS (SELECT 1 FROM collection c WHERE c.collection_key = m.collection_key)
		ORDER BY p.never_seen DESC, p.oldest, p.value DESC, m.source_card_id, m.collection_key`, source, cards)
	if err != nil {
		return nil, fmt.Errorf("query stalest: %w", err)
	}
	return collectMappings(rows)
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
