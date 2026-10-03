package alert

import (
	"testing"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/config"
	"github.com/abinashstack/indmoney-watch/internal/state"
)

func TestDaysUntilUsesLocalCalendarDates(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+30*60)
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, ist) // market hours
	cases := map[string]int{
		"2026-10-04": 0, // due today — previously -1 (never alerted)
		"2026-10-05": 1, // due tomorrow — previously reported as 0
		"2026-10-07": 3,
		"2026-10-03": -1,
	}
	for date, want := range cases {
		got, err := daysUntil(date, now)
		if err != nil {
			t.Fatalf("daysUntil(%s): %v", date, err)
		}
		if got != want {
			t.Errorf("daysUntil(%s) = %d, want %d", date, got, want)
		}
	}
	if _, err := daysUntil("not-a-date", now); err == nil {
		t.Error("expected parse error")
	}
}

func TestPruneLastFired(t *testing.T) {
	now := time.Now()
	st := state.New()
	st.LastFired["old"] = now.Add(-31 * 24 * time.Hour)
	st.LastFired["recent"] = now.Add(-time.Hour)
	e := &Engine{cfg: config.Defaults(), st: st}
	e.pruneLastFired(now)
	if _, ok := st.LastFired["old"]; ok {
		t.Error("old entry not pruned")
	}
	if _, ok := st.LastFired["recent"]; !ok {
		t.Error("recent entry pruned")
	}
}
