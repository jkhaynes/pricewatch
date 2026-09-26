package site

import (
	_ "embed"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// Activity is the last day's work, for the profile card.
type Activity struct {
	Checked int `json:"checked"` // keys whose latest price is under a day old
	Moved   int `json:"moved"`   // of those, how many moved significantly from their previous check
}

// CountActivity reads the last 24 hours from each key's own history. A first
// price is a check but never a move, matching Today and Movers.
func CountActivity(hist map[string]series, now time.Time) Activity {
	var a Activity
	for _, s := range hist {
		latest := s[len(s)-1]
		if now.Sub(latest.ObservedAt) > day {
			continue
		}
		a.Checked++
		if len(s) >= 2 && significant(*s[len(s)-2].Market, *latest.Market) {
			a.Moved++
		}
	}
	return a
}

// The card's fixed geometry, matching card.svg.
const (
	ringCircumference = 2 * math.Pi * 36 // the budget ring's radius is 36
	sparkLeft         = 280.0
	sparkRight        = 473.0
	sparkTop          = 136.0
	sparkBottom       = 156.0
	maxTitle          = 34 // runes that fit the 193px mover column at 11px
	maxDetail         = 34
)

// cardView is Data reduced to the strings and numbers card.svg prints, so the
// template holds layout only and every decision is testable here.
type cardView struct {
	RingDash, RingGap float64
	Used, Limit       string
	Checked, Moved    string
	Tracked, Priced   string
	Updated           string
	Mover             *moverView
}

type moverView struct {
	Title, Detail, Prices, Change string
	Up                            bool
	Spark                         string // polyline points; empty draws no line
	EndX, EndY                    float64
}

func newCardView(d Data) cardView {
	v := cardView{
		Used:    thousands(d.Budget.Used),
		Limit:   thousands(d.Budget.Limit),
		Checked: thousands(d.Activity.Checked),
		Moved:   thousands(d.Activity.Moved),
		Tracked: thousands(d.Coverage.Total),
		Priced:  thousands(d.Coverage.Priced),
		Updated: d.GeneratedAt.UTC().Format("15:04"),
	}
	if d.Budget.Limit > 0 {
		v.RingDash = ringCircumference * min(float64(d.Budget.Used)/float64(d.Budget.Limit), 1)
	}
	v.RingGap = ringCircumference
	if m := d.Spotlight; m != nil {
		v.Mover = newMoverView(*m)
	}
	return v
}

func newMoverView(m Mover) *moverView {
	mv := &moverView{
		Title:  shorten(joinNonEmpty(" · ", m.Name, m.Variant), maxTitle),
		Detail: shorten(joinNonEmpty(" ", m.Set, m.Number), maxDetail),
		Prices: fmt.Sprintf("$%.2f → $%.2f", m.Was, m.Now),
		Up:     m.Percent >= 0,
	}
	arrow := "▲"
	if !mv.Up {
		arrow = "▼"
	}
	mv.Change = fmt.Sprintf("%s %.1f%%", arrow, math.Abs(m.Percent))
	mv.Spark, mv.EndX, mv.EndY = spark(m.History)
	return mv
}

// spark scales prices into the sparkline box: oldest on the left, the highest
// price at the top. A flat series sits on the middle line.
func spark(h []Point) (points string, endX, endY float64) {
	if len(h) < 2 {
		return "", 0, 0
	}
	lo, hi := h[0].Price, h[0].Price
	for _, p := range h {
		lo, hi = min(lo, p.Price), max(hi, p.Price)
	}
	pts := make([]string, len(h))
	for i, p := range h {
		x := sparkLeft + (sparkRight-sparkLeft)*float64(i)/float64(len(h)-1)
		y := (sparkTop + sparkBottom) / 2
		if hi > lo {
			y = sparkBottom - (sparkBottom-sparkTop)*(p.Price-lo)/(hi-lo)
		}
		pts[i] = fmt.Sprintf("%.1f,%.1f", x, y)
		endX, endY = x, y
	}
	return strings.Join(pts, " "), endX, endY
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// shorten cuts s to at most n runes, ending in an ellipsis when it cuts.
// It counts runes, not bytes, so "é" in "Pokémon" is one character.
func shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func joinNonEmpty(sep string, parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, sep)
}

//go:embed card.svg
var cardSVG string

// text/template, not html/template: the output is SVG, and html/template's
// contextual escaping is built for HTML. Every string from the database goes
// through xml instead, so a card name cannot break the markup.
var cardTmpl = template.Must(template.New("card").Funcs(template.FuncMap{"xml": xmlEscape}).Parse(cardSVG))

// RenderCard writes the profile card: a small, self-contained SVG meant to be
// embedded in a GitHub README (DD-14's derived numbers only).
func RenderCard(w io.Writer, d Data) error {
	if err := cardTmpl.Execute(w, newCardView(d)); err != nil {
		return fmt.Errorf("render card: %w", err)
	}
	return nil
}

var xmlReplacer = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

func xmlEscape(s string) string { return xmlReplacer.Replace(s) }
