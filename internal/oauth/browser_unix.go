//go:build !windows

package oauth

import "os/exec"

func openBrowser(u string) error {
	return exec.Command("/usr/bin/open", u).Start()
}
