package ask

import (
	"context"
	"fmt"
)

const maxQueryRows = 500

// queryDoc is built per source, because every card_map and observations query
// must filter on it.
func queryDoc(source string) string {
	return fmt.Sprintf(`Run one read-only SQLite SELECT against the pricewatch database, for questions the
other tools don't answer. At most 500 rows, 10 second limit. Writes are refused.

Tables:
- collection(collection_key, card_name, card_number, expansion, rarity, variant, language,
  condition, quantity, tcgc_price): one row per line of the TCG Collector export. Several rows
  can share a collection_key (the same print in different conditions); sum quantity over them.
- card_map(collection_key, source, source_card_id, variant, status, reason): how each key
  resolved at a price source. status is resolved, ambiguous or unmatched; reason says why not.
- observations(run_id, card_id, source, market_price, low_price, high_price, observed_at): price
  history in USD. card_id is the collection_key. market_price can be NULL.
- runs(id, started_at, finished_at, ok_count, error_count, requests): pricing runs.
- quota(source, day, used): requests spent per UTC day.

Rules, or the answer will be wrong:
- Always filter card_map and observations on source = '%s'.
- A card's current price is its LATEST observation with a non-NULL market_price, not an average
  or a sum over observations.
- Value is current price times quantity.
- collection_key identifies one exact print. Never add or average prices across different
  collection_keys of the same card: Normal and Reverse Holo prices differ, often by far more
  than any price movement.
- tcgc_price is TCG Collector's price when the export was made, not a current market price.`, source)
}

type QueryIn struct {
	SQL string `json:"sql" jsonschema:"one SQLite SELECT statement"`
}

type QueryOut struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated,omitempty" jsonschema:"more than 500 rows matched; narrow the query"`
}

func (t tools) query(ctx context.Context, st Store, in QueryIn) (QueryOut, error) {
	ctx, cancel := context.WithTimeout(ctx, t.queryTimeout)
	defer cancel()
	cols, rows, truncated, err := st.Query(ctx, in.SQL, maxQueryRows)
	if err != nil {
		return QueryOut{}, err
	}
	return QueryOut{Columns: cols, Rows: rows, Truncated: truncated}, nil
}
