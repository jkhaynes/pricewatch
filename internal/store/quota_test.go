package store

import (
	"testing"

	"github.com/jkhaynes/pricewatch/internal/source"
)

// Compile-time proof that the store fits the HTTP client's consumer-side interface.
var _ source.Quota = (*SQLite)(nil)

func TestQuota(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	steps := []struct {
		name string
		op   func() error
		want int
	}{
		{"empty day is zero", func() error { return nil }, 0},
		{"add", func() error { return s.QuotaAdd(ctx, "pw", "2026-09-18", 1) }, 1},
		{"add again", func() error { return s.QuotaAdd(ctx, "pw", "2026-09-18", 2) }, 3},
		{"server says otherwise", func() error { return s.QuotaSet(ctx, "pw", "2026-09-18", 10) }, 10},
	}
	for _, st := range steps {
		if err := st.op(); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		got, err := s.QuotaUsed(ctx, "pw", "2026-09-18")
		if err != nil || got != st.want {
			t.Errorf("%s: used = %d, %v; want %d", st.name, got, err, st.want)
		}
	}
	if other, _ := s.QuotaUsed(ctx, "pw", "2026-09-19"); other != 0 {
		t.Errorf("next day used = %d, want 0", other)
	}
	if other, _ := s.QuotaUsed(ctx, "tcgdex", "2026-09-18"); other != 0 {
		t.Errorf("other source used = %d, want 0", other)
	}
}
