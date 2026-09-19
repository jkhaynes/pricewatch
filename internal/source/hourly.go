package source

import (
	"sync"
	"time"
)

// hourly follows the source's own hourly request count, read from response
// headers (DD-11). Requests go out while the server says some remain; once it
// says none do, callers wait until the window has certainly reset.
type hourly struct {
	mu          sync.Mutex
	window      time.Duration
	aligned     bool          // the window resets on clock boundaries (ClockHour)
	margin      time.Duration // slack after an aligned boundary, for clock differences
	known       bool          // false until a response has reported the count
	remaining   int           // the server's count, minus requests still in flight
	inFlight    int           // reserved, no count reported back yet
	windowStart time.Time     // first request seen in the current window; zero if none yet
	spentAt     time.Time     // when the window was found spent; zero while some remain
}

// newHourly tracks a window whose reset time is unknown, or rolling: a spent
// window is waited out a full window after its first request.
func newHourly(window time.Duration) *hourly { return &hourly{window: window} }

// newClockHourly tracks a window that resets on clock boundaries, such as the
// top of every UTC hour: a spent window is waited out until the next boundary.
func newClockHourly(window, margin time.Duration) *hourly {
	return &hourly{window: window, aligned: true, margin: margin}
}

// reserve claims one request at now. It returns 0 when the request may be
// sent, or how long to wait before calling reserve again.
func (h *hourly) reserve(now time.Time) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.known && h.remaining <= 0 {
		if h.spentAt.IsZero() {
			h.spentAt = now // spent by local reservations, noticed now
		}
		if resetAt := h.resetAt(); now.Before(resetAt) {
			return resetAt.Sub(now)
		}
		// The window has certainly reset. Relearn the count from the next response.
		h.known, h.windowStart, h.spentAt = false, time.Time{}, time.Time{}
	}
	if h.windowStart.IsZero() {
		// Never earlier than the real window start, so waits measured from it
		// can be too long but never too short.
		h.windowStart = now
	}
	if h.known {
		h.remaining--
	}
	h.inFlight++
	return 0
}

// observe records the count a response reported for a request sent at sentAt.
func (h *hourly) observe(sentAt time.Time, limit, remaining int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inFlight = max(h.inFlight-1, 0)
	if remaining == limit-1 {
		h.windowStart = sentAt // the first request of a fresh window
	}
	h.known = true
	h.remaining = remaining - h.inFlight
	switch {
	case h.remaining > 0:
		h.spentAt = time.Time{}
	case h.spentAt.IsZero():
		h.spentAt = sentAt
	}
}

// resetAt is when a spent window has certainly reset. A clock-aligned window
// resets at the first boundary after it was found spent. Otherwise, a full
// window after its first request is safe for fixed and rolling windows alike.
func (h *hourly) resetAt() time.Time {
	if h.aligned {
		return h.spentAt.Truncate(h.window).Add(h.window + h.margin)
	}
	return h.windowStart.Add(h.window)
}

// release ends a reservation whose response reported no count, such as a
// network error. The server may still have counted the request, so the local
// count is not given back.
func (h *hourly) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inFlight = max(h.inFlight-1, 0)
}
