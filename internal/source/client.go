package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"

	"github.com/jkhaynes/pricewatch/internal/card"
)

// Limits a provider publishes (or we impose). Zero means none of that kind.
type Limits struct {
	PerSecond float64
	PerHour   int
	PerDay    int // enforced durably through Quota, not by the in-memory limiter
}

// Quota is durable per-source, per-UTC-day request accounting.
type Quota interface {
	QuotaUsed(ctx context.Context, source, day string) (int, error)
	QuotaAdd(ctx context.Context, source, day string, n int) error
	QuotaSet(ctx context.Context, source, day string, used int) error
}

type Config struct {
	Name    string
	BaseURL string
	Header  http.Header // e.g. the API key
	Limits  Limits
	Timeout time.Duration
	Quota   Quota                         // nil: no daily accounting
	DayUsed func(http.Header) (int, bool) // nil: no server-side sync
}

type Client struct {
	cfg     Config
	http    *http.Client
	limiter *rate.Limiter
	now     func() time.Time
}

func New(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{}, limiter: rate.NewLimiter(limitFor(cfg.Limits), 1), now: time.Now}
}

func (c *Client) Name() string { return c.cfg.Name }

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

func (c *Client) GetJSON(ctx context.Context, path string, v any) error {
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
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

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
		return fmt.Errorf("GET %s: %s: %w", path, body, card.ErrRateLimited)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &StatusError{Code: resp.StatusCode, Path: path}
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// HeaderDayUsed reads "used today" as limit minus remaining from two response headers.
func HeaderDayUsed(limitHeader, remainingHeader string) func(http.Header) (int, bool) {
	return func(h http.Header) (int, bool) {
		limit, err1 := strconv.Atoi(h.Get(limitHeader))
		remaining, err2 := strconv.Atoi(h.Get(remainingHeader))
		if err1 != nil || err2 != nil {
			return 0, false
		}
		return limit - remaining, true
	}
}
