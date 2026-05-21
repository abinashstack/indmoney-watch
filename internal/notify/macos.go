package notify

import (
	"fmt"
	"os/exec"
	"strings"
)

// MacBanner fires a native macOS notification banner via osascript.
func MacBanner(title, subtitle, message string) error {
	esc := func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return s
	}
	script := fmt.Sprintf(
		`display notification "%s" with title "%s" subtitle "%s" sound name "Submarine"`,
		esc(message), esc(title), esc(subtitle),
	)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
