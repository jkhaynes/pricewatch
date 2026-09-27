package ask

import (
	"strings"
	"testing"
	"time"
)

func TestQuery(t *testing.T) {
	st := collection(t)
	tests := []struct {
		name          string
		sql           string
		wantCols      []string
		wantRows      int
		wantTruncated bool
		wantErr       string
	}{
		{name: "select", sql: `SELECT card_name, quantity FROM collection WHERE card_name = 'Mudkip' ORDER BY variant`,
			wantCols: []string{"card_name", "quantity"}, wantRows: 2},
		{name: "capped at 500 rows",
			sql:      `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n LIMIT 600) SELECT x FROM n`,
			wantCols: []string{"x"}, wantRows: 500, wantTruncated: true},
		{name: "bad sql is a readable error", sql: `SELECT nope FROM collection`, wantErr: "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testTools().query(t.Context(), st, QueryIn{SQL: tt.sql})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got.Columns, ",") != strings.Join(tt.wantCols, ",") ||
				len(got.Rows) != tt.wantRows || got.Truncated != tt.wantTruncated {
				t.Errorf("got cols %v, %d rows, truncated %v", got.Columns, len(got.Rows), got.Truncated)
			}
		})
	}
}

func TestQueryTimesOut(t *testing.T) {
	tt := testTools()
	tt.queryTimeout = 50 * time.Millisecond
	start := time.Now()
	_, err := tt.query(t.Context(), collection(t),
		QueryIn{SQL: `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n) SELECT COUNT(*) FROM n`})
	if err == nil {
		t.Fatal("an endless query returned no error")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v to give up; the timeout was 50ms", time.Since(start))
	}
}

func TestQueryDocNamesTheSource(t *testing.T) {
	if doc := queryDoc("pokewallet"); !strings.Contains(doc, "source = 'pokewallet'") {
		t.Errorf("query doc does not tell the model which source to filter on:\n%s", doc)
	}
}
