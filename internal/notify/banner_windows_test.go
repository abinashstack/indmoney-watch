package notify

import (
	"os/exec"
	"strings"
	"testing"
)

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
	cmd := exec.Command(powershellExe(), "-NoProfile", "-NonInteractive", "-EncodedCommand", encodeCommand(check))
	cmd.Env = append(cmd.Environ(), "EXPECTED="+toastScript)
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("powershell: %v\n%s", err, out)
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
	cmd := exec.Command(powershellExe(), "-NoProfile", "-NonInteractive", "-EncodedCommand", encodeCommand(check))
	cmd.Env = append(cmd.Environ(), "INDW_MSG="+hostile)
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("powershell: %v\n%s", err, out)
	}
}
