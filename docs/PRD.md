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
- Not a web application, API, or UI. The one planned exception is phase 4's local, read-only
  dashboard (DD-10), which changes none of the other non-goals
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
| FR-10 | A second price source implementation, swappable by flag. Moved out of phase 2 to section 13, idea 5 (2026-09-19) | Idea |
| FR-10a | Weight check priority by card value, so a $400 card is checked more often than a $0.06 one | P1 |
| FR-11 | Durable job queue with acks, retry and dead-lettering | P2 |
| FR-12 | Publish price events consumed independently by persister, mover detector and notifier | P2 |
| FR-13 | Discord webhook on significant price movement | P2 |
| FR-14 | Runs execute unattended on a schedule, never overlap, and write their report somewhere readable afterwards | P1 |
| FR-15 | A local, read-only dashboard: collection value over time with pricing coverage, biggest movers, and unresolved cards | P3 |

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

### DD-8: The export price breaks staleness ties

**Decision:** when choosing what to check next, cards are ordered by staleness first. Cards
that are equally stale are ordered by their TCG Collector export price, highest first. A
source card that covers several collection rows (Normal and Reverse Holo, priced by one
request) is worth its most valuable row. A missing price counts as zero. The source card ID
is the final tie-breaker, so the order is deterministic.

**Rationale:** on the first pass every card is tied at "never seen", so the tie-breaker
decides the order of the first ~5,400 requests, about five and a half days. Without it that
order comes from the source's opaque card IDs, and a $400 card could wait days behind
$0.06 commons. The export already carries a price snapshot for every row (DD-6), so this
costs one column in the selection query and needs no extra requests. Because the first pass
runs in value order, later re-checks keep roughly that order.

**Scope:** this is a tie-breaker, not value weighting. It never makes a card get checked
*more often* than staleness allows, and a stale cheap card still comes before a fresher
expensive one. Checking valuable cards more often remains the phase 2 refinement to DD-7
(FR-10a). The snapshot price is used only for ordering, never reported as a market price.

### DD-9: Scheduling is an external trigger; the database stays the source of truth

**Decision:** in phase 2, unattended runs are started by the operating system's scheduler
(Windows Task Scheduler, or cron elsewhere) running `pricewatch run`. Pricewatch gains no
daemon of its own. Runs must not overlap. A run that finds another still open exits without
selecting work, and the scheduled task also disallows parallel instances. Each run appends
its report to a log file, since nobody reads its terminal. In phase 3 the same scheduler
triggers the producer that queues the stalest cards, and the RabbitMQ consumer runs
continuously.

**Rationale:** DD-7 already makes a run a durable, resumable slice, so scheduling needs a
trigger, not new machinery. RabbitMQ does not replace the scheduler. A broker moves,
retries and dead-letters work, but something still has to decide when to look for it.
Which cards are due stays a database question (DD-7, DD-8): one query, deterministic, and
easy to inspect.

**Guidance:**
- Prices change at most daily, so re-checking a card more than once a day wastes budget.
- Smaller, frequent runs spread the daily allowance best, for example hourly runs of about
  40 requests against PokeWallet's 100 per hour and 1,000 per day.
- The durable quota stops any excess regardless.

**Phase 3 experiment, not a commitment:** broker-side rescheduling, where a priced card is
republished with a TTL and dead-lettered back into the work queue when it is due, is a
worthwhile RabbitMQ exercise and a natural fit for value weighting (FR-10a). If tried, its
schedule is only a hint. A periodic sweep re-queues anything the database says is overdue,
because a purged queue or a lost message would otherwise drop a card from rotation silently.

### DD-10: The phase 4 dashboard is local and read-only

**Decision:** phase 4 adds `pricewatch serve`, an HTTP server bound to `127.0.0.1` for the
author only. It reads the existing database and never writes to it. It has no
authentication, no API for other programs, no hosting, and no deployment. Its first views
are:
- collection value over time, with a pricing-coverage line;
- biggest movers;
- unresolved cards with their reasons.

**Rationale:** value over time and its caveats are hard to present in a terminal.

The caveats are:
- runs are slices (DD-7), so on any day part of the total is days old;
- during the first pass the total rises simply because coverage rises;
- unpriced and excluded cards must be left out, or shown only as labelled export snapshots
  (DD-6).

A chart with a coverage line shows all of this honestly. It is also the next step in Go
practice: `net/http` routing, `html/template` with `//go:embed`, graceful shutdown with
`http.Server.Shutdown`, and server-sent events.

**Relationship to phase 3:** the dashboard is built after phase 3 and becomes one more
independent consumer of the price events (FR-12), pushing live updates to the browser with
server-sent events. That doubles as a test that the event fan-out really is independent.

**Prerequisite:** the "value as of day X" query, meaning each card's latest observation as
of that day times its quantity, is built and table-tested before any view renders it.

### DD-11: Pace against the server's own hourly count: burst, then wait out the window

**Decision:** the shared HTTP client stops spacing requests evenly across the hour. When a
source reports its hourly count in response headers (PokeWallet sends
`X-RateLimit-Limit-Hour` and `X-RateLimit-Remaining-Hour`), the client:
- **sends immediately while the server says requests remain this hour**, subject only to a
  small politeness cap (PokeWallet: 2 per second);
- **re-syncs its count from every response**, and reserves a request locally before sending,
  so concurrent workers cannot overshoot between responses;
- **when the server says none remain, waits until the window has certainly reset**: one hour
  after the first request it saw in the current window. The first request of a window is the
  one whose response shows `remaining = limit - 1`; failing that, it is the first request this
  process made. The wait honours cancellation, so Ctrl-C still works while it waits;
- **falls back to today's even spacing** for a source that sends no hourly headers.

The daily quota is unchanged: it stays durable, in SQLite, and synced from headers. A 429
still stops dispatch cleanly and defers the remaining cards. That is the backstop if the
count is ever wrong, with one exception.

**An hourly 429 is waited out.** A process starting mid-hour sends one request before it
knows the count. If the hour is already spent, that request draws a 429. When the 429's own
headers say the hour is spent but the day is not, the client records "none left", waits out
the window, and retries the same request, up to 3 times. A daily-limit 429, or one carrying
no counts, still stops dispatch immediately, so re-running a command is always safe.

**PokeWallet resets on the clock hour.** It sends no reset time: no `Retry-After` and no
`X-RateLimit-Reset`. But its allowance ran out at 23:29 local on 2026-09-18 and was full
again at 00:06. A rolling window would still have been spent until about 00:28, so the
reset is at the top of the hour. The window type is a per-source setting:
- **`ClockHour`** (PokeWallet) waits until the next UTC hour boundary plus 30 seconds for
  clock differences, measured from when the hour was found spent. That is at most an hour
  and about half an hour on average, and exact even after a restart.
- **`Rolling`**, the default for a source whose reset is unknown, keeps the conservative
  rule above: a full hour after the window's first request.

If PokeWallet ever still reports the hour spent just after a boundary, the hourly-429 retry
learns that, waits again, and carries on.

**Header semantics belong to the provider.** The shared client expects the count left
*after* the request that carries the headers. PokeWallet reports the count from *before*
it: a fresh key's first response said 100 of 100. Reading that as "after" sent one request
too many and drew a 429 on the first real import (2026-09-18). The PokeWallet provider now
subtracts the carrying request from both the hourly and the daily figures.

**Rationale:** the hourly cap binds either way, so throughput over any stretch longer than an
hour is identical. What changes:
- **Short runs finish in minutes instead of up to an hour.** A `--budget 5` run takes seconds
  rather than about two and a half minutes.
- **It suits scheduled runs (DD-9).** A run fires, spends its allowance, and exits, which
  lowers the risk of overlap and of a sleeping laptop interrupting it.
- **The worker pool now matters,** because requests are no longer serialised 36 seconds apart.

The two reasons for the original even spacing are handled directly instead of by caution:
- **The in-memory limiter forgot the hourly count on restart.** The server's own count is now
  the source of truth.
- **It is unknown whether the source counts a fixed clock hour or a rolling 60 minutes.**
  Waiting a full hour after the window's first request is safe under both. After a restart,
  the process's own first request is later than the real window start, so the wait can only
  be longer than necessary, never shorter.

**Consequence:** work beyond one hour's allowance now arrives in bursts, for example 100
requests in about a minute, a pause of up to an hour, then the next burst, instead of a
steady trickle. A 150-request import finishes about as late as before, but most of it is done
in the first few minutes.

## 9. Acceptance criteria, v1

- [x] `pricewatch import export.csv` loads the collection and reports how many rows resolved, were ambiguous, or went unmatched
- [x] `pricewatch run` fetches prices for resolved cards and persists them
- [x] A second run continues from where the first stopped rather than starting over
- [x] Changes are reported against each card's previous observation
- [x] Cards are selected stalest first
- [x] An invalid card ID is logged and the run completes
- [x] The published rate limit is never exceeded
- [x] Ctrl-C mid-run exits cleanly with in-flight work either finished or explicitly abandoned
- [x] Table-driven tests covering the diff logic and at least one error path
- [x] README includes the Go versus C# section

## 10. Phases

**Phase 1, v1.** FR-1 through FR-8. Complete and useful on its own.

**Phase 2, v1.1.** FR-9, FR-10a, FR-14. Retry with backoff, value-weighted priority, and
unattended scheduled runs through the OS scheduler (DD-9). A second price source (FR-10) is
no longer part of this phase; it is a future idea (section 13, idea 5).

**Phase 3, v2.** FR-11 through FR-13. RabbitMQ job dispatch with dead-lettering, event fan-out
to independent consumers, Discord notification. This is the phase that addresses the
messaging gap. The phase 2 scheduler now triggers the producer, and the consumer runs
continuously (DD-9).

**Phase 4, v3 (future, not committed).** FR-15. A local, read-only dashboard served by
`pricewatch serve` (DD-10). It opens section 4 only as far as DD-10 states, and starts only
after phase 3, so it can consume the price events.

Each phase leaves something complete.

## 11. Open questions

### Resolved

- **Which price source.** pokemontcg.io is deprecated: new registrations closed, existing keys
  work only through 2027-03-01. **PokeWallet is the primary source.** TCGdex is the best
  candidate for a second source, which is now a future idea (section 13, idea 5); see "which
  source handles variants best" below. pokemontcg.io is not planned as a source, because its
  keys stop working in 2027.
- **Where the card list comes from.** A TCG Collector collection export.
- **Collection scale.** Roughly 8,000 to 9,000 rows.
- **Which source handles variants best.** Both separate variants. They resell the same
  TCGplayer data and matched to the cent on every card tested (2026-09-18, live requests):
  Ruby & Sapphire Mudkip 59/109 Normal $8.22 and Reverse Holo $50.47, Neo Genesis Lugia 1st
  Edition Holo $1,079.79 and Unlimited Holo $518.99, Neo Genesis Sunflora 1st Edition and
  Unlimited, and Twilight Masquerade Tangela Normal and Reverse Holo. **PokeWallet is the
  primary source.**
  - It prices Base Set 1st Edition and Shadowless, which TCGdex cannot.
  - Its limits (100 per hour, 1,000 per day) are real, published and enforced, which makes
    it the honest fit for DD-4.
  - TCGdex's per-card `variants` flags proved unreliable, so only the price keys are trusted.
  - TCGdex, with no published limit, is the best candidate for a second source (section 13,
    idea 5).
  - A variant must match exactly one of the source's price sub-types. None or several is
    reported, never guessed (DD-5).
- **Whether to price by condition (v1).** No. v1 records the TCGplayer market price only, and
  the export's condition column is stored but ignored. The real export holds only Mint
  (7,915 rows) and Near Mint (847), so ignoring condition costs little today.
  Condition-aware pricing is the first idea in section 13.

### Still open

- Whether graded pricing ever matters enough to justify Scrydex at $29/month.
- **When the source's daily quota resets.** The quota table counts per UTC day and resyncs
  from the source's headers, but PokeWallet does not document its reset time. Scheduled runs
  (DD-9) make best use of each day if they start just after it.
- **How missed scheduled runs behave.** Runs are durable slices, so skipping a missed run
  loses only freshness. Decide in phase 2 whether the task catches up or skips.

## 12. Risks

| Risk | Mitigation |
|---|---|
| Free API changes or disappears again | DD-1. The interface exists for exactly this |
| Scope creep into a web UI or a product | Section 4. Non-goals are explicit. The only planned UI is phase 4's local, read-only dashboard, bounded by DD-10 and not started before phase 3 |
| Overlapping scheduled runs price the same cards twice | DD-9. Runs refuse to start while another is open, and the scheduled task disallows parallel instances |
| Bursting overspends the hourly allowance, for example after a restart | DD-11. The server's own hourly count is the source of truth, requests are reserved before sending, and a 429 still stops dispatch cleanly |
| Phase 3 never happens | DD-2 keeps the cost of phase 3 low, and phase 1 stands on its own |
| Time lost to setup rather than Go | Minimal dependencies, pure-Go SQLite, no Docker in v1 |
| Variant mismatches produce silently wrong prices | DD-5. Treat ambiguous matches as failures, not guesses |
| Expansion display name to set code has no clean mapping source | Maintain it as data. Seed from the source API's set list, accept manual entries for the long tail |

## 13. Ideas for future extensions

Candidates, not commitments. None belongs to a phase until it is promoted into a numbered
FR, and one that changes a non-goal or a settled decision needs a DD of its own, as DD-10
did for the dashboard.

1. **Condition-aware pricing.** Value each row at a price for its own condition rather than
   the single market price v1 records. Things to settle first:
   - which source offers prices per condition (TCGplayer's market price does not
     distinguish);
   - how TCG Collector's condition labels map onto that source's grades;
   - how condition interacts with change detection. A card's history must stay comparable
     if its recorded condition changes between imports.

   The export already carries the condition, so no new data is needed on the collection side.

2. **Special-print variants.** About 800 export rows (9%) are excluded in phase 1 and reported
   as `unsupported variant`:
   - ball-pattern reverse holos: Poké Ball (219), Master Ball (46), plus Friend, Quick, Love,
     Dusk and Rocket;
   - Energy Reverse Holo (106);
   - Cosmos Holo, including Prize Pack printings;
   - Prerelease, stamped and promo variants.

   TCGplayer, and so PokeWallet, probably lists the ball-pattern prints as separate products
   (a separately named card) rather than as price sub-types of the base card. If so,
   supporting them means teaching the resolver to find those products by name, not adding
   rows to the variant table. Confirm how the source names a few of them before designing
   anything. Each supported print must still match exactly one price, or be reported (DD-5).

3. **Cross-provider mapping by TCGplayer product ID.** Both sources expose TCGplayer's
   product ID:
   - PokeWallet includes a `tcgplayer.url` ending in `/product/<id>`, even in set listings;
   - TCGdex's `variants_detailed` carries `thirdParty.tcgplayer`.

   Once the first provider has resolved a card, a second provider could be mapped by product
   ID instead of re-matching expansion, number and name, which makes switching or adding a
   source cheaper and more reliable. It would sit on top of the resolver, not replace it.
   Cards without a product ID, or whose IDs disagree, still go through normal resolution and
   are reported when they fail.

4. **A price sanity bound.** Some source prices look like caps or placeholders rather than
   market values. 1st Edition Shadowless Charizard reports exactly $10,000. A bound would
   flag observations like that for review instead of reporting them as real moves.
   Candidates: round-number ceilings, a market price outside the observation's own low and
   high, or a jump beyond a threshold. Flagged prices would still be stored, never silently
   dropped or corrected, so no data is lost and the decision stays visible.

5. **A second price source (formerly FR-10, moved out of phase 2 on 2026-09-19).** TCGdex is
   the natural candidate: free, no key, no published limit, and it separates variants
   through its `pricing.tcgplayer` keys (verified live on 2026-09-18).
   - **Why it might be worth it:** it may sell the plain print of the 356 cards for which
     PokeWallet lists only a ball-pattern product, and it could cross-check PokeWallet's
     prices.
   - **Known limits:** it cannot price Base Set 1st Edition or Shadowless, and its per-card
     `variants` flags are unreliable, so only the price keys may be trusted.
   - **What is already in place:** the provider seam (DD-1). Adding a source is one package
     under `internal/source/` implementing `Sets`, `Cards` and `Quote`, plus one registry
     line. The phase 1 plan's appendix, "adding a provider", walks through TCGdex. `card_map`
     keeps each source's mappings separately, and observations are keyed by collection key,
     so price history stays continuous across sources.
   - **What it would need:** its own overrides file, because set IDs differ between sources,
     and a re-import with `--source tcgdex`. Idea 3 (mapping by TCGplayer product ID) would
     make that re-import cheaper and more reliable.

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
