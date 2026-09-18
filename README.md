# pricewatch

A Go CLI that tracks Pokémon TCG card prices for a collection exported from
[TCG Collector](https://tcgcollector.com), stores price history, and reports what moved.

## Status

Pre-implementation. Design is in [`docs/PRD.md`](docs/PRD.md).

## Why this exists

Primarily to learn Go. The product is real and should work, but the design
favours whichever option exercises more Go. See PRD section 2.

## Getting started

```
go mod init github.com/jkhaynes/pricewatch
go mod tidy
```

## Sanctioned dependencies

Standard library first. Only two third-party packages are pre-approved:

- `golang.org/x/time/rate` for rate limiting
- `modernc.org/sqlite` for storage (pure Go, no cgo, which matters on Windows)

Anything else needs a reason.

## Data

The TCG Collector export is personal data and is gitignored. Keep it in `data/`.
