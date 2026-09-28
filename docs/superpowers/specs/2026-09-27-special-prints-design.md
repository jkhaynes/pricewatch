# Special prints: ball-pattern, Energy and Rocket reverse holos

**Date:** 2026-09-27
**Status:** approved; implemented on feat/special-prints
**PRD:** §13 idea 2 (delivered for these prints), FR-17 and DD-16 (added with this spec)

## Goal

Price the 538 export rows whose variant is a pattern reverse holo, which phase 1
excludes as `unsupported variant`:

| Collection label | Rows | Expansions |
|---|---:|---|
| Poké Ball Reverse Holo | 219 | Prismatic Evolutions, Black Bolt, White Flare, Ascended Heroes |
| Master Ball Reverse Holo | 46 | Prismatic Evolutions, Black Bolt, White Flare |
| Friend Ball Reverse Holo | 21 | Ascended Heroes |
| Quick Ball Reverse Holo | 20 | Ascended Heroes |
| Love Ball Reverse Holo | 19 | Ascended Heroes |
| Dusk Ball Reverse Holo | 17 | Ascended Heroes |
| Energy Reverse Holo | 106 | Ascended Heroes |
| Rocket Reverse Holo | 8 | Ascended Heroes |

Each must match exactly one PokéWallet product and one price, or be reported (DD-5).

## Scope

- The eight labels above only. Cosmos, Prize Pack, stamps, promos and staff prints stay
  excluded.
- PokéWallet only. The design keeps source wording in the provider, so a second provider
  supplies its own table.
- No schema change and no change to `pipeline`, `store`, `ask` or `site` logic.

## What the source does (probe, 2026-09-27)

A pattern print is a **separate product** at the same set and number as the plain card,
not a price sub-type of it. Each pattern product carries exactly one TCGplayer sub-type.

| Set | Products at one number | Pattern price sub-type |
|---|---|---|
| SV: Prismatic Evolutions | `Exeggcute`, `Exeggcute (Poke Ball Pattern)`, `Exeggcute (Master Ball Pattern)` | `Holofoil` ($0.32, $1.33) |
| SV: Black Bolt, White Flare | `Pansage`, `Pansage (Poke Ball Pattern)`, `Pansage (Master Ball Pattern)` | `Holofoil` ($0.26) |
| ME: Ascended Heroes | `Chikorita`, `Chikorita (Friend Ball)`, `Chikorita (Energy Symbol Pattern)` | `Reverse Holofoil` ($0.24) |

Two naming eras: SV sets say `(Poke Ball Pattern)`, ME sets say `(Poke Ball)`,
`(Friend Ball)`, `(Team Rocket)`. ME names can also put the number suffix before the
qualifier: `Erika's Tangela - 007/217 (Poke Ball)`.

The same probe found that the 356 plain rows reported on 2026-09-19 as "only a ball-pattern
print listed at this number" were stale: PokéWallet's listings now include the plain card
at every number, and a cloud re-import resolved all 356. Listings are complete and can be
relied on.

## Design

### `internal/card`: the shared vocabulary

```go
type Print string

const (
	PrintStandard   Print = ""
	PrintPokeBall   Print = "pokeball"
	PrintMasterBall Print = "masterball"
	PrintFriendBall Print = "friendball"
	PrintQuickBall  Print = "quickball"
	PrintLoveBall   Print = "loveball"
	PrintDuskBall   Print = "duskball"
	PrintRocket     Print = "rocket"
	PrintEnergy     Print = "energy"
)

const VariantPattern Variant = "pattern"
```

- `VariantPattern` means "the pattern product's own foil, whichever sub-type the source
  files it under".
- `ParseVariant(label string) (Variant, Print, error)`. The label table gains the eight
  rows above, each mapping to `VariantPattern` and its `Print`. Existing labels return
  `PrintStandard`. Unknown labels still return `ErrUnsupportedVariant`.
- `SourceCard` gains `Print Print`. The zero value is `PrintStandard`, so providers that
  know nothing of patterns are unaffected.

### `internal/source/pokewallet`: the source's wording

- A `patterns` map, qualifier (lower-cased) to `Print`, covering both eras:

  | Qualifier | Print |
  |---|---|
  | `poke ball pattern`, `poke ball` | `PrintPokeBall` |
  | `master ball pattern` | `PrintMasterBall` |
  | `friend ball` | `PrintFriendBall` |
  | `quick ball` | `PrintQuickBall` |
  | `love ball` | `PrintLoveBall` |
  | `dusk ball` | `PrintDuskBall` |
  | `team rocket` | `PrintRocket` |
  | `energy symbol pattern` | `PrintEnergy` |

  `dusk ball` and `team rocket` are inferred, not yet seen for a card in the collection:
  the probe read only the first 100 Ascended Heroes products. The implementation plan
  confirms both against the full listing before relying on them.

  It is an allow-list, like `ownPrint`: an unlisted qualifier stays a different,
  unmatchable print until added.
- In `Cards`: if the name's final parenthesised qualifier is in `patterns`, set `Print`
  and strip it. The existing cleanup (trim ` - <number>`, then `aliases`) runs on the
  remaining base name.
- `Name` stays the raw product name, for reporting. For a pattern product the cleaned base
  name is added to `Aliases`, so the resolver matches it by alias.
- `subtypes[VariantPattern] = {"Holofoil", "Reverse Holofoil"}`. `source.Pick` already
  returns `ErrVariantAmbiguous` when both are present and `ErrVariantUnavailable` when
  neither is.

### `internal/resolve`: one filter

When collecting candidates at the row's number, skip any card whose `Print` differs from
the row's. Name and alias matching run unchanged on what is left.

This protects both directions: a plain row can never land on a pattern product, and a
Poké Ball row can never land on the plain card or on the Master Ball product.

A second, unfiltered list of names at the number is kept for the failure reason (below).

### Flow for one row

`Pansage 004/086 "Master Ball Reverse Holo"`
→ `ParseVariant` gives `(pattern, masterball)`
→ Black Bolt #004 candidates filtered to `Print == masterball`
→ `Pansage (Master Ball Pattern)` matched by its alias `Pansage`
→ `card_map`: that product's ID, variant `pattern`
→ `run` quotes it; `Pick` finds `Holofoil`.

Everything after resolution is keyed on `source_card_id` + `variant`, and display uses the
raw export label, so downstream code needs no change.

## Error handling

Every failure is reported, never guessed (DD-5).

| Case | Result |
|---|---|
| Number exists, no product with the row's print | `unmatched`, reason `no <print> print at set <id> #<n>; has [<every name at the number>]`. Without this the filter would surface as a misleading `number not in set`. |
| Number not in set at all | `unmatched`, `number <n> not in set <id>`, as today. |
| Two products with the same print, number and base name | `ambiguous`, as today. |
| Qualifier not in `patterns` (`(Black Dot Error)`, a future `(Great Ball)`) | Standard print, no alias, never matches; stays reported. |
| Pattern plus own-print qualifier, `X (Full Art) (Poke Ball)` | Pattern stripped first; `aliases` handles `(Full Art)`. |
| Pattern product has both `Holofoil` and `Reverse Holofoil` | Quote error `ErrVariantAmbiguous`; the run logs, records and continues. |
| Pattern product has no TCGplayer price | Quote error `ErrVariantUnavailable`, as today. |

## Existing data and budget

- The 538 rows are `unmatched` in `card_map`, and `import` retries every non-resolved row,
  so the next cloud import picks them up. No migration.
- Resolved plain rows are not re-resolved and need not be: they already point at plain
  products.
- About 538 new product IDs join the rotation, roughly half a day of the 1,000-request
  daily budget per full pass.

## Copy and docs

- `internal/site/sections.go`: the "Special prints not priced yet" description drops
  "Poké Ball and Energy reverse holos".
- PRD: add FR-17 (price pattern reverse holos) and DD-16 (a print is a separate source
  product, identified by the provider's qualifier table and matched by `Print`); mark
  §13 idea 2 as delivered for these prints.

## Testing

TDD, table-driven, in dependency order.

1. **`card.ParseVariant`:** each of the eight labels gives `(VariantPattern, Print…)`;
   existing labels give `PrintStandard`; `Cosmos Holo` still gives `ErrUnsupportedVariant`.
2. **`pokewallet.Cards`** (map-backed fake `Getter`): real names from the probe:
   `Exeggcute`, `Exeggcute (Poke Ball Pattern)`, `Pansage (Master Ball Pattern)`,
   `Chikorita (Friend Ball)`, `Erika's Tangela - 007/217 (Poke Ball)`,
   `Chikorita (Energy Symbol Pattern)`, a `(Team Rocket)` product, `Foo (Black Dot Error)`.
   Assert `Print`, `Name`, `Aliases`.
3. **`pokewallet.Quote`** for `VariantPattern`: `Holofoil` only, `Reverse Holofoil` only,
   both (ambiguous), neither (unavailable).
4. **`resolve.Resolve`** against a fake catalog with Black Bolt #004 holding all three
   products: Normal and Reverse Holo reach the plain product; Poké Ball and Master Ball
   reach their own. Isolation: a plain row at a number with only pattern products fails;
   a Poké Ball row at a number with only the plain card fails with the `no pokeball print`
   reason listing every name. Two same-print products give `ambiguous`.
5. **End to end** (`cmd/pricewatch/e2e_test.go`): a Poké Ball row beside its plain sibling
   resolves to a different product ID, and both get an observation.

## Live verification

After merge: dispatch a cloud `import` and confirm about 538 rows move from
`unsupported variant` to `resolved`. In the next hourly run, spot-check against the probe:
Exeggcute Poké Ball about $0.32, Master Ball about $1.33, Chikorita Friend Ball about $0.24.
