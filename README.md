# pricewatch

A Go CLI that tracks Pokémon TCG card prices for a collection exported from
[TCG Collector](https://tcgcollector.com), stores price history, and reports what moved.

## Status

Phase 1 (FR-1 to FR-8) is implemented and under acceptance testing against the real API.
The design is in [`docs/PRD.md`](docs/PRD.md), and the build plan is in
[`docs/superpowers/plans/2026-09-18-phase1.md`](docs/superpowers/plans/2026-09-18-phase1.md).

## Why this exists

Primarily to learn Go. The product is real and should work, but the design
favours whichever option exercises more Go. See PRD section 2.

## How it works

A collection of about 8,800 rows against a free API budget of 1,000 requests a day cannot be
priced in one go, so pricewatch never tries. Each run is a **bounded slice** of work, and
progress lives in SQLite, so the next run carries on where the last one stopped.

1. **`import`** reads the TCG Collector CSV and resolves each row to a card at the price
   source. The export has no card IDs, so the join key is
   `region|expansion|number|variant|language`. Rows that can't be matched, or match more than
   one card, are **reported, never guessed**. A Normal and a Reverse Holo of the same card can
   differ in price by more than any movement being tracked.
2. **`run`** picks the stalest cards and prices them through a bounded worker pool, within the
   source's rate limits. It saves one observation per card and reports every card whose price
   moved since *that card's* previous observation.

Choosing what to check next: cards never priced come first, then the least recently priced.
Ties go to the most valuable card by export price (PRD DD-8), so the first pass prices your
best cards first. One request prices every variant of a card, so the budget counts
requests, not rows.

## Setup

Requires Go 1.27 or later.

```powershell
go build -o pricewatch.exe ./cmd/pricewatch
```

The primary price source is [PokéWallet](https://www.pokewallet.io). Its free key needs no
card. Set it as a user environment variable, then open a new terminal (or restart VS Code) so
it's picked up:

```powershell
[Environment]::SetEnvironmentVariable("POKEWALLET_API_KEY", "pk_...", "User")
```

Put your TCG Collector export at `data/export.csv`. The `data/` folder and all database files
are gitignored, because the collection is personal data.

## Usage

### Import the collection

```powershell
./pricewatch.exe import --expansions data/expansions.csv data/export.csv
```

This prints a summary (resolved, ambiguous, unmatched) and then every unresolved row with its
reason. Re-running is safe and cheap: resolved rows are skipped, and only failures are
retried.

When an expansion name differs between TCG Collector and the source, the report says
`unknown expansion "…" (candidates: <set id> "…")`. Check the suggestion, add a line to the
overrides file, and re-run:

```csv
expansion,set_id
Sun & Moon,1863
BREAKpoint,1701
```

The first import takes a while at PokéWallet's pace, about 2½ hours for 64 expansions. It is
safe to stop with Ctrl-C and resume later.

### Price a slice

```powershell
./pricewatch.exe run --budget 100
```

The output looks like this:

```text
run 3 (pokewallet): 100 requests, 148 cards: ok 146, failed 2, abandoned 0, deferred 0

changed since each card's previous observation:
  BEFORE    NOW  DELTA      %  CARD
   50.47  60.00  +9.53  +18.9  international|ex ruby & sapphire|59/109|reverse holo|english
```

What the counts mean:

| Count | Meaning |
|---|---|
| **ok** | priced and saved |
| **failed** | could not be priced. A variant the source doesn't offer, or a card it no longer knows, is reported and removed from rotation; other errors are logged and retried on a later run |
| **abandoned** | in flight when you pressed Ctrl-C twice |
| **deferred** | not priced this run, and **not a failure**: the budget ran out, you stopped the run, or the source said "limit reached". These cards come first next run |

**Ctrl-C:**
- Press it **once** to stop starting new cards. Cards in flight finish and are saved.
- Press it **twice** to abandon cards in flight. Completed work is still saved.

### Flags

| Flag | Commands | Default | Meaning |
|---|---|---|---|
| `--db` | both | `pricewatch.db` | SQLite database path |
| `--source` | both | `pokewallet` | price source |
| `--expansions` | import | none | CSV of `expansion,set_id` overrides |
| `--budget` | run | `100` | maximum requests (source cards) this run |
| `--workers` | run | `2` | concurrent workers |
| `--timeout` | both | `15s` | per-request timeout |
| `--base-url` | both | the source's | override the API base URL |

### Rate limits

PokéWallet's free plan allows **100 requests per hour and 1,000 per day**.
- pricewatch spaces requests to one every 36 seconds, so a 100-request run takes about an
  hour.
- The daily count is stored in the database and corrected from the response headers, so
  restarts can't overspend it.
- When a limit is reached, the run stops cleanly and the remaining cards are deferred.

## What phase 1 does not do

- **Condition:** prices are TCGplayer market prices, and the condition column is ignored.
- **Special prints:** ball-pattern and Energy reverse holos, Cosmos, Prize Pack, stamps and
  promos (about 9% of the author's collection) are reported as `unsupported variant`.
- **Retries and a second source:** retry with backoff, a second source (TCGdex), value
  weighting and scheduled runs are phase 2. See PRD sections 10 and 13 for the roadmap and
  future ideas.

## Adding a price source

A source is one package under `internal/source/` implementing three methods: `Sets`, `Cards`
and `Quote`. It gets rate limiting, quota, timeouts and 429 handling from the shared
`source.Client`, and registers with one line in `cmd/pricewatch/providers.go`. The build plan's
appendix, "adding a provider", walks through it for TCGdex.

## Development

```powershell
go test ./...
```

- Tests never touch the network, apart from one opt-in live test that spends 5 PokéWallet
  requests:

  ```powershell
  $env:PRICEWATCH_LIVE=1; go test ./internal/source/pokewallet -run TestLive -v; Remove-Item Env:PRICEWATCH_LIVE
  ```

- The worker pool's tests use `testing/synctest`'s fake clock, so they're instant and
  deterministic.
- The race detector (`go test -race`) needs cgo and a C compiler, which this Windows machine
  doesn't have.

### Dependencies

Standard library first. Only two third-party packages are pre-approved:

- `golang.org/x/time/rate` for rate limiting
- `modernc.org/sqlite` for storage (pure Go, no cgo, which matters on Windows)

Anything else needs a reason.

## What surprised me coming from C#

<!--
Written by the author, during the build (PRD Appendix B). The notes under each heading are
prompts drawn from this codebase, not the section's content. Replace them with your own
opinions, then delete the note.
-->

### Errors as values

<!--
Prompts:
- `if err != nil` everywhere. Tiring or clarifying? Compare `internal/store/sqlite.go`
  with the equivalent try/catch.
- Wrapping with `%w` then `errors.Is`: `ErrRateLimited` travels from `source/client.go`,
  through `pokewallet`, into the runner's `switch`, and it stays recognisable at every layer.
- Errors *inside* results: `card.Quote.Err` lets one request say "Normal priced, Reverse
  Holo unavailable". What would that be in C#: a result type, or exceptions per item?
- `errors.As` for typed errors (`*source.StatusError`) versus `catch (X e) when (...)`.
-->

### context.Context versus CancellationToken

<!--
Prompts:
- It is the first parameter everywhere, by convention and by `go vet`. More or less
  intrusive than `CancellationToken ct = default`?
- The deadline travels *inside* the context (`context.WithTimeout` in `source/client.go`),
  so nothing below needs a timeout parameter.
- `context.WithoutCancel` in `pipeline/runner.go`: saving finished work after Ctrl-C. How
  would you express "cancel the work, not the cleanup" with tokens?
- Two stop signals, a soft `stop` channel and a hard `ctx`, for two-stage Ctrl-C.
-->

### Goroutines and channels versus async/await and Task

<!--
Prompts:
- `pipeline/pool.go`: `Feed` and `Work` are `Channel<T>` plus `Parallel.ForEachAsync`, as
  language features.
- `select`: no direct C# equivalent. `Task.WhenAny` is the nearest, and it's clumsier.
- No coloured functions: nothing is `async`, and any function can block. Freeing, or
  unnerving?
- `sync.Once` to close a channel exactly once, because closing twice panics.
- `testing/synctest`: a fake clock makes the pool tests instant and deterministic. Is there
  an equivalent you'd reach for in .NET?
-->

### Implicit interfaces, declared where they are used

<!--
Prompts:
- `pipeline.Store`, `resolve.Catalog`, `source.Quota` and `pokewallet.Getter` are all
  declared by the *consumer*. `*store.SQLite` satisfies two of them without naming either.
- `var _ pipeline.Store = (*SQLite)(nil)`: the compile-time check you need *because* nothing
  says "implements".
- The provider registry in `cmd/pricewatch/providers.go` is a map of functions, with no DI
  container. Did you miss one?
- `pokewallet.Getter` lets the tests pass a map instead of an HTTP server.
-->

### Struct embedding instead of inheritance

<!--
Prompts:
- `card.Observation` embeds `card.Price`, so `obs.Market` works directly, yet an
  Observation is not a Price.
- `rows.Scan(&cur.Market)` reaching through the embedded field.
- Composition that looks like inheritance at the call site. Where did it help, and where
  would you have wanted real inheritance?
-->

### How far the standard library goes

<!--
Prompts:
- CSV (`encoding/csv`), HTTP (`net/http`, `httptest`), JSON, SQL (`database/sql`), logging
  (`log/slog`), embedding files (`//go:embed`), column alignment (`text/tabwriter`), fake
  time (`testing/synctest`): two third-party packages in total.
- Where it fell short: no Unicode normalisation (`golang.org/x/text` isn't approved), hence
  the "é" replacer in `resolve.go`.
- `go test`, `go vet`, `gofmt` and `go mod` come with the toolchain. Compare the .NET
  equivalents (analyzers, formatters, test adapters).
- Go map iteration order is deliberately random, hence `slices.Sorted(maps.Keys(...))`
  wherever output must be stable.
-->
