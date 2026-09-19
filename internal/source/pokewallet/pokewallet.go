package pokewallet

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

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
			name := strings.TrimSuffix(c.CardInfo.Name, " - "+number)
			out = append(out, card.SourceCard{
				ID:      c.ID,
				Number:  local,
				Name:    name,
				Aliases: aliases(name),
			})
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

// aliases strips trailing qualifiers one at a time, as in
// "Gardevoir & Sylveon GX (205) (Alternate Full Art)". Every one must be
// allowed: a single qualifier marking a different print means no alias at all.
func aliases(name string) []string {
	base := name
	for {
		m := trailingQualifier.FindStringSubmatch(base)
		if m == nil {
			break
		}
		if q := strings.ToLower(m[2]); !digitsOnly.MatchString(q) && !ownPrint[q] {
			return nil
		}
		base = m[1]
	}
	if base == name {
		return nil
	}
	return []string{base}
}

func (p *Provider) Quote(ctx context.Context, sourceCardID string, variants []card.Variant) ([]card.Quote, error) {
	var resp struct {
		TCGPlayer *struct {
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
	return source.Quotes(available, subtypes, variants), nil
}
