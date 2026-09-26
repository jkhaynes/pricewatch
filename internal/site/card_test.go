package site

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

func TestActivity(t *testing.T) {
	p := func(v float64) *float64 { return &v }
	obs := func(key string, price float64, ago time.Duration) card.Observation {
		return card.Observation{CardID: key, Price: card.Price{Market: p(price)}, ObservedAt: now.Add(-ago)}
	}
	tests := []struct {
		name         string
		obs          []card.Observation
		checked, mov int
	}{
		{"nothing priced", nil, 0, 0},
		{"first price is a check, not a move", []card.Observation{obs("a", 10, time.Hour)}, 1, 0},
		{"a significant move inside the day", []card.Observation{obs("a", 10, 3*day), obs("a", 12, time.Hour)}, 1, 1},
		{"a move below the noise floor", []card.Observation{obs("a", 10, 3*day), obs("a", 10.2, time.Hour)}, 1, 0},
		{"a check older than a day is not counted", []card.Observation{obs("a", 10, 3*day), obs("a", 12, 25*time.Hour)}, 0, 0},
		{"counts each key once", []card.Observation{
			obs("a", 10, 3*day), obs("a", 12, 2*time.Hour),
			obs("b", 5, 3*day), obs("b", 5, time.Hour),
			obs("c", 1, 2*day),
		}, 2, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountActivity(byKey(tt.obs), now)
			if got.Checked != tt.checked || got.Moved != tt.mov {
				t.Errorf("got %+v, want checked %d moved %d", got, tt.checked, tt.mov)
			}
		})
	}
}

func TestNewCardView(t *testing.T) {
	base := Data{
		GeneratedAt: time.Date(2026, 9, 20, 14, 7, 0, 0, time.UTC),
		Coverage:    Coverage{Priced: 4200, Total: 4886},
		Budget:      Budget{Used: 912, Limit: 1000},
		Activity:    Activity{Checked: 1104, Moved: 37},
	}
	tests := []struct {
		name  string
		edit  func(*Data)
		check func(*testing.T, cardView)
	}{
		{"budget ring fills by share used", nil, func(t *testing.T, v cardView) {
			if v.RingDash < 206.2 || v.RingDash > 206.4 {
				t.Errorf("ring dash = %.2f, want about 206.3 of %.1f", v.RingDash, ringCircumference)
			}
		}},
		{"an overspent budget caps the ring at full", func(d *Data) { d.Budget.Used = 1200 }, func(t *testing.T, v cardView) {
			if v.RingDash != ringCircumference {
				t.Errorf("ring dash = %.2f, want %.2f", v.RingDash, ringCircumference)
			}
		}},
		{"no limit means an empty ring, not a division by zero", func(d *Data) { d.Budget.Limit = 0 }, func(t *testing.T, v cardView) {
			if v.RingDash != 0 {
				t.Errorf("ring dash = %.2f, want 0", v.RingDash)
			}
		}},
		{"numbers get thousands separators", nil, func(t *testing.T, v cardView) {
			if v.Checked != "1,104" || v.Tracked != "4,886" || v.Limit != "1,000" {
				t.Errorf("checked %q tracked %q limit %q", v.Checked, v.Tracked, v.Limit)
			}
		}},
		{"no spotlight yet", nil, func(t *testing.T, v cardView) {
			if v.Mover != nil {
				t.Errorf("mover = %+v, want nil", v.Mover)
			}
		}},
		{"a rising spotlight", func(d *Data) {
			d.Spotlight = &Mover{Name: "Mudkip", Set: "EX Ruby & Sapphire", Number: "59/109", Variant: "Reverse Holo",
				Was: 50.47, Now: 60, Percent: 18.88, History: []Point{{Price: 50.47}, {Price: 55}, {Price: 60}}}
		}, func(t *testing.T, v cardView) {
			m := v.Mover
			if m == nil || m.Title != "Mudkip · Reverse Holo" || m.Detail != "EX Ruby & Sapphire 59/109" {
				t.Fatalf("mover = %+v", m)
			}
			if m.Change != "▲ 18.9%" || !m.Up || m.Prices != "$50.47 → $60.00" {
				t.Errorf("change %q up %v prices %q", m.Change, m.Up, m.Prices)
			}
			if got := strings.Count(m.Spark, ","); got != 3 {
				t.Errorf("spark = %q, want 3 points", m.Spark)
			}
			if !strings.HasPrefix(m.Spark, "280.0,156.0") || !strings.HasSuffix(m.Spark, "473.0,136.0") {
				t.Errorf("spark = %q, want lowest price bottom-left and highest top-right", m.Spark)
			}
		}},
		{"a falling spotlight", func(d *Data) {
			d.Spotlight = &Mover{Name: "Tropius", Was: 20, Now: 15, Percent: -25}
		}, func(t *testing.T, v cardView) {
			if v.Mover.Change != "▼ 25.0%" || v.Mover.Up {
				t.Errorf("change %q up %v", v.Mover.Change, v.Mover.Up)
			}
		}},
		{"a flat history draws a level line", func(d *Data) {
			d.Spotlight = &Mover{Name: "X", Was: 5, Now: 6, Percent: 20, History: []Point{{Price: 5}, {Price: 5}}}
		}, func(t *testing.T, v cardView) {
			if v.Mover.Spark != "280.0,146.0 473.0,146.0" {
				t.Errorf("spark = %q", v.Mover.Spark)
			}
		}},
		{"long names are shortened to fit", func(d *Data) {
			d.Spotlight = &Mover{Name: strings.Repeat("Pikachu ", 10), Was: 1, Now: 2, Percent: 100}
		}, func(t *testing.T, v cardView) {
			if n := len([]rune(v.Mover.Title)); n > maxTitle || !strings.HasSuffix(v.Mover.Title, "…") {
				t.Errorf("title = %q (%d runes)", v.Mover.Title, n)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := base
			if tt.edit != nil {
				tt.edit(&d)
			}
			tt.check(t, newCardView(d))
		})
	}
}

func TestRenderCardIsWellFormedSVG(t *testing.T) {
	d := Data{
		GeneratedAt: time.Date(2026, 9, 20, 14, 7, 0, 0, time.UTC),
		Budget:      Budget{Used: 912, Limit: 1000},
		Spotlight:   &Mover{Name: `<Mudkip> & "friends"`, Set: "EX Ruby & Sapphire", Was: 1, Now: 2, Percent: 100},
	}
	var buf bytes.Buffer
	if err := RenderCard(&buf, d); err != nil {
		t.Fatal(err)
	}
	// Parsing proves every card name was escaped: one stray & or < fails here.
	dec := xml.NewDecoder(&buf)
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("card is not well-formed XML: %v", err)
		}
		if c, ok := tok.(xml.CharData); ok {
			text.Write(c)
		}
	}
	for _, want := range []string{`<Mudkip> & "friends"`, "912", "updated 14:07 UTC"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("card text is missing %q", want)
		}
	}
}
