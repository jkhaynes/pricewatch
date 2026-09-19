package source

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/jkhaynes/pricewatch/internal/card"
)

type memQuota struct {
	mu   sync.Mutex
	used map[string]int
}

func newMemQuota() *memQuota { return &memQuota{used: map[string]int{}} }

func (q *memQuota) QuotaUsed(_ context.Context, src, day string) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.used[src+day], nil
}
func (q *memQuota) QuotaAdd(_ context.Context, src, day string, n int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.used[src+day] += n
	return nil
}
func (q *memQuota) QuotaSet(_ context.Context, src, day string, used int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.used[src+day] = used
	return nil
}

func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestGetJSONStatusMapping(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
		wantSE  int
	}{
		{"ok", 200, `{"v":1}`, nil, 0},
		{"404", 404, `{}`, card.ErrNotFound, 0},
		{"429", 429, `{"error":"Rate limit exceeded","message":"Hourly limit exceeded"}`, card.ErrRateLimited, 0},
		{"503", 503, ``, nil, 503},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-API-Key") != "secret" {
					t.Errorf("auth header missing")
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			})
			c := New(Config{Name: "pw", BaseURL: base, Header: http.Header{"X-API-Key": {"secret"}}, Timeout: time.Second})
			var v struct{ V int }
			err := c.GetJSON(t.Context(), "/x", &v)
			var se *StatusError
			switch {
			case tt.wantSE != 0:
				if !errors.As(err, &se) || se.Code != tt.wantSE {
					t.Fatalf("err = %v, want StatusError %d", err, tt.wantSE)
				}
			case !errors.Is(err, tt.wantErr):
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.status == 200 && v.V != 1 {
				t.Errorf("decoded %+v", v)
			}
		})
	}
}

func TestDailyQuotaStopsBeforeRequesting(t *testing.T) {
	var hits atomic.Int32
	base := serve(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte(`{}`)) })
	q := newMemQuota()
	c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerDay: 2}, Quota: q, Timeout: time.Second})
	for i := range 3 {
		err := c.GetJSON(t.Context(), "/x", &struct{}{})
		if i < 2 && err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if i == 2 && !errors.Is(err, card.ErrQuotaExhausted) {
			t.Fatalf("call 3: err = %v, want ErrQuotaExhausted", err)
		}
	}
	if hits.Load() != 2 {
		t.Errorf("server hit %d times, want 2", hits.Load())
	}
}

func TestQuotaSyncsToServerHeaders(t *testing.T) {
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit-Day", "1000")
		w.Header().Set("X-RateLimit-Remaining-Day", "990")
		w.Write([]byte(`{}`))
	})
	q := newMemQuota()
	c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerDay: 1000}, Quota: q, Timeout: time.Second,
		DayUsed: HeaderDayUsed("X-RateLimit-Limit-Day", "X-RateLimit-Remaining-Day")})
	if err := c.GetJSON(t.Context(), "/x", &struct{}{}); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Format(time.DateOnly)
	if used, _ := q.QuotaUsed(t.Context(), "pw", day); used != 10 {
		t.Errorf("used = %d, want 10 (server truth)", used)
	}
}

func TestPerRequestTimeout(t *testing.T) {
	base := serve(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	c := New(Config{Name: "pw", BaseURL: base, Timeout: 50 * time.Millisecond})
	start := time.Now()
	err := c.GetJSON(t.Context(), "/x", &struct{}{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("timeout not applied, took %v", time.Since(start))
	}
}

func TestRateIsHonoured(t *testing.T) {
	base := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerSecond: 20}, Timeout: time.Second})
	start := time.Now()
	for range 5 {
		if err := c.GetJSON(t.Context(), "/x", &struct{}{}); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 190*time.Millisecond {
		t.Errorf("5 requests at 20/s took %v; limiter not applied", el)
	}
}

func TestCancelledContextMakesNoRequest(t *testing.T) {
	base := serve(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerSecond: 1}, Timeout: time.Second})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.GetJSON(ctx, "/x", &struct{}{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
}

func TestLimitFor(t *testing.T) {
	tests := []struct {
		l    Limits
		want rate.Limit
	}{
		{Limits{}, rate.Inf},
		{Limits{PerSecond: 2}, 2},
		{Limits{PerHour: 3600}, 1},
		{Limits{PerSecond: 5, PerHour: 100}, rate.Limit(100.0 / 3600)}, // the stricter wins
		{Limits{PerDay: 1000}, rate.Inf},                               // daily is quota, not pace
	}
	for _, tt := range tests {
		if got := limitFor(tt.l); got != tt.want {
			t.Errorf("limitFor(%+v) = %v, want %v", tt.l, got, tt.want)
		}
	}
}

func hourHeaders(w http.ResponseWriter, remaining int) {
	w.Header().Set("X-RateLimit-Limit-Hour", "100")
	w.Header().Set("X-RateLimit-Remaining-Hour", strconv.Itoa(remaining))
}

var hourCount = HeaderCount("X-RateLimit-Limit-Hour", "X-RateLimit-Remaining-Hour")

func TestBurstsWhileServerReportsRequestsLeft(t *testing.T) {
	var hits atomic.Int32
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hourHeaders(w, 100-int(hits.Add(1)))
		w.Write([]byte(`{}`))
	})
	c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerHour: 100}, Timeout: time.Second, HourCount: hourCount})
	start := time.Now()
	for range 10 {
		if err := c.GetJSON(t.Context(), "/x", &struct{}{}); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("10 requests took %v; with requests left they should burst, not wait 36 s each", el)
	}
}

func TestWaitsOutTheWindowWhenServerSaysNoneLeft(t *testing.T) {
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hourHeaders(w, 0)
		w.Write([]byte(`{}`))
	})
	var logs bytes.Buffer
	c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerHour: 100}, Timeout: time.Second, HourCount: hourCount,
		Log: slog.New(slog.NewTextHandler(&logs, nil))})
	c.hour = newHourly(300 * time.Millisecond) // a short "hour" for the test
	if err := c.GetJSON(t.Context(), "/x", &struct{}{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := c.GetJSON(t.Context(), "/x", &struct{}{}); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 200*time.Millisecond {
		t.Errorf("second request went after %v; it should have waited for the window", el)
	}
	if !strings.Contains(logs.String(), "waiting for the hourly window") {
		t.Errorf("a long pause must be logged, got %q", logs.String())
	}
}

func TestWaitingForTheWindowHonoursCancellation(t *testing.T) {
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hourHeaders(w, 0)
		w.Write([]byte(`{}`))
	})
	c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerHour: 100}, Timeout: time.Second, HourCount: hourCount})
	if err := c.GetJSON(t.Context(), "/x", &struct{}{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.GetJSON(ctx, "/x", &struct{}{})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v; Ctrl-C must interrupt the hour-long wait", err, time.Since(start))
	}
}

func TestPacing(t *testing.T) {
	count := func(http.Header) (int, int, bool) { return 0, 0, false }
	tests := []struct {
		name string
		cfg  Config
		want rate.Limit
	}{
		{"no hourly headers: even spacing as before", Config{Limits: Limits{PerHour: 3600}}, 1},
		{"hourly headers: only the politeness cap", Config{Limits: Limits{PerSecond: 2, PerHour: 100}, HourCount: count}, 2},
		{"hourly headers and no cap", Config{Limits: Limits{PerHour: 100}, HourCount: count}, rate.Inf},
	}
	for _, tt := range tests {
		if got := pacing(tt.cfg); got != tt.want {
			t.Errorf("%s: pacing = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A 429 on a run's very first request, before any count is known, used to stop
// the run. If its headers say the hour is spent while the day is not, the
// client now waits out the window and retries.
func TestHourly429IsWaitedOutAndRetried(t *testing.T) {
	var hits atomic.Int32
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			hourHeaders(w, 0)
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"Rate limit exceeded","message":"Hourly limit exceeded"}`))
			return
		}
		hourHeaders(w, 99)
		w.Write([]byte(`{"v":1}`))
	})
	c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerHour: 100}, Timeout: time.Second, HourCount: hourCount})
	c.hour = newHourly(200 * time.Millisecond) // a short "hour" for the test
	start := time.Now()
	var v struct{ V int }
	if err := c.GetJSON(t.Context(), "/x", &v); err != nil {
		t.Fatalf("an hourly 429 should be waited out, got %v", err)
	}
	if hits.Load() != 2 || v.V != 1 {
		t.Errorf("hits = %d, v = %+v; want the request retried once and decoded", hits.Load(), v)
	}
	if el := time.Since(start); el < 150*time.Millisecond {
		t.Errorf("retried after %v; it must wait for the window first", el)
	}
}

func TestA429ThatIsNotHourlyStillStops(t *testing.T) {
	tests := []struct {
		name   string
		header func(w http.ResponseWriter)
	}{
		{"the daily allowance is spent", func(w http.ResponseWriter) {
			hourHeaders(w, 0)
			w.Header().Set("X-RateLimit-Limit-Day", "1000")
			w.Header().Set("X-RateLimit-Remaining-Day", "0")
		}},
		{"no counts at all", func(w http.ResponseWriter) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			base := serve(t, func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				tt.header(w)
				w.WriteHeader(http.StatusTooManyRequests)
			})
			c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerHour: 100, PerDay: 1000}, Timeout: time.Second,
				Quota: newMemQuota(), HourCount: hourCount, DayUsed: HeaderDayUsed("X-RateLimit-Limit-Day", "X-RateLimit-Remaining-Day")})
			c.hour = newHourly(50 * time.Millisecond)
			err := c.GetJSON(t.Context(), "/x", &struct{}{})
			if !errors.Is(err, card.ErrRateLimited) || hits.Load() != 1 {
				t.Errorf("err = %v after %d requests; want ErrRateLimited with no retry", err, hits.Load())
			}
		})
	}
}

func TestHourlyRetriesAreCapped(t *testing.T) {
	var hits atomic.Int32
	base := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		hourHeaders(w, 0)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerHour: 100}, Timeout: time.Second, HourCount: hourCount})
	c.hour = newHourly(20 * time.Millisecond)
	err := c.GetJSON(t.Context(), "/x", &struct{}{})
	if !errors.Is(err, card.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited once retries run out", err)
	}
	if got, want := int(hits.Load()), 1+maxHourlyRetries; got != want {
		t.Errorf("sent %d requests, want %d (one plus %d retries)", got, want, maxHourlyRetries)
	}
}

func TestNewPicksTheSourcesHourWindow(t *testing.T) {
	count := func(http.Header) (int, int, bool) { return 0, 0, false }
	rolling := New(Config{HourCount: count})
	clock := New(Config{HourCount: count, HourWindow: ClockHour})
	if rolling.hour.aligned || !clock.hour.aligned {
		t.Errorf("aligned: default %v, ClockHour %v; want false, true", rolling.hour.aligned, clock.hour.aligned)
	}
}

// A scheduled run must never sit out an hour: it would still be running when
// the next one starts. With NoWait, a spent hour ends the request at once.
func TestNoWaitStopsInsteadOfWaiting(t *testing.T) {
	tests := []struct {
		name     string
		status   int // the server's answer, always with 0 left this hour
		wantHits int32
	}{
		{"the hour spent by a success", http.StatusOK, 1},
		{"an hourly 429 is not retried", http.StatusTooManyRequests, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			base := serve(t, func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				hourHeaders(w, 0)
				w.WriteHeader(tt.status)
				w.Write([]byte(`{}`))
			})
			c := New(Config{Name: "pw", BaseURL: base, Limits: Limits{PerHour: 100}, Timeout: time.Second,
				HourCount: hourCount, NoWait: true})
			// Without NoWait these calls would wait an hour; the deadline turns that into a quick failure.
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			var err error
			for range 3 {
				if err = c.GetJSON(ctx, "/x", &struct{}{}); err != nil {
					break
				}
			}
			if !errors.Is(err, card.ErrRateLimited) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err = %v, want ErrRateLimited without waiting", err)
			}
			if hits.Load() != tt.wantHits {
				t.Errorf("hits = %d, want %d: a spent hour must not send more requests", hits.Load(), tt.wantHits)
			}
		})
	}
}
