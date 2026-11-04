package scheduler

import (
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

// NextPing estimates when t's loop will next trigger a ping, given the usage
// snapshot u read at now. It mirrors the order runTarget gates on: active hours,
// monthly/weekly exhaustion, a missing 5h window, the running 5h window, then
// the one-time align_start anchor.
//
// The estimate cannot see transient states the snapshot does not carry — a
// failed ping's backoff, an active CLI turn deferring the ping, a rate-limited
// usage endpoint — so it names the next scheduled opportunity, not a promise.
func NextPing(t Target, u *usage.Usage, cfg config.Config, now time.Time) time.Time {
	if u == nil {
		return now
	}

	// The loop checks active hours before reading usage, so outside them it
	// sleeps straight to the next opening. Everything below is evaluated from
	// that moment: a window that resets before it is already free by then.
	ready := now
	if !t.ActiveHours.Contains(now) {
		ready = t.ActiveHours.NextStart(now)
	}
	buffer := cfg.ResetBuffer.Duration
	switch {
	case u.MonthlyExhausted(cfg.WeeklyThreshold):
		ready = later(ready, u.Monthly.ResetsAt.Add(buffer))
	case u.WeeklyExhausted(cfg.WeeklyThreshold):
		ready = later(ready, u.Weekly.ResetsAt.Add(buffer))
	case u.FiveHour.Missing() && windowActive(u.Weekly, now):
		ready = later(ready, u.Weekly.ResetsAt.Add(buffer))
	case windowActive(u.FiveHour, now):
		ready = later(ready, u.FiveHour.ResetsAt.Add(buffer))
	}

	// A usage wait that ends outside active hours sends the loop back to the
	// top, which sleeps until the range opens before reading again.
	if !t.ActiveHours.Contains(ready) {
		ready = t.ActiveHours.NextStart(ready)
	}
	// The anchor is honored once, when the window is free. A future anchor has
	// by definition not been passed yet, so it still gates the next ping.
	if ready.Before(t.AlignStart) {
		ready = t.AlignStart
	}
	// The anchor may itself land outside active hours; the loop then waits for
	// the range to open before pinging.
	if !t.ActiveHours.Contains(ready) {
		ready = t.ActiveHours.NextStart(ready)
	}
	return ready
}

// windowActive is usage.Window.Active against the snapshot time rather than the
// wall clock, so a plan computed from one read stays consistent.
func windowActive(w usage.Window, now time.Time) bool {
	return !w.ResetsAt.IsZero() && now.Before(w.ResetsAt)
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
