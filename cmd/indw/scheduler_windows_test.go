package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

type taskDef struct {
	StartBoundary string   `xml:"Triggers>CalendarTrigger>StartBoundary"`
	Interval      string   `xml:"Triggers>CalendarTrigger>Repetition>Interval"`
	Duration      string   `xml:"Triggers>CalendarTrigger>Repetition>Duration"`
	DaysOfWeek    struct {
		Days []xmlTag `xml:",any"`
	} `xml:"Triggers>CalendarTrigger>ScheduleByWeek>DaysOfWeek"`
	Command       string   `xml:"Actions>Exec>Command"`
	Arguments     string   `xml:"Actions>Exec>Arguments"`
	OnBatteries   bool     `xml:"Settings>DisallowStartIfOnBatteries"`
}

type xmlTag struct{ XMLName xml.Name }

func parseTask(t *testing.T, s string) taskDef {
	t.Helper()
	// encoding/xml refuses non-UTF-8 declarations; s is already a Go string.
	s = strings.Replace(s, ` encoding="UTF-16"`, "", 1)
	var d taskDef
	if err := xml.Unmarshal([]byte(s), &d); err != nil {
		t.Fatalf("task XML: %v\n%s", err, s)
	}
	return d
}

func dayNames(d taskDef) string {
	var n []string
	for _, x := range d.DaysOfWeek.Days {
		n = append(n, x.XMLName.Local[:3])
	}
	return strings.Join(n, ",")
}

func TestTaskXMLSchedule(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	d := parseTask(t, taskXML(`C:\Users\A & B\<bin>\indw.exe`, time.Date(2026, 10, 7, 12, 0, 0, 0, la), la))
	if d.StartBoundary != "2026-10-04T20:30:00" || dayNames(d) != "Sun,Mon,Tue,Wed,Thu" {
		t.Errorf("LA schedule: start=%s days=%s", d.StartBoundary, dayNames(d))
	}
	if d.Interval != "PT10M" || d.Duration != "PT421M" || d.OnBatteries {
		t.Errorf("repetition/settings: %+v", d)
	}
	if d.Arguments != `--headless "C:\Users\A & B\<bin>\indw.exe" run-once --log` {
		t.Errorf("arguments = %q", d.Arguments)
	}
}

// TestRegisterTaskRoundTrip registers a real task (pointing at a nonexistent
// exe, so it can't do anything if it fires) under a throwaway name, reads it back from Task
// Scheduler, and deletes it. This is what catches XML Task Scheduler rejects.
// decodeConsole returns schtasks output as a string; it may be UTF-16LE
// (with or without BOM) or 8-bit depending on Windows version.
func decodeConsole(b []byte) string {
	b = bytes.TrimPrefix(b, []byte{0xFF, 0xFE})
	if bytes.IndexByte(b, 0) < 0 {
		return string(b)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func TestRegisterTaskRoundTrip(t *testing.T) {
	name := fmt.Sprintf("indmoney-watch-test-%d", time.Now().UnixNano())
	exe := `C:\nonexistent\indw test\indw.exe`
	if err := registerTask(name, taskXML(exe, time.Now(), time.Local)); err != nil {
		t.Fatalf("registerTask: %v", err)
	}
	t.Cleanup(func() { _ = deleteTask(name) })

	if !taskExists(name) {
		t.Fatal("task not found after registration")
	}
	out, err := exec.Command(system32("schtasks.exe"), "/Query", "/TN", name, "/XML").Output()
	if err != nil {
		t.Fatalf("schtasks /Query: %v", err)
	}
	got := decodeConsole(out)
	for _, want := range []string{`run-once --log`, `indw test\indw.exe`, `PT10M`, `IgnoreNew`, `conhost.exe`} {
		if !strings.Contains(got, want) {
			t.Errorf("registered task XML missing %q:\n%s", want, got)
		}
	}

	if err := deleteTask(name); err != nil {
		t.Fatalf("deleteTask: %v", err)
	}
	if taskExists(name) {
		t.Fatal("task still exists after delete")
	}
}
