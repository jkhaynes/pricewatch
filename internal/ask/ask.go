// Package ask answers questions about the collection as MCP tools (DD-15).
// Every tool reads through Store, and every answer says how fresh it is.
package ask

import (
	"context"
	"math"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Store is what the tools read. *store.SQLite satisfies it.
type Store interface {
	Listings(ctx context.Context, source string) ([]card.Listing, error)
	History(ctx context.Context, source string) ([]card.Observation, error)
	LastFinished(ctx context.Context) (*time.Time, error)
	MappingCounts(ctx context.Context, source string) (map[card.Status]int, error)
	Unresolved(ctx context.Context, source string) ([]card.Mapping, error)
	RunsSince(ctx context.Context, since time.Time) ([]card.Run, error)
	QuotaUsed(ctx context.Context, source, day string) (int, error)
	Query(ctx context.Context, query string, maxRows int) (cols []string, rows [][]any, truncated bool, err error)
}

// Acquire hands out an open Store. release must be called when the tool is
// done with it. stale explains why the data may be out of date.
type Acquire func(ctx context.Context) (st Store, release func(), stale string, err error)

// Answer wraps every tool's result with how fresh the data is.
type Answer[T any] struct {
	DataAsOf *time.Time `json:"data_as_of" jsonschema:"when the latest pricing run finished; null if none has"`
	Stale    string     `json:"stale,omitempty" jsonschema:"set when the data could not be refreshed, with the reason"`
	Result   T          `json:"result"`
}

type tools struct {
	source       string
	now          func() time.Time
	queryTimeout time.Duration
}

// NewServer returns the MCP server with every tool registered.
func NewServer(acq Acquire, source string, now func() time.Time) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "pricewatch", Version: "v1"}, nil)
	t := tools{source: source, now: now, queryTimeout: 10 * time.Second}
	add(s, acq, "collection_value", valueDoc, t.collectionValue)
	add(s, acq, "top_cards", topDoc, t.topCards)
	add(s, acq, "find_cards", findDoc, t.findCards)
	add(s, acq, "price_history", historyDoc, t.priceHistory)
	add(s, acq, "movers", moversDoc, t.movers)
	add(s, acq, "pipeline_status", statusDoc, t.pipelineStatus)
	add(s, acq, "query", queryDoc(source), t.query)
	return s
}

// add registers fn as a tool: it acquires the store, runs fn and wraps the
// result in an Answer. An error from fn becomes a tool error the model can
// read, never a crash.
func add[In, Out any](s *mcp.Server, acq Acquire, name, doc string, fn func(context.Context, Store, In) (Out, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: doc},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Answer[Out], error) {
			st, release, stale, err := acq(ctx)
			if err != nil {
				return nil, Answer[Out]{}, err
			}
			defer release()
			out, err := fn(ctx, st, in)
			if err != nil {
				return nil, Answer[Out]{}, err
			}
			asOf, err := st.LastFinished(ctx)
			if err != nil {
				return nil, Answer[Out]{}, err
			}
			return nil, Answer[Out]{DataAsOf: asOf, Stale: stale, Result: out}, nil
		})
}

// Card is one collection key: one exact print, never merged with another
// variant of the same card.
type Card struct {
	Key       string     `json:"collection_key" jsonschema:"identifies this exact print; pass it to price_history"`
	Name      string     `json:"name"`
	Expansion string     `json:"expansion"`
	Number    string     `json:"number"`
	Variant   string     `json:"variant"`
	Quantity  int        `json:"quantity" jsonschema:"copies owned"`
	Status    string     `json:"status" jsonschema:"how the card resolved at the price source: resolved, ambiguous, unmatched or unmapped"`
	Price     *float64   `json:"price" jsonschema:"latest market price in USD; null if never priced"`
	PricedAt  *time.Time `json:"priced_at" jsonschema:"when that price was observed"`
	Value     *float64   `json:"value" jsonschema:"price times quantity"`
}

func newCard(l card.Listing, s site.Series) Card {
	c := Card{Key: l.Key, Name: l.Name, Expansion: l.Expansion, Number: l.Number, Variant: l.VariantLabel,
		Quantity: l.Quantity, Status: string(l.Status)}
	if c.Status == "" {
		c.Status = "unmapped"
	}
	if len(s) > 0 {
		last := s[len(s)-1] // History only returns observations with a market price
		v := cents(*last.Market * float64(l.Quantity))
		c.Price, c.PricedAt, c.Value = last.Market, &last.ObservedAt, &v
	}
	return c
}

// cards reads every collection key with its latest price, plus the full
// history by key for tools that need more than the latest.
func (t tools) cards(ctx context.Context, st Store) ([]Card, map[string]site.Series, error) {
	listings, err := st.Listings(ctx, t.source)
	if err != nil {
		return nil, nil, err
	}
	obs, err := st.History(ctx, t.source)
	if err != nil {
		return nil, nil, err
	}
	hist := site.ByKey(obs)
	out := make([]Card, len(listings))
	for i, l := range listings {
		out[i] = newCard(l, hist[l.Key])
	}
	return out, hist, nil
}

// cents rounds to whole cents, so sums of prices don't show float noise.
func cents(x float64) float64 { return math.Round(x*100) / 100 }
