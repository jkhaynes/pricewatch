package pokewallet

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/source"
)

// TestLive spends 5 real requests. It runs only when asked:
//
//	$env:PRICEWATCH_LIVE=1; go test ./internal/source/pokewallet -run TestLive -v
func TestLive(t *testing.T) {
	key := os.Getenv("POKEWALLET_API_KEY")
	if os.Getenv("PRICEWATCH_LIVE") != "1" || key == "" {
		t.Skip("set PRICEWATCH_LIVE=1 and POKEWALLET_API_KEY to run")
	}
	p := New(source.New(Config(DefaultBaseURL, key, nil, 15*time.Second)))
	ctx := t.Context()

	sets, err := p.Sets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(sets, func(s card.SourceSet) bool { return s.ID == "1393" }) {
		t.Fatal("Ruby and Sapphire (1393) not in /sets")
	}
	cards, err := p.Cards(ctx, "1393")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(cards, func(c card.SourceCard) bool { return c.Number == "59" && c.Name == "Mudkip" })
	if i < 0 {
		t.Fatalf("Mudkip 59 not found among %d cards", len(cards))
	}
	qs, err := p.Quote(ctx, cards[i].ID, []card.Variant{card.VariantNormal, card.VariantReverseHolo})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range qs {
		if q.Err != nil || q.Price.Market == nil {
			t.Errorf("%s: %+v", q.Variant, q)
		}
	}
	t.Logf("Mudkip 59/109: normal=%v reverse=%v", *qs[0].Price.Market, *qs[1].Price.Market)
}
