package oauth

import (
	"os"
	"os/exec"
	"path/filepath"
)

// openBrowser opens u in the default browser. rundll32 receives the URL as a
// plain argument, so unlike `cmd /c start` the `&` separators in the query
// string aren't interpreted by a shell.
func openBrowser(u string) error {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return exec.Command(filepath.Join(root, "System32", "rundll32.exe"), "url.dll,FileProtocolHandler", u).Start()
}
