package config

import (
	"testing"
	"time"
)

func TestParseHours(t *testing.T) {
	h, err := ParseHours("07:00-22:00")
	if err != nil {
		t.Fatalf("ParseHours: %v", err)
	}
	if h.IsZero() || h.String() != "07:00-22:00" {
		t.Fatalf("Hours = %q, want 07:00-22:00", h.String())
	}
	if zero, err := ParseHours(""); err != nil || !zero.IsZero() {
		t.Fatalf("empty ParseHours = %+v, %v; want a disabled range", zero, err)
	}
	if _, err := ParseHours("07:00-07:00"); err == nil {
		t.Fatal("ParseHours accepted an empty range")
	}
	if _, err := ParseHours("22:00-07:00"); err == nil {
		t.Fatal("ParseHours accepted an overnight range")
	}
	for _, bad := range []string{"07:00", "7-22", "25:00-26:00", "07:00-x"} {
		if _, err := ParseHours(bad); err == nil {
			t.Fatalf("ParseHours accepted %q", bad)
		}
	}
}

func TestHoursContains(t *testing.T) {
	h, err := ParseHours("07:00-22:00")
	if err != nil {
		t.Fatal(err)
	}
	at := func(hour, minute int) time.Time {
		return time.Date(2026, 11, 3, hour, minute, 0, 0, time.Local)
	}
	cases := []struct {
		at   time.Time
		want bool
	}{
		{at(6, 59), false},
		{at(7, 0), true},
		{at(12, 0), true},
		{at(21, 59), true},
		{at(22, 0), false},
		{at(23, 59), false},
	}
	for _, c := range cases {
		if got := h.Contains(c.at); got != c.want {
			t.Errorf("Contains(%s) = %t, want %t", c.at.Format("15:04"), got, c.want)
		}
	}
	if !(Hours{}).Contains(at(3, 0)) {
		t.Error("zero Hours should allow every moment")
	}
}

func TestHoursNextStart(t *testing.T) {
	h, err := ParseHours("07:00-22:00")
	if err != nil {
		t.Fatal(err)
	}
	at := func(hour, minute int) time.Time {
		return time.Date(2026, 11, 3, hour, minute, 0, 0, time.Local)
	}
	if got := h.NextStart(at(5, 0)); !got.Equal(at(7, 0)) {
		t.Errorf("NextStart before the range = %s, want today 07:00", got)
	}
	if got := h.NextStart(at(7, 0)); !got.Equal(at(7, 0).AddDate(0, 0, 1)) {
		t.Errorf("NextStart at the range's start = %s, want tomorrow 07:00", got)
	}
	if got := h.NextStart(at(23, 0)); !got.Equal(at(7, 0).AddDate(0, 0, 1)) {
		t.Errorf("NextStart after the range = %s, want tomorrow 07:00", got)
	}
}
