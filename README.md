# pricewatch

A Go CLI that tracks Pokémon TCG card prices for a collection exported from
[TCG Collector](https://tcgcollector.com), stores price history, and reports what moved.

## Status

Phases 1 to 3 are complete: pricing (FR-1 to FR-8), value tiers and scheduled runs (FR-10a,
FR-14), and a public status page (FR-15) at
[jkhaynes.github.io/pricewatch-site](https://jkhaynes.github.io/pricewatch-site).
The design is in [`docs/PRD.md`](docs/PRD.md). The build plans are in
[`docs/superpowers/plans/`](docs/superpowers/plans/).

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
2. **`run`** picks the cards that are due and prices them through a bounded worker pool,
   within the source's rate limits. It saves one observation per card and reports every card
   whose price moved since *that card's* previous observation.

Choosing what to check next: a card is due once its value tier's interval has passed (see
[Value tiers](#value-tiers)). Cards never priced come first, then the most overdue, and ties
go to the most valuable card (PRD DD-8, DD-12), so the first pass prices your best cards
first. One request prices every variant of a card, so the budget counts requests, not rows.

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
run 3 (pokewallet): 38 requests, 52 cards: ok 50, failed 2, abandoned 0, deferred 0; 4886 cards not due yet

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
| **not due yet** | priced recently enough for its value tier (see below). Skipped on purpose, so a run can finish with budget to spare |

**Ctrl-C:**
- Press it **once** to stop starting new cards. Cards in flight finish and are saved.
- Press it **twice** to abandon cards in flight. Completed work is still saved.

### Flags

| Flag | Commands | Default | Meaning |
|---|---|---|---|
| `--db` | all | `pricewatch.db` | SQLite database path |
| `--source` | all | `pokewallet` | price source |
| `--expansions` | import | none | CSV of `expansion,set_id` overrides |
| `--budget` | run | `100` | maximum requests (source cards) this run |
| `--no-wait` | run | off | when the hour's allowance is spent, stop and defer the rest instead of waiting (scheduled runs) |
| `--workers` | run | `2` | concurrent workers |
| `--timeout` | import, run | `15s` | per-request timeout |
| `--base-url` | import, run | the source's | override the API base URL |
| `--out` | site | `site` | directory for the generated `index.html` |
| `--daily-limit` | site | `1000` | the source's daily limit, for the budget section |
| `--run-minute` | site | `7` | the schedule's minute past each hour, for the countdown |

### Rate limits

PokéWallet's free plan allows **100 requests per hour and 1,000 per day**. pricewatch paces
itself against PokéWallet's own count of what's left (PRD DD-11):
- **While requests are left this hour, they go out immediately**, at most 2 per second, so
  a 100-request run takes about a minute.
- **Once the hour's allowance is spent, it pauses** until PokéWallet's allowance resets at
  the top of the next hour (plus 30 seconds' margin), and logs that it's waiting. Ctrl-C
  interrupts the pause as usual. With `--no-wait`, it stops instead and defers the rest.
- The daily count is stored in the database and corrected from the response headers, so
  restarts can't overspend it.
- If a command starts while the hour is already used up, PokéWallet's first answer is "too
  many requests". pricewatch reads the hourly count from that answer, waits out the hour and
  carries on, so re-running a command is always safe.
- When the daily limit is reached, the run stops cleanly and the remaining cards are deferred.

### Value tiers

A run prices only the cards that are due. How often a card is due depends on its value: its
latest market price, or the export price until it has one (PRD DD-12).

| Value | Checked every |
|---|---|
| $100+ | 1 day |
| $20 to $100 | 2 days |
| $5 to $20 | 4 days |
| under $5 | 7 days |

Never-priced cards come first, then the most overdue, then the most valuable. With about
4,900 priceable cards that is roughly 915 requests a day, just under the daily 1,000.

## Scheduled runs on GitHub Actions

pricewatch runs every hour on GitHub Actions, so your computer doesn't need to be on (PRD
DD-13). The workflow lives in a separate **private** repo, because the database and the
export are personal data and this repo is public.

One-time setup:

1. Create a private repo, for example `pricewatch-data`.
2. In its **Settings → Secrets and variables → Actions**, add the secret
   `POKEWALLET_API_KEY`.
3. Add the data and the workflow to its `main` branch:

   ```powershell
   git clone https://github.com/<you>/pricewatch-data.git
   cd pricewatch-data
   Copy-Item ..\pricewatch\data\export.csv, ..\pricewatch\data\expansions.csv .
   New-Item -ItemType Directory -Force .github\workflows
   Copy-Item ..\pricewatch\deploy\github-actions\pricewatch.yml .github\workflows\
   git add .
   git commit -m "collection and workflow"
   git push
   ```

4. Seed the `db` branch with your local database, so nothing is resolved twice. Close any
   pricewatch command or database viewer first, so the file is complete:

   ```powershell
   git switch --orphan db
   Copy-Item ..\pricewatch\pricewatch.db .
   git add -f pricewatch.db
   git commit -m "seed database"
   git push -u origin db
   git switch main
   ```

   If you have no local database, dispatch an **import** instead (step 5).

5. Under **Actions → pricewatch → Run workflow**, choose `run` to check it works. From then on
   it runs hourly at seven minutes past.

After that:
- **Reports** are in each run's log, on the Actions tab.
- **New export:** commit the new `export.csv` to `main` and dispatch an `import`.
- **Looking at the data locally:** download `pricewatch.db` from the `db` branch. The cloud
  copy is the source of truth. A local run against a downloaded copy is not merged back.
- GitHub may delay or skip a scheduled run when it is busy. Progress is durable, so a missed
  hour just means the next run has a little more to do.

## Status page

Every scheduled run also rebuilds a public status page (PRD DD-14): price movement, the
biggest movers with card art, how each card is scheduled, the request budget and the cards
pricewatch refuses to guess. It publishes derived numbers only: never the database, the
export, quantities or the collection's total value.

To preview it locally from any copy of the database:

```powershell
./pricewatch.exe site --db pricewatch.db --out site
start site\index.html
```

One-time setup for publishing:

1. Create a **public** repo named `pricewatch-site`, with no files.
2. Create a fine-grained personal access token (GitHub → Settings → Developer settings →
   Fine-grained tokens): repository access **only `pricewatch-site`**, permission
   **Contents: Read and write**, and the longest expiry you're comfortable renewing.
3. In `pricewatch-data`, add it as the Actions secret `SITE_TOKEN`.
4. Copy the updated `deploy/github-actions/pricewatch.yml` over the data repo's
   `.github/workflows/pricewatch.yml`, commit and push.
5. Dispatch a `run`. When it's green, open `pricewatch-site` → **Settings → Pages** and set
   the source to **Deploy from a branch**, `main`, `/ (root)`.

When the token expires, the publish step fails but the database is still saved. Create a
new token and replace the secret.

## What phase 1 does not do

- **Condition:** prices are TCGplayer market prices, and the condition column is ignored.
- **Special prints:** ball-pattern and Energy reverse holos, Cosmos, Prize Pack, stamps and
  promos (about 9% of the author's collection) are reported as `unsupported variant`.
- **Retry with backoff** arrives in phase 4 with the job queue. Until then, a card that fails
  transiently is simply first in line on the next run. A second source (TCGdex) is a future
  idea. See PRD sections 10 and 13.

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

Thoughts on differences between C# and GO coming from my 9 years experience as a C# backend developer.

### Errors as values

I still don’t love how repetitive `if err != nil` gets. That didn’t change by the end of the project. `internal/store/sqlite.go` has about a dozen of them in 160 lines, and most just add a little context and pass the error up:

```go
if err != nil {
    return fmt.Errorf("loading prices: %w", err)
}
```

After a while, you start skimming them—which is exactly what you don’t want to do with error handling.

Still, I get why Go works this way. It makes normal failure paths explicit, and two parts of that really grew on me.

**Failure is part of the signature.** `store.Open(ctx, path)` returns `(*SQLite, error)`, so I immediately know it can fail. It doesn’t tell me which errors it can return, but that’s still more than C# gives me with `Task<SqliteStore> OpenAsync(...)`. Whether that throws, and what it might throw, lives in the docs if I’m lucky or the implementation if I’m not.

**Every caller has to acknowledge the error.** It can handle it, wrap it, or pass it up, but it can’t just move silently through the method. In C#, an exception can travel through several methods before anything catches it. That’s convenient, but it also makes it harder to see where a failure came from and what a piece of code might produce.

Wrapping helps too. Using `fmt.Errorf("...: %w", err)` adds context as the error moves up without losing the original error. `ErrRateLimited` starts in the HTTP client, gets wrapped twice, and the runner can still recognize it with `errors.Is` and stop cleanly.

Because errors are values, they also work well for partial success. One PokéWallet request can return “Normal priced, Reverse Holo unavailable” by storing an `Err` on each variant’s `Quote`. That is still a domain-specific result structure, but it lets me keep the successful data without throwing away the whole response because one part failed.

Would I swap back to exceptions? No. I prefer being able to see errors enter and leave a function, even if the syntax gets repetitive. But I’d happily take a shorter way to write “if this failed, wrap it and return it.” C# already does something similar for repetitive null handling with `?.` and `??`. A little syntax like that would take most of the sting out of `if err != nil`.

### `context.Context` versus `CancellationToken`

At first, `context.Context` looked like `CancellationToken` with extra steps. What won me over was how Go treats cancellation and deadlines as one description of an operation’s lifetime.

The shared HTTP client creates a 15-second context for each request, and everything underneath respects it. C# can do the same with a timed `CancellationTokenSource`, but the deadline itself isn’t part of the token. In Go, it travels with the context and can be inspected by anything receiving it.

Passing `ctx` as the first parameter of every I/O function is noisy, but it quickly became automatic. It doesn’t feel much different from adding `CancellationToken ct = default` to every async C# method, except Go’s convention around passing it along feels much stronger.

Where this really worked for the project was graceful shutdown. Because the command should be safe to stop and resume later, Ctrl-C has two stages:

```go
// First Ctrl-C
close(stopStarting) // Finish current cards, but start no more.

// Second Ctrl-C
hardCancel() // Cancel cards still in progress.
```

The C# version would need the same two signals:

```csharp
// First Ctrl-C
stopStarting.Cancel();

// Second Ctrl-C
hardStop.Cancel();
```

Neither language removes the need for two signals because they have different meanings. What I liked in Go was how naturally they mapped to a channel for controlling the runner and a context for cancelling its I/O.

The most useful part was separating cancelled work from the save that makes resuming possible. SQLite provides the actual resumability by recording completed cards. But after a hard stop, the work context is already cancelled. Reusing it for the database write could cancel the checkpoint too.

`context.WithoutCancel` lets the save ignore that cancellation while keeping the rest of the context. I then give it a new timeout so cleanup cannot run forever:

```go
saveCtx, cancel := context.WithTimeout(
    context.WithoutCancel(workCtx),
    5*time.Second,
)
defer cancel()

err := store.Save(saveCtx, completedCards)
```

In C#, I would create a separate token for the save:

```csharp
using var saveTimeout =
    new CancellationTokenSource(TimeSpan.FromSeconds(5));

await store.SaveAsync(completedCards, saveTimeout.Token);
```

The Go version isn’t dramatically shorter. The benefit is that contexts made me think clearly about the different lifetimes: stop scheduling new work, cancel in-flight work, then give completed results a short independent window to save. The context didn’t create resumability, but it made it harder to accidentally cancel the checkpoint that resumability depends on.

### Goroutines and channels versus async/await and Task

This was the part of Go I was most curious about.

Go has no `async` keyword. A function runs concurrently when you start it with `go`. In C#, making one method asynchronous often means updating every method that calls it. I prefer how little the calling code changes in Go.

The worker pool in `internal/pipeline/pool.go` is similar to combining `Channel<T>` with `Parallel.ForEachAsync`. The pool is not built into Go, but goroutines, channels, and `select` are. One goroutine sends jobs to a channel, workers process them, and results are sent through another channel. Closing the jobs channel tells the workers there is no more work.

I especially liked `select`. It handles several possible events in one place:

```go
select {
case jobs <- job:
case <-stopStarting:
    return
case <-ctx.Done():
    return
}
```

This means “send the job, stop if Ctrl-C was pressed, or stop if the context was cancelled.” The closest C# equivalent I know is `Task.WhenAny`, which takes more code to set up and read.

There are tradeoffs. Closing a channel twice panics, so I used `sync.Once` because multiple paths could trigger the stop. A nil channel blocks forever, although this is useful inside `select` because it disables that case. Starting a goroutine also does not return a `Task`, so results, errors, and completion must be handled with channels or synchronization such as `WaitGroup`.

`testing/synctest` was also useful. It uses fake time, so tests that wait one second per job can still finish in 0.00s. It does not make all concurrent behavior deterministic, but it removes real delays and makes these tests faster and more reliable.

### Implicit interfaces, declared where they are used

This took the longest to feel natural because it is different from how I normally use interfaces in C#.

In C#, I like that a class explicitly says which interfaces it implements. I also like having registrations in a DI container because it gives the application one place to manage dependencies and their lifetimes. In a larger domain, that structure makes relationships easier to find and understand.

Go works differently. The consumer usually declares a small interface containing only the methods it needs. The implementation does not reference it. `*store.SQLite` satisfies both `pipeline.Store` and `source.Quota` without naming either one. The PokéWallet provider also satisfies two interfaces it does not know about.

This worked well because the project is small. The interfaces are usually one to six methods and live next to the code that uses them. Reading a consumer shows exactly what it needs, and there was no reason to create a larger shared interface. C# can also use small, focused interfaces, but Go pushes the code in that direction by default.

The downside is discoverability. Nothing on `SQLite` says that it implements `pipeline.Store`. Go checks the relationship when the type is used as that interface, or I can add an explicit compile time check:

```go
var _ pipeline.Store = (*SQLite)(nil)
```

I also did not need a DI container for this project. Dependencies are passed through constructors, and a map of constructor functions in `providers.go` handles the price sources. The PokéWallet provider depends on a one method HTTP interface, so tests can return canned JSON without a mocking library or HTTP server.

I liked this approach here, but I would not automatically prefer it for a larger application with a more complicated domain. Manual wiring could become harder to manage, and the implicit relationships could make the system harder to navigate. For that kind of application, I still prefer C# interfaces and dependency injection because the contracts and dependency graph are more explicit.

### Struct embedding instead of inheritance

I barely used struct embedding, which reflects how simple the domain is. Go does not support class inheritance, and I did not need it for this project.

The main example is `Observation`, which embeds `Price`. This promotes the fields from `Price`, so I can write `obs.Market` instead of `obs.Price.Market`. However, an `Observation` is not a `Price` and cannot be passed to a function that expects one.

```go
type Observation struct {
    Price
    ObservedAt time.Time
}
```

This is composition with shorter field access. It does not provide the subtype relationship or virtual behavior that inheritance can provide in C#.

Embedding also worked cleanly with the database code:

```go
rows.Scan(&cur.Market, ...)
```

The promoted field can be accessed directly, and scanning a SQL `NULL` into a `*float64` leaves it `nil`. That pointer serves the same purpose as `double?` in C#, but it did not require additional database mapping.

I did not miss inheritance here because the project did not have a domain that needed class hierarchies or shared polymorphic behavior. Some of my C# hierarchies could probably have used composition instead, but that does not mean embedding replaces inheritance in every case. For this project, simple structs, embedding, and interfaces were enough.

### How far the standard library goes

This was one of the strongest parts of Go for me. The project has only two external dependencies: SQLite and a rate limiter. Everything else comes from the standard library:

- CSV parsing with `encoding/csv`
- HTTP clients and test servers with `net/http` and `httptest`
- JSON with `encoding/json`
- SQL with `database/sql`
- Structured logging with `log/slog`
- Embedded files with `//go:embed`
- Report formatting with `text/tabwriter`
- Fake time for concurrency tests with `testing/synctest`

In C#, I likely would have added CsvHelper, Serilog, Moq, Polly, and a time testing package. Go already covered what this project needed.

The tooling is also included. `go test`, `go vet`, `gofmt`, and `go mod` all work without choosing additional packages or installing a test adapter. I did not have to make formatting decisions because `gofmt` made them for me.

There are still gaps. Unicode normalization and accent folding are not included in the standard library. Matching “Pokémon” to “Pokemon” uses a small replacement function in this project. A complete solution would require another dependency, such as `golang.org/x/text`.

Map iteration order is also unspecified, so output can change between runs. I now sort map keys anywhere the order matters:

```go
slices.Sorted(maps.Keys(m))
```

Overall, I spent less time selecting and configuring libraries than I normally would in C#. For a project this size, the standard library covered almost everything I needed.
