package engine

import (
	"slices"
	"testing"
	"time"
)

func TestStatsDays(t *testing.T) {
	moscow := time.FixedZone("MSK", 3*60*60)
	at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, moscow) }
	for _, tc := range []struct {
		from, to time.Time
		want     []string
	}{
		{at(25, 15), at(27, 9), []string{"2026-10-25", "2026-10-26", "2026-10-27"}},
		// to - начало дня: этот день не входит
		{at(25, 0), at(27, 0), []string{"2026-10-25", "2026-10-26"}},
		{at(27, 10), at(27, 11), []string{"2026-10-27"}},
	} {
		if got := statsDays(tc.from, tc.to); !slices.Equal(got, tc.want) {
			t.Errorf("statsDays(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
	if days := statsDays(at(1, 0), at(1, 0).AddDate(2, 0, 0)); len(days) != MaxStatsDays+1 {
		t.Errorf("two years: %d days, want cut at %d", len(days), MaxStatsDays+1)
	}
}
