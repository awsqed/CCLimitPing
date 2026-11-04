package scheduler

import (
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

func TestNextPing(t *testing.T) {
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local)
	day := func(h, m int) time.Time {
		return time.Date(2026, 11, 4, h, m, 0, 0, time.Local)
	}
	tomorrow := func(h, m int) time.Time {
		return time.Date(2026, 11, 5, h, m, 0, 0, time.Local)
	}

	base := func() config.Config {
		cfg := config.Default()
		cfg.ResetBuffer = config.Duration{Duration: 10 * time.Second}
		return cfg
	}
	hours := func(spec string) config.Hours {
		h, err := config.ParseHours(spec)
		if err != nil {
			t.Fatalf("ParseHours(%q): %v", spec, err)
		}
		return h
	}

	tests := []struct {
		name string
		t    Target
		u    *usage.Usage
		cfg  config.Config
		want time.Time
	}{
		{
			name: "free window pings now",
			u:    &usage.Usage{},
			cfg:  base(),
			want: now,
		},
		{
			name: "future align_start gates a free window",
			t:    Target{AlignStart: day(11, 30)},
			u:    &usage.Usage{},
			cfg:  base(),
			want: day(11, 30),
		},
		{
			name: "align_start outside active hours waits for the opening",
			t:    Target{AlignStart: day(23, 0), ActiveHours: hours("07:00-22:00")},
			u:    &usage.Usage{},
			cfg:  base(),
			want: tomorrow(7, 0),
		},
		{
			name: "running 5h window waits for its reset plus buffer",
			u:    &usage.Usage{FiveHour: usage.Window{ResetsAt: day(12, 0)}},
			cfg:  base(),
			want: day(12, 0).Add(10 * time.Second),
		},
		{
			name: "5h reset outside active hours waits for the opening",
			t:    Target{ActiveHours: hours("07:00-22:00")},
			u:    &usage.Usage{FiveHour: usage.Window{ResetsAt: day(23, 0)}},
			cfg:  base(),
			want: tomorrow(7, 0),
		},
		{
			name: "align_start later than the 5h reset wins",
			t:    Target{AlignStart: day(14, 0)},
			u:    &usage.Usage{FiveHour: usage.Window{ResetsAt: day(12, 0)}},
			cfg:  base(),
			want: day(14, 0),
		},
		{
			name: "outside active hours waits for the opening",
			t:    Target{ActiveHours: hours("10:00-22:00")},
			u:    &usage.Usage{},
			cfg:  base(),
			want: day(10, 0),
		},
		{
			name: "outside active hours still honors a later align_start",
			t:    Target{AlignStart: day(11, 0), ActiveHours: hours("10:00-22:00")},
			u:    &usage.Usage{},
			cfg:  base(),
			want: day(11, 0),
		},
		{
			name: "exhausted weekly window waits for its reset",
			u:    &usage.Usage{Weekly: usage.Window{UsedPercent: 100, ResetsAt: day(11, 0)}},
			cfg:  base(),
			want: day(11, 0).Add(10 * time.Second),
		},
		{
			name: "exhausted monthly window waits for its reset",
			u:    &usage.Usage{Monthly: usage.Window{UsedPercent: 100, ResetsAt: day(13, 0)}},
			cfg:  base(),
			want: day(13, 0).Add(10 * time.Second),
		},
		{
			name: "weekly-only limits wait for the weekly reset",
			u:    &usage.Usage{Weekly: usage.Window{UsedPercent: 10, ResetsAt: day(15, 0)}},
			cfg:  base(),
			want: day(15, 0).Add(10 * time.Second),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextPing(tt.t, tt.u, tt.cfg, now)
			if !got.Equal(tt.want) {
				t.Fatalf("NextPing = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNextPingNilUsage(t *testing.T) {
	now := time.Now()
	if got := NextPing(Target{}, nil, config.Default(), now); !got.Equal(now) {
		t.Fatalf("NextPing(nil) = %s, want %s", got, now)
	}
}
