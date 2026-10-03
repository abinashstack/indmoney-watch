package notify

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// toastScript shows a Windows toast notification via WinRT from Windows
// PowerShell 5.1 (present on every Windows 10/11 install).
//
// As with the macOS osascript path, strings are passed through environment
// variables and never spliced into the script source, so text from INDmoney
// can't inject PowerShell. They are XML-escaped before going into the toast
// template so they can't inject toast markup either. The notifier uses
// PowerShell's own AppUserModelID, which is registered on every system, so
// no Start-menu shortcut has to be installed first.
const toastScript = `$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
function Esc([string]$s) { [System.Security.SecurityElement]::Escape($s) }
$t = Esc $env:INDW_TITLE
$s = Esc $env:INDW_SUBTITLE
$m = Esc $env:INDW_MSG
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml("<toast><visual><binding template=""ToastGeneric""><text>$t</text><text>$s</text><text>$m</text></binding></visual><audio src=""ms-winsoundevent:Notification.Default""/></toast>")
$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show($toast)
`

// powershellExe returns the absolute path to Windows PowerShell so a
// powershell.exe planted earlier in %PATH% is never picked up.
func powershellExe() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

// encodeCommand encodes a script for powershell -EncodedCommand (base64 of
// UTF-16LE), which sidesteps every command-line quoting rule.
func encodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i] = byte(c)
		b[2*i+1] = byte(c >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// Banner fires a native Windows toast notification.
func Banner(title, subtitle, message string) error {
	cmd := exec.Command(powershellExe(),
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-WindowStyle", "Hidden", "-EncodedCommand", encodeCommand(toastScript))
	cmd.Env = append(cmd.Environ(),
		"INDW_TITLE="+title,
		"INDW_SUBTITLE="+subtitle,
		"INDW_MSG="+message,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("toast: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
