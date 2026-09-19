# pricewatch

## What this is

A Go CLI that tracks Pokémon TCG card prices for a collection imported from TCG Collector.

**The primary purpose of this project is hands-on Go practice.** The product is real and
should work, but when a decision is close, choose the option that exercises more Go.

## Who you are working with

A senior .NET/C# engineer, roughly nine years in. Assume strong general engineering ability
and no Go-specific knowledge.

- Explain Go idioms where they differ meaningfully from C#. Do not explain general
  programming concepts.
- When you use a Go pattern that has no direct C# equivalent, say so in one line.
- Do not write code the author has not been given a chance to understand. The point of this
  project is the learning, not the artifact.

## Read the design first

The full design is in `docs/PRD.md`. Read it before planning anything.

Decisions DD-1 through DD-12 are settled. Do not re-open them without a concrete reason
grounded in something discovered during implementation.

The brainstorming phase is already complete. Start from the PRD, not from scratch.

## Hard constraints

**Dependencies.** Standard library first. Two third-party packages are pre-approved:
`golang.org/x/time/rate` and `modernc.org/sqlite`. Anything else requires justification
before it is added. Explicitly not wanted in v1: web frameworks, ORMs, CLI frameworks,
Docker, logging frameworks (use `log/slog`).

**Scope.** Section 4 of the PRD lists non-goals. They are real. No web UI, no HTTP API,
no multi-user support, no deployment concerns. The single exception is phase 4's local,
read-only dashboard, bounded by DD-10 and not started before phase 3. It is not a licence
for any UI earlier.

**Phases.** Deliver phase 1 (FR-1 through FR-8) completely before starting phase 2. Each
phase must leave the project in a working, useful state.

## Engineering standards

- **TDD.** Write the failing test first, then the smallest change that passes, then refactor.
  This is not optional and it applies to every unit of work.
- **Table-driven tests.** The idiomatic Go pattern. Use it.
- **Errors are values.** Wrap with `%w`, inspect with `errors.Is` and `errors.As`. Never
  swallow an error silently.
- **`context.Context` is the first parameter** of anything that performs IO, and it is
  honoured, not accepted and ignored.
- **Interfaces are declared in the package that consumes them**, not next to their
  implementations. This is the Go convention and the reverse of the C# habit.
- **A single failing card never aborts a run.** Log it, record it, continue. This mirrors
  a failure mode that is easy to reproduce and expensive to debug.

## Domain gotchas that will bite

**There is no stable card identifier.** The TCG Collector export has no ID column. The join
key must be constructed from `region|expansion|number|variant|language`. Expansion is a
display name such as "EX Ruby & Sapphire", not a set code. Card number is `"59/109"`, not `59`.

**Variant is the hard part, and getting it wrong fails silently.** One card number can appear
as Normal, Reverse Holo, 1st Edition, Jumbo, and World Championship prints. In the author's
own data, Tropius 001/084 is $0.06 as Normal and $0.20 as Reverse Holo. On chase cards the
spread is far larger than any price movement being detected. **An ambiguous variant match is
a failure to be reported, never a guess to be made.**

**Scale changes the shape of the work.** Roughly 8,000 to 9,000 rows against a 1,000
request daily budget means a full pass takes about nine days. A run is a bounded slice of
work, not a sweep. Progress must be durable across runs, and change detection compares
against a card's previous observation rather than against the previous run.

## Things that are not bugs

- The import CSV is personal data and is gitignored. Do not commit it.
- `card_map` intentionally caches resolution results. Resolving is the expensive,
  rate-limited step and should happen once per distinct card.
