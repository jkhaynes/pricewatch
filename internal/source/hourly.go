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
	known       bool      // false until a response has reported the count
	remaining   int       // the server's count, minus requests still in flight
	inFlight    int       // reserved, no count reported back yet
	windowStart time.Time // first request seen in the current window; zero if none yet
}

func newHourly(window time.Duration) *hourly { return &hourly{window: window} }

// reserve claims one request at now. It returns 0 when the request may be
// sent, or how long to wait before calling reserve again.
func (h *hourly) reserve(now time.Time) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.known && h.remaining <= 0 {
		if resetAt := h.windowStart.Add(h.window); now.Before(resetAt) {
			return resetAt.Sub(now)
		}
		// A full window has passed since its first request, so the count has
		// reset whether the server uses fixed or rolling windows. Relearn it.
		h.known, h.windowStart = false, time.Time{}
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
}

// release ends a reservation whose response reported no count, such as a
// network error. The server may still have counted the request, so the local
// count is not given back.
func (h *hourly) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inFlight = max(h.inFlight-1, 0)
}
