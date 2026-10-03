//go:build !windows

package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/config"
)

// ---- launchd ----

// plistEscape XML-escapes a string for safe substitution inside a
// <string>…</string> element of the launchd plist we generate. The two values
// we substitute (`exe` from os.Executable, `logFile` under $HOME/.config) are
// trusted in normal use, but a path containing `<` or `&` would corrupt the
// plist, and a maliciously-crafted path could close the <string> tag and
// inject directives like RunAtLoad=true. Defense in depth: cheaper to escape
// every substitution than to reason about whether each input is safe.
func plistEscape(s string) string {
	var buf strings.Builder
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

func cmdStart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	logDir, err := config.Dir()
	if err != nil {
		return err
	}
	logFile := filepath.Join(logDir, "agent.log")

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>run-once</string>
  </array>
  <key>StartCalendarInterval</key>
  <array>
%s
  </array>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin</string>
  </dict>
</dict>
</plist>
`, launchdLabel, plistEscape(exe), calendarSlots(time.Now(), time.Local), plistEscape(logFile), plistEscape(logFile))

	pp, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pp), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(pp, []byte(plist), 0o644); err != nil {
		return err
	}
	// Bootstrap.
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d", uid)
	_ = exec.Command("/bin/launchctl", "bootout", target, pp).Run()
	out, err := exec.Command("/bin/launchctl", "bootstrap", target, pp).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	fmt.Println("Installed launchd agent:", pp)
	fmt.Println("Logs:", logFile)
	fmt.Printf("It will run every %d minutes between 09:00–16:00 IST, Mon–Fri.\n", int(pollEvery.Minutes()))
	if _, off := time.Now().Zone(); off != 5*3600+30*60 {
		fmt.Println("Note: the schedule is converted to your local time zone at install time.")
		fmt.Println("If your clock changes for daylight saving, re-run `indw start`.")
	}
	return nil
}

func cmdStop() error {
	pp, err := plistPath()
	if err != nil {
		return err
	}
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d", uid)
	_ = exec.Command("/bin/launchctl", "bootout", target, pp).Run()
	if err := os.Remove(pp); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Println("Removed launchd agent.")
	return nil
}

// calendarSlots returns StartCalendarInterval entries for every pollEvery
// between 09:00 and 16:00 IST on Mon–Fri, expressed in host-local time
// (launchd only understands local time).
//
// Each slot is converted individually, weekday included: for hosts far from
// IST the market window falls on a different local day (09:00 IST Monday is
// Sunday evening in the US), so reusing the IST weekday would schedule polls
// on the wrong days. The conversion uses the IST week containing now, so the
// UTC offset matches the host's current daylight-saving state.
func calendarSlots(now time.Time, local *time.Location) string {
	istLoc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		istLoc = time.FixedZone("IST", 5*3600+30*60)
	}
	t := now.In(istLoc)
	daysSinceMonday := (int(t.Weekday()) + 6) % 7
	monday := time.Date(t.Year(), t.Month(), t.Day()-daysSinceMonday, 0, 0, 0, 0, istLoc)

	var sb strings.Builder
	for d := 0; d < 5; d++ { // Mon–Fri IST
		start := monday.AddDate(0, 0, d).Add(9 * time.Hour)
		end := monday.AddDate(0, 0, d).Add(16 * time.Hour)
		for slot := start; !slot.After(end); slot = slot.Add(pollEvery) {
			lt := slot.In(local)
			fmt.Fprintf(&sb,
				"    <dict><key>Weekday</key><integer>%d</integer><key>Hour</key><integer>%d</integer><key>Minute</key><integer>%d</integer></dict>\n",
				int(lt.Weekday()), lt.Hour(), lt.Minute(),
			)
		}
	}
	return sb.String()
}

// schedulerLocation describes the installed scheduler entry for `indw paths`.
func schedulerLocation() string {
	pp, _ := plistPath()
	return "launchd plist\t" + pp
}
