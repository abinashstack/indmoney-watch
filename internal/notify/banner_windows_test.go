package notify

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// runPS runs script in Windows PowerShell with extra environment variables
// and returns stdout. Stderr is kept separate: PowerShell may write progress
// records there as CLIXML, which isn't a failure.
func runPS(t *testing.T, script string, env ...string) string {
	t.Helper()
	cmd := exec.Command(powershellExe(), "-NoProfile", "-NonInteractive", "-EncodedCommand",
		encodeCommand("$ProgressPreference = 'SilentlyContinue'\n"+script))
	cmd.Env = append(cmd.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("powershell: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// TestToastScriptParses checks the encoded script reaches PowerShell intact
// and is syntactically valid, without actually showing a toast (CI runners
// have no notification centre to show it in).
func TestToastScriptParses(t *testing.T) {
	check := `$src = [Text.Encoding]::Unicode.GetString([Convert]::FromBase64String('` + encodeCommand(toastScript) + `'))
$errs = $null
[System.Management.Automation.PSParser]::Tokenize($src, [ref]$errs) | Out-Null
if ($errs.Count -gt 0) { $errs | ForEach-Object { Write-Output $_.Message }; exit 1 }
if ($src -ne $env:EXPECTED) { Write-Output 'round-trip mismatch'; exit 1 }
Write-Output 'ok'`
	if out := runPS(t, check, "EXPECTED="+toastScript); out != "ok" {
		t.Fatalf("toast script check: %s", out)
	}
}

// TestToastEscapesHostileText runs the escaping step of the script on hostile
// input and checks the resulting toast XML is well-formed and literal.
func TestToastEscapesHostileText(t *testing.T) {
	hostile := `</text><text>pwn</text>"; $(Start-Process calc) & <x>`
	check := `function Esc([string]$s) { [System.Security.SecurityElement]::Escape($s) }
$m = Esc $env:INDW_MSG
$doc = New-Object System.Xml.XmlDocument
$doc.LoadXml("<toast><text>$m</text></toast>")
if ($doc.toast.text -ne $env:INDW_MSG) { Write-Output 'mismatch'; exit 1 }
Write-Output 'ok'`
	if out := runPS(t, check, "INDW_MSG="+hostile); out != "ok" {
		t.Fatalf("escaping check: %s", out)
	}
}
