package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"

	"github.com/jkhaynes/pricewatch/internal/card"
)

// Limits a provider publishes (or we impose). Zero means none of that kind.
type Limits struct {
	PerSecond float64 // with HourCount set, only a politeness cap
	PerHour   int     // spacing fallback for sources that report no hourly count
	PerDay    int     // enforced durably through Quota, not by the in-memory limiter
}

// Quota is durable per-source, per-UTC-day request accounting.
type Quota interface {
	QuotaUsed(ctx context.Context, source, day string) (int, error)
	QuotaAdd(ctx context.Context, source, day string, n int) error
	QuotaSet(ctx context.Context, source, day string, used int) error
}

type Config struct {
	Name      string
	BaseURL   string
	Header    http.Header // e.g. the API key
	Limits    Limits
	Timeout   time.Duration
	Quota     Quota                                             // nil: no daily accounting
	DayUsed   func(http.Header) (int, bool)                     // nil: no server-side daily sync
	HourCount func(http.Header) (limit, remaining int, ok bool) // nil: even spacing by PerHour (DD-11)
	// HourWindow says how the source counts its hour. The zero value, Rolling,
	// is the safe default for a source whose reset time is unknown.
	HourWindow Window
	// NoWait makes a spent hour an error wrapping card.ErrRateLimited instead
	// of a pause, for scheduled runs that must end before the next one (DD-9).
	NoWait bool
	Log    *slog.Logger // nil: pauses are not logged
}

// Window is how a source counts its hourly allowance.
type Window int

const (
	// Rolling covers a rolling window, or one whose reset time is unknown: a
	// spent hour is waited out a full hour after the window's first request.
	Rolling Window = iota
	// ClockHour resets at the top of every UTC hour: a spent hour is waited out
	// until the next boundary, plus clockMargin.
	ClockHour
)

// clockMargin is the slack after an hour boundary, allowing for the difference
// between our clock and the source's.
const clockMargin = 30 * time.Second

type Client struct {
	cfg     Config
	http    *http.Client
	limiter *rate.Limiter
	hour    *hourly // nil unless the source reports an hourly count
	now     func() time.Time
}

func New(cfg Config) *Client {
	c := &Client{cfg: cfg, http: &http.Client{}, limiter: rate.NewLimiter(pacing(cfg), 1), now: time.Now}
	switch {
	case cfg.HourCount == nil:
	case cfg.HourWindow == ClockHour:
		c.hour = newClockHourly(time.Hour, clockMargin)
	default:
		c.hour = newHourly(time.Hour)
	}
	return c
}

func (c *Client) Name() string { return c.cfg.Name }

// pacing is the in-memory limiter's rate. A source that reports its hourly
// count is only held to the politeness cap, because hourly tracks the real
// allowance (DD-11). Otherwise requests are spaced evenly across the hour.
func pacing(cfg Config) rate.Limit {
	if cfg.HourCount == nil {
		return limitFor(cfg.Limits)
	}
	if cfg.Limits.PerSecond > 0 {
		return rate.Limit(cfg.Limits.PerSecond)
	}
	return rate.Inf
}

func limitFor(l Limits) rate.Limit {
	lim := rate.Inf
	if l.PerSecond > 0 {
		lim = rate.Limit(l.PerSecond)
	}
	if l.PerHour > 0 {
		if h := rate.Limit(float64(l.PerHour) / 3600); h < lim {
			lim = h
		}
	}
	return lim
}

type StatusError struct {
	Code int
	Path string
}

func (e *StatusError) Error() string { return fmt.Sprintf("GET %s: HTTP %d", e.Path, e.Code) }

// maxHourlyRetries bounds how many times one request waits out an hourly 429.
// Each retry can mean an hour's wait, so a misbehaving server cannot hold a
// run forever.
const maxHourlyRetries = 3

// hourlyLimited marks a 429 whose headers say the hour is spent while the day
// is not: worth waiting out. It unwraps to the ErrRateLimited error, so callers
// that see it after the retries run out still get card.ErrRateLimited.
type hourlyLimited struct{ err error }

func (e *hourlyLimited) Error() string { return e.err.Error() }
func (e *hourlyLimited) Unwrap() error { return e.err }

// GetJSON fetches path and decodes it into v. An hourly 429 is waited out and
// retried (DD-11); every other failure is returned as is.
func (c *Client) GetJSON(ctx context.Context, path string, v any) error {
	for attempt := 0; ; attempt++ {
		err := c.getOnce(ctx, path, v)
		var hl *hourlyLimited
		if !errors.As(err, &hl) || attempt == maxHourlyRetries {
			return err
		}
		// getOnce recorded "none left this hour", so the next attempt's
		// awaitHour waits for the window before sending again.
	}
}

func (c *Client) getOnce(ctx context.Context, path string, v any) error {
	day := c.now().UTC().Format(time.DateOnly)
	if c.cfg.Limits.PerDay > 0 && c.cfg.Quota != nil {
		used, err := c.cfg.Quota.QuotaUsed(ctx, c.cfg.Name, day)
		if err != nil {
			return fmt.Errorf("read quota: %w", err)
		}
		if used >= c.cfg.Limits.PerDay {
			return fmt.Errorf("%s: %d of %d used on %s: %w", c.cfg.Name, used, c.cfg.Limits.PerDay, day, card.ErrQuotaExhausted)
		}
	}

	var sent time.Time
	var header http.Header // stays nil unless a response arrives
	if c.hour != nil {
		if err := c.awaitHour(ctx); err != nil {
			return err
		}
		defer func() { c.settleHour(sent, header) }()
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", path, err)
	}
	for k, vs := range c.cfg.Header {
		for _, v := range vs {
			req.Header.Add(k, v) // Add canonicalises the name; direct map writes would not
		}
	}
	if c.cfg.Quota != nil {
		if err := c.cfg.Quota.QuotaAdd(ctx, c.cfg.Name, day, 1); err != nil {
			return fmt.Errorf("count request: %w", err)
		}
	}
	sent = c.now()
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	header = resp.Header

	if c.cfg.Quota != nil && c.cfg.DayUsed != nil {
		if used, ok := c.cfg.DayUsed(resp.Header); ok {
			if err := c.cfg.Quota.QuotaSet(ctx, c.cfg.Name, day, used); err != nil {
				return fmt.Errorf("sync quota: %w", err)
			}
		}
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("GET %s: %w", path, card.ErrNotFound)
	case resp.StatusCode == http.StatusTooManyRequests:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512)) // detail only; the status is what matters
		err := fmt.Errorf("GET %s: %s: %w", path, body, card.ErrRateLimited)
		if c.hourSpentDayNot(resp.Header) {
			return &hourlyLimited{err: err}
		}
		return err
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &StatusError{Code: resp.StatusCode, Path: path}
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// hourSpentDayNot reports whether a 429's headers show this hour's allowance
// used up while the day still has room. Only then is waiting worthwhile.
func (c *Client) hourSpentDayNot(h http.Header) bool {
	if c.hour == nil {
		return false
	}
	if _, remaining, ok := c.cfg.HourCount(h); !ok || remaining > 0 {
		return false
	}
	if c.cfg.DayUsed != nil && c.cfg.Limits.PerDay > 0 {
		if used, ok := c.cfg.DayUsed(h); ok && used >= c.cfg.Limits.PerDay {
			return false // the day is spent too: waiting an hour would not help
		}
	}
	return true
}

// awaitHour reserves a request against the hourly count, pausing for the
// window to reset when the server says none remain. Cancelling ctx ends it.
func (c *Client) awaitHour(ctx context.Context) error {
	for {
		wait := c.hour.reserve(c.now())
		if wait == 0 {
			return nil
		}
		if c.cfg.NoWait {
			return fmt.Errorf("%s: hourly allowance used, resets in %v: %w",
				c.cfg.Name, wait.Round(time.Second), card.ErrRateLimited)
		}
		if c.cfg.Log != nil {
			c.cfg.Log.Info("hourly allowance used; waiting for the hourly window to reset",
				"source", c.cfg.Name, "wait", wait.Round(time.Second))
		}
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("waiting for the hourly window: %w", ctx.Err())
		}
	}
}

// settleHour ends a reservation: it records the count the response reported,
// or just frees the slot when there was no response or no count in it.
func (c *Client) settleHour(sent time.Time, header http.Header) {
	if header != nil {
		if limit, remaining, ok := c.cfg.HourCount(header); ok {
			c.hour.observe(sent, limit, remaining)
			return
		}
	}
	c.hour.release()
}

// HeaderDayUsed reads "used today" as limit minus remaining from two response headers.
func HeaderDayUsed(limitHeader, remainingHeader string) func(http.Header) (int, bool) {
	return func(h http.Header) (int, bool) {
		limit, remaining, ok := HeaderCount(limitHeader, remainingHeader)(h)
		return limit - remaining, ok
	}
}

// HeaderCount reads a limit and a remaining count from two response headers.
func HeaderCount(limitHeader, remainingHeader string) func(http.Header) (limit, remaining int, ok bool) {
	return func(h http.Header) (int, int, bool) {
		limit, err1 := strconv.Atoi(h.Get(limitHeader))
		remaining, err2 := strconv.Atoi(h.Get(remainingHeader))
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return limit, remaining, true
	}
}
