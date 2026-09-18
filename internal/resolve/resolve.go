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

type Resolver struct {
	cat       Catalog
	source    string
	overrides map[string]string            // normalised expansion -> set ID
	byName    map[string][]string          // normalised set name -> set IDs; nil until loaded
	cards     map[string][]card.SourceCard // set ID -> cards
}

func New(cat Catalog, source string, overrides map[string]string) *Resolver {
	norm := make(map[string]string, len(overrides))
	for k, v := range overrides {
		norm[normExpansion(k)] = v
	}
	return &Resolver{cat: cat, source: source, overrides: norm, cards: map[string][]card.SourceCard{}}
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

	setIDs, err := r.setsFor(ctx, row.Expansion)
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
	local, _, _ := strings.Cut(row.Number, "/")
	var byNumber, byName []card.SourceCard
	for _, c := range cards {
		if normNumber(c.Number) == normNumber(local) {
			byNumber = append(byNumber, c)
			if normName(c.Name) == normName(row.Name) {
				byName = append(byName, c)
			}
		}
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

func (r *Resolver) setsFor(ctx context.Context, expansion string) ([]string, error) {
	name := normExpansion(expansion)
	if id, ok := r.overrides[name]; ok {
		return []string{id}, nil
	}
	if err := r.load(ctx); err != nil {
		return nil, err
	}
	return r.byName[name], nil
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

func normNumber(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsDigit(r) }) == -1 {
		if t := strings.TrimLeft(s, "0"); t != "" {
			return t
		}
		return "0"
	}
	return strings.ToUpper(s)
}

func normName(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1 // drop
	}, unaccent.Replace(s))
}

func LoadOverrides(rd io.Reader) (map[string]string, error) {
	recs, err := csv.NewReader(rd).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read overrides: %w", err)
	}
	out := map[string]string{}
	for i, rec := range recs {
		if i == 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "expansion") {
			continue // header
		}
		if len(rec) != 2 {
			return nil, fmt.Errorf("overrides line %d: want 2 fields, got %d", i+1, len(rec))
		}
		out[strings.TrimSpace(rec[0])] = strings.TrimSpace(rec[1])
	}
	return out, nil
}
