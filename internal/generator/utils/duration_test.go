package utils

import (
	"testing"
	"time"
)

func TestParseISODuration(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"PT3S":       3 * time.Second,
		"PT0.5S":     500 * time.Millisecond,
		"PT1M30S":    90 * time.Second,
		"PT2H":       2 * time.Hour,
		"P1DT2H":     26 * time.Hour,
		"P1W":        7 * 24 * time.Hour,
		" PT10M ":    10 * time.Minute,
		"P2D":        48 * time.Hour,
		"PT1H1M1,5S": time.Hour + time.Minute + 1500*time.Millisecond,
	} {
		got, err := ParseISODuration(value)
		if err != nil || got != want {
			t.Errorf("%q: got %v, %v; want %v", value, got, err, want)
		}
	}
	for _, value := range []string{"", "2s", "P", "PT", "P1M", "P1Y", "PT0S", "3", "PT-1S", "P1DT"} {
		if got, err := ParseISODuration(value); err == nil {
			t.Errorf("%q: got %v, want error", value, got)
		}
	}
}

func TestDurationLiteral(t *testing.T) {
	for d, want := range map[time.Duration]string{
		3 * time.Second:         "3 * time.Second",
		90 * time.Second:        "90 * time.Second",
		2 * time.Hour:           "2 * time.Hour",
		1500 * time.Millisecond: "1500 * time.Millisecond",
		time.Microsecond:        "time.Duration(1000)",
	} {
		if got := DurationLiteral(d); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
}
