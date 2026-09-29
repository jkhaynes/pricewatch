# Stamped prints: Prerelease, Staff, World Championships, 30th Anniversary

**Date:** 2026-09-29
**Status:** approved; implemented on feat/stamped-prints
**PRD:** extends FR-17 and DD-16; adds DD-17 (the promo-set rule)

## Goal

Price the stamped promo prints the export labels `Prerelease`, `Prerelease (Staff)`,
`World Championships`, `World Championships (Staff)` and `30th Anniversary`, which are
reported today as `unsupported variant`. Also fix the number-suffix cleanup that leaves
Mega Evolution Promos names such as `Ampharos - 075` unmatched.

Expected: about 94 rows move to resolved (33 labelled, 44 by the promo-set rule, 17 by the
suffix fix). Destined Rivals' 8 prerelease rows (not listed at PokéWallet) and 2 World
Championships rows with no single match stay reported.

## What the source does (probe, 2026-09-29)

Listings of ME Promos (24451), SV Promos (22872) and Destined Rivals (24269):

| Example print | Products at the number |
|---|---|
| Ceruledge #014 (ME Promos) | `Ceruledge (Prerelease)`, `Ceruledge (Prerelease) [Staff]` |
| Paradise Resort #150 (SV Promos) | `Paradise Resort - 150 (World Championships 2024)`, `… (World Championships 2024) [Staff]` |
| Ampharos #075 (ME Promos) | `Ampharos - 075`, `Ampharos - 075 [Staff]` |
| Alakazam #003 (ME Promos) | `Alakazam - 003`, `Alakazam - 003 (Staff)` |
| Bulbasaur #037 (ME Promos) | `Bulbasaur - 037` only |
| Sylveon ex #100 (ME Promos) | `Sylveon ex - 100 (30th Celebration)` |
| Team Rocket's Mimikyu #087 (Destined Rivals) | only the plain set card; no prerelease product anywhere probed |

- Each stamped product carries one price: mostly `Holofoil`, `Normal` for World
  Championships, `Reverse Holofoil` for a few staff prints. Serperior #064 carries both
  `Holofoil` and `Normal`.
- The staff marker is a square-bracket suffix `[Staff]`, stacked after another qualifier,
  or sometimes `(Staff)`.
- World Championships qualifiers carry a year, once misspelt `World Championship 2025`.
- ME Promos numbers are unpadded (`75`) while names pad them (`Ampharos - 075`,
  `Quaxly -  063` with two spaces); `Mimikyu -160/091` has no space after the dash.

## Design

### `card`

- New prints: `PrintPrerelease "prerelease"`, `PrintPrereleaseStaff "prerelease-staff"`,
  `PrintWorlds "worlds"`, `PrintWorldsStaff "worlds-staff"`, `PrintAnniversary "30th"`.
- New variant `VariantStamped Variant = "stamped"`: the product's only price.
- Labels: `prerelease`, `prerelease (staff)`, `world championships`,
  `world championships (staff)`, `30th anniversary`, each to `VariantStamped` and its print.
- `VariantPattern` is unchanged, so the 452 stored `pattern` mappings keep their meaning.

### `pokewallet`

- `subtypes[VariantStamped] = {"Normal", "Holofoil", "Reverse Holofoil"}`; `Pick` reports
  more than one as ambiguous.
- `printOf(name)` replaces `patternOf`:
  1. A trailing `[Staff]` (any case) is removed and marks staff. A last qualifier
     `(Staff)` does the same.
  2. The last `(…)` qualifier then decides: `prerelease` → Prerelease, or Prerelease
     Staff; `world championships? <year>` → Worlds, or Worlds Staff; `30th celebration` →
     Anniversary; a `patterns` entry → that pattern.
  3. Staff with none of those → Prerelease Staff (`Ampharos - 075 [Staff]`).
  4. Staff stacked with a pattern or 30th, or anything unrecognised → standard with the
     raw name, which never matches.
- Other brackets (`[Professor Oak]`) are not staff markers and are left alone.
- Number suffix: a trailing ` - <number>` is removed when its number equals the card's
  own, compared on the part before any `/`, ignoring leading zeros and case, with any
  whitespace around the dash. Used for standard and stamped names alike.

### `resolve`: the promo-set rule (DD-17)

When no product at the number carries the row's print, and the row's print is Prerelease
or Anniversary, and the row's expansion ends in `Promos`, the resolver matches again
against the standard products at the number. Name and alias matching are unchanged: one
match resolves (variant `stamped`), several are ambiguous, none is a name mismatch.

Why it is safe there and only there: in a promo set each number is its own release, so a
prerelease-only promo such as Ampharos #075 is listed unlabelled. In a main set the
unlabelled product is the ordinary card: Destined Rivals #087 must still report
`no prerelease print`. Staff and World Championships never fall back; they are always
labelled.

## Error handling

| Case | Result |
|---|---|
| Labelled product exists | resolved to it |
| Promo set, nothing labelled, one plain match | resolved to the plain product (DD-17) |
| Main set, nothing labelled | `no prerelease print at set … #…; has [...]` |
| Staff or Worlds with nothing labelled | `no prerelease-staff print …` / `no worlds print …` |
| Product with several prices (Serperior #064) | quote error `ErrVariantAmbiguous`, recorded, run continues |

## Testing

- `card.ParseVariant`: the five new labels.
- `pokewallet.Cards`: real names from the probe (table above), plus `Quaxly -  063`,
  `Mimikyu -160/091`, `World Championship 2025`, `X (30th Celebration) [Staff]`,
  `Professor's Research [Professor Oak]`.
- `pokewallet.Quote` for `VariantStamped`: Normal only, Holofoil only, Holofoil and Normal
  (ambiguous).
- `resolve.Resolve`: labelled Ceruledge; Ampharos Prerelease by the promo-set rule and its
  Staff by `[Staff]`; Bulbasaur 30th; Paradise Resort Worlds Staff; Destined Rivals #087
  Prerelease must not fall back; a staff row in a promo set with nothing labelled must not
  fall back.
