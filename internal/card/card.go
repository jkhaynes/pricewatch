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
	// VariantPattern is a pattern print's own foil, whichever sub-type the
	// source files it under. Only pattern prints use it.
	VariantPattern Variant = "pattern"
	// VariantStamped is a stamped promo print's only price, whichever
	// sub-type the source files it under.
	VariantStamped Variant = "stamped"
)

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

	PrintPrerelease      Print = "prerelease"
	PrintPrereleaseStaff Print = "prerelease-staff"
	PrintWorlds          Print = "worlds"
	PrintWorldsStaff     Print = "worlds-staff"
	PrintAnniversary     Print = "30th"
)

func (p Print) String() string {
	if p == PrintStandard {
		return "standard"
	}
	return string(p)
}

type finish struct {
	variant Variant
	print   Print
}

// variantLabels maps normalised TCG Collector labels to what they mean (labels
// from the real export, Task 0 of phase 1). Anything absent is excluded on
// purpose: Cosmos, Prize Pack, set stamps and other promos.
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

	"prerelease":                  {VariantStamped, PrintPrerelease},
	"prerelease (staff)":          {VariantStamped, PrintPrereleaseStaff},
	"world championships":         {VariantStamped, PrintWorlds},
	"world championships (staff)": {VariantStamped, PrintWorldsStaff},
	"30th anniversary":            {VariantStamped, PrintAnniversary},
}

func ParseVariant(label string) (Variant, Print, error) {
	f, ok := variantLabels[Normalize(label)]
	if !ok {
		return "", PrintStandard, fmt.Errorf("%w: %q", ErrUnsupportedVariant, label)
	}
	return f.variant, f.print, nil
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
// Aliases are extra names the card may be matched by, but only when no card at
// the same number matches Name exactly. Providers add them for qualifiers that
// name the card's own print ("Whismur (117)" -> "Whismur"), never for ones that
// mark a different print at the same number.
// Print is the product's print. A pattern product ("Pansear (Poke Ball
// Pattern)") has its pattern's Print and its bare name in Aliases.
type SourceCard struct {
	ID      string
	Number  string
	Name    string
	Print   Print
	Aliases []string
}
