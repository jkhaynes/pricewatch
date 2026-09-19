package source

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

func at(minute int) time.Time { return t0.Add(time.Duration(minute) * time.Minute) }

func TestHourlyUnknownCountNeverWaits(t *testing.T) {
	h := newHourly(time.Hour)
	for i := range 5 {
		if w := h.reserve(at(i)); w != 0 {
			t.Fatalf("reserve %d waited %v before the server reported any count", i, w)
		}
		h.release()
	}
}

func TestHourly(t *testing.T) {
	tests := []struct {
		name string
		// run drives h and returns the wait from its last reserve call.
		run  func(h *hourly) time.Duration
		want time.Duration
	}{
		{"burst while the server says requests remain", func(h *hourly) time.Duration {
			h.reserve(at(0))
			h.observe(at(0), 100, 99)
			return h.reserve(at(1))
		}, 0},
		{"spent: wait until an hour after the window's first request", func(h *hourly) time.Duration {
			h.reserve(at(0))
			h.observe(at(0), 100, 99) // remaining = limit-1: 10:00 opened the window
			h.reserve(at(5))
			h.observe(at(5), 100, 0)
			return h.reserve(at(20))
		}, 40 * time.Minute},
		{"window over: send again and relearn the count", func(h *hourly) time.Duration {
			h.reserve(at(0))
			h.observe(at(0), 100, 99)
			h.reserve(at(5))
			h.observe(at(5), 100, 0)
			return h.reserve(at(60))
		}, 0},
		{"in-flight requests count against what remains", func(h *hourly) time.Duration {
			h.reserve(at(0))
			h.observe(at(0), 100, 2)
			h.reserve(at(1)) // 1 left
			h.reserve(at(1)) // 0 left, both still in flight
			return h.reserve(at(2))
		}, 58 * time.Minute},
		{"a response does not undo other in-flight reservations", func(h *hourly) time.Duration {
			h.reserve(at(0))
			h.observe(at(0), 100, 2)
			h.reserve(at(1))
			h.reserve(at(1))
			h.observe(at(1), 100, 1) // server: 1 left, but one request is still in flight
			return h.reserve(at(2))
		}, 58 * time.Minute},
		{"restart mid-window: measure from this process's first request", func(h *hourly) time.Duration {
			h.reserve(at(30)) // the real window began earlier; we can't know when
			h.observe(at(30), 100, 0)
			return h.reserve(at(31))
		}, 59 * time.Minute},
		{"a failed request keeps its count", func(h *hourly) time.Duration {
			h.reserve(at(0))
			h.observe(at(0), 100, 1)
			h.reserve(at(1))
			h.release() // network error: the server may still have counted it
			return h.reserve(at(2))
		}, 58 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.run(newHourly(time.Hour)); got != tt.want {
				t.Errorf("wait = %v, want %v", got, tt.want)
			}
		})
	}
}

// A clock-hour source resets on the UTC hour, so a spent hour waits for the
// next boundary plus a margin, measured from when the hour was found spent.
// Observed for PokeWallet: spent at 23:29 local, full again at 00:06.
func TestClockHourWaitsForTheNextHourBoundary(t *testing.T) {
	const margin = 30 * time.Second
	clock := func(h, m, s int) time.Time { return time.Date(2026, 9, 19, h, m, s, 0, time.UTC) }
	tests := []struct {
		name string
		run  func(h *hourly) time.Duration
		want time.Duration
	}{
		{"spent at :29 waits until :00 plus the margin", func(h *hourly) time.Duration {
			h.reserve(clock(3, 28, 0))
			h.observe(clock(3, 28, 0), 100, 99)
			h.reserve(clock(3, 29, 0))
			h.observe(clock(3, 29, 0), 100, 0)
			return h.reserve(clock(3, 29, 30))
		}, 31 * time.Minute},
		{"spent seconds before the hour waits only seconds", func(h *hourly) time.Duration {
			h.reserve(clock(3, 59, 50))
			h.observe(clock(3, 59, 50), 100, 0)
			return h.reserve(clock(3, 59, 55))
		}, 35 * time.Second},
		{"after a restart the wait is exact, not up to an hour too long", func(h *hourly) time.Duration {
			h.reserve(clock(3, 45, 0)) // this process's first request, mid-hour
			h.observe(clock(3, 45, 0), 100, 0)
			return h.reserve(clock(3, 45, 1))
		}, 15*time.Minute - time.Second + margin},
		{"spent by local reservations, before any response says so", func(h *hourly) time.Duration {
			h.reserve(clock(3, 10, 0))
			h.observe(clock(3, 10, 0), 100, 1)
			h.reserve(clock(3, 20, 0)) // the last one: nothing left locally
			return h.reserve(clock(3, 20, 5))
		}, 40*time.Minute - 5*time.Second + margin},
		{"past the boundary and margin: send again and relearn", func(h *hourly) time.Duration {
			h.reserve(clock(3, 29, 0))
			h.observe(clock(3, 29, 0), 100, 0)
			return h.reserve(clock(4, 0, 30))
		}, 0},
		{"a spent hour from before the boundary does not follow into the next", func(h *hourly) time.Duration {
			h.reserve(clock(3, 29, 0))
			h.observe(clock(3, 29, 0), 100, 0)
			h.reserve(clock(4, 1, 0)) // relearn in the new hour
			h.observe(clock(4, 1, 0), 100, 99)
			return h.reserve(clock(4, 2, 0))
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.run(newClockHourly(time.Hour, margin)); got != tt.want {
				t.Errorf("wait = %v, want %v", got, tt.want)
			}
		})
	}
}
