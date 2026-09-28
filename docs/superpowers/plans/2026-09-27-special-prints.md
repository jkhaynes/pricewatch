# Special Prints Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Price the 538 ball-pattern, Energy and Rocket reverse holo rows that phase 1 reports as `unsupported variant`.

**Architecture:** A pattern print is a separate PokéWallet product at the same number as the plain card. `card` gains a `Print` type. The PokéWallet provider tags each product with its `Print` from its own qualifier table, and the resolver only considers products whose `Print` equals the row's. Pattern products are priced through a new `VariantPattern`, whose candidate sub-types are `Holofoil` and `Reverse Holofoil`; the existing `Pick` reports ambiguity if both are present.

**Tech Stack:** Go standard library only. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-27-special-prints-design.md`

## Global Constraints

- Standard library only; no new dependencies.
- TDD for every task: failing test first, show the red output, smallest change to green, commit.
- Table-driven tests.
- Never guess: every failure is `unmatched` or `ambiguous` with a reason (DD-5).
- Interfaces stay in the consuming package; no new interfaces are needed.
- Branch: `feat/special-prints` (already created; the spec is committed on it).
- Commit messages end with:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01MB2L77AbZgKcuJXsQtZtre
  ```
- Run the whole suite with `go test ./...` from the repo root.

## File map

| File | Change |
|---|---|
| `internal/card/card.go` | `Print` type and constants, `Print.String`, `VariantPattern`, `ParseVariant` returns a `Print`, `SourceCard.Print` |
| `internal/card/card_test.go` | `TestParseVariant` gains the print column and the eight new labels; `TestPrintString` |
| `internal/source/pokewallet/pokewallet.go` | `patterns` table, `patternOf`, `bareName` (refactor of `aliases`), `Cards` sets `Print` and alias, `subtypes[VariantPattern]` |
| `internal/source/pokewallet/pokewallet_test.go` | `TestCardsPatternPrints`, pattern rows moved out of `TestCardsQualifierAliases`, `TestQuotePattern` |
| `internal/resolve/resolve.go` | Print filter and the `no <print> print` reason |
| `internal/resolve/resolve_test.go` | White Flare fixture gets real pattern products; new resolve cases |
| `internal/site/sections.go` | Copy for special prints; new reason group |
| `internal/site/sections_test.go` | New reason in the grouping test |
| `cmd/pricewatch/e2e_test.go` | `TestPatternPrintsArePricedAsTheirOwnProducts` |
| `docs/PRD.md`, `README.md` | FR-17, DD-16, idea 2 note, README exclusions |

---

### Task 1: `card.Print` and `ParseVariant` returning a print

**Files:**
- Modify: `internal/card/card.go`
- Modify: `internal/resolve/resolve.go:75` (call site only)
- Test: `internal/card/card_test.go`

**Interfaces:**
- Produces:
  - `type Print string` with `PrintStandard Print = ""`, `PrintPokeBall = "pokeball"`, `PrintMasterBall = "masterball"`, `PrintFriendBall = "friendball"`, `PrintQuickBall = "quickball"`, `PrintLoveBall = "loveball"`, `PrintDuskBall = "duskball"`, `PrintRocket = "rocket"`, `PrintEnergy = "energy"`
  - `func (p Print) String() string`: `"standard"` for `PrintStandard`, else `string(p)`
  - `const VariantPattern Variant = "pattern"`
  - `func ParseVariant(label string) (Variant, Print, error)`
  - `SourceCard.Print Print` (new field, after `Name`)

- [ ] **Step 1: Write the failing tests**

Replace `TestParseVariant` in `internal/card/card_test.go` and add `TestPrintString`:

```go
func TestParseVariant(t *testing.T) {
	tests := []struct {
		label     string
		want      Variant
		wantPrint Print
		wantErr   error
	}{
		{"Normal", VariantNormal, PrintStandard, nil},
		{"Non-holo", VariantNormal, PrintStandard, nil},
		{"Holo", VariantHolo, PrintStandard, nil},
		{"Normal Holo", VariantHolo, PrintStandard, nil},
		{"Reverse Holo", VariantReverseHolo, PrintStandard, nil},
		{" reverse holo ", VariantReverseHolo, PrintStandard, nil},
		{"1st Edition", VariantFirstEdition, PrintStandard, nil},
		{"1st Edition Holo", VariantFirstEditionHolo, PrintStandard, nil},
		// Pattern reverse holos: a separate product at the source, priced as its own foil.
		{"Poké Ball Reverse Holo", VariantPattern, PrintPokeBall, nil},
		{"Master Ball Reverse Holo", VariantPattern, PrintMasterBall, nil},
		{"Friend Ball Reverse Holo", VariantPattern, PrintFriendBall, nil},
		{"Quick Ball Reverse Holo", VariantPattern, PrintQuickBall, nil},
		{"Love Ball Reverse Holo", VariantPattern, PrintLoveBall, nil},
		{"Dusk Ball Reverse Holo", VariantPattern, PrintDuskBall, nil},
		{"Rocket Reverse Holo", VariantPattern, PrintRocket, nil},
		{"Energy Reverse Holo", VariantPattern, PrintEnergy, nil},
		// Real labels from the export that are still excluded on purpose.
		{"Cosmos Holo", "", PrintStandard, ErrUnsupportedVariant},
		{"Play! Pokémon Prize Pack, Non-holo", "", PrintStandard, ErrUnsupportedVariant},
		{"Jumbo Size", "", PrintStandard, ErrUnsupportedVariant},
		{"", "", PrintStandard, ErrUnsupportedVariant},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got, gotPrint, err := ParseVariant(tt.label)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want || gotPrint != tt.wantPrint {
				t.Errorf("got (%q, %q), want (%q, %q)", got, gotPrint, tt.want, tt.wantPrint)
			}
		})
	}
}

// Print's zero value is the plain card, which reads badly in a reason ("no  print").
func TestPrintString(t *testing.T) {
	tests := []struct {
		p    Print
		want string
	}{
		{PrintStandard, "standard"},
		{PrintPokeBall, "pokeball"},
		{PrintEnergy, "energy"},
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("%q.String() = %q, want %q", string(tt.p), got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/card/`
Expected: build failure: `assignment mismatch: 3 variables but ParseVariant returns 2 values`, and `undefined: PrintStandard` / `VariantPattern`.

- [ ] **Step 3: Implement**

In `internal/card/card.go`, add `VariantPattern` to the `Variant` constants:

```go
	VariantFirstEditionHolo Variant = "1st-edition-holo"
	// VariantPattern is a pattern print's own foil, whichever sub-type the
	// source files it under. Only pattern prints use it.
	VariantPattern Variant = "pattern"
)
```

After the `Variant` constants, add:

```go
// Print says which physical print a row or source product is. Most cards have
// one, PrintStandard, carrying Normal, Holo and Reverse Holo prices. Pattern
// reverse holos (Poké Ball, Energy, ...) are separate products at the source,
// at the same number as the plain card, so they need their own identity.
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

func (p Print) String() string {
	if p == PrintStandard {
		return "standard"
	}
	return string(p)
}
```

Replace `variantLabels` and `ParseVariant`:

```go
type finish struct {
	variant Variant
	print   Print
}

// variantLabels maps normalised TCG Collector labels to what they mean (labels
// from the real export, Task 0 of phase 1). Anything absent is excluded on
// purpose: Cosmos, Prize Pack, stamps and promos.
var variantLabels = map[string]finish{
	"normal":           {VariantNormal, PrintStandard},
	"non-holo":         {VariantNormal, PrintStandard},
	"holo":             {VariantHolo, PrintStandard},
	"normal holo":      {VariantHolo, PrintStandard},
	"reverse holo":     {VariantReverseHolo, PrintStandard},
	"1st edition":      {VariantFirstEdition, PrintStandard},
	"1st edition holo": {VariantFirstEditionHolo, PrintStandard},

	"poké ball reverse holo":   {VariantPattern, PrintPokeBall},
	"master ball reverse holo": {VariantPattern, PrintMasterBall},
	"friend ball reverse holo": {VariantPattern, PrintFriendBall},
	"quick ball reverse holo":  {VariantPattern, PrintQuickBall},
	"love ball reverse holo":   {VariantPattern, PrintLoveBall},
	"dusk ball reverse holo":   {VariantPattern, PrintDuskBall},
	"rocket reverse holo":      {VariantPattern, PrintRocket},
	"energy reverse holo":      {VariantPattern, PrintEnergy},
}

func ParseVariant(label string) (Variant, Print, error) {
	f, ok := variantLabels[Normalize(label)]
	if !ok {
		return "", PrintStandard, fmt.Errorf("%w: %q", ErrUnsupportedVariant, label)
	}
	return f.variant, f.print, nil
}
```

Write `é` in `"poké ball reverse holo"` as the literal character, never as an escape.

Add the field to `SourceCard`, and extend its doc comment:

```go
// ... (existing comment) ...
// Print is the product's print. A pattern product ("Pansear (Poke Ball
// Pattern)") has its pattern's Print and its bare name in Aliases.
type SourceCard struct {
	ID      string
	Number  string
	Name    string
	Print   Print
	Aliases []string
}
```

In `internal/resolve/resolve.go`, keep it compiling (Task 3 uses the print):

```go
	variant, _, err := card.ParseVariant(row.Variant)
```

> **Go note:** Go has no enums. A named string type plus typed constants is the idiom. Unlike a C# enum, `Print("banana")` compiles, so the parse tables are the only place values should come from. `String()` makes `Print` a `fmt.Stringer`, so `%s` prints `standard` instead of an empty string. It's like overriding `ToString()`, but picked up through an implicit interface.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./...`
Expected: all PASS. `resolve` still passes because the print is discarded for now.

- [ ] **Step 5: Commit**

```bash
git add internal/card internal/resolve/resolve.go
git commit -m "card: Print, VariantPattern, and ParseVariant returns the print"
```

---

### Task 2: PokéWallet tags pattern products and prices their foil

**Files:**
- Modify: `internal/source/pokewallet/pokewallet.go`
- Test: `internal/source/pokewallet/pokewallet_test.go`

**Interfaces:**
- Consumes: `card.Print*`, `card.VariantPattern`, `SourceCard.Print` (Task 1)
- Produces: `Cards` returns pattern products with `Print` set, `Name` unchanged (raw), `Aliases` = `[bare name]`; `Quote` answers `VariantPattern` from `Holofoil` or `Reverse Holofoil`.

- [ ] **Step 1: Confirm the unverified qualifiers live**

The probe only saw `(Friend Ball)`, `(Quick Ball)`, `(Love Ball)`, `(Poke Ball)`, `(Team Rocket)` and `(Energy Symbol Pattern)` in the first 100 Ascended Heroes products. It never saw a Dusk Ball product. This step costs 13 requests, so run it when the hour has room. Stop and ask the user if the hour is spent.

```bash
python -c "
import json,os,urllib.request,collections,re
H={'X-API-Key':os.environ['POKEWALLET_API_KEY'],'User-Agent':'curl/8'}
q=collections.Counter()
for p in range(1,14):
  d=json.load(urllib.request.urlopen(urllib.request.Request(f'https://api.pokewallet.io/sets/24541?page={p}&limit=50',headers=H)))
  for c in d['cards']:
    m=re.search(r'\(([^()]+)\)\s*$',c['card_info']['name'])
    if m: q[m.group(1)]+=1
print(q)
"
```

Expected: qualifiers including `Dusk Ball` and `Team Rocket`. If the wording differs (e.g. `Dusk Ball Pattern`), use the observed wording in the `patterns` table in Step 3 and in the test in Step 2. If any qualifier appears that isn't in the table, report it to the user. Do not add it without asking.

- [ ] **Step 2: Write the failing tests**

In `TestCardsQualifierAliases`, **delete** these three rows. Pattern products now get an alias, and the new test below covers them:

```go
		{"Pansear (Poke Ball Pattern)", nil},
		{"Sewaddle (Master Ball Pattern)", nil},
		...
		{"Pikachu (Secret) (Poke Ball Pattern)", nil},
```

Keep `{"Pikachu (Poke Ball Pattern) (Secret)", nil}`: a pattern that is not the last qualifier is not recognised, so the product stays unmatchable.

Add after `TestCardsQualifierAliases`:

```go
// Pattern reverse holos are separate products at the same number as the plain
// card. Names from the live listings (probe, 2026-09-27): SV sets say
// "(Poke Ball Pattern)", ME sets say "(Poke Ball)", and ME names may put the
// number suffix before the qualifier.
func TestCardsPatternPrints(t *testing.T) {
	tests := []struct {
		name, number string
		wantPrint    card.Print
		wantAliases  []string
	}{
		{"Exeggcute", "001/131", card.PrintStandard, nil},
		{"Exeggcute (Poke Ball Pattern)", "001/131", card.PrintPokeBall, []string{"Exeggcute"}},
		{"Exeggcute (Master Ball Pattern)", "001/131", card.PrintMasterBall, []string{"Exeggcute"}},
		{"Pansage (Master Ball Pattern)", "004/086", card.PrintMasterBall, []string{"Pansage"}},
		{"Erika's Oddish (Poke Ball)", "001/217", card.PrintPokeBall, []string{"Erika's Oddish"}},
		{"Erika's Tangela - 007/217 (Poke Ball)", "007/217", card.PrintPokeBall, []string{"Erika's Tangela"}},
		{"Chikorita (Friend Ball)", "008/217", card.PrintFriendBall, []string{"Chikorita"}},
		{"Chikorita (Quick Ball)", "008/217", card.PrintQuickBall, []string{"Chikorita"}},
		{"Chikorita (Love Ball)", "008/217", card.PrintLoveBall, []string{"Chikorita"}},
		{"Chikorita (Dusk Ball)", "008/217", card.PrintDuskBall, []string{"Chikorita"}},
		{"Chikorita (Energy Symbol Pattern)", "008/217", card.PrintEnergy, []string{"Chikorita"}},
		{"Team Rocket's Ekans (Team Rocket)", "050/217", card.PrintRocket, []string{"Team Rocket's Ekans"}},
		// A pattern over an own-print qualifier: both are stripped.
		{"Pikachu (Secret) (Poke Ball Pattern)", "117/168", card.PrintPokeBall, []string{"Pikachu"}},
		// A pattern over a different print: the pattern is known, the base is not
		// this card, so no alias and it can never match.
		{"Charizard (Black Dot Error) (Poke Ball Pattern)", "004/102", card.PrintPokeBall, nil},
		// Not a pattern: an unknown qualifier stays a standard, unmatchable product.
		{"Charizard (Black Dot Error)", "004/102", card.PrintStandard, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"cards":      []any{map[string]any{"id": "pk_x", "card_info": map[string]any{"name": tt.name, "card_number": tt.number}}},
				"pagination": map[string]any{"page": 1, "total_pages": 1},
			})
			cards, err := New(fakeGetter{"/sets/s?page=1&limit=50": string(body)}).Cards(t.Context(), "s")
			if err != nil {
				t.Fatal(err)
			}
			c := cards[0]
			if c.Print != tt.wantPrint || !slices.Equal(c.Aliases, tt.wantAliases) {
				t.Errorf("Print, Aliases = %q, %q; want %q, %q", c.Print, c.Aliases, tt.wantPrint, tt.wantAliases)
			}
			if c.Print != card.PrintStandard && c.Name != tt.name {
				t.Errorf("Name = %q; a pattern product keeps its full name for reports", c.Name)
			}
		})
	}
}

// A pattern product carries one price, under Holofoil in SV sets and Reverse
// Holofoil in ME sets (probe, 2026-09-27). Both at once would be a guess.
func TestQuotePattern(t *testing.T) {
	tests := []struct {
		name, body string
		want       any // float64 market, or error sentinel
	}{
		{"SV pattern: Holofoil", `{"tcgplayer":{"prices":[{"sub_type_name":"Holofoil","market_price":0.32}]}}`, 0.32},
		{"ME pattern: Reverse Holofoil", `{"tcgplayer":{"prices":[{"sub_type_name":"Reverse Holofoil","market_price":0.24}]}}`, 0.24},
		{"both is ambiguous", `{"tcgplayer":{"prices":[
			{"sub_type_name":"Holofoil","market_price":0.3},{"sub_type_name":"Reverse Holofoil","market_price":0.2}]}}`, card.ErrVariantAmbiguous},
		{"neither is unavailable", `{"tcgplayer":{"prices":[{"sub_type_name":"Normal","market_price":0.05}]}}`, card.ErrVariantUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs, err := New(fakeGetter{"/cards/pk_x": tt.body}).Quote(t.Context(), "pk_x", []card.Variant{card.VariantPattern})
			if err != nil {
				t.Fatal(err)
			}
			switch w := tt.want.(type) {
			case float64:
				if qs[0].Err != nil || qs[0].Price.Market == nil || *qs[0].Price.Market != w {
					t.Errorf("quote = %+v, want market %v", qs[0], w)
				}
			case error:
				if !errors.Is(qs[0].Err, w) {
					t.Errorf("err = %v, want %v", qs[0].Err, w)
				}
			}
		})
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/source/pokewallet/ -run "TestCardsPatternPrints|TestQuotePattern" -v`
Expected: FAIL. `Print` is `""` and `Aliases` is `[]` for every pattern row, and `TestQuotePattern` fails with `ErrVariantUnavailable` for the priced cases (`want one of [] ...`).

- [ ] **Step 4: Implement**

In `pokewallet.go`, add to `subtypes`:

```go
	card.VariantPattern:          {"Holofoil", "Reverse Holofoil"}, // one or the other by era (probe, 2026-09-27)
```

After `ownPrint`, add:

```go
// patterns maps PokeWallet's qualifiers for pattern reverse holos to prints,
// in both naming eras seen live (2026-09-27): SV sets say "(Poke Ball
// Pattern)", ME sets "(Poke Ball)". An allow-list like ownPrint: an unknown
// qualifier stays a standard product with no alias, so it never matches.
var patterns = map[string]card.Print{
	"poke ball pattern":     card.PrintPokeBall,
	"poke ball":             card.PrintPokeBall,
	"master ball pattern":   card.PrintMasterBall,
	"friend ball":           card.PrintFriendBall,
	"quick ball":            card.PrintQuickBall,
	"love ball":             card.PrintLoveBall,
	"dusk ball":             card.PrintDuskBall,
	"team rocket":           card.PrintRocket,
	"energy symbol pattern": card.PrintEnergy,
}

// patternOf splits a pattern product's last qualifier off its name:
// "Pansear (Poke Ball Pattern)" is PrintPokeBall with base "Pansear".
// Anything else is PrintStandard with the name unchanged.
func patternOf(name string) (card.Print, string) {
	m := trailingQualifier.FindStringSubmatch(name)
	if m == nil {
		return card.PrintStandard, name
	}
	p, ok := patterns[strings.ToLower(m[2])]
	if !ok {
		return card.PrintStandard, name
	}
	return p, m[1]
}
```

Replace `aliases` with `bareName` plus a thin `aliases`:

```go
// bareName strips trailing qualifiers one at a time, as in
// "Gardevoir & Sylveon GX (205) (Alternate Full Art)". Every one must be
// allowed: a number, the card's own number with its prefix ("(SV66)" at
// SV66/SV94), or an ownPrint qualifier. A single qualifier marking a different
// print means the name is not this card's: ok is false.
func bareName(name, number string) (string, bool) {
	base := name
	for {
		m := trailingQualifier.FindStringSubmatch(base)
		if m == nil {
			return base, true
		}
		q := strings.ToLower(m[2])
		if !digitsOnly.MatchString(q) && !strings.EqualFold(q, number) && !ownPrint[q] {
			return "", false
		}
		base = m[1]
	}
}

// aliases gives a standard product its bare name, when it differs.
func aliases(name, number string) []string {
	if base, ok := bareName(name, number); ok && base != name {
		return []string{base}
	}
	return nil
}
```

In `Cards`, replace the body of the `for _, c := range resp.Cards` loop:

```go
		for _, c := range resp.Cards {
			number := c.CardInfo.CardNumber
			local, _, _ := strings.Cut(number, "/")
			sc := card.SourceCard{ID: c.ID, Number: local}
			if p, base := patternOf(c.CardInfo.Name); p != card.PrintStandard {
				// Keep the full name for reports; match by the bare name.
				sc.Name, sc.Print = c.CardInfo.Name, p
				if bare, ok := bareName(strings.TrimSuffix(base, " - "+number), local); ok {
					sc.Aliases = []string{bare}
				}
			} else {
				sc.Name = strings.TrimSuffix(c.CardInfo.Name, " - "+number)
				sc.Aliases = aliases(sc.Name, local)
			}
			out = append(out, sc)
		}
```

> **Go note:** `if p, base := patternOf(...); p != ...` is an if statement with an initializer. `p` and `base` are scoped to the if/else, the way a C# `is` pattern variable is scoped to its branch. Multiple return values, `(card.Print, string)` here, replace C# `out` parameters or tuples.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/source/pokewallet/ -v -run "TestCards|TestQuote"` then `go test ./...`
Expected: all PASS, including the unchanged rows of `TestCardsQualifierAliases` and `TestCardsQualifierMatchingTheCardsOwnNumber`.

- [ ] **Step 6: Commit**

```bash
git add internal/source/pokewallet
git commit -m "pokewallet: tag pattern products with their print and price their foil"
```

---

### Task 3: The resolver matches only products of the row's print

**Files:**
- Modify: `internal/resolve/resolve.go` (`Resolve`)
- Test: `internal/resolve/resolve_test.go`

**Interfaces:**
- Consumes: `card.ParseVariant` → `(Variant, Print, error)`, `SourceCard.Print`, `Print.String` (Task 1)
- Produces: new reason format `no <print> print at set <id> #<n>; has [<names>]` (Task 4 groups it by the substring `print at set`).

- [ ] **Step 1: Write the failing tests**

In `newFake()`, replace the `"24326"` entry with the shapes the provider now produces:

```go
			// White Flare #014 as PokeWallet lists it: the plain card and two
			// pattern products (live, 2026-09-27). #015 has only a pattern
			// product; #016 only the plain card.
			"24326": {
				{ID: "pk_pansear", Number: "014", Name: "Pansear"},
				{ID: "pk_pb", Number: "014", Name: "Pansear (Poke Ball Pattern)", Print: card.PrintPokeBall, Aliases: []string{"Pansear"}},
				{ID: "pk_mb", Number: "014", Name: "Pansear (Master Ball Pattern)", Print: card.PrintMasterBall, Aliases: []string{"Pansear"}},
				{ID: "pk_simi_pb", Number: "015", Name: "Simisear (Poke Ball Pattern)", Print: card.PrintPokeBall, Aliases: []string{"Simisear"}},
				{ID: "pk_panpour", Number: "016", Name: "Panpour"},
				{ID: "pk_tw1", Number: "017", Name: "Twin (Poke Ball Pattern)", Print: card.PrintPokeBall, Aliases: []string{"Twin"}},
				{ID: "pk_tw2", Number: "017", Name: "Twin (Poke Ball)", Print: card.PrintPokeBall, Aliases: []string{"Twin"}},
			},
```

In `TestResolve`, replace the case `"a pattern print gets no alias"` with:

```go
		// Pattern prints: a separate product at the same number, matched only by print.
		{"plain row takes the plain product", r("Pansear", "White Flare", "014/086", "Normal", "English"), nil,
			card.StatusResolved, "pk_pansear", card.VariantNormal, ""},
		{"reverse holo takes the plain product", r("Pansear", "White Flare", "014/086", "Reverse Holo", "English"), nil,
			card.StatusResolved, "pk_pansear", card.VariantReverseHolo, ""},
		{"poke ball takes its own product", r("Pansear", "White Flare", "014/086", "Poké Ball Reverse Holo", "English"), nil,
			card.StatusResolved, "pk_pb", card.VariantPattern, ""},
		{"master ball takes its own product", r("Pansear", "White Flare", "014/086", "Master Ball Reverse Holo", "English"), nil,
			card.StatusResolved, "pk_mb", card.VariantPattern, ""},
		{"plain row never takes a pattern product", r("Simisear", "White Flare", "015/086", "Normal", "English"), nil,
			card.StatusUnmatched, "", "", `no standard print at set 24326 #015; has ["Simisear (Poke Ball Pattern)"]`},
		{"pattern row never takes the plain product", r("Panpour", "White Flare", "016/086", "Poké Ball Reverse Holo", "English"), nil,
			card.StatusUnmatched, "", "", `no pokeball print at set 24326 #016; has ["Panpour"]`},
		{"missing pattern lists every product at the number", r("Pansear", "White Flare", "014/086", "Energy Reverse Holo", "English"), nil,
			card.StatusUnmatched, "", "", `no energy print at set 24326 #014; has ["Pansear" "Pansear (Poke Ball Pattern)" "Pansear (Master Ball Pattern)"]`},
		{"two products of one print are ambiguous", r("Twin", "White Flare", "017/086", "Poké Ball Reverse Holo", "English"), nil,
			card.StatusAmbiguous, "", "", "match"},
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/resolve/ -run TestResolve -v`
Expected: FAIL. Nothing filters by print yet, so `poke ball takes its own product` resolves to `pk_pansear` (exact name beats aliases), and `plain row never takes a pattern product` resolves to `pk_simi_pb` by alias. The other `no … print` cases fail on the reason substring.

- [ ] **Step 3: Implement**

In `Resolve`, take the print:

```go
	variant, rowPrint, err := card.ParseVariant(row.Variant)
```

Replace the candidate loop and the switch that follows it:

```go
	var atNumber []string // every product at the number, of any print, for reasons
	var byNumber, byName, byAlias []card.SourceCard
	want := normName(row.Name)
	for _, c := range cards {
		if normNumber(c.Number) != normNumber(local) {
			continue
		}
		atNumber = append(atNumber, c.Name)
		// A pattern print is its own product: a plain row must never price one,
		// nor a Poké Ball row the plain card or the Master Ball one.
		if c.Print != rowPrint {
			continue
		}
		byNumber = append(byNumber, c)
		if normName(c.Name) == want {
			byName = append(byName, c)
		}
		if slices.ContainsFunc(c.Aliases, func(a string) bool { return normName(a) == want }) {
			byAlias = append(byAlias, c)
		}
	}
	// Aliases only count when no card at this number matches the name exactly,
	// so "Charizard" still beats "Charizard (Full Art)" at the same number.
	if len(byName) == 0 {
		byName = byAlias
	}
	switch {
	case len(atNumber) == 0:
		return fail(card.StatusUnmatched, "number %s not in set %s", local, setID)
	case len(byNumber) == 0:
		return fail(card.StatusUnmatched, "no %s print at set %s #%s; has %q", rowPrint, setID, local, atNumber)
	case len(byName) == 0:
		var names []string
		for _, c := range byNumber {
			names = append(names, c.Name)
		}
		return fail(card.StatusUnmatched, "name mismatch: collection %q, set %s #%s has %q", row.Name, setID, local, names)
	case len(byName) > 1:
		return fail(card.StatusAmbiguous, "%d cards in set %s match #%s %q", len(byName), setID, local, row.Name)
	}
```

> **Go note:** `%s` on `rowPrint` calls its `String()` method, so the plain print reads `standard`. `%q` on a `[]string` quotes each element: `["Pansear" "Panpour"]`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/resolve/ -v` then `go test ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/resolve
git commit -m "resolve: match only source products of the row's print"
```

---

### Task 4: Status page copy and the new reason group

**Files:**
- Modify: `internal/site/sections.go:103-104`
- Test: `internal/site/sections_test.go` (the grouping test around line 70–110)

**Interfaces:**
- Consumes: the reason substring `print at set` (Task 3).

- [ ] **Step 1: Write the failing test**

In the reasons slice of the grouping test, add a line after the `name mismatch` reason:

```go
		`no pokeball print at set 24326 #016; has ["Panpour"]`,
```

In `want`, add:

```go
		"Pattern print not listed": 1,
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/site/ -run TestGroups -v`
Expected: FAIL: `group "Pattern print not listed" = 0, want 1`, and `Other` = 2.

- [ ] **Step 3: Implement**

In `reasonGroups`, change the first two entries and add one after them. The old `name mismatch` hint ("Mostly cards the source lists only as a pattern print") described the 356 stale rows that the 2026-09-27 re-import resolved, so it is no longer true.

```go
	{card.ErrUnsupportedVariant.Error(), "Special prints not priced yet", "Cosmos, Prize Pack, stamps and promos"},
	{"name mismatch", "Name differs at the source", "Spelling or formatting differs between the export and the source"},
	{"print at set", "Pattern print not listed", "The source has the number, but not in this print"},
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/site/ ./cmd/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/site
git commit -m "site: special-print copy and a group for missing pattern prints"
```

---

### Task 5: End to end, a pattern row beside its plain sibling

**Files:**
- Test: `cmd/pricewatch/e2e_test.go` (new test at the end of the file; the shared `fakePokeWallet` is not touched, so existing counts stay valid)

**Interfaces:**
- Consumes: `importCollection`, `priceRun`, `providerOpts`, `quiet` (existing in `package main`), `store.Open`, `(*store.SQLite).History(ctx, source) ([]card.Observation, error)`.

- [ ] **Step 1: Write the test**

Add `"github.com/jkhaynes/pricewatch/internal/store"` to the imports, then:

```go
// The silent failure CLAUDE.md warns about: one print priced as another. A
// Poké Ball row and a Master Ball row sit beside the plain card's Normal and
// Reverse Holo rows; each must be priced from its own product.
func TestPatternPrintsArePricedAsTheirOwnProducts(t *testing.T) {
	t.Setenv("POKEWALLET_API_KEY", "test-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sets":
			io.WriteString(w, `{"data":[{"name":"SV: White Flare","set_code":"WHT","set_id":"24326","language":"eng"}]}`)
		case "/sets/24326":
			io.WriteString(w, `{"cards":[
				{"id":"pk_plain","card_info":{"name":"Pansear","card_number":"014/086"}},
				{"id":"pk_pb","card_info":{"name":"Pansear (Poke Ball Pattern)","card_number":"014/086"}},
				{"id":"pk_mb","card_info":{"name":"Pansear (Master Ball Pattern)","card_number":"014/086"}}],
				"pagination":{"page":1,"total_pages":1}}`)
		case "/cards/pk_plain":
			io.WriteString(w, `{"tcgplayer":{"prices":[{"sub_type_name":"Normal","market_price":0.05},{"sub_type_name":"Reverse Holofoil","market_price":0.2}]}}`)
		case "/cards/pk_pb":
			io.WriteString(w, `{"tcgplayer":{"prices":[{"sub_type_name":"Holofoil","market_price":0.3}]}}`)
		case "/cards/pk_mb":
			io.WriteString(w, `{"tcgplayer":{"prices":[{"sub_type_name":"Holofoil","market_price":1.5}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	csvPath, db := filepath.Join(dir, "export.csv"), filepath.Join(dir, "pw.db")
	body := "TCG region,Card name,Card number,Card number sorting order,Expansion,Rarity,Card variant,Card language,Card condition,Quantity,Price,Total price,Note\n" +
		"International,Pansear,014/086,14,White Flare,Common,Normal,English,Mint,1,,,\n" +
		"International,Pansear,014/086,14,White Flare,Common,Reverse Holo,English,Mint,1,,,\n" +
		"International,Pansear,014/086,14,White Flare,Common,Poké Ball Reverse Holo,English,Mint,1,,,\n" +
		"International,Pansear,014/086,14,White Flare,Common,Master Ball Reverse Holo,English,Mint,1,,,\n"
	if err := os.WriteFile(csvPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	prov := providerOpts{BaseURL: srv.URL, Timeout: time.Second, Limits: &source.Limits{}}

	var out bytes.Buffer
	counts, err := importCollection(t.Context(), importOpts{DB: db, CSV: csvPath, Source: "pokewallet", Provider: prov}, &out, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if counts[card.StatusResolved] != 4 {
		t.Fatalf("import counts = %v\n%s", counts, out.String())
	}

	sum, err := priceRun(t.Context(), nil, runOpts{DB: db, Source: "pokewallet", Budget: 10, Workers: 2,
		Provider: prov, Now: time.Now}, &out, quiet)
	if err != nil {
		t.Fatal(err)
	}
	// Three products, one request each; the plain one answers two rows.
	if sum.Requests != 3 || sum.OK != 4 {
		t.Fatalf("run = %+v\n%s", sum, out.String())
	}

	st, err := store.Open(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	obs, err := st.History(t.Context(), "pokewallet")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, o := range obs {
		got[o.CardID] = *o.Market
	}
	const k = "international|white flare|014/086|"
	want := map[string]float64{
		k + "normal|english":                   0.05,
		k + "reverse holo|english":             0.2,
		k + "poké ball reverse holo|english":   0.3,
		k + "master ball reverse holo|english": 1.5,
	}
	for key, w := range want {
		if got[key] != w {
			t.Errorf("%s = %v, want %v", key, got[key], w)
		}
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./cmd/pricewatch/ -run TestPatternPrintsArePricedAsTheirOwnProducts -v`
Expected: PASS. Tasks 1–3 already implement the behaviour, so this is a regression guard, not a red test. To prove it guards something, temporarily comment out the `if c.Print != rowPrint { continue }` block in `resolve.go` and rerun. Expected: FAIL, with import counts showing ambiguous rows. Restore the block and rerun to PASS. Show both outputs.

If `History` has a different signature, or `CardID` isn't the collection key, check `internal/store/read.go:45` and `internal/card/price.go:24` and adapt the test to the real names. Do not change `store`.

- [ ] **Step 3: Commit**

```bash
git add cmd/pricewatch/e2e_test.go
git commit -m "e2e: pattern prints are priced from their own products"
```

---

### Task 6: PRD and README

**Files:**
- Modify: `docs/PRD.md` (section 6 table, section 8 after DD-15, section 13 idea 2)
- Modify: `README.md:270-271`

- [ ] **Step 1: FR-17**

In the section 6 table, after FR-16:

```markdown
| FR-17 | Price pattern reverse holos (Poké, Master, Friend, Quick, Love, Dusk Ball, Rocket, Energy) as their own source products, never as the plain card (DD-16) | P1 |
```

- [ ] **Step 2: DD-16**

After DD-15's closing **Scope** paragraph, add:

```markdown
### DD-16: A pattern print is its own source product, matched by print

**Decision (2026-09-27):** a pattern reverse holo (Poké Ball, Master Ball, Friend, Quick,
Love, Dusk Ball, Rocket, Energy) is priced from its own source product, never from the
plain card. The design is in `docs/superpowers/specs/2026-09-27-special-prints-design.md`.

- **What the source does:** PokéWallet lists each pattern print as a separate product at
  the plain card's number, e.g. `Pansear (Poke Ball Pattern)`. SV sets name it
  `(… Ball Pattern)`, ME sets `(… Ball)`, and the one price sits under `Holofoil` or
  `Reverse Holofoil` depending on the era (probe, 2026-09-27).
- **`card.Print`:** each row and each source product has a print. The provider derives a
  product's print from its own qualifier table, so source wording never reaches the
  resolver. The resolver only considers products whose print equals the row's.
- **Pricing:** `VariantPattern` accepts `Holofoil` or `Reverse Holofoil`. A product with
  both is ambiguous and reported (DD-5).
```

- [ ] **Step 3: Section 13, idea 2**

At the end of idea 2's text (before idea 3), add:

```markdown
   **Delivered 2026-09-27 for ball-pattern, Rocket and Energy reverse holos (FR-17, DD-16).**
   Cosmos, Prize Pack, stamps and promos remain excluded.
```

- [ ] **Step 4: README**

Replace the special-prints bullet under "What phase 1 does not do":

```markdown
- **Special prints:** Cosmos, Prize Pack, stamps and promos are reported as
  `unsupported variant`. Ball-pattern, Rocket and Energy reverse holos are priced from
  their own products (PRD DD-16).
```

- [ ] **Step 5: Verify and commit**

Run: `gofmt -l . ; go vet ./... ; go test ./...`
Expected: no gofmt output, vet clean, all tests PASS.

```bash
git add docs/PRD.md README.md
git commit -m "docs: FR-17 and DD-16, pattern prints are priced"
```

---

### Task 7: Merge and verify live (needs the user at each step)

These steps touch GitHub and the live database. Ask before each one.

- [ ] **Step 1:** Push `feat/special-prints` and open a PR against `main`. The body summarises the change and ends with the attribution lines from the system reminder.
- [ ] **Step 2:** After the user merges, dispatch the cloud import:
  `gh workflow run pricewatch.yml -R jkhaynes/pricewatch-data -f command=import`, then `gh run watch <id> -R jkhaynes/pricewatch-data`.
- [ ] **Step 3:** From the run log, confirm that `resolved` rose by about 538, from 7,882, and that `unsupported variant` fell from 804 to about 266. List any `no <print> print` rows for the user.
- [ ] **Step 4:** After the next hourly runs have priced some pattern products, spot-check against the probe via the pricewatch MCP `price_history` / `find_cards` tools: Exeggcute Poké Ball about $0.32, Master Ball about $1.33 (Prismatic Evolutions #001), Chikorita Friend Ball about $0.24 (Ascended Heroes #008).
