# MCP server: ask questions about the collection

**Date:** 2026-09-26
**Status:** approved in brainstorming, pending spec review
**PRD:** FR-16, DD-15 (added with this spec)

## Goal

A local MCP server, `pricewatch mcp`, that lets Claude answer plain-English questions
about the collection: what it is worth, what moved, what a given card is, how the
pipeline is doing, and anything ad hoc.

## Scope

- Local stdio server only, started by the MCP client (Claude Code or Claude Desktop).
  No network listener, no HTTP transport, no other users.
- Read-only. It never writes to the collection database.
- Tools only. No MCP resources, prompts or sampling.

## Architecture

```
cmd/pricewatch/mcp.go   "pricewatch mcp" subcommand: flags, wiring, server.Run over stdio
internal/ask/           tool handlers, one typed In/Out struct pair per tool
internal/dbsync/        GitHub download, TTL cache, RWMutex hot swap
internal/store/         + OpenReadOnly, + Query (raw read-only SQL for the query tool)
internal/site/          small export so movers can be computed for any window
```

**Protocol:** the official SDK, `github.com/modelcontextprotocol/go-sdk` (v1.8.0 at the
time of writing; confirm the API against that version during planning). Tools are
registered with the generic `mcp.AddTool`, which derives each tool's JSON input schema
from its `In` struct. No `Tool` interface of our own: the SDK's generic registration
already does that job.

Our package is `ask`, not `mcp`, so it does not collide with the SDK's package name.

**Stdout is the protocol.** Every log line goes to stderr through `slog`. A stray write
to stdout corrupts the stream.

## Data: dbsync

The source of truth is `pricewatch.db` on the `db` branch of the private
`jkhaynes/pricewatch-data` repo (DD-13). The server keeps a local cached copy.

- **Cache location:** `os.UserCacheDir()/pricewatch/pricewatch.db`.
- **Download:** `GET /repos/{owner}/{repo}/contents/pricewatch.db?ref=db` with
  `Accept: application/vnd.github.raw` and `Authorization: Bearer $PRICEWATCH_GITHUB_TOKEN`
  (fine-grained, read-only Contents on `pricewatch-data`). Stdlib `net/http`, with a
  `context` timeout. The body streams to a temp file in the cache dir, then `os.Rename`
  replaces the old copy.
  - *Ceiling:* the contents API serves raw files up to 100 MB. The database is far
    below that. If it ever grows past it, switch to the git blobs API.
- **Freshness:** the cached file's mtime is its timestamp. It is fresh while younger than
  `--ttl` (default `1h`, matching the hourly job).
- **When it checks:** at startup, and at the start of any tool call when the copy is
  stale. After a failed download the server does not retry until another TTL has passed,
  so an offline session does not pay a timeout on every call.
- **Hot swap:** the open `*store.SQLite` sits behind a `sync.RWMutex`. Tool calls hold
  the read lock for the duration of their queries. A refresh downloads outside the lock,
  then takes the write lock only to close the old store, rename the file and reopen.
- **Failure:**
  - Download fails, cached copy exists: serve the cache, and every result carries a
    `stale` warning with the reason.
  - Download fails, no cached copy: the tool call returns an error result that says
    why (for example, missing token). The server keeps running.
  - `--db <path>` given: use that file, never download. For tests and offline use.

## Store changes

- **`OpenReadOnly(ctx, path)`:** DSN with `mode=ro` and `_pragma=query_only(1)`. It
  skips the schema and `addColumn` migrations, which are writes. SQLite, not string
  inspection, is what makes the `query` tool safe.
- **`Query(ctx, sql string, maxRows int) (columns []string, rows [][]any, truncated bool, err error)`**
  for the `query` tool.

## Tools

Every result includes `data_as_of` (the latest `runs.finished_at`), plus `stale` when
dbsync fell back to the cache.

| Tool | Inputs | Returns |
|---|---|---|
| `collection_value` | `expansion?` | total value, priced and unpriced card counts, per-expansion breakdown |
| `top_cards` | `limit` (default 20, max 100), `expansion?` | most valuable cards: name, number, variant, quantity, unit price, line value, `collection_key` |
| `find_cards` | `name`, `number?`, `expansion?` | every matching collection key, one row per variant, never merged: mapping status, latest price, last checked, `collection_key` |
| `price_history` | `collection_key` | every observation for that one key, oldest first |
| `movers` | `window` (default `7d`) | biggest risers and fallers over the window |
| `pipeline_status` | none | resolved, ambiguous and unmatched counts; unresolved grouped by reason; today's quota; recent runs |
| `query` | `sql` | columns and rows as JSON, at most 500 rows (`truncated` flag), 10 s timeout |

### Domain rules the tools enforce

1. **Value** is each collection key's latest market price times its quantity. Keys with
   no price are counted and reported as unpriced, never silently valued at $0.
2. **Variants are never merged.** `find_cards` returns one row per variant.
   `price_history` accepts only a `collection_key`, so a history is always for one exact
   variant. When a name matches several variants, the model sees all of them and must
   pick one or ask. This is DD-5 applied to the tools: ambiguity is reported, not guessed.
3. **Movers windows.** DD-12 checks cheap cards weekly, so a window shorter than 7 days
   only sees the cards checked inside it. The tool's description says so. Movers reuse the
   `site` package's logic: `site` gains an exported function that returns every mover for
   a given window, sorted by magnitude, and `site.Movers` calls it with its fixed 7 days.
4. **The `query` description** carries the schema and the gotchas: take each key's latest
   observation, multiply by `quantity`, join `card_map` on `source`, and never aggregate
   prices across variants.

`find_cards` matches `name` case-insensitively as a substring, and `number` exactly
(`"59/109"`).

## Configuration

```
pricewatch mcp [--db path] [--repo jkhaynes/pricewatch-data] [--branch db] [--ttl 1h] [--source pokewallet]
env: PRICEWATCH_GITHUB_TOKEN
```

Registered once:

```
claude mcp add pricewatch -e PRICEWATCH_GITHUB_TOKEN=... -- pricewatch mcp
```

## Error handling

- Tool failures come back as MCP tool errors (`IsError` results) with a readable
  message, so the model can explain them. They never crash the server.
- Errors are wrapped with `%w` through dbsync, store and ask.
- Context cancellation from the client is honoured by every query and by the download.
- A bad `query` (syntax error, write attempt) returns SQLite's error text as the tool
  error.

## Testing

TDD throughout, table-driven where there is more than one case.

- **store:** `OpenReadOnly` rejects `INSERT`, `UPDATE`, `DELETE` and `DROP`. `Query` caps
  rows and reports `truncated`.
- **dbsync:** `httptest.Server` stands in for GitHub. Cases: fresh cache means no request;
  stale cache means download and swap; download failure with a cache means a stale warning;
  failure without a cache means an error; the auth header and `ref` are sent; no retry
  within a TTL after a failure. Time is injected, not read from `time.Now`.
- **ask:** each handler runs against a fixture database built with the existing store
  write methods. Value multiplies by quantity and counts unpriced cards; `find_cards`
  returns Normal and Reverse Holo as separate rows; movers respects the window.
- **end to end:** the SDK's in-memory transport connects a client to the server, then
  `tools/list` returns all seven tools and one `tools/call` round-trips.

## Not in scope

- HTTP or SSE transport, MCP resources, prompts.
- Writing to the database, triggering runs or imports.
- ETag or conditional downloads. Add if the database grows large enough for the hourly
  download to matter.
