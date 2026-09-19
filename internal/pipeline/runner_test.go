package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/priority"
)

// memStore honours ctx like a real database would: a cancelled ctx fails writes.
type memStore struct {
	mu       sync.Mutex
	due      []card.Mapping
	notDue   []card.Candidate
	saved    []card.Observation
	mappings []card.Mapping
	finished bool
	ok, fail int
	saveErr  error
	art      map[string]string // source card ID -> image URL
	artCalls int
}

func (m *memStore) StartRun(ctx context.Context) (int64, error) { return 1, ctx.Err() }

// Candidates turns the due mappings into never-priced candidates, grouped by
// source card as the real store does, and adds any not-yet-due ones.
func (m *memStore) Candidates(ctx context.Context, _ string) ([]card.Candidate, error) {
	var out []card.Candidate
	for _, mp := range m.due {
		if n := len(out); n == 0 || out[n-1].SourceCardID != mp.SourceCardID {
			out = append(out, card.Candidate{SourceCardID: mp.SourceCardID, NeverSeen: true})
		}
		out[len(out)-1].Keys = append(out[len(out)-1].Keys, mp)
	}
	return append(out, m.notDue...), ctx.Err()
}
func (m *memStore) Save(ctx context.Context, _ int64, o card.Observation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.saveErr != nil {
		return m.saveErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved = append(m.saved, o)
	return nil
}
func (m *memStore) PutMapping(ctx context.Context, mp card.Mapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mappings = append(m.mappings, mp)
	return ctx.Err()
}
func (m *memStore) FinishRun(ctx context.Context, _ int64, ok, failed int) error {
	m.finished, m.ok, m.fail = true, ok, failed
	return ctx.Err()
}
func (m *memStore) Changes(ctx context.Context, _ int64) ([]card.Change, error) {
	return nil, ctx.Err()
}

// mapping builds a resolved mapping; key is "<sourceID>/<variant>".
func mapping(sourceID string, v card.Variant) card.Mapping {
	return card.Mapping{Key: sourceID + "/" + string(v), SourceCardID: sourceID, Variant: v, Status: card.StatusResolved}
}

// scriptSource: requestErr fails the whole request; variantErr fails one variant.
type scriptSource struct {
	requestErr map[string]error
	variantErr map[string]error // key "<sourceID>/<variant>"
	short      map[string]bool  // return one quote too few
	image      string           // set on every quote
	calls      atomic.Int32
}

func (s *scriptSource) Quote(ctx context.Context, id string, variants []card.Variant) ([]card.Quote, error) {
	s.calls.Add(1)
	if err := s.requestErr[id]; err != nil {
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	m := 1.0
	var qs []card.Quote
	for _, v := range variants {
		q := card.Quote{Variant: v, Price: card.Price{Market: &m}, Image: s.image}
		if err := s.variantErr[id+"/"+string(v)]; err != nil {
			q = card.Quote{Variant: v, Err: fmt.Errorf("%s: %w", v, err)}
		}
		qs = append(qs, q)
	}
	if s.short[id] {
		qs = qs[:len(qs)-1]
	}
	return qs, nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRunClassifiesEveryOutcomeAndNeverAborts(t *testing.T) {
	st := &memStore{due: []card.Mapping{
		mapping("ok", card.VariantNormal), mapping("ok", card.VariantReverseHolo), // one request, two keys
		mapping("part", card.VariantNormal), mapping("part", card.VariantHolo), // one variant priced, one not
		mapping("amb", card.VariantHolo),
		mapping("gone", card.VariantNormal), mapping("gone", card.VariantReverseHolo), // 404: both keys unmatched
		mapping("boom", card.VariantNormal),
		mapping("short", card.VariantNormal),
	}}
	src := &scriptSource{
		requestErr: map[string]error{"gone": card.ErrNotFound, "boom": errors.New("connection reset")},
		variantErr: map[string]error{"part/holo": card.ErrVariantUnavailable, "amb/holo": card.ErrVariantAmbiguous},
		short:      map[string]bool{"short": true},
	}
	r := &Runner{Store: st, Source: src, SourceName: "pw", Budget: 100, Workers: 3, Log: quiet}

	sum, err := r.Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Requests != 6 || sum.Keys != 9 || sum.OK != 3 || sum.Failed != 6 || sum.Deferred != 0 {
		t.Errorf("summary = %+v", sum)
	}
	status := map[string]card.Status{}
	for _, m := range st.mappings {
		status[m.Key] = m.Status
		if m.Reason == "" {
			t.Errorf("mapping %s has no reason", m.Key)
		}
	}
	want := map[string]card.Status{
		"part/holo": card.StatusUnmatched, "amb/holo": card.StatusAmbiguous,
		"gone/normal": card.StatusUnmatched, "gone/reverse": card.StatusUnmatched,
	}
	if len(status) != len(want) || len(sum.NewlyUnresolved) != len(want) {
		t.Errorf("mappings = %v; generic and contract errors must not touch card_map", status)
	}
	for k, v := range want {
		if status[k] != v {
			t.Errorf("mapping %s = %q, want %q", k, status[k], v)
		}
	}
	if !st.finished || st.ok != 3 || st.fail != 6 {
		t.Errorf("FinishRun ok=%d fail=%d finished=%v", st.ok, st.fail, st.finished)
	}
}

func TestRunBudgetCountsRequestsNotKeys(t *testing.T) {
	st := &memStore{due: []card.Mapping{
		mapping("a", card.VariantNormal), mapping("a", card.VariantReverseHolo),
		mapping("b", card.VariantNormal), mapping("c", card.VariantNormal),
	}}
	src := &scriptSource{}
	r := &Runner{Store: st, Source: src, SourceName: "pw", Budget: 2, Workers: 4, Log: quiet}
	sum, err := r.Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if src.calls.Load() != 2 || sum.Keys != 3 || len(st.saved) != 3 {
		t.Errorf("calls=%d keys=%d saved=%d, want 2/3/3", src.calls.Load(), sum.Keys, len(st.saved))
	}
}

func TestRateLimitStopsDispatchAndBlamesNoCard(t *testing.T) {
	st := &memStore{due: []card.Mapping{
		mapping("a", card.VariantNormal), mapping("b", card.VariantNormal), mapping("c", card.VariantNormal),
		mapping("d", card.VariantNormal), mapping("e", card.VariantNormal),
	}}
	// Like the real server: once limited, every later request is limited too.
	src := &limitAfter{n: 1}
	r := &Runner{Store: st, Source: src, SourceName: "pw", Budget: 10, Workers: 1, Log: quiet}

	sum, err := r.Run(t.Context(), nil)
	if err != nil {
		t.Fatalf("a rate limit must end the run cleanly, got %v", err)
	}
	if !errors.Is(sum.StoppedBy, card.ErrRateLimited) {
		t.Errorf("StoppedBy = %v", sum.StoppedBy)
	}
	if sum.OK != 1 || sum.Failed != 0 || sum.Deferred != 4 || len(st.mappings) != 0 {
		t.Errorf("summary = %+v, mappings = %v", sum, st.mappings)
	}
	if src.calls.Load() >= 5 {
		t.Errorf("dispatch did not stop: %d calls", src.calls.Load())
	}
}

type limitAfter struct {
	n     int32
	calls atomic.Int32
}

func (l *limitAfter) Quote(ctx context.Context, id string, variants []card.Variant) ([]card.Quote, error) {
	if l.calls.Add(1) > l.n {
		return nil, fmt.Errorf("GET /cards/%s: %w", id, card.ErrRateLimited)
	}
	return (&scriptSource{}).Quote(ctx, id, variants)
}

func TestSaveFailureIsCountedNotFatal(t *testing.T) {
	st := &memStore{due: []card.Mapping{mapping("a", card.VariantNormal), mapping("b", card.VariantNormal)}, saveErr: errors.New("disk full")}
	r := &Runner{Store: st, Source: &scriptSource{}, SourceName: "pw", Budget: 10, Workers: 1, Log: quiet}
	sum, err := r.Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.OK != 0 || sum.Failed != 2 {
		t.Errorf("summary = %+v", sum)
	}
}

func TestHardCancelStillPersistsCompletedWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var due []card.Mapping
		for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
			due = append(due, mapping(id, card.VariantNormal))
		}
		st := &memStore{due: due}
		ctx, cancel := context.WithCancel(t.Context())
		r := &Runner{Store: st, Source: &slowSource{}, SourceName: "pw", Budget: 10, Workers: 2, Log: quiet}

		done := make(chan Summary)
		go func() {
			sum, err := r.Run(ctx, nil)
			if err != nil {
				t.Error(err)
			}
			done <- sum
		}()
		time.Sleep(1500 * time.Millisecond) // 2 finished, 2 in flight
		cancel()
		sum := <-done

		if sum.OK != 2 || len(st.saved) != 2 {
			t.Errorf("completed work lost: OK=%d saved=%d", sum.OK, len(st.saved))
		}
		// At least the 2 in flight. Feed can hand out one more job in the instant
		// after cancel (select picks randomly among ready cases); it fails fast and is abandoned too.
		if sum.Abandoned < 2 || sum.OK+sum.Abandoned+sum.Deferred != sum.Keys {
			t.Errorf("summary = %+v", sum)
		}
		if !st.finished {
			t.Error("run row not closed after cancel")
		}
	})
}

func TestRunPricesOnlyDueCardsAndStopsEarly(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	st := &memStore{
		due: []card.Mapping{mapping("new", card.VariantNormal)},
		notDue: []card.Candidate{{SourceCardID: "fresh", Keys: []card.Mapping{mapping("fresh", card.VariantNormal)},
			LastChecked: now.Add(-time.Hour), Value: 400}}, // checked an hour ago: not due for a day
	}
	src := &scriptSource{}
	r := &Runner{Store: st, Source: src, SourceName: "pw", Budget: 100, Workers: 2, Log: quiet,
		Policy: priority.Default, Now: func() time.Time { return now }}
	sum, err := r.Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if src.calls.Load() != 1 || sum.Requests != 1 || sum.NotDue != 1 || sum.Deferred != 0 {
		t.Errorf("calls=%d summary=%+v; want only the due card priced and the fresh one counted as not due", src.calls.Load(), sum)
	}
	if len(st.saved) != 1 || !st.saved[0].ObservedAt.Equal(now) {
		t.Errorf("saved = %+v; observations must carry the runner's clock", st.saved)
	}
}

func (m *memStore) PutArt(ctx context.Context, _ string, sourceCardID, url string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.art == nil {
		m.art = map[string]string{}
	}
	m.art[sourceCardID] = url
	m.artCalls++
	return ctx.Err()
}

func TestRunStoresCardArtOncePerRequest(t *testing.T) {
	tests := []struct {
		name      string
		image     string
		wantCalls int
	}{
		{"source reports art", "https://img.example/a.jpg", 2},
		{"source reports none", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &memStore{due: []card.Mapping{
				mapping("a", card.VariantNormal), mapping("a", card.VariantReverseHolo), // one request, two keys
				mapping("b", card.VariantNormal),
			}}
			r := &Runner{Store: st, Source: &scriptSource{image: tt.image}, SourceName: "pw", Budget: 10, Workers: 1, Log: quiet}
			if _, err := r.Run(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			if st.artCalls != tt.wantCalls || (tt.image != "" && st.art["a"] != tt.image) {
				t.Errorf("PutArt calls = %d, art = %v; want %d calls, once per request", st.artCalls, st.art, tt.wantCalls)
			}
		})
	}
}
