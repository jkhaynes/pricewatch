package site

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

//go:embed page.html
var page string

const placeholder = "{{DATA}}"

// Render writes the status page with d embedded as JSON. encoding/json escapes
// <, > and & inside strings by default, so no card name can close the
// script element that holds the data.
func Render(w io.Writer, d Data) error {
	b, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode page data: %w", err)
	}
	if _, err := io.WriteString(w, strings.Replace(page, placeholder, string(b), 1)); err != nil {
		return fmt.Errorf("write page: %w", err)
	}
	return nil
}
