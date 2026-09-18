package card

import (
	"fmt"
	"strings"
)

// Row is one line of a TCG Collector export.
type Row struct {
	Region     string
	Name       string
	Number     string // "59/109", not 59
	SortNumber *int
	Expansion  string // display name, e.g. "EX Ruby & Sapphire"
	Rarity     string
	Variant    string // raw label, e.g. "Reverse Holo"
	Language   string
	Condition  string
	Quantity   int
	Price      *float64 // snapshot from the export (DD-6)
	Note       string
}

// Key is the constructed join key region|expansion|number|variant|language.
// The export has no card identifier, so this is the only stable identity we have.
func (r Row) Key() string {
	parts := []string{r.Region, r.Expansion, r.Number, r.Variant, r.Language}
	for i, p := range parts {
		parts[i] = Normalize(p)
	}
	return strings.Join(parts, "|")
}

// Normalize lower-cases, trims and collapses internal whitespace.
func Normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

type Variant string

const (
	VariantNormal           Variant = "normal"
	VariantHolo             Variant = "holo"
	VariantReverseHolo      Variant = "reverse"
	VariantFirstEdition     Variant = "1st-edition"
	VariantFirstEditionHolo Variant = "1st-edition-holo"
)

// variantLabels maps normalised TCG Collector labels to Variants (labels from
// the real export, Task 0). Anything absent is excluded on purpose: ball-pattern
// and Energy reverse holos, Cosmos, Prize Pack, stamps and promos are phase 2.
var variantLabels = map[string]Variant{
	"normal":           VariantNormal,
	"non-holo":         VariantNormal,
	"holo":             VariantHolo,
	"normal holo":      VariantHolo,
	"reverse holo":     VariantReverseHolo,
	"1st edition":      VariantFirstEdition,
	"1st edition holo": VariantFirstEditionHolo,
}

func ParseVariant(label string) (Variant, error) {
	v, ok := variantLabels[Normalize(label)]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedVariant, label)
	}
	return v, nil
}

type Status string

const (
	StatusResolved  Status = "resolved"
	StatusAmbiguous Status = "ambiguous"
	StatusUnmatched Status = "unmatched"
)

// Mapping is one card_map row: a collection key resolved (or not) to a card at
// one source. Several keys (Normal, Reverse Holo) share one SourceCardID.
type Mapping struct {
	Key          string
	Source       string
	SourceCardID string  // empty unless resolved
	Variant      Variant // empty unless resolved
	Status       Status
	Reason       string
}

// SourceSet is an expansion as a provider knows it. Names holds every display
// name that should match it; providers add aliases here.
type SourceSet struct {
	ID    string
	Names []string
}

// SourceCard is a card within a SourceSet. Providers clean Name (no "- 59/109"
// suffixes) and put only the local part of the number in Number ("59", "009").
type SourceCard struct {
	ID     string
	Number string
	Name   string
}
