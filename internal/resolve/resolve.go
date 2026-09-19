package resolve

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/jkhaynes/pricewatch/internal/card"
)

// Catalog is what the resolver needs from a provider. Declared here, at the
// point of use; providers satisfy it without referring to it.
type Catalog interface {
	Sets(ctx context.Context) ([]card.SourceSet, error)
	Cards(ctx context.Context, setID string) ([]card.SourceCard, error)
}

// Override pins an expansion to a provider set. With a NumberPrefix it applies
// only to cards whose number starts with that prefix, which is how subsets the
// export files under their parent reach their own set: Shining Fates cards
// numbered "SV086/SV122" live in PokeWallet's "Shining Fates: Shiny Vault".
type Override struct {
	Expansion    string
	NumberPrefix string // optional, e.g. "SV", "GG", "TG"
	SetID        string
}

type Resolver struct {
	cat       Catalog
	source    string
	overrides map[string]string            // overrideKey(expansion, prefix) -> set ID
	byName    map[string][]string          // normalised set name -> set IDs; nil until loaded
	cards     map[string][]card.SourceCard // set ID -> cards
}

func New(cat Catalog, source string, overrides []Override) *Resolver {
	norm := make(map[string]string, len(overrides))
	for _, o := range overrides {
		norm[overrideKey(o.Expansion, o.NumberPrefix)] = o.SetID
	}
	return &Resolver{cat: cat, source: source, overrides: norm, cards: map[string][]card.SourceCard{}}
}

func overrideKey(expansion, prefix string) string {
	return normExpansion(expansion) + "|" + strings.ToUpper(prefix)
}

// numberPrefix returns the leading letters of a card's local number:
// "SV086" -> "SV", "GG24" -> "GG", "59" -> "".
func numberPrefix(local string) string {
	i := strings.IndexFunc(local, func(r rune) bool { return !unicode.IsLetter(r) })
	if i < 0 {
		return local
	}
	return local[:i]
}

var english = map[string]bool{"english": true, "en": true}

func (r *Resolver) Resolve(ctx context.Context, row card.Row) (card.Mapping, error) {
	m := card.Mapping{Key: row.Key(), Source: r.source}
	fail := func(st card.Status, format string, args ...any) (card.Mapping, error) {
		m.Status, m.Reason = st, fmt.Sprintf(format, args...)
		return m, nil
	}

	if !english[card.Normalize(row.Language)] {
		return fail(card.StatusUnmatched, "unsupported language %q", row.Language)
	}
	variant, err := card.ParseVariant(row.Variant)
	if err != nil {
		return fail(card.StatusUnmatched, "%v", err)
	}

	local, _, _ := strings.Cut(row.Number, "/")
	setIDs, err := r.setsFor(ctx, row.Expansion, numberPrefix(strings.TrimSpace(local)))
	if err != nil {
		return card.Mapping{}, err
	}
	switch len(setIDs) {
	case 0:
		return fail(card.StatusUnmatched, "unknown expansion %q%s", row.Expansion, r.suggest(row.Expansion))
	case 1:
	default:
		return fail(card.StatusAmbiguous, "expansion %q matches sets %v", row.Expansion, setIDs)
	}
	setID := setIDs[0]

	cards, err := r.cardsIn(ctx, setID)
	if err != nil {
		return card.Mapping{}, err
	}
	var byNumber, byName, byAlias []card.SourceCard
	want := normName(row.Name)
	for _, c := range cards {
		if normNumber(c.Number) != normNumber(local) {
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
	case len(byNumber) == 0:
		return fail(card.StatusUnmatched, "number %s not in set %s", local, setID)
	case len(byName) == 0:
		var names []string
		for _, c := range byNumber {
			names = append(names, c.Name)
		}
		return fail(card.StatusUnmatched, "name mismatch: collection %q, set %s #%s has %q", row.Name, setID, local, names)
	case len(byName) > 1:
		return fail(card.StatusAmbiguous, "%d cards in set %s match #%s %q", len(byName), setID, local, row.Name)
	}

	m.Status, m.SourceCardID, m.Variant = card.StatusResolved, byName[0].ID, variant
	return m, nil
}

func (r *Resolver) load(ctx context.Context) error {
	if r.byName != nil {
		return nil
	}
	sets, err := r.cat.Sets(ctx)
	if err != nil {
		return fmt.Errorf("load sets: %w", err)
	}
	r.byName = map[string][]string{}
	for _, s := range sets {
		for _, n := range s.Names {
			k := normExpansion(n)
			if !slices.Contains(r.byName[k], s.ID) { // a set must never look ambiguous with itself
				r.byName[k] = append(r.byName[k], s.ID)
			}
		}
	}
	return nil
}

// setsFor finds the set for an expansion. A prefix-specific override wins, then
// a plain override, then a name match against the provider's set names.
func (r *Resolver) setsFor(ctx context.Context, expansion, prefix string) ([]string, error) {
	if prefix != "" {
		if id, ok := r.overrides[overrideKey(expansion, prefix)]; ok {
			return []string{id}, nil
		}
	}
	if id, ok := r.overrides[overrideKey(expansion, "")]; ok {
		return []string{id}, nil
	}
	if err := r.load(ctx); err != nil {
		return nil, err
	}
	return r.byName[normExpansion(expansion)], nil
}

// suggest lists up to three set IDs whose names contain, or are contained in,
// the expansion. A hint for the override file, never a match.
func (r *Resolver) suggest(expansion string) string {
	want := normExpansion(expansion)
	var hits []string
	for _, n := range slices.Sorted(maps.Keys(r.byName)) {
		if strings.Contains(want, n) || strings.Contains(n, want) {
			for _, id := range r.byName[n] {
				hits = append(hits, fmt.Sprintf("%s %q", id, n))
			}
		}
	}
	if len(hits) == 0 {
		return ""
	}
	return " (candidates: " + strings.Join(hits[:min(3, len(hits))], ", ") + ")"
}

func (r *Resolver) cardsIn(ctx context.Context, setID string) ([]card.SourceCard, error) {
	if cs, ok := r.cards[setID]; ok {
		return cs, nil
	}
	cs, err := r.cat.Cards(ctx, setID)
	if err != nil {
		return nil, fmt.Errorf("load cards for %s: %w", setID, err)
	}
	r.cards[setID] = cs
	return cs, nil
}

// unaccent folds the accents TCG Collector uses and PokeWallet drops ("Pokémon"
// vs "Pokemon"). A replacer, not Unicode normalisation: golang.org/x/text is not
// an approved dependency, and "é" is the only accent seen in the real data.
var unaccent = strings.NewReplacer("é", "e", "É", "E")

func normExpansion(s string) string {
	return card.Normalize(unaccent.Replace(strings.ReplaceAll(s, "&", " and ")))
}

// normNumber compares card numbers without leading zeros, with or without a
// letter prefix: "001" == "1", and "SV086" == "SV86". Anything else, such as
// "SWSH074a", is compared case-insensitively as is.
func normNumber(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	prefix := numberPrefix(s)
	digits := s[len(prefix):]
	if digits == "" || strings.IndexFunc(digits, func(r rune) bool { return !unicode.IsDigit(r) }) != -1 {
		return s
	}
	if t := strings.TrimLeft(digits, "0"); t != "" {
		return prefix + t
	}
	return prefix + "0"
}

func normName(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1 // drop
	}, unaccent.Replace(s))
}

// LoadOverrides reads "expansion,set_id[,number_prefix]" lines. The header line
// is optional and so is the third column, line by line.
func LoadOverrides(rd io.Reader) ([]Override, error) {
	cr := csv.NewReader(rd)
	cr.FieldsPerRecord = -1 // two or three fields per line
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read overrides: %w", err)
	}
	var out []Override
	for i, rec := range recs {
		if i == 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "expansion") {
			continue // header
		}
		if len(rec) != 2 && len(rec) != 3 {
			return nil, fmt.Errorf("overrides line %d: want 2 or 3 fields, got %d", i+1, len(rec))
		}
		o := Override{Expansion: strings.TrimSpace(rec[0]), SetID: strings.TrimSpace(rec[1])}
		if len(rec) == 3 {
			o.NumberPrefix = strings.TrimSpace(rec[2])
		}
		if o.Expansion == "" || o.SetID == "" {
			return nil, fmt.Errorf("overrides line %d: expansion and set_id are required", i+1)
		}
		out = append(out, o)
	}
	return out, nil
}
