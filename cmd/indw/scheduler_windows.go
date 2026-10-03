package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/abinashstack/indmoney-watch/internal/config"
)

// taskName is the Task Scheduler entry for the poller (under the root folder).
const taskName = "indmoney-watch"

func system32(exe string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", exe)
}

func xmlEscape(s string) string {
	var buf strings.Builder
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// taskXML builds the Task Scheduler definition: every pollEvery from 09:00 to
// 16:00 IST, Mon–Fri, expressed in local time (like launchd, Task Scheduler
// triggers are local). The weekday is converted along with the time, so hosts
// where the IST window falls on a different local day still poll on the right
// days. The conversion uses the IST week containing now, so the offset
// matches the host's current daylight-saving state.
//
// The poller runs under `conhost --headless` so no console window flashes up
// every few minutes, only when the user is logged on (toasts need their
// session), even on battery, never overlapping, and is killed after 5 min.
func taskXML(exe string, now time.Time, local *time.Location) string {
	istLoc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		istLoc = time.FixedZone("IST", 5*3600+30*60) // Windows has no tzdata
	}
	t := now.In(istLoc)
	daysSinceMonday := (int(t.Weekday()) + 6) % 7
	monday := time.Date(t.Year(), t.Month(), t.Day()-daysSinceMonday, 9, 0, 0, 0, istLoc)

	first := monday.In(local)
	var days strings.Builder
	for d := 0; d < 5; d++ {
		days.WriteString("<" + monday.AddDate(0, 0, d).In(local).Weekday().String() + "/>")
	}
	window := 7*time.Hour + time.Minute // 09:00 → 16:00 inclusive

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>indmoney-watch: poll INDmoney and raise alerts during Indian market hours</Description>
  </RegistrationInfo>
  <Triggers>
    <CalendarTrigger>
      <Repetition>
        <Interval>PT%dM</Interval>
        <Duration>PT%dM</Duration>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
      <StartBoundary>%s</StartBoundary>
      <Enabled>true</Enabled>
      <ScheduleByWeek>
        <DaysOfWeek>%s</DaysOfWeek>
        <WeeksInterval>1</WeeksInterval>
      </ScheduleByWeek>
    </CalendarTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>false</StartWhenAvailable>
    <ExecutionTimeLimit>PT5M</ExecutionTimeLimit>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`,
		int(pollEvery.Minutes()), int(window.Minutes()),
		first.Format("2006-01-02T15:04:05"),
		days.String(),
		xmlEscape(system32("conhost.exe")),
		// Windows paths can't contain '"', so quoting exe is sufficient.
		xmlEscape(`--headless "`+exe+`" run-once --log`),
	)
}

// utf16File encodes s as UTF-16LE with a BOM — the encoding schtasks /XML
// reliably accepts.
func utf16File(s string) []byte {
	u := utf16.Encode([]rune(s))
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xFE})
	for _, c := range u {
		b.WriteByte(byte(c))
		b.WriteByte(byte(c >> 8))
	}
	return b.Bytes()
}

// registerTask creates (or replaces) the named task from its XML definition.
func registerTask(name, def string) error {
	d, err := config.Dir()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(d, "task-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(utf16File(def)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	out, err := exec.Command(system32("schtasks.exe"), "/Create", "/TN", name, "/XML", f.Name(), "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks /Create: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func deleteTask(name string) error {
	out, err := exec.Command(system32("schtasks.exe"), "/Delete", "/TN", name, "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks /Delete: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func taskExists(name string) bool {
	return exec.Command(system32("schtasks.exe"), "/Query", "/TN", name).Run() == nil
}

func cmdStart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	if err := registerTask(taskName, taskXML(exe, time.Now(), time.Local)); err != nil {
		return err
	}
	logFile, _ := agentLogPath()
	fmt.Println("Installed scheduled task:", taskName, "(Task Scheduler → Task Scheduler Library)")
	fmt.Println("Logs:", logFile)
	fmt.Printf("It will run every %d minutes between 09:00–16:00 IST, Mon–Fri, while you're logged on.\n", int(pollEvery.Minutes()))
	if _, off := time.Now().Zone(); off != 5*3600+30*60 {
		fmt.Println("Note: the schedule is converted to your local time zone at install time.")
		fmt.Println("If your clock changes for daylight saving, re-run `indw start`.")
	}
	return nil
}

func cmdStop() error {
	if !taskExists(taskName) {
		fmt.Println("No scheduled task installed.")
		return nil
	}
	if err := deleteTask(taskName); err != nil {
		return err
	}
	fmt.Println("Removed scheduled task.")
	return nil
}

// schedulerLocation describes the installed scheduler entry for `indw paths`.
func schedulerLocation() string {
	return "scheduled task\t" + taskName + " (Task Scheduler Library)"
}
