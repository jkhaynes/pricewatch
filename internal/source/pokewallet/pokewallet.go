package pokewallet

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/source"
)

const (
	Name           = "pokewallet"
	DefaultBaseURL = "https://api.pokewallet.io"
	pageSize       = 50
)

// Limits are the free plan's, as reported by GET / and every response's headers.
// PerSecond is a politeness cap: pacing follows the server's own hourly count (DD-11).
var Limits = source.Limits{PerSecond: 2, PerHour: 100, PerDay: 1000}

// subtypes maps our variants to tcgplayer sub_type_name values (spike, 2026-09-18).
var subtypes = map[card.Variant][]string{
	card.VariantNormal:           {"Normal", "Unlimited"},
	card.VariantHolo:             {"Holofoil", "Unlimited Holofoil"},
	card.VariantReverseHolo:      {"Reverse Holofoil"},
	card.VariantFirstEdition:     {"1st Edition"},
	card.VariantFirstEditionHolo: {"1st Edition Holofoil"},
	card.VariantPattern:          {"Holofoil", "Reverse Holofoil"},           // one or the other by era (probe, 2026-09-27)
	card.VariantStamped:          {"Normal", "Holofoil", "Reverse Holofoil"}, // the product's only price (probe, 2026-09-29)
}

func Config(baseURL, apiKey string, q source.Quota, timeout time.Duration) source.Config {
	return source.Config{
		Name:       Name,
		BaseURL:    baseURL,
		Header:     http.Header{"X-API-Key": {apiKey}},
		Limits:     Limits,
		Timeout:    timeout,
		Quota:      q,
		DayUsed:    dayUsed,
		HourCount:  hourCount,
		HourWindow: source.ClockHour, // spent at 23:29, full again at 00:06 (2026-09-18)
	}
}

// PokeWallet's X-RateLimit-Remaining-* headers report the count from before
// the request carrying them was counted: a fresh key's first response said
// 100 of 100 per hour and 1000 of 1000 per day. The shared client expects the
// count after the request, so both readers subtract it here.
var (
	rawHour = source.HeaderCount("X-RateLimit-Limit-Hour", "X-RateLimit-Remaining-Hour")
	rawDay  = source.HeaderCount("X-RateLimit-Limit-Day", "X-RateLimit-Remaining-Day")
)

func hourCount(h http.Header) (limit, remaining int, ok bool) {
	limit, remaining, ok = rawHour(h)
	return limit, max(remaining-1, 0), ok
}

func dayUsed(h http.Header) (int, bool) {
	limit, remaining, ok := rawDay(h)
	return min(limit-remaining+1, limit), ok
}

type Getter interface {
	GetJSON(ctx context.Context, path string, v any) error
}

type Provider struct{ get Getter }

func New(g Getter) *Provider { return &Provider{get: g} }

// codePrefix matches both prefix styles in /sets: "SV06: Twilight Masquerade"
// and "SM - Guardians Rising".
var codePrefix = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:\s+|\s+-\s+)`)

func (p *Provider) Sets(ctx context.Context) ([]card.SourceSet, error) {
	var resp struct {
		Data []struct {
			Name     string `json:"name"`
			SetID    string `json:"set_id"`
			Language string `json:"language"`
		} `json:"data"`
	}
	if err := p.get.GetJSON(ctx, "/sets", &resp); err != nil {
		return nil, fmt.Errorf("list sets: %w", err)
	}
	var out []card.SourceSet
	for _, s := range resp.Data {
		// Negative IDs are CardMarket-only sets: no TCGplayer prices, so never a match in phase 1.
		if s.Language != "eng" || strings.HasPrefix(s.SetID, "-") {
			continue
		}
		names := []string{s.Name}
		if bare := codePrefix.ReplaceAllString(s.Name, ""); bare != s.Name {
			names = append(names, bare)
		}
		out = append(out, card.SourceSet{ID: s.SetID, Names: names})
	}
	return out, nil
}

func (p *Provider) Cards(ctx context.Context, setID string) ([]card.SourceCard, error) {
	var out []card.SourceCard
	for page := 1; ; page++ {
		var resp struct {
			Cards []struct {
				ID       string `json:"id"`
				CardInfo struct {
					Name       string `json:"name"`
					CardNumber string `json:"card_number"`
				} `json:"card_info"`
			} `json:"cards"`
			Pagination struct {
				TotalPages int `json:"total_pages"`
			} `json:"pagination"`
		}
		path := fmt.Sprintf("/sets/%s?page=%d&limit=%d", url.PathEscape(setID), page, pageSize)
		if err := p.get.GetJSON(ctx, path, &resp); err != nil {
			return nil, fmt.Errorf("set %s page %d: %w", setID, page, err)
		}
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
		if page >= resp.Pagination.TotalPages {
			return out, nil
		}
	}
}

// trailingQualifier splits "Whismur (117)" into "Whismur" and "117".
var trailingQualifier = regexp.MustCompile(`^(.+?)\s+\(([^()]+)\)$`)

var digitsOnly = regexp.MustCompile(`^\d+$`)

// ownPrint lists the qualifiers PokeWallet uses for a card's own print, which
// has its own number. It is an allow-list on purpose: anything else, such as
// "(Poke Ball Pattern)", "(Black Dot Error)" or "(Pokemon Center Exclusive)",
// marks a different print that shares a number with the plain card, and gets
// no alias. New qualifiers stay unmatched, and are reported, until added here.
var ownPrint = map[string]bool{
	"full art":             true,
	"alternate full art":   true,
	"texture full art":     true,
	"secret":               true,
	"secret rare":          true,
	"secret shining":       true,
	"alternate art secret": true,
	"shiny":                true,
	"holo common":          true,
}

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

// staffMarker is PokeWallet's suffix for staff prints: "Ceruledge (Prerelease) [Staff]".
var staffMarker = regexp.MustCompile(`(?i)\s*\[staff\]$`)

// worlds matches World Championships qualifiers whatever the year, including
// the "World Championship 2025" misspelling seen live.
var worlds = regexp.MustCompile(`^world championships? \d{4}$`)

// printOf reads a product's print from its trailing qualifiers and returns the
// name without them: "Ceruledge (Prerelease) [Staff]" is PrintPrereleaseStaff
// with base "Ceruledge". Anything unrecognised is PrintStandard with the name
// unchanged, so it never matches a pattern or stamped row. A staff marker over
// an unrecognised qualifier, such as "Slowbro - 083 (Pitch Black Stamped)
// [Staff]", instead gives PrintPrereleaseStaff with the qualifier still in the
// name: bareName then refuses it an alias, so it still never matches.
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
// is padded: "Ampharos - 075" at card number "75" is "Ampharos". It tries the
// exact suffix first, so a number that doesn't end in a digit ("177a",
// "SM-P") is still trimmed; the regex only handles padding and spacing.
func trimNumber(name, number string) string {
	if trimmed := strings.TrimSuffix(name, " - "+number); trimmed != name {
		return trimmed
	}
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

func (p *Provider) Quote(ctx context.Context, sourceCardID string, variants []card.Variant) ([]card.Quote, error) {
	var resp struct {
		TCGPlayer *struct {
			URL    string `json:"url"`
			Prices []struct {
				SubType string   `json:"sub_type_name"`
				Low     *float64 `json:"low_price"`
				Market  *float64 `json:"market_price"`
				High    *float64 `json:"high_price"`
			} `json:"prices"`
		} `json:"tcgplayer"`
	}
	if err := p.get.GetJSON(ctx, "/cards/"+url.PathEscape(sourceCardID), &resp); err != nil {
		return nil, err
	}
	available := map[string]card.Price{}
	if resp.TCGPlayer != nil {
		for _, pr := range resp.TCGPlayer.Prices {
			available[pr.SubType] = card.Price{Market: pr.Market, Low: pr.Low, High: pr.High}
		}
	}
	quotes := source.Quotes(available, subtypes, variants)
	if resp.TCGPlayer != nil {
		img := artURL(resp.TCGPlayer.URL)
		for i := range quotes {
			quotes[i].Image = img
		}
	}
	return quotes, nil
}

// productID finds TCGplayer's product number in a URL such as
// https://www.tcgplayer.com/product/83475, with or without a slug after it.
var productID = regexp.MustCompile(`/product/(\d+)`)

// artURL is TCGplayer's image CDN address for the product (DD-14). It is an
// unofficial address, so the status page falls back to a plain tile.
func artURL(tcgplayerURL string) string {
	m := productID.FindStringSubmatch(tcgplayerURL)
	if m == nil {
		return ""
	}
	return "https://tcgplayer-cdn.tcgplayer.com/product/" + m[1] + "_in_1000x1000.jpg"
}
