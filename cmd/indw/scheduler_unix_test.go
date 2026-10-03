//go:build !windows

package main

import (
	"regexp"
	"strconv"
	"testing"
	"time"
)

var slotRe = regexp.MustCompile(`<key>Weekday</key><integer>(\d)</integer><key>Hour</key><integer>(\d+)</integer><key>Minute</key><integer>(\d+)</integer>`)

type slot struct{ wd, h, m int }

func parseSlots(t *testing.T, s string) []slot {
	t.Helper()
	var out []slot
	for _, m := range slotRe.FindAllStringSubmatch(s, -1) {
		wd, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		mi, _ := strconv.Atoi(m[3])
		out = append(out, slot{wd, h, mi})
	}
	return out
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tzdata for %s unavailable: %v", name, err)
	}
	return loc
}

func TestCalendarSlotsIST(t *testing.T) {
	ist := mustLoc(t, "Asia/Kolkata")
	slots := parseSlots(t, calendarSlots(time.Date(2026, 10, 7, 12, 0, 0, 0, ist), ist))
	// 09:00–16:00 inclusive every 10 min = 43 per day, Mon–Fri.
	if len(slots) != 43*5 {
		t.Fatalf("got %d slots, want %d", len(slots), 43*5)
	}
	first, last := slots[0], slots[len(slots)-1]
	if first != (slot{1, 9, 0}) || last != (slot{5, 16, 0}) {
		t.Fatalf("first=%v last=%v", first, last)
	}
}

func TestCalendarSlotsUSUsesLocalWeekday(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	// October: PDT (UTC-7). 09:00 IST Monday = 20:30 Sunday PDT;
	// 16:00 IST Friday = 03:30 Friday PDT.
	slots := parseSlots(t, calendarSlots(time.Date(2026, 10, 7, 12, 0, 0, 0, la), la))
	if slots[0] != (slot{0, 20, 30}) {
		t.Errorf("first slot = %v, want Sunday 20:30", slots[0])
	}
	if got := slots[len(slots)-1]; got != (slot{5, 3, 30}) {
		t.Errorf("last slot = %v, want Friday 03:30", got)
	}
	// January: PST (UTC-8) — schedule follows the current offset.
	slots = parseSlots(t, calendarSlots(time.Date(2026, 1, 7, 12, 0, 0, 0, la), la))
	if slots[0] != (slot{0, 19, 30}) {
		t.Errorf("winter first slot = %v, want Sunday 19:30", slots[0])
	}
}
