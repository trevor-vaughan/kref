package render

import (
	"testing"
	"time"
)

func TestRelTime(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"minutes", now.Add(-5 * time.Minute), "5m ago"},
		{"one hour boundary", now.Add(-60 * time.Minute), "1h ago"},
		{"hours", now.Add(-3 * time.Hour), "3h ago"},
		{"one day boundary", now.Add(-24 * time.Hour), "1d ago"},
		{"days", now.Add(-3 * 24 * time.Hour), "3d ago"},
		{"one week boundary", now.Add(-7 * 24 * time.Hour), "1w ago"},
		{"weeks", now.Add(-3 * 7 * 24 * time.Hour), "3w ago"},
		{"past 8 weeks falls back to empty", now.Add(-9 * 7 * 24 * time.Hour), ""},
		{"future clamps to just now", now.Add(1 * time.Hour), "just now"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RelTime(now, c.t); got != c.want {
				t.Fatalf("relTime(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}
