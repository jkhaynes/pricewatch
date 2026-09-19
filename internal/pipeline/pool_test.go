package pipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

// slowSource takes one (fake) second per request and tracks peak concurrency.
type slowSource struct {
	calls, inFlight, peak atomic.Int32
}

func (s *slowSource) Quote(ctx context.Context, _ string, variants []card.Variant) ([]card.Quote, error) {
	s.calls.Add(1)
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	select {
	case <-time.After(time.Second):
		m := 1.0
		qs := make([]card.Quote, len(variants))
		for i, v := range variants {
			qs[i] = card.Quote{Variant: v, Price: card.Price{Market: &m}}
		}
		return qs, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func jobs(n int) []Job {
	out := make([]Job, n)
	for i := range out {
		id := string(rune('a' + i))
		out[i] = Job{SourceCardID: id, Targets: []Target{{Key: "key-" + id, Variant: card.VariantNormal}}}
	}
	return out
}

func collect(ch <-chan Result) []Result {
	var out []Result
	for r := range ch {
		out = append(out, r)
	}
	return out
}

func TestWorkProcessesEveryJobWithBoundedConcurrency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &slowSource{}
		res := collect(Work(t.Context(), src, Feed(t.Context(), nil, jobs(10)), 3))
		if len(res) != 10 {
			t.Fatalf("got %d results, want 10", len(res))
		}
		for _, r := range res {
			if r.Err != nil || len(r.Quotes) != len(r.Job.Targets) {
				t.Errorf("bad result %+v", r)
			}
		}
		if p := src.peak.Load(); p != 3 {
			t.Errorf("peak concurrency %d, want 3", p)
		}
	})
}

func TestStopFinishesInFlightAndDispatchesNoMore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &slowSource{}
		stop := make(chan struct{})
		out := Work(t.Context(), src, Feed(t.Context(), stop, jobs(10)), 2)

		time.Sleep(500 * time.Millisecond) // two jobs in flight
		close(stop)
		res := collect(out)

		if int(src.calls.Load()) != len(res) {
			t.Fatalf("calls=%d results=%d: a started job lost its result", src.calls.Load(), len(res))
		}
		if len(res) >= 10 {
			t.Fatalf("stop did not stop dispatch: %d results", len(res))
		}
		for _, r := range res {
			if r.Err != nil {
				t.Errorf("soft stop must let in-flight work finish, got %v", r.Err)
			}
		}
	})
}

func TestCancelAbandonsInFlightWithExplicitResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &slowSource{}
		ctx, cancel := context.WithCancel(t.Context())
		out := Work(ctx, src, Feed(ctx, nil, jobs(10)), 2)

		time.Sleep(500 * time.Millisecond)
		cancel()
		res := collect(out)

		if int(src.calls.Load()) != len(res) {
			t.Fatalf("calls=%d results=%d", src.calls.Load(), len(res))
		}
		var abandoned int
		for _, r := range res {
			if errors.Is(r.Err, context.Canceled) {
				abandoned++
			}
		}
		if abandoned == 0 {
			t.Error("expected in-flight jobs to report context.Canceled")
		}
	})
}
