package notify

import (
	"fmt"
	"os/exec"
	"strings"
)

// MacBanner fires a native macOS notification banner via osascript.
//
// Strings are passed through environment variables and read inside the
// AppleScript via `system attribute`, NOT concatenated into the script body.
// This eliminates AppleScript injection: even if a watchlist name contained
// newlines, quotes, backslashes, or AppleScript metacharacters, they cannot
// escape the string literal because they're never substituted into the
// script source. INDmoney is a trusted-but-still-untrusted upstream; defense
// in depth is cheap here.
func MacBanner(title, subtitle, message string) error {
	const script = `display notification (system attribute "INDW_MSG") ` +
		`with title (system attribute "INDW_TITLE") ` +
		`subtitle (system attribute "INDW_SUBTITLE") ` +
		`sound name "Submarine"`

	cmd := exec.Command("osascript", "-e", script)
	cmd.Env = append(cmd.Environ(),
		"INDW_TITLE="+title,
		"INDW_SUBTITLE="+subtitle,
		"INDW_MSG="+message,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
