package tcgcollector

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jkhaynes/pricewatch/internal/card"
)

type field int

const (
	fRegion field = iota
	fName
	fNumber
	fSort
	fExpansion
	fRarity
	fVariant
	fLanguage
	fCondition
	fQuantity
	fPrice
	fNote
)

// headerNames are the real export's column names (Task 0). "Total price" is
// quantity x price, so it is deliberately not read.
var headerNames = map[field]string{
	fRegion: "TCG region", fName: "Card name", fNumber: "Card number", fSort: "Card number sorting order",
	fExpansion: "Expansion", fRarity: "Rarity", fVariant: "Card variant", fLanguage: "Card language",
	fCondition: "Card condition", fQuantity: "Quantity", fPrice: "Price", fNote: "Note",
}

var required = []field{fRegion, fName, fNumber, fExpansion, fVariant}

func Parse(r io.Reader) ([]card.Row, []error, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows; missing trailing cells read as ""

	head, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read header: %w", err)
	}
	col := map[field]int{}
	for i, h := range head {
		h = strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF"))
		for f, name := range headerNames {
			if strings.EqualFold(h, name) {
				col[f] = i
			}
		}
	}
	var missing []string
	for _, f := range required {
		if _, ok := col[f]; !ok {
			missing = append(missing, headerNames[f])
		}
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("missing required columns: %s", strings.Join(missing, ", "))
	}

	var rows []card.Row
	var rowErrs []error
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line, _ := cr.FieldPos(0)
		if err != nil {
			rowErrs = append(rowErrs, fmt.Errorf("line %d: %w", line, err))
			continue
		}
		row, err := toRow(rec, col)
		if err != nil {
			rowErrs = append(rowErrs, fmt.Errorf("line %d: %w", line, err))
			continue
		}
		rows = append(rows, row)
	}
	return rows, rowErrs, nil
}

func toRow(rec []string, col map[field]int) (card.Row, error) {
	get := func(f field) string {
		i, ok := col[f]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	for _, f := range required {
		if get(f) == "" {
			return card.Row{}, fmt.Errorf("empty %s", headerNames[f])
		}
	}
	row := card.Row{
		Region: get(fRegion), Name: get(fName), Number: get(fNumber), Expansion: get(fExpansion),
		Rarity: get(fRarity), Variant: get(fVariant), Language: get(fLanguage),
		Condition: get(fCondition), Note: get(fNote), Quantity: 1,
	}
	if s := get(fQuantity); s != "" {
		q, err := strconv.Atoi(s)
		if err != nil {
			return card.Row{}, fmt.Errorf("quantity %q: %w", s, err)
		}
		row.Quantity = q
	}
	if s := get(fSort); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return card.Row{}, fmt.Errorf("sort number %q: %w", s, err)
		}
		row.SortNumber = &n
	}
	if s := strings.NewReplacer("$", "", ",", "").Replace(get(fPrice)); s != "" {
		p, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return card.Row{}, fmt.Errorf("price %q: %w", s, err)
		}
		row.Price = &p
	}
	return row, nil
}
