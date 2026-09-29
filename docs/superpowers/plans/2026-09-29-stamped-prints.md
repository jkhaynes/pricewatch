# Stamped Prints Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Price the Prerelease, Prerelease (Staff), World Championships (± Staff) and 30th Anniversary promo prints, and fix the number-suffix cleanup that leaves `Ampharos - 075` unmatched.

**Architecture:** Extends DD-16's `card.Print`. The PokéWallet provider reads stamped prints from a product's trailing qualifiers (`(Prerelease)`, `[Staff]`, `(World Championships 2024)`, `(30th Celebration)`) and strips a padded ` - 075` number suffix. A new `VariantStamped` prices the product's only price. The resolver gains one narrow rule (DD-17): in a promo set, a Prerelease or 30th Anniversary row with no labelled product at its number matches the plain product.

**Tech Stack:** Go standard library only.

**Spec:** `docs/superpowers/specs/2026-09-29-stamped-prints-design.md`

## Global Constraints

- Standard library only; no new dependencies.
- TDD for every task: failing test first, show the red output, smallest change to green, commit.
- Table-driven tests.
- Never guess: every failure is `unmatched` or `ambiguous` with a reason (DD-5). The only sanctioned inference is DD-17, exactly as specified.
- `VariantPattern` and its `"pattern"` value are unchanged.
- Branch: `feat/stamped-prints` (created; the spec is committed on it).
- Commit messages end with:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01MB2L77AbZgKcuJXsQtZtre
  ```
- Characters such as `é` are written literally, never as escapes.
- Full suite: `go test ./...`; also `gofmt -l .` (must print nothing) and `go vet ./...`.

---

### Task 1: `card`: stamped prints and `VariantStamped`

**Files:**
- Modify: `internal/card/card.go`
- Test: `internal/card/card_test.go` (`TestParseVariant`)

**Interfaces:**
- Produces: `PrintPrerelease Print = "prerelease"`, `PrintPrereleaseStaff = "prerelease-staff"`, `PrintWorlds = "worlds"`, `PrintWorldsStaff = "worlds-staff"`, `PrintAnniversary = "30th"`; `VariantStamped Variant = "stamped"`; five new `ParseVariant` labels.

- [ ] **Step 1: Failing test.** In `TestParseVariant`'s table, after the `{"Energy Reverse Holo", …}` row, add:

```go
		// Stamped promo prints: a separate product at the source, priced at its only price.
		{"Prerelease", VariantStamped, PrintPrerelease, nil},
		{"Prerelease (Staff)", VariantStamped, PrintPrereleaseStaff, nil},
		{"World Championships", VariantStamped, PrintWorlds, nil},
		{"World Championships (Staff)", VariantStamped, PrintWorldsStaff, nil},
		{"30th Anniversary", VariantStamped, PrintAnniversary, nil},
```

- [ ] **Step 2: Red.** `go test ./internal/card/` → build failure: `undefined: VariantStamped`, `undefined: PrintPrerelease`, ….

- [ ] **Step 3: Implement.** In the `Variant` constants, after `VariantPattern`:

```go
	// VariantStamped is a stamped promo print's only price, whichever
	// sub-type the source files it under.
	VariantStamped Variant = "stamped"
```

In the `Print` constants, after `PrintEnergy`:

```go
	PrintPrerelease      Print = "prerelease"
	PrintPrereleaseStaff Print = "prerelease-staff"
	PrintWorlds          Print = "worlds"
	PrintWorldsStaff     Print = "worlds-staff"
	PrintAnniversary     Print = "30th"
```

In `variantLabels`, after the `"energy reverse holo"` entry:

```go

	"prerelease":                  {VariantStamped, PrintPrerelease},
	"prerelease (staff)":          {VariantStamped, PrintPrereleaseStaff},
	"world championships":         {VariantStamped, PrintWorlds},
	"world championships (staff)": {VariantStamped, PrintWorldsStaff},
	"30th anniversary":            {VariantStamped, PrintAnniversary},
```

Update `variantLabels`' comment: "Anything absent is excluded on purpose: Cosmos, Prize Pack, set stamps and other promos."

- [ ] **Step 4: Green.** `go test ./...` → PASS.
- [ ] **Step 5: Commit.** `card: stamped promo prints and VariantStamped`

---

### Task 2: PokéWallet reads stamped prints and padded number suffixes

**Files:**
- Modify: `internal/source/pokewallet/pokewallet.go`
- Test: `internal/source/pokewallet/pokewallet_test.go`

**Interfaces:**
- Consumes: Task 1's prints and `VariantStamped`.
- Produces: `Cards` sets `Print` for stamped products (full raw `Name`, bare name in `Aliases`); standard names lose a ` - <own number>` suffix however it is padded; `Quote` answers `VariantStamped` from exactly one of `Normal`, `Holofoil`, `Reverse Holofoil`.

- [ ] **Step 1: Failing tests.** Add after `TestCardsPatternPrints`:

```go
// Stamped promo prints (probe, 2026-09-29): the staff marker is a trailing
// "[Staff]" or "(Staff)", World Championships carry a year, and promo names pad
// the number ("Ampharos - 075" at card number 75).
func TestCardsStampedPrints(t *testing.T) {
	tests := []struct {
		name, number string
		wantPrint    card.Print
		wantAliases  []string
	}{
		{"Ceruledge (Prerelease)", "14", card.PrintPrerelease, []string{"Ceruledge"}},
		{"Ceruledge (Prerelease) [Staff]", "14", card.PrintPrereleaseStaff, []string{"Ceruledge"}},
		{"Chi-Yu - 057 (Prerelease)", "057", card.PrintPrerelease, []string{"Chi-Yu"}},
		{"Ledian - 133 (Prerelease) [Staff]", "133", card.PrintPrereleaseStaff, []string{"Ledian"}},
		{"Ampharos - 075 [Staff]", "75", card.PrintPrereleaseStaff, []string{"Ampharos"}},
		{"Alakazam - 003 (Staff)", "3", card.PrintPrereleaseStaff, []string{"Alakazam"}},
		{"Paradise Resort - 150 (World Championships 2024)", "150", card.PrintWorlds, []string{"Paradise Resort"}},
		{"Paradise Resort - 150 (World Championships 2024) [Staff]", "150", card.PrintWorldsStaff, []string{"Paradise Resort"}},
		{"Paradise Resort - 224 (World Championship 2025)", "224", card.PrintWorlds, []string{"Paradise Resort"}},
		{"Sylveon ex - 100 (30th Celebration)", "100", card.PrintAnniversary, []string{"Sylveon ex"}},
		// Unknown combinations stay standard and unmatchable.
		{"Nidorina - 101 (30th Celebration) (Pokemon Center Exclusive)", "101", card.PrintStandard, nil},
		{"Mew - 105 (30th Celebration) [Staff]", "105", card.PrintStandard, nil},
		{"Pikachu (Poke Ball Pattern) [Staff]", "117", card.PrintStandard, nil},
		// A staff print of a different print: staff is known, the base is not this card.
		{"Slowbro - 083 (Pitch Black Stamped) [Staff]", "83", card.PrintPrereleaseStaff, nil},
		// Other brackets are not staff markers.
		{"Professor's Research [Professor Oak]", "122/131", card.PrintStandard, nil},
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
				t.Errorf("Name = %q; a stamped product keeps its full name for reports", c.Name)
			}
		})
	}
}

// A trailing " - <number>" is the card's own number however it is padded or
// spaced, and is removed; any other number is part of the name.
func TestCardsNumberSuffix(t *testing.T) {
	tests := []struct{ name, number, want string }{
		{"Mudkip - 59/109", "59/109", "Mudkip"},
		{"Ampharos - 075", "75", "Ampharos"},
		{"Quaxly -  063", "63", "Quaxly"},
		{"Haunter  - 027", "27", "Haunter"},
		{"Mimikyu -160/091", "160/091", "Mimikyu"},
		{"Mega Charizard X ex - 023", "23", "Mega Charizard X ex"},
		{"Porygon - 2", "150", "Porygon - 2"},
		{"Destined Rivals Booster Box", "", "Destined Rivals Booster Box"},
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
			if cards[0].Name != tt.want {
				t.Errorf("Name = %q, want %q", cards[0].Name, tt.want)
			}
		})
	}
}

// A stamped product carries one price: Holofoil for most, Normal for World
// Championships, Reverse Holofoil for a few staff prints. Two is a guess.
func TestQuoteStamped(t *testing.T) {
	tests := []struct {
		name, body string
		want       any // float64 market, or error sentinel
	}{
		{"Normal", `{"tcgplayer":{"prices":[{"sub_type_name":"Normal","market_price":641.76}]}}`, 641.76},
		{"Holofoil", `{"tcgplayer":{"prices":[{"sub_type_name":"Holofoil","market_price":84.02}]}}`, 84.02},
		{"Reverse Holofoil", `{"tcgplayer":{"prices":[{"sub_type_name":"Reverse Holofoil","market_price":3.5}]}}`, 3.5},
		{"two prices is ambiguous", `{"tcgplayer":{"prices":[
			{"sub_type_name":"Holofoil","market_price":2},{"sub_type_name":"Normal","market_price":1}]}}`, card.ErrVariantAmbiguous},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs, err := New(fakeGetter{"/cards/pk_x": tt.body}).Quote(t.Context(), "pk_x", []card.Variant{card.VariantStamped})
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

- [ ] **Step 2: Red.** `go test ./internal/source/pokewallet/ -run "TestCardsStampedPrints|TestCardsNumberSuffix|TestQuoteStamped" -v` → stamped rows report `Print ""`; padded suffixes are not removed (`Ampharos - 075`, `Quaxly -  063`, `Haunter  - 027`, `Mimikyu -160/091`, `Mega Charizard X ex - 023`); `TestQuoteStamped` priced cases give `ErrVariantUnavailable`. Rows expected to stay standard pass.

- [ ] **Step 3: Implement.** Add `"unicode"` to the imports. In `subtypes`, after the `VariantPattern` line:

```go
	card.VariantStamped:          {"Normal", "Holofoil", "Reverse Holofoil"}, // the product's only price (probe, 2026-09-29)
```

Replace `patternOf` (keep the `patterns` map and its comment) with:

```go
// staffMarker is PokeWallet's suffix for staff prints: "Ceruledge (Prerelease) [Staff]".
var staffMarker = regexp.MustCompile(`(?i)\s*\[staff\]$`)

// worlds matches World Championships qualifiers whatever the year, including
// the "World Championship 2025" misspelling seen live.
var worlds = regexp.MustCompile(`^world championships? \d{4}$`)

// printOf reads a product's print from its trailing qualifiers and returns the
// name without them: "Ceruledge (Prerelease) [Staff]" is PrintPrereleaseStaff
// with base "Ceruledge". Anything unrecognised is PrintStandard with the name
// unchanged, so it never matches a pattern or stamped row.
func printOf(name string) (card.Print, string) {
	base, staff := name, false
	if loc := staffMarker.FindStringIndex(base); loc != nil {
		base, staff = base[:loc[0]], true
	}
	m := trailingQualifier.FindStringSubmatch(base)
	if m != nil && strings.EqualFold(m[2], "staff") {
		base, staff = m[1], true
		m = trailingQualifier.FindStringSubmatch(base)
	}
	q := ""
	if m != nil {
		q = strings.ToLower(m[2])
	}
	switch {
	case q == "prerelease" && staff:
		return card.PrintPrereleaseStaff, m[1]
	case q == "prerelease":
		return card.PrintPrerelease, m[1]
	case worlds.MatchString(q) && staff:
		return card.PrintWorldsStaff, m[1]
	case worlds.MatchString(q):
		return card.PrintWorlds, m[1]
	case staff && (q == "30th celebration" || patterns[q] != ""):
		return card.PrintStandard, name // a combination not seen live
	case q == "30th celebration":
		return card.PrintAnniversary, m[1]
	case patterns[q] != "":
		return patterns[q], m[1]
	case staff:
		return card.PrintPrereleaseStaff, base // "Ampharos - 075 [Staff]"
	}
	return card.PrintStandard, name
}

// numberSuffix matches a trailing number: " - 075", "  -  063", " -160/091".
var numberSuffix = regexp.MustCompile(`\s+-\s*([A-Za-z]*\d+(?:/[A-Za-z]*\d+)?)$`)

// trimNumber removes a trailing number when it is the card's own, however it
// is padded: "Ampharos - 075" at card number "75" is "Ampharos".
func trimNumber(name, number string) string {
	m := numberSuffix.FindStringSubmatchIndex(name)
	if m == nil || !sameLocal(name[m[2]:m[3]], number) {
		return name
	}
	return name[:m[0]]
}

// sameLocal compares two card numbers before any "/", ignoring leading zeros
// and case: "075" and "75/132" are the same card.
func sameLocal(a, b string) bool {
	a, _, _ = strings.Cut(a, "/")
	b, _, _ = strings.Cut(b, "/")
	return strings.EqualFold(unpad(a), unpad(b))
}

// unpad drops leading zeros after any letter prefix: "SV086" is "SV86".
func unpad(s string) string {
	i := strings.IndexFunc(s, unicode.IsDigit)
	if i < 0 {
		return s
	}
	d := strings.TrimLeft(s[i:], "0")
	if d == "" {
		d = "0"
	}
	return s[:i] + d
}
```

In `Cards`, replace the loop body:

```go
		for _, c := range resp.Cards {
			number := c.CardInfo.CardNumber
			local, _, _ := strings.Cut(number, "/")
			sc := card.SourceCard{ID: c.ID, Number: local}
			if pr, base := printOf(c.CardInfo.Name); pr != card.PrintStandard {
				// Keep the full name for reports; match by the bare name.
				sc.Name, sc.Print = c.CardInfo.Name, pr
				if bare, ok := bareName(trimNumber(base, number), local); ok {
					sc.Aliases = []string{bare}
				}
			} else {
				sc.Name = trimNumber(c.CardInfo.Name, number)
				sc.Aliases = aliases(sc.Name, local)
			}
			out = append(out, sc)
		}
```

- [ ] **Step 4: Green.** Focused tests, then `go test ./...`. All existing `TestCards*` tests must pass unchanged.
- [ ] **Step 5: Commit.** `pokewallet: read stamped prints and padded number suffixes`

> **Go note:** `FindStringSubmatchIndex` returns byte offsets in pairs (`m[0]:m[1]` whole match, `m[2]:m[3]` first group), so the name can be sliced without a second search.

---

### Task 3: Resolver: the promo-set rule (DD-17)

**Files:**
- Modify: `internal/resolve/resolve.go` (`Resolve`, new `candidates`)
- Test: `internal/resolve/resolve_test.go`

**Interfaces:**
- Consumes: Task 1's prints and `VariantStamped`.
- Produces: reasons `no prerelease print …`, `no prerelease-staff print …` (existing format).

- [ ] **Step 1: Failing tests.** In `newFake()`, add to `sets`:

```go
			{ID: "24451", Names: []string{"ME: Mega Evolution Promo", "Mega Evolution Promo"}},
			{ID: "22872", Names: []string{"SV: Scarlet & Violet Promo Cards", "Scarlet & Violet Promo Cards"}},
			{ID: "24269", Names: []string{"SV10: Destined Rivals", "Destined Rivals"}},
```

and to `cards`:

```go
			// Mega Evolution Promos as PokeWallet lists them (probe, 2026-09-29).
			// The plain Ceruledge is added to prove a labelled product wins.
			"24451": {
				{ID: "pk_cer", Number: "14", Name: "Ceruledge"},
				{ID: "pk_cer_pre", Number: "14", Name: "Ceruledge (Prerelease)", Print: card.PrintPrerelease, Aliases: []string{"Ceruledge"}},
				{ID: "pk_cer_staff", Number: "14", Name: "Ceruledge (Prerelease) [Staff]", Print: card.PrintPrereleaseStaff, Aliases: []string{"Ceruledge"}},
				{ID: "pk_amph", Number: "75", Name: "Ampharos"},
				{ID: "pk_amph_staff", Number: "75", Name: "Ampharos - 075 [Staff]", Print: card.PrintPrereleaseStaff, Aliases: []string{"Ampharos"}},
				{ID: "pk_bulba", Number: "37", Name: "Bulbasaur"},
				{ID: "pk_luna", Number: "4", Name: "Lunatone"},
			},
			"22872": {
				{ID: "pk_pr150", Number: "150", Name: "Paradise Resort - 150 (World Championships 2024)", Print: card.PrintWorlds, Aliases: []string{"Paradise Resort"}},
				{ID: "pk_pr150_staff", Number: "150", Name: "Paradise Resort - 150 (World Championships 2024) [Staff]", Print: card.PrintWorldsStaff, Aliases: []string{"Paradise Resort"}},
			},
			// A main set: the unlabelled product is the ordinary card.
			"24269": {{ID: "pk_trmimikyu", Number: "087", Name: "Team Rocket's Mimikyu"}},
```

In `TestResolve`, add before the loop:

```go
	me := []Override{{Expansion: "Mega Evolution Promos", SetID: "24451"}}
	sv := []Override{{Expansion: "Scarlet & Violet Promos", SetID: "22872"}}
```

Move the `tests` declaration below these two lines if needed, and add cases:

```go
		// Stamped promo prints.
		{"labelled prerelease beats the plain product", r("Ceruledge", "Mega Evolution Promos", "014", "Prerelease", "English"), me,
			card.StatusResolved, "pk_cer_pre", card.VariantStamped, ""},
		{"labelled prerelease staff", r("Ceruledge", "Mega Evolution Promos", "014", "Prerelease (Staff)", "English"), me,
			card.StatusResolved, "pk_cer_staff", card.VariantStamped, ""},
		{"promo set: unlabelled prerelease is the plain product", r("Ampharos", "Mega Evolution Promos", "075", "Prerelease", "English"), me,
			card.StatusResolved, "pk_amph", card.VariantStamped, ""},
		{"promo set: staff by its marker", r("Ampharos", "Mega Evolution Promos", "075", "Prerelease (Staff)", "English"), me,
			card.StatusResolved, "pk_amph_staff", card.VariantStamped, ""},
		{"promo set: 30th anniversary is the plain product", r("Bulbasaur", "Mega Evolution Promos", "037", "30th Anniversary", "English"), me,
			card.StatusResolved, "pk_bulba", card.VariantStamped, ""},
		{"worlds staff", r("Paradise Resort", "Scarlet & Violet Promos", "150", "World Championships (Staff)", "English"), sv,
			card.StatusResolved, "pk_pr150_staff", card.VariantStamped, ""},
		{"worlds", r("Paradise Resort", "Scarlet & Violet Promos", "150", "World Championships", "English"), sv,
			card.StatusResolved, "pk_pr150", card.VariantStamped, ""},
		{"staff never falls back to the plain product", r("Lunatone", "Mega Evolution Promos", "004", "Prerelease (Staff)", "English"), me,
			card.StatusUnmatched, "", "", `no prerelease-staff print at set 24451 #004; has ["Lunatone"]`},
		{"main set: prerelease never falls back", r("Team Rocket's Mimikyu", "Destined Rivals", "087/182", "Prerelease", "English"), nil,
			card.StatusUnmatched, "", "", `no prerelease print at set 24269 #087; has ["Team Rocket's Mimikyu"]`},
```

- [ ] **Step 2: Red.** `go test ./internal/resolve/ -run TestResolve -v` → the two "promo set: … is the plain product" cases fail with `no prerelease print …` / `no 30th print …`; every other new case already passes (Tasks 1–2 did the matching).

- [ ] **Step 3: Implement.** In `resolve.go`, before `Resolve`:

```go
// fallsBackInPromoSets lists the prints a promo set may leave unlabelled. A
// prerelease-only or anniversary-only promo number is listed as the plain
// product (Ampharos #075, Bulbasaur #037; probe 2026-09-29), so in a promo set,
// and only there, the plain product is taken as that print (DD-17). In a main
// set the plain product is the ordinary card.
var fallsBackInPromoSets = map[card.Print]bool{card.PrintPrerelease: true, card.PrintAnniversary: true}
```

Replace the candidate loop and the alias fallback with:

```go
	want := normName(row.Name)
	var atNumber []string // every product at the number, of any print, for reasons
	var here []card.SourceCard
	for _, c := range cards {
		if normNumber(c.Number) == normNumber(local) {
			atNumber = append(atNumber, c.Name)
			here = append(here, c)
		}
	}
	byNumber, byName := candidates(here, rowPrint, want)
	if len(byNumber) == 0 && fallsBackInPromoSets[rowPrint] && strings.HasSuffix(card.Normalize(row.Expansion), "promos") {
		byNumber, byName = candidates(here, card.PrintStandard, want)
	}
```

The `switch` below is unchanged. Add after `Resolve`:

```go
// candidates returns the products of print p, and those whose name matches
// want. A pattern or stamped print is its own product: a plain row must never
// price one, nor a Poké Ball row the plain card. Aliases only count when no
// product matches the name exactly, so "Charizard" still beats "Charizard
// (Full Art)" at the same number.
func candidates(here []card.SourceCard, p card.Print, want string) (byPrint, byName []card.SourceCard) {
	var byAlias []card.SourceCard
	for _, c := range here {
		if c.Print != p {
			continue
		}
		byPrint = append(byPrint, c)
		if normName(c.Name) == want {
			byName = append(byName, c)
		}
		if slices.ContainsFunc(c.Aliases, func(a string) bool { return normName(a) == want }) {
			byAlias = append(byAlias, c)
		}
	}
	if len(byName) == 0 {
		byName = byAlias
	}
	return byPrint, byName
}
```

- [ ] **Step 4: Green.** `go test ./internal/resolve/ -v`, then `go test ./...`.
- [ ] **Step 5: Commit.** `resolve: in promo sets, an unlabelled prerelease or 30th print is the plain product`

> **Go note:** `candidates` returns two named slices. Named results document what each return means, the way C# tuple element names do.

---

### Task 4: Docs

**Files:** `docs/PRD.md`, `README.md`, `docs/superpowers/specs/2026-09-29-stamped-prints-design.md`

- [ ] **Step 1: FR-17.** Replace the FR-17 row's requirement text with: `Price pattern reverse holos (Poké, Master, Friend, Quick, Love, Dusk Ball, Rocket, Energy) and stamped promo prints (Prerelease, Staff, World Championships, 30th Anniversary) as their own source products, never as the plain card (DD-16, DD-17)`.
- [ ] **Step 2: DD-17.** After DD-16's last bullet, add:

```markdown
### DD-17: In a promo set, an unlabelled prerelease or anniversary print is the plain product

**Decision (2026-09-29, the author's call):** stamped promo prints extend DD-16 with prints
for Prerelease, Prerelease (Staff), World Championships (± Staff) and 30th Anniversary,
read from PokéWallet's `(Prerelease)`, `[Staff]` / `(Staff)`, `(World Championships <year>)`
and `(30th Celebration)` qualifiers and priced at the product's only price. The design is in
`docs/superpowers/specs/2026-09-29-stamped-prints-design.md`.

- **The inference:** when a promo number *is* the prerelease or anniversary card,
  PokéWallet lists it unlabelled (`Ampharos - 075`, `Bulbasaur - 037`) and labels only the
  staff print. So in a set whose TCG Collector name ends in "Promos", a Prerelease or
  30th Anniversary row with no labelled product at its number takes the plain product.
- **Where it does not apply:** main sets, where the plain product is the ordinary card
  (Destined Rivals #087 stays `no prerelease print`), and Staff or World Championships
  rows, which are always labelled.
- This is the one sanctioned exception to "never guess" (DD-5): a rule the author signed
  off, narrow enough to state in a sentence and tested both ways.
```

- [ ] **Step 3: Idea 2.** Append to the "Delivered 2026-09-27 …" note in §13 idea 2: ` Stamped promo prints (Prerelease, Staff, World Championships, 30th Anniversary) delivered 2026-09-29 (DD-17).`
- [ ] **Step 4: README.** Replace the special-prints bullet under "What phase 1 does not do" with:

```markdown
- **Special prints:** Cosmos, Prize Pack, set stamps and other promos are reported as
  `unsupported variant`. Ball-pattern, Rocket and Energy reverse holos, and Prerelease,
  Staff, World Championships and 30th Anniversary promos, are priced from their own
  products (PRD DD-16, DD-17).
```

- [ ] **Step 5: Spec status.** `**Status:** approved; implemented on feat/stamped-prints`.
- [ ] **Step 6: Verify and commit.** `gofmt -l .`, `go vet ./...`, `go test ./...`. Commit: `docs: DD-17, stamped promo prints`.

---

### After merge (needs the user)

- [ ] Dispatch the cloud `import`; expect about 94 more resolved rows (8,334 → ~8,428).
- [ ] List the `no prerelease` / `no prerelease-staff` / `no worlds` / `no 30th` reasons for the user.
