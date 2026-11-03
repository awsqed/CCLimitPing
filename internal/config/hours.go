package config

import (
	"fmt"
	"strings"
	"time"
)

// Hours is a local-time range during which pings may start, such as
// "07:00-22:00". The zero value means no restriction: pings may start around
// the clock. Start is inclusive and end exclusive.
type Hours struct {
	start int // seconds since local midnight
	end   int
}

// ParseHours parses "HH:MM-HH:MM" (seconds are accepted too). An empty string
// is the unset range.
func ParseHours(text string) (Hours, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Hours{}, nil
	}
	parts := strings.Split(text, "-")
	if len(parts) != 2 {
		return Hours{}, fmt.Errorf("want HH:MM-HH:MM, got %q", text)
	}
	start, err := parseClock(strings.TrimSpace(parts[0]))
	if err != nil {
		return Hours{}, fmt.Errorf("start: %w", err)
	}
	end, err := parseClock(strings.TrimSpace(parts[1]))
	if err != nil {
		return Hours{}, fmt.Errorf("end: %w", err)
	}
	if start >= end {
		return Hours{}, fmt.Errorf("start must be before end, got %q (an overnight range is not supported)", text)
	}
	return Hours{start: start, end: end}, nil
}

func parseClock(text string) (int, error) {
	for _, layout := range []string{"15:04", "15:04:05"} {
		t, err := time.Parse(layout, text)
		if err == nil {
			return t.Hour()*3600 + t.Minute()*60 + t.Second(), nil
		}
	}
	return 0, fmt.Errorf("want HH:MM or HH:MM:SS, got %q", text)
}

// IsZero reports whether no range is configured.
func (h Hours) IsZero() bool { return h.start == 0 && h.end == 0 }

// Contains reports whether t is inside the range (or whether the range is
// unset, in which case every moment is allowed).
func (h Hours) Contains(t time.Time) bool {
	if h.IsZero() {
		return true
	}
	sec := t.Hour()*3600 + t.Minute()*60 + t.Second()
	return sec >= h.start && sec < h.end
}

// NextStart returns the next moment the range opens: today's start if it is
// still ahead, otherwise tomorrow's.
func (h Hours) NextStart(t time.Time) time.Time {
	start := time.Date(t.Year(), t.Month(), t.Day(),
		h.start/3600, h.start%3600/60, h.start%60, 0, t.Location())
	if !start.After(t) {
		start = start.AddDate(0, 0, 1)
	}
	return start
}

func (h Hours) String() string {
	if h.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s-%s", formatClock(h.start), formatClock(h.end))
}

func formatClock(sec int) string {
	return fmt.Sprintf("%02d:%02d", sec/3600, sec%3600/60)
}

// UnmarshalText lets TOML decode the field from "07:00-22:00".
func (h *Hours) UnmarshalText(text []byte) error {
	parsed, err := ParseHours(string(text))
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

func (h Hours) MarshalText() ([]byte, error) {
	return []byte(h.String()), nil
}
