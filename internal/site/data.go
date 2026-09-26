// Package site builds pricewatch's public status page (DD-14): pure functions
// turn the database into Data, and Render embeds it in a static page.
package site

import "time"

const day = 24 * time.Hour

// Data is everything the page shows. It is published, so it holds derived
// numbers only: no quantities, no export prices, no collection total.
type Data struct {
	GeneratedAt     time.Time `json:"generatedAt"`
	RunMinute       int       `json:"runMinute"` // the schedule's minute past each hour, for "next run in"
	Runs            []bool    `json:"runs"`      // the last 24 hourly slots, oldest first: did a run finish in it
	Index           Index     `json:"index"`
	Coverage        Coverage  `json:"coverage"`
	Spotlight       *Mover    `json:"spotlight"` // nil until some card has a week of history
	Rising          []Mover   `json:"rising"`
	Falling         []Mover   `json:"falling"`
	Today           []Mover   `json:"today"`
	Bands           []Band    `json:"bands"`
	Unresolved      []Group   `json:"unresolved"`
	UnresolvedTotal int       `json:"unresolvedTotal"`
	Budget          Budget    `json:"budget"`
	Activity        Activity  `json:"activity"` // the last day, for the profile card
}

// Index is the value-weighted price change in percent; nil means no card has
// a baseline that old yet.
type Index struct {
	Week  *float64 `json:"week"`
	Month *float64 `json:"month"`
}

type Coverage struct {
	Priced     int     `json:"priced"`     // keys with at least one market price
	Total      int     `json:"total"`      // all collection keys
	ValueShare float64 `json:"valueShare"` // share of export value that is priced, 0 to 1
}

type Mover struct {
	key     string
	Name    string  `json:"name"`
	Set     string  `json:"set"`
	Number  string  `json:"number"`
	Variant string  `json:"variant"`
	Image   string  `json:"image,omitempty"`
	Was     float64 `json:"was"`
	Now     float64 `json:"now"`
	Percent float64 `json:"percent"`
	History []Point `json:"history,omitempty"` // the spotlight's checks over the last 30 days
}

type Point struct {
	At    time.Time `json:"at"`
	Price float64   `json:"price"`
}

// Band is one DD-12 tier on the freshness map.
type Band struct {
	Label     string `json:"label"`
	EveryDays int    `json:"everyDays"`
	Due       int    `json:"due"`
	Tiles     []Tile `json:"tiles"`
}

// Tile is one source card. Short JSON names keep about 5,000 tiles small.
type Tile struct {
	Name   string   `json:"n"`
	Set    string   `json:"s"`
	Number string   `json:"no"`
	Ratio  *float64 `json:"r"` // share of the tier's interval used; nil = never priced
}

type Group struct {
	Label string `json:"label"`
	Hint  string `json:"hint"`
	Count int    `json:"count"`
}

type Budget struct {
	Used    int   `json:"used"`
	Limit   int   `json:"limit"`
	PerHour []int `json:"perHour"` // the last 24 hourly slots, oldest first
}
