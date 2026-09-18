# PRD: Card Price Watcher (Go)

| | |
|---|---|
| **Status** | Draft |
| **Author** | Jessica Haynes |
| **Created** | 2026-09-18 |
| **Target** | v1 in a weekend, later phases optional |

## 1. Summary

A command line tool that tracks market prices for a Pokémon TCG collection imported from
TCG Collector. It works through the collection incrementally against a rate-limited public
API, stores a price history, and reports what moved.

**Scale: roughly 8,000 to 9,000 rows.** Against a 1,000 request daily budget a full pass
takes about nine days. So the tool does not sweep. It makes **durable, prioritized
progress**, and that constraint drives most of the design below.

The product is real but secondary. **The primary objective is hands-on Go experience**, and
scoping decisions favour whichever option exercises more Go.

## 2. Background

This is a learning project. My background is C# and .NET, and I wanted hands-on experience
with Go's concurrency model, error handling and interface design on a problem with real
constraints rather than a tutorial.

Two things made this a better vehicle than a toy project:

- A rate-limited API and a collection of several thousand cards means the interesting
  question is *what do I check next*, not *how do I fetch things in parallel*.
- Matching records across two systems that share no key is a genuine data problem, and it
  fails silently if you get it wrong.

Phase 3 adds a message broker, which the workload justifies on its own terms: work that
spans days, must survive restarts, and needs retry with dead-lettering.

A secondary goal is a written comparison of Go against nine years of C#, which lives in
the README.

## 3. Goals

1. Produce working, idiomatic Go exercising concurrency, context, error handling and interfaces
2. Finish something complete rather than abandon something ambitious
3. Produce a written account of Go versus C# from real use
4. Leave a clean seam so a message broker can be added later without restructuring
5. Be genuinely useful for collection tracking

## 4. Non-goals

- Not a product for anyone else to run or deploy
- Not a web application, API, or UI
- Not a general TCG platform. Pokémon only, one source at a time
- No authentication, multi-user support, or hosting
- Not optimizing for coverage or completeness over learning

## 5. Users

**Primary:** the author, tracking a personal collection and watching for movement.

**Secondary:** a local card shop, as a reference point when evaluating trade-ins. Enough to
keep the project honest, not enough to justify building for them.

## 6. Functional requirements

| ID | Requirement | Priority |
|---|---|---|
| FR-1 | Import a TCG Collector collection export | P0 |
| FR-1a | Resolve each imported row to a card identifier in the price source, and persist the mapping | P0 |
| FR-1b | Report rows that could not be resolved, or resolved ambiguously, rather than dropping them silently | P0 |
| FR-2 | Fetch current prices concurrently using a bounded worker pool | P0 |
| FR-2a | Select which cards to check next, stalest observation first | P0 |
| FR-2b | Progress persists across runs. The next run continues where the last stopped | P0 |
| FR-3 | Respect the source's published rate limit | P0 |
| FR-4 | Apply a per-request timeout | P0 |
| FR-5 | A failed card is logged and skipped. The run continues | P0 |
| FR-6 | Persist one price observation per card per run | P0 |
| FR-7 | Report cards whose price changed since that card's previous observation, not since the previous run | P0 |
| FR-8 | Ctrl-C cancels cleanly without dropping in-flight work | P0 |
| FR-9 | Retry transient failures with backoff | P1 |
| FR-10 | A second price source implementation, swappable by flag | P1 |
| FR-10a | Weight check priority by card value, so a $400 card is checked more often than a $0.06 one | P1 |
| FR-11 | Durable job queue with acks, retry and dead-lettering | P2 |
| FR-12 | Publish price events consumed independently by persister, mover detector and notifier | P2 |
| FR-13 | Discord webhook on significant price movement | P2 |

## 7. Technical design

### 7.1 Architecture, v1

Single binary, two commands. `import` loads and resolves the export once. `run` selects the
next slice of work by staleness, feeds it onto a job channel, a bounded worker pool consumes
it against a rate limiter, results flow back on a results channel, and a single consumer
persists and reports changes.

**A run is a slice, not a sweep.** It processes as much as the budget allows and stops.
State lives in the database, so the next run picks up from there.

```
export.csv -> import -> collection + card_map
                              |
run -> select stalest -> jobs chan -> [worker pool] -> results chan -> persist + report
                                           |
                                      PriceSource (HTTP, rate limited)
```

### 7.2 Package layout

```
cmd/pricewatch/main.go     flag parsing, wiring, signal handling
internal/card/             domain types: Card, Observation, Change
internal/source/           PriceSource implementations
internal/store/            Store implementations
internal/pipeline/         worker pool and orchestration
```

### 7.3 Data model

```sql
CREATE TABLE runs (
  id          INTEGER PRIMARY KEY,
  started_at  TIMESTAMP NOT NULL,
  finished_at TIMESTAMP,
  ok_count    INTEGER NOT NULL DEFAULT 0,
  error_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE observations (
  id           INTEGER PRIMARY KEY,
  run_id       INTEGER NOT NULL REFERENCES runs(id),
  card_id      TEXT NOT NULL,
  source       TEXT NOT NULL,
  market_price REAL,
  low_price    REAL,
  high_price   REAL,
  observed_at  TIMESTAMP NOT NULL
);

CREATE INDEX idx_observations_card_time ON observations(card_id, observed_at DESC);

-- Imported straight from the TCG Collector export
CREATE TABLE collection (
  id            INTEGER PRIMARY KEY,
  tcg_region    TEXT NOT NULL,
  card_name     TEXT NOT NULL,
  card_number   TEXT NOT NULL,   -- "59/109"
  sort_number   INTEGER,
  expansion     TEXT NOT NULL,   -- display name, e.g. "EX Ruby & Sapphire"
  rarity        TEXT,
  variant       TEXT NOT NULL,   -- "Normal", "Reverse Holo", "1st Edition Holo"
  language      TEXT,
  condition     TEXT,
  quantity      INTEGER NOT NULL DEFAULT 1,
  tcgc_price    REAL,            -- snapshot price from the export
  note          TEXT
);

-- Resolution cache. Resolve once, reuse forever.
CREATE TABLE card_map (
  collection_key TEXT PRIMARY KEY,  -- region|expansion|number|variant|language
  source         TEXT NOT NULL,
  source_card_id TEXT,
  status         TEXT NOT NULL,     -- resolved | ambiguous | unmatched
  resolved_at    TIMESTAMP
);
```

`card_map` is why resolution stays cheap after the first run. Resolving is the expensive,
rate-limited part and only has to happen once per distinct card.

### 7.4 Interfaces

Declared in the package that consumes them, not next to their implementations.

```go
type PriceSource interface {
    Price(ctx context.Context, cardID string) (card.Observation, error)
}

type Store interface {
    StartRun(ctx context.Context) (int64, error)
    Save(ctx context.Context, runID int64, obs card.Observation) error
    FinishRun(ctx context.Context, runID int64, ok, failed int) error
    Changes(ctx context.Context, runID int64) ([]card.Change, error)
}
```

### 7.5 Dependencies

- `golang.org/x/time/rate` for rate limiting
- `modernc.org/sqlite` for storage, pure Go so no cgo, which matters on Windows

Everything else standard library: `net/http`, `encoding/json`, `encoding/csv`,
`database/sql`, `flag`, `log/slog`, `context`, `errors`, `sync`, `os/signal`.

## 8. Design decisions

### DD-1: Price source behind an interface

**Decision:** `PriceSource` is an interface with at least one implementation, swappable by flag.

**Rationale:** not theoretical. pokemontcg.io migrated to Scrydex, which has no free tier,
before any code was written. The source is the least stable part of the system.

### DD-2: Job producer returns a channel

**Decision:** the pipeline consumes `<-chan Job` and never learns where jobs originate.

**Rationale:** in v1 a database query fills the channel. In phase 3 a RabbitMQ consumer fills
the same channel. The worker code does not change.

### DD-3: RabbitMQ over Kafka for phase 3

**Decision:** RabbitMQ.

**Rationale:** this is a work queue with retry semantics, not an event log. Acks, requeue,
dead-letter exchanges and prefetch limits are first-class in RabbitMQ. Kafka is a durable log
and using it for job retry fights the tool.

### DD-4: Tight rate limits are a design constraint, not an obstacle

**Decision:** build against a free tier with real limits rather than paying for headroom.

**Rationale:** thousands of cards against a 1,000/day budget forces scheduling, caching and
prioritization. That is a genuine design problem and more Go practice than an unlimited API.

### DD-5: Identifier resolution is a first-class component

**Decision:** a dedicated resolution step maps TCG Collector rows to price source card IDs,
persists the result in `card_map`, and reports failures explicitly.

**Rationale:** the export has no stable card identifier. The join key must be built from
region, expansion, card number, variant and language. Expansion is a display name rather than
a set code, and card number is `"59/109"` rather than `59`.

**Variant is the hard part.** One card number can appear as Normal, Reverse Holo, 1st Edition,
Jumbo, and World Championship prints, differing enormously in value. A variant mismatch does
not fail loudly, it returns a confidently wrong price. **Ambiguous matches are failures to be
reported, never guesses to be made.**

### DD-6: The product is price history, not price lookup

**Decision:** the export's existing `Price` column is stored as a snapshot, not treated as
the output.

**Rationale:** TCG Collector already shows a current price. It does not show movement over
time. History and change detection are the reason this tool exists.

### DD-7: A run is a bounded slice of work, not a full pass

**Decision:** each run processes as much as the rate budget allows, choosing the stalest
observations first, then stops. Progress is durable.

**Rationale:** 8,000 to 9,000 rows against 1,000 requests a day means a full pass takes about
nine days. "Check everything" is not an available operation, so the design question becomes
*what do I check next*.

**Consequence:** "changed since the last run" is meaningless when a run touches an eleventh of
the collection. Change detection compares against that card's previous observation.

**Phase 2 refinement:** staleness alone is a weak priority when most of the collection is
worth pennies. Weighting by card value means a $400 card gets checked often and a $0.06 card
rarely.

## 9. Acceptance criteria, v1

- [ ] `pricewatch import export.csv` loads the collection and reports how many rows resolved, were ambiguous, or went unmatched
- [ ] `pricewatch run` fetches prices for resolved cards and persists them
- [ ] A second run continues from where the first stopped rather than starting over
- [ ] Changes are reported against each card's previous observation
- [ ] Cards are selected stalest first
- [ ] An invalid card ID is logged and the run completes
- [ ] The published rate limit is never exceeded
- [ ] Ctrl-C mid-run exits cleanly with in-flight work either finished or explicitly abandoned
- [ ] Table-driven tests covering the diff logic and at least one error path
- [ ] README includes the Go versus C# section

## 10. Phases

**Phase 1, v1.** FR-1 through FR-8. Complete and useful on its own.

**Phase 2, v1.1.** FR-9, FR-10, FR-10a. Retry with backoff, second source, value-weighted priority.

**Phase 3, v2.** FR-11 through FR-13. RabbitMQ job dispatch with dead-lettering, event fan-out
to independent consumers, Discord notification. This is the phase that addresses the
messaging gap.

Each phase leaves something complete.

## 11. Open questions

### Resolved

- **Which price source.** pokemontcg.io is deprecated: new registrations closed, existing keys
  work only through 2027-03-01. Use TCGdex or PokeWallet as primary. An existing pokemontcg.io
  key becomes the second implementation.
- **Where the card list comes from.** A TCG Collector collection export.
- **Collection scale.** Roughly 8,000 to 9,000 rows.

### Still open

- **Which source handles variants best.** This matters more than rate limits. A source that
  cannot distinguish Reverse Holo from Normal Holo is unusable here. Test both against a few
  known multi-variant cards before committing.
- **Whether to price by condition.** The export carries a condition column and conditions
  differ a lot in value. Simplest v1 is market price only, stated explicitly.
- Whether graded pricing ever matters enough to justify Scrydex at $29/month.

## 12. Risks

| Risk | Mitigation |
|---|---|
| Free API changes or disappears again | DD-1. The interface exists for exactly this |
| Scope creep into a web UI or a product | Section 4. Non-goals are explicit |
| Phase 3 never happens | DD-2 keeps the cost of phase 3 low, and phase 1 stands on its own |
| Time lost to setup rather than Go | Minimal dependencies, pure-Go SQLite, no Docker in v1 |
| Variant mismatches produce silently wrong prices | DD-5. Treat ambiguous matches as failures, not guesses |
| Expansion display name to set code has no clean mapping source | Maintain it as data. Seed from the source API's set list, accept manual entries for the long tail |

## Appendix A: price source comparison

| API | Free tier | Pricing data | Key |
|---|---|---|---|
| TCGdex | Free, open source, no published limit | Market integration | None |
| PokéWallet | 100/hr, 1,000/day | Yes | Free, no card |
| pokemontcg.io | Deprecated. Existing keys work to 2027-03-01 | TCGplayer, Cardmarket | Closed to new users |
| Scrydex | None. $29/mo for 5,000 credits | Graded prices, population reports, sales history | Paid |

## Appendix B: the README section

A short "what surprised me coming from C#", written during the build rather than after.

Have an opinion on:

- Errors as values and the `if err != nil` rhythm versus exceptions
- `context.Context` in every signature versus `CancellationToken`
- Goroutines and channels versus `async`/`await` and `Task`
- Implicit interface satisfaction, and writing the interface where it is consumed
- Struct embedding instead of inheritance
- How far the standard library goes before reaching for a package
